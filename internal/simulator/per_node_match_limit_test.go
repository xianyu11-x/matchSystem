package simulator

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"sort"
	"testing"

	"github.com/xianyu11-x/matchSystem/pkg/common"
	"github.com/xianyu11-x/matchSystem/pkg/identity"
	"github.com/xianyu11-x/matchSystem/pkg/matchsystem"
)

func perNodeLimitScenario() (Scenario, []identity.OwnerRef) {
	scenario, first := testScenario()
	secondPhysical := NewPhysicalNodeSpec(testPhysicalID("p2"), "inproc://p2")
	secondRule := (Scenario{Rules: scenario.Rules}).Clone().Rules[0]
	secondRule.PhysicalNodeID = secondPhysical.ID
	secondRule.LogicalNode.PlacementID = testPlacementID("p2")
	scenario.PhysicalNodes = append(scenario.PhysicalNodes, secondPhysical)
	scenario.Rules = append(scenario.Rules, secondRule)
	owners := []identity.OwnerRef{
		{PhysicalNodeID: scenario.PhysicalNodes[0].ID, LogicalNode: first},
		{PhysicalNodeID: secondPhysical.ID, LogicalNode: secondRule.LogicalNode},
	}
	sort.Slice(owners, func(i, j int) bool { return owners[i].PhysicalNodeID < owners[j].PhysicalNodeID })
	return scenario, owners
}

func addPerNodeLimitTickets(t *testing.T, sim *Simulator, owner identity.OwnerRef, firstID uint64, count int) {
	t.Helper()
	for i := 0; i < count; i++ {
		id := firstID + uint64(i)
		if _, err := sim.AddRoutedTicket(context.Background(), common.RouteDecision{Owner: owner}, TicketInput{Rule: owner.LogicalNode.Rule, TicketID: id, CreatedAt: int64(id)}); err != nil {
			t.Fatalf("添加 Ticket %d: %v", id, err)
		}
	}
}

func TestRunRoundUsesIndependentPhysicalNodeLimits(t *testing.T) {
	for _, test := range []struct {
		name     string
		counts   [2]int
		limit    int
		want     [2]int
		wantLeft int
	}{
		{name: "both nodes reach quota", counts: [2]int{3, 3}, limit: 2, want: [2]int{2, 2}, wantLeft: 2},
		{name: "first node exhausted", counts: [2]int{1, 4}, limit: 2, want: [2]int{1, 2}, wantLeft: 2},
		{name: "first node empty", counts: [2]int{0, 3}, limit: 2, want: [2]int{0, 2}, wantLeft: 1},
		{name: "manual zero remains unlimited", counts: [2]int{3, 3}, limit: 0, want: [2]int{3, 3}},
	} {
		t.Run(test.name, func(t *testing.T) {
			scenario, owners := perNodeLimitScenario()
			sim, err := NewService(scenario)
			if err != nil {
				t.Fatal(err)
			}
			defer sim.Close()
			for i, owner := range owners {
				addPerNodeLimitTickets(t, sim, owner, uint64(100+i*100), test.counts[i])
			}
			result, err := sim.RunRound(context.Background(), 1000, test.limit)
			if err != nil {
				t.Fatal(err)
			}
			counts := make(map[identity.PhysicalNodeID]int)
			for _, match := range result.Matches {
				counts[match.PhysicalNodeID]++
			}
			if counts[owners[0].PhysicalNodeID] != test.want[0] || counts[owners[1].PhysicalNodeID] != test.want[1] {
				t.Fatalf("各节点成局数=%v, 期望 %v", counts, test.want)
			}
			if test.counts == [2]int{3, 3} {
				var order []identity.PhysicalNodeID
				for _, match := range result.Matches {
					order = append(order, match.PhysicalNodeID)
				}
				var want []identity.PhysicalNodeID
				for i := 0; i < test.want[0]; i++ {
					want = append(want, owners[0].PhysicalNodeID, owners[1].PhysicalNodeID)
				}
				if !reflect.DeepEqual(order, want) {
					t.Fatalf("节点轮转顺序=%v, 期望 %v", order, want)
				}
			}
			remaining, err := sim.ListTickets(context.Background(), TicketQuery{Limit: 100})
			if err != nil || remaining.Total != test.wantLeft {
				t.Fatalf("剩余 Ticket=%+v, err=%v", remaining, err)
			}
		})
	}
}

func TestProduceAllSharesQuotaAcrossLogicalNodes(t *testing.T) {
	scenario, owners := perNodeLimitScenario()
	firstRule := scenario.Rules[0]
	secondRule := (Scenario{Rules: []RuleSpec{firstRule}}).Clone().Rules[0]
	secondRule.LogicalNode.Rule.RuleID = 2
	secondRule.RuleJSON = bytes.Replace(secondRule.RuleJSON, []byte(`"ruleId":1`), []byte(`"ruleId":2`), 1)
	scenario.Rules = append(scenario.Rules, secondRule)
	sim, err := NewService(scenario)
	if err != nil {
		t.Fatal(err)
	}
	defer sim.Close()
	firstOwner := identity.OwnerRef{PhysicalNodeID: firstRule.PhysicalNodeID, LogicalNode: firstRule.LogicalNode}
	secondOwner := identity.OwnerRef{PhysicalNodeID: secondRule.PhysicalNodeID, LogicalNode: secondRule.LogicalNode}
	addPerNodeLimitTickets(t, sim, firstOwner, 1, 2)
	addPerNodeLimitTickets(t, sim, secondOwner, 10, 2)
	other := owners[0]
	if other.PhysicalNodeID == firstOwner.PhysicalNodeID {
		other = owners[1]
	}
	addPerNodeLimitTickets(t, sim, other, 20, 2)
	if err := sim.BeginRound(context.Background(), 1000); err != nil {
		t.Fatal(err)
	}
	result, err := sim.ProduceAll(context.Background(), 1)
	if err != nil || len(result.Matches) != 2 {
		t.Fatalf("每节点最多一局，结果=%+v, err=%v", result, err)
	}
	counts := make(map[identity.PhysicalNodeID]int)
	for _, match := range result.Matches {
		counts[match.PhysicalNodeID]++
	}
	if counts[firstOwner.PhysicalNodeID] != 1 || counts[other.PhysicalNodeID] != 1 {
		t.Fatalf("逻辑部署未共享物理节点配额: %v", counts)
	}
}

func TestRunRoundPreservesPartialResultsAndCancellation(t *testing.T) {
	scenario, owners := perNodeLimitScenario()
	for i := range scenario.Rules {
		if scenario.Rules[i].PhysicalNodeID == owners[1].PhysicalNodeID {
			scenario.Rules[i].ObjectFactProvider = simulatorTestObjectProviderFor(func(_ *common.Ticket, _ int64, _ matchsystem.Facts, _ matchsystem.ObjectFactWriter) error {
				return errors.New("provider failure")
			}, simulatorTestObjectDescriptor("object_tag", 2))
		}
	}
	sim, err := NewService(scenario)
	if err != nil {
		t.Fatal(err)
	}
	defer sim.Close()
	for i, owner := range owners {
		addPerNodeLimitTickets(t, sim, owner, uint64(100+i), 1)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := sim.RunRound(ctx, 1000, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("已取消请求应返回取消: %v", err)
	}
	result, err := sim.RunRound(context.Background(), 1000, 1)
	if err == nil || len(result.Matches) != 1 || result.Matches[0].PhysicalNodeID != owners[0].PhysicalNodeID || result.Round != 1 {
		t.Fatalf("错误发生前的匹配结果未保留: result=%+v, err=%v", result, err)
	}
}
