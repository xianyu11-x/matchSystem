package simulatorapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"matchSystem/internal/client"
	"matchSystem/internal/common"
	"matchSystem/internal/identity"
	"matchSystem/internal/matchsystem"
	"matchSystem/internal/simulator"
)

// SimulatorAdapter is the explicit seam between the in-process simulator
// application and the HTTP transport.  It converts runtime identities,
// typed-value snapshots, timestamps, and observation records into the raw
// JSON shape consumed by apps/web.
type SimulatorAdapter struct {
	runtime      *simulator.Simulator
	pollInterval time.Duration
}

func NewSimulatorAdapter(runtime *simulator.Simulator) *SimulatorAdapter {
	return &SimulatorAdapter{runtime: runtime, pollInterval: 100 * time.Millisecond}
}

// NewAdapter is the concise constructor used by command hosts and tests.
func NewAdapter(runtime *simulator.Simulator) *SimulatorAdapter {
	return NewSimulatorAdapter(runtime)
}

var _ Service = (*SimulatorAdapter)(nil)
var _ LogicalNodeFactService = (*SimulatorAdapter)(nil)
var _ MatchDetailService = (*SimulatorAdapter)(nil)

// MaxWireTicketID is the largest TicketID that can safely cross a JSON
// number boundary into JavaScript. The simulator core intentionally retains
// the full uint64 domain; this limit applies only to the HTTP wire contract.
const MaxWireTicketID uint64 = 9007199254740991

// unixNanosecondThreshold distinguishes ordinary Unix-millisecond timestamps
// (currently around 1e12) from the legacy Dashboard value expressed in Unix
// nanoseconds (currently around 1e18). The public API is milliseconds; the
// compatibility conversion is kept at this HTTP boundary so the simulator
// registry always compares timestamps in one unit.
const unixNanosecondThreshold int64 = 1_000_000_000_000_000

func roundNowMillis(now int64) int64 {
	if now >= unixNanosecondThreshold || now <= -unixNanosecondThreshold {
		return now / int64(time.Millisecond)
	}
	return now
}

func (a *SimulatorAdapter) Health(ctx context.Context) (HealthResponse, error) {
	if err := a.check(); err != nil {
		return HealthResponse{}, err
	}
	status, err := a.runtime.Health(ctx)
	if err != nil {
		return HealthResponse{}, adaptRuntimeError(err)
	}
	return HealthResponse{Status: status.Status, Service: status.Service}, nil
}

func (a *SimulatorAdapter) Capabilities(ctx context.Context) (CapabilitiesResponse, error) {
	if err := a.check(); err != nil {
		return CapabilitiesResponse{}, err
	}
	capabilities, err := a.runtime.Capabilities(ctx)
	if err != nil {
		return CapabilitiesResponse{}, adaptRuntimeError(err)
	}
	versions := uniqueStrings([]string{
		capabilities.ScenarioSchemaVersion,
		capabilities.RuleSchemaVersion,
		capabilities.ContractSchemaVersion,
		capabilities.ExpressionSchemaVersion,
		capabilities.PrefilterSchemaVersion,
		capabilities.EvaluationSchemaVersion,
	})
	return CapabilitiesResponse{
		SchemaVersions:   versions,
		Selectors:        append([]string(nil), capabilities.Selectors...),
		CandidateScorers: append([]string(nil), capabilities.CandidateScorers...),
		SeedSelections:   append([]string(nil), capabilities.SeedSelections...),
		FactTypes:        append([]string(nil), capabilities.FactTypes...),
		ExpressionOps:    append([]string(nil), capabilities.ExpressionOps...),
		BitmapOps:        append([]string(nil), capabilities.BitmapOps...),
		IndexTypes:       append([]string(nil), capabilities.IndexTypes...),
		FactScopes:       append([]string(nil), capabilities.FactScopes...),
		ScalarOperators:  cloneOperatorCapabilities(capabilities.ScalarOperators),
		BitmapOperators:  cloneOperatorCapabilities(capabilities.BitmapOperators),
		Limits:           map[string]int{},
	}, nil
}

func (a *SimulatorAdapter) GetScenario(ctx context.Context) (ScenarioResponse, error) {
	if err := a.check(); err != nil {
		return ScenarioResponse{}, err
	}
	scenario, err := a.runtime.GetScenario(ctx)
	if err != nil {
		return ScenarioResponse{}, adaptRuntimeError(err)
	}
	return scenarioResponse(scenario)
}

