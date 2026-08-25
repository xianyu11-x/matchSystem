package matchsystem

import (
	"context"
	"errors"
	"testing"

	"matchSystem/internal/common"
	"matchSystem/internal/identity"
	"matchSystem/internal/matchsystem/prefilter"
)

func TestPhysicalNodeEnforcesLocalRuleUniqueness(t *testing.T) {
	node := mustPhysicalNode(t, "physical-a")
	rule := identity.RuleKey{Namespace: "prod", RuleID: 1}
	mustLoad(t, node, logicalSpec(rule, "p1", true))
	if err := node.Load(context.Background(), logicalSpec(rule, "p2", true)); !errors.Is(err, ErrDuplicateRuleKey) {
		t.Fatalf("expected ErrDuplicateRuleKey, got %v", err)
	}

	other := mustPhysicalNode(t, "physical-b")
	if err := other.Load(context.Background(), logicalSpec(rule, "p2", true)); err != nil {
		t.Fatalf("same RuleKey on another PhysicalNode must be allowed: %v", err)
	}
}

func TestLogicalNodeTicketDataIsIsolated(t *testing.T) {
	node := mustPhysicalNode(t, "physical-a")
	ruleA := identity.RuleKey{RuleID: 1}
	ruleB := identity.RuleKey{RuleID: 2}
	specA := logicalSpec(ruleA, "p1", false)
	specB := logicalSpec(ruleB, "p1", false)
	mustLoad(t, node, specA)
	mustLoad(t, node, specB)
	ownerA := owner(node.ID(), specA.Key)
	ownerB := owner(node.ID(), specB.Key)

	ticketA := &common.Ticket{TicketID: testTicketID("same-id"), StringLists: map[string][]string{"source": {"a"}}}
	ticketB := &common.Ticket{TicketID: testTicketID("same-id"), StringLists: map[string][]string{"source": {"b"}}}
	if _, err := node.Add(context.Background(), ownerA, ticketA); err != nil {
		t.Fatal(err)
	}
	if _, err := node.Add(context.Background(), ownerB, ticketB); err != nil {
		t.Fatal(err)
	}
	if removed, err := node.Remove(context.Background(), ownerA, testTicketID("same-id")); err != nil || !removed {
		t.Fatalf("remove from A: removed=%v err=%v", removed, err)
	}
	stored, ok, err := node.Get(context.Background(), ownerB, testTicketID("same-id"))
	if err != nil || !ok {
		t.Fatalf("get from B: ok=%v err=%v", ok, err)
	}
	if stored.StringLists["source"][0] != "b" {
		t.Fatalf("logical node data leaked: %#v", stored)
	}
}

func TestPhysicalNodeAddAndGetKeepOwnershipBoundary(t *testing.T) {
	node := mustPhysicalNode(t, "physical-a")
	spec := logicalSpec(identity.RuleKey{RuleID: 1}, "p1", false)
	mustLoad(t, node, spec)
	ref := owner(node.ID(), spec.Key)
	source := &common.Ticket{
		TicketID:    testTicketID("ticket"),
		StringLists: map[string][]string{"mode": {"ranked"}},
	}
	if _, err := node.Add(context.Background(), ref, source); err != nil {
		t.Fatal(err)
	}
	source.StringLists["mode"][0] = "mutated-source"

	first, ok, err := node.Get(context.Background(), ref, source.TicketID)
	if err != nil || !ok {
		t.Fatalf("Get: ticket=%#v ok=%v err=%v", first, ok, err)
	}
	if first.StringLists["mode"][0] != "ranked" {
		t.Fatalf("Add exposed caller-owned data: %#v", first)
	}
	first.StringLists["mode"][0] = "mutated-get"
	first.StringLists["added"] = []string{"only-in-get-result"}
	second, ok, err := node.Get(context.Background(), ref, source.TicketID)
	if err != nil || !ok || second == first {
		t.Fatalf("Get did not return an independent copy: first=%p second=%p ok=%v err=%v", first, second, ok, err)
	}
	if second.StringLists["mode"][0] != "ranked" {
		t.Fatalf("mutating Get result changed pool state: %#v", second)
	}
	if _, exists := second.StringLists["added"]; exists {
		t.Fatalf("mutating Get result map changed pool state: %#v", second)
	}
}

