package simulator

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"testing"

	"matchSystem/internal/common"
	"matchSystem/internal/identity"
	"matchSystem/internal/matchsystem"
	"matchSystem/internal/matchsystem/contract"
	"matchSystem/internal/matchsystem/fact"
)

type typedNilMatchFactProvider struct{}

func (*typedNilMatchFactProvider) Initialize(context.Context, matchsystem.InitializeInput) (matchsystem.Facts, error) {
	panic("typed-nil provider must not be invoked")
}

func (*typedNilMatchFactProvider) OnJoin(context.Context, matchsystem.JoinInput) (matchsystem.Facts, error) {
	panic("typed-nil provider must not be invoked")
}

func testScenario() (Scenario, identity.LogicalNodeKey) {
	key := identity.LogicalNodeKey{
		Rule:        identity.RuleKey{Namespace: "test", RuleID: 1},
		PlacementID: "p1",
	}
	ruleJSON := []byte(`{
		"schemaVersion":"match-rule/v1",
		"ruleKey":{"namespace":"test","ruleId":1},
		"contract":{"schemaVersion":"logical-node-contract/v3","attributes":[],"facts":[{"name":"object_tag","type":"strings","scope":"object","maxValues":2}],"indexes":[]},
		"prefilter":{"schemaVersion":"prefilter/v3","bitmap":{"resultType":"bitmap","expr":{"op":"none"}}},
		"evaluation":{"schemaVersion":"evaluation/v3","canJoin":{"schemaVersion":"expression-scalar/v3","resultType":"bool","expr":{"op":"bool_literal","value":true}},"canComplete":{"schemaVersion":"expression-scalar/v3","resultType":"bool","expr":{"op":"bool_literal","value":true}}},
		"scoring":{"type":"created_at","params":{"direction":"descending"}},
		"seedSelection":{"type":"arrival","params":{}},
		"runtime":{"candidateLimitPerSeed":128,"maxPlayers":8,"attemptLimitPerProduceMatch":500,"attemptLimitPerMatchRound":500}
	}`)
	rule := NewRuleSpec(key, "p1", ruleJSON)
	rule.ObjectFactProviderDescriptor = &matchsystem.ProviderDescriptor{
		ID:      "test.object-facts",
		Version: "v1",
		Facts: []matchsystem.FactSpec{{
			Name: "object_tag", Type: matchsystem.FactTypeStrings,
			Scope: matchsystem.FactScopeObject, MaxValues: 2,
		}},
	}
	return Scenario{
		SchemaVersion: ScenarioSchemaVersion,
		PhysicalNodes: []PhysicalNodeSpec{{ID: "p1", Endpoint: "inproc://p1", Enabled: true}},
		Rules:         []RuleSpec{rule},
	}, key
}

