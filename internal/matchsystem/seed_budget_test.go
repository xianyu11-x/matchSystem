package matchsystem

import (
	"context"
	"errors"
	"testing"

	"matchSystem/internal/identity"
	"matchSystem/internal/matchsystem/prefilter"
)

func TestSeedAttemptLimitAccumulatesAcrossProduceMatchCalls(t *testing.T) {
	physical := mustPhysicalNode(t, "physical-a")
	spec := logicalSpec(identity.RuleKey{RuleID: 1}, "p1", false)
	spec.Config.SeedScheduler = SeedSchedulerConfig{
		AttemptLimitPerProduceMatch: 1,
		AttemptLimitPerMatchRound:   2,
	}
	mustLoad(t, physical, spec)
	ref := owner(physical.ID(), spec.Key)
	for _, ticketID := range []string{"a", "b", "c"} {
		mustAddTicket(t, physical, ref, ticketID)
	}
	if err := physical.BeginMatchRound(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		result, err := physical.ProduceMatch(context.Background())
		if err != nil || result.Match != nil {
			t.Fatalf("ProduceMatch %d: result=%#v err=%v", attempt, result, err)
		}
	}
	if _, err := physical.ProduceMatch(context.Background()); !errors.Is(err, ErrNoLogicalNodeAvailable) {
		t.Fatalf("expected exhausted round, got %v", err)
	}
	logical := physical.nodes[spec.Key.Rule]
	if logical.seedRound.attemptedSeeds != 2 {
		t.Fatalf("attempted seeds=%d want=2", logical.seedRound.attemptedSeeds)
	}
}

func TestSeedAttemptBudgetResetsAtBeginMatchRound(t *testing.T) {
	physical := mustPhysicalNode(t, "physical-a")
	spec := logicalSpec(identity.RuleKey{RuleID: 1}, "p1", false)
	spec.Config.SeedScheduler = SeedSchedulerConfig{
		AttemptLimitPerProduceMatch: 1,
		AttemptLimitPerMatchRound:   1,
	}
	seen := map[TicketID]int{}
	spec.ObjectFactProvider = func(ticket *Ticket, _ int64, _ Facts) (Facts, error) {
		seen[ticket.TicketID]++
		return Facts{}, nil
	}
	mustLoad(t, physical, spec)
	ref := owner(physical.ID(), spec.Key)
	mustAddTicket(t, physical, ref, "a")
	mustAddTicket(t, physical, ref, "b")
	if err := physical.BeginMatchRound(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if _, err := physical.ProduceMatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := physical.ProduceMatch(context.Background()); !errors.Is(err, ErrNoLogicalNodeAvailable) {
		t.Fatalf("expected exhausted first round, got %v", err)
	}
	if err := physical.BeginMatchRound(context.Background(), 2); err != nil {
		t.Fatal(err)
	}
	if _, err := physical.ProduceMatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	if seen[testTicketID("a")] != 2 {
		t.Fatalf("round reset did not reset seed order/budget: seen=%v", seen)
	}
	logical := physical.nodes[spec.Key.Rule]
	if logical.seedRound.attemptedSeeds != 1 {
		t.Fatalf("second-round attempted seeds=%d want=1", logical.seedRound.attemptedSeeds)
	}
}

func TestStaleSeedDoesNotConsumeRoundAttemptBudget(t *testing.T) {
	node := seedBudgetTestNode(t, SeedSchedulerConfig{
		AttemptLimitPerProduceMatch: 10,
		AttemptLimitPerMatchRound:   3,
	})
	mustAdd(t, node, &Ticket{TicketID: testTicketID("a")})
	mustAdd(t, node, &Ticket{TicketID: testTicketID("b")})
	mustAdd(t, node, &Ticket{TicketID: testTicketID("c")})
	if err := node.BeginMatchRound(1); err != nil {
		t.Fatal(err)
	}
	if !node.Remove(testTicketID("a")) {
		t.Fatal("remove stale seed failed")
	}
	first := node.nextSeed()
	second := node.nextSeed()
	if first == nil || first.TicketID != testTicketID("b") || second == nil || second.TicketID != testTicketID("c") {
		t.Fatalf("stale seed changed valid order: first=%#v second=%#v", first, second)
	}
	if node.seedRound.attemptedSeeds != 2 {
		t.Fatalf("stale seed consumed budget: attempted=%d", node.seedRound.attemptedSeeds)
	}
	if node.hasUntriedSeed() {
		t.Fatal("stale suffix should be exhausted")
	}
}

func TestSuccessfulMatchDoesNotResetSeedAttemptBudget(t *testing.T) {
	physical := mustPhysicalNode(t, "physical-a")
	spec := logicalSpec(identity.RuleKey{RuleID: 1}, "p1", true)
	spec.Config.SeedScheduler = SeedSchedulerConfig{
		AttemptLimitPerProduceMatch: 1,
		AttemptLimitPerMatchRound:   2,
	}
	mustLoad(t, physical, spec)
	ref := owner(physical.ID(), spec.Key)
	mustAddTicket(t, physical, ref, "a")
	mustAddTicket(t, physical, ref, "b")
	mustAddTicket(t, physical, ref, "c")
	if err := physical.BeginMatchRound(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		result, err := physical.ProduceMatch(context.Background())
		if err != nil || result.Match == nil {
			t.Fatalf("successful ProduceMatch %d: result=%#v err=%v", attempt, result, err)
		}
	}
	if _, err := physical.ProduceMatch(context.Background()); !errors.Is(err, ErrNoLogicalNodeAvailable) {
		t.Fatalf("successful matches reset the round budget: %v", err)
	}
	logical := physical.nodes[spec.Key.Rule]
	if logical.seedRound.attemptedSeeds != 2 {
		t.Fatalf("attempted seeds=%d want=2", logical.seedRound.attemptedSeeds)
	}
}

func TestSeedAttemptLimitsUseSmallerPerCallOrRoundCapacity(t *testing.T) {
	tests := []struct {
		name       string
		perCall    int
		perRound   int
		wantCalled int
	}{
		{name: "round caps larger call", perCall: 5, perRound: 2, wantCalled: 2},
		{name: "call caps larger round", perCall: 2, perRound: 5, wantCalled: 2},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			node := seedBudgetTestNode(t, SeedSchedulerConfig{
				AttemptLimitPerProduceMatch: test.perCall,
				AttemptLimitPerMatchRound:   test.perRound,
			})
			seen := 0
			node.objectFacts = func(_ *Ticket, _ int64, _ Facts) (Facts, error) {
				seen++
				return Facts{}, nil
			}
			for _, ticketID := range []string{"a", "b", "c", "d"} {
				mustAdd(t, node, &Ticket{TicketID: testTicketID(ticketID)})
			}
			if err := node.BeginMatchRound(1); err != nil {
				t.Fatal(err)
			}
			if _, err := node.ProduceMatch(Facts{}); err != nil {
				t.Fatal(err)
			}
			if seen != test.wantCalled {
				t.Fatalf("seed calls=%d want=%d", seen, test.wantCalled)
			}
			if node.seedRound.attemptedSeeds != test.wantCalled {
				t.Fatalf("attempted seeds=%d want=%d", node.seedRound.attemptedSeeds, test.wantCalled)
			}
		})
	}
}

