package simulator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/xianyu11-x/matchSystem/pkg/client"
	"github.com/xianyu11-x/matchSystem/pkg/common"
	"github.com/xianyu11-x/matchSystem/pkg/identity"
	"github.com/xianyu11-x/matchSystem/pkg/matchsystem"
	"github.com/xianyu11-x/matchSystem/pkg/matchsystem/contract"
	"github.com/xianyu11-x/matchSystem/pkg/matchsystem/fact"
)

var (
	ErrSimulatorClosed  = errors.New("simulator is closed")
	ErrRoundNotStarted  = errors.New("simulator round has not started")
	ErrUnknownNode      = errors.New("simulator PhysicalNode is not present")
	ErrUnknownRule      = errors.New("simulator LogicalNode is not present")
	ErrInvalidMatchID   = errors.New("invalid simulator match ID")
	ErrInvalidTicket    = errors.New("invalid simulator ticket")
	ErrInvalidBatchSpec = errors.New("invalid simulator batch generator specification")
)

// Simulator is the application/runtime boundary around one or more
// matchsystem.PhysicalNode instances. Its mutex serializes route-table access,
// runtime swaps, and cross-node commands; each PhysicalNode has an additional
// owner goroutine in physicalNodeAdapter.
type Simulator struct {
	mu            sync.RWMutex
	runtime       *simulatorRuntime
	capabilities  Capabilities
	closed        bool
	traffic       *trafficRun
	trafficStatus TrafficStatus
	nextMatchID   uint64
	replayLog     *replayLog
	// ruleLibrary 独立于运行态，场景替换和节点增删均不会修改规则库。
	ruleLibrary      RuleLibrarySnapshot
	ruleLibraryIndex map[identity.RuleID]json.RawMessage
	// matchIDInitialized distinguishes a zero-value Simulator from a real
	// sequence value of zero. A zero value is initialized on first scenario
	// publication; an initialized counter at zero is treated as overflow.
	matchIDInitialized bool
}

// Application is a semantic alias: the simulator runtime is the application
// object exposed to an HTTP host.
type Application = Simulator

// Runtime is a semantic alias for code that integrates the core as a runtime
// rather than as a visual simulation service.
type Runtime = Simulator

type simulatorRuntime struct {
	scenario    Scenario
	router      *client.Router
	nodes       map[identity.PhysicalNodeID]*physicalNodeAdapter
	physical    map[identity.PhysicalNodeID]PhysicalNodeSpec
	rules       map[logicalIndexKey]RuleSpec
	routeTimers map[identity.PhysicalNodeID]*routeTimer
	validators  map[ownerIndexKey]*fact.Validator
	providers   map[logicalIndexKey]*PythonWorker
	// providerDescriptors 保存 LogicalNode 构造时 Provider 握手取得的有效描述符，
	// 不从 Contract Facts 或运行时值推断。
	providerDescriptors       map[logicalIndexKey]matchsystem.ProviderDescriptorSet
	providerDescriptorSources map[logicalIndexKey]providerDescriptorSourceSet
	registry                  *ObservationRegistry
	replayLog                 *replayLog

	roundActive bool
	round       uint64
	roundNow    int64
	nextEvent   uint64
	events      []Event
	subscribers map[*eventSubscription]struct{}
}

type providerDescriptorSourceSet struct {
	Tick   string
	Object string
	Match  string
}

type describedProvider interface {
	Describe() (matchsystem.ProviderDescriptor, error)
}

func providerDescriptorSource(descriptor *matchsystem.ProviderDescriptor, provider describedProvider, python bool) string {
	if descriptor == nil {
		return ""
	}
	if python && providerValuePresent(provider) {
		return "python"
	}
	return "go"
}

type eventSubscription struct {
	ch        chan Event
	closeOnce sync.Once
}

func (subscription *eventSubscription) close() {
	if subscription == nil {
		return
	}
	subscription.closeOnce.Do(func() { close(subscription.ch) })
}

// NewSimulator builds and validates a complete in-process simulator runtime.
// No partially loaded runtime is published when scenario construction fails.
func NewSimulator(scenario Scenario) (*Simulator, error) {
	return newSimulatorWithReplayLog(scenario, newReplayLog())
}

// NewSimulatorWithReplayLogOutput 将回放消息及普通日志同步写入 output，同时保留内存环。
// 首次写入错误或短写时停止文件输出并调用 onError；回调不得重入模拟器。
// output 的关闭由调用方负责，应在 Simulator.Close 返回后关闭。
func NewSimulatorWithReplayLogOutput(scenario Scenario, output io.Writer, onError func(error)) (*Simulator, error) {
	return newSimulatorWithReplayLog(scenario, newReplayLogWithOutput(output, onError))
}

func newSimulatorWithReplayLog(scenario Scenario, log *replayLog) (*Simulator, error) {
	runtime, err := buildRuntime(scenario, log)
	if err != nil {
		return nil, err
	}
	library, libraryIndex := initialRuleLibrary(runtime.scenario)
	simulator := &Simulator{runtime: runtime, replayLog: log, capabilities: defaultCapabilities(), nextMatchID: 1, matchIDInitialized: true, ruleLibrary: library, ruleLibraryIndex: libraryIndex}
	simulator.mu.Lock()
	simulator.startRouteTimersLocked(runtime)
	simulator.mu.Unlock()
	return simulator, nil
}

// NewRuntime is an alias used by hosts that name the in-process execution
// object explicitly.
func NewRuntime(scenario Scenario) (*Simulator, error) { return NewSimulator(scenario) }

// New is the concise constructor used by application hosts.
func New(scenarios ...Scenario) (*Simulator, error) {
	return NewService(scenarios...)
}

// NewService is an application-facing constructor. With no argument it
// starts an empty runtime that can be populated through ReplaceScenario.
func NewService(scenarios ...Scenario) (*Simulator, error) {
	if len(scenarios) > 1 {
		return nil, fmt.Errorf("NewService accepts at most one Scenario")
	}
	if len(scenarios) == 0 {
		return NewSimulator(Scenario{})
	}
	return NewSimulator(scenarios[0])
}

// NewApplication names the same constructor for hosts that distinguish the
// simulator application from the lower-level matchsystem package.
func NewApplication(scenarios ...Scenario) (*Simulator, error) {
	return NewService(scenarios...)
}

func buildRuntime(input Scenario, log *replayLog) (*simulatorRuntime, error) {
	scenario := input.Clone()
	if scenario.SchemaVersion == "" {
		scenario.SchemaVersion = ScenarioSchemaVersion
	}
	for index := range scenario.PhysicalNodes {
		scenario.PhysicalNodes[index].Selector = normalizeSelector(scenario.PhysicalNodes[index].Selector)
	}
	report := validateScenario(scenario)
	if !report.Valid {
		return nil, report.Err()
	}
	runtime := &simulatorRuntime{
		scenario:                  scenario,
		nodes:                     make(map[identity.PhysicalNodeID]*physicalNodeAdapter, len(scenario.PhysicalNodes)),
		physical:                  make(map[identity.PhysicalNodeID]PhysicalNodeSpec, len(scenario.PhysicalNodes)),
		rules:                     make(map[logicalIndexKey]RuleSpec, len(scenario.Rules)),
		validators:                make(map[ownerIndexKey]*fact.Validator, len(scenario.Rules)),
		providers:                 make(map[logicalIndexKey]*PythonWorker, len(scenario.Rules)),
		providerDescriptors:       make(map[logicalIndexKey]matchsystem.ProviderDescriptorSet, len(scenario.Rules)),
		providerDescriptorSources: make(map[logicalIndexKey]providerDescriptorSourceSet, len(scenario.Rules)),
		registry:                  NewObservationRegistryWithLimit(scenario.MatchHistoryLimit),
		replayLog:                 log,
		nextEvent:                 1,
		subscribers:               make(map[*eventSubscription]struct{}),
	}
	cleanup := true
	defer func() {
		if cleanup {
			closeRuntime(runtime)
		}
	}()

	for _, physicalSpec := range scenario.PhysicalNodes {
		runtime.physical[physicalSpec.ID] = physicalSpec
		adapter, err := createRuntimePhysicalNode(physicalSpec, scenario.Rules, log)
		if err != nil {
			return nil, err
		}
		runtime.nodes[physicalSpec.ID] = adapter
	}

	for _, rule := range scenario.Rules {
		if err := loadRuntimeRule(context.Background(), runtime, rule); err != nil {
			return nil, err
		}
	}
	router, err := snapshotRouter(context.Background(), runtime, scenario.Routing)
	if err != nil {
		return nil, err
	}
	runtime.router = router

	cleanup = false
	return runtime, nil
}