func TestSimulatorVerticalLifecycle(t *testing.T) {
	scenario, key := testScenario()
	sim, err := NewService(scenario)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	defer sim.Close()

	ctx := context.Background()
	input := TicketInput{
		Rule:      key.Rule,
		TicketID:  42,
		CreatedAt: 100,
		ObjectFacts: FactSnapshot{
			StringLists: map[string][]string{"object_tag": {"demo"}},
		},
	}
	view, err := sim.AddTicket(ctx, input)
	if err != nil {
		t.Fatalf("AddTicket: %v", err)
	}
	if view.Owner.LogicalNode != key || view.Owner.PhysicalNodeID != "p1" {
		t.Fatalf("unexpected owner: %#v", view.Owner)
	}
	if view.ObjectFacts.StringLists["object_tag"][0] != "demo" {
		t.Fatalf("object Fact was not observed: %#v", view.ObjectFacts)
	}
	view.ObjectFacts.StringLists["object_tag"][0] = "mutated"
	page, err := sim.ListTickets(ctx, TicketQuery{Limit: 10})
	if err != nil {
		t.Fatalf("ListTickets: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].ObjectFacts.StringLists["object_tag"][0] != "demo" {
		t.Fatalf("registry leaked mutable state: %#v", page.Items)
	}
	for _, search := range []string{"42", "DEMO", "object_tag"} {
		filtered, filterErr := sim.ListTickets(ctx, TicketQuery{Limit: 10, Search: search})
		if filterErr != nil || filtered.Total != 1 {
			t.Fatalf("ListTickets search %q: total=%d err=%v", search, filtered.Total, filterErr)
		}
	}
	filtered, err := sim.ListTickets(ctx, TicketQuery{Limit: 10, Search: "missing"})
	if err != nil || filtered.Total != 0 {
		t.Fatalf("ListTickets missing search: total=%d err=%v", filtered.Total, err)
	}

	if err := sim.BeginRound(ctx, 1000); err != nil {
		t.Fatalf("BeginRound: %v", err)
	}
	result, err := sim.ProduceMatch(ctx)
	if err != nil {
		t.Fatalf("ProduceMatch: %v", err)
	}
	if result.Match == nil || len(result.Match.Tickets) != 1 || result.Match.Tickets[0].TicketID != input.TicketID {
		t.Fatalf("unexpected produced match: %#v", result)
	}
	if got := result.Match.Tickets[0].ObjectFacts.StringLists["object_tag"][0]; got != "demo" {
		t.Fatalf("default ObjectFactProvider snapshot: got %q, want %q", got, "demo")
	}
	waiting, err := sim.ListTickets(ctx, TicketQuery{Limit: 10})
	if err != nil {
		t.Fatalf("ListTickets after match: %v", err)
	}
	if len(waiting.Items) != 0 {
		t.Fatalf("matched Ticket remains waiting: %#v", waiting.Items)
	}
	matches, err := sim.ListMatches(ctx, MatchQuery{Limit: 10})
	if err != nil {
		t.Fatalf("ListMatches: %v", err)
	}
	if matches.Total != 1 || matches.Items[0].ID != result.Match.ID {
		t.Fatalf("unexpected match page: %#v", matches)
	}
	events, err := sim.Events(ctx, EventQuery{Limit: 10})
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	if len(events.Items) < 2 || events.Items[len(events.Items)-1].Type != "match_created" {
		t.Fatalf("missing match event: %#v", events.Items)
	}
}

func TestSimulatorFactProviderReceivesNodeSnapshot(t *testing.T) {
	scenario, key := testScenario()
	var got matchsystem.TickFactInput
	scenario.Rules[0].FactProvider = func(_ context.Context, input matchsystem.TickFactInput) (matchsystem.Facts, error) {
		got = input
		return matchsystem.Facts{}, nil
	}
	sim, err := NewSimulator(scenario)
	if err != nil {
		t.Fatalf("NewSimulator: %v", err)
	}
	defer sim.Close()

	ctx := context.Background()
	if _, err := sim.AddTicket(ctx, TicketInput{Rule: key.Rule, TicketID: 1, CreatedAt: 10}); err != nil {
		t.Fatalf("AddTicket: %v", err)
	}
	if err := sim.BeginRound(ctx, 100); err != nil {
		t.Fatalf("BeginRound: %v", err)
	}
	if _, err := sim.ProduceMatch(ctx); err != nil {
		t.Fatalf("ProduceMatch: %v", err)
	}
	if got.Now != 100 || got.Node.Key != key || got.Node.State != matchsystem.LogicalNodeReady || got.Node.WaitingCount != 1 {
		t.Fatalf("unexpected FactProvider input: %#v", got)
	}
}

func TestValidatingFactProviderChecksContract(t *testing.T) {
	validator, err := fact.NewValidator([]fact.Spec{
		{Name: "waiting-count", Type: fact.TypeInt64, Scope: fact.ScopeTick},
	})
	if err != nil {
		t.Fatalf("create Fact validator: %v", err)
	}
	provider := validatingTickProvider(func(context.Context, matchsystem.TickFactInput) (matchsystem.Facts, error) {
		return matchsystem.Facts{StringLists: map[string][]string{"waiting-count": {"wrong-type"}}}, nil
	}, validator)
	_, err = provider(context.Background(), matchsystem.TickFactInput{})
	if err == nil {
		t.Fatal("validating FactProvider accepted a value in the wrong type map")
	}
}

func TestRuntimeLogicalNodeSpecTreatsTypedNilMatchFactProviderAsAbsent(t *testing.T) {
	var provider *typedNilMatchFactProvider
	rule := RuleSpec{MatchFactProvider: provider}
	schema := contract.Contract{Facts: []fact.Spec{
		{Name: "match-count", Type: fact.TypeInt64, Scope: fact.ScopeMatch},
	}}
	spec := runtimeLogicalNodeSpec(rule, schema, NewObservationRegistry(), identity.OwnerRef{}, nil)
	if spec.MatchFactProvider == nil {
		t.Fatal("typed-nil MatchFactProvider did not receive the simulator default provider")
	}
	if spec.MatchFactProviderDescriptor != nil {
		t.Fatal("simulator synthesized a MatchFactProvider descriptor")
	}
	values, err := spec.MatchFactProvider.Initialize(context.Background(), matchsystem.InitializeInput{})
	if err != nil {
		t.Fatalf("default MatchFactProvider Initialize: %v", err)
	}
	if got := values.Int64Values["match-count"]; got != 1 {
		t.Fatalf("default MatchFactProvider value: got %d, want 1", got)
	}
}

func TestSimulatorDoesNotSynthesizeProviderDescriptorFromContractOrRuntimeValues(t *testing.T) {
	scenario, _ := testScenario()
	scenario.Rules[0].ObjectFactProviderDescriptor = nil
	scenario.Rules[0].TickFacts = FactSnapshot{Int64Values: map[string]int64{"unused": 1}}
	_, err := NewSimulator(scenario)
	if err == nil {
		t.Fatal("NewSimulator accepted a Contract Fact without an explicit provider descriptor")
	}
	var validationErr *ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("error=%T %v, want simulator validation error", err, err)
	}
	found := false
	for _, issue := range validationErr.Issues {
		if issue.Code != matchsystem.ProviderHandshakeMissingDescriptor {
			continue
		}
		if issue.Path != "$.rules[0].rule.provider.object" {
			t.Fatalf("missing descriptor issue path=%q, want $.rules[0].rule.provider.object", issue.Path)
		}
		found = true
	}
	if !found {
		t.Fatalf("validation issues=%#v, want missing descriptor issue", validationErr.Issues)
	}
}

