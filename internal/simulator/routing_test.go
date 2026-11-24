package simulator

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/xianyu11-x/matchSystem/pkg/client"
	"github.com/xianyu11-x/matchSystem/pkg/common"
	"github.com/xianyu11-x/matchSystem/pkg/identity"
)

func routingTestScenario() (Scenario, identity.LogicalNodeKey, identity.LogicalNodeKey) {
	scenario, first := libraryScenario()
	scenario.Rules[0].Weight = 10
	second := first
	second.PlacementID = testPlacementID("p2")
	physical := NewPhysicalNodeSpec(testPhysicalID("p2"), "inproc://route-second")
	rule := scenario.Rules[0]
	rule.PhysicalNodeID = physical.ID
	rule.LogicalNode = second
	rule.Weight = 1
	scenario.PhysicalNodes = append(scenario.PhysicalNodes, physical)
	scenario.Rules = append(scenario.Rules, rule)
	return scenario, first, second
}

func routingDecision(t *testing.T, sim *Simulator, rule identity.RuleKey) common.RouteDecision {
	t.Helper()
	decision, err := sim.RouteNew(context.Background(), common.RouteRequest{Rule: rule, TicketID: 999})
	if err != nil {
		t.Fatalf("路由失败: %v", err)
	}
	return decision
}

func awaitRoutingOwner(t *testing.T, sim *Simulator, rule identity.RuleKey, owner identity.PhysicalNodeID) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if routingDecision(t, sim, rule).Owner.PhysicalNodeID == owner {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("等待路由切换到物理节点 %d 超时", owner)
}

func routedDecision(physical PhysicalNodeSpec, logical identity.LogicalNodeKey) common.RouteDecision {
	return common.RouteDecision{
		DecisionID: 1, Owner: identity.OwnerRef{PhysicalNodeID: physical.ID, LogicalNode: logical}, Endpoint: physical.Endpoint,
	}
}