func (a *SimulatorAdapter) ReplaceScenario(ctx context.Context, request ScenarioRequest) (ScenarioResponse, error) {
	if err := a.check(); err != nil {
		return ScenarioResponse{}, err
	}
	if err := requireJSONObject(request.Scenario, "scenario"); err != nil {
		return ScenarioResponse{}, err
	}
	var scenario simulator.Scenario
	if err := json.Unmarshal(request.Scenario, &scenario); err != nil {
		return ScenarioResponse{}, &ServiceError{Status: 400, Code: "INVALID_SCENARIO", Message: "scenario is invalid JSON", Details: err.Error(), Err: err}
	}
	committed, err := a.runtime.ReplaceScenarioAndGet(ctx, scenario)
	if err != nil {
		return ScenarioResponse{}, adaptRuntimeError(err)
	}
	// ReplaceScenarioAndGet captures this exact committed runtime while holding
	// the publication lock. The response therefore remains tied to this
	// request even if another replacement wins immediately afterwards, and it
	// does not consult the request context after the swap.
	return scenarioResponse(committed)
}

func (a *SimulatorAdapter) ValidateRule(ctx context.Context, request ValidateRuleRequest) (ValidationResponse, error) {
	if err := a.check(); err != nil {
		return ValidationResponse{}, err
	}
	if err := ctxErr(ctx); err != nil {
		return ValidationResponse{}, err
	}
	report := simulator.ValidateRuleJSON(request.Rule)
	response := ValidationResponse{Valid: report.Valid, Fingerprint: report.Fingerprint, Issues: make([]ValidationIssue, 0, len(report.Issues))}
	for _, issue := range report.Issues {
		response.Issues = append(response.Issues, ValidationIssue{Path: issue.Path, Code: issue.Code, Message: issue.Message})
	}
	return response, nil
}

func (a *SimulatorAdapter) Topology(ctx context.Context) (TopologyResponse, error) {
	if err := a.check(); err != nil {
		return TopologyResponse{}, err
	}
	topology, err := a.runtime.Topology(ctx)
	if err != nil {
		return TopologyResponse{}, adaptRuntimeError(err)
	}
	response := TopologyResponse{PhysicalNodes: make([]PhysicalNodeStatus, 0, len(topology.PhysicalNodes))}
	for _, physical := range topology.PhysicalNodes {
		node := PhysicalNodeStatus{
			PhysicalNodeID: string(physical.ID),
			Endpoint:       string(physical.Endpoint),
			Enabled:        physical.Enabled,
			LogicalNodes:   make([]LogicalStatus, 0, len(physical.LogicalNodes)),
		}
		for _, logical := range physical.LogicalNodes {
			node.LogicalNodes = append(node.LogicalNodes, LogicalStatus{
				Key:         wirePlacementKey(logical.Key),
				State:       logical.State,
				TicketCount: logical.TicketCount,
			})
		}
		response.PhysicalNodes = append(response.PhysicalNodes, node)
	}
	return response, nil
}

func (a *SimulatorAdapter) GetLogicalNodeFacts(ctx context.Context, query LogicalNodeFactsQuery) (LogicalNodeFactsResponse, error) {
	if err := a.check(); err != nil {
		return LogicalNodeFactsResponse{}, err
	}
	if query.RuleID <= 0 {
		return LogicalNodeFactsResponse{}, &ServiceError{Status: 400, Code: "INVALID_QUERY", Message: "ruleId must be a positive integer", Path: "ruleId"}
	}
	if strings.TrimSpace(query.PlacementID) == "" {
		return LogicalNodeFactsResponse{}, &ServiceError{Status: 400, Code: "INVALID_QUERY", Message: "placementId is required", Path: "placementId"}
	}
	rule := identity.RuleKey{Namespace: query.RuleNamespace, RuleID: query.RuleID}
	if rule.Namespace == "" {
		resolved, err := a.resolveLogicalNodeFactRule(ctx, rule)
		if err != nil {
			return LogicalNodeFactsResponse{}, err
		}
		rule = resolved
	}
	key := identity.LogicalNodeKey{Rule: rule, PlacementID: identity.PlacementID(query.PlacementID)}
	metadata, err := a.runtime.FactMetadata(ctx, key)
	if err != nil {
		if errors.Is(err, simulator.ErrUnknownRule) {
			return LogicalNodeFactsResponse{}, &ServiceError{
				Status:  404,
				Code:    "LOGICAL_NODE_NOT_FOUND",
				Message: fmt.Sprintf("LogicalNode %s is not present", key),
				Details: key,
				Err:     err,
			}
		}
		return LogicalNodeFactsResponse{}, adaptRuntimeError(err)
	}
	contractFacts := wireFactSpecs(metadata.ContractFacts)
	return LogicalNodeFactsResponse{
		LogicalNode:         wirePlacementKey(key),
		Facts:               append([]FactSpec(nil), contractFacts...),
		ContractFacts:       contractFacts,
		ProviderDescriptors: wireProviderDescriptors(metadata),
		RuntimeFacts: RuntimeFactValues{
			Tick: wireFacts(metadata.RuntimeTickFacts),
		},
	}, nil
}

