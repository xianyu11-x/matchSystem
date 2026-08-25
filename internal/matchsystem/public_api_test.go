package matchsystem_test

import (
	"context"
	"testing"

	"matchSystem/internal/common"
	"matchSystem/internal/identity"
	ms "matchSystem/internal/matchsystem"
	pf "matchSystem/internal/matchsystem/prefilter"
)

func TestPublicExtensionSurface(t *testing.T) {
	config := pf.Config{
		Indexes: []pf.IndexSpec{pf.NewInt64RangeIndex(pf.Int64RangeIndexConfig{Name: "value_index", Field: "value"})},
		Root:    pf.Lookup(pf.Int64RangeQuery{Index: "value_index", Min: pf.LiteralInt64(0), Max: pf.LiteralInt64(10)}),
	}
	start := ms.FuncGroupEvaluator{EvaluatorFlagsValue: ms.GroupEvaluatorStart, AllowFn: func(_ ms.GroupEvaluatorContext, group []*ms.Ticket, _ *ms.Ticket) bool { return len(group) >= 2 }}
	node, err := ms.NewLogicalNode(ms.LogicalNodeSpec{
		Key:    identity.LogicalNodeKey{Rule: identity.RuleKey{RuleID: "public-api"}, PlacementID: "test"},
		Config: ms.LogicalNodeConfig{MaxPlayers: 2, Prefilter: config},
		Rules:  ms.NewRuleSet(start),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = node.Add(&ms.Ticket{TicketID: "a", Int64Values: map[string]int64{"value": 1}}); err != nil {
		t.Fatal(err)
	}
	if _, err = node.Add(&ms.Ticket{TicketID: "b", Int64Values: map[string]int64{"value": 2}}); err != nil {
		t.Fatal(err)
	}
	if err := node.BeginMatchRound(1); err != nil {
		t.Fatal(err)
	}
	match, err := node.ProduceMatch(ms.Facts{})
	if err != nil || match == nil {
		t.Fatalf("expected one match, got %#v, err=%v", match, err)
	}
}

func TestPublicPhysicalNodeSurface(t *testing.T) {
	rules := ms.NewRuleSet(ms.FuncGroupEvaluator{
		EvaluatorFlagsValue: ms.GroupEvaluatorForceStart,
		AllowFn:             func(ms.GroupEvaluatorContext, []*ms.Ticket, *ms.Ticket) bool { return true },
	})
	physical, err := ms.NewPhysicalNode("physical-a")
	if err != nil {
		t.Fatal(err)
	}
	key := identity.LogicalNodeKey{Rule: identity.RuleKey{RuleID: "ranked"}, PlacementID: "placement-a"}
	if err := physical.Load(context.Background(), ms.LogicalNodeSpec{
		Key:    key,
		Config: ms.LogicalNodeConfig{Prefilter: pf.Config{Root: pf.None()}},
		Rules:  rules,
	}); err != nil {
		t.Fatal(err)
	}
	owner := identity.OwnerRef{LogicalNode: key, PhysicalNodeID: physical.ID()}
	if _, err := physical.Add(context.Background(), owner, &common.Ticket{TicketID: "ticket"}); err != nil {
		t.Fatal(err)
	}
	if err := physical.BeginMatchRound(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	result, err := physical.ProduceMatch(context.Background())
	if err != nil || result.Match == nil || result.LogicalNode != key {
		t.Fatalf("unexpected PhysicalNode result: result=%#v err=%v", result, err)
	}
}