func TestRoutingPeriodicSnapshotsAndRecovery(t *testing.T) {
	ctx := context.Background()
	scenario, first, second := routingTestScenario()
	scenario.Routing = RoutingConfig{Algorithm: RoutingWeightedPriority, TicketLimit: 1}
	scenario.PhysicalNodes[0].RouteUpdateIntervalMS = 40
	sim, err := NewSimulator(scenario)
	if err != nil {
		t.Fatal(err)
	}
	defer sim.Close()
	p1, p2 := scenario.PhysicalNodes[0], scenario.PhysicalNodes[1]
	topology, err := sim.Topology(ctx)
	if err != nil {
		t.Fatal(err)
	}
	intervals := make(map[identity.PhysicalNodeID]int64)
	for _, node := range topology.PhysicalNodes {
		intervals[node.ID] = node.RouteUpdateIntervalMS
	}
	if intervals[p1.ID] != 40 || intervals[p2.ID] != DefaultRouteUpdateIntervalMS {
		t.Fatalf("物理节点更新间隔错误: %+v", intervals)
	}
	configured, err := sim.Scenario()
	if err != nil || configured.PhysicalNodes[1].RouteUpdateIntervalMS != 0 {
		t.Fatalf("默认间隔不应回写输入配置: %+v, %v", configured.PhysicalNodes, err)
	}

	// 同一锁内完成投递和选路，验证投递不会立即更新路由计数。
	sim.mu.Lock()
	_, addErr := sim.runtime.addTicket(ctx, TicketInput{Rule: first.Rule, TicketID: 1})
	immediate, routeErr := sim.runtime.router.RouteNew(ctx, common.RouteRequest{Rule: first.Rule, TicketID: 999})
	sim.mu.Unlock()
	if addErr != nil || routeErr != nil || immediate.Owner.PhysicalNodeID != p1.ID {
		t.Fatalf("上报周期前不应改变选择: %+v, %v, %v", immediate, addErr, routeErr)
	}
	awaitRoutingOwner(t, sim, first.Rule, p2.ID)
	if _, err := sim.AddRoutedTicket(ctx, routedDecision(p2, second), TicketInput{Rule: first.Rule, TicketID: 2}); err != nil {
		t.Fatal(err)
	}
	// 等待短周期节点再执行一次，默认周期节点仍保留旧计数。
	sim.mu.RLock()
	oldTimer := sim.runtime.routeTimers[p1.ID]
	sim.mu.RUnlock()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		sim.mu.RLock()
		changed := sim.runtime.routeTimers[p1.ID] != oldTimer
		sim.mu.RUnlock()
		if changed {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	sim.mu.RLock()
	changed := sim.runtime.routeTimers[p1.ID] != oldTimer
	sim.mu.RUnlock()
	if !changed || routingDecision(t, sim, first.Rule).Owner.PhysicalNodeID != p2.ID {
		t.Fatal("默认周期节点不应跟随其他节点的周期更新")
	}
	sim.mu.Lock()
	err = refreshPhysicalRoute(ctx, sim.runtime, p2.ID)
	sim.mu.Unlock()
	if err != nil || routingDecision(t, sim, first.Rule).Owner.PhysicalNodeID != p1.ID {
		t.Fatalf("默认周期节点主动上报后应采用其真实计数: %v", err)
	}
	if _, err := sim.AddRoutedTicket(ctx, routedDecision(p1, first), TicketInput{Rule: first.Rule, TicketID: 3}); err != nil {
		t.Fatal(err)
	}
	awaitRoutingOwner(t, sim, first.Rule, p2.ID)
	for _, id := range []uint64{1, 3} {
		if removed, err := sim.RemoveTicket(ctx, routedDecision(p1, first).Owner, id); err != nil || !removed {
			t.Fatalf("移除 Ticket %d 失败: %v", id, err)
		}
	}
	awaitRoutingOwner(t, sim, first.Rule, p1.ID)
}

func TestRoutingTopologyChangesAndTimerLifecycle(t *testing.T) {
	ctx := context.Background()
	scenario, first, second := routingTestScenario()
	scenario.Routing = RoutingConfig{Algorithm: RoutingWeightedPriority, TicketLimit: 1}
	p1, p2 := scenario.PhysicalNodes[0], scenario.PhysicalNodes[1]
	secondRule := scenario.Rules[1]
	scenario.PhysicalNodes = scenario.PhysicalNodes[:1]
	scenario.Rules = scenario.Rules[:1]
	sim, err := NewSimulator(scenario)
	if err != nil {
		t.Fatal(err)
	}
	defer sim.Close()
	if _, err := sim.AddTicket(ctx, TicketInput{Rule: first.Rule, TicketID: 1}); err != nil {
		t.Fatal(err)
	}
	if err := sim.AddPhysicalNode(ctx, p2, []RuleSpec{secondRule}); err != nil {
		t.Fatal(err)
	}
	if _, err := sim.ResolveOwner(routedDecision(p2, second).Owner); err != nil {
		t.Fatalf("新增节点应立即可解析: %v", err)
	}
	if got := routingDecision(t, sim, first.Rule); got.Owner.PhysicalNodeID != p1.ID {
		t.Fatalf("新增节点不应刷新其他节点的计数: %+v", got)
	}
	sim.mu.RLock()
	oldTimer := sim.runtime.routeTimers[p1.ID]
	sim.mu.RUnlock()
	invalid := deploymentFor(first, 0)
	changedPhysical := p1
	changedPhysical.Endpoint = "inproc://updated"
	changedPhysical.RouteUpdateIntervalMS = 250
	if err := sim.UpdatePhysicalNodeFromLibrary(ctx, changedPhysical, []RuleDeploymentSpec{invalid}); err == nil {
		t.Fatal("无效部署不应成功")
	}
	sim.mu.RLock()
	unchangedTimer := sim.runtime.routeTimers[p1.ID] == oldTimer
	sim.mu.RUnlock()
	if !unchangedTimer {
		t.Fatal("失败的更新重置了定时器")
	}
	if err := sim.UpdatePhysicalNodeFromLibrary(ctx, changedPhysical, []RuleDeploymentSpec{deploymentFor(first, 10)}); err != nil {
		t.Fatal(err)
	}
	if got := routingDecision(t, sim, first.Rule); got.Owner.PhysicalNodeID != p2.ID {
		t.Fatalf("节点更新应立即发布保留的真实队列计数: %+v", got)
	}
	if resolved, err := sim.ResolveOwner(routedDecision(p1, first).Owner); err != nil || resolved.Endpoint != changedPhysical.Endpoint {
		t.Fatalf("节点更新未发布新端点: %+v, %v", resolved, err)
	}
	if _, found, err := sim.GetTicket(ctx, routedDecision(p1, first).Owner, 1); err != nil || !found {
		t.Fatalf("节点更新丢失了未替换的队列: %v, %v", found, err)
	}
	sim.mu.RLock()
	reset := sim.runtime.routeTimers[p1.ID] != oldTimer
	retired := sim.runtime.routeTimers[p2.ID]
	runtime := sim.runtime
	sim.mu.RUnlock()
	if !reset {
		t.Fatal("成功的更新没有重置该节点的定时器")
	}

	// 排空中的部署只能解析，停止后的部署从快照中移除。
	sim.mu.Lock()
	err = runtime.nodes[p2.ID].BeginDrain(ctx, second)
	if err == nil {
		err = refreshPhysicalRoute(ctx, runtime, p2.ID)
	}
	sim.mu.Unlock()
	if err != nil || routingDecision(t, sim, first.Rule).Owner.PhysicalNodeID != p1.ID {
		t.Fatalf("排空节点仍参与新路由: %v", err)
	}
	if _, err := sim.ResolveOwner(routedDecision(p2, second).Owner); err != nil {
		t.Fatalf("排空节点应保留历史目标解析: %v", err)
	}
	sim.mu.Lock()
	err = runtime.nodes[p2.ID].Stop(ctx, second)
	if err == nil {
		err = refreshPhysicalRoute(ctx, runtime, p2.ID)
	}
	sim.mu.Unlock()
	if _, resolveErr := sim.ResolveOwner(routedDecision(p2, second).Owner); err != nil || !errors.Is(resolveErr, client.ErrOwnerNotFound) {
		t.Fatalf("停止节点仍可解析: %v, %v", err, resolveErr)
	}
	if err := sim.RemovePhysicalNode(ctx, p2.ID); err != nil {
		t.Fatal(err)
	}
	if err := sim.AddPhysicalNode(ctx, p2, []RuleSpec{secondRule}); err != nil {
		t.Fatal(err)
	}
	sim.mu.RLock()
	newTimer := runtime.routeTimers[p2.ID]
	sim.mu.RUnlock()
	sim.refreshRouteTimer(runtime, p2.ID, retired)
	sim.mu.RLock()
	stillCurrent := runtime.routeTimers[p2.ID] == newTimer
	sim.mu.RUnlock()
	if !stillCurrent {
		t.Fatal("已删除节点的旧回调影响了重建节点")
	}
	if err := sim.ReplaceScenario(ctx, scenario); err != nil {
		t.Fatal(err)
	}
	sim.refreshRouteTimer(runtime, p2.ID, newTimer)
	sim.mu.RLock()
	oldTimerCount := len(runtime.routeTimers)
	newRuntime := sim.runtime
	newEntry := newRuntime.routeTimers[p1.ID]
	sim.mu.RUnlock()
	if oldTimerCount != 0 {
		t.Fatal("替换场景未清除旧运行时定时器")
	}
	if err := sim.Close(); err != nil {
		t.Fatal(err)
	}
	sim.refreshRouteTimer(newRuntime, p1.ID, newEntry)
	if len(newRuntime.routeTimers) != 0 {
		t.Fatal("关闭后未清除定时器")
	}
}

func TestRoutingSnapshotFailureRetainsTableAndRetries(t *testing.T) {
	ctx := context.Background()
	scenario, first, _ := routingTestScenario()
	scenario.Routing = RoutingConfig{Algorithm: RoutingWeightedPriority, TicketLimit: 1}
	sim, err := NewSimulator(scenario)
	if err != nil {
		t.Fatal(err)
	}
	defer sim.Close()
	p1, p2 := scenario.PhysicalNodes[0], scenario.PhysicalNodes[1]
	if _, err := sim.AddRoutedTicket(ctx, routedDecision(p1, first), TicketInput{Rule: first.Rule, TicketID: 1}); err != nil {
		t.Fatal(err)
	}
	// 制造无法发布的快照；定时器仍须保留旧表并安排重试。
	sim.mu.Lock()
	runtime := sim.runtime
	runtime.scenario.Rules[0].Weight = 0
	entry := runtime.routeTimers[p1.ID]
	sim.mu.Unlock()
	sim.refreshRouteTimer(runtime, p1.ID, entry)
	sim.mu.Lock()
	next := runtime.routeTimers[p1.ID]
	runtime.scenario.Rules[0].Weight = 10
	sim.mu.Unlock()
	if next == nil || next == entry || routingDecision(t, sim, first.Rule).Owner.PhysicalNodeID != p1.ID {
		t.Fatal("快照失败后未保留旧表或未安排重试")
	}
	sim.refreshRouteTimer(runtime, p1.ID, next)
	if got := routingDecision(t, sim, first.Rule); got.Owner.PhysicalNodeID != p2.ID {
		t.Fatalf("修复配置后的重试未发布新快照: %+v", got)
	}
}

func TestQueueWeightedRandomRouting(t *testing.T) {
	scenario, first, _ := routingTestScenario()
	scenario.Routing = RoutingConfig{Algorithm: RoutingQueueWeightedRandom}
	sim, err := NewSimulator(scenario)
	if err != nil {
		t.Fatal(err)
	}
	defer sim.Close()
	counts := make(map[identity.PhysicalNodeID]int)
	for i := 0; i < 256; i++ {
		counts[routingDecision(t, sim, first.Rule).Owner.PhysicalNodeID]++
	}
	// 两个队列均为空时概率相等，配置权重和固定请求键不影响结果。
	if counts[scenario.PhysicalNodes[0].ID] < 40 || counts[scenario.PhysicalNodes[1].ID] < 40 {
		t.Fatalf("队列加权随机选路未同时覆盖两个空队列: %+v", counts)
	}
}

func TestUpdateRoutingPreservesStateAndTraffic(t *testing.T) {
	ctx := context.Background()
	scenario, first, _ := routingTestScenario()
	p1, p2 := scenario.PhysicalNodes[0], scenario.PhysicalNodes[1]
	sim, err := NewSimulator(scenario)
	if err != nil {
		t.Fatal(err)
	}
	defer sim.Close()
	if _, err := sim.AddRoutedTicket(ctx, routedDecision(p1, first), TicketInput{Rule: first.Rule, TicketID: 1}); err != nil {
		t.Fatal(err)
	}
	if result, err := sim.RunRound(ctx, 100, 1); err != nil || len(result.Matches) != 1 {
		t.Fatalf("准备匹配历史失败: %+v, %v", result, err)
	}
	if _, err := sim.AddRoutedTicket(ctx, routedDecision(p1, first), TicketInput{Rule: first.Rule, TicketID: 2}); err != nil {
		t.Fatal(err)
	}
	sim.mu.RLock()
	runtime, adapter, oldTimer := sim.runtime, sim.runtime.nodes[p1.ID], sim.runtime.routeTimers[p1.ID]
	sim.mu.RUnlock()
	config := RoutingConfig{Algorithm: RoutingWeightedPriority, TicketLimit: 1}
	if got, err := sim.UpdateRouting(ctx, config); err != nil || got != config {
		t.Fatalf("更新路由配置失败: %+v, %v", got, err)
	}
	if got := routingDecision(t, sim, first.Rule); got.Owner.PhysicalNodeID != p2.ID {
		t.Fatalf("算法切换应读取节点真实队列: %+v", got)
	}
	sim.mu.RLock()
	sameRuntime := sim.runtime == runtime && runtime.nodes[p1.ID] == adapter && runtime.routeTimers[p1.ID] == oldTimer
	previousRouter := runtime.router
	sim.mu.RUnlock()
	if !sameRuntime {
		t.Fatal("路由切换重建了节点或定时器")
	}
	if _, found, err := sim.GetTicket(ctx, routedDecision(p1, first).Owner, 2); err != nil || !found {
		t.Fatalf("路由切换丢失等待队列: %v, %v", found, err)
	}
	if history, err := sim.ListMatches(ctx, MatchQuery{}); err != nil || history.Total != 1 {
		t.Fatalf("路由切换丢失匹配历史: %+v, %v", history, err)
	}
	if _, err := sim.UpdateRouting(ctx, RoutingConfig{Algorithm: RoutingWeightedPriority}); err == nil {
		t.Fatal("无效路由配置应拒绝")
	}
	// 模拟快照与部署不一致，验证构表失败时不会提交配置和路由。
	sim.mu.Lock()
	runtime.scenario.Rules[0].Weight = 0
	sim.mu.Unlock()
	if _, err := sim.UpdateRouting(ctx, RoutingConfig{Algorithm: RoutingQueueWeightedRandom}); err == nil {
		t.Fatal("无法构建的路由表应拒绝")
	}
	sim.mu.Lock()
	runtime.scenario.Rules[0].Weight = 10
	preserved := runtime.router == previousRouter && runtime.scenario.Routing == config
	sim.mu.Unlock()
	if !preserved || routingDecision(t, sim, first.Rule).Owner.PhysicalNodeID != p2.ID {
		t.Fatal("失败的切换改变了当前配置或路由")
	}
	if _, err := sim.StartTraffic(TrafficConfig{Distribution: "constant", Rate: 100, MatchIntervalMS: 100, MaxMatches: 10}, BatchGeneratorSpec{Rule: first.Rule}); err != nil {
		t.Fatal(err)
	}
	awaitTraffic(t, sim, func(status TrafficStatus) bool { return status.Injected >= 2 })
	if _, err := sim.UpdateRouting(ctx, RoutingConfig{Algorithm: RoutingQueueWeightedRandom}); err != nil {
		t.Fatal(err)
	}
	status := sim.Traffic()
	if status.State != "running" {
		t.Fatalf("切换路由停止了流量: %+v", status)
	}
	awaitTraffic(t, sim, func(next TrafficStatus) bool { return next.Injected > status.Injected })
}

func TestReplaceScenarioChangesRoutingAlgorithm(t *testing.T) {
	ctx := context.Background()
	scenario, first, _ := routingTestScenario()
	scenario.Routing = RoutingConfig{Algorithm: RoutingWeightedPriority, TicketLimit: 1}
	sim, err := NewSimulator(scenario)
	if err != nil {
		t.Fatal(err)
	}
	defer sim.Close()
	if got := routingDecision(t, sim, first.Rule); got.Owner.PhysicalNodeID != scenario.PhysicalNodes[0].ID {
		t.Fatalf("初始场景未采用权重优先算法: %+v", got)
	}
	other := scenario.Clone()
	other.Rules[1].Weight = 20
	if err := sim.ReplaceScenario(ctx, other); err != nil {
		t.Fatal(err)
	}
	if got := routingDecision(t, sim, first.Rule); got.Owner.PhysicalNodeID != other.PhysicalNodes[1].ID {
		t.Fatalf("新场景未采用自身路由配置: %+v", got)
	}
	other.Routing = RoutingConfig{}
	if err := sim.ReplaceScenario(ctx, other); err != nil {
		t.Fatal(err)
	}
	// 默认路由与独立客户端在同一请求上的选择一致。
	tableConfig := client.RouteTableConfig{}
	for _, physical := range other.PhysicalNodes {
		tableConfig.PhysicalNodes = append(tableConfig.PhysicalNodes, client.PhysicalRoute{
			PhysicalNodeID: physical.ID, Endpoint: physical.Endpoint, Enabled: physical.Enabled,
		})
	}
	for _, rule := range other.Rules {
		tableConfig.Rules = append(tableConfig.Rules, client.RuleRoute{
			LogicalNode: rule.LogicalNode, PhysicalNodeID: rule.PhysicalNodeID, Weight: rule.Weight, Enabled: rule.Enabled,
		})
	}
	table, err := client.NewRouteTable(tableConfig)
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := client.NewRouter(table)
	if err != nil {
		t.Fatal(err)
	}
	for id := uint64(1); id <= 20; id++ {
		request := common.RouteRequest{Rule: first.Rule, TicketID: id}
		want, wantErr := baseline.RouteNew(ctx, request)
		got, gotErr := sim.RouteNew(ctx, request)
		if wantErr != nil || gotErr != nil || got != want {
			t.Fatalf("默认路由与客户端不一致: %+v / %+v, %v / %v", got, want, gotErr, wantErr)
		}
	}
}
