package simulator

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/xianyu11-x/matchSystem/pkg/common"
	"github.com/xianyu11-x/matchSystem/pkg/identity"
	"github.com/xianyu11-x/matchSystem/pkg/matchsystem"
)

func libraryRuleID(t *testing.T, raw json.RawMessage, id string) json.RawMessage {
	t.Helper()
	var rule map[string]json.RawMessage
	if err := json.Unmarshal(raw, &rule); err != nil {
		t.Fatal(err)
	}
	var key map[string]json.RawMessage
	if err := json.Unmarshal(rule["ruleKey"], &key); err != nil {
		t.Fatal(err)
	}
	key["ruleId"] = json.RawMessage(id)
	var err error
	rule["ruleKey"], err = json.Marshal(key)
	if err != nil {
		t.Fatal(err)
	}
	result, err := json.Marshal(rule)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func deploymentFor(key identity.LogicalNodeKey, weight uint32) RuleDeploymentSpec {
	return RuleDeploymentSpec{LogicalNode: key, Weight: weight, Enabled: true}
}

func libraryScenario() (Scenario, identity.LogicalNodeKey) {
	scenario, key := testScenario()
	raw := bytes.ReplaceAll(scenario.Rules[0].RuleJSON, []byte("object_tag"), []byte("preferredRoles"))
	scenario.Rules[0].RuleJSON = bytes.Replace(raw, []byte(`"maxValues":2`), []byte(`"maxValues":3`), 1)
	scenario.Rules[0].ObjectFactProvider = nil
	return scenario, key
}

type libraryProviderProbe struct{ calls int }

func (probe *libraryProviderProbe) Describe() (matchsystem.ProviderDescriptor, error) {
	probe.calls++
	return simulatorTestObjectDescriptor("preferredRoles", 3), nil
}

func (probe *libraryProviderProbe) Provide(_ *common.Ticket, _ int64, _ matchsystem.Facts, out matchsystem.ObjectFactWriter) error {
	return out.SetStrings("preferredRoles", []string{"probe"})
}

func TestRuleLibraryIndependentFromRuntime(t *testing.T) {
	ctx := context.Background()
	scenario, key := testScenario()
	sim, err := NewSimulator(scenario)
	if err != nil {
		t.Fatal(err)
	}
	defer sim.Close()
	initial, err := sim.RuleLibrary(ctx)
	if err != nil || initial.Revision == 0 || len(initial.Rules) != 1 {
		t.Fatalf("初始规则库错误: %+v, %v", initial, err)
	}
	initial.Rules[0][0] = 'x'
	again, err := sim.RuleLibrary(ctx)
	if err != nil || again.Rules[0][0] == 'x' {
		t.Fatalf("规则库返回了内部引用: %v", err)
	}
	if _, err := sim.AddTicket(ctx, TicketInput{Rule: key.Rule, TicketID: 11}); err != nil {
		t.Fatal(err)
	}
	if _, err := sim.ReplaceRuleLibrary(ctx, again.Revision, []json.RawMessage{again.Rules[0], again.Rules[0]}); err == nil {
		t.Fatal("重复 RuleKey 未被拒绝")
	}
	if _, err := sim.ReplaceRuleLibrary(ctx, again.Revision, []json.RawMessage{json.RawMessage(`{}`)}); err == nil {
		t.Fatal("无效规则未被拒绝")
	}
	saved, err := sim.ReplaceRuleLibrary(ctx, again.Revision, nil)
	if err != nil || saved.Revision != again.Revision+1 || len(saved.Rules) != 0 || saved.Rules == nil {
		t.Fatalf("清空规则库失败: %+v, %v", saved, err)
	}
	if _, err := sim.ReplaceRuleLibrary(ctx, again.Revision, nil); !errors.Is(err, ErrRuleLibraryConflict) {
		t.Fatalf("过期 revision 未拒绝: %v", err)
	}
	owner := identity.OwnerRef{PhysicalNodeID: testPhysicalID("p1"), LogicalNode: key}
	if _, found, err := sim.GetTicket(ctx, owner, 11); err != nil || !found {
		t.Fatalf("保存规则库影响了正在运行的队列: %v %v", found, err)
	}
	if err := sim.ReplaceScenario(ctx, Scenario{}); err != nil {
		t.Fatal(err)
	}
	after, err := sim.RuleLibrary(ctx)
	if err != nil || after.Revision != saved.Revision || len(after.Rules) != 0 || after.Rules == nil {
		t.Fatalf("替换场景改变了规则库: %+v, %v", after, err)
	}
	encoded, err := json.Marshal(after)
	if err != nil || !strings.Contains(string(encoded), `"revision":"`+strconv.FormatUint(saved.Revision, 10)+`"`) || !strings.Contains(string(encoded), `"rules":[]`) {
		t.Fatalf("规则库 JSON 错误: %s, %v", encoded, err)
	}
	var standalone Simulator
	zero, err := standalone.RuleLibrary(ctx)
	if err != nil || zero.Revision != 0 || zero.Rules == nil {
		t.Fatalf("无运行态时无法读取规则库: %+v, %v", zero, err)
	}
	if _, err := standalone.ReplaceRuleLibrary(ctx, zero.Revision, []json.RawMessage{again.Rules[0]}); err != nil {
		t.Fatalf("无运行态时无法保存规则库: %v", err)
	}
}

func TestRuleLibraryConcurrentRevisionConflict(t *testing.T) {
	ctx := context.Background()
	scenario, _ := testScenario()
	sim, err := NewSimulator(scenario)
	if err != nil {
		t.Fatal(err)
	}
	defer sim.Close()
	library, err := sim.RuleLibrary(ctx)
	if err != nil {
		t.Fatal(err)
	}
	const writers = 8
	results := make(chan error, writers)
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := sim.ReplaceRuleLibrary(ctx, library.Revision, library.Rules)
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	success, conflicts := 0, 0
	for err := range results {
		switch {
		case err == nil:
			success++
		case errors.Is(err, ErrRuleLibraryConflict):
			conflicts++
		default:
			t.Fatalf("意外的并发保存错误: %v", err)
		}
	}
	if success != 1 || conflicts != writers-1 {
		t.Fatalf("并发保存没有原子校验版本: success=%d conflicts=%d", success, conflicts)
	}
}

func TestRuleLibraryCanonicalIdentityRoundTripPreservesDeployment(t *testing.T) {
	ctx := context.Background()
	scenario, key := libraryScenario()
	probe := &libraryProviderProbe{}
	scenario.Rules[0].ObjectFactProvider = probe
	sim, err := NewSimulator(scenario)
	if err != nil {
		t.Fatal(err)
	}
	defer sim.Close()
	if _, err := sim.AddTicket(ctx, TicketInput{Rule: key.Rule, TicketID: 42}); err != nil {
		t.Fatal(err)
	}
	library, err := sim.RuleLibrary(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(library.Rules[0], []byte(`"ruleId":1`)) {
		t.Fatal("测试需要数字形式的规则 ID")
	}
	encoded, err := json.Marshal(library)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(encoded, []byte(`"ruleId":"1"`)) {
		t.Fatalf("规则库响应未输出标准身份: %s", encoded)
	}
	var received RuleLibrarySnapshot
	if err := json.Unmarshal(encoded, &received); err != nil {
		t.Fatal(err)
	}
	if _, err := sim.ReplaceRuleLibrary(ctx, received.Revision, received.Rules); err != nil {
		t.Fatal(err)
	}
	apply := deploymentFor(key, 1)
	apply.ApplyLatest = true
	calls := probe.calls
	physical := NewPhysicalNodeSpec(testPhysicalID("p1"), "inproc://p1")
	if err := sim.UpdatePhysicalNodeFromLibrary(ctx, physical, []RuleDeploymentSpec{apply}); err != nil {
		t.Fatal(err)
	}
	owner := identity.OwnerRef{PhysicalNodeID: physical.ID, LogicalNode: key}
	if _, found, err := sim.GetTicket(ctx, owner, 42); err != nil || !found {
		t.Fatalf("规则身份规范化后丢失等待队列: found=%v err=%v", found, err)
	}
	if probe.calls != calls {
		t.Fatalf("规则身份规范化导致重新握手: %d -> %d", calls, probe.calls)
	}
}

func TestRuleLibraryRejectsNoncanonicalRuleKeysWithoutChangingSnapshot(t *testing.T) {
	ctx := context.Background()
	scenario, _ := libraryScenario()
	sim, err := NewSimulator(scenario)
	if err != nil {
		t.Fatal(err)
	}
	defer sim.Close()
	before, err := sim.RuleLibrary(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{`"ruleId":"legacy"`, `"ruleId":"01"`, `"ruleId":null`} {
		rule := bytes.Replace(before.Rules[0], []byte(`"ruleId":1`), []byte(invalid), 1)
		if report := ValidateRuleJSON(rule); report.Valid {
			t.Fatalf("规则校验错误接受身份 %s", invalid)
		}
		if _, err := sim.ReplaceRuleLibrary(ctx, before.Revision, []json.RawMessage{rule}); err == nil {
			t.Fatalf("规则库错误接受身份 %s", invalid)
		}
	}
	after, err := sim.RuleLibrary(ctx)
	if err != nil || after.Revision != before.Revision || !bytes.Equal(after.Rules[0], before.Rules[0]) {
		t.Fatalf("无效规则改变了快照: before=%+v after=%+v err=%v", before, after, err)
	}
}

func TestUpdatePhysicalNodePreservesUnchangedRuntime(t *testing.T) {
	ctx := context.Background()
	scenario, key := libraryScenario()
	probe := &libraryProviderProbe{}
	scenario.Rules[0].ObjectFactProvider = probe
	sim, err := NewSimulator(scenario)
	if err != nil {
		t.Fatal(err)
	}
	defer sim.Close()
	owner := identity.OwnerRef{PhysicalNodeID: testPhysicalID("p1"), LogicalNode: key}
	for _, id := range []uint64{1, 2} {
		if _, err := sim.AddTicket(ctx, TicketInput{Rule: key.Rule, TicketID: id, CreatedAt: int64(id)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := sim.BeginRound(ctx, 100); err != nil {
		t.Fatal(err)
	}
	first, err := sim.ProduceOne(ctx, testPhysicalID("p1"))
	if err != nil || first.Match == nil {
		t.Fatalf("首次匹配失败: %+v, %v", first, err)
	}
	physical := NewPhysicalNodeSpec(testPhysicalID("p1"), "inproc://changed")
	physical.Selector = SelectorWeighted
	calls := probe.calls
	if err := sim.UpdatePhysicalNodeFromLibrary(ctx, physical, []RuleDeploymentSpec{deploymentFor(key, 3)}); err != nil {
		t.Fatal(err)
	}
	if probe.calls != calls {
		t.Fatalf("未改变的 Provider 被重新握手: %d -> %d", calls, probe.calls)
	}
	applySame := deploymentFor(key, 3)
	applySame.ApplyLatest = true
	if err := sim.UpdatePhysicalNodeFromLibrary(ctx, physical, []RuleDeploymentSpec{applySame}); err != nil || probe.calls != calls {
		t.Fatalf("应用内容相同的规则破坏了运行实例: %v, calls=%d", err, probe.calls)
	}
	snapshot, err := sim.Scenario()
	if err != nil || snapshot.Rules[0].ObjectFactProvider != probe {
		t.Fatalf("未保留宿主的 Go Provider: %+v, %v", snapshot.Rules, err)
	}
	resolved, err := sim.ResolveOwner(owner)
	if err != nil || resolved.Endpoint != "inproc://changed" {
		t.Fatalf("端点未更新: %+v, %v", resolved, err)
	}
	second, err := sim.ProduceOne(ctx, testPhysicalID("p1"))
	if err != nil || second.Match == nil || second.Match.Round != first.Match.Round || second.Match.Now != first.Match.Now {
		t.Fatalf("更新破坏了当前轮次或原队列: %+v, %v", second, err)
	}
	if _, err := sim.AddTicket(ctx, TicketInput{Rule: key.Rule, TicketID: 3}); err != nil {
		t.Fatal(err)
	}
	if err := sim.UpdatePhysicalNodeFromLibrary(ctx, physical, nil); err != nil {
		t.Fatal(err)
	}
	page, err := sim.ListTickets(ctx, TicketQuery{})
	if err != nil || page.Total != 0 {
		t.Fatalf("卸载部署后未清除其等待队列: %+v, %v", page, err)
	}
	history, err := sim.ListMatches(ctx, MatchQuery{})
	if err != nil || history.Total != 2 {
		t.Fatalf("卸载部署影响了 Match 历史: %+v, %v", history, err)
	}
	topology, err := sim.Topology(ctx)
	if err != nil || len(topology.PhysicalNodes) != 1 || len(topology.PhysicalNodes[0].LogicalNodes) != 0 {
		t.Fatalf("空物理节点未保留: %+v, %v", topology, err)
	}
	if err := sim.UpdatePhysicalNodeFromLibrary(ctx, physical, []RuleDeploymentSpec{deploymentFor(key, 1)}); err != nil {
		t.Fatalf("不能向空节点重新部署规则: %v", err)
	}
	if err := sim.UpdatePhysicalNodeFromLibrary(ctx, NewPhysicalNodeSpec(testPhysicalID("missing"), "x"), nil); !errors.Is(err, ErrUnknownNode) {
		t.Fatalf("不存在的物理节点错误不正确: %v", err)
	}
}

func TestPhysicalNodeUpdateProviderFailureRollsBack(t *testing.T) {
	ctx := context.Background()
	scenario, key := testScenario()
	probe := &dynamicNodeProviderProbe{}
	scenario.Rules[0].ObjectFactProvider = probe
	sim, err := NewSimulator(scenario)
	if err != nil {
		t.Fatal(err)
	}
	defer sim.Close()
	if _, err := sim.AddTicket(ctx, TicketInput{Rule: key.Rule, TicketID: 77}); err != nil {
		t.Fatal(err)
	}
	library, _ := sim.RuleLibrary(ctx)
	changed := bytes.Replace(library.Rules[0], []byte(`"maxPlayers":8`), []byte(`"maxPlayers":7`), 1)
	if _, err := sim.ReplaceRuleLibrary(ctx, library.Revision, []json.RawMessage{changed}); err != nil {
		t.Fatal(err)
	}
	before, _ := sim.Scenario()
	events, _ := sim.Events(ctx, EventQuery{})
	probe.failAfter = probe.calls
	apply := deploymentFor(key, 1)
	apply.ApplyLatest = true
	physical := NewPhysicalNodeSpec(testPhysicalID("p1"), "inproc://changed")
	if err := sim.UpdatePhysicalNodeFromLibrary(ctx, physical, []RuleDeploymentSpec{apply}); err == nil {
		t.Fatal("Provider 握手失败的替换被接受")
	}
	after, _ := sim.Scenario()
	if !reflect.DeepEqual(before, after) {
		t.Fatal("Provider 握手失败改变了运行场景")
	}
	owner := identity.OwnerRef{PhysicalNodeID: testPhysicalID("p1"), LogicalNode: key}
	if _, found, err := sim.GetTicket(ctx, owner, 77); err != nil || !found {
		t.Fatalf("Provider 握手失败破坏了等待队列: found=%v err=%v", found, err)
	}
	resolved, err := sim.ResolveOwner(owner)
	if err != nil || resolved.Endpoint != "inproc://p1" {
		t.Fatalf("Provider 握手失败修改了路由: %+v, %v", resolved, err)
	}
	afterEvents, _ := sim.Events(ctx, EventQuery{})
	if afterEvents.Total != events.Total {
		t.Fatal("失败的更新不应发布事件")
	}
}

func TestPhysicalNodeUpdateKeepsExactTickFacts(t *testing.T) {
	ctx := context.Background()
	scenario, key := libraryScenario()
	old := []byte(`"facts":[{"name":"preferredRoles","type":"strings","scope":"object","maxValues":3}]`)
	withTick := []byte(`"facts":[{"name":"preferredRoles","type":"strings","scope":"object","maxValues":3},{"name":"waitingCount","type":"int64","scope":"tick"}]`)
	scenario.Rules[0].RuleJSON = bytes.Replace(scenario.Rules[0].RuleJSON, old, withTick, 1)
	if !bytes.Contains(scenario.Rules[0].RuleJSON, withTick) {
		t.Fatal("测试规则缺少 Tick Fact")
	}
	scenario.Rules[0].TickFacts.Int64Values = map[string]int64{"waitingCount": math.MaxInt64}
	sim, err := NewSimulator(scenario)
	if err != nil {
		t.Fatal(err)
	}
	defer sim.Close()
	if _, err := sim.AddTicket(ctx, TicketInput{Rule: key.Rule, TicketID: 44}); err != nil {
		t.Fatal(err)
	}
	deployment := deploymentFor(key, 7)
	deployment.TickFacts = FactSnapshot{Int64Values: map[string]int64{"waitingCount": 1}}
	deployment.KeepTickFacts = true
	if err := sim.UpdatePhysicalNodeFromLibrary(ctx, NewPhysicalNodeSpec(testPhysicalID("p1"), "inproc://changed"), []RuleDeploymentSpec{deployment}); err != nil {
		t.Fatal(err)
	}
	after, err := sim.Scenario()
	if err != nil || after.Rules[0].TickFacts.Int64Values["waitingCount"] != math.MaxInt64 {
		t.Fatalf("路由编辑改变了精确 Tick Fact: %+v, %v", after.Rules[0].TickFacts, err)
	}
	owner := identity.OwnerRef{PhysicalNodeID: testPhysicalID("p1"), LogicalNode: key}
	if _, found, err := sim.GetTicket(ctx, owner, 44); err != nil || !found {
		t.Fatalf("保留 Tick Facts 时等待队列被清除: found=%v err=%v", found, err)
	}
	other := deployment
	other.LogicalNode.PlacementID = testPlacementID("new")
	err = sim.AddPhysicalNodeFromLibrary(ctx, NewPhysicalNodeSpec(testPhysicalID("p2"), "inproc://p2"), []RuleDeploymentSpec{other})
	var validation *ValidationError
	if !errors.As(err, &validation) || len(validation.Issues) == 0 || validation.Issues[0].Code != "KEEP_TICK_FACTS_REQUIRES_EXISTING" {
		t.Fatalf("新部署不应接受 keepTickFacts: %v", err)
	}
}

func TestRuleDeploymentJSONRequiresEnabled(t *testing.T) {
	for _, body := range []string{
		`{"logicalNode":{"rule":{"ruleId":1},"placementId":"1481096887605153049"},"weight":1}`,
		`{"logicalNode":{"rule":{"ruleId":1},"placementId":"1481096887605153049"},"weight":1,"enabled":null}`,
		`{"logicalNode":{"rule":{"ruleId":1,"unexpected":true},"placementId":"1481096887605153049"},"weight":1,"enabled":false}`,
	} {
		var deployment RuleDeploymentSpec
		if err := json.Unmarshal([]byte(body), &deployment); err == nil {
			t.Fatalf("无效部署 JSON 被接受: %s", body)
		}
	}
	var deployment RuleDeploymentSpec
	if err := json.Unmarshal([]byte(`{"logicalNode":{"rule":{"ruleId":1},"placementId":"1481096887605153049"},"weight":1,"enabled":false,"keepTickFacts":true}`), &deployment); err != nil || deployment.Enabled || !deployment.KeepTickFacts {
		t.Fatalf("显式 false 或 keepTickFacts 解码错误: %+v, %v", deployment, err)
	}
}

func TestLibraryDeploymentVersionConflictAndSelectiveReplacement(t *testing.T) {
	ctx := context.Background()
	scenario, firstKey := libraryScenario()
	sim, err := NewSimulator(scenario)
	if err != nil {
		t.Fatal(err)
	}
	defer sim.Close()
	library, _ := sim.RuleLibrary(ctx)
	secondRule := libraryRuleID(t, library.Rules[0], "2")
	library, err = sim.ReplaceRuleLibrary(ctx, library.Revision, []json.RawMessage{library.Rules[0], secondRule})
	if err != nil {
		t.Fatal(err)
	}
	secondKey := firstKey
	secondKey.Rule.RuleID = 2
	secondKey.PlacementID = testPlacementID("second")
	p1 := NewPhysicalNodeSpec(testPhysicalID("p1"), "inproc://p1")
	if err := sim.UpdatePhysicalNodeFromLibrary(ctx, p1, []RuleDeploymentSpec{deploymentFor(firstKey, 1), deploymentFor(secondKey, 1)}); err != nil {
		t.Fatal(err)
	}
	p2 := NewPhysicalNodeSpec(testPhysicalID("p2"), "inproc://p2")
	remoteKey := firstKey
	remoteKey.PlacementID = testPlacementID("remote")
	if err := sim.AddPhysicalNodeFromLibrary(ctx, p2, []RuleDeploymentSpec{deploymentFor(remoteKey, 1)}); err != nil {
		t.Fatal(err)
	}
	for _, target := range []struct {
		owner identity.OwnerRef
		id    uint64
	}{
		{identity.OwnerRef{PhysicalNodeID: testPhysicalID("p1"), LogicalNode: firstKey}, 10},
		{identity.OwnerRef{PhysicalNodeID: testPhysicalID("p1"), LogicalNode: secondKey}, 20},
		{identity.OwnerRef{PhysicalNodeID: testPhysicalID("p2"), LogicalNode: remoteKey}, 30},
	} {
		_, err := sim.AddRoutedTicket(ctx, common.RouteDecision{Owner: target.owner}, TicketInput{Rule: target.owner.LogicalNode.Rule, TicketID: target.id})
		if err != nil {
			t.Fatalf("插入测试 Ticket 失败: %v", err)
		}
	}
	updated := bytes.Replace(library.Rules[0], []byte(`"maxPlayers":8`), []byte(`"maxPlayers":7`), 1)
	if bytes.Equal(updated, library.Rules[0]) {
		t.Fatal("测试规则未修改")
	}
	if _, err := sim.ReplaceRuleLibrary(ctx, library.Revision, []json.RawMessage{updated, secondRule}); err != nil {
		t.Fatal(err)
	}
	apply := deploymentFor(firstKey, 1)
	apply.ApplyLatest = true
	before, _ := sim.Scenario()
	err = sim.UpdatePhysicalNodeFromLibrary(ctx, p1, []RuleDeploymentSpec{apply, deploymentFor(secondKey, 1)})
	var validation *ValidationError
	if !errors.As(err, &validation) || len(validation.Issues) == 0 || validation.Issues[0].Code != "RULE_CONFIG_MISMATCH" {
		t.Fatalf("跨节点版本冲突未拒绝: %v", err)
	}
	after, _ := sim.Scenario()
	if !reflect.DeepEqual(before, after) {
		t.Fatal("版本冲突改变了运行场景")
	}
	if err := sim.UpdatePhysicalNodeFromLibrary(ctx, p2, nil); err != nil {
		t.Fatal(err)
	}
	if err := sim.UpdatePhysicalNodeFromLibrary(ctx, p1, []RuleDeploymentSpec{apply, deploymentFor(secondKey, 1)}); err != nil {
		t.Fatal(err)
	}
	page, err := sim.ListTickets(ctx, TicketQuery{})
	if err != nil || page.Total != 1 || page.Items[0].Owner.LogicalNode != secondKey || page.Items[0].TicketID != 20 {
		t.Fatalf("替换规则没有只清理变更部署的队列: %+v, %v", page, err)
	}
	if _, err := sim.ReplaceRuleLibrary(ctx, library.Revision+1, nil); err != nil {
		t.Fatal(err)
	}
	if err := sim.UpdatePhysicalNodeFromLibrary(ctx, p1, []RuleDeploymentSpec{deploymentFor(firstKey, 2), deploymentFor(secondKey, 1)}); err != nil {
		t.Fatalf("规则库删除后不能保留已部署快照: %v", err)
	}
	if err := sim.UpdatePhysicalNodeFromLibrary(ctx, p1, []RuleDeploymentSpec{apply, deploymentFor(secondKey, 1)}); err == nil {
		t.Fatal("ApplyLatest 不应接受已从规则库删除的规则")
	}
}

func TestPhysicalNodeUpdateWeightedSelectorAndRound(t *testing.T) {
	ctx := context.Background()
	scenario, firstKey := libraryScenario()
	sim, err := NewSimulator(scenario)
	if err != nil {
		t.Fatal(err)
	}
	defer sim.Close()
	library, _ := sim.RuleLibrary(ctx)
	_, err = sim.ReplaceRuleLibrary(ctx, library.Revision, []json.RawMessage{library.Rules[0], libraryRuleID(t, library.Rules[0], "2")})
	if err != nil {
		t.Fatal(err)
	}
	secondKey := firstKey
	secondKey.Rule.RuleID = 2
	secondKey.PlacementID = testPlacementID("second")
	physical := NewPhysicalNodeSpec(testPhysicalID("p1"), "inproc://p1")
	physical.Selector = SelectorWeighted
	if err := sim.BeginRound(ctx, 100); err != nil {
		t.Fatal(err)
	}
	if err := sim.UpdatePhysicalNodeFromLibrary(ctx, physical, []RuleDeploymentSpec{deploymentFor(firstKey, 1), deploymentFor(secondKey, 5)}); err != nil {
		t.Fatal(err)
	}
	for _, key := range []identity.LogicalNodeKey{firstKey, secondKey} {
		owner := identity.OwnerRef{PhysicalNodeID: testPhysicalID("p1"), LogicalNode: key}
		for id := uint64(1); id <= 6; id++ {
			if _, err := sim.AddRoutedTicket(ctx, common.RouteDecision{Owner: owner}, TicketInput{Rule: key.Rule, TicketID: id}); err != nil {
				t.Fatal(err)
			}
		}
	}
	// 票据在轮次开始后加入，先开启新轮次使它们成为种子。
	if err := sim.BeginRound(ctx, 200); err != nil {
		t.Fatal(err)
	}
	counts := make(map[identity.RuleKey]int)
	for i := 0; i < 6; i++ {
		result, err := sim.ProduceOne(ctx, testPhysicalID("p1"))
		if err != nil || result.Match == nil {
			t.Fatalf("加权匹配失败: %+v, %v", result, err)
		}
		counts[result.LogicalNode.Rule]++
	}
	if counts[firstKey.Rule] != 1 || counts[secondKey.Rule] != 5 {
		t.Fatalf("新部署的调度权重未生效: %+v", counts)
	}
	if err := sim.BeginRound(ctx, 300); err != nil && !errors.Is(err, matchsystem.ErrNoLogicalNodeAvailable) {
		t.Fatal(err)
	}
}

func TestPhysicalNodeUpdateKeepsTrafficRunning(t *testing.T) {
	scenario, key := testScenario()
	sim, err := NewSimulator(scenario)
	if err != nil {
		t.Fatal(err)
	}
	defer sim.Close()
	_, err = sim.StartTraffic(TrafficConfig{Distribution: "constant", Rate: 100, MatchIntervalMS: 100, MaxMatches: 10}, BatchGeneratorSpec{Rule: key.Rule})
	if err != nil {
		t.Fatal(err)
	}
	awaitTraffic(t, sim, func(status TrafficStatus) bool { return status.Injected >= 2 })
	physical := NewPhysicalNodeSpec(testPhysicalID("p1"), "inproc://changed")
	if err := sim.UpdatePhysicalNodeFromLibrary(context.Background(), physical, []RuleDeploymentSpec{deploymentFor(key, 2)}); err != nil {
		t.Fatal(err)
	}
	status := sim.Traffic()
	if status.State != "running" {
		t.Fatalf("物理节点更新停止了持续流量: %+v", status)
	}
	awaitTraffic(t, sim, func(next TrafficStatus) bool { return next.Injected > status.Injected })
}