func createRuntimePhysicalNode(physical PhysicalNodeSpec, rules []RuleSpec, log *replayLog) (*physicalNodeAdapter, error) {
	selector, err := selectorForPhysical(physical.ID, physical.Selector, rules)
	if err != nil {
		return nil, err
	}
	var logger *slog.Logger
	if log != nil {
		logger = log.logger
	}
	core, err := matchsystem.NewPhysicalNode(physical.ID, matchsystem.WithLogicalNodeSelector(selector), matchsystem.WithLogger(logger), matchsystem.WithTimeline(matchsystem.TimelineConfig{Enabled: log != nil, Clock: log.clockTime}))
	if err != nil {
		return nil, fmt.Errorf("create PhysicalNode %d: %w", physical.ID, err)
	}
	return newPhysicalNodeAdapter(core)
}

// loadRuntimeRule 将规则和 Provider 加载到指定的运行时；调用方负责失败时关闭已创建的资源。
func loadRuntimeRule(ctx context.Context, runtime *simulatorRuntime, rule RuleSpec) error {
	err := loadRuntimeRuleWith(ctx, runtime, rule, func(spec matchsystem.LogicalNodeSpec) (matchsystem.ProviderDescriptorSet, error) {
		adapter := runtime.nodes[rule.PhysicalNodeID]
		if adapter == nil {
			return matchsystem.ProviderDescriptorSet{}, fmt.Errorf("%w: %d", ErrUnknownNode, rule.PhysicalNodeID)
		}
		if err := adapter.Load(ctx, spec); err != nil {
			return matchsystem.ProviderDescriptorSet{}, runtimeRuleLoadError(runtime, rule, err)
		}
		descriptors, err := adapter.ProviderDescriptors(ctx, rule.LogicalNode)
		if err != nil {
			return matchsystem.ProviderDescriptorSet{}, fmt.Errorf("read Provider descriptors for LogicalNode %s: %w", rule.LogicalNode, err)
		}
		return descriptors, nil
	})
	if err != nil {
		return err
	}
	runtime.rules[logicalIndex(rule.LogicalNode)] = rule
	return nil
}

// loadRuntimeRuleWith 共用规则编译、Provider 启动与握手结果登记。
func loadRuntimeRuleWith(ctx context.Context, runtime *simulatorRuntime, rule RuleSpec, load func(matchsystem.LogicalNodeSpec) (matchsystem.ProviderDescriptorSet, error)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	owner := identity.OwnerRef{LogicalNode: rule.LogicalNode, PhysicalNodeID: rule.PhysicalNodeID}
	compiled, err := matchsystem.CompileRuleJSON(rule.RuleJSON)
	if err != nil {
		return fmt.Errorf("compile Rule JSON for %s: %w", rule.LogicalNode, err)
	}
	schema := compiled.Contract()
	validator, err := fact.NewValidator(schema.FactSpecs())
	if err != nil {
		return fmt.Errorf("compile Fact validator for %s: %w", rule.LogicalNode, err)
	}
	runtime.validators[ownerIndex(owner)] = validator
	runtimeRule := rule
	if rule.PythonProvider != nil {
		startup := newPythonProviderStartupInput(schema.FactSpecs(), rule.TickFacts.values())
		worker, startErr := startPythonProvider(*rule.PythonProvider, schema.FactSpecs(), startup)
		if startErr != nil {
			var providerErr *PythonProviderError
			if !errors.As(startErr, &providerErr) {
				startErr = &PythonProviderError{Method: "startup", Diagnostic: startErr.Error(), Err: startErr}
			}
			return fmt.Errorf("start Python provider for LogicalNode %s: %w", rule.LogicalNode, startErr)
		}
		runtime.providers[logicalIndex(rule.LogicalNode)] = worker
		runtimeRule.FactProvider, runtimeRule.ObjectFactProvider, runtimeRule.MatchFactProvider = pythonProviderCallbacks(worker, runtime.registry, owner)
	}
	spec := runtimeLogicalNodeSpec(runtimeRule, schema, runtime.registry, owner, validator)
	descriptors, err := load(spec)
	if err != nil {
		return err
	}
	runtime.providerDescriptors[logicalIndex(rule.LogicalNode)] = descriptors.Clone()
	runtime.providerDescriptorSources[logicalIndex(rule.LogicalNode)] = providerDescriptorSourceSet{
		Tick:   providerDescriptorSource(descriptors.Tick, runtimeRule.FactProvider, rule.PythonProvider != nil),
		Object: providerDescriptorSource(descriptors.Object, runtimeRule.ObjectFactProvider, rule.PythonProvider != nil),
		Match:  providerDescriptorSource(descriptors.Match, runtimeRule.MatchFactProvider, rule.PythonProvider != nil),
	}
	return nil
}

func runtimeRuleLoadError(runtime *simulatorRuntime, rule RuleSpec, err error) error {
	if rule.PythonProvider != nil {
		var handshakeErr *matchsystem.ProviderHandshakeError
		if errors.As(err, &handshakeErr) {
			err = pythonProviderHandshakeError(runtime.providers[logicalIndex(rule.LogicalNode)], err)
		}
	}
	return fmt.Errorf("load LogicalNode %s on PhysicalNode %d: %w", rule.LogicalNode, rule.PhysicalNodeID, err)
}

func selectorForPhysical(id identity.PhysicalNodeID, kind SelectorKind, rules []RuleSpec) (matchsystem.LogicalNodeSelector, error) {
	switch normalizeSelector(kind) {
	case SelectorRoundRobin:
		return matchsystem.NewRoundRobinLogicalNodeSelector(), nil
	case SelectorLargestQueue:
		return matchsystem.NewLargestQueueLogicalNodeSelector(), nil
	case SelectorOldestWaiting:
		return matchsystem.NewOldestWaitingLogicalNodeSelector(), nil
	case SelectorWeighted:
		weights := make(map[identity.RuleKey]uint32)
		for _, rule := range rules {
			if rule.PhysicalNodeID == id {
				weights[rule.LogicalNode.Rule] = rule.Weight
			}
		}
		return matchsystem.NewSmoothWeightedRoundRobinLogicalNodeSelector(weights)
	default:
		return nil, fmt.Errorf("unsupported selector %q", kind)
	}
}

