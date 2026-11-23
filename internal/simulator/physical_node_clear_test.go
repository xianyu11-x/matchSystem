package simulator

import (
	"bytes"
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/xianyu11-x/matchSystem/pkg/common"
	"github.com/xianyu11-x/matchSystem/pkg/identity"
	"github.com/xianyu11-x/matchSystem/pkg/matchsystem"
)

func TestClearPhysicalNodeTicketsPreservesRuntime(t *testing.T) {
	scenario, firstKey := testScenario()
	secondRule := (Scenario{Rules: scenario.Rules}).Clone().Rules[0]
	secondRule.LogicalNode.Rule.RuleID = 2
	secondRule.LogicalNode.PlacementID = testPlacementID("second")
	secondRule.RuleJSON = bytes.Replace(secondRule.RuleJSON, []byte(`"ruleId":1`), []byte(`"ruleId":2`), 1)
	otherRule := (Scenario{Rules: scenario.Rules}).Clone().Rules[0]
	otherRule.PhysicalNodeID = testPhysicalID("p2")
	otherRule.LogicalNode.PlacementID = testPlacementID("p2")
	scenario.PhysicalNodes = append(scenario.PhysicalNodes, NewPhysicalNodeSpec(testPhysicalID("p2"), "inproc://p2"))
	scenario.Rules = append(scenario.Rules, secondRule, otherRule)
	sim, err := NewSimulator(scenario)
	if err != nil {
		t.Fatal(err)
	}
	defer sim.Close()
	ctx := context.Background()
	first := identity.OwnerRef{PhysicalNodeID: testPhysicalID("p1"), LogicalNode: firstKey}
	second := identity.OwnerRef{PhysicalNodeID: first.PhysicalNodeID, LogicalNode: secondRule.LogicalNode}
	other := identity.OwnerRef{PhysicalNodeID: testPhysicalID("p2"), LogicalNode: otherRule.LogicalNode}
	add := func(owner identity.OwnerRef, id uint64) {
		t.Helper()
		if _, err := sim.AddRoutedTicket(ctx, common.RouteDecision{Owner: owner}, TicketInput{Rule: owner.LogicalNode.Rule, TicketID: id}); err != nil {
			t.Fatal(err)
		}
	}
	add(first, 99)
	if err := sim.BeginRound(ctx, 100); err != nil {
		t.Fatal(err)
	}
	matched, err := sim.ProduceOne(ctx, first.PhysicalNodeID)
	if err != nil || matched.Match == nil {
		t.Fatalf("无法创建历史匹配: %+v %v", matched, err)
	}
	for _, owner := range []identity.OwnerRef{first, second, other} {
		add(owner, 1)
		add(owner, 2)
	}
	// 模拟既有取消竞争后核心中存在但观测表中缺失的 Ticket。
	if err := sim.runtime.nodes[first.PhysicalNodeID].Add(ctx, first, &common.Ticket{TicketID: 3}); err != nil {
		t.Fatal(err)
	}
	if err := sim.BeginRound(ctx, 200); err != nil {
		t.Fatal(err)
	}
	eventsBefore, err := sim.Events(ctx, EventQuery{})
	if err != nil {
		t.Fatal(err)
	}
	removed, err := sim.ClearPhysicalNodeTickets(ctx, first.PhysicalNodeID)
	if err != nil || removed != 5 {
		t.Fatalf("清空数量不正确: removed=%d err=%v", removed, err)
	}
	page, err := sim.ListTickets(ctx, TicketQuery{})
	if err != nil || page.Total != 2 || len(page.Items) != 2 || page.Items[0].Owner != other || page.Items[1].Owner != other {
		t.Fatalf("清空后观测数据不正确: %+v %v", page, err)
	}
	if _, exists, err := sim.runtime.nodes[first.PhysicalNodeID].Get(ctx, first, 3); err != nil || exists {
		t.Fatalf("未登记的核心 Ticket 未被清理: exists=%v err=%v", exists, err)
	}
	if _, err := sim.ResolveOwner(first); err != nil {
		t.Fatalf("清空改变了路由: %v", err)
	}
	topology, err := sim.Topology(ctx)
	if err != nil || len(topology.PhysicalNodes) != 2 {
		t.Fatalf("清空改变了节点或部署: %+v %v", topology, err)
	}
	logicalCount := 0
	for _, node := range topology.PhysicalNodes {
		logicalCount += len(node.LogicalNodes)
	}
	if logicalCount != 3 {
		t.Fatalf("清空改变了逻辑部署: %+v", topology)
	}
	if record, found, err := sim.GetMatch(ctx, matched.Match.ID); err != nil || !found || record.ID != matched.Match.ID {
		t.Fatalf("清空影响了匹配历史: %+v %v %v", record, found, err)
	}
	result, err := sim.ProduceOne(ctx, other.PhysicalNodeID)
	if err != nil || result.Match == nil || result.Match.Round != 2 || result.Match.Now != 200 {
		t.Fatalf("其他节点的活动轮次受到影响: %+v %v", result, err)
	}
	eventsAfter, err := sim.Events(ctx, EventQuery{})
	if err != nil || eventsAfter.Total != eventsBefore.Total+2 {
		t.Fatalf("清空应只发一个事件，随后匹配也有一个事件: %+v %v", eventsAfter, err)
	}
	event := eventsAfter.Items[len(eventsAfter.Items)-2]
	if event.Type != "physical_node_tickets_cleared" || event.Data["physicalNodeId"] != strconv.FormatUint(uint64(first.PhysicalNodeID), 10) || event.Data["removedTickets"] != 5 {
		t.Fatalf("清空事件不正确: %+v", event)
	}
	if removed, err := sim.ClearPhysicalNodeTickets(ctx, first.PhysicalNodeID); err != nil || removed != 0 {
		t.Fatalf("重复清空失败: removed=%d err=%v", removed, err)
	}
	add(first, 1)
	if err := sim.BeginRound(ctx, 300); err != nil {
		t.Fatal(err)
	}
	if result, err := sim.ProduceOne(ctx, first.PhysicalNodeID); err != nil || result.Match == nil || result.Match.Round != 3 {
		t.Fatalf("清空后不能重新接收并匹配: %+v %v", result, err)
	}
}

