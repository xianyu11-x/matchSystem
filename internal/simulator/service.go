package simulator

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"sync"
	"time"

	"matchSystem/internal/client"
	"matchSystem/internal/common"
	"matchSystem/internal/identity"
	"matchSystem/internal/matchsystem"
	"matchSystem/internal/matchsystem/contract"
	"matchSystem/internal/matchsystem/fact"
)

var (
	ErrSimulatorClosed  = errors.New("simulator is closed")
	ErrRoundNotStarted  = errors.New("simulator round has not started")
	ErrUnknownNode      = errors.New("simulator PhysicalNode is not present")
	ErrUnknownRule      = errors.New("simulator LogicalNode is not present")
	ErrInvalidTicket    = errors.New("invalid simulator ticket")
	ErrInvalidBatchSpec = errors.New("invalid simulator batch generator specification")
)

// Simulator is the application/runtime boundary around one or more
// matchsystem.PhysicalNode instances. Its mutex serializes route-table access,
// runtime swaps, and cross-node commands; each PhysicalNode has an additional
// owner goroutine in physicalNodeAdapter.
type Simulator struct {
	mu           sync.RWMutex
	runtime      *simulatorRuntime
	capabilities Capabilities
	closed       bool
}

// Application is a semantic alias: the simulator runtime is the application
// object exposed to an HTTP host.
type Application = Simulator

// Runtime is a semantic alias for code that integrates the core as a runtime
// rather than as a visual simulation service.
type Runtime = Simulator