func runtimeLogicalNodeSpec(rule RuleSpec, schema contract.Contract, registry *ObservationRegistry, owner identity.OwnerRef, validator *fact.Validator) matchsystem.LogicalNodeSpec {
	tickSpecs := factsForScope(schema.Facts, fact.ScopeTick)
	objectSpecs := factsForScope(schema.Facts, fact.ScopeObject)
	matchSpecs := factsForScope(schema.Facts, fact.ScopeMatch)
	var tickProvider matchsystem.TickProvider
	if providerValuePresent(rule.FactProvider) {
		tickProvider = validatingTickProvider(rule.FactProvider, validator)
	} else {
		tickProvider = validatingTickProvider(simulatorTickFactProvider(rule.TickFacts, tickSpecs), validator)
	}

	var objectProvider matchsystem.ObjectProvider
	if providerValuePresent(rule.ObjectFactProvider) {
		objectProvider = validatingObjectProvider(rule.ObjectFactProvider, validator)
	}
	if !providerValuePresent(objectProvider) {
		if len(objectSpecs) > 0 {
			objectProvider = validatingObjectProvider(simulatorObjectFactProvider(registry, owner, objectSpecs), validator)
		}
	}

	var matchProvider matchsystem.MatchProvider
	if providerValuePresent(rule.MatchFactProvider) {
		matchProvider = rule.MatchFactProvider
	}
	if matchProvider == nil && len(matchSpecs) > 0 {
		matchProvider = defaultMatchFactProvider{specs: matchSpecs}
	}
	var describedMatchProvider matchsystem.MatchProvider
	if matchProvider != nil {
		describedMatchProvider = validatingMatchFactProvider{provider: matchProvider, validator: validator}
	}

	return matchsystem.LogicalNodeSpec{
		Key:                   rule.LogicalNode,
		RuleJSON:              append([]byte(nil), rule.RuleJSON...),
		FactProvider:          tickProvider,
		ObjectFactProvider:    objectProvider,
		MatchFactProvider:     describedMatchProvider,
		MatchFactSnapshotMode: matchsystem.MatchFactSnapshotModeDeepCopy,
	}
}

// pythonProviderHandshakeError keeps failures that happen after a worker has
// successfully answered Ready (descriptor source selection and core Contract
// coverage) in the same structured error family as protocol failures. The API
// adapter can then expose a stable PYTHON_PROVIDER_FAILED code and the actual
// handshake diagnostic instead of reducing the candidate failure to 500
// INTERNAL_ERROR.
func pythonProviderHandshakeError(worker *PythonWorker, err error) error {
	if err == nil {
		return nil
	}
	var providerErr *PythonProviderError
	if errors.As(err, &providerErr) {
		return err
	}
	diagnostic := err.Error()
	if worker != nil && worker.Diagnostic() != "" {
		diagnostic += "; stderr: " + worker.Diagnostic()
	}
	return &PythonProviderError{Method: "handshake", Diagnostic: diagnostic, Err: err}
}

func cloneProviderDescriptor(descriptor *matchsystem.ProviderDescriptor) *matchsystem.ProviderDescriptor {
	if descriptor == nil {
		return nil
	}
	copy := descriptor.Clone()
	return &copy
}

func factsForScope(specs []fact.Spec, scope fact.Scope) []fact.Spec {
	result := make([]fact.Spec, 0)
	for _, spec := range specs {
		if spec.Scope == scope {
			result = append(result, spec)
		}
	}
	return result
}

func providerValuePresent(provider describedProvider) bool {
	if provider == nil {
		return false
	}
	value := reflect.ValueOf(provider)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return !value.IsNil()
	default:
		return true
	}
}

func validatingTickProvider(provider matchsystem.TickProvider, validator *fact.Validator) matchsystem.TickProvider {
	if !providerValuePresent(provider) {
		return nil
	}
	return describedTickProvider{provider: provider, validator: validator}
}

type describedTickProvider struct {
	provider  matchsystem.TickProvider
	validator *fact.Validator
}

func (p describedTickProvider) Provide(ctx context.Context, input matchsystem.TickFactInput) (matchsystem.Facts, error) {
	values, err := p.provider.Provide(ctx, input)
	if err != nil {
		return matchsystem.Facts{}, err
	}
	if p.validator != nil {
		if _, err := p.validator.ValidateLayer("facts.tick", values, fact.ScopeTick); err != nil {
			return matchsystem.Facts{}, err
		}
	}
	return fact.Clone(values), nil
}

func (p describedTickProvider) Describe() (matchsystem.ProviderDescriptor, error) {
	return p.provider.Describe()
}

func validatingObjectProvider(provider matchsystem.ObjectProvider, validator *fact.Validator) matchsystem.ObjectProvider {
	if !providerValuePresent(provider) {
		return nil
	}
	return describedObjectProvider{provider: provider, validator: validator}
}

type describedObjectProvider struct {
	provider  matchsystem.ObjectProvider
	validator *fact.Validator
}

func (p describedObjectProvider) Provide(object *common.Ticket, now int64, tick matchsystem.Facts, out matchsystem.ObjectFactWriter) error {
	if err := p.provider.Provide(object, now, tick, out); err != nil {
		return err
	}
	if p.validator != nil {
		if _, err := p.validator.ValidateLayer("facts.object", out.Values(), fact.ScopeObject); err != nil {
			return err
		}
	}
	return nil
}

func (p describedObjectProvider) Describe() (matchsystem.ProviderDescriptor, error) {
	return p.provider.Describe()
}

type validatingMatchFactProvider struct {
	provider  matchsystem.MatchProvider
	validator *fact.Validator
}

func (p validatingMatchFactProvider) Describe() (matchsystem.ProviderDescriptor, error) {
	return p.provider.Describe()
}

func (p validatingMatchFactProvider) Initialize(ctx context.Context, input matchsystem.InitializeInput) (matchsystem.Facts, error) {
	values, err := p.provider.Initialize(ctx, input)
	if err != nil {
		return matchsystem.Facts{}, err
	}
	return p.validate(values)
}

func (p validatingMatchFactProvider) OnJoin(ctx context.Context, input matchsystem.JoinInput) (matchsystem.Facts, error) {
	values, err := p.provider.OnJoin(ctx, input)
	if err != nil {
		return matchsystem.Facts{}, err
	}
	return p.validate(values)
}

func (p validatingMatchFactProvider) validate(values matchsystem.Facts) (matchsystem.Facts, error) {
	if p.validator != nil {
		if err := p.validator.ValidateCompleteMatch("facts.match", values); err != nil {
			return matchsystem.Facts{}, err
		}
	}
	return fact.Clone(values), nil
}

type defaultMatchFactProvider struct{ specs []fact.Spec }

func (defaultMatchFactProvider) Describe() (matchsystem.ProviderDescriptor, error) {
	return matchsystem.ProviderDescriptor{
		ID:      "simulator.match-facts",
		Version: "v1",
		Facts: []matchsystem.FactSpec{{
			Name: MemberCountFactName, Type: matchsystem.FactTypeInt64, Scope: matchsystem.FactScopeMatch,
		}},
	}, nil
}

func (p defaultMatchFactProvider) Initialize(context.Context, matchsystem.InitializeInput) (matchsystem.Facts, error) {
	return p.initialValues(), nil
}

