package matchsystem

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/xianyu11-x/matchSystem/pkg/common"
	"github.com/xianyu11-x/matchSystem/pkg/identity"
	"github.com/xianyu11-x/matchSystem/pkg/matchsystem/evaluation"
	"github.com/xianyu11-x/matchSystem/pkg/matchsystem/fact"
	"github.com/xianyu11-x/matchSystem/pkg/matchsystem/prefilter"
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

	seedOrderRuntime        SeedOrderRuntime
	seedRound               seedRound
	factGeneration          uint64
	candidateRankingScratch candidateHeap
	// providerDescriptors are captured from the Provider implementations during
	// the load handshake. Hosts can query this snapshot without calling
	// Describe a second time outside the owner goroutine.
	providerDescriptors ProviderDescriptorSet
	logger              *slog.Logger
	timeline            *timelineState
	ruleJSON            []byte
}

type LogicalNodeSpec struct {
	Key identity.LogicalNodeKey
	// RuleJSON is the complete match-rule/v1 document for Key.Rule. It is the
	// sole production source of Contract, Prefilter, Evaluation, candidate
	// scoring, Seed selection, and runtime matching limits.
	RuleJSON []byte
	// FactProvider is the self-describing Tick Provider port.
	FactProvider TickProvider
	// ObjectFactProvider is the self-describing Object Provider port.
	ObjectFactProvider ObjectProvider
	// MatchFactProvider is the sole writer of Match-scoped Facts and the
	// self-describing Match Provider port.
	MatchFactProvider MatchProvider
	// MatchFactSnapshotMode defaults to None. DeepCopy is intended for
	// inspection/adapter paths that need detached Fact snapshots on Match.
	MatchFactSnapshotMode MatchFactSnapshotMode
	// Logger 由宿主配置；录制与普通运行日志共用此出口，为空时不输出日志。
	Logger *slog.Logger
	// Timeline 默认关闭，独立逻辑节点可在此启用时间线录制。
	Timeline TimelineConfig
}

type LogicalNodeDescriptor struct {
	Key         identity.LogicalNodeKey
	State       LogicalNodeState
	TicketCount int
}

func NewLogicalNode(spec LogicalNodeSpec) (*LogicalNode, error) {
	return newLogicalNode(spec, 0)
}

