package simulator

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/xianyu11-x/matchSystem/pkg/client"
	"github.com/xianyu11-x/matchSystem/pkg/common"
	"github.com/xianyu11-x/matchSystem/pkg/identity"
)

func trafficTargetScenario() (Scenario, identity.LogicalNodeKey, identity.LogicalNodeKey) {
	scenario, first := testScenario()
	secondRule := (Scenario{Rules: scenario.Rules}).Clone().Rules[0]
	secondRule.PhysicalNodeID = testPhysicalID("p2")
	secondRule.LogicalNode.PlacementID = testPlacementID("p2")
	scenario.PhysicalNodes = append(scenario.PhysicalNodes, NewPhysicalNodeSpec(secondRule.PhysicalNodeID, "inproc://p2"))
	scenario.Rules = append(scenario.Rules, secondRule)
	return scenario, first, secondRule.LogicalNode
}

func TestTrafficPlacementAndRuleRouting(t *testing.T) {
	for _, targeted := range []bool{true, false} {
		name := "按规则路由"
		if targeted {
			name = "指定节点"
		}
		t.Run(name, func(t *testing.T) {
			scenario, first, second := trafficTargetScenario()
			sim, err := NewSimulator(scenario)
			if err != nil {
				t.Fatal(err)
			}
			defer sim.Close()
			config := TrafficConfig{Mode: TrafficModeFixed, Distribution: "constant", Rate: 10,
				MatchIntervalMS: 10000, MaxMatches: 1, Steps: 41, StepMS: 100, StartTimeMS: 1000}
			spec := BatchGeneratorSpec{Rule: first.Rule, FirstTicketID: 100}
			// 同一 RuleID 的别名不同仍应定位到部署中的实际节点。
			spec.Rule.Namespace = "请求别名"
			if targeted {
				_, err = sim.StartTrafficAtPlacement(config, spec, second.PlacementID)
			} else {
				_, err = sim.StartTraffic(config, spec)
			}
			if err != nil {
				t.Fatal(err)
			}
			status := awaitTraffic(t, sim, func(status TrafficStatus) bool { return status.State != "running" })
			if status.State != "completed" || status.Injected != 40 || status.NextTicketID != 140 {
				t.Fatalf("流量状态不正确: %+v", status)
			}
			page, err := sim.ListTickets(context.Background(), TicketQuery{Limit: 100})
			if err != nil || len(page.Items) != 40 {
				t.Fatalf("读取注入对象失败: %+v, %v", page, err)
			}
			seen := make(map[identity.PlacementID]bool)
			for _, ticket := range page.Items {
				seen[ticket.Owner.LogicalNode.PlacementID] = true
				if targeted {
					if ticket.Owner.LogicalNode != second || ticket.Owner.PhysicalNodeID != testPhysicalID("p2") || ticket.Decision.DecisionID != 0 {
						t.Fatalf("未定向投递到选中节点: %+v", ticket)
					}
				} else {
					decision, err := sim.RouteNew(context.Background(), common.RouteRequest{Rule: spec.Rule, TicketID: ticket.TicketID})
					if err != nil || ticket.Decision != decision {
						t.Fatalf("规则路由与 Router 结果不同: %+v, %+v, %v", ticket.Decision, decision, err)
					}
				}
			}
			if !targeted && (!seen[first.PlacementID] || !seen[second.PlacementID]) {
				t.Fatalf("规则路由未使用两个节点: %+v", seen)
			}
		})
	}
}

func TestTrafficPlacementRejectsMissingOrDisabled(t *testing.T) {
	for _, test := range []struct {
		name      string
		placement identity.PlacementID
		ruleID    uint64
		disable   string
		want      error
	}{
		{name: "零位置", want: ErrInvalidBatchSpec},
		{name: "缺失节点", placement: testPlacementID("missing"), want: ErrUnknownRule},
		{name: "缺失规则", placement: testPlacementID("p2"), ruleID: 999, want: ErrUnknownRule},
		{name: "禁用物理节点", placement: testPlacementID("p2"), disable: "physical", want: client.ErrNoRoute},
		{name: "禁用逻辑节点", placement: testPlacementID("p2"), disable: "rule", want: client.ErrNoRoute},
	} {
		t.Run(test.name, func(t *testing.T) {
			scenario, first, _ := trafficTargetScenario()
			if test.disable == "physical" {
				scenario.PhysicalNodes[1].Enabled = false
			} else if test.disable == "rule" {
				scenario.Rules[1].Enabled = false
			}
			sim, err := NewSimulator(scenario)
			if err != nil {
				t.Fatal(err)
			}
			defer sim.Close()
			config := TrafficConfig{Distribution: "constant", Rate: 10, MatchIntervalMS: 100, MaxMatches: 1}
			rule := first.Rule
			if test.ruleID != 0 {
				rule.RuleID = test.ruleID
			}
			_, err = sim.StartTrafficAtPlacement(config, BatchGeneratorSpec{Rule: rule}, test.placement)
			if !errors.Is(err, test.want) || sim.Traffic().State != "idle" {
				t.Fatalf("不应启动无效目标: %v, %+v", err, sim.Traffic())
			}
		})
	}
}

func TestTrafficPlacementDoesNotRerouteAfterTargetChanges(t *testing.T) {
	for _, change := range []string{"移除", "禁用物理节点", "禁用逻辑节点"} {
		t.Run(change, func(t *testing.T) {
			scenario, first, second := trafficTargetScenario()
			sim, err := NewSimulator(scenario)
			if err != nil {
				t.Fatal(err)
			}
			defer sim.Close()
			config := TrafficConfig{Distribution: "constant", Rate: 100, MatchIntervalMS: 10000, MaxMatches: 1}
			if _, err := sim.StartTrafficAtPlacement(config, BatchGeneratorSpec{Rule: first.Rule, FirstTicketID: 100}, second.PlacementID); err != nil {
				t.Fatal(err)
			}
			awaitTraffic(t, sim, func(status TrafficStatus) bool { return status.Injected >= 2 })
			ctx := context.Background()
			if change == "移除" {
				err = sim.RemovePhysicalNode(ctx, testPhysicalID("p2"))
			} else {
				physical := scenario.PhysicalNodes[1]
				if change == "禁用物理节点" {
					physical.Enabled = false
				}
				deployment := RuleDeploymentSpec{LogicalNode: second, Weight: 1, Enabled: change != "禁用逻辑节点", KeepTickFacts: true}
				err = sim.UpdatePhysicalNodeFromLibrary(ctx, physical, []RuleDeploymentSpec{deployment})
			}
			if err != nil {
				t.Fatal(err)
			}
			failed := awaitTraffic(t, sim, func(status TrafficStatus) bool { return status.State == "failed" })
			want := client.ErrNoRoute
			if change == "移除" {
				want = client.ErrOwnerNotFound
			}
			if !strings.Contains(failed.Error, want.Error()) || failed.NextTicketID != 100+failed.Injected {
				t.Fatalf("目标改变后的状态不正确: %+v", failed)
			}
			page, err := sim.ListTickets(ctx, TicketQuery{PhysicalNodeID: testPhysicalID("p1"), Limit: 100})
			if err != nil || page.Total != 0 {
				t.Fatalf("流量意外转发到其他节点: %+v, %v", page, err)
			}
		})
	}
}
