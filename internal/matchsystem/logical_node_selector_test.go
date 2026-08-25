package matchsystem

import (
	"testing"

	"matchSystem/internal/identity"
)

func TestRoundRobinLogicalNodeSelectorSkipsIneligibleNodes(t *testing.T) {
	selector := NewRoundRobinLogicalNodeSelector()
	ctx := LogicalNodeSelectContext{Candidates: []LogicalNodeCandidate{
		{Key: selectorTestKey(1), Eligible: true},
		{Key: selectorTestKey(2), Eligible: false},
		{Key: selectorTestKey(3), Eligible: true},
	}}
	want := []identity.LogicalNodeKey{selectorTestKey(1), selectorTestKey(3), selectorTestKey(1)}
	for index, expected := range want {
		actual, err := selector.Select(ctx)
		if err != nil || actual != expected {
			t.Fatalf("selection %d: key=%s err=%v want=%s", index, actual, err, expected)
		}
	}
}

func TestSmoothWeightedRoundRobinLogicalNodeSelectorDistribution(t *testing.T) {
	a := selectorTestKey(1)
	b := selectorTestKey(2)
	selector, err := NewSmoothWeightedRoundRobinLogicalNodeSelector(map[identity.RuleKey]uint32{
		a.Rule: 1,
		b.Rule: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := LogicalNodeSelectContext{Candidates: []LogicalNodeCandidate{
		{Key: a, Eligible: true},
		{Key: b, Eligible: true},
	}}
	counts := map[identity.LogicalNodeKey]int{}
	for range 8 {
		key, err := selector.Select(ctx)
		if err != nil {
			t.Fatal(err)
		}
		counts[key]++
	}
	if counts[a] != 2 || counts[b] != 6 {
		t.Fatalf("weighted distribution=%v", counts)
	}
}

func TestQueueAndOldestLogicalNodeSelectors(t *testing.T) {
	a := selectorTestKey(1)
	b := selectorTestKey(2)
	ctx := LogicalNodeSelectContext{Candidates: []LogicalNodeCandidate{
		{Key: a, Eligible: true, TicketCount: 5, OldestCreatedAt: 20},
		{Key: b, Eligible: true, TicketCount: 2, OldestCreatedAt: 10},
	}}
	if key, err := NewLargestQueueLogicalNodeSelector().Select(ctx); err != nil || key != a {
		t.Fatalf("largest queue selected %s, err=%v", key, err)
	}
	if key, err := NewOldestWaitingLogicalNodeSelector().Select(ctx); err != nil || key != b {
		t.Fatalf("oldest waiting selected %s, err=%v", key, err)
	}
}

func TestPhysicalNodeUsesInjectedLogicalNodeSelector(t *testing.T) {
	physical, err := NewPhysicalNode("physical-a", WithLogicalNodeSelector(NewLargestQueueLogicalNodeSelector()))
	if err != nil {
		t.Fatal(err)
	}
	small := logicalSpec(identity.RuleKey{RuleID: 1}, "p1", true)
	large := logicalSpec(identity.RuleKey{RuleID: 2}, "p1", true)
	mustLoad(t, physical, small)
	mustLoad(t, physical, large)
	mustAddTicket(t, physical, owner(physical.ID(), small.Key), "small-a")
	mustAddTicket(t, physical, owner(physical.ID(), large.Key), "large-a")
	mustAddTicket(t, physical, owner(physical.ID(), large.Key), "large-b")
	if err := physical.BeginMatchRound(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	result, err := physical.ProduceMatch(t.Context())
	if err != nil || result.LogicalNode != large.Key {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func selectorTestKey(ruleID identity.RuleID) identity.LogicalNodeKey {
	return identity.LogicalNodeKey{
		Rule:        identity.RuleKey{RuleID: ruleID},
		PlacementID: "test-placement",
	}
}