func newLogicalNode(spec LogicalNodeSpec, physicalID identity.PhysicalNodeID) (*LogicalNode, error) {
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
	if compiled.ruleKey.RuleID != spec.Key.Rule.RuleID {
		return nil, ruleCompileError("$.ruleKey", "RULE_KEY_MISMATCH", "Rule JSON declares %s but LogicalNode key requires %s", compiled.ruleKey, spec.Key.Rule)
	}
	if spec.MatchFactSnapshotMode != MatchFactSnapshotModeNone && spec.MatchFactSnapshotMode != MatchFactSnapshotModeDeepCopy {
		return nil, fmt.Errorf("invalid MatchFactSnapshotMode %d", spec.MatchFactSnapshotMode)
	}
	schema := compiled.contract.Clone()
	tickProvider, tickDescriptor, tickPresent, err := resolveTickProvider(spec.FactProvider)
	if err != nil {
		return nil, fmt.Errorf("provider handshake for LogicalNode %s: %w", spec.Key, err)
	}
	objectProvider, objectDescriptor, objectPresent, err := resolveObjectProvider(spec.ObjectFactProvider)
	if err != nil {
		return nil, fmt.Errorf("provider handshake for LogicalNode %s: %w", spec.Key, err)
	}
	matchProvider, matchDescriptor, matchPresent, err := resolveMatchProvider(spec.MatchFactProvider)
	if err != nil {
		return nil, fmt.Errorf("provider handshake for LogicalNode %s: %w", spec.Key, err)
	}
	if err := validateProviderHandshake("tick", fact.ScopeTick, factSpecsForScope(schema.Facts, fact.ScopeTick), tickPresent, tickDescriptor); err != nil {
		return nil, fmt.Errorf("provider handshake for LogicalNode %s: %w", spec.Key, err)
	}
	if err := validateProviderHandshake("object", fact.ScopeObject, factSpecsForScope(schema.Facts, fact.ScopeObject), objectPresent, objectDescriptor); err != nil {
		return nil, fmt.Errorf("provider handshake for LogicalNode %s: %w", spec.Key, err)
	}
	if err := validateProviderHandshake("match", fact.ScopeMatch, factSpecsForScope(schema.Facts, fact.ScopeMatch), matchPresent, matchDescriptor); err != nil {
		return nil, fmt.Errorf("provider handshake for LogicalNode %s: %w", spec.Key, err)
	}
	objectLayout, err := fact.NewObjectLayout(schema.Facts)
	if err != nil {
		return nil, fmt.Errorf("compile Object Fact layout for LogicalNode %s: %w", spec.Key, err)
	}
	config := compiled.config
	objectFactProvider := objectProvider
	seedOrderRuntime := compiled.seedPolicy
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
		matchFactProvider = matchProvider
	}
	evaluationPlan := compiled.evaluation
	store := newTicketStore(prefilterStore, objectLayout)
	evaluator := newSeedEvaluator(seedEvaluatorConfig{
		key:                   spec.Key,
		tickFacts:             tickProvider,
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
	timeline, err := newTimelineState(spec.Logger, spec.Timeline, physicalID)
	if err != nil {
		return nil, err
	}
	node := &LogicalNode{
		key:                     spec.Key,
		state:                   LogicalNodeReady,
		config:                  config,
		factSpecs:               schema.FactSpecs(),
		store:                   store,
		evaluator:               evaluator,
		seedOrderRuntime:        seedOrderRuntime,
		candidateRankingScratch: make(candidateHeap, 0, candidateRankingScratchCapacity),
		logger:                  spec.Logger,
		timeline:                timeline,
		ruleJSON:                append([]byte(nil), spec.RuleJSON...),
		providerDescriptors: ProviderDescriptorSet{
			Tick:   tickDescriptor,
			Object: objectDescriptor,
			Match:  matchDescriptor,
		},
	}
	node.startTimeline()
	return node, nil
}

// ProviderDescriptors returns the immutable capability snapshot captured by
// the LogicalNode startup handshake. The returned descriptors are detached
// from the node and may be freely modified by the caller.
func (p *LogicalNode) ProviderDescriptors() ProviderDescriptorSet {
	if p == nil {
		return ProviderDescriptorSet{}
	}
	return p.providerDescriptors.Clone()
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

// TimelineInstanceID 返回当前录制流的实例标识；未开启录制时返回空字符串。
func (p *LogicalNode) TimelineInstanceID() string {
	if p == nil || p.timeline == nil {
		return ""
	}
	return p.timeline.instanceID
}

// Add inserts a Ticket into this LogicalNode's owned pool. The node's private
// DocID is retained entirely inside ticketStore/Prefilter and never crosses
// this API boundary. The caller's Ticket is never retained or mutated.
func (p *LogicalNode) Add(ticket *Ticket) (err error) {
	if p.logger != nil {
		defer func() {
			if err != nil {
				p.logAddError(ticket, err)
			}
		}()
	}
	_, err = p.store.Add(ticket)
	if err != nil {
		return err
	}
	if p.seedOrderRuntime != nil {
		stored, ok := p.store.lookupTicketID(ticket.TicketID)
		if !ok {
			return fmt.Errorf("ticket store added TicketID %d without a live entry", ticket.TicketID)
		}
		p.seedOrderRuntime.Add(stored.Ticket)
	}
	if p.timelineActive() {
		// 日志与池互不共享 Ticket，即使 Handler 异步保留事件也不会被后续成局修改。
		stored, _ := p.store.lookupTicketID(ticket.TicketID)
		sequence := p.timeline.emit(p, TimelineEvent{Type: TimelineTicketAdd, Ticket: common.CloneTicket(stored.Ticket)})
		p.timeline.addSequences[ticket.TicketID] = sequence
	}
	return nil
}

func (p *LogicalNode) logAddError(ticket *Ticket, err error) {
	if p.logger == nil || err == nil {
		return
	}
	var ticketID TicketID
	if ticket != nil {
		ticketID = ticket.TicketID
	}
	p.logger.Warn("ticket add rejected", "logical_node", p.key.String(), "ticket_id", ticketID, "error", err)
}

func (p *LogicalNode) Remove(ticketID TicketID) bool {
	return p.remove(ticketID, TimelineTicketRemove)
}

// Withdraw 表示宿主主动取消排队；只有成功删除才记录主动退出事件。
func (p *LogicalNode) Withdraw(ticketID TicketID) bool {
	return p.remove(ticketID, TimelineTicketWithdraw)
}

func (p *LogicalNode) remove(ticketID TicketID, eventType TimelineEventType) bool {
	removed := p.store.Remove(ticketID)
	if removed {
		if p.seedOrderRuntime != nil {
			p.seedOrderRuntime.Remove(ticketID)
		}
		if p.timelineActive() {
			sequence := p.timeline.addSequences[ticketID]
			delete(p.timeline.addSequences, ticketID)
			p.timeline.emit(p, TimelineEvent{Type: eventType, TicketID: ticketID, AddSequence: sequence})
		}
	}
	return removed
}

// Get returns an owned deep copy of the requested Ticket. Mutating or
// retaining the returned value cannot affect the LogicalNode-owned Ticket.
func (p *LogicalNode) Get(ticketID TicketID) (*Ticket, bool) {
	return p.store.Get(ticketID)
}

func (p *LogicalNode) Len() int { return p.store.Len() }

func (p *LogicalNode) addTicket(ctx context.Context, ticket *common.Ticket) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if ticket == nil {
		err := fmt.Errorf("ticket is nil")
		p.logAddError(ticket, err)
		return err
	}
	if p.state != LogicalNodeReady {
		p.logAddError(ticket, ErrLogicalNodeNotReady)
		return ErrLogicalNodeNotReady
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
	if !p.timelineActive() {
		match, err := p.produceMatchImpl(ctx, trace)
		p.logProduceError(err)
		return match, err
	}
	// 在入口保留时刻和序号，即使 Handler 在调用结束后才收到结果，也可还原顺序。
	timeline := p.timeline
	at := timeline.clock()
	before := p.seedRound.attemptedSeeds
	event := timeline.reserve(p, at, TimelineEvent{Type: TimelineProduceMatch, Round: timeline.round, Now: p.seedRound.now})
	match, err := p.produceMatchImpl(ctx, trace)
	event.SeedAttempts = p.seedRound.attemptedSeeds - before
	switch {
	case err != nil:
		event.Result = TimelineResultError
		event.Error = err.Error()
	case match != nil:
		event.Result = TimelineResultMatch
		for _, ticket := range match.Tickets {
			if ticket != nil {
				event.MatchTicketIDs = append(event.MatchTicketIDs, ticket.TicketID)
				delete(timeline.addSequences, ticket.TicketID)
			}
		}
	default:
		event.Result = TimelineResultNoMatch
	}
	timeline.write(event)
	p.logProduceError(err)
	return match, err
}

func (p *LogicalNode) logProduceError(err error) {
	if p.logger != nil && err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		p.logger.Warn("match production failed", "logical_node", p.key.String(), "error", err)
	}
}

func (p *LogicalNode) produceMatchImpl(ctx context.Context, trace *produceMatchTrace) (*common.Match, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if p.state != LogicalNodeReady && p.state != LogicalNodeDraining {
		return nil, ErrLogicalNodeNotReady
	}
	if !p.seedRound.initialized {
		return nil, ErrMatchRoundNotStarted
	}
	// Reserve one seed from the runtime-owned stream before creating Tick
	// Facts. Provider/configuration failures must not make that seed selectable
	// again in this round.
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
	}, generation, &p.candidateRankingScratch, trace)
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
		for _, ticket := range match.Tickets {
			if ticket == nil {
				continue
			}
			if p.seedOrderRuntime != nil {
				p.seedOrderRuntime.Remove(ticket.TicketID)
			}
		}
		if p.logger != nil {
			p.logger.Info("match produced", "logical_node", p.key.String(), "ticket_count", len(match.Tickets))
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