func (a *SimulatorAdapter) resolveLogicalNodeFactRule(ctx context.Context, rule identity.RuleKey) (identity.RuleKey, error) {
	if rule.Namespace != "" {
		return rule, nil
	}
	scenario, err := a.runtime.GetScenario(ctx)
	if err != nil {
		return identity.RuleKey{}, adaptRuntimeError(err)
	}
	var match identity.RuleKey
	found := false
	for _, spec := range scenario.Rules {
		candidate := spec.LogicalNode.Rule
		if candidate.RuleID != rule.RuleID {
			continue
		}
		if found && match.Namespace != candidate.Namespace {
			return identity.RuleKey{}, &ServiceError{
				Status:  409,
				Code:    "LOGICAL_NODE_AMBIGUOUS",
				Message: "ruleNamespace is required because ruleId matches multiple namespaces",
				Details: rule.RuleID,
			}
		}
		match = candidate
		found = true
	}
	if found {
		return match, nil
	}
	return rule, nil
}

func (a *SimulatorAdapter) ListTickets(ctx context.Context, query TicketListQuery) (TicketPage, error) {
	if err := a.check(); err != nil {
		return TicketPage{}, err
	}
	runtimeQuery := simulator.TicketQuery{
		Cursor: query.Cursor, Limit: query.Limit, Search: query.Search,
		PhysicalNodeID: identity.PhysicalNodeID(query.PhysicalNodeID),
		Status:         simulator.TicketStatus(query.State),
	}
	if runtimeQuery.Status == "all" {
		runtimeQuery.Status = ""
	}
	if runtimeQuery.Status == "expired" {
		runtimeQuery.Status = simulator.TicketRemoved
	}
	if query.RuleID > 0 {
		rule := identity.RuleKey{Namespace: query.RuleNamespace, RuleID: query.RuleID}
		if rule.Namespace == "" {
			resolved, err := a.resolveRule(ctx, rule)
			if err != nil {
				return TicketPage{}, err
			}
			rule = resolved
		}
		runtimeQuery.Rule = &rule
	}
	page, err := a.runtime.ListTickets(ctx, runtimeQuery)
	if err != nil {
		return TicketPage{}, adaptRuntimeError(err)
	}
	response := TicketPage{NextCursor: page.NextCursor, Total: page.Total, Items: make([]TicketView, 0, len(page.Items))}
	for _, item := range page.Items {
		response.Items = append(response.Items, wireTicketView(item))
	}
	return response, nil
}

func (a *SimulatorAdapter) CreateTicket(ctx context.Context, request TicketCreateRequest) (TicketView, error) {
	if err := a.check(); err != nil {
		return TicketView{}, err
	}
	if err := validateWireTicketID(request.Ticket.TicketID); err != nil {
		return TicketView{}, err
	}
	rule, err := a.resolveRule(ctx, identityRule(request.Rule))
	if err != nil {
		return TicketView{}, err
	}
	input := simulator.TicketInput{
		Rule:        rule,
		TicketID:    request.Ticket.TicketID,
		CreatedAt:   roundNowMillis(request.Ticket.CreatedAt),
		StringLists: cloneStringLists(request.Ticket.StringLists),
		Uint64Lists: cloneUint64Lists(request.Ticket.Uint64Lists),
		Int64Values: cloneInt64Values(request.Ticket.Int64Values),
		ObjectFacts: runtimeFacts(request.Facts),
		AffinityKey: request.AffinityKey,
	}
	if input.CreatedAt == 0 {
		input.CreatedAt = time.Now().UnixMilli()
	}
	view, err := a.addTicketInput(ctx, input, request.PlacementID)
	if err != nil {
		return TicketView{}, adaptRuntimeError(err)
	}
	return wireTicketView(view), nil
}

func (a *SimulatorAdapter) CreateCustomTickets(ctx context.Context, request CustomTicketsRequest) (TicketBatchResponse, error) {
	if err := a.check(); err != nil {
		return TicketBatchResponse{}, err
	}
	return a.createGeneratedTickets(ctx, request, false)
}

func (a *SimulatorAdapter) CreateTicketsBatch(ctx context.Context, request TicketBatchRequest) (TicketBatchResponse, error) {
	if err := a.check(); err != nil {
		return TicketBatchResponse{}, err
	}
	if request.Tickets == nil && request.Count > 0 {
		return a.createGeneratedTickets(ctx, CustomTicketsRequest{
			Count: request.Count, Seed: request.Seed, Rule: request.Rule,
			PlacementID: request.PlacementID, StartTicketID: request.StartTicketID,
			CreatedAtStart: request.CreatedAtStart,
			StringChoices:  request.StringChoices, Uint64Choices: request.Uint64Choices,
			Int64Ranges: request.Int64Ranges,
		}, request.Atomic)
	}
	response := TicketBatchResponse{Tickets: make([]TicketView, 0, len(request.Tickets))}
	for _, ticket := range request.Tickets {
		view, err := a.CreateTicket(ctx, ticket)
		if err != nil {
			response.Rejected++
			if request.Atomic {
				return response, a.rollbackTickets(response.Tickets, err)
			}
			continue
		}
		response.Accepted++
		response.Tickets = append(response.Tickets, view)
	}
	return response, nil
}