type simulatorRuntime struct {
	scenario   Scenario
	router     *client.Router
	nodes      map[identity.PhysicalNodeID]*physicalNodeAdapter
	physical   map[identity.PhysicalNodeID]PhysicalNodeSpec
	rules      map[identity.LogicalNodeKey]RuleSpec
	validators map[identity.OwnerRef]*fact.Validator
	registry   *ObservationRegistry

	roundActive bool
	round       uint64
	roundNow    int64
	nextMatch   uint64
	nextEvent   uint64
	events      []Event
	subscribers map[*eventSubscription]struct{}
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
	runtime, err := buildRuntime(scenario)
	if err != nil {
		return nil, err
	}
	return &Simulator{runtime: runtime, capabilities: defaultCapabilities()}, nil
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

func buildRuntime(input Scenario) (*simulatorRuntime, error) {
	scenario := input.Clone()
	if scenario.SchemaVersion == "" {
		scenario.SchemaVersion = ScenarioSchemaVersion
	}
	for index := range scenario.PhysicalNodes {
		scenario.PhysicalNodes[index].Selector = normalizeSelector(scenario.PhysicalNodes[index].Selector)
	}
	if report := ValidateScenario(scenario); !report.Valid {
		return nil, report.Err()
	}

	runtime := &simulatorRuntime{
		scenario:    scenario,
		nodes:       make(map[identity.PhysicalNodeID]*physicalNodeAdapter, len(scenario.PhysicalNodes)),
		physical:    make(map[identity.PhysicalNodeID]PhysicalNodeSpec, len(scenario.PhysicalNodes)),
		rules:       make(map[identity.LogicalNodeKey]RuleSpec, len(scenario.Rules)),
		validators:  make(map[identity.OwnerRef]*fact.Validator, len(scenario.Rules)),
		registry:    NewObservationRegistry(),
		nextMatch:   1,
		nextEvent:   1,
		subscribers: make(map[*eventSubscription]struct{}),
	}
	cleanup := true
	defer func() {
		if cleanup {
			closeRuntime(runtime)
		}
	}()

	for _, physicalSpec := range scenario.PhysicalNodes {
		runtime.physical[physicalSpec.ID] = physicalSpec
		selector, err := selectorForPhysical(physicalSpec.ID, physicalSpec.Selector, scenario.Rules)
		if err != nil {
			return nil, err
		}
		core, err := matchsystem.NewPhysicalNode(physicalSpec.ID, matchsystem.WithLogicalNodeSelector(selector))
		if err != nil {
			return nil, fmt.Errorf("create PhysicalNode %s: %w", physicalSpec.ID, err)
		}
		adapter, err := newPhysicalNodeAdapter(core)
		if err != nil {
			return nil, err
		}
		runtime.nodes[physicalSpec.ID] = adapter
	}

	for _, rule := range scenario.Rules {
		owner := identity.OwnerRef{LogicalNode: rule.LogicalNode, PhysicalNodeID: rule.PhysicalNodeID}
		compiled, err := matchsystem.CompileRuleJSON(rule.RuleJSON)
		if err != nil {
			return nil, fmt.Errorf("compile Rule JSON for %s: %w", rule.LogicalNode, err)
		}
		schema := compiled.Contract()
		validator, err := fact.NewValidator(schema.FactSpecs())
		if err != nil {
			return nil, fmt.Errorf("compile Fact validator for %s: %w", rule.LogicalNode, err)
		}
		runtime.validators[owner] = validator
		runtime.rules[rule.LogicalNode] = rule
		spec := runtimeLogicalNodeSpec(rule, schema, runtime.registry, owner, validator)
		adapter := runtime.nodes[rule.PhysicalNodeID]
		if adapter == nil {
			return nil, fmt.Errorf("%w: %s", ErrUnknownNode, rule.PhysicalNodeID)
		}
		if err := adapter.Load(context.Background(), spec); err != nil {
			return nil, fmt.Errorf("load LogicalNode %s on PhysicalNode %s: %w", rule.LogicalNode, rule.PhysicalNodeID, err)
		}
	}

	physicalRoutes := make([]client.PhysicalRoute, 0, len(scenario.PhysicalNodes))
	for _, node := range scenario.PhysicalNodes {
		physicalRoutes = append(physicalRoutes, client.PhysicalRoute{
			PhysicalNodeID: node.ID,
			Endpoint:       node.Endpoint,
			Enabled:        node.Enabled,
		})
	}
	ruleRoutes := make([]client.RuleRoute, 0, len(scenario.Rules))
	for _, rule := range scenario.Rules {
		ruleRoutes = append(ruleRoutes, client.RuleRoute{
			LogicalNode:    rule.LogicalNode,
			PhysicalNodeID: rule.PhysicalNodeID,
			Weight:         rule.Weight,
			Enabled:        rule.Enabled,
		})
	}
	routeTable, err := client.NewRouteTable(client.RouteTableConfig{PhysicalNodes: physicalRoutes, Rules: ruleRoutes})
	if err != nil {
		return nil, fmt.Errorf("build simulator route table: %w", err)
	}
	runtime.router, err = client.NewRouter(routeTable)
	if err != nil {
		return nil, err
	}
	cleanup = false
	return runtime, nil
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
	var tickProvider matchsystem.FactProvider
	if rule.FactProvider != nil {
		tickProvider = validatingTickProvider(rule.FactProvider, validator)
	} else {
		static := rule.TickFacts.clone()
		tickProvider = validatingTickProvider(func(context.Context, int64) (matchsystem.Facts, error) {
			return static.values(), nil
		}, validator)
	}

	objectProvider := rule.ObjectFactProvider
	if objectProvider == nil {
		objectProvider = func(object *common.Ticket, _ int64, _ matchsystem.Facts) (matchsystem.Facts, error) {
			if object == nil {
				return matchsystem.Facts{}, fmt.Errorf("object Ticket is nil")
			}
			if values, ok := registry.ObjectFacts(owner, object.TicketID); ok {
				return values, nil
			}
			return matchsystem.Facts{}, nil
		}
	}
	objectProvider = validatingObjectProvider(objectProvider, validator)

	matchProvider := rule.MatchFactProvider
	if matchProvider == nil && hasMatchFacts(schema.Facts) {
		matchProvider = defaultMatchFactProvider{specs: schema.Facts}
	}
	if matchProvider != nil {
		matchProvider = validatingMatchFactProvider{provider: matchProvider, validator: validator}
	}

	return matchsystem.LogicalNodeSpec{
		Key:                rule.LogicalNode,
		RuleJSON:           append([]byte(nil), rule.RuleJSON...),
		FactProvider:       tickProvider,
		ObjectFactProvider: objectProvider,
		MatchFactProvider:  matchProvider,
	}
}

func validatingTickProvider(provider matchsystem.FactProvider, validator *fact.Validator) matchsystem.FactProvider {
	if provider == nil {
		return nil
	}
	return func(ctx context.Context, now int64) (matchsystem.Facts, error) {
		values, err := provider(ctx, now)
		if err != nil {
			return matchsystem.Facts{}, err
		}
		if validator != nil {
			if _, err := validator.ValidateLayer("facts.tick", values, fact.ScopeTick); err != nil {
				return matchsystem.Facts{}, err
			}
		}
		return fact.Clone(values), nil
	}
}

func validatingObjectProvider(provider matchsystem.ObjectFactProvider, validator *fact.Validator) matchsystem.ObjectFactProvider {
	if provider == nil {
		return nil
	}
	return func(object *common.Ticket, now int64, tick matchsystem.Facts) (matchsystem.Facts, error) {
		values, err := provider(object, now, tick)
		if err != nil {
			return matchsystem.Facts{}, err
		}
		if validator != nil {
			if _, err := validator.ValidateLayer("facts.object", values, fact.ScopeObject); err != nil {
				return matchsystem.Facts{}, err
			}
		}
		return fact.Clone(values), nil
	}
}

type validatingMatchFactProvider struct {
	provider  matchsystem.MatchFactProvider
	validator *fact.Validator
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
			values.Int64Values[spec.Name]++
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
	for subscription := range runtime.subscribers {
		subscription.close()
	}
	runtime.subscribers = nil
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
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	next, err := buildRuntime(scenario)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		closeRuntime(next)
		return err
	}
	if s == nil {
		closeRuntime(next)
		return ErrSimulatorClosed
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		closeRuntime(next)
		return ErrSimulatorClosed
	}
	old := s.runtime
	s.runtime = next
	s.mu.Unlock()
	closeRuntime(old)
	return nil
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
	result, err := s.AddTicketWithDecision(ctx, input)
	if err != nil {
		return TicketView{}, err
	}
	return result.Ticket, nil
}