func (p defaultMatchFactProvider) OnJoin(_ context.Context, input matchsystem.JoinInput) (matchsystem.Facts, error) {
	values := fact.Clone(input.MatchFactsBefore)
	if values.StringLists == nil {
		values.StringLists = make(map[string][]string)
	}
	if values.Uint64Lists == nil {
		values.Uint64Lists = make(map[string][]uint64)
	}
	if values.Int64Values == nil {
		values.Int64Values = make(map[string]int64)
	}
	memberCountDeclared := false
	for _, spec := range p.specs {
		switch spec.Type {
		case fact.TypeStrings:
			if _, exists := values.StringLists[spec.Name]; !exists {
				values.StringLists[spec.Name] = []string{}
			}
		case fact.TypeUint64s:
			if _, exists := values.Uint64Lists[spec.Name]; !exists {
				values.Uint64Lists[spec.Name] = []uint64{}
			}
		case fact.TypeInt64:
			if spec.Name == MemberCountFactName {
				memberCountDeclared = true
				continue
			}
			values.Int64Values[spec.Name]++
		}
	}
	// A simulator Match always starts with one seed and OnJoin is called only
	// after a candidate has passed CanJoin. Only materialize the standard Fact
	// when the Contract declares it, keeping runtime values aligned with the
	// other simulator-owned built-in providers.
	if memberCountDeclared {
		if current, exists := values.Int64Values[MemberCountFactName]; exists {
			if current == math.MaxInt64 {
				return matchsystem.Facts{}, fmt.Errorf("simulator Match Fact %q overflow", MemberCountFactName)
			}
			values.Int64Values[MemberCountFactName] = current + 1
		} else {
			// A direct provider call without Initialize is still well-defined:
			// the seed plus the joining candidate form a two-member Match.
			values.Int64Values[MemberCountFactName] = 2
		}
	}
	return values, nil
}

func (p defaultMatchFactProvider) initialValues() matchsystem.Facts {
	values := matchsystem.Facts{
		StringLists: make(map[string][]string),
		Uint64Lists: make(map[string][]uint64),
		Int64Values: make(map[string]int64),
	}
	for _, spec := range p.specs {
		switch spec.Type {
		case fact.TypeStrings:
			values.StringLists[spec.Name] = []string{}
		case fact.TypeUint64s:
			values.Uint64Lists[spec.Name] = []uint64{}
		case fact.TypeInt64:
			values.Int64Values[spec.Name] = 1
		}
	}
	return values
}

func closeRuntime(runtime *simulatorRuntime) {
	if runtime == nil {
		return
	}
	ids := make([]identity.PhysicalNodeID, 0, len(runtime.nodes))
	for id := range runtime.nodes {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		if adapter := runtime.nodes[id]; adapter != nil {
			adapter.close()
		}
	}
	providerKeys := make([]logicalIndexKey, 0, len(runtime.providers))
	for key := range runtime.providers {
		providerKeys = append(providerKeys, key)
	}
	sort.Slice(providerKeys, func(i, j int) bool {
		if providerKeys[i].ruleID != providerKeys[j].ruleID {
			return providerKeys[i].ruleID < providerKeys[j].ruleID
		}
		return providerKeys[i].placementID < providerKeys[j].placementID
	})
	for _, key := range providerKeys {
		if worker := runtime.providers[key]; worker != nil {
			worker.Close()
		}
	}
	runtime.providers = nil
	runtime.providerDescriptors = nil
	runtime.providerDescriptorSources = nil
	for subscription := range runtime.subscribers {
		subscription.close()
	}
	runtime.subscribers = nil
}

// providerFailure reports a terminal process-backed provider failure in a
// stable LogicalNode order. Explicit worker shutdown has no failure value, so
// runtime replacement and normal service close do not turn into run errors.
func (runtime *simulatorRuntime) providerFailure() error {
	if runtime == nil {
		return nil
	}
	keys := make([]logicalIndexKey, 0, len(runtime.providers))
	for key := range runtime.providers {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].ruleID != keys[j].ruleID {
			return keys[i].ruleID < keys[j].ruleID
		}
		return keys[i].placementID < keys[j].placementID
	})
	for _, key := range keys {
		if worker := runtime.providers[key]; worker != nil {
			if err := worker.Failure(); err != nil {
				return fmt.Errorf("Python provider for LogicalNode %s failed: %w", runtime.rules[key].LogicalNode, err)
			}
		}
	}
	return nil
}

func (s *Simulator) runtimeReadLocked() (*simulatorRuntime, error) {
	if s == nil || s.closed || s.runtime == nil {
		return nil, ErrSimulatorClosed
	}
	return s.runtime, nil
}

// ReplaceScenario constructs a complete new cluster before atomically
// publishing it. The previous runtime remains usable if validation/building
// fails, and is closed only after the swap is visible.
func (s *Simulator) ReplaceScenario(ctx context.Context, scenario Scenario) error {
	_, err := s.replaceScenario(ctx, scenario)
	return err
}

// ReplaceScenarioAndGet publishes a complete new runtime and returns the
// detached Scenario snapshot committed by that exact publication. The swap
// and snapshot capture occur under one Simulator lock, so a concurrent
// replacement cannot cause the caller to observe a later scenario. It is the
// response-safe variant for adapters; ReplaceScenario remains available for
// callers that only need the error result.
func (s *Simulator) ReplaceScenarioAndGet(ctx context.Context, scenario Scenario) (Scenario, error) {
	return s.replaceScenario(ctx, scenario)
}

func (s *Simulator) replaceScenario(ctx context.Context, scenario Scenario) (Scenario, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return Scenario{}, err
	}
	if s == nil {
		return Scenario{}, ErrSimulatorClosed
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return Scenario{}, ErrSimulatorClosed
	}
	if s.replayLog == nil {
		s.replayLog = newReplayLog()
	}
	log := s.replayLog
	s.mu.Unlock()
	next, err := buildRuntime(scenario, log)
	if err != nil {
		return Scenario{}, err
	}
	if err := ctx.Err(); err != nil {
		closeRuntime(next)
		return Scenario{}, err
	}
	if s == nil {
		closeRuntime(next)
		return Scenario{}, ErrSimulatorClosed
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		closeRuntime(next)
		return Scenario{}, ErrSimulatorClosed
	}
	s.stopTrafficLocked("stopped")
	old := s.runtime
	stopRouteTimersLocked(old)
	if !s.matchIDInitialized {
		s.nextMatchID = 1
		s.matchIDInitialized = true
	}
	s.runtime = next
	s.startRouteTimersLocked(next)
	committed := next.scenario.Clone()
	s.mu.Unlock()
	closeRuntime(old)
	return committed, nil
}

// Scenario returns a detached copy of the currently published scenario.
func (s *Simulator) Scenario() (Scenario, error) {
	if s == nil {
		return Scenario{}, ErrSimulatorClosed
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	runtime, err := s.runtimeReadLocked()
	if err != nil {
		return Scenario{}, err
	}
	return runtime.scenario.Clone(), nil
}

// GetScenario is the context-aware form used by HTTP/application adapters.
func (s *Simulator) GetScenario(ctx context.Context) (Scenario, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return Scenario{}, err
	}
	return s.Scenario()
}

func (s *Simulator) ValidateScenario(scenario Scenario) ValidationReport {
	return ValidateScenario(scenario)
}

func (s *Simulator) Capabilities(ctx context.Context) (Capabilities, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return Capabilities{}, err
	}
	if s == nil {
		return Capabilities{}, ErrSimulatorClosed
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return Capabilities{}, ErrSimulatorClosed
	}
	return cloneCapabilities(s.capabilities), nil
}

func cloneCapabilities(capabilities Capabilities) Capabilities {
	out := capabilities
	out.SchemaVersions = append([]string(nil), capabilities.SchemaVersions...)
	out.Selectors = append([]string(nil), capabilities.Selectors...)
	out.CandidateScorers = append([]string(nil), capabilities.CandidateScorers...)
	out.SeedSelections = append([]string(nil), capabilities.SeedSelections...)
	out.FactTypes = append([]string(nil), capabilities.FactTypes...)
	out.FactScopes = append([]string(nil), capabilities.FactScopes...)
	out.IndexTypes = append([]string(nil), capabilities.IndexTypes...)
	out.BuiltinAttributes = append([]BuiltinAttributeCapability(nil), capabilities.BuiltinAttributes...)
	for index := range out.BuiltinAttributes {
		out.BuiltinAttributes[index].IndexTypes = append([]string(nil), capabilities.BuiltinAttributes[index].IndexTypes...)
	}
	out.ExpressionOps = append([]string(nil), capabilities.ExpressionOps...)
	out.BitmapOps = append([]string(nil), capabilities.BitmapOps...)
	out.ScalarOperators = cloneOperatorCapabilities(capabilities.ScalarOperators)
	out.BitmapOperators = cloneOperatorCapabilities(capabilities.BitmapOperators)
	return out
}