func (a *SimulatorAdapter) DeleteTicket(ctx context.Context, ticketID string) (DeleteTicketResponse, error) {
	if err := a.check(); err != nil {
		return DeleteTicketResponse{}, err
	}
	id, err := strconv.ParseUint(ticketID, 10, 64)
	if err != nil || id == 0 {
		return DeleteTicketResponse{}, &ServiceError{Status: 400, Code: "INVALID_TICKET_ID", Message: "ticketId must be a positive integer", Details: ticketID}
	}
	if err := validateWireTicketID(id); err != nil {
		return DeleteTicketResponse{}, err
	}
	deleted, err := a.runtime.DeleteTicket(ctx, id)
	if err != nil {
		return DeleteTicketResponse{}, adaptRuntimeError(err)
	}
	if !deleted {
		return DeleteTicketResponse{}, &ServiceError{Status: 404, Code: "NOT_FOUND", Message: "ticket was not found", Details: ticketID}
	}
	return DeleteTicketResponse{TicketID: id, Deleted: deleted}, nil
}

func (a *SimulatorAdapter) RunRound(ctx context.Context, request RoundRequest) (RoundResponse, error) {
	if err := a.check(); err != nil {
		return RoundResponse{}, err
	}
	now := time.Now().UnixMilli()
	if request.Now != nil {
		now = roundNowMillis(*request.Now)
	} else if request.Seed != nil {
		// Seed is retained as a compatibility alias for the deterministic
		// round timestamp used by older callers, so apply the same boundary
		// normalization when it is used in place of now.
		now = roundNowMillis(*request.Seed)
	}
	maxMatches := request.MaxMatches
	if maxMatches == 0 {
		maxMatches = request.MatchLimit
	}
	result, err := a.runtime.RunRound(ctx, now, maxMatches)
	if err != nil {
		return RoundResponse{}, adaptRuntimeError(err)
	}
	response := RoundResponse{
		RoundID:  "round-" + strconv.FormatUint(result.Round, 10),
		Produced: len(result.Matches),
		Matches:  make([]MatchView, 0, len(result.Matches)),
	}
	for _, match := range result.Matches {
		response.Matches = append(response.Matches, wireMatchView(match))
	}
	return response, nil
}

func (a *SimulatorAdapter) ListMatches(ctx context.Context, query MatchListQuery) (MatchPage, error) {
	if err := a.check(); err != nil {
		return MatchPage{}, err
	}
	page, err := a.runtime.ListMatches(ctx, simulator.MatchQuery{Cursor: query.Cursor, Limit: query.Limit})
	if err != nil {
		return MatchPage{}, adaptRuntimeError(err)
	}
	response := MatchPage{NextCursor: page.NextCursor, Total: page.Total, Items: make([]MatchView, 0, len(page.Items))}
	for _, match := range page.Items {
		response.Items = append(response.Items, wireMatchView(match))
	}
	return response, nil
}

// GetMatch returns one retained Match and its detached member observations.
// A missing or evicted Match is translated to a stable 404 for the HTTP
// boundary; the in-process Simulator keeps the boolean distinction so hosts
// can choose their own error representation.
func (a *SimulatorAdapter) GetMatch(ctx context.Context, matchID string) (MatchView, error) {
	if err := a.check(); err != nil {
		return MatchView{}, err
	}
	match, ok, err := a.runtime.GetMatch(ctx, matchID)
	if err != nil {
		return MatchView{}, adaptRuntimeError(err)
	}
	if !ok {
		return MatchView{}, &ServiceError{
			Status:  http.StatusNotFound,
			Code:    "MATCH_NOT_FOUND",
			Message: "match was not found or is no longer retained",
			Details: matchID,
		}
	}
	return wireMatchView(match), nil
}

func (a *SimulatorAdapter) SubscribeEvents(ctx context.Context, query EventQuery) (<-chan Event, error) {
	if err := a.check(); err != nil {
		return nil, err
	}
	start := uint64(0)
	if query.After != "" {
		value, err := strconv.ParseUint(query.After, 10, 64)
		if err != nil {
			return nil, &ServiceError{Status: 400, Code: "INVALID_EVENT_CURSOR", Message: "after must be a non-negative integer", Details: query.After}
		}
		start = value
	}
	interval := a.pollInterval
	if interval <= 0 {
		interval = 100 * time.Millisecond
	}
	result := make(chan Event, 16)
	go func() {
		defer close(result)
		cursor := start
		for {
			page, err := a.runtime.Events(ctx, simulator.EventQuery{Cursor: strconv.FormatUint(cursor, 10), Limit: maxPageLimit})
			if err != nil {
				return
			}
			for _, item := range page.Items {
				if item.Sequence <= cursor {
					continue
				}
				cursor = item.Sequence
				select {
				case result <- Event{ID: strconv.FormatUint(item.Sequence, 10), Type: item.Type, At: time.Now().UTC().Format(time.RFC3339Nano), Payload: item.Data}:
				case <-ctx.Done():
					return
				}
			}
			timer := time.NewTimer(interval)
			select {
			case <-ctx.Done():
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				return
			case <-timer.C:
			}
		}
	}()
	return result, nil
}

