package matchsystem

import (
	"context"
	"errors"
	"fmt"

	"matchSystem/internal/common"
	"matchSystem/internal/identity"
	"matchSystem/internal/matchsystem/prefilter"
)

var (
	ErrDuplicateRuleKey       = errors.New("duplicate RuleKey on PhysicalNode")
	ErrLogicalNodeNotFound    = errors.New("logical node not found")
	ErrLogicalNodeNotReady    = errors.New("logical node is not ready")
	ErrLogicalNodeNotEmpty    = errors.New("logical node still contains tickets")
	ErrWrongPhysicalNode      = errors.New("OwnerRef targets another PhysicalNode")
	ErrOwnerMismatch          = errors.New("OwnerRef does not match local LogicalNode")
	ErrNoLogicalNodeAvailable = errors.New("no logical node is available for matching")
	ErrMatchRoundNotStarted   = errors.New("matching round has not started")
)

type LogicalNodeState string

const (
	LogicalNodeReady    LogicalNodeState = "Ready"
	LogicalNodeDraining LogicalNodeState = "Draining"
	LogicalNodeStopped  LogicalNodeState = "Stopped"
)

type LogicalNodeSpec struct {
	Key                identity.LogicalNodeKey
	Config             LogicalNodeConfig
	Rules              *RuleSet
	FactProvider       FactProvider
	ObjectFactProvider ObjectFactProvider
	// SeedOrderPolicy overrides Config.SeedScheduler.Order when non-nil. One
	// runtime policy instance is owned by exactly one LogicalNode.
	SeedOrderPolicy SeedOrderPolicy
	// SeedFactProvider is a compatibility alias. Its callback now runs once for
	// any seed or candidate object first used during one ProduceMatch call.
	SeedFactProvider SeedFactProvider
}

// FactProvider runs synchronously on the owning PhysicalNode goroutine. It
// must not re-enter or mutate that PhysicalNode.
type FactProvider func(ctx context.Context, now int64) (Facts, error)

// ObjectFactProvider runs at most once per Ticket during one ProduceMatch call,
// immediately before that Ticket is first used as a seed or candidate. Inputs
// are immutable.
type ObjectFactProvider func(object *Ticket, now int64, tickFacts Facts) (Facts, error)

type SeedFactProvider = ObjectFactProvider

type LogicalNodeDescriptor struct {
	Key         identity.LogicalNodeKey
	State       LogicalNodeState
	TicketCount int
}

func NewLogicalNode(spec LogicalNodeSpec) (*LogicalNode, error) {
	if err := spec.Key.Validate(); err != nil {
		return nil, err
	}
	config := spec.Config
	if spec.ObjectFactProvider != nil && spec.SeedFactProvider != nil {
		return nil, fmt.Errorf("LogicalNode %s configures both ObjectFactProvider and SeedFactProvider", spec.Key)
	}
	objectFactProvider := spec.ObjectFactProvider
	if objectFactProvider == nil {
		objectFactProvider = spec.SeedFactProvider
	}
	if len(config.Facts) == 0 {
		config.Facts = append([]FactSpec(nil), config.Prefilter.Facts...)
	} else if len(config.Prefilter.Facts) != 0 && !sameFactSpecs(config.Facts, config.Prefilter.Facts) {
		return nil, fmt.Errorf("LogicalNode %s has different node and Prefilter Fact contracts", spec.Key)
	}
	config.Prefilter.Facts = append([]prefilter.FactSpec(nil), config.Facts...)
	if config.SeedScheduler.AttemptLimitPerProduceMatch <= 0 {
		config.SeedScheduler.AttemptLimitPerProduceMatch = 500
	}
	if config.MaxPlayers <= 0 {
		config.MaxPlayers = 8
	}
	seedOrderPolicy := spec.SeedOrderPolicy
	if seedOrderPolicy == nil {
		var err error
		seedOrderPolicy, err = NewSeedOrderPolicy(config.SeedScheduler.Order)
		if err != nil {
			return nil, fmt.Errorf("create seed order policy for LogicalNode %s: %w", spec.Key, err)
		}
	}
	rules := spec.Rules
	if rules == nil {
		rules = NewRuleSet()
	} else {
		rules = rules.clone()
	}
	plan, err := prefilter.Compile(config.Prefilter)
	if err != nil {
		return nil, fmt.Errorf("compile prefilter for LogicalNode %s: %w", spec.Key, err)
	}
	prefilterStore, err := prefilter.New(plan)
	if err != nil {
		return nil, fmt.Errorf("create prefilter index store for LogicalNode %s: %w", spec.Key, err)
	}
	return &LogicalNode{
		key:             spec.Key,
		state:           LogicalNodeReady,
		tickFacts:       spec.FactProvider,
		objectFacts:     objectFactProvider,
		config:          config,
		rules:           rules,
		builder:         newGroupBuilder(config.GroupBuilder, config.MaxPlayers),
		prefilterStore:  prefilterStore,
		seedOrderPolicy: seedOrderPolicy,
		nextDocID:       1,
		ticketsByDocID:  make(map[uint32]*Ticket),
		ticketIDToDocID: make(map[string]uint32),
	}, nil
}

