package simulator

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/xianyu11-x/matchSystem/pkg/client"
	"github.com/xianyu11-x/matchSystem/pkg/common"
	"github.com/xianyu11-x/matchSystem/pkg/identity"
	"github.com/xianyu11-x/matchSystem/pkg/matchsystem"
)

func TestDynamicPhysicalNodePreservesRuntimeState(t *testing.T) {
	scenario, key := testScenario()
	sim, err := NewSimulator(scenario)
	if err != nil {
		t.Fatal(err)
	}
	defer sim.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	owner1 := identity.OwnerRef{PhysicalNodeID: testPhysicalID("p1"), LogicalNode: key}
	addAt := func(owner identity.OwnerRef, endpoint common.Endpoint, ticketID uint64) {
		t.Helper()
		_, addErr := sim.AddRoutedTicket(ctx, common.RouteDecision{Owner: owner, Endpoint: endpoint}, TicketInput{
			Rule: key.Rule, TicketID: ticketID, CreatedAt: int64(ticketID),
		})
		if addErr != nil {
			t.Fatal(addErr)
		}
	}
	for _, id := range []uint64{1, 2, 3} {
		addAt(owner1, "inproc://p1", id)
	}
	if err := sim.BeginRound(ctx, 100); err != nil {
		t.Fatal(err)
	}
	first, err := sim.ProduceOne(ctx, testPhysicalID("p1"))
	if err != nil || first.Match == nil || first.Match.Round != 1 {
		t.Fatalf("首次匹配失败: %+v, %v", first, err)
	}
	stream, err := sim.SubscribeEvents(ctx, EventQuery{Cursor: "5"})
	if err != nil {
		t.Fatal(err)
	}

	physical := NewPhysicalNodeSpec(testPhysicalID("p2"), "inproc://p2")
	rule := (Scenario{Rules: scenario.Rules}).Clone().Rules[0]
	rule.PhysicalNodeID = physical.ID
	rule.LogicalNode.PlacementID = testPlacementID("p2")
	owner2 := identity.OwnerRef{PhysicalNodeID: testPhysicalID("p2"), LogicalNode: rule.LogicalNode}
	if err := sim.AddPhysicalNode(ctx, physical, []RuleSpec{rule}); err != nil {
		t.Fatal(err)
	}
	rule.RuleJSON[0] = 'x'
	committed, err := sim.Scenario()
	if err != nil || len(committed.PhysicalNodes) != 2 || len(committed.Rules) != 2 || committed.Rules[1].RuleJSON[0] == 'x' {
		t.Fatalf("新增节点的场景快照不正确: %+v, %v", committed, err)
	}
	if resolved, err := sim.ResolveOwner(owner2); err != nil || resolved.Endpoint != physical.Endpoint {
		t.Fatalf("新增节点 Owner 无法解析: %+v, %v", resolved, err)
	}
	selected := make(map[identity.OwnerRef]bool)
	for id := uint64(1); id <= 256; id++ {
		decision, err := sim.RouteNew(ctx, common.RouteRequest{Rule: key.Rule, TicketID: id})
		if err != nil {
			t.Fatalf("新增节点后的路由失败: %v", err)
		}
		selected[decision.Owner] = true
	}
	if !selected[owner1] || !selected[owner2] {
		t.Fatalf("新旧节点没有同时参与路由: %+v", selected)
	}
	if _, err := sim.ProduceOne(ctx, testPhysicalID("p2")); !errors.Is(err, matchsystem.ErrNoLogicalNodeAvailable) {
		t.Fatalf("新节点没有加入当前轮次: %v", err)
	}
	second, err := sim.ProduceOne(ctx, testPhysicalID("p1"))
	if err != nil || second.Match == nil || second.Match.Round != first.Match.Round || second.Match.Now != first.Match.Now {
		t.Fatalf("原有轮次或队列被重置: %+v, %v", second, err)
	}
	addAt(owner2, physical.Endpoint, 10)
	addAt(owner2, physical.Endpoint, 3)
	if err := sim.BeginRound(ctx, 200); err != nil {
		t.Fatal(err)
	}
	third, err := sim.ProduceOne(ctx, testPhysicalID("p2"))
	if err != nil || third.Match == nil || third.Match.Round != 2 || third.Match.ID == first.Match.ID || third.Match.ID == second.Match.ID {
		t.Fatalf("新节点匹配或 Match ID 异常: %+v, %v", third, err)
	}
	if err := sim.RemovePhysicalNode(ctx, testPhysicalID("p2")); err != nil {
		t.Fatal(err)
	}
	if _, err := sim.ResolveOwner(owner2); !errors.Is(err, client.ErrOwnerNotFound) {
		t.Fatalf("已删除的 Owner 仍可解析: %v", err)
	}
	if _, found, err := sim.GetTicket(ctx, owner1, 3); err != nil || !found {
		t.Fatalf("其他节点的等待票被删除: found=%v err=%v", found, err)
	}
	page, err := sim.ListTickets(ctx, TicketQuery{})
	if err != nil || page.Total != 1 || page.Items[0].Owner != owner1 {
		t.Fatalf("删除后等待票不正确: %+v, %v", page, err)
	}
	matches, err := sim.ListMatches(ctx, MatchQuery{})
	if err != nil || matches.Total != 3 {
		t.Fatalf("删除后匹配历史不正确: %+v, %v", matches, err)
	}
	if record, found, err := sim.GetMatch(ctx, third.Match.ID); err != nil || !found || record.PhysicalNodeID != testPhysicalID("p2") {
		t.Fatalf("已删除节点的匹配历史丢失: %+v, %v, %v", record, found, err)
	}
	for id := uint64(1); id <= 50; id++ {
		decision, err := sim.RouteNew(ctx, common.RouteRequest{Rule: key.Rule, TicketID: id})
		if err != nil || decision.Owner != owner1 {
			t.Fatalf("删除后的路由不正确: %+v, %v", decision, err)
		}
	}
	committed, err = sim.GetScenario(ctx)
	if err != nil || len(committed.PhysicalNodes) != 1 || len(committed.Rules) != 1 || committed.PhysicalNodes[0].ID != testPhysicalID("p1") {
		t.Fatalf("删除后的场景不正确: %+v, %v", committed, err)
	}
	added, removed := false, false
	for !removed {
		select {
		case event, ok := <-stream:
			if !ok {
				t.Fatal("节点变化关闭了事件订阅")
			}
			if event.Type == "physical_node_added" {
				added = event.Data["physicalNodeId"] == strconv.FormatUint(uint64(testPhysicalID("p2")), 10)
			}
			if event.Type == "physical_node_removed" {
				removed = true
				if event.Data["physicalNodeId"] != strconv.FormatUint(uint64(testPhysicalID("p2")), 10) || event.Data["removedTickets"] != 1 {
					t.Fatalf("删除事件不正确: %+v", event)
				}
			}
		case <-time.After(time.Second):
			t.Fatal("未收到节点删除事件")
		}
	}
	if !added {
		t.Fatal("未收到节点新增事件")
	}
}