// addTicketInput inserts one already-normalized input. An explicit placement
// is an exact logical-node selection; an omitted placement deliberately keeps
// the normal Router path.
func (a *SimulatorAdapter) addTicketInput(ctx context.Context, input simulator.TicketInput, placementID string) (simulator.TicketView, error) {
	if placementID == "" {
		return a.runtime.AddTicket(ctx, input)
	}
	owner, err := a.resolvePlacement(ctx, input.Rule, placementID)
	if err != nil {
		return simulator.TicketView{}, err
	}
	return a.runtime.AddRoutedTicket(ctx, common.RouteDecision{Owner: owner}, input)
}

// resolvePlacement resolves the exact (RuleKey, PlacementID) pair from the
// active scenario. Placement is not a routing hint: when supplied, silently
// falling back to the Router would make the wire request misleading.
func (a *SimulatorAdapter) resolvePlacement(ctx context.Context, rule identity.RuleKey, placementID string) (identity.OwnerRef, error) {
	if placementID == "" {
		return identity.OwnerRef{}, &ServiceError{Status: 400, Code: "INVALID_PLACEMENT", Message: "placementId must not be empty", Path: "placementId"}
	}
	scenario, err := a.runtime.GetScenario(ctx)
	if err != nil {
		return identity.OwnerRef{}, adaptRuntimeError(err)
	}
	for _, spec := range scenario.Rules {
		if spec.LogicalNode.Rule != rule || string(spec.LogicalNode.PlacementID) != placementID {
			continue
		}
		owner := identity.OwnerRef{LogicalNode: spec.LogicalNode, PhysicalNodeID: spec.PhysicalNodeID}
		if err := owner.Validate(); err != nil {
			return identity.OwnerRef{}, &ServiceError{Status: 500, Code: "INVALID_SCENARIO", Message: "scenario contains an invalid placement", Details: err.Error(), Err: err}
		}
		return owner, nil
	}
	return identity.OwnerRef{}, &ServiceError{
		Status:  404,
		Code:    "PLACEMENT_NOT_FOUND",
		Message: "placement was not found for rule",
		Path:    "placementId",
		Details: map[string]any{"rule": wireRule(rule), "placementId": placementID},
	}
}

func (a *SimulatorAdapter) createGeneratedTickets(ctx context.Context, request CustomTicketsRequest, atomic bool) (TicketBatchResponse, error) {
	if request.Count <= 0 {
		return TicketBatchResponse{}, invalidBody("count", "count must be at least 1")
	}
	if request.CreatedAtStart == 0 {
		request.CreatedAtStart = time.Now().UnixMilli()
	} else {
		request.CreatedAtStart = roundNowMillis(request.CreatedAtStart)
	}
	if err := validateGeneratedWireTicketIDs(request.StartTicketID, request.Count); err != nil {
		return TicketBatchResponse{}, err
	}
	if request.Template != nil {
		if err := validateWireTicketID(request.Template.TicketID); err != nil {
			return TicketBatchResponse{}, err
		}
	}
	spec := generatorSpec(request)
	resolvedRule, err := a.resolveRule(ctx, spec.Rule)
	if err != nil {
		return TicketBatchResponse{}, err
	}
	spec.Rule = resolvedRule
	inputs, err := a.runtime.GenerateBatch(ctx, spec)
	if err != nil {
		return TicketBatchResponse{}, adaptRuntimeError(err)
	}
	response := TicketBatchResponse{
		Seed:        request.Seed,
		GeneratorID: "generator-" + strconv.FormatInt(request.Seed, 10),
	}
	added := make([]TicketView, 0, len(inputs))
	for _, input := range inputs {
		view, addErr := a.addTicketInput(ctx, input, request.PlacementID)
		if addErr != nil {
			response.Rejected++
			if atomic {
				return response, a.rollbackTickets(added, adaptRuntimeError(addErr))
			}
			return response, adaptRuntimeError(addErr)
		}
		response.Accepted++
		added = append(added, wireTicketView(view))
	}
	return response, nil
}

func (a *SimulatorAdapter) rollbackTickets(added []TicketView, cause error) error {
	var rollbackErr error
	for index := len(added) - 1; index >= 0; index-- {
		view := added[index]
		if _, err := a.runtime.RemoveTicket(context.Background(), identityOwner(view.Owner), view.Ticket.TicketID); err != nil {
			rollbackErr = errors.Join(rollbackErr, err)
		}
	}
	if rollbackErr == nil {
		return cause
	}
	return &ServiceError{
		Status:  500,
		Code:    "ATOMIC_ROLLBACK_FAILED",
		Message: "atomic ticket batch rollback failed",
		Details: rollbackErr.Error(),
		Err:     errors.Join(cause, rollbackErr),
	}
}