func (p *LogicalNode) addCommon(ctx context.Context, ticket *common.Ticket) (uint32, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if ticket == nil {
		return 0, fmt.Errorf("ticket is nil")
	}
	if p.state != LogicalNodeReady {
		return 0, ErrLogicalNodeNotReady
	}
	return p.Add(toLogicalTicket(*ticket))
}

func (p *LogicalNode) removeCommon(ctx context.Context, ticketID string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return p.Remove(ticketID), nil
}

func (p *LogicalNode) getCommon(ctx context.Context, ticketID string) (*common.Ticket, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	ticket, ok := p.Get(ticketID)
	if !ok {
		return nil, false, nil
	}
	result := fromLogicalTicket(ticket)
	return &result, true, nil
}

func (p *LogicalNode) produceMatchCommon(ctx context.Context) (*common.Match, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if p.state != LogicalNodeReady && p.state != LogicalNodeDraining {
		return nil, ErrLogicalNodeNotReady
	}
	if !p.seedRound.initialized {
		return nil, ErrMatchRoundNotStarted
	}
	// Reserve one seed before creating Tick Facts. Provider/configuration
	// failures must not make that seed selectable again in this round.
	seed := p.nextSeed()
	if seed == nil {
		return nil, nil
	}
	facts := Facts{}
	var err error
	if p.tickFacts != nil {
		facts, err = p.tickFacts(ctx, p.seedRound.now)
		if err != nil {
			return nil, fmt.Errorf("create Tick Facts for %s: %w", p.key, err)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	match, err := p.produceMatchFromSeed(p.seedRound.now, facts, seed)
	if match == nil {
		return nil, err
	}
	result := fromLogicalMatch(match)
	return &result, err
}

func (p *LogicalNode) beginDrain() {
	p.state = LogicalNodeDraining
}

func (p *LogicalNode) stop() error {
	if p.Len() != 0 {
		return ErrLogicalNodeNotEmpty
	}
	p.state = LogicalNodeStopped
	return nil
}

func (p *LogicalNode) descriptor() LogicalNodeDescriptor {
	return LogicalNodeDescriptor{Key: p.key, State: p.state, TicketCount: p.Len()}
}

func (p *LogicalNode) runnable() bool {
	return (p.state == LogicalNodeReady || p.state == LogicalNodeDraining) && p.Len() > 0
}

func toLogicalTicket(ticket common.Ticket) *Ticket {
	copy := common.CloneTicket(ticket)
	return &Ticket{
		TicketID:    copy.TicketID,
		CreatedAt:   copy.CreatedAt,
		StringLists: copy.StringLists,
		Uint64Lists: copy.Uint64Lists,
		Int64Values: copy.Int64Values,
	}
}

func fromLogicalTicket(ticket *Ticket) common.Ticket {
	if ticket == nil {
		return common.Ticket{}
	}
	return common.CloneTicket(common.Ticket{
		TicketID:    ticket.TicketID,
		CreatedAt:   ticket.CreatedAt,
		StringLists: ticket.StringLists,
		Uint64Lists: ticket.Uint64Lists,
		Int64Values: ticket.Int64Values,
	})
}

func fromLogicalMatch(match *Match) common.Match {
	result := common.Match{Tickets: make([]common.Ticket, 0, len(match.Tickets))}
	for _, ticket := range match.Tickets {
		result.Tickets = append(result.Tickets, fromLogicalTicket(ticket))
	}
	return result
}
