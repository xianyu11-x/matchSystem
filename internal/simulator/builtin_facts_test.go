package simulator

import (
	"context"
	"math"
	"testing"

	"matchSystem/internal/common"
	"matchSystem/internal/identity"
	"matchSystem/internal/matchsystem"
	"matchSystem/internal/matchsystem/fact"
)

func TestSimulatorDefaultTickFactProviderComputesWaitingCount(t *testing.T) {
	provider := simulatorTickFactProvider(FactSnapshot{
		Int64Values: map[string]int64{"static": 99},
	}, []fact.Spec{
		{Name: FactNameWaitingCount, Type: fact.TypeInt64, Scope: fact.ScopeTick},
		{Name: "queueDepth", Type: fact.TypeInt64, Scope: fact.ScopeTick},
	})
	values, err := provider(context.Background(), matchsystem.TickFactInput{
		Node: matchsystem.LogicalNodeSnapshot{WaitingCount: 7},
	})
	if err != nil {
		t.Fatalf("default Tick Fact provider: %v", err)
	}
	if got := values.Int64Values[FactNameWaitingCount]; got != 7 {
		t.Fatalf("waitingCount=%d, want 7", got)
	}
	if got := values.Int64Values["queueDepth"]; got != 7 {
		t.Fatalf("queueDepth=%d, want 7", got)
	}
	if got := values.Int64Values["static"]; got != 99 {
		t.Fatalf("static Tick Fact=%d, want 99", got)
	}
}

func TestSimulatorDefaultFactProvidersComputeCurrentWaitingFacts(t *testing.T) {
	key := identity.LogicalNodeKey{
		Rule:        identity.RuleKey{Namespace: "builtin", RuleID: 1},
		PlacementID: "default",
	}
	ruleJSON := []byte(`{
		"schemaVersion":"match-rule/v1",
		"ruleKey":{"namespace":"builtin","ruleId":1},
		"contract":{"schemaVersion":"logical-node-contract/v3","attributes":[],"facts":[
			{"name":"waitingCount","type":"int64","scope":"tick"},
			{"name":"waitingTime","type":"int64","scope":"object"}
		],"indexes":[]},
		"prefilter":{"schemaVersion":"prefilter/v3","bitmap":{"resultType":"bitmap","expr":{"op":"none"}}},
		"evaluation":{"schemaVersion":"evaluation/v3","canJoin":{"schemaVersion":"expression-scalar/v3","resultType":"bool","expr":{"op":"bool_literal","value":true}},"canComplete":{"schemaVersion":"expression-scalar/v3","resultType":"bool","expr":{"op":"int64_eq","left":{"op":"int64_ref","source":"tick_facts","name":"waitingCount"},"right":{"op":"int64_literal","value":2}}}},
		"scoring":{"type":"created_at","params":{"direction":"descending"}},
		"seedSelection":{"type":"arrival","params":{}},
		"runtime":{"candidateScoringLimitPerSeed":500,"candidateLimitPerSeed":50,"maxPlayers":8,"attemptLimitPerProduceMatch":500,"attemptLimitPerMatchRound":500}
	}`)
	rule := NewRuleSpec(key, "p1", ruleJSON)
	rule.FactProviderDescriptor = &matchsystem.ProviderDescriptor{
		ID:      "simulator.tick-facts",
		Version: "v1",
		Facts: []matchsystem.FactSpec{{
			Name: FactNameWaitingCount, Type: fact.TypeInt64, Scope: fact.ScopeTick,
		}},
	}
	rule.ObjectFactProviderDescriptor = &matchsystem.ProviderDescriptor{
		ID:      "simulator.object-facts",
		Version: "v1",
		Facts: []matchsystem.FactSpec{{
			Name: FactNameWaitingTime, Type: fact.TypeInt64, Scope: fact.ScopeObject,
		}},
	}
	sim, err := NewSimulator(Scenario{
		SchemaVersion: ScenarioSchemaVersion,
		PhysicalNodes: []PhysicalNodeSpec{NewPhysicalNodeSpec("p1", "inproc://p1")},
		Rules:         []RuleSpec{rule},
	})
	if err != nil {
		t.Fatalf("NewSimulator: %v", err)
	}
	defer sim.Close()

	ctx := context.Background()
	for _, ticket := range []struct {
		id        common.TicketID
		createdAt int64
	}{{id: 1, createdAt: 700}, {id: 2, createdAt: 800}} {
		if _, err := sim.AddTicket(ctx, TicketInput{
			Rule:      key.Rule,
			TicketID:  ticket.id,
			CreatedAt: ticket.createdAt,
		}); err != nil {
			t.Fatalf("AddTicket %d: %v", ticket.id, err)
		}
	}
	if err := sim.BeginRound(ctx, 1000); err != nil {
		t.Fatalf("BeginRound: %v", err)
	}
	result, err := sim.ProduceMatch(ctx)
	if err != nil {
		t.Fatalf("ProduceMatch: %v", err)
	}
	if result.Match == nil || len(result.Match.Tickets) != 1 {
		t.Fatalf("unexpected Match: %#v", result)
	}
	if got := result.Match.Tickets[0].TicketID; got != 1 {
		t.Fatalf("seed selected TicketID=%d, want 1", got)
	}
	if got := result.Match.Tickets[0].ObjectFacts.Int64Values[FactNameWaitingTime]; got != 300 {
		t.Fatalf("waitingTime=%d, want 300 in caller timestamp units", got)
	}
}

func TestElapsedFactTimeClampsFutureAndOverflow(t *testing.T) {
	tests := []struct {
		name      string
		now       int64
		createdAt int64
		want      int64
	}{
		{name: "future ticket", now: 100, createdAt: 120, want: 0},
		{name: "normal", now: 100, createdAt: 40, want: 60},
		{name: "overflow", now: math.MaxInt64, createdAt: -1, want: math.MaxInt64},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := elapsedFactTime(test.now, test.createdAt); got != test.want {
				t.Fatalf("elapsedFactTime(%d, %d)=%d, want %d", test.now, test.createdAt, got, test.want)
			}
		})
	}
}
