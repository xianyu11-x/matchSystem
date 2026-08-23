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
	rule := identity.RuleKey{Namespace: "prod", RuleID: "ranked"}
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
	ruleA := identity.RuleKey{RuleID: "ranked"}
	ruleB := identity.RuleKey{RuleID: "casual"}
	specA := logicalSpec(ruleA, "p1", false)
	specB := logicalSpec(ruleB, "p1", false)
	mustLoad(t, node, specA)
	mustLoad(t, node, specB)
	ownerA := owner(node.ID(), specA.Key)
	ownerB := owner(node.ID(), specB.Key)

	ticketA := &common.Ticket{TicketID: "same-id", StringLists: map[string][]string{"source": {"a"}}}
	ticketB := &common.Ticket{TicketID: "same-id", StringLists: map[string][]string{"source": {"b"}}}
	if _, err := node.Add(context.Background(), ownerA, ticketA); err != nil {
		t.Fatal(err)
	}
	if _, err := node.Add(context.Background(), ownerB, ticketB); err != nil {
		t.Fatal(err)
	}
	if removed, err := node.Remove(context.Background(), ownerA, "same-id"); err != nil || !removed {
		t.Fatalf("remove from A: removed=%v err=%v", removed, err)
	}
	stored, ok, err := node.Get(context.Background(), ownerB, "same-id")
	if err != nil || !ok {
		t.Fatalf("get from B: ok=%v err=%v", ok, err)
	}
	if stored.StringLists["source"][0] != "b" {
		t.Fatalf("logical node data leaked: %#v", stored)
	}
}

func TestPhysicalNodeTickUsesRoundRobinAndReturnsOneMatch(t *testing.T) {
	node := mustPhysicalNode(t, "physical-a")
	first := logicalSpec(identity.RuleKey{RuleID: "first"}, "p1", true)
	second := logicalSpec(identity.RuleKey{RuleID: "second"}, "p1", true)
	mustLoad(t, node, first)
	mustLoad(t, node, second)
	mustAddCommon(t, node, owner(node.ID(), first.Key), "ticket-first")
	mustAddCommon(t, node, owner(node.ID(), second.Key), "ticket-second")

	result1, err := node.Tick(context.Background(), 100)
	if err != nil || result1.Match == nil || result1.LogicalNode != first.Key {
		t.Fatalf("first Tick: result=%#v err=%v", result1, err)
	}
	if len(result1.Match.Tickets) != 1 || result1.Match.Tickets[0].TicketID != "ticket-first" {
		t.Fatalf("unexpected first match: %#v", result1.Match)
	}
	result2, err := node.Tick(context.Background(), 101)
	if err != nil || result2.Match == nil || result2.LogicalNode != second.Key {
		t.Fatalf("second Tick: result=%#v err=%v", result2, err)
	}
}

func TestPhysicalNodeBuildsFactsInsideSelectedLogicalNode(t *testing.T) {
	node := mustPhysicalNode(t, "physical-a")
	spec := logicalSpec(identity.RuleKey{RuleID: "ranked"}, "p1", true)
	var observedNow int64
	spec.FactProvider = func(_ context.Context, now int64) (prefilter.Facts, error) {
		observedNow = now
		return prefilter.Facts{Int64Values: map[string]int64{"capacity": 10}}, nil
	}
	mustLoad(t, node, spec)
	mustAddCommon(t, node, owner(node.ID(), spec.Key), "ticket")
	if _, err := node.Tick(context.Background(), 123); err != nil {
		t.Fatal(err)
	}
	if observedNow != 123 {
		t.Fatalf("FactProvider observed now=%d", observedNow)
	}
}