func TestPhysicalMatchTransfersCommittedTicketFields(t *testing.T) {
	node := mustPhysicalNode(t, "physical-a")
	spec := logicalSpec(identity.RuleKey{RuleID: 1}, "p1", true)
	mustLoad(t, node, spec)
	ref := owner(node.ID(), spec.Key)
	if _, err := node.Add(context.Background(), ref, &common.Ticket{
		TicketID:    testTicketID("ticket"),
		StringLists: map[string][]string{"mode": {"ranked"}},
	}); err != nil {
		t.Fatal(err)
	}
	logical := node.nodes[spec.Key.Rule]
	stored, ok := logical.lookupTicket(testTicketID("ticket"))
	if !ok {
		t.Fatal("stored ticket not found")
	}
	if err := node.BeginMatchRound(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	result, err := node.ProduceMatch(context.Background())
	if err != nil || result.Match == nil {
		t.Fatalf("ProduceMatch: result=%#v err=%v", result, err)
	}
	if result.Match.Tickets[0] != stored {
		t.Fatal("committed Ticket pointer was cloned instead of transferred")
	}
	if _, exists := logical.lookupTicket(stored.TicketID); exists {
		t.Fatal("committed Ticket remained owned by LogicalNode")
	}
}

func TestPhysicalNodeProduceMatchUsesRoundRobinAndReturnsOneMatch(t *testing.T) {
	node := mustPhysicalNode(t, "physical-a")
	first := logicalSpec(identity.RuleKey{RuleID: 1}, "p1", true)
	second := logicalSpec(identity.RuleKey{RuleID: 2}, "p1", true)
	mustLoad(t, node, first)
	mustLoad(t, node, second)
	mustAddTicket(t, node, owner(node.ID(), first.Key), "ticket-first")
	mustAddTicket(t, node, owner(node.ID(), second.Key), "ticket-second")
	if err := node.BeginMatchRound(context.Background(), 100); err != nil {
		t.Fatal(err)
	}

	result1, err := node.ProduceMatch(context.Background())
	if err != nil || result1.Match == nil || result1.LogicalNode != first.Key {
		t.Fatalf("first ProduceMatch: result=%#v err=%v", result1, err)
	}
	if len(result1.Match.Tickets) != 1 || result1.Match.Tickets[0].TicketID != testTicketID("ticket-first") {
		t.Fatalf("unexpected first match: %#v", result1.Match)
	}
	result2, err := node.ProduceMatch(context.Background())
	if err != nil || result2.Match == nil || result2.LogicalNode != second.Key {
		t.Fatalf("second ProduceMatch: result=%#v err=%v", result2, err)
	}
}

func TestPhysicalNodeProduceMatchRequiresRound(t *testing.T) {
	node := mustPhysicalNode(t, "physical-a")
	if _, err := node.ProduceMatch(context.Background()); !errors.Is(err, ErrMatchRoundNotStarted) {
		t.Fatalf("ProduceMatch error=%v", err)
	}
}

func TestBeginMatchRoundDoesNotResetLogicalNodeSelection(t *testing.T) {
	node := mustPhysicalNode(t, "physical-a")
	first := logicalSpec(identity.RuleKey{RuleID: 1}, "p1", true)
	second := logicalSpec(identity.RuleKey{RuleID: 2}, "p1", true)
	mustLoad(t, node, first)
	mustLoad(t, node, second)
	mustAddTicket(t, node, owner(node.ID(), first.Key), "first-a")
	mustAddTicket(t, node, owner(node.ID(), first.Key), "first-b")
	mustAddTicket(t, node, owner(node.ID(), second.Key), "second-a")
	mustAddTicket(t, node, owner(node.ID(), second.Key), "second-b")

	if err := node.BeginMatchRound(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	result, err := node.ProduceMatch(context.Background())
	if err != nil || result.LogicalNode != first.Key {
		t.Fatalf("first ProduceMatch: result=%#v err=%v", result, err)
	}
	if err := node.BeginMatchRound(context.Background(), 2); err != nil {
		t.Fatal(err)
	}
	result, err = node.ProduceMatch(context.Background())
	if err != nil || result.LogicalNode != second.Key {
		t.Fatalf("selector should continue to second node after seed reset: result=%#v err=%v", result, err)
	}
}

func TestProduceMatchDoesNotReuseSeedBeforeCursorReset(t *testing.T) {
	node := mustPhysicalNode(t, "physical-a")
	spec := logicalSpec(identity.RuleKey{RuleID: 1}, "p1", false)
	spec.Config.SeedScheduler.AttemptLimitPerProduceMatch = 1
	seen := make(map[TicketID]int)
	spec.ObjectFactProvider = func(ticket *Ticket, _ int64, _ Facts) (Facts, error) {
		seen[ticket.TicketID]++
		return Facts{}, nil
	}
	mustLoad(t, node, spec)
	ref := owner(node.ID(), spec.Key)
	mustAddTicket(t, node, ref, "ticket-a")
	mustAddTicket(t, node, ref, "ticket-b")
	if err := node.BeginMatchRound(context.Background(), 1); err != nil {
		t.Fatal(err)
	}

	if result, err := node.ProduceMatch(context.Background()); err != nil || result.Match != nil {
		t.Fatalf("first ProduceMatch: result=%#v err=%v", result, err)
	}
	if result, err := node.ProduceMatch(context.Background()); err != nil || result.Match != nil {
		t.Fatalf("second ProduceMatch: result=%#v err=%v", result, err)
	}
	if _, err := node.ProduceMatch(context.Background()); !errors.Is(err, ErrNoLogicalNodeAvailable) {
		t.Fatalf("expected exhausted round, got %v", err)
	}
	if seen[testTicketID("ticket-a")] != 1 || seen[testTicketID("ticket-b")] != 1 {
		t.Fatalf("seeds were reused in one round: %#v", seen)
	}

	if err := node.BeginMatchRound(context.Background(), 2); err != nil {
		t.Fatal(err)
	}
	if _, err := node.ProduceMatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	if seen[testTicketID("ticket-a")] != 2 || seen[testTicketID("ticket-b")] != 1 {
		t.Fatalf("cursor reset did not start from first seed: %#v", seen)
	}
}

func TestExternalRoundCanControlProducedGroupCount(t *testing.T) {
	node := mustPhysicalNode(t, "physical-a")
	spec := logicalSpec(identity.RuleKey{RuleID: 1}, "p1", true)
	mustLoad(t, node, spec)
	ref := owner(node.ID(), spec.Key)
	for _, ticketID := range []string{"ticket-a", "ticket-b", "ticket-c"} {
		mustAddTicket(t, node, ref, ticketID)
	}

	if err := node.BeginMatchRound(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	results := make([]PhysicalMatchResult, 0, 2)
	for len(results) < cap(results) {
		result, err := node.ProduceMatch(context.Background())
		if errors.Is(err, ErrNoLogicalNodeAvailable) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if result.Match != nil {
			results = append(results, result)
		}
	}
	if len(results) != 2 || results[0].Match.Tickets[0].TicketID != testTicketID("ticket-a") || results[1].Match.Tickets[0].TicketID != testTicketID("ticket-b") {
		t.Fatalf("unexpected external round results: %#v", results)
	}
	stored, ok, err := node.Get(context.Background(), ref, testTicketID("ticket-c"))
	if err != nil || !ok || stored == nil {
		t.Fatalf("group limit should leave ticket-c: ticket=%#v ok=%v err=%v", stored, ok, err)
	}
}

func TestPhysicalNodeBuildsFactsInsideSelectedLogicalNode(t *testing.T) {
	node := mustPhysicalNode(t, "physical-a")
	spec := logicalSpec(identity.RuleKey{RuleID: 1}, "p1", true)
	spec.Config.Facts = []FactSpec{{Name: "capacity", Type: FactTypeInt64}}
	var observedNow int64
	spec.FactProvider = func(_ context.Context, now int64) (Facts, error) {
		observedNow = now
		return Facts{Int64Values: map[string]int64{"capacity": 10}}, nil
	}
	mustLoad(t, node, spec)
	mustAddTicket(t, node, owner(node.ID(), spec.Key), "ticket")
	if err := node.BeginMatchRound(context.Background(), 123); err != nil {
		t.Fatal(err)
	}
	if _, err := node.ProduceMatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	if observedNow != 123 {
		t.Fatalf("FactProvider observed now=%d", observedNow)
	}
}

func TestFactProviderRefreshesAfterEachCommittedMatch(t *testing.T) {
	node := mustPhysicalNode(t, "physical-a")
	spec := logicalSpec(identity.RuleKey{RuleID: 1}, "p1", true)
	spec.Config.Facts = []FactSpec{{Name: "remaining_capacity", Type: FactTypeInt64}}
	providerCalls := 0
	spec.FactProvider = func(_ context.Context, _ int64) (Facts, error) {
		providerCalls++
		return Facts{Int64Values: map[string]int64{"remaining_capacity": int64(3 - providerCalls)}}, nil
	}
	mustLoad(t, node, spec)
	ref := owner(node.ID(), spec.Key)
	mustAddTicket(t, node, ref, "ticket-a")
	mustAddTicket(t, node, ref, "ticket-b")
	if err := node.BeginMatchRound(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		result, err := node.ProduceMatch(context.Background())
		if err != nil || result.Match == nil {
			t.Fatalf("ProduceMatch: result=%#v err=%v", result, err)
		}
	}
	if providerCalls != 2 {
		t.Fatalf("FactProvider calls=%d want=2", providerCalls)
	}
}

func TestNoMatchDoesNotTrySecondLogicalNodeInSameProduceMatch(t *testing.T) {
	node := mustPhysicalNode(t, "physical-a")
	noMatch := logicalSpec(identity.RuleKey{RuleID: 1}, "p1", false)
	wouldMatch := logicalSpec(identity.RuleKey{RuleID: 2}, "p1", true)
	mustLoad(t, node, noMatch)
	mustLoad(t, node, wouldMatch)
	mustAddTicket(t, node, owner(node.ID(), noMatch.Key), "ticket-a")
	mustAddTicket(t, node, owner(node.ID(), wouldMatch.Key), "ticket-b")
	if err := node.BeginMatchRound(context.Background(), 100); err != nil {
		t.Fatal(err)
	}

	first, err := node.ProduceMatch(context.Background())
	if err != nil || first.LogicalNode != noMatch.Key || first.Match != nil {
		t.Fatalf("first ProduceMatch should stop at no-match node: result=%#v err=%v", first, err)
	}
	second, err := node.ProduceMatch(context.Background())
	if err != nil || second.LogicalNode != wouldMatch.Key || second.Match == nil {
		t.Fatalf("second ProduceMatch should select next node: result=%#v err=%v", second, err)
	}
}

func TestOwnerMustTargetThisPhysicalNode(t *testing.T) {
	node := mustPhysicalNode(t, "physical-a")
	spec := logicalSpec(identity.RuleKey{RuleID: 1}, "p1", false)
	mustLoad(t, node, spec)
	wrongOwner := owner("physical-b", spec.Key)
	_, err := node.Add(context.Background(), wrongOwner, &common.Ticket{TicketID: testTicketID("ticket")})
	if !errors.Is(err, ErrWrongPhysicalNode) {
		t.Fatalf("expected ErrWrongPhysicalNode, got %v", err)
	}
}

func TestDrainingRejectsNewAddAndAllowsExistingTicketToMatch(t *testing.T) {
	node := mustPhysicalNode(t, "physical-a")
	spec := logicalSpec(identity.RuleKey{RuleID: 1}, "p1", true)
	mustLoad(t, node, spec)
	ref := owner(node.ID(), spec.Key)
	mustAddTicket(t, node, ref, "existing")
	if err := node.BeginDrain(context.Background(), spec.Key); err != nil {
		t.Fatal(err)
	}
	if _, err := node.Add(context.Background(), ref, &common.Ticket{TicketID: testTicketID("new")}); !errors.Is(err, ErrLogicalNodeNotReady) {
		t.Fatalf("expected draining node to reject Add, got %v", err)
	}
	if err := node.Stop(context.Background(), spec.Key); !errors.Is(err, ErrLogicalNodeNotEmpty) {
		t.Fatalf("expected non-empty node to reject Stop, got %v", err)
	}
	if err := node.BeginMatchRound(context.Background(), 100); err != nil {
		t.Fatal(err)
	}
	result, err := node.ProduceMatch(context.Background())
	if err != nil || result.Match == nil {
		t.Fatalf("draining node did not finish existing ticket: result=%#v err=%v", result, err)
	}
	if err := node.Stop(context.Background(), spec.Key); err != nil {
		t.Fatalf("empty draining node did not stop: %v", err)
	}
}

func TestPreviouslySelectedNodeCannotExecuteAfterStop(t *testing.T) {
	node := mustPhysicalNode(t, "physical-a")
	spec := logicalSpec(identity.RuleKey{RuleID: 1}, "p1", false)
	mustLoad(t, node, spec)
	ref := owner(node.ID(), spec.Key)
	mustAddTicket(t, node, ref, "ticket")
	if err := node.BeginMatchRound(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	selected, _, err := node.selectLogicalNode()
	if err != nil {
		t.Fatal(err)
	}
	if removed, err := node.Remove(context.Background(), ref, testTicketID("ticket")); err != nil || !removed {
		t.Fatalf("remove: removed=%v err=%v", removed, err)
	}
	if err := node.Stop(context.Background(), spec.Key); err != nil {
		t.Fatal(err)
	}
	if _, err := selected.produceMatch(context.Background()); !errors.Is(err, ErrLogicalNodeNotReady) {
		t.Fatalf("stopped detached node executed: %v", err)
	}
}

func TestLogicalNodeCanBeCreatedDirectly(t *testing.T) {
	node, err := NewLogicalNode(logicalSpec(identity.RuleKey{RuleID: 1}, "p1", false))
	if err != nil {
		t.Fatal(err)
	}
	if node == nil {
		t.Fatal("NewLogicalNode returned nil")
	}
}

func mustPhysicalNode(t *testing.T, id identity.PhysicalNodeID) *PhysicalNode {
	t.Helper()
	node, err := NewPhysicalNode(id)
	if err != nil {
		t.Fatal(err)
	}
	return node
}

func logicalSpec(rule identity.RuleKey, placement identity.PlacementID, forceStart bool) LogicalNodeSpec {
	rules := NewRuleSet()
	if forceStart {
		rules.Use(FuncGroupEvaluator{
			EvaluatorFlagsValue: GroupEvaluatorForceStart,
			AllowFn: func(_ GroupEvaluatorContext, _ []*Ticket, _ *Ticket) bool {
				return true
			},
		})
	}
	return LogicalNodeSpec{
		Key: identity.LogicalNodeKey{Rule: rule, PlacementID: placement},
		Config: LogicalNodeConfig{
			Prefilter: prefilter.Config{Root: prefilter.None()},
		},
		Rules: rules,
	}
}

func mustLoad(t *testing.T, node *PhysicalNode, spec LogicalNodeSpec) {
	t.Helper()
	if err := node.Load(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
}

func mustAddTicket(t *testing.T, node *PhysicalNode, ref identity.OwnerRef, ticketID string) {
	t.Helper()
	if _, err := node.Add(context.Background(), ref, &common.Ticket{TicketID: testTicketID(ticketID)}); err != nil {
		t.Fatal(err)
	}
}

func owner(physical identity.PhysicalNodeID, logical identity.LogicalNodeKey) identity.OwnerRef {
	return identity.OwnerRef{LogicalNode: logical, PhysicalNodeID: physical}
}
