package matchsystem

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/xianyu11-x/matchSystem/pkg/identity"
)

func TestPhysicalNodeRouteSnapshotTracksLifecycle(t *testing.T) {
	ctx := context.Background()
	const physicalID identity.PhysicalNodeID = 42
	physical, err := NewPhysicalNode(physicalID)
	if err != nil {
		t.Fatal(err)
	}
	assertSnapshot := func(want ...LogicalNodeDescriptor) PhysicalNodeRouteSnapshot {
		t.Helper()
		got := physical.RouteSnapshot()
		if got.PhysicalNodeID != physicalID || !slices.Equal(got.LogicalNodes, want) {
			t.Fatalf("路由快照不符合预期: got=%+v wantID=%d wantNodes=%+v", got, physicalID, want)
		}
		return got
	}

	empty := assertSnapshot()
	keys := []identity.LogicalNodeKey{
		{Rule: identity.RuleKey{Namespace: "第一组", RuleID: 3}, PlacementID: 33},
		{Rule: identity.RuleKey{Namespace: "第二组", RuleID: 1}, PlacementID: 11},
		{Rule: identity.RuleKey{Namespace: "第三组", RuleID: 2}, PlacementID: 22},
	}
	for _, key := range keys {
		spec := LogicalNodeSpec{
			Key: key,
			RuleJSON: testRuleJSON(t, key.Rule, integrationEmptyContractJSON,
				integrationNonePrefilterJSON, integrationCompleteEvaluationJSON,
				logicalNodeConfig{MaxPlayers: 1}),
		}
		if err := physical.Load(ctx, spec); err != nil {
			t.Fatalf("加载逻辑节点 %s: %v", key, err)
		}
	}
	if len(empty.LogicalNodes) != 0 {
		t.Fatalf("加载节点修改了已有的空快照: %+v", empty)
	}
	first := LogicalNodeDescriptor{Key: keys[0], State: LogicalNodeReady}
	second := LogicalNodeDescriptor{Key: keys[1], State: LogicalNodeReady}
	third := LogicalNodeDescriptor{Key: keys[2], State: LogicalNodeReady}
	assertSnapshot(first, second, third)

	firstOwner := identity.OwnerRef{PhysicalNodeID: physicalID, LogicalNode: keys[0]}
	secondOwner := identity.OwnerRef{PhysicalNodeID: physicalID, LogicalNode: keys[1]}
	for _, item := range []struct {
		owner identity.OwnerRef
		id    TicketID
	}{
		{firstOwner, 101},
		{firstOwner, 102},
		{secondOwner, 201},
	} {
		if err := physical.Add(ctx, item.owner, testTicket(item.id)); err != nil {
			t.Fatalf("加入 Ticket %d: %v", item.id, err)
		}
	}
	first.TicketCount, second.TicketCount = 2, 1
	previous := assertSnapshot(first, second, third)
	removed, err := physical.Remove(ctx, firstOwner, 102)
	if err != nil || !removed {
		t.Fatalf("删除 Ticket 102 失败: removed=%t err=%v", removed, err)
	}
	first.TicketCount = 1
	assertSnapshot(first, second, third)
	if previous.LogicalNodes[0].TicketCount != 2 {
		t.Fatalf("删除 Ticket 修改了已有快照: %+v", previous)
	}

	if err := physical.BeginDrain(ctx, keys[0]); err != nil {
		t.Fatalf("开始排空: %v", err)
	}
	first.State = LogicalNodeDraining
	assertSnapshot(first, second, third)
	if err := physical.Stop(ctx, keys[0]); !errors.Is(err, ErrLogicalNodeNotEmpty) {
		t.Fatalf("非空节点停止应失败: %v", err)
	}
	assertSnapshot(first, second, third)
	if err := physical.BeginMatchRound(ctx, 100); err != nil {
		t.Fatalf("开始匹配轮次: %v", err)
	}
	result, err := physical.ProduceMatch(ctx)
	if err != nil || result.LogicalNode != keys[0] || result.Match == nil || len(result.Match.Tickets) != 1 || result.Match.Tickets[0].TicketID != 101 {
		t.Fatalf("排空节点应继续消费 Ticket: result=%+v err=%v", result, err)
	}
	first.TicketCount = 0
	assertSnapshot(first, second, third)
	if err := physical.Stop(ctx, keys[0]); err != nil {
		t.Fatalf("停止已排空节点: %v", err)
	}
	assertSnapshot(second, third)

	if err := physical.Reconfigure([]identity.LogicalNodeKey{keys[2], keys[1]}, nil, nil); err != nil {
		t.Fatalf("调整部署顺序: %v", err)
	}
	assertSnapshot(third, second)
	if removed, err := physical.ClearTickets(ctx); err != nil || removed != 1 {
		t.Fatalf("清空队列失败: removed=%d err=%v", removed, err)
	}
	second.TicketCount = 0
	assertSnapshot(third, second)
	for _, key := range []identity.LogicalNodeKey{keys[1], keys[2]} {
		if err := physical.Stop(ctx, key); err != nil {
			t.Fatalf("停止逻辑节点 %s: %v", key, err)
		}
	}
	assertSnapshot()
}

func TestPhysicalNodeRouteSnapshotIsDetached(t *testing.T) {
	ctx := context.Background()
	physical, err := NewPhysicalNode(42)
	if err != nil {
		t.Fatal(err)
	}
	key := identity.LogicalNodeKey{Rule: identity.RuleKey{Namespace: "原始别名", RuleID: 1}, PlacementID: 11}
	if err := physical.Load(ctx, LogicalNodeSpec{Key: key, RuleJSON: testRuleJSON(t, key.Rule,
		integrationEmptyContractJSON, integrationNonePrefilterJSON, integrationCompleteEvaluationJSON,
		logicalNodeConfig{MaxPlayers: 1})}); err != nil {
		t.Fatal(err)
	}
	owner := identity.OwnerRef{PhysicalNodeID: 42, LogicalNode: key}
	if err := physical.Add(ctx, owner, testTicket(101)); err != nil {
		t.Fatal(err)
	}

	first := physical.RouteSnapshot()
	second := physical.RouteSnapshot()
	first.PhysicalNodeID = 99
	first.LogicalNodes[0].Key.Rule.Namespace = "修改后的别名"
	first.LogicalNodes[0].Key.PlacementID = 99
	first.LogicalNodes[0].State = LogicalNodeStopped
	first.LogicalNodes[0].TicketCount = 99
	first.LogicalNodes = append(first.LogicalNodes, LogicalNodeDescriptor{})
	want := LogicalNodeDescriptor{Key: key, State: LogicalNodeReady, TicketCount: 1}
	if second.PhysicalNodeID != 42 || !slices.Equal(second.LogicalNodes, []LogicalNodeDescriptor{want}) {
		t.Fatalf("修改一个快照影响了另一个快照: %+v", second)
	}
	current := physical.RouteSnapshot()
	if current.PhysicalNodeID != 42 || !slices.Equal(current.LogicalNodes, []LogicalNodeDescriptor{want}) {
		t.Fatalf("修改快照影响了物理节点: %+v", current)
	}
	if removed, err := physical.Remove(ctx, owner, 101); err != nil || !removed {
		t.Fatalf("修改快照后无法访问原节点: removed=%t err=%v", removed, err)
	}
	if second.LogicalNodes[0].TicketCount != 1 {
		t.Fatalf("节点变化影响了已有快照: %+v", second)
	}
}