func TestNoMatchDoesNotTrySecondLogicalNodeInSameTick(t *testing.T) {
	node := mustPhysicalNode(t, "physical-a")
	noMatch := logicalSpec(identity.RuleKey{RuleID: "no-match"}, "p1", false)
	wouldMatch := logicalSpec(identity.RuleKey{RuleID: "would-match"}, "p1", true)
	mustLoad(t, node, noMatch)
	mustLoad(t, node, wouldMatch)
	mustAddCommon(t, node, owner(node.ID(), noMatch.Key), "ticket-a")
	mustAddCommon(t, node, owner(node.ID(), wouldMatch.Key), "ticket-b")

	first, err := node.Tick(context.Background(), 100)
	if err != nil || first.LogicalNode != noMatch.Key || first.Match != nil {
		t.Fatalf("first Tick should stop at no-match node: result=%#v err=%v", first, err)
	}
	second, err := node.Tick(context.Background(), 101)
	if err != nil || second.LogicalNode != wouldMatch.Key || second.Match == nil {
		t.Fatalf("second Tick should select next node: result=%#v err=%v", second, err)
	}
}

func TestOwnerMustTargetThisPhysicalNode(t *testing.T) {
	node := mustPhysicalNode(t, "physical-a")
	spec := logicalSpec(identity.RuleKey{RuleID: "ranked"}, "p1", false)
	mustLoad(t, node, spec)
	wrongOwner := owner("physical-b", spec.Key)
	_, err := node.Add(context.Background(), wrongOwner, &common.Ticket{TicketID: "ticket"})
	if !errors.Is(err, ErrWrongPhysicalNode) {
		t.Fatalf("expected ErrWrongPhysicalNode, got %v", err)
	}
}

func TestDrainingRejectsNewAddAndAllowsExistingTicketToMatch(t *testing.T) {
	node := mustPhysicalNode(t, "physical-a")
	spec := logicalSpec(identity.RuleKey{RuleID: "ranked"}, "p1", true)
	mustLoad(t, node, spec)
	ref := owner(node.ID(), spec.Key)
	mustAddCommon(t, node, ref, "existing")
	if err := node.BeginDrain(context.Background(), spec.Key); err != nil {
		t.Fatal(err)
	}
	if _, err := node.Add(context.Background(), ref, &common.Ticket{TicketID: "new"}); !errors.Is(err, ErrLogicalNodeNotReady) {
		t.Fatalf("expected draining node to reject Add, got %v", err)
	}
	if err := node.Stop(context.Background(), spec.Key); !errors.Is(err, ErrLogicalNodeNotEmpty) {
		t.Fatalf("expected non-empty node to reject Stop, got %v", err)
	}
	result, err := node.Tick(context.Background(), 100)
	if err != nil || result.Match == nil {
		t.Fatalf("draining node did not finish existing ticket: result=%#v err=%v", result, err)
	}
	if err := node.Stop(context.Background(), spec.Key); err != nil {
		t.Fatalf("empty draining node did not stop: %v", err)
	}
}

func TestPreviouslySelectedNodeCannotExecuteAfterStop(t *testing.T) {
	node := mustPhysicalNode(t, "physical-a")
	spec := logicalSpec(identity.RuleKey{RuleID: "ranked"}, "p1", false)
	mustLoad(t, node, spec)
	ref := owner(node.ID(), spec.Key)
	mustAddCommon(t, node, ref, "ticket")
	selected, _, err := node.selectLogicalNode()
	if err != nil {
		t.Fatal(err)
	}
	if removed, err := node.Remove(context.Background(), ref, "ticket"); err != nil || !removed {
		t.Fatalf("remove: removed=%v err=%v", removed, err)
	}
	if err := node.Stop(context.Background(), spec.Key); err != nil {
		t.Fatal(err)
	}
	if _, err := selected.tickCommon(context.Background(), 1); !errors.Is(err, ErrLogicalNodeNotReady) {
		t.Fatalf("stopped detached node executed: %v", err)
	}
}

func TestLogicalNodeCanBeCreatedDirectly(t *testing.T) {
	node, err := NewLogicalNode(logicalSpec(identity.RuleKey{RuleID: "ranked"}, "p1", false))
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

func mustAddCommon(t *testing.T, node *PhysicalNode, ref identity.OwnerRef, ticketID string) {
	t.Helper()
	if _, err := node.Add(context.Background(), ref, &common.Ticket{TicketID: ticketID}); err != nil {
		t.Fatal(err)
	}
}

func owner(physical identity.PhysicalNodeID, logical identity.LogicalNodeKey) identity.OwnerRef {
	return identity.OwnerRef{LogicalNode: logical, PhysicalNodeID: physical}
}
