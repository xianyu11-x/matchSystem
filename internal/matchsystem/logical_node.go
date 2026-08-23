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
)

type LogicalNodeState string

const (
	LogicalNodeReady    LogicalNodeState = "Ready"
	LogicalNodeDraining LogicalNodeState = "Draining"
	LogicalNodeStopped  LogicalNodeState = "Stopped"
)

type LogicalNodeSpec struct {
	Key          identity.LogicalNodeKey
	Config       LogicalNodeConfig
	Rules        *RuleSet
	FactProvider FactProvider
}

// FactProvider runs synchronously on the owning PhysicalNode goroutine. It
// must not re-enter or mutate that PhysicalNode.
type FactProvider func(ctx context.Context, now int64) (prefilter.Facts, error)

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
	if config.SeedScheduler.SeedLimitPerTick <= 0 {
		config.SeedScheduler.SeedLimitPerTick = 500
	}
	if config.MaxPlayers <= 0 {
		config.MaxPlayers = 8
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
		facts:           spec.FactProvider,
		config:          config,
		rules:           rules,
		builder:         newGroupBuilder(config.GroupBuilder, config.MaxPlayers),
		prefilterStore:  prefilterStore,
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

func (p *LogicalNode) tickCommon(ctx context.Context, now int64) (*common.Match, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if p.state != LogicalNodeReady && p.state != LogicalNodeDraining {
		return nil, ErrLogicalNodeNotReady
	}
	facts := prefilter.Facts{}
	var err error
	if p.facts != nil {
		facts, err = p.facts(ctx, now)
		if err != nil {
			return nil, fmt.Errorf("create prefilter Facts for %s: %w", p.key, err)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	match, err := p.TickOneWithFacts(now, facts)
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