func cloneOperatorCapabilities(values []OperatorCapability) []OperatorCapability {
	out := make([]OperatorCapability, len(values))
	for index, value := range values {
		out[index] = value
		out[index].Inputs = append([]string(nil), value.Inputs...)
		out[index].Fields = append([]string(nil), value.Fields...)
	}
	return out
}

// RouteNew exposes the client.Router decision without dispatching a Ticket.
// The returned OwnerRef is the stable dispatch target for AddRoutedTicket.
func (s *Simulator) RouteNew(ctx context.Context, request common.RouteRequest) (common.RouteDecision, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil {
		return common.RouteDecision{}, ErrSimulatorClosed
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	runtime, err := s.runtimeReadLocked()
	if err != nil {
		return common.RouteDecision{}, err
	}
	return runtime.router.RouteNew(ctx, request)
}

func (s *Simulator) ResolveOwner(owner identity.OwnerRef) (common.ResolvedOwner, error) {
	if s == nil {
		return common.ResolvedOwner{}, ErrSimulatorClosed
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	runtime, err := s.runtimeReadLocked()
	if err != nil {
		return common.ResolvedOwner{}, err
	}
	return runtime.router.ResolveOwner(owner)
}

// AddTicket routes and inserts one Ticket, then records its detached
// RouteDecision and object Fact snapshot. Ticket IDs are unique only within a
// LogicalNode owner, so the registry key retains the complete OwnerRef.
func (s *Simulator) AddTicket(ctx context.Context, input TicketInput) (TicketView, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil {
		return TicketView{}, ErrSimulatorClosed
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	runtime, err := s.runtimeReadLocked()
	if err != nil {
		return TicketView{}, err
	}
	return runtime.addTicket(ctx, input)
}

// addTicket 要求调用方持有模拟器写锁，按规则路由并投递对象。
func (runtime *simulatorRuntime) addTicket(ctx context.Context, input TicketInput) (TicketView, error) {
	request, err := input.routeRequest()
	if err != nil {
		return TicketView{}, err
	}
	decision, err := runtime.router.RouteNew(ctx, request)
	if err != nil {
		return TicketView{}, err
	}
	return runtime.addAtOwner(ctx, decision, input)
}

// AddRoutedTicket dispatches to an already retained RouteDecision. This is
// useful when an HTTP or test client separates routing from insertion.
func (s *Simulator) AddRoutedTicket(ctx context.Context, decision common.RouteDecision, input TicketInput) (TicketView, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil {
		return TicketView{}, ErrSimulatorClosed
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	runtime, err := s.runtimeReadLocked()
	if err != nil {
		return TicketView{}, err
	}
	return runtime.addRoutedTicket(ctx, decision, input)
}

// resolveRoutedDecision 要求调用方持有模拟器写锁，供单条和持续流量复用。
func (runtime *simulatorRuntime) resolveRoutedDecision(decision common.RouteDecision) (common.RouteDecision, error) {
	resolved, err := runtime.router.ResolveOwner(decision.Owner)
	if err != nil {
		return common.RouteDecision{}, err
	}
	physicalSpec, physicalExists := runtime.physical[decision.Owner.PhysicalNodeID]
	ruleSpec, ruleExists := runtime.rules[logicalIndex(decision.Owner.LogicalNode)]
	if !physicalExists || !ruleExists || !physicalSpec.Enabled || !ruleSpec.Enabled {
		return common.RouteDecision{}, client.ErrNoRoute
	}
	if resolved.Endpoint != decision.Endpoint || decision.DecisionID == 0 {
		// A decision may be reconstructed by a caller that only retained the
		// OwnerRef; preserve the route-table endpoint but reject stale or
		// malformed decisions with a different endpoint.
		if decision.Endpoint != "" && resolved.Endpoint != decision.Endpoint {
			return common.RouteDecision{}, fmt.Errorf("route decision endpoint does not match OwnerRef")
		}
		decision.Endpoint = resolved.Endpoint
	}
	return decision, nil
}

// addRoutedTicket 要求调用方持有模拟器写锁。
func (runtime *simulatorRuntime) addRoutedTicket(ctx context.Context, decision common.RouteDecision, input TicketInput) (TicketView, error) {
	decision, err := runtime.resolveRoutedDecision(decision)
	if err != nil {
		return TicketView{}, err
	}
	return runtime.addAtOwner(ctx, decision, input)
}

func (runtime *simulatorRuntime) addAtOwner(ctx context.Context, decision common.RouteDecision, input TicketInput) (TicketView, error) {
	if runtime == nil {
		return TicketView{}, ErrSimulatorClosed
	}
	if input.TicketID == 0 || input.Rule.Validate() != nil {
		return TicketView{}, ErrInvalidTicket
	}
	owner := decision.Owner
	if err := owner.Validate(); err != nil {
		return TicketView{}, err
	}
	if owner.LogicalNode.Rule.RuleID != input.Rule.RuleID {
		return TicketView{}, fmt.Errorf("route decision Rule %s does not match Ticket Rule %s", owner.LogicalNode.Rule, input.Rule)
	}
	adapter := runtime.nodes[owner.PhysicalNodeID]
	if adapter == nil {
		return TicketView{}, fmt.Errorf("%w: %d", ErrUnknownNode, owner.PhysicalNodeID)
	}
	if rule, exists := runtime.rules[logicalIndex(owner.LogicalNode)]; !exists || rule.PhysicalNodeID != owner.PhysicalNodeID {
		return TicketView{}, fmt.Errorf("%w: %s", ErrUnknownRule, owner.LogicalNode)
	}
	if _, exists := runtime.registry.GetTicket(owner, input.TicketID); exists {
		return TicketView{}, ErrTicketAlreadyObserved
	}
	validator := runtime.validators[ownerIndex(owner)]
	if validator != nil {
		if err := validateFactSnapshot("ticket.objectFacts", validator, input.ObjectFacts, fact.ScopeObject); err != nil {
			return TicketView{}, err
		}
	}
	if err := adapter.Add(ctx, owner, input.ticket()); err != nil {
		return TicketView{}, err
	}
	view, err := runtime.registry.RecordTicket(owner, decision, input.ticket(), input.ObjectFacts)
	if err != nil {
		// Keep core and observation state aligned if registry insertion fails.
		_, _ = adapter.Remove(context.Background(), owner, input.TicketID)
		return TicketView{}, err
	}
	if runtime.replayLog != nil {
		instance, _ := adapter.TimelineInstanceID(context.Background(), owner.LogicalNode)
		runtime.replayLog.recordFacts(instance, input.TicketID, input.ObjectFacts)
	}
	runtime.appendEvent("ticket_added", map[string]any{
		"ticketId": input.TicketID,
		"owner":    owner.String(),
	})
	return view, nil
}

func (s *Simulator) SetObjectFacts(ctx context.Context, owner identity.OwnerRef, ticketID common.TicketID, values FactSnapshot) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil {
		return ErrSimulatorClosed
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	runtime, err := s.runtimeReadLocked()
	if err != nil {
		return err
	}
	validator := runtime.validators[ownerIndex(owner)]
	if validator == nil {
		return fmt.Errorf("%w: %s", ErrUnknownRule, owner.LogicalNode)
	}
	if err := validateFactSnapshot("ticket.objectFacts", validator, values, fact.ScopeObject); err != nil {
		return err
	}
	if err := runtime.registry.SetObjectFacts(owner, ticketID, values); err != nil {
		return err
	}
	if runtime.replayLog != nil {
		if adapter := runtime.nodes[owner.PhysicalNodeID]; adapter != nil {
			instance, _ := adapter.TimelineInstanceID(context.Background(), owner.LogicalNode)
			runtime.replayLog.recordFacts(instance, ticketID, values)
		}
	}
	return nil
}

func (s *Simulator) GetTicket(ctx context.Context, owner identity.OwnerRef, ticketID common.TicketID) (TicketView, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil {
		return TicketView{}, false, ErrSimulatorClosed
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	runtime, err := s.runtimeReadLocked()
	if err != nil {
		return TicketView{}, false, err
	}
	adapter := runtime.nodes[owner.PhysicalNodeID]
	if adapter == nil {
		return TicketView{}, false, fmt.Errorf("%w: %d", ErrUnknownNode, owner.PhysicalNodeID)
	}
	_, exists, err := adapter.Get(ctx, owner, ticketID)
	if err != nil {
		return TicketView{}, false, err
	}
	view, observed := runtime.registry.GetTicket(owner, ticketID)
	return view, exists && observed, nil
}

// RemoveTicket removes one Ticket from a known owner and its observation.
func (s *Simulator) RemoveTicket(ctx context.Context, owner identity.OwnerRef, ticketID common.TicketID) (bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil {
		return false, ErrSimulatorClosed
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	runtime, err := s.runtimeReadLocked()
	if err != nil {
		return false, err
	}
	return runtime.removeAtOwner(ctx, owner, ticketID, true)
}

// RollbackTicket 删除批量操作中已加入的 Ticket，区别于用户主动撤票。
func (s *Simulator) RollbackTicket(ctx context.Context, owner identity.OwnerRef, ticketID common.TicketID) (bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil {
		return false, ErrSimulatorClosed
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	runtime, err := s.runtimeReadLocked()
	if err != nil {
		return false, err
	}
	return runtime.removeAtOwner(ctx, owner, ticketID, false)
}

func (runtime *simulatorRuntime) removeAtOwner(ctx context.Context, owner identity.OwnerRef, ticketID common.TicketID, withdraw bool) (bool, error) {
	adapter := runtime.nodes[owner.PhysicalNodeID]
	if adapter == nil {
		return false, fmt.Errorf("%w: %d", ErrUnknownNode, owner.PhysicalNodeID)
	}
	var removed bool
	var err error
	if withdraw {
		removed, err = adapter.Withdraw(ctx, owner, ticketID)
	} else {
		removed, err = adapter.Remove(ctx, owner, ticketID)
	}
	if err != nil {
		return false, err
	}
	observed := runtime.registry.RemoveTicket(owner, ticketID)
	if removed || observed {
		runtime.appendEvent("ticket_removed", map[string]any{"ticketId": ticketID, "owner": owner.String()})
	}
	return removed || observed, nil
}

// DeleteTicket finds a unique owner from observations. It rejects ambiguity
// instead of silently deleting a Ticket with the same ID on another node.
func (s *Simulator) DeleteTicket(ctx context.Context, ticketID common.TicketID) (bool, error) {
	if s == nil {
		return false, ErrSimulatorClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	runtime, err := s.runtimeReadLocked()
	if err != nil {
		return false, err
	}
	owners := runtime.registry.OwnersForTicket(ticketID)
	if len(owners) == 0 {
		return false, nil
	}
	if len(owners) != 1 {
		return false, ErrTicketAmbiguous
	}
	return runtime.removeAtOwner(ctx, owners[0], ticketID, true)
}

func (s *Simulator) ListTickets(ctx context.Context, query TicketQuery) (TicketPage, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return TicketPage{}, err
	}
	if s == nil {
		return TicketPage{}, ErrSimulatorClosed
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	runtime, err := s.runtimeReadLocked()
	if err != nil {
		return TicketPage{}, err
	}
	return runtime.registry.ListTickets(query)
}

func (s *Simulator) BeginRound(ctx context.Context, now int64) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil {
		return ErrSimulatorClosed
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	runtime, err := s.runtimeReadLocked()
	if err != nil {
		return err
	}
	return runtime.beginRound(ctx, now)
}

func (runtime *simulatorRuntime) beginRound(ctx context.Context, now int64) error {
	if runtime == nil {
		return ErrSimulatorClosed
	}
	if runtime.round == math.MaxUint64 {
		return fmt.Errorf("simulator round counter overflow")
	}
	ids := sortedRuntimePhysicalIDs(runtime.nodes)
	for _, id := range ids {
		if err := runtime.nodes[id].BeginMatchRound(ctx, now); err != nil {
			return fmt.Errorf("begin round on PhysicalNode %d: %w", id, err)
		}
	}
	runtime.round++
	runtime.roundNow = now
	runtime.roundActive = true
	runtime.appendEvent("round_started", map[string]any{"round": runtime.round, "now": now})
	return nil
}

// ProduceMatch performs one PhysicalNode attempt. The node is selected by
// deterministic physical ID order; use ProduceAll to drain every node.
func (s *Simulator) ProduceMatch(ctx context.Context) (ProduceResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil {
		return ProduceResult{}, ErrSimulatorClosed
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	runtime, err := s.runtimeReadLocked()
	if err != nil {
		return ProduceResult{}, err
	}
	if !runtime.roundActive {
		return ProduceResult{}, ErrRoundNotStarted
	}
	for _, id := range sortedRuntimePhysicalIDs(runtime.nodes) {
		result, produceErr := runtime.produceOne(ctx, id, s.allocateMatchID)
		if errors.Is(produceErr, matchsystem.ErrNoLogicalNodeAvailable) {
			continue
		}
		return result, produceErr
	}
	return ProduceResult{}, matchsystem.ErrNoLogicalNodeAvailable
}

func (s *Simulator) ProduceOne(ctx context.Context, physicalID identity.PhysicalNodeID) (ProduceResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil {
		return ProduceResult{}, ErrSimulatorClosed
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	runtime, err := s.runtimeReadLocked()
	if err != nil {
		return ProduceResult{}, err
	}
	return runtime.produceOne(ctx, physicalID, s.allocateMatchID)
}

// allocateMatchID is called while Simulator.mu is held by the matching
// command. Keeping the sequence on Simulator, rather than simulatorRuntime,
// prevents a successful Scenario replacement from reusing an old Match ID.
func (s *Simulator) allocateMatchID() (string, error) {
	if s == nil {
		return "", ErrSimulatorClosed
	}
	if !s.matchIDInitialized {
		s.nextMatchID = 1
		s.matchIDInitialized = true
	}
	if s.nextMatchID == 0 || s.nextMatchID == math.MaxUint64 {
		return "", fmt.Errorf("simulator match counter overflow")
	}
	matchID := "match-" + strconv.FormatUint(s.nextMatchID, 10)
	s.nextMatchID++
	return matchID, nil
}

func (runtime *simulatorRuntime) produceOne(ctx context.Context, physicalID identity.PhysicalNodeID, allocateMatchID func() (string, error)) (ProduceResult, error) {
	if runtime == nil {
		return ProduceResult{}, ErrSimulatorClosed
	}
	adapter := runtime.nodes[physicalID]
	if adapter == nil {
		return ProduceResult{}, fmt.Errorf("%w: %d", ErrUnknownNode, physicalID)
	}
	// Measure this successful attempt independently using Go's monotonic clock.
	// Excludes round setup, command lock wait and history/HTTP serialization.
	started := time.Now()
	coreResult, err := adapter.ProduceMatch(ctx)
	processingDurationNs := time.Since(started).Nanoseconds()
	result := ProduceResult{PhysicalNodeID: physicalID, LogicalNode: coreResult.LogicalNode}
	if err != nil {
		return result, err
	}
	// A core evaluation may continue with another candidate after a provider
	// error. The worker records terminal failures, so the host can fail the
	// operation even when the core returned a later successful Match.
	if providerErr := runtime.providerFailure(); providerErr != nil {
		return result, providerErr
	}
	if coreResult.Match == nil {
		return result, nil
	}
	if allocateMatchID == nil {
		return result, fmt.Errorf("simulator Match ID allocator is nil")
	}
	owner := identity.OwnerRef{LogicalNode: coreResult.LogicalNode, PhysicalNodeID: physicalID}
	matchID, err := allocateMatchID()
	if err != nil {
		return result, err
	}
	record, err := runtime.registry.CommitMatch(owner, coreResult.Match, matchID, runtime.round, runtime.roundNow, physicalID, coreResult.LogicalNode, processingDurationNs)
	if err != nil {
		return result, err
	}
	runtime.appendEvent("match_created", map[string]any{
		"matchId":      record.ID,
		"round":        record.Round,
		"physicalNode": strconv.FormatUint(uint64(record.PhysicalNodeID), 10),
		"logicalNode":  record.LogicalNode.String(),
	})
	result.Match = &record
	return result, nil
}

// ProduceAll 轮流处理物理节点，每个节点最多产生 maxMatches 局；0 表示不限制局数。
func (s *Simulator) ProduceAll(ctx context.Context, maxMatches int) (RoundResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if maxMatches < 0 {
		return RoundResult{}, fmt.Errorf("maxMatches must not be negative")
	}
	if s == nil {
		return RoundResult{}, ErrSimulatorClosed
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	runtime, err := s.runtimeReadLocked()
	if err != nil {
		return RoundResult{}, err
	}
	if !runtime.roundActive {
		return RoundResult{}, ErrRoundNotStarted
	}
	return runtime.produceAll(ctx, maxMatches, s.allocateMatchID)
}

func (runtime *simulatorRuntime) produceAll(ctx context.Context, maxMatches int, allocateMatchID func() (string, error)) (RoundResult, error) {
	result := RoundResult{Round: runtime.round, Now: runtime.roundNow}
	ids := sortedRuntimePhysicalIDs(runtime.nodes)
	var producedByNode map[identity.PhysicalNodeID]int
	if maxMatches > 0 {
		producedByNode = make(map[identity.PhysicalNodeID]int, len(ids))
	}
	for {
		progress := false
		for _, id := range ids {
			if maxMatches > 0 && producedByNode[id] >= maxMatches {
				continue
			}
			attempt, err := runtime.produceOne(ctx, id, allocateMatchID)
			if errors.Is(err, matchsystem.ErrNoLogicalNodeAvailable) {
				continue
			}
			if err != nil {
				return result, err
			}
			result.Attempts++
			progress = true
			if attempt.Match != nil {
				result.Matches = append(result.Matches, *attempt.Match)
				if maxMatches > 0 {
					producedByNode[id]++
				}
			}
		}
		if !progress {
			return result, nil
		}
	}
}

func (s *Simulator) RunRound(ctx context.Context, now int64, maxMatches int) (RoundResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil {
		return RoundResult{}, ErrSimulatorClosed
	}
	if err := ctx.Err(); err != nil {
		return RoundResult{}, err
	}
	if maxMatches < 0 {
		return RoundResult{}, fmt.Errorf("maxMatches must not be negative")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	runtime, err := s.runtimeReadLocked()
	if err != nil {
		return RoundResult{}, err
	}
	if err := runtime.beginRound(ctx, now); err != nil {
		return RoundResult{}, err
	}
	return runtime.produceAll(ctx, maxMatches, s.allocateMatchID)
}

// Run is an alias for RunRound for hosts that model the simulator as a loop.
func (s *Simulator) Run(ctx context.Context, now int64, maxMatches int) (RoundResult, error) {
	return s.RunRound(ctx, now, maxMatches)
}

func (s *Simulator) ListMatches(ctx context.Context, query MatchQuery) (MatchPage, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return MatchPage{}, err
	}
	if s == nil {
		return MatchPage{}, ErrSimulatorClosed
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	runtime, err := s.runtimeReadLocked()
	if err != nil {
		return MatchPage{}, err
	}
	return runtime.registry.ListMatches(query)
}

// GetMatch returns one retained, detached Match snapshot. Match history is
// owned by the active runtime, so replacing a Scenario deliberately makes
// records from the previous Scenario unavailable.
func (s *Simulator) GetMatch(ctx context.Context, matchID string) (MatchRecord, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return MatchRecord{}, false, err
	}
	if strings.TrimSpace(matchID) == "" {
		return MatchRecord{}, false, ErrInvalidMatchID
	}
	if s == nil {
		return MatchRecord{}, false, ErrSimulatorClosed
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	runtime, err := s.runtimeReadLocked()
	if err != nil {
		return MatchRecord{}, false, err
	}
	record, ok := runtime.registry.GetMatch(matchID)
	return record, ok, nil
}

// Match is a concise alias for GetMatch used by hosts that model Match as a
// first-class observation resource.
func (s *Simulator) Match(ctx context.Context, matchID string) (MatchRecord, bool, error) {
	return s.GetMatch(ctx, matchID)
}

func (s *Simulator) Matches(ctx context.Context, query MatchQuery) (MatchPage, error) {
	return s.ListMatches(ctx, query)
}

func (s *Simulator) Topology(ctx context.Context) (TopologySnapshot, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return TopologySnapshot{}, err
	}
	if s == nil {
		return TopologySnapshot{}, ErrSimulatorClosed
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	runtime, err := s.runtimeReadLocked()
	if err != nil {
		return TopologySnapshot{}, err
	}
	result := TopologySnapshot{PhysicalNodes: make([]PhysicalNodeView, 0, len(runtime.physical))}
	for _, id := range sortedRuntimePhysicalIDs(runtime.nodes) {
		spec := runtime.physical[id]
		descriptors, err := runtime.nodes[id].Describe(ctx)
		if err != nil {
			return TopologySnapshot{}, err
		}
		result.PhysicalNodes = append(result.PhysicalNodes, PhysicalNodeView{
			ID:                    spec.ID,
			Endpoint:              spec.Endpoint,
			Enabled:               spec.Enabled,
			RouteUpdateIntervalMS: effectiveRouteUpdateIntervalMS(spec.RouteUpdateIntervalMS),
			LogicalNodes:          descriptors,
		})
	}
	return result, nil
}

func (s *Simulator) GetTopology(ctx context.Context) (TopologySnapshot, error) {
	return s.Topology(ctx)
}

// FactSpecs returns an owned snapshot of the complete Fact metadata declared
// by one LogicalNode. LogicalNode identity is global within a Scenario; the
// simulator resolves its PhysicalNode owner internally and serializes the
// query through that owner's worker goroutine.
func (s *Simulator) FactSpecs(ctx context.Context, key identity.LogicalNodeKey) ([]matchsystem.FactSpec, error) {
	metadata, err := s.FactMetadata(ctx, key)
	if err != nil {
		return nil, err
	}
	return append([]matchsystem.FactSpec(nil), metadata.ContractFacts...), nil
}

// FactMetadata returns detached LogicalNode Fact metadata while keeping the
// rule-side Contract declarations, provider-side handshake declarations, and
// simulator-owned runtime Tick values as separate fields. No descriptor is
// synthesized from the Contract or from runtime values.
func (s *Simulator) FactMetadata(ctx context.Context, key identity.LogicalNodeKey) (LogicalNodeFactMetadata, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return LogicalNodeFactMetadata{}, err
	}
	if err := key.Validate(); err != nil {
		return LogicalNodeFactMetadata{}, err
	}
	if s == nil {
		return LogicalNodeFactMetadata{}, ErrSimulatorClosed
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	runtime, err := s.runtimeReadLocked()
	if err != nil {
		return LogicalNodeFactMetadata{}, err
	}
	rule, ok := runtime.rules[logicalIndex(key)]
	if !ok {
		return LogicalNodeFactMetadata{}, ErrUnknownRule
	}
	adapter := runtime.nodes[rule.PhysicalNodeID]
	if adapter == nil {
		return LogicalNodeFactMetadata{}, ErrUnknownNode
	}
	contractFacts, err := adapter.FactSpecs(ctx, key)
	if err != nil {
		return LogicalNodeFactMetadata{}, err
	}
	var descriptors matchsystem.ProviderDescriptorSet
	if captured, exists := runtime.providerDescriptors[logicalIndex(key)]; exists {
		descriptors = captured.Clone()
	}
	tickDescriptor := cloneProviderDescriptor(descriptors.Tick)
	objectDescriptor := cloneProviderDescriptor(descriptors.Object)
	matchDescriptor := cloneProviderDescriptor(descriptors.Match)
	descriptorSources := runtime.providerDescriptorSources[logicalIndex(key)]
	return LogicalNodeFactMetadata{
		LogicalNode:                    rule.LogicalNode,
		ContractFacts:                  append([]matchsystem.FactSpec(nil), contractFacts...),
		TickProviderDescriptor:         tickDescriptor,
		ObjectProviderDescriptor:       objectDescriptor,
		MatchProviderDescriptor:        matchDescriptor,
		TickProviderDescriptorSource:   descriptorSources.Tick,
		ObjectProviderDescriptorSource: descriptorSources.Object,
		MatchProviderDescriptorSource:  descriptorSources.Match,
		RuntimeTickFacts:               rule.TickFacts.clone(),
	}, nil
}

// GetFactSpecs is the context-aware alias used by application integrations
// that prefer a getter-style name.
func (s *Simulator) GetFactSpecs(ctx context.Context, key identity.LogicalNodeKey) ([]matchsystem.FactSpec, error) {
	return s.FactSpecs(ctx, key)
}

// GetFactMetadata is the getter-style alias for integrations that need both
// the independent provider declarations and simulator runtime values.
func (s *Simulator) GetFactMetadata(ctx context.Context, key identity.LogicalNodeKey) (LogicalNodeFactMetadata, error) {
	return s.FactMetadata(ctx, key)
}

func (s *Simulator) Events(ctx context.Context, query EventQuery) (EventPage, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return EventPage{}, err
	}
	if s == nil {
		return EventPage{}, ErrSimulatorClosed
	}
	start, limit, err := pageBounds(query.Cursor, query.Limit)
	if err != nil {
		return EventPage{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	runtime, err := s.runtimeReadLocked()
	if err != nil {
		return EventPage{}, err
	}
	total := len(runtime.events)
	if start > total {
		start = total
	}
	end := total
	if start < total && limit < total-start {
		end = start + limit
	}
	items := make([]Event, 0, pageItemCapacity(start, total, limit))
	for _, event := range runtime.events[start:end] {
		items = append(items, cloneEvent(event))
	}
	return EventPage{Items: items, NextCursor: nextCursor(start, limit, total), Total: total}, nil
}

// SubscribeEvents returns a live, buffered stream. Existing events after the
// cursor are sent before new events; replacement or Close closes subscribers
// belonging to the retired runtime.
func (s *Simulator) SubscribeEvents(ctx context.Context, query EventQuery) (<-chan Event, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	start, limit, err := pageBounds(query.Cursor, query.Limit)
	if err != nil {
		return nil, err
	}
	if s == nil {
		return nil, ErrSimulatorClosed
	}
	s.mu.Lock()
	runtime, err := s.runtimeReadLocked()
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}
	if start > len(runtime.events) {
		start = len(runtime.events)
	}
	historyCount := len(runtime.events) - start
	subscription := &eventSubscription{ch: make(chan Event, maxInt(16, historyCount+limit))}
	for _, event := range runtime.events[start:] {
		subscription.ch <- cloneEvent(event)
	}
	if runtime.subscribers == nil {
		runtime.subscribers = make(map[*eventSubscription]struct{})
	}
	runtime.subscribers[subscription] = struct{}{}
	s.mu.Unlock()
	go func() {
		<-ctx.Done()
		s.mu.Lock()
		if current := s.runtime; current != nil {
			delete(current.subscribers, subscription)
		}
		// If the runtime was replaced, closeRuntime owns the same once; if
		// it is current, this cancellation path closes it here.
		subscription.close()
		s.mu.Unlock()
	}()
	return subscription.ch, nil
}

func (runtime *simulatorRuntime) appendEvent(eventType string, data map[string]any) {
	if runtime == nil {
		return
	}
	sequence := runtime.nextEvent
	if sequence == 0 {
		sequence = 1
	}
	runtime.nextEvent = sequence + 1
	copyData := make(map[string]any, len(data))
	for key, value := range data {
		copyData[key] = value
	}
	event := Event{Sequence: sequence, Type: eventType, Data: copyData}
	runtime.events = append(runtime.events, event)
	for subscription := range runtime.subscribers {
		select {
		case subscription.ch <- cloneEvent(event):
		default:
			// Subscribers are observation clients. A slow client must not
			// block matching; it can reconnect with the last event cursor.
		}
	}
}

func cloneEvent(event Event) Event {
	out := event
	out.Data = make(map[string]any, len(event.Data))
	for key, value := range event.Data {
		out.Data[key] = value
	}
	return out
}

func sortedRuntimePhysicalIDs(nodes map[identity.PhysicalNodeID]*physicalNodeAdapter) []identity.PhysicalNodeID {
	ids := make([]identity.PhysicalNodeID, 0, len(nodes))
	for id := range nodes {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// Close stops every owner goroutine and makes the simulator unusable. It is
// idempotent and does not mutate the previously returned detached snapshots.
func (s *Simulator) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.stopTrafficLocked("stopped")
	old := s.runtime
	stopRouteTimersLocked(old)
	s.runtime = nil
	s.mu.Unlock()
	closeRuntime(old)
	return nil
}

// Health reports a small transport-neutral liveness value for API adapters.
type HealthStatus struct {
	Status  string `json:"status"`
	Service string `json:"service"`
}

func (s *Simulator) Health(ctx context.Context) (HealthStatus, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return HealthStatus{}, err
	}
	if s == nil {
		return HealthStatus{}, ErrSimulatorClosed
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed || s.runtime == nil {
		return HealthStatus{}, ErrSimulatorClosed
	}
	return HealthStatus{Status: "ok", Service: "simulator"}, nil
}

// Ensure the public runtime-facing ports remain assignable to their intended
// collaborators without importing simulator from matchsystem.

// Keep time imported for hosts that pass a zero timestamp to RunRound through
// the optional helper below.
func (s *Simulator) RunRoundNow(ctx context.Context, maxMatches int) (RoundResult, error) {
	return s.RunRound(ctx, time.Now().UnixMilli(), maxMatches)
}
