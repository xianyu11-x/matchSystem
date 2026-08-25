package matchsystem

import (
	"context"
	"errors"
	"fmt"

	"matchSystem/internal/common"
	"matchSystem/internal/identity"
	"matchSystem/internal/matchsystem/fact"
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
	} else if len(config.Prefilter.Facts) != 0 && !fact.SameSpecs(config.Facts, config.Prefilter.Facts) {
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
		ticketsByDocID:  make(map[uint32]*storedTicket),
		ticketIDToDocID: make(map[TicketID]uint32),
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
	return p.Add(ticket)
}

func (p *LogicalNode) removeCommon(ctx context.Context, ticketID common.TicketID) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return p.Remove(ticketID), nil
}

func (p *LogicalNode) getCommon(ctx context.Context, ticketID common.TicketID) (*common.Ticket, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	ticket, ok := p.Get(ticketID)
	return ticket, ok, nil
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
	return match, err
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
