package matchsystem

import (
	"context"
	"errors"
	"fmt"

	"matchSystem/internal/common"
	"matchSystem/internal/identity"
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

// MatchFactSnapshotMode controls which Fact data is retained on a Match
// returned by ProduceMatch. None is the zero-value hot-path mode: Match
// carries only Tickets. DeepCopy preserves detached Match/Object Fact
// snapshots for inspection-oriented callers such as the simulator.
type MatchFactSnapshotMode uint8

const (
	MatchFactSnapshotModeNone MatchFactSnapshotMode = iota
	MatchFactSnapshotModeDeepCopy
)

type logicalNodeConfig struct {
	SeedScheduler                seedSchedulerConfig
	CandidateScoringLimitPerSeed int
	CandidateLimitPerSeed        int
	MaxPlayers                   int
}

// LogicalNode owns scheduling state for one isolated matching partition.
// Ticket/index lifetime is owned by ticketStore and all matching decisions
// are owned by seedEvaluator. Methods are serialized by the owning
// PhysicalNode; LogicalNode is intentionally not goroutine-safe.
type LogicalNode struct {
	key    identity.LogicalNodeKey
	state  LogicalNodeState
	config logicalNodeConfig
	// factSpecs is the immutable Fact contract compiled from RuleJSON. Keep a
	// private copy so callers can query metadata without reaching into the
	// compiler or exposing mutable rule state.
	factSpecs []FactSpec

	store     *ticketStore
	evaluator *seedEvaluator

	seedOrderPolicy SeedOrderPolicy
	seedRound       seedRound
	seedCandidates  []*Ticket
	factGeneration  uint64
}

type LogicalNodeSpec struct {
	Key identity.LogicalNodeKey
	// RuleJSON is the complete match-rule/v1 document for Key.Rule. It is the
	// sole production source of Contract, Prefilter, Evaluation, candidate
	// scoring, Seed selection, and runtime matching limits.
	RuleJSON []byte
	// FactProviderDescriptor is the startup contract advertised by
	// FactProvider. It is required when the rule declares Tick-scoped Facts.
	// The descriptor is not used to execute the provider and is never part of
	// the rule JSON.
	FactProviderDescriptor *ProviderDescriptor
	// ObjectFactProviderDescriptor is the startup contract advertised by
	// ObjectFactProvider. It is required when the rule declares Object-scoped
	// Facts.
	ObjectFactProviderDescriptor *ProviderDescriptor
	// MatchFactProvider is the sole writer of Match-scoped Facts.  It is
	// required when the Contract declares at least one Match Fact and is never
	// called for a Contract without Match-scoped Facts.
	MatchFactProvider MatchFactProvider
	// MatchFactProviderDescriptor is the startup contract advertised by
	// MatchFactProvider. It is required when the rule declares Match-scoped
	// Facts.
	MatchFactProviderDescriptor *ProviderDescriptor
	// FactProvider creates the Tick-scoped Facts for one ProduceMatch attempt
	// and receives a value-only TickFactInput containing the current node
	// snapshot.
	FactProvider       FactProvider
	ObjectFactProvider ObjectFactProvider
	// MatchFactSnapshotMode defaults to None. DeepCopy is intended for
	// inspection/adapter paths that need detached Fact snapshots on Match.
	MatchFactSnapshotMode MatchFactSnapshotMode
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
	// Compile the complete rule once. Each LogicalNode receives its own
	// scorer/Seed policy instance, so multiple Placements can share identical
	// RuleJSON without sharing mutable matching state.
	compiled, err := CompileRuleJSON(spec.RuleJSON)
	if err != nil {
		return nil, fmt.Errorf("compile Rule JSON for LogicalNode %s: %w", spec.Key, err)
	}
	if compiled.ruleKey != spec.Key.Rule {
		return nil, ruleCompileError("$.ruleKey", "RULE_KEY_MISMATCH", "Rule JSON declares %s but LogicalNode key requires %s", compiled.ruleKey, spec.Key.Rule)
	}
	if spec.MatchFactSnapshotMode != MatchFactSnapshotModeNone && spec.MatchFactSnapshotMode != MatchFactSnapshotModeDeepCopy {
		return nil, fmt.Errorf("invalid MatchFactSnapshotMode %d", spec.MatchFactSnapshotMode)
	}
	schema := compiled.contract.Clone()
	if err := validateProviderHandshake("tick", fact.ScopeTick, factSpecsForScope(schema.Facts, fact.ScopeTick), providerIsPresent(spec.FactProvider), spec.FactProviderDescriptor); err != nil {
		return nil, fmt.Errorf("provider handshake for LogicalNode %s: %w", spec.Key, err)
	}
	if err := validateProviderHandshake("object", fact.ScopeObject, factSpecsForScope(schema.Facts, fact.ScopeObject), providerIsPresent(spec.ObjectFactProvider), spec.ObjectFactProviderDescriptor); err != nil {
		return nil, fmt.Errorf("provider handshake for LogicalNode %s: %w", spec.Key, err)
	}
	if err := validateProviderHandshake("match", fact.ScopeMatch, factSpecsForScope(schema.Facts, fact.ScopeMatch), providerIsPresent(spec.MatchFactProvider), spec.MatchFactProviderDescriptor); err != nil {
		return nil, fmt.Errorf("provider handshake for LogicalNode %s: %w", spec.Key, err)
	}
	objectLayout, err := fact.NewObjectLayout(schema.Facts)
	if err != nil {
		return nil, fmt.Errorf("compile Object Fact layout for LogicalNode %s: %w", spec.Key, err)
	}
	config := compiled.config
	objectFactProvider := spec.ObjectFactProvider
	seedOrderPolicy := compiled.seedPolicy
	plan := compiled.plan
	for _, required := range plan.Requirements().Facts {
		if required.Scope == fact.ScopeMatch {
			return nil, fmt.Errorf("compile prefilter for LogicalNode %s: Fact %q has match scope and is unavailable before a Match exists", spec.Key, required.Name)
		}
	}
	prefilterStore, err := prefilter.New(plan)
	if err != nil {
		return nil, fmt.Errorf("create prefilter index store for LogicalNode %s: %w", spec.Key, err)
	}
	hasMatchFacts := len(factSpecsForScope(schema.Facts, fact.ScopeMatch)) > 0
	var matchFactProvider MatchFactProvider
	if hasMatchFacts {
		matchFactProvider = spec.MatchFactProvider
	}
	evaluationPlan := compiled.evaluation
	store := newTicketStore(prefilterStore, objectLayout)
	evaluator := newSeedEvaluator(seedEvaluatorConfig{
		key:                   spec.Key,
		tickFacts:             spec.FactProvider,
		objectFacts:           objectFactProvider,
		evaluation:            evaluationPlan,
		scorer:                compiled.scorer,
		matchFacts:            matchFactProvider,
		candidateScoringLimit: config.CandidateScoringLimitPerSeed,
		candidateLimit:        config.CandidateLimitPerSeed,
		maxPlayers:            config.MaxPlayers,
		matchFactSnapshotMode: spec.MatchFactSnapshotMode,
		store:                 store,
	})
	return &LogicalNode{
		key:             spec.Key,
		state:           LogicalNodeReady,
		config:          config,
		factSpecs:       schema.FactSpecs(),
		store:           store,
		evaluator:       evaluator,
		seedOrderPolicy: seedOrderPolicy,
	}, nil
}

// FactSpecs returns an owned snapshot of this LogicalNode's complete Fact
// metadata. The returned slice may be freely modified by the caller without
// affecting the compiled rule or the matching runtime.
func (p *LogicalNode) FactSpecs() []FactSpec {
	if p == nil {
		return nil
	}
	return append([]FactSpec(nil), p.factSpecs...)
}

// Add inserts a Ticket into this LogicalNode's owned pool and returns its
// private DocID. The caller's Ticket is never retained or mutated.
func (p *LogicalNode) Add(ticket *Ticket) (uint32, error) {
	return p.store.Add(ticket)
}

func (p *LogicalNode) Remove(ticketID TicketID) bool {
	return p.store.Remove(ticketID)
}

// Get returns an owned deep copy of the requested Ticket. Mutating or
// retaining the returned value cannot affect the LogicalNode-owned Ticket.
func (p *LogicalNode) Get(ticketID TicketID) (*Ticket, bool) {
	return p.store.Get(ticketID)
}

func (p *LogicalNode) Len() int { return p.store.Len() }

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
// The normal path keeps the optional timing instrumentation disabled.
func (p *LogicalNode) ProduceMatch(ctx context.Context) (*common.Match, error) {
	match, err := p.produceMatch(ctx, nil)
	return match, err
}

func (p *LogicalNode) produceMatch(ctx context.Context, trace *produceMatchTrace) (*common.Match, error) {
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
	seedStart := trace.start()
	seed := p.nextSeed()
	trace.addDuration(produceStageSeedPreparation, seedStart)
	if seed == nil {
		return nil, nil
	}
	trace.recordSeedAttempt()
	p.factGeneration++
	if p.factGeneration == 0 {
		return nil, fmt.Errorf("Fact generation overflow")
	}
	generation := p.factGeneration
	sessionStart := trace.start()
	session, err := p.evaluator.BeginSession(ctx, TickFactInput{
		Now:  p.seedRound.now,
		Node: p.snapshot(),
	}, generation, trace)
	trace.addDuration(produceStageSessionPreparation, sessionStart)
	if err != nil {
		return nil, err
	}
	attemptLimit := p.config.SeedScheduler.AttemptLimitPerProduceMatch
	// The first seed has already been reserved by nextSeed, so add it back
	// when calculating this call's remaining round capacity.
	if remaining := p.config.SeedScheduler.AttemptLimitPerMatchRound - p.seedRound.attemptedSeeds + 1; attemptLimit > remaining {
		attemptLimit = remaining
	}
	if attemptLimit <= 0 {
		return nil, nil
	}
	var evaluationErrors []error
	for attempted := 0; attempted < attemptLimit; attempted++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		currentSeed := seed
		if attempted > 0 {
			seedStart := trace.start()
			currentSeed = p.nextSeed()
			trace.addDuration(produceStageSeedPreparation, seedStart)
		}
		if currentSeed == nil {
			break
		}
		if attempted > 0 {
			trace.recordSeedAttempt()
		}
		match, err := session.Evaluate(ctx, currentSeed)
		if err != nil {
			if isContextTermination(err) {
				return nil, err
			}
			var evaluationErr *evaluation.Error
			if errors.As(err, &evaluationErr) {
				return nil, err
			}
			evaluationErrors = append(evaluationErrors, fmt.Errorf("seed %d: evaluation: %w", currentSeed.TicketID, err))
			continue
		}
		if match == nil {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			continue
		}
		commitStart := trace.start()
		err = p.store.Commit(match)
		trace.addDuration(produceStageCommit, commitStart)
		trace.recordCommit()
		if err != nil {
			return nil, err
		}
		return match, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nil, errors.Join(evaluationErrors...)
}

// snapshot returns the only LogicalNode-owned data exposed to a
// FactProvider. The owner goroutine calls it synchronously, so the values
// describe one stable point in the matching attempt without exposing the
// mutable store or any Ticket pointers.
func (p *LogicalNode) snapshot() LogicalNodeSnapshot {
	if p == nil {
		return LogicalNodeSnapshot{}
	}
	return LogicalNodeSnapshot{
		Key:          p.key,
		State:        p.state,
		WaitingCount: p.Len(),
	}
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