func TestPhysicalSelectorSkipsLogicalNodeWithExhaustedRoundBudget(t *testing.T) {
	physical := mustPhysicalNode(t, "physical-a")
	first := logicalSpec(identity.RuleKey{RuleID: 1}, "p1", false)
	first.Config.SeedScheduler = SeedSchedulerConfig{
		AttemptLimitPerProduceMatch: 1,
		AttemptLimitPerMatchRound:   1,
	}
	second := logicalSpec(identity.RuleKey{RuleID: 2}, "p1", true)
	second.Config.SeedScheduler = first.Config.SeedScheduler
	mustLoad(t, physical, first)
	mustLoad(t, physical, second)
	mustAddTicket(t, physical, owner(physical.ID(), first.Key), "first")
	mustAddTicket(t, physical, owner(physical.ID(), second.Key), "second")
	if err := physical.BeginMatchRound(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	firstResult, err := physical.ProduceMatch(context.Background())
	if err != nil || firstResult.LogicalNode != first.Key || firstResult.Match != nil {
		t.Fatalf("first ProduceMatch: result=%#v err=%v", firstResult, err)
	}
	secondResult, err := physical.ProduceMatch(context.Background())
	if err != nil || secondResult.LogicalNode != second.Key || secondResult.Match == nil {
		t.Fatalf("selector did not skip exhausted node: result=%#v err=%v", secondResult, err)
	}
}

func TestSeedRoundMaterializesAtMostRoundLimit(t *testing.T) {
	policies := []SeedOrderPolicyConfig{
		{Kind: SeedOrderArrival},
		{Kind: SeedOrderOldest},
		{Kind: SeedOrderInt64Priority, PriorityField: "priority"},
		{Kind: SeedOrderRandom, RandomSeed: 1},
	}
	for _, policy := range policies {
		t.Run(string(policy.Kind), func(t *testing.T) {
			node := seedBudgetTestNode(t, SeedSchedulerConfig{
				AttemptLimitPerProduceMatch: 1,
				AttemptLimitPerMatchRound:   3,
				Order:                       policy,
			})
			for index := 0; index < 1000; index++ {
				mustAdd(t, node, &Ticket{
					TicketID:    TicketID(index + 1),
					CreatedAt:   int64(1000 - index),
					Int64Values: map[string]int64{"priority": int64(index)},
				})
			}
			if err := node.BeginMatchRound(1); err != nil {
				t.Fatal(err)
			}
			if len(node.seedRound.order) != 3 {
				t.Fatalf("materialized seed order length=%d want=3", len(node.seedRound.order))
			}
			if cap(node.seedRound.order) > 3 && policy.Kind != SeedOrderArrival {
				t.Fatalf("materialized seed order cap=%d want<=3", cap(node.seedRound.order))
			}
		})
	}
}

func TestCustomSeedOrderReceivesFullCandidatesAndMaxSeeds(t *testing.T) {
	var candidateCount, maxSeeds int
	policy := FuncSeedOrderPolicy(func(ctx SeedOrderContext) ([]TicketID, error) {
		candidateCount = len(ctx.Candidates)
		maxSeeds = ctx.MaxSeeds
		// Pick the last two candidates to prove that the policy can select from
		// the complete pool instead of only the first two arrival entries.
		return []TicketID{
			ctx.Candidates[len(ctx.Candidates)-2].TicketID,
			ctx.Candidates[len(ctx.Candidates)-1].TicketID,
		}, nil
	})
	node := seedBudgetTestNode(t, SeedSchedulerConfig{
		AttemptLimitPerProduceMatch: 1,
		AttemptLimitPerMatchRound:   2,
	})
	node.seedOrderPolicy = policy
	for _, ticketID := range []string{"a", "b", "c", "d"} {
		mustAdd(t, node, &Ticket{TicketID: testTicketID(ticketID)})
	}
	if err := node.BeginMatchRound(1); err != nil {
		t.Fatal(err)
	}
	if candidateCount != 4 || maxSeeds != 2 || len(node.seedRound.order) != 2 {
		t.Fatalf("custom policy candidates=%d maxSeeds=%d order=%d want=4,2,2", candidateCount, maxSeeds, len(node.seedRound.order))
	}
}

func TestCustomSeedOrderRejectsUnknownTicketID(t *testing.T) {
	policy := FuncSeedOrderPolicy(func(ctx SeedOrderContext) ([]TicketID, error) {
		return []TicketID{ctx.Candidates[0].TicketID, testTicketID("unknown")}, nil
	})
	node := seedBudgetTestNode(t, SeedSchedulerConfig{
		AttemptLimitPerProduceMatch: 1,
		AttemptLimitPerMatchRound:   2,
	})
	node.seedOrderPolicy = policy
	mustAdd(t, node, &Ticket{TicketID: testTicketID("a")})
	mustAdd(t, node, &Ticket{TicketID: testTicketID("b")})
	mustAdd(t, node, &Ticket{TicketID: testTicketID("c")})
	if err := node.BeginMatchRound(1); err == nil {
		t.Fatal("custom policy returned an unknown TicketID")
	}
}

func TestCustomSeedOrderCannotExceedMaxSeeds(t *testing.T) {
	policy := FuncSeedOrderPolicy(func(ctx SeedOrderContext) ([]TicketID, error) {
		return seedTicketIDs(ctx.Candidates), nil
	})
	node := seedBudgetTestNode(t, SeedSchedulerConfig{
		AttemptLimitPerProduceMatch: 1,
		AttemptLimitPerMatchRound:   2,
	})
	node.seedOrderPolicy = policy
	mustAdd(t, node, &Ticket{TicketID: testTicketID("a")})
	mustAdd(t, node, &Ticket{TicketID: testTicketID("b")})
	mustAdd(t, node, &Ticket{TicketID: testTicketID("c")})
	if err := node.BeginMatchRound(1); err == nil {
		t.Fatal("custom policy exceeded MaxSeeds")
	}
}

func seedBudgetTestNode(t *testing.T, scheduler SeedSchedulerConfig) *LogicalNode {
	t.Helper()
	node, err := NewLogicalNode(LogicalNodeSpec{
		Key: identity.LogicalNodeKey{
			Rule:        identity.RuleKey{RuleID: 1},
			PlacementID: "seed-budget",
		},
		Config: LogicalNodeConfig{
			SeedScheduler: scheduler,
			Prefilter:     prefilter.Config{Root: prefilter.None()},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return node
}