func (a *SimulatorAdapter) check() error {
	if a == nil || a.runtime == nil {
		return ErrServiceUnavailable
	}
	return nil
}

// resolveRule keeps the wire-friendly ruleId-only form usable when a
// scenario has a namespaced RuleKey.  An explicit namespace always wins;
// omission is resolved only when the runtime has exactly one matching ID.
func (a *SimulatorAdapter) resolveRule(ctx context.Context, rule identity.RuleKey) (identity.RuleKey, error) {
	if rule.Namespace != "" {
		return rule, nil
	}
	scenario, err := a.runtime.GetScenario(ctx)
	if err != nil {
		return identity.RuleKey{}, adaptRuntimeError(err)
	}
	var match identity.RuleKey
	found := false
	for _, spec := range scenario.Rules {
		if spec.LogicalNode.Rule.RuleID != rule.RuleID {
			continue
		}
		if found && match.Namespace != spec.LogicalNode.Rule.Namespace {
			return rule, nil
		}
		match = spec.LogicalNode.Rule
		found = true
	}
	if found {
		return match, nil
	}
	return rule, nil
}

func scenarioResponse(scenario simulator.Scenario) (ScenarioResponse, error) {
	payload, err := json.Marshal(scenario)
	if err != nil {
		return ScenarioResponse{}, &ServiceError{Status: 500, Code: "SCENARIO_ENCODING_ERROR", Message: "failed to encode scenario", Err: err}
	}
	return ScenarioResponse{Revision: time.Now().UTC().Format(time.RFC3339Nano), Scenario: payload}, nil
}

func identityRule(key RuleKey) identity.RuleKey {
	return identity.RuleKey{Namespace: key.Namespace, RuleID: key.RuleID}
}

func identityOwner(owner OwnerRef) identity.OwnerRef {
	return identity.OwnerRef{
		PhysicalNodeID: identity.PhysicalNodeID(owner.PhysicalNodeID),
		LogicalNode: identity.LogicalNodeKey{
			Rule:        identity.RuleKey{Namespace: owner.LogicalNode.Rule.Namespace, RuleID: owner.LogicalNode.Rule.RuleID},
			PlacementID: identity.PlacementID(owner.LogicalNode.PlacementID),
		},
	}
}

func wireRule(key identity.RuleKey) RuleKey {
	return RuleKey{Namespace: key.Namespace, RuleID: key.RuleID}
}

func wirePlacementKey(key identity.LogicalNodeKey) PlacementKey {
	return PlacementKey{Rule: wireRule(key.Rule), PlacementID: string(key.PlacementID)}
}

func wireOwner(owner identity.OwnerRef) OwnerRef {
	return OwnerRef{PhysicalNodeID: string(owner.PhysicalNodeID), LogicalNode: wirePlacementKey(owner.LogicalNode)}
}

func wireFacts(facts simulator.FactSnapshot) TypedValues {
	uint64s, omittedUint64 := cloneWireUint64ListsWithOmitted(facts.Uint64Lists)
	int64s, omittedInt64 := cloneWireInt64ValuesWithOmitted(facts.Int64Values)
	return TypedValues{
		StringLists:           cloneStringLists(facts.StringLists),
		Uint64Lists:           uint64s,
		Int64Values:           int64s,
		OmittedNumericSamples: omittedUint64 + omittedInt64,
	}
}

func wireFactSpecs(specs []matchsystem.FactSpec) []FactSpec {
	result := make([]FactSpec, len(specs))
	for index, spec := range specs {
		result[index] = FactSpec{
			Name:        spec.Name,
			Type:        factTypeName(spec.Type),
			Scope:       string(spec.Scope),
			MaxValues:   spec.MaxValues,
			Description: spec.Description,
		}
	}
	return result
}

func wireProviderDescriptors(metadata simulator.LogicalNodeFactMetadata) ProviderDescriptorSet {
	return ProviderDescriptorSet{
		Tick:   wireProviderDescriptor(metadata.TickProviderDescriptor),
		Object: wireProviderDescriptor(metadata.ObjectProviderDescriptor),
		Match:  wireProviderDescriptor(metadata.MatchProviderDescriptor),
	}
}

func wireProviderDescriptor(descriptor *matchsystem.ProviderDescriptor) *FactProviderDescriptor {
	if descriptor == nil {
		return nil
	}
	return &FactProviderDescriptor{
		ID:      descriptor.ID,
		Version: descriptor.Version,
		Facts:   wireFactSpecs(descriptor.Facts),
	}
}

func factTypeName(value matchsystem.FactType) string {
	switch value {
	case matchsystem.FactTypeStrings:
		return "strings"
	case matchsystem.FactTypeInt64:
		return "int64"
	case matchsystem.FactTypeUint64s:
		return "uint64s"
	default:
		return "unknown"
	}
}

func runtimeFacts(facts TypedValues) simulator.FactSnapshot {
	return simulator.FactSnapshot{
		StringLists: cloneStringLists(facts.StringLists),
		Uint64Lists: cloneUint64Lists(facts.Uint64Lists),
		Int64Values: cloneInt64Values(facts.Int64Values),
	}
}

