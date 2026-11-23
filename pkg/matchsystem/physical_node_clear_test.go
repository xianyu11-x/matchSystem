package matchsystem

import (
	"context"
	"errors"
	"testing"

	"github.com/xianyu11-x/matchSystem/pkg/identity"
)

func TestPhysicalNodeClearTicketsPreservesDeploymentsAndRound(t *testing.T) {
	ctx := context.Background()
	physical, err := NewPhysicalNode(1)
	if err != nil {
		t.Fatal(err)
	}
	firstKey := identity.LogicalNodeKey{Rule: identity.RuleKey{RuleID: 1}, PlacementID: 11}
	secondKey := identity.LogicalNodeKey{Rule: identity.RuleKey{RuleID: 2}, PlacementID: 22}
	first, second := newReconfigureLogicalNode(t, firstKey), newReconfigureLogicalNode(t, secondKey)
	if err := physical.Reconfigure([]identity.LogicalNodeKey{firstKey, secondKey}, []*LogicalNode{first, second}, nil); err != nil {
		t.Fatal(err)
	}
	for _, key := range []identity.LogicalNodeKey{firstKey, secondKey} {
		owner := identity.OwnerRef{PhysicalNodeID: 1, LogicalNode: key}
		for _, id := range []TicketID{1, 2} {
			if err := physical.Add(ctx, owner, testTicket(id)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := physical.BeginMatchRound(ctx, 100); err != nil {
		t.Fatal(err)
	}
	if first.nextSeed() == nil {
		t.Fatal("没有生成已占用的种子")
	}
	firstRound, secondRound := first.seedRound, second.seedRound

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if removed, err := physical.ClearTickets(canceled); !errors.Is(err, context.Canceled) || removed != 0 || first.Len() != 2 || second.Len() != 2 {
		t.Fatalf("取消后的队列发生变化: removed=%d err=%v", removed, err)
	}
	removed, err := physical.ClearTickets(ctx)
	if err != nil || removed != 4 {
		t.Fatalf("清空结果不正确: removed=%d err=%v", removed, err)
	}
	for _, node := range []*LogicalNode{first, second} {
		if node.Len() != 0 || node.store.prefilterStore.Len() != 0 || len(node.store.oldestTickets) != 0 || node.seedOrderRuntime.HasNext() {
			t.Fatal("清空后核心队列、预筛选、最早时间堆或种子索引仍有 Ticket")
		}
	}
	if !physical.roundActive || physical.roundNow != 100 || first.seedRound != firstRound || second.seedRound != secondRound || len(physical.Describe()) != 2 {
		t.Fatal("清空改变了部署或活动轮次")
	}
	if removed, err := physical.ClearTickets(ctx); err != nil || removed != 0 {
		t.Fatalf("重复清空失败: removed=%d err=%v", removed, err)
	}
	owner := identity.OwnerRef{PhysicalNodeID: 1, LogicalNode: firstKey}
	if err := physical.Add(ctx, owner, testTicket(1)); err != nil {
		t.Fatalf("无法重新添加相同 Ticket ID: %v", err)
	}
	if err := physical.BeginMatchRound(ctx, 200); err != nil {
		t.Fatal(err)
	}
	result, err := physical.ProduceMatch(ctx)
	if err != nil || result.Match == nil || len(result.Match.Tickets) != 1 || result.Match.Tickets[0].TicketID != 1 {
		t.Fatalf("清空后重新加入的 Ticket 不能匹配: result=%+v err=%v", result, err)
	}
}
