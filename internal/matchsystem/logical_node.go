package matchsystem

import (
	"context"
	"errors"
	"fmt"

	"matchSystem/internal/common"
	"matchSystem/internal/identity"
	"matchSystem/internal/matchsystem/contract"
	"matchSystem/internal/matchsystem/evaluation"
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
	Key identity.LogicalNodeKey
	// ContractJSON is the complete logical-node-contract/v3 document. It is
	// the only production configuration path for the shared Contract.
	ContractJSON []byte
	Config       LogicalNodeConfig
	// PrefilterJSON is the complete prefilter/v3 envelope. Prefilter is
	// compiled once when the LogicalNode is created; there is no typed or
	// legacy configuration path.
	PrefilterJSON []byte
	// EvaluationJSON is the complete evaluation/v3 envelope.  The runtime
	// deliberately has no typed-config or compatibility fallback path.
	EvaluationJSON []byte
	// CandidateScorer is the one scorer owned by this LogicalNode.  Scorers are
	// Go orchestration dependencies, not named Evaluation registry entries.
	CandidateScorer CandidateScorer
	// MatchFactProvider is the sole writer of Match-scoped Facts.  It is
	// required when the Contract declares at least one Match Fact and is never
	// called for a Contract without Match-scoped Facts.
	MatchFactProvider  MatchFactProvider
	FactProvider       FactProvider
	ObjectFactProvider ObjectFactProvider
	// SeedOrderPolicy overrides Config.SeedScheduler.Order when non-nil. One
	// runtime policy instance is owned by exactly one LogicalNode.
	SeedOrderPolicy SeedOrderPolicy
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
	// LogicalNode is a JSON-only production boundary. Parse the one shared
	// logical-node-contract/v3 document exactly once before compiling any
	// domain plan. The parser rejects every other schema version; the parsed
	// value is immutable and each downstream compiler takes its own defensive
	// snapshot of the same contract.
	schema, err := contract.Parse(spec.ContractJSON, contract.DefaultLimits())
	if err != nil {
		return nil, fmt.Errorf("parse LogicalNode contract %s: %w", spec.Key, err)
	}
	schema = schema.Clone()
	objectFactProvider := spec.ObjectFactProvider
	if config.SeedScheduler.AttemptLimitPerProduceMatch <= 0 {
		config.SeedScheduler.AttemptLimitPerProduceMatch = defaultAttemptLimitPerProduceMatch
	}
	if config.SeedScheduler.AttemptLimitPerMatchRound <= 0 {
		config.SeedScheduler.AttemptLimitPerMatchRound = defaultAttemptLimitPerMatchRound
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
	prefilterCompiler, err := prefilter.NewJSONCompiler(schema)
	if err != nil {
		return nil, fmt.Errorf("create prefilter compiler for LogicalNode %s: %w", spec.Key, err)
	}
	plan, err := prefilterCompiler.Compile(spec.PrefilterJSON)
	if err != nil {
		return nil, fmt.Errorf("compile prefilter for LogicalNode %s: %w", spec.Key, err)
	}
	for _, required := range plan.Requirements().Facts {
		if required.Scope == fact.ScopeMatch {
			return nil, fmt.Errorf("compile prefilter for LogicalNode %s: Fact %q has match scope and is unavailable before a Match exists", spec.Key, required.Name)
		}
	}
	prefilterStore, err := prefilter.New(plan)
	if err != nil {
		return nil, fmt.Errorf("create prefilter index store for LogicalNode %s: %w", spec.Key, err)
	}
	if spec.CandidateScorer == nil {
		return nil, &evaluation.Error{Phase: "compile", Path: "candidateScorer", Code: "MISSING_SCORER", Err: fmt.Errorf("LogicalNode %s requires a non-nil CandidateScorer", spec.Key)}
	}
	hasMatchFacts := false
	for _, declared := range schema.Facts {
		if declared.Scope == fact.ScopeMatch {
			hasMatchFacts = true
			break
		}
	}
	if hasMatchFacts && spec.MatchFactProvider == nil {
		return nil, &evaluation.Error{Phase: "compile", Path: "matchFactProvider", Code: "MISSING_MATCH_FACT_PROVIDER", Err: fmt.Errorf("LogicalNode %s declares Match-scoped Facts but has no MatchFactProvider", spec.Key)}
	}
	var matchFactProvider MatchFactProvider
	if hasMatchFacts {
		matchFactProvider = spec.MatchFactProvider
	}
	evaluationPlan, err := evaluation.CompileJSON(spec.EvaluationJSON, schema)
	if err != nil {
		return nil, fmt.Errorf("compile evaluation for LogicalNode %s: %w", spec.Key, err)
	}
	matchFacts, err := fact.NewValidator(schema.FactSpecs())
	if err != nil {
		return nil, fmt.Errorf("compile Match Fact validator for LogicalNode %s: %w", spec.Key, err)
	}
	return &LogicalNode{
		key:             spec.Key,
		contract:        schema,
		state:           LogicalNodeReady,
		tickFacts:       spec.FactProvider,
		objectFacts:     objectFactProvider,
		config:          config,
		evaluation:      evaluationPlan,
		scorer:          spec.CandidateScorer,
		matchFacts:      matchFactProvider,
		factValidator:   matchFacts,
		builder:         newGroupBuilder(config.GroupBuilder, config.MaxPlayers),
		prefilterStore:  prefilterStore,
		seedOrderPolicy: seedOrderPolicy,
		nextDocID:       1,
		ticketsByDocID:  make(map[uint32]*storedTicket),
		ticketIDToDocID: make(map[TicketID]uint32),
	}, nil
}

func (p *LogicalNode) addTicket(ctx context.Context, ticket *common.Ticket) (uint32, error) {
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

func (p *LogicalNode) removeTicket(ctx context.Context, ticketID common.TicketID) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return p.Remove(ticketID), nil
}

func (p *LogicalNode) getTicket(ctx context.Context, ticketID common.TicketID) (*common.Ticket, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	ticket, ok := p.Get(ticketID)
	return ticket, ok, nil
}

// ProduceMatch consumes one seed from the current round and returns at most
// one Match. Tick Facts are always obtained from the LogicalNode's configured
// FactProvider; callers cannot inject a Tick Fact layer into this operation.
func (p *LogicalNode) ProduceMatch(ctx context.Context) (*common.Match, error) {
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
		facts, err = invokeProvider(ctx, "tickFacts", func() (Facts, error) {
			return p.tickFacts(ctx, p.seedRound.now)
		})
		if err != nil {
			return nil, fmt.Errorf("create Tick Facts for %s: %w", p.key, err)
		}
		// Own the callback result before the provider can reuse or mutate its
		// maps. The FactFrame takes another defensive copy for its lifetime.
		facts = fact.Clone(facts)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	match, err := p.produceMatchFromSeed(ctx, p.seedRound.now, facts, seed)
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