func TestClearPhysicalNodeTicketsErrorsDoNotMutate(t *testing.T) {
	scenario, key := testScenario()
	sim, err := NewSimulator(scenario)
	if err != nil {
		t.Fatal(err)
	}
	defer sim.Close()
	ctx := context.Background()
	if _, err := sim.AddTicket(ctx, TicketInput{Rule: key.Rule, TicketID: 1}); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if removed, err := sim.ClearPhysicalNodeTickets(canceled, testPhysicalID("p1")); !errors.Is(err, context.Canceled) || removed != 0 {
		t.Fatalf("取消结果不正确: removed=%d err=%v", removed, err)
	}
	if _, err := sim.ClearPhysicalNodeTickets(ctx, testPhysicalID("missing")); !errors.Is(err, ErrUnknownNode) {
		t.Fatalf("未知节点未返回预期错误: %v", err)
	}
	if _, err := sim.ClearPhysicalNodeTickets(ctx, 0); err == nil {
		t.Fatal("接受零节点 ID")
	}
	owner := identity.OwnerRef{PhysicalNodeID: testPhysicalID("p1"), LogicalNode: key}
	if _, found, err := sim.GetTicket(ctx, owner, 1); err != nil || !found {
		t.Fatalf("失败的清空改变了 Ticket: found=%v err=%v", found, err)
	}
}

// clearCancelAtCommitContext 模拟最后一次取消检查之后立即发生取消。
type clearCancelAtCommitContext struct {
	context.Context
	cancel context.CancelFunc
	checks int
}

func (c *clearCancelAtCommitContext) Err() error {
	c.checks++
	if c.checks == 3 {
		defer c.cancel()
		return nil
	}
	return c.Context.Err()
}