func wireTicket(ticket Ticket) simulator.TicketInput {
	return simulator.TicketInput{
		TicketID:    ticket.TicketID,
		CreatedAt:   ticket.CreatedAt,
		StringLists: cloneStringLists(ticket.StringLists),
		Uint64Lists: cloneUint64Lists(ticket.Uint64Lists),
		Int64Values: cloneInt64Values(ticket.Int64Values),
	}
}

func wireTicketView(view simulator.TicketView) TicketView {
	uint64s, omittedUint64 := cloneWireUint64ListsWithOmitted(view.Uint64Lists)
	int64s, omittedInt64 := cloneWireInt64ValuesWithOmitted(view.Int64Values)
	decision := RouteDecision{
		DecisionID: view.Decision.DecisionID,
		Owner:      wireOwner(view.Decision.Owner),
		Endpoint:   string(view.Decision.Endpoint),
	}
	return TicketView{
		Ticket: Ticket{
			TypedValues: TypedValues{
				StringLists:           cloneStringLists(view.StringLists),
				Uint64Lists:           uint64s,
				Int64Values:           int64s,
				OmittedNumericSamples: omittedUint64 + omittedInt64,
			},
			TicketID: view.TicketID, CreatedAt: view.CreatedAt,
		},
		Facts: wireFacts(view.ObjectFacts),
		Owner: wireOwner(view.Owner),
		Route: &decision,
		State: string(view.Status),
	}
}

func wireMatchView(match simulator.MatchRecord) MatchView {
	view := MatchView{
		MatchID:        match.ID,
		Round:          match.Round,
		PhysicalNodeID: string(match.PhysicalNodeID),
		LogicalNode:    wirePlacementKey(match.LogicalNode),
		MemberCount:    len(match.Tickets),
		Tickets:        make([]Ticket, 0, len(match.Tickets)),
		Members:        make([]TicketView, 0, len(match.Tickets)),
		Facts:          wireFacts(match.Facts),
		CreatedAt:      match.Now,
		DurationMs:     match.DurationMs,
	}
	for _, ticket := range match.Tickets {
		// Ticket IDs are emitted as JSON numbers and consumed by JavaScript.
		// Do not let a full-domain uint64 silently lose precision at this
		// boundary. The HTTP input validators already reject such IDs, while
		// this guard protects records supplied by an in-process host.
		if ticket.TicketID > MaxWireTicketID {
			continue
		}
		member := wireTicketView(ticket)
		view.Tickets = append(view.Tickets, member.Ticket)
		view.Members = append(view.Members, member)
	}
	return view
}

func generatorSpec(request CustomTicketsRequest) simulator.BatchGeneratorSpec {
	spec := simulator.BatchGeneratorSpec{
		Rule:           identityRule(request.Rule),
		Count:          request.Count,
		Seed:           request.Seed,
		FirstTicketID:  request.StartTicketID,
		CreatedAtStart: request.CreatedAtStart,
		StringChoices:  cloneStringLists(request.StringChoices),
		Uint64Choices:  cloneUint64Lists(request.Uint64Choices),
		StringLists:    make(map[string][]string),
		Uint64Lists:    make(map[string][]uint64),
		Int64Values:    make(map[string]int64),
		Int64Ranges:    make(map[string]simulator.Int64Range, len(request.Int64Ranges)),
	}
	for name, value := range request.Int64Ranges {
		spec.Int64Ranges[name] = simulator.Int64Range{Min: value.Min, Max: value.Max}
	}
	if request.Template != nil {
		spec.StringLists = cloneStringLists(request.Template.StringLists)
		spec.Uint64Lists = cloneUint64Lists(request.Template.Uint64Lists)
		spec.Int64Values = cloneInt64Values(request.Template.Int64Values)
	}
	for name, values := range request.Attributes {
		spec.StringLists[name] = append([]string(nil), values...)
	}
	return spec
}