type dynamicNodeProviderProbe struct {
	calls     int
	failAfter int
}

func (p *dynamicNodeProviderProbe) Describe() (matchsystem.ProviderDescriptor, error) {
	p.calls++
	if p.failAfter > 0 && p.calls > p.failAfter {
		return matchsystem.ProviderDescriptor{}, errors.New("provider describe rejected")
	}
	return simulatorTestObjectDescriptor("object_tag", 2), nil
}

func (p *dynamicNodeProviderProbe) Provide(_ *common.Ticket, _ int64, _ matchsystem.Facts, out matchsystem.ObjectFactWriter) error {
	return out.SetStrings("object_tag", []string{"probe"})
}

func TestDynamicPhysicalNodeValidationAndRollback(t *testing.T) {
	scenario, key := testScenario()
	oldProvider := &dynamicNodeProviderProbe{}
	scenario.Rules[0].ObjectFactProvider = oldProvider
	sim, err := NewSimulator(scenario)
	if err != nil {
		t.Fatal(err)
	}
	defer sim.Close()
	ctx := context.Background()
	if _, err := sim.AddTicket(ctx, TicketInput{Rule: key.Rule, TicketID: 55}); err != nil {
		t.Fatal(err)
	}
	before, err := sim.Scenario()
	if err != nil {
		t.Fatal(err)
	}
	beforeJSON, err := json.Marshal(before)
	if err != nil {
		t.Fatal(err)
	}
	eventsBefore, err := sim.Events(ctx, EventQuery{})
	if err != nil {
		t.Fatal(err)
	}
	oldCalls := oldProvider.calls
	oldProvider.failAfter = oldCalls
	physical := NewPhysicalNodeSpec(testPhysicalID("p2"), "inproc://p2")
	rule := (Scenario{Rules: scenario.Rules}).Clone().Rules[0]
	rule.PhysicalNodeID = physical.ID
	rule.LogicalNode.PlacementID = testPlacementID("p2")
	rule.ObjectFactProvider = &dynamicNodeProviderProbe{failAfter: 1}
	foreign := rule
	foreign.PhysicalNodeID = testPhysicalID("p1")
	badJSON := rule
	badJSON.RuleJSON = []byte(`{}`)
	alias := NewPhysicalNodeSpec(testPhysicalID(strconv.FormatUint(uint64(testPhysicalID("p1")), 10)), "inproc://alias")
	tests := []struct {
		name string
		run  func() error
	}{
		{"duplicate", func() error { return sim.AddPhysicalNode(ctx, scenario.PhysicalNodes[0], nil) }},
		{"foreign rule", func() error { return sim.AddPhysicalNode(ctx, physical, []RuleSpec{foreign}) }},
		{"invalid rule", func() error { return sim.AddPhysicalNode(ctx, physical, []RuleSpec{badJSON}) }},
		{"numeric identity collision", func() error { return sim.AddPhysicalNode(ctx, alias, nil) }},
		{"provider load failure", func() error { return sim.AddPhysicalNode(ctx, physical, []RuleSpec{rule}) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.run()
			if err == nil {
				t.Fatal("无效变更被接受")
			}
			if test.name == "duplicate" && !errors.Is(err, ErrPhysicalNodeAlreadyExists) {
				t.Fatalf("重复节点错误不正确: %v", err)
			}
			if test.name == "foreign rule" {
				var validation *ValidationError
				if !errors.As(err, &validation) || len(validation.Issues) != 1 || validation.Issues[0].Code != "PHYSICAL_NODE_MISMATCH" || validation.Issues[0].Path != "$.rules[0].physicalNodeId" {
					t.Fatalf("规则归属错误不正确: %v", err)
				}
			}
			after, err := sim.Scenario()
			if err != nil {
				t.Fatal(err)
			}
			afterJSON, err := json.Marshal(after)
			if err != nil || !bytes.Equal(beforeJSON, afterJSON) {
				t.Fatalf("失败的节点变更修改了场景: %s / %s / %v", beforeJSON, afterJSON, err)
			}
			if _, found, err := sim.GetTicket(ctx, identity.OwnerRef{PhysicalNodeID: testPhysicalID("p1"), LogicalNode: key}, 55); err != nil || !found {
				t.Fatalf("失败的变更修改了已有队列: %v, %v", found, err)
			}
		})
	}
	if oldProvider.calls != oldCalls {
		t.Fatalf("重新握手了现有 Provider: %d -> %d", oldCalls, oldProvider.calls)
	}
	eventsAfter, err := sim.Events(ctx, EventQuery{})
	if err != nil || eventsAfter.Total != eventsBefore.Total {
		t.Fatalf("失败的变更发布了事件: %+v, %v", eventsAfter, err)
	}
	if err := sim.AddPhysicalNode(ctx, physical, nil); err != nil {
		t.Fatalf("不能添加空节点: %v", err)
	}
	topology, err := sim.Topology(ctx)
	emptyNodeFound := false
	for _, node := range topology.PhysicalNodes {
		if node.ID == physical.ID && len(node.LogicalNodes) == 0 {
			emptyNodeFound = true
		}
	}
	if err != nil || len(topology.PhysicalNodes) != 2 || !emptyNodeFound {
		t.Fatalf("空节点未出现在拓扑中: %+v, %v", topology, err)
	}
	if err := sim.RemovePhysicalNode(ctx, physical.ID); err != nil {
		t.Fatal(err)
	}
	if err := sim.RemovePhysicalNode(ctx, physical.ID); err != nil {
		t.Fatalf("重复删除未保持幂等: %v", err)
	}
	if oldProvider.calls != oldCalls {
		t.Fatal("新增或删除空节点时调用了现有 Provider")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := sim.AddPhysicalNode(cancelled, physical, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("新增未响应取消: %v", err)
	}
	if err := sim.RemovePhysicalNode(cancelled, testPhysicalID("p1")); !errors.Is(err, context.Canceled) {
		t.Fatalf("删除未响应取消: %v", err)
	}
	if err := sim.RemovePhysicalNode(ctx, testPhysicalID("")); err == nil {
		t.Fatal("接受空物理节点 ID")
	}
}

func TestDynamicPhysicalNodeKeepsTrafficRunning(t *testing.T) {
	scenario, key := testScenario()
	sim, err := NewSimulator(scenario)
	if err != nil {
		t.Fatal(err)
	}
	defer sim.Close()
	rule := (Scenario{Rules: scenario.Rules}).Clone().Rules[0]
	rule.PhysicalNodeID = testPhysicalID("p2")
	rule.LogicalNode.PlacementID = testPlacementID("p2")
	_, err = sim.StartTraffic(TrafficConfig{Distribution: "constant", Rate: 100, MatchIntervalMS: 100, MaxMatches: 10}, BatchGeneratorSpec{Rule: key.Rule})
	if err != nil {
		t.Fatal(err)
	}
	awaitTraffic(t, sim, func(status TrafficStatus) bool { return status.Injected >= 2 })
	if err := sim.AddPhysicalNode(context.Background(), NewPhysicalNodeSpec(testPhysicalID("p2"), "inproc://p2"), []RuleSpec{rule}); err != nil {
		t.Fatal(err)
	}
	afterAdd := sim.Traffic()
	if afterAdd.State != "running" {
		t.Fatalf("新增节点停止了流量: %+v", afterAdd)
	}
	awaitTraffic(t, sim, func(status TrafficStatus) bool { return status.Injected > afterAdd.Injected })
	if err := sim.RemovePhysicalNode(context.Background(), testPhysicalID("p2")); err != nil {
		t.Fatal(err)
	}
	before := sim.Traffic()
	if before.State != "running" {
		t.Fatalf("删除节点停止了流量: %+v", before)
	}
	awaitTraffic(t, sim, func(status TrafficStatus) bool { return status.Injected > before.Injected })
	if err := sim.RemovePhysicalNode(context.Background(), testPhysicalID("p1")); err != nil {
		t.Fatal(err)
	}
	failed := awaitTraffic(t, sim, func(status TrafficStatus) bool { return status.State == "failed" })
	if !strings.Contains(failed.Error, client.ErrNoRoute.Error()) {
		t.Fatalf("最后一条路由下线后的流量错误不正确: %+v", failed)
	}
}

func TestDynamicPhysicalNodePythonWorkerLifecycle(t *testing.T) {
	python := testPythonExecutable(t)
	good := writePythonProviderScript(t, "def provide(method, data):\n    return {}\n")
	scenario, keys := pythonProviderScenario(t, python, []string{good, good})
	initial := scenario.Clone()
	initial.PhysicalNodes = initial.PhysicalNodes[:1]
	initial.Rules = initial.Rules[:1]
	sim, err := NewSimulator(initial)
	if err != nil {
		t.Fatal(err)
	}
	defer sim.Close()
	oldWorker := snapshotPythonProviders(t, sim, keys[:1])[keys[0]]
	if err := sim.AddPhysicalNode(context.Background(), scenario.PhysicalNodes[1], scenario.Rules[1:]); err != nil {
		t.Fatal(err)
	}
	newWorker := snapshotPythonProviders(t, sim, keys[1:])[keys[1]]
	if newWorker == nil || newWorker.Closed() {
		t.Fatal("新增节点的 Python Worker 未启动")
	}
	if err := sim.RemovePhysicalNode(context.Background(), scenario.PhysicalNodes[1].ID); err != nil {
		t.Fatal(err)
	}
	assertPythonWorkerReaped(t, newWorker)
	if oldWorker == nil || oldWorker.Closed() {
		t.Fatal("删除节点影响了其他节点的 Python Worker")
	}

	marker := filepath.Join(t.TempDir(), "staged.pid")
	stagedScript := writePythonProviderScript(t, pythonProviderPIDScript(marker))
	broken, _ := pythonProviderScenario(t, python, []string{stagedScript, filepath.Join(t.TempDir(), "missing.py")})
	for index := range broken.Rules {
		broken.Rules[index].PhysicalNodeID = testPhysicalID("staged")
		broken.Rules[index].LogicalNode.PlacementID = identity.PlacementID(index + 1000)
	}
	if err := sim.AddPhysicalNode(context.Background(), NewPhysicalNodeSpec(testPhysicalID("staged"), "inproc://staged"), broken.Rules); err == nil {
		t.Fatal("无法启动的 Python Worker 被接受")
	}
	waitForProcessExit(t, readPythonProviderPID(t, marker))
	if oldWorker.Closed() {
		t.Fatal("加载失败关闭了已有 Python Worker")
	}
	if _, err := sim.ResolveOwner(identity.OwnerRef{PhysicalNodeID: testPhysicalID("staged"), LogicalNode: broken.Rules[0].LogicalNode}); !errors.Is(err, client.ErrOwnerNotFound) {
		t.Fatalf("加载失败后仍可解析 Owner: %v", err)
	}
}