func (s *Simulator) AddTicketWithDecision(ctx context.Context, input TicketInput) (AddTicketResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil {
		return AddTicketResult{}, ErrSimulatorClosed
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	runtime, err := s.runtimeReadLocked()
	if err != nil {
		return AddTicketResult{}, err
	}
	decision, err := runtime.router.RouteNew(ctx, input.routeRequest())
	if err != nil {
		return AddTicketResult{}, err
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
	resolved, err := runtime.router.ResolveOwner(decision.Owner)
	if err != nil {
		return TicketView{}, err
	}
	physicalSpec, physicalExists := runtime.physical[decision.Owner.PhysicalNodeID]
	ruleSpec, ruleExists := runtime.rules[decision.Owner.LogicalNode]
	if !physicalExists || !ruleExists || !physicalSpec.Enabled || !ruleSpec.Enabled {
		return TicketView{}, client.ErrNoRoute
	}
	if resolved.Endpoint != decision.Endpoint || decision.DecisionID == "" {
		// A decision may be reconstructed by a caller that only retained the
		// OwnerRef; preserve the route-table endpoint but reject stale or
		// malformed decisions with a different endpoint.
		if decision.Endpoint != "" && resolved.Endpoint != decision.Endpoint {
			return TicketView{}, fmt.Errorf("route decision endpoint does not match OwnerRef")
		}
		decision.Endpoint = resolved.Endpoint
	}
	returnView, err := runtime.addAtOwner(ctx, decision, input)
	return returnView.Ticket, err
}

func (runtime *simulatorRuntime) addAtOwner(ctx context.Context, decision common.RouteDecision, input TicketInput) (AddTicketResult, error) {
	if runtime == nil {
		return AddTicketResult{}, ErrSimulatorClosed
	}
	if input.TicketID == 0 || input.Rule.Validate() != nil {
		return AddTicketResult{}, ErrInvalidTicket
	}
	owner := decision.Owner
	if err := owner.Validate(); err != nil {
		return AddTicketResult{}, err
	}
	if owner.LogicalNode.Rule != input.Rule {
		return AddTicketResult{}, fmt.Errorf("route decision Rule %s does not match Ticket Rule %s", owner.LogicalNode.Rule, input.Rule)
	}
	adapter := runtime.nodes[owner.PhysicalNodeID]
	if adapter == nil {
		return AddTicketResult{}, fmt.Errorf("%w: %s", ErrUnknownNode, owner.PhysicalNodeID)
	}
	if _, exists := runtime.rules[owner.LogicalNode]; !exists {
		return AddTicketResult{}, fmt.Errorf("%w: %s", ErrUnknownRule, owner.LogicalNode)
	}
	if _, exists := runtime.registry.GetTicket(owner, input.TicketID); exists {
		return AddTicketResult{}, ErrTicketAlreadyObserved
	}
	validator := runtime.validators[owner]
	if validator != nil {
		if err := validateFactSnapshot("ticket.objectFacts", validator, input.ObjectFacts, fact.ScopeObject); err != nil {
			return AddTicketResult{}, err
		}
	}
	if _, err := adapter.Add(ctx, owner, input.ticket()); err != nil {
		return AddTicketResult{}, err
	}
	view, err := runtime.registry.RecordTicket(owner, decision, input.ticket(), input.ObjectFacts)
	if err != nil {
		// Keep core and observation state aligned if registry insertion fails.
		_, _ = adapter.Remove(context.Background(), owner, input.TicketID)
		return AddTicketResult{}, err
	}
	runtime.appendEvent("ticket_added", map[string]any{
		"ticketId": input.TicketID,
		"owner":    owner.String(),
	})
	return AddTicketResult{Ticket: view, Decision: decision}, nil
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
	validator := runtime.validators[owner]
	if validator == nil {
		return fmt.Errorf("%w: %s", ErrUnknownRule, owner.LogicalNode)
	}
	if err := validateFactSnapshot("ticket.objectFacts", validator, values, fact.ScopeObject); err != nil {
		return err
	}
	return runtime.registry.SetObjectFacts(owner, ticketID, values)
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
		return TicketView{}, false, fmt.Errorf("%w: %s", ErrUnknownNode, owner.PhysicalNodeID)
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
	return runtime.removeAtOwner(ctx, owner, ticketID)
}

func (runtime *simulatorRuntime) removeAtOwner(ctx context.Context, owner identity.OwnerRef, ticketID common.TicketID) (bool, error) {
	adapter := runtime.nodes[owner.PhysicalNodeID]
	if adapter == nil {
		return false, fmt.Errorf("%w: %s", ErrUnknownNode, owner.PhysicalNodeID)
	}
	removed, err := adapter.Remove(ctx, owner, ticketID)
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
	return runtime.removeAtOwner(ctx, owners[0], ticketID)
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
			return fmt.Errorf("begin round on PhysicalNode %s: %w", id, err)
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
		result, produceErr := runtime.produceOne(ctx, id)
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
	return runtime.produceOne(ctx, physicalID)
}

func (runtime *simulatorRuntime) produceOne(ctx context.Context, physicalID identity.PhysicalNodeID) (ProduceResult, error) {
	if runtime == nil {
		return ProduceResult{}, ErrSimulatorClosed
	}
	adapter := runtime.nodes[physicalID]
	if adapter == nil {
		return ProduceResult{}, fmt.Errorf("%w: %s", ErrUnknownNode, physicalID)
	}
	coreResult, err := adapter.ProduceMatch(ctx)
	result := ProduceResult{PhysicalNodeID: physicalID, LogicalNode: coreResult.LogicalNode}
	if err != nil {
		return result, err
	}
	if coreResult.Match == nil {
		return result, nil
	}
	if runtime.nextMatch == math.MaxUint64 {
		return result, fmt.Errorf("simulator match counter overflow")
	}
	owner := identity.OwnerRef{LogicalNode: coreResult.LogicalNode, PhysicalNodeID: physicalID}
	matchID := "match-" + strconv.FormatUint(runtime.nextMatch, 10)
	runtime.nextMatch++
	record, err := runtime.registry.CommitMatch(owner, coreResult.Match, matchID, runtime.round, runtime.roundNow, physicalID, coreResult.LogicalNode)
	if err != nil {
		return result, err
	}
	runtime.appendEvent("match_created", map[string]any{
		"matchId":      record.ID,
		"round":        record.Round,
		"physicalNode": string(record.PhysicalNodeID),
		"logicalNode":  record.LogicalNode.String(),
	})
	result.Match = &record
	return result, nil
}

// ProduceAll drains each PhysicalNode until no logical node has an untried
// seed. maxMatches <= 0 means no result-count limit.
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
	return runtime.produceAll(ctx, maxMatches)
}

func (runtime *simulatorRuntime) produceAll(ctx context.Context, maxMatches int) (RoundResult, error) {
	result := RoundResult{Round: runtime.round, Now: runtime.roundNow}
	ids := sortedRuntimePhysicalIDs(runtime.nodes)
	for {
		progress := false
		for _, id := range ids {
			attempt, err := runtime.produceOne(ctx, id)
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
				if maxMatches > 0 && len(result.Matches) >= maxMatches {
					return result, nil
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
	return runtime.produceAll(ctx, maxMatches)
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
			ID:           spec.ID,
			Endpoint:     spec.Endpoint,
			Enabled:      spec.Enabled,
			LogicalNodes: descriptors,
		})
	}
	return result, nil
}

func (s *Simulator) GetTopology(ctx context.Context) (TopologySnapshot, error) {
	return s.Topology(ctx)
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
	old := s.runtime
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
var _ NodePort = (*physicalNodeAdapter)(nil)
var _ RoutePort = (*Simulator)(nil)

// Keep time imported for hosts that pass a zero timestamp to RunRound through
// the optional helper below.
func (s *Simulator) RunRoundNow(ctx context.Context, maxMatches int) (RoundResult, error) {
	return s.RunRound(ctx, time.Now().UnixMilli(), maxMatches)
}