func adaptRuntimeError(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := asServiceError(err); ok {
		return err
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var validationErr *simulator.ValidationError
	if errors.As(err, &validationErr) {
		return &ServiceError{Status: 422, Code: "VALIDATION_FAILED", Message: validationErr.Error(), Details: validationErr.Issues, Err: err}
	}
	switch {
	case errors.Is(err, simulator.ErrSimulatorClosed):
		return &ServiceError{Status: 503, Code: "SERVICE_UNAVAILABLE", Message: "simulator is closed", Err: err}
	case errors.Is(err, simulator.ErrInvalidMatchID):
		return &ServiceError{Status: 400, Code: "INVALID_MATCH_ID", Message: "matchId must be a single non-empty identifier", Err: err}
	case errors.Is(err, simulator.ErrInvalidTicket), errors.Is(err, simulator.ErrInvalidBatchSpec):
		return &ServiceError{Status: 400, Code: "INVALID_TICKET", Message: "ticket input is invalid", Err: err}
	case errors.Is(err, simulator.ErrInvalidCursor):
		return &ServiceError{Status: 400, Code: "INVALID_CURSOR", Message: "cursor is invalid", Err: err}
	case errors.Is(err, simulator.ErrUnknownNode), errors.Is(err, simulator.ErrUnknownRule), errors.Is(err, simulator.ErrTicketNotObserved):
		return &ServiceError{Status: 404, Code: "NOT_FOUND", Message: "simulator resource was not found", Err: err}
	case errors.Is(err, simulator.ErrTicketAmbiguous):
		return &ServiceError{Status: 409, Code: "AMBIGUOUS_TICKET", Message: "ticket id belongs to multiple owners", Err: err}
	case errors.Is(err, simulator.ErrTicketAlreadyObserved):
		return &ServiceError{Status: 409, Code: "DUPLICATE_TICKET", Message: "ticket is already present", Err: err}
	case errors.Is(err, simulator.ErrRoundNotStarted):
		return &ServiceError{Status: 409, Code: "ROUND_NOT_STARTED", Message: "simulator round has not started", Err: err}
	case errors.Is(err, client.ErrNoRoute):
		return &ServiceError{Status: 409, Code: "NO_ROUTE", Message: "no route is available for the rule", Err: err}
	default:
		return err
	}
}

func ctxErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func cloneStringLists(values map[string][]string) map[string][]string {
	if values == nil {
		return nil
	}
	result := make(map[string][]string, len(values))
	for key, value := range values {
		result[key] = append([]string(nil), value...)
	}
	return result
}

func cloneUint64Lists(values map[string][]uint64) map[string][]uint64 {
	if values == nil {
		return nil
	}
	result := make(map[string][]uint64, len(values))
	for key, value := range values {
		result[key] = append([]uint64(nil), value...)
	}
	return result
}

// cloneWireUint64Lists keeps only values that round-trip through a JSON
// number in JavaScript. The simulator core still retains the complete uint64
// values; values outside this transport-safe domain are intentionally omitted
// from HTTP observations rather than being rounded into a different value.
func cloneWireUint64Lists(values map[string][]uint64) map[string][]uint64 {
	result, _ := cloneWireUint64ListsWithOmitted(values)
	return result
}

func cloneWireUint64ListsWithOmitted(values map[string][]uint64) (map[string][]uint64, int) {
	if values == nil {
		return nil, 0
	}
	result := make(map[string][]uint64, len(values))
	omitted := 0
	for key, items := range values {
		safe := make([]uint64, 0, len(items))
		for _, item := range items {
			if item <= MaxWireTicketID {
				safe = append(safe, item)
			} else {
				omitted++
			}
		}
		if len(safe) > 0 {
			result[key] = safe
		}
	}
	return result, omitted
}

func cloneWireInt64ValuesWithOmitted(values map[string]int64) (map[string]int64, int) {
	if values == nil {
		return nil, 0
	}
	result := make(map[string]int64, len(values))
	omitted := 0
	for key, item := range values {
		if item < -int64(MaxWireTicketID) || item > int64(MaxWireTicketID) {
			omitted++
			continue
		}
		result[key] = item
	}
	return result, omitted
}

func cloneInt64Values(values map[string]int64) map[string]int64 {
	if values == nil {
		return nil
	}
	result := make(map[string]int64, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}

func cloneOperatorCapabilities(values []simulator.OperatorCapability) []OperatorCapability {
	result := make([]OperatorCapability, len(values))
	for index, value := range values {
		result[index] = OperatorCapability{
			Name:       value.Name,
			ResultType: value.ResultType,
			Inputs:     append([]string(nil), value.Inputs...),
			Fields:     append([]string(nil), value.Fields...),
		}
	}
	return result
}

func validateWireTicketID(id uint64) error {
	if id == 0 {
		return &ServiceError{Status: 400, Code: "INVALID_TICKET_ID", Message: "ticketId must be positive", Path: "ticketId"}
	}
	if id > MaxWireTicketID {
		return &ServiceError{
			Status:  400,
			Code:    "UNSAFE_TICKET_ID",
			Message: "ticketId must be at most 9007199254740991 for JSON/JavaScript safety",
			Path:    "ticketId",
			Details: id,
		}
	}
	return nil
}

func validateGeneratedWireTicketIDs(start uint64, count int) error {
	if count <= 0 {
		return invalidBody("count", "count must be at least 1")
	}
	if start == 0 {
		start = 1
	}
	last := start + uint64(count-1)
	if last < start || last > MaxWireTicketID {
		return &ServiceError{
			Status:  400,
			Code:    "UNSAFE_TICKET_ID",
			Message: "generated ticketId values must be at most 9007199254740991 for JSON/JavaScript safety",
			Path:    "startTicketId",
			Details: map[string]any{"startTicketId": start, "count": count, "maximum": MaxWireTicketID},
		}
	}
	return nil
}
