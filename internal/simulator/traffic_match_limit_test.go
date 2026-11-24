package simulator

import (
	"context"
	"testing"
	"time"

	"github.com/xianyu11-x/matchSystem/pkg/identity"
)

func TestFixedTrafficZeroOnlyInjects(t *testing.T) {
	scenario, key := testScenario()
	sim, err := NewService(scenario)
	if err != nil {
		t.Fatal(err)
	}
	defer sim.Close()
	config := TrafficConfig{
		Mode: TrafficModeFixed, Distribution: "constant", Rate: 10,
		MatchIntervalMS: 100, MaxMatches: 0, Steps: 4, StepMS: 100, StartTimeMS: 1000,
	}
	if _, err := sim.StartTraffic(config, BatchGeneratorSpec{Rule: key.Rule, FirstTicketID: 10}); err != nil {
		t.Fatal(err)
	}
	status := awaitTraffic(t, sim, func(status TrafficStatus) bool { return status.State == "completed" || status.State == "failed" })
	if status.State != "completed" || status.Injected != 3 || status.Rounds != 0 || status.Produced != 0 || status.Config.MaxMatches != 0 {
		t.Fatalf("零配额固定流量状态不正确: %+v", status)
	}
	page, err := sim.ListTickets(context.Background(), TicketQuery{Limit: 100})
	if err != nil || page.Total != 3 {
		t.Fatalf("固定流量应保留全部注入票据: %+v, %v", page, err)
	}
	result, err := sim.RunRound(context.Background(), 2000, 1)
	if err != nil || result.Round != 1 || len(result.Matches) != 1 {
		t.Fatalf("零配额流量后手动轮次不正确: %+v, %v", result, err)
	}
}

func TestRealtimeTrafficZeroOnlyInjects(t *testing.T) {
	scenario, key := testScenario()
	sim, err := NewService(scenario)
	if err != nil {
		t.Fatal(err)
	}
	defer sim.Close()
	config := TrafficConfig{Distribution: "constant", Rate: 100, MatchIntervalMS: 100, MaxMatches: 0}
	if _, err := sim.StartTraffic(config, BatchGeneratorSpec{Rule: key.Rule, FirstTicketID: 100}); err != nil {
		t.Fatal(err)
	}
	status := awaitTraffic(t, sim, func(status TrafficStatus) bool { return status.Injected >= 15 || status.State == "failed" })
	status = sim.StopTraffic()
	if status.State != "stopped" || status.Injected < 15 || status.Rounds != 0 || status.Produced != 0 {
		t.Fatalf("零配额实时流量状态不正确: %+v", status)
	}
	page, err := sim.ListTickets(context.Background(), TicketQuery{Limit: 100})
	if err != nil || uint64(page.Total) != status.Injected {
		t.Fatalf("实时流量应保留全部注入票据: %+v, %v", page, err)
	}
}

func TestFixedTrafficAppliesLimitPerPhysicalNode(t *testing.T) {
	scenario, owners := perNodeLimitScenario()
	sim, err := NewService(scenario)
	if err != nil {
		t.Fatal(err)
	}
	defer sim.Close()
	for i, owner := range owners {
		addPerNodeLimitTickets(t, sim, owner, uint64(100+i*100), 2)
	}
	config := TrafficConfig{
		Mode: TrafficModeFixed, Distribution: "constant", Rate: 1,
		MatchIntervalMS: 100, MaxMatches: 1, Steps: 1, StepMS: 100, StartTimeMS: 1000,
	}
	if _, err := sim.StartTraffic(config, BatchGeneratorSpec{Rule: owners[0].LogicalNode.Rule, FirstTicketID: 1000}); err != nil {
		t.Fatal(err)
	}
	status := awaitTraffic(t, sim, func(status TrafficStatus) bool { return status.State == "completed" || status.State == "failed" })
	if status.State != "completed" || status.Injected != 0 || status.Rounds != 1 || status.Produced != 2 {
		t.Fatalf("每节点独立限额未应用于固定流量: %+v", status)
	}
	page, err := sim.ListMatches(context.Background(), MatchQuery{Limit: 100})
	if err != nil || page.Total != 2 {
		t.Fatalf("固定流量成局节点不正确: %+v, %v", page, err)
	}
	counts := make(map[identity.PhysicalNodeID]int)
	for _, match := range page.Items {
		counts[match.PhysicalNodeID]++
	}
	if counts[owners[0].PhysicalNodeID] != 1 || counts[owners[1].PhysicalNodeID] != 1 {
		t.Fatalf("固定流量各节点成局数不正确: %v", counts)
	}
}

func TestRealtimeTrafficAppliesLimitPerPhysicalNode(t *testing.T) {
	scenario, owners := perNodeLimitScenario()
	sim, err := NewService(scenario)
	if err != nil {
		t.Fatal(err)
	}
	defer sim.Close()
	for i, owner := range owners {
		addPerNodeLimitTickets(t, sim, owner, uint64(100+i*100), 3)
	}
	config := TrafficConfig{Distribution: "constant", Rate: 0.001, MatchIntervalMS: 100, MaxMatches: 1}
	if _, err := sim.StartTraffic(config, BatchGeneratorSpec{Rule: owners[0].LogicalNode.Rule, FirstTicketID: 1000}); err != nil {
		t.Fatal(err)
	}
	status := awaitTraffic(t, sim, func(status TrafficStatus) bool { return status.Rounds >= 1 || status.State == "failed" })
	status = sim.StopTraffic()
	if status.State != "stopped" || status.Rounds < 1 || status.Injected != 0 {
		t.Fatalf("实时流量未完成首轮: %+v", status)
	}
	page, err := sim.ListMatches(context.Background(), MatchQuery{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	firstRound := make(map[identity.PhysicalNodeID]int)
	for _, match := range page.Items {
		if match.Round == 1 {
			firstRound[match.PhysicalNodeID]++
		}
	}
	if len(firstRound) != 2 || firstRound[owners[0].PhysicalNodeID] != 1 || firstRound[owners[1].PhysicalNodeID] != 1 {
		t.Fatalf("实时流量首轮各节点成局数不正确: %v", firstRound)
	}
}

func TestRealtimeTrafficDeadlineIgnoresMatchTimerWhenDisabled(t *testing.T) {
	base := time.Unix(0, 0)
	arrival := base.Add(10 * time.Second)
	match := base.Add(time.Second)
	if got := nextTrafficDeadline(arrival, match, false); !got.Equal(arrival) {
		t.Fatalf("禁用匹配时不应因匹配定时器唤醒: %v", got)
	}
	if got := nextTrafficDeadline(arrival, match, true); !got.Equal(match) {
		t.Fatalf("启用匹配时应使用较早的匹配期限: %v", got)
	}
}