func TestClearPhysicalNodeTicketsCompletesAfterCommitCancellation(t *testing.T) {
	scenario, key := testScenario()
	sim, err := NewSimulator(scenario)
	if err != nil {
		t.Fatal(err)
	}
	defer sim.Close()
	if _, err := sim.AddTicket(context.Background(), TicketInput{Rule: key.Rule, TicketID: 1}); err != nil {
		t.Fatal(err)
	}
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &clearCancelAtCommitContext{Context: base, cancel: cancel}
	removed, err := sim.ClearPhysicalNodeTickets(ctx, testPhysicalID("p1"))
	if err != nil || removed != 1 || ctx.checks != 3 || !errors.Is(base.Err(), context.Canceled) {
		t.Fatalf("提交后的取消导致结果不确定: removed=%d checks=%d err=%v", removed, ctx.checks, err)
	}
	owner := identity.OwnerRef{PhysicalNodeID: testPhysicalID("p1"), LogicalNode: key}
	if _, found, err := sim.GetTicket(context.Background(), owner, 1); err != nil || found || sim.runtime.registry.Len() != 0 {
		t.Fatalf("提交后核心和观测未同时清空: found=%v err=%v", found, err)
	}
}

// clearQueuedContext 在请求完成入队前检查时通知测试协程。
type clearQueuedContext struct {
	context.Context
	checked chan struct{}
	checks  int
}

func (c *clearQueuedContext) Err() error {
	err := c.Context.Err()
	c.checks++
	if c.checks == 2 {
		close(c.checked)
	}
	return err
}

func TestClearPhysicalNodeTicketsCanceledWhileWaitingForOwner(t *testing.T) {
	scenario, key := testScenario()
	sim, err := NewSimulator(scenario)
	if err != nil {
		t.Fatal(err)
	}
	defer sim.Close()
	if _, err := sim.AddTicket(context.Background(), TicketInput{Rule: key.Rule, TicketID: 1}); err != nil {
		t.Fatal(err)
	}
	adapter := sim.runtime.nodes[testPhysicalID("p1")]
	entered, release := make(chan struct{}), make(chan struct{})
	workerDone := make(chan error, 1)
	go func() {
		_, err := adapter.call(context.Background(), func(*matchsystem.PhysicalNode) (any, error) {
			close(entered)
			<-release
			return nil, nil
		})
		workerDone <- err
	}()
	<-entered
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &clearQueuedContext{Context: base, checked: make(chan struct{})}
	result := make(chan error, 1)
	go func() {
		_, err := sim.ClearPhysicalNodeTickets(ctx, testPhysicalID("p1"))
		result <- err
	}()
	<-ctx.checked
	cancel()
	close(release)
	if err := <-workerDone; err != nil {
		t.Fatal(err)
	}
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("执行前取消未被观察到: %v", err)
	}
	owner := identity.OwnerRef{PhysicalNodeID: testPhysicalID("p1"), LogicalNode: key}
	if _, found, err := sim.GetTicket(context.Background(), owner, 1); err != nil || !found || sim.runtime.registry.Len() != 1 {
		t.Fatalf("执行前取消改变了核心或观测: found=%v err=%v", found, err)
	}
}

func TestClearPhysicalNodeTicketsKeepsTrafficRunning(t *testing.T) {
	scenario, key := testScenario()
	sim, err := NewSimulator(scenario)
	if err != nil {
		t.Fatal(err)
	}
	defer sim.Close()
	if _, err := sim.StartTraffic(TrafficConfig{Distribution: "constant", Rate: 100, MatchIntervalMS: 1000, MaxMatches: 10}, BatchGeneratorSpec{Rule: key.Rule}); err != nil {
		t.Fatal(err)
	}
	awaitTraffic(t, sim, func(status TrafficStatus) bool { return status.Injected >= 2 })
	if _, err := sim.ClearPhysicalNodeTickets(context.Background(), testPhysicalID("p1")); err != nil {
		t.Fatal(err)
	}
	after := sim.Traffic()
	if after.State != "running" {
		t.Fatalf("清空停止了持续流量: %+v", after)
	}
	awaitTraffic(t, sim, func(status TrafficStatus) bool { return status.Injected > after.Injected })
}