func TestSimulatorBatchIsDeterministicAndRoutes(t *testing.T) {
	scenario, key := testScenario()
	sim, err := NewSimulator(scenario)
	if err != nil {
		t.Fatalf("NewSimulator: %v", err)
	}
	defer sim.Close()
	spec := BatchGeneratorSpec{
		Rule:           key.Rule,
		Count:          5,
		Seed:           77,
		FirstTicketID:  100,
		CreatedAtStart: 10,
		CreatedAtStep:  2,
		AffinityPrefix: "aff-",
		ObjectFacts:    FactSnapshot{StringLists: map[string][]string{"object_tag": {"batch"}}},
	}
	left, err := GenerateBatch(spec)
	if err != nil {
		t.Fatalf("GenerateBatch: %v", err)
	}
	right, err := GenerateBatch(spec)
	if err != nil {
		t.Fatalf("GenerateBatch second run: %v", err)
	}
	if !reflect.DeepEqual(left, right) {
		t.Fatalf("same seed generated different data:\nleft=%#v\nright=%#v", left, right)
	}
	result, err := sim.AddBatch(context.Background(), spec)
	if err != nil {
		t.Fatalf("AddBatch: %v", err)
	}
	if result.Added != spec.Count || len(result.Decisions) != spec.Count {
		t.Fatalf("unexpected batch result: %#v", result)
	}
	page, err := sim.ListTickets(context.Background(), TicketQuery{Limit: 20})
	if err != nil {
		t.Fatalf("ListTickets: %v", err)
	}
	if page.Total != spec.Count {
		t.Fatalf("batch Tickets not visible: %#v", page)
	}
	for _, item := range page.Items {
		if item.Decision.Owner.LogicalNode != key || item.ObjectFacts.StringLists["object_tag"][0] != "batch" {
			t.Fatalf("batch Ticket has wrong observation: %#v", item)
		}
	}
}

func TestScenarioReplacementIsAtomicOnValidationFailure(t *testing.T) {
	scenario, key := testScenario()
	sim, err := New(scenario)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer sim.Close()
	invalid := scenario.Clone()
	invalid.Rules[0].RuleJSON = []byte(`{"schemaVersion":"match-rule/v1","unexpected":true}`)
	if err := sim.ReplaceScenario(context.Background(), invalid); err == nil {
		t.Fatal("invalid replacement unexpectedly succeeded")
	}
	if _, err := sim.AddTicket(context.Background(), TicketInput{Rule: key.Rule, TicketID: 1}); err != nil {
		t.Fatalf("old scenario was not preserved: %v", err)
	}
}

func TestValidateScenarioRejectsDifferentConfigsForOneRuleKey(t *testing.T) {
	scenario, key := testScenario()
	scenario.PhysicalNodes = append(scenario.PhysicalNodes, NewPhysicalNodeSpec("p2", "inproc://p2"))
	second := NewRuleSpec(
		identity.LogicalNodeKey{Rule: key.Rule, PlacementID: "p2"},
		"p2",
		scenario.Rules[0].RuleJSON,
	)
	second.ObjectFactProviderDescriptor = cloneProviderDescriptor(scenario.Rules[0].ObjectFactProviderDescriptor)
	second.RuleJSON = bytes.Replace(second.RuleJSON, []byte(`"direction":"descending"`), []byte(`"direction":"ascending"`), 1)
	scenario.Rules = append(scenario.Rules, second)
	report := ValidateScenario(scenario)
	if report.Valid {
		t.Fatal("same RuleKey with different match-rule/v1 documents was accepted")
	}
	found := false
	for _, issue := range report.Issues {
		if issue.Code == "RULE_CONFIG_MISMATCH" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("RULE_CONFIG_MISMATCH issue not found: %#v", report.Issues)
	}
}

func TestValidateScenarioPrefixesAggregateRuleIssuePath(t *testing.T) {
	scenario, _ := testScenario()
	scenario.Rules[0].RuleJSON = bytes.Replace(
		scenario.Rules[0].RuleJSON,
		[]byte(`"type":"created_at"`),
		[]byte(`"type":"unsupported"`),
		1,
	)
	report := ValidateScenario(scenario)
	if report.Valid || len(report.Issues) == 0 {
		t.Fatal("invalid scorer unexpectedly passed validation")
	}
	if got, want := report.Issues[0].Path, "$.rules[0].rule.scoring.type"; got != want {
		t.Fatalf("issue path = %q, want %q; issues=%#v", got, want, report.Issues)
	}
}

func TestCapabilitiesExposeClosedOperatorVocabulary(t *testing.T) {
	sim, err := NewSimulator(Scenario{})
	if err != nil {
		t.Fatalf("NewSimulator empty scenario: %v", err)
	}
	defer sim.Close()
	caps, err := sim.Capabilities(context.Background())
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	if len(caps.ScalarOperators) < 30 || len(caps.BitmapOperators) != 8 {
		t.Fatalf("incomplete operator catalog: scalar=%d bitmap=%d", len(caps.ScalarOperators), len(caps.BitmapOperators))
	}
	if !contains(caps.ExpressionOps, "strings_intersects") || !contains(caps.BitmapOps, "lookup_range") {
		t.Fatalf("flat operator names are incomplete: %#v %#v", caps.ExpressionOps, caps.BitmapOps)
	}
	var containsFields, rangeFields []string
	for _, operator := range caps.ScalarOperators {
		if operator.Name == "strings_contains" {
			containsFields = operator.Fields
		}
	}
	for _, operator := range caps.BitmapOperators {
		if operator.Name == "lookup_range" {
			rangeFields = operator.Fields
		}
	}
	if !contains(containsFields, "needle") || !contains(rangeFields, "min") || !contains(rangeFields, "max") {
		t.Fatalf("operator field metadata is incomplete: contains=%v range=%v", containsFields, rangeFields)
	}
}

func TestEventSubscriptionReceivesAndCloses(t *testing.T) {
	scenario, key := testScenario()
	sim, err := NewSimulator(scenario)
	if err != nil {
		t.Fatalf("NewSimulator: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream, err := sim.SubscribeEvents(ctx, EventQuery{Limit: 10})
	if err != nil {
		t.Fatalf("SubscribeEvents: %v", err)
	}
	if _, err := sim.AddTicket(context.Background(), TicketInput{Rule: key.Rule, TicketID: 1}); err != nil {
		t.Fatalf("AddTicket: %v", err)
	}
	select {
	case event := <-stream:
		if event.Type != "ticket_added" {
			t.Fatalf("event type = %q", event.Type)
		}
	case <-context.Background().Done():
		t.Fatal("unreachable")
	}
	if err := sim.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case _, open := <-stream:
		if open {
			t.Fatal("event stream stayed open after Close")
		}
	case <-context.Background().Done():
		t.Fatal("unreachable")
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestDeleteTicketRejectsAmbiguousObservation(t *testing.T) {
	scenario, key := testScenario()
	second := scenario.Rules[0]
	second.LogicalNode.PlacementID = "p2"
	second.PhysicalNodeID = "p2"
	scenario.PhysicalNodes = append(scenario.PhysicalNodes, PhysicalNodeSpec{ID: "p2", Endpoint: "inproc://p2", Enabled: true})
	scenario.Rules = append(scenario.Rules, second)
	sim, err := NewSimulator(scenario)
	if err != nil {
		t.Fatalf("NewSimulator: %v", err)
	}
	defer sim.Close()
	// AddRoutedTicket intentionally creates the same TicketID at two owners;
	// DeleteTicket must not guess which waiting Ticket the caller meant.
	for _, physical := range []string{"p1", "p2"} {
		owner := identity.OwnerRef{LogicalNode: identity.LogicalNodeKey{Rule: key.Rule, PlacementID: identity.PlacementID(physical)}, PhysicalNodeID: identity.PhysicalNodeID(physical)}
		decision, err := sim.RouteNew(context.Background(), common.RouteRequest{Rule: key.Rule, TicketID: 900, AffinityKey: physical})
		if err != nil {
			t.Fatalf("RouteNew %s: %v", physical, err)
		}
		decision.Owner = owner
		decision.Endpoint = common.Endpoint("inproc://" + physical)
		if _, err := sim.AddRoutedTicket(context.Background(), decision, TicketInput{Rule: key.Rule, TicketID: 900}); err != nil {
			t.Fatalf("AddRoutedTicket %s: %v", physical, err)
		}
	}
	if _, err := sim.DeleteTicket(context.Background(), 900); !errors.Is(err, ErrTicketAmbiguous) {
		t.Fatalf("DeleteTicket error = %v, want ErrTicketAmbiguous", err)
	}
}
