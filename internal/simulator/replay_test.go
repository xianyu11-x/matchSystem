package simulator

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/xianyu11-x/matchSystem/pkg/identity"
	"github.com/xianyu11-x/matchSystem/pkg/matchsystem"
	"github.com/xianyu11-x/matchSystem/pkg/matchsystem/contract"
	"github.com/xianyu11-x/matchSystem/pkg/matchsystem/fact"
)

func replayPairScenario(t *testing.T) (Scenario, []byte) {
	t.Helper()
	scenario := formRuntimeScenario(t, `{"type":"arrival","params":{}}`, `{"type":"constant","params":{"value":0}}`, true)
	var rule map[string]any
	if err := json.Unmarshal(scenario.Rules[0].RuleJSON, &rule); err != nil {
		t.Fatal(err)
	}
	rule["runtime"] = map[string]any{"maxPlayers": 4, "candidateScoringLimitPerSeed": 10, "candidateLimitPerSeed": 10, "attemptLimitPerProduceMatch": 1, "attemptLimitPerMatchRound": 10}
	complete := rule["evaluation"].(map[string]any)["canComplete"].(map[string]any)["expr"].(map[string]any)
	complete["right"].(map[string]any)["value"] = 4
	source, err := json.Marshal(rule)
	if err != nil {
		t.Fatal(err)
	}
	complete["right"].(map[string]any)["value"] = 2
	rule["ruleKey"].(map[string]any)["ruleId"] = 2
	target, err := json.Marshal(rule)
	if err != nil {
		t.Fatal(err)
	}
	scenario.Rules[0].RuleJSON = source
	scenario.Rules[0].ObjectFactProvider = nil
	return scenario, target
}

func installReplayTarget(t *testing.T, sim *Simulator, target []byte) {
	t.Helper()
	ctx := context.Background()
	library, err := sim.RuleLibrary(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sim.ReplaceRuleLibrary(ctx, library.Revision, append(library.Rules, target)); err != nil {
		t.Fatal(err)
	}
}

func extractedReplay(t *testing.T, sim *Simulator) ReplayExtractResult {
	t.Helper()
	ctx := context.Background()
	instances, err := sim.ExtractTimeline(ctx, ReplayExtractRequest{})
	if err != nil || len(instances.Instances) != 1 {
		t.Fatalf("实例列表错误: %+v, %v", instances, err)
	}
	extracted, err := sim.ExtractTimeline(ctx, ReplayExtractRequest{InstanceID: instances.Instances[0].InstanceID})
	if err != nil || !extracted.Instances[0].Replayable || extracted.TimelineJSON == "" {
		t.Fatalf("提取失败: %+v, %v", extracted, err)
	}
	return extracted
}

func TestReplayChangesMembersAndKeepsSameRoundWithdrawalOrder(t *testing.T) {
	ctx := context.Background()
	scenario, target := replayPairScenario(t)
	sim, err := NewSimulator(scenario)
	if err != nil {
		t.Fatal(err)
	}
	defer sim.Close()
	installReplayTarget(t, sim, target)
	key := scenario.Rules[0].LogicalNode
	owner := identity.OwnerRef{LogicalNode: key, PhysicalNodeID: scenario.PhysicalNodes[0].ID}
	for _, id := range []uint64{9007199254740993, 9007199254740994, 9007199254740995} {
		_, err := sim.AddTicket(ctx, TicketInput{Rule: key.Rule, TicketID: id, CreatedAt: 10, StringLists: map[string][]string{"pool": {"all"}}})
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := sim.BeginRound(ctx, 1234); err != nil {
		t.Fatal(err)
	}
	if first, err := sim.ProduceOne(ctx, owner.PhysicalNodeID); err != nil || first.Match != nil {
		t.Fatalf("源四人规则不应成局: %+v, %v", first, err)
	}
	if removed, err := sim.RemoveTicket(ctx, owner, 9007199254740994); err != nil || !removed {
		t.Fatalf("源撤票失败: %v, %v", removed, err)
	}
	if second, err := sim.ProduceOne(ctx, owner.PhysicalNodeID); err != nil || second.Match != nil {
		t.Fatalf("源第二次调用异常: %+v, %v", second, err)
	}
	extracted := extractedReplay(t, sim)
	if !strings.Contains(extracted.TimelineJSON, `9007199254740993`) {
		t.Fatal("时间线丢失 64 位 TicketID 精度")
	}
	result, err := sim.ReplayTimeline(ctx, ReplayRequest{TimelineJSON: extracted.TimelineJSON, TargetRuleID: "2"})
	if err != nil || result.Status != "completed" || result.Calls != 2 || result.Matches != 1 {
		t.Fatalf("回放失败: %+v, %v", result, err)
	}
	var matched []string
	var statuses []string
	for _, event := range result.Events {
		if event.Type == "produce_match" {
			statuses = append(statuses, event.Status)
			matched = append(matched, event.TicketIDs...)
		} else if event.Type == "ticket_withdraw" {
			statuses = append(statuses, event.Status)
		}
	}
	if !reflect.DeepEqual(matched, []string{"9007199254740993", "9007199254740994"}) || !reflect.DeepEqual(statuses, []string{"matched", "already_left", "no_match"}) {
		t.Fatalf("成员或同轮顺序错误: %v %v", matched, statuses)
	}
	page, err := sim.ListTickets(ctx, TicketQuery{})
	if err != nil || page.Total != 2 {
		t.Fatalf("回放改变源队列: %+v, %v", page, err)
	}
}

func TestReplayExtractDedupAndRejectMalformedOrIncomplete(t *testing.T) {
	ctx := context.Background()
	scenario, target := replayPairScenario(t)
	sim, err := NewSimulator(scenario)
	if err != nil {
		t.Fatal(err)
	}
	defer sim.Close()
	installReplayTarget(t, sim, target)
	_, err = sim.AddTicket(ctx, TicketInput{Rule: scenario.Rules[0].LogicalNode.Rule, TicketID: 1, StringLists: map[string][]string{"pool": {"all"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := sim.SetObjectFacts(ctx, identity.OwnerRef{PhysicalNodeID: scenario.PhysicalNodes[0].ID, LogicalNode: scenario.Rules[0].LogicalNode}, 1, FactSnapshot{}); err != nil {
		t.Fatal(err)
	}
	extracted := extractedReplay(t, sim)
	lines := strings.Split(strings.TrimSpace(sim.replayLog.snapshot()), "\n")
	for left, right := 0, len(lines)-1; left < right; left, right = left+1, right-1 {
		lines[left], lines[right] = lines[right], lines[left]
	}
	logs := strings.Join(lines, "\n") + "\n" + strings.Join(lines, "\n")
	dedup, err := sim.ExtractTimeline(ctx, ReplayExtractRequest{Logs: &logs, InstanceID: extracted.Instances[0].InstanceID})
	if err != nil || dedup.TimelineJSON != extracted.TimelineJSON {
		t.Fatalf("乱序轮转去重失败: %+v, %v", dedup, err)
	}
	var timeline replayTimeline
	if err := json.Unmarshal([]byte(extracted.TimelineJSON), &timeline); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		data string
	}{
		{"未知字段", strings.Replace(extracted.TimelineJSON, `"schemaVersion":`, `"unknown":1,"schemaVersion":`, 1)},
		{"重复字段", strings.Replace(extracted.TimelineJSON, `"schemaVersion":`, `"schemaVersion":"match-timeline/v1","schemaVersion":`, 1)},
		{"缺流头", func() string {
			copy := timeline
			copy.Events = copy.Events[1:]
			data, _ := json.Marshal(copy)
			return string(data)
		}()},
		{"事实更新缺号", func() string {
			copy := timeline
			copy.Facts = append([]replayFactsRecord(nil), timeline.Facts...)
			copy.Facts[0].Order = 2
			data, _ := json.Marshal(copy)
			return string(data)
		}()},
		{"时钟偏移溢出", func() string {
			copy := timeline
			copy.Events = append([]matchsystem.TimelineEvent(nil), timeline.Events...)
			copy.Events[len(copy.Events)-1].OffsetNS = math.MaxInt64
			data, _ := json.Marshal(copy)
			return string(data)
		}()},
		{"源规则身份不符", func() string {
			copy := timeline
			copy.Events = append([]matchsystem.TimelineEvent(nil), timeline.Events...)
			copy.Events[0].RuleJSON = libraryRuleID(t, copy.Events[0].RuleJSON, "3")
			data, _ := json.Marshal(copy)
			return string(data)
		}()},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := sim.ReplayTimeline(ctx, ReplayRequest{TimelineJSON: test.data, TargetRuleID: "2"}); err == nil {
				t.Fatal("无效时间线未被拒绝")
			}
		})
	}
}

func TestReplayRejectsMissingAndIncompatibleAttributes(t *testing.T) {
	scenario, _ := replayPairScenario(t)
	source, err := matchsystem.CompileRuleJSON(scenario.Rules[0].RuleJSON)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		modify func(*contract.Contract)
	}{
		{"源契约不提供", func(target *contract.Contract) {
			target.Attributes = append(target.Attributes, contract.AttributeSpec{Name: "region", Type: fact.TypeInt64})
		}},
		{"同名属性类型不同", func(target *contract.Contract) {
			for index := range target.Attributes {
				if target.Attributes[index].Name == "rating" {
					target.Attributes[index].Type = fact.TypeStrings
					target.Attributes[index].MaxValues = 1
				}
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			target := source.Contract()
			test.modify(&target)
			if err := validateReplayContracts(replayTimeline{}, source.Contract(), target, false); err == nil || !strings.Contains(err.Error(), "属性") {
				t.Fatalf("不兼容属性未在无票据时拒绝: %v", err)
			}
		})
	}
}

func TestReplaySeparatesRecreatedInstances(t *testing.T) {
	ctx := context.Background()
	scenario, key := libraryScenario()
	sim, err := NewSimulator(scenario)
	if err != nil {
		t.Fatal(err)
	}
	defer sim.Close()
	add := func(value string) {
		t.Helper()
		_, err := sim.AddTicket(ctx, TicketInput{Rule: key.Rule, TicketID: 1, ObjectFacts: FactSnapshot{StringLists: map[string][]string{"preferredRoles": {value}}}})
		if err != nil {
			t.Fatal(err)
		}
	}
	add("old")
	old := extractedReplay(t, sim)
	if err := sim.ReplaceScenario(ctx, scenario); err != nil {
		t.Fatal(err)
	}
	add("new")
	list, err := sim.ExtractTimeline(ctx, ReplayExtractRequest{})
	if err != nil || len(list.Instances) != 2 || list.Instances[0].InstanceID == list.Instances[1].InstanceID {
		t.Fatalf("重建节点未生成独立实例: %+v %v", list, err)
	}
	for _, item := range list.Instances {
		extracted, err := sim.ExtractTimeline(ctx, ReplayExtractRequest{InstanceID: item.InstanceID})
		if err != nil || !item.Replayable {
			t.Fatalf("实例时间线不可回放: %+v %v", item, err)
		}
		var timeline replayTimeline
		if err := json.Unmarshal([]byte(extracted.TimelineJSON), &timeline); err != nil {
			t.Fatal(err)
		}
		want := "new"
		if item.InstanceID == old.Instances[0].InstanceID {
			want = "old"
			if !item.Ended {
				t.Fatal("场景替换后旧实例没有结束标记")
			}
		} else if item.Ended {
			t.Fatal("新实例不应已经结束")
		}
		if len(timeline.Facts) != 1 || timeline.Facts[0].Facts.StringLists["preferredRoles"][0] != want {
			t.Fatalf("实例事实混淆: %+v, want %s", timeline.Facts, want)
		}
	}
}

func TestReplayFactSnapshotsAndContractCompatibility(t *testing.T) {
	ctx := context.Background()
	scenario, key := libraryScenario()
	sim, err := NewSimulator(scenario)
	if err != nil {
		t.Fatal(err)
	}
	defer sim.Close()
	installReplayTarget(t, sim, libraryRuleID(t, scenario.Rules[0].RuleJSON, "2"))
	owner := identity.OwnerRef{PhysicalNodeID: scenario.PhysicalNodes[0].ID, LogicalNode: key}
	_, err = sim.AddTicket(ctx, TicketInput{Rule: key.Rule, TicketID: 1, ObjectFacts: FactSnapshot{StringLists: map[string][]string{"preferredRoles": {"one"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := sim.SetObjectFacts(ctx, owner, 1, FactSnapshot{StringLists: map[string][]string{"preferredRoles": {"two"}}}); err != nil {
		t.Fatal(err)
	}
	if err := sim.BeginRound(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := sim.ProduceOne(ctx, owner.PhysicalNodeID); err != nil {
		t.Fatal(err)
	}
	extracted := extractedReplay(t, sim)
	result, err := sim.ReplayTimeline(ctx, ReplayRequest{TimelineJSON: extracted.TimelineJSON, TargetRuleID: "2"})
	if err != nil || result.Status != "completed" || result.Matches != 1 {
		t.Fatalf("带宿主 Fact 快照回放失败: %+v %v", result, err)
	}
	var timeline replayTimeline
	if err := json.Unmarshal([]byte(extracted.TimelineJSON), &timeline); err != nil {
		t.Fatal(err)
	}
	if len(timeline.Facts) != 2 || timeline.Facts[0].Facts.StringLists["preferredRoles"][0] != "one" || timeline.Facts[1].Facts.StringLists["preferredRoles"][0] != "two" {
		t.Fatalf("Fact 更新顺序丢失: %+v", timeline.Facts)
	}
	timeline.Facts = nil
	data, _ := json.Marshal(timeline)
	if _, err := sim.ReplayTimeline(ctx, ReplayRequest{TimelineJSON: string(data), TargetRuleID: "2"}); err == nil || !strings.Contains(err.Error(), "preferredRoles") {
		t.Fatalf("缺少宿主 Fact 未拒绝: %v", err)
	}
	for _, changed := range []struct{ name, field, value string }{
		{"scope", `"scope":"object"`, `"scope":"tick"`},
		{"maxValues", `"maxValues":3`, `"maxValues":4`},
	} {
		t.Run(changed.name, func(t *testing.T) {
			altered := strings.Replace(string(scenario.Rules[0].RuleJSON), changed.field, changed.value, 1)
			if changed.name == "scope" {
				// 仅比较契约：此变种的 Provider 不可装载，但仍须在回放前拒绝。
			}
			compiled, err := matchsystem.CompileRuleJSON([]byte(altered))
			if err != nil {
				t.Fatal(err)
			}
			source, err := matchsystem.CompileRuleJSON(scenario.Rules[0].RuleJSON)
			if err != nil {
				t.Fatal(err)
			}
			if err := validateReplayContracts(replayTimeline{}, source.Contract(), compiled.Contract(), false); err == nil {
				t.Fatal(fmt.Sprintf("%s 不兼容未拒绝", changed.name))
			}
		})
	}
}

func TestReplayProviderIsolation(t *testing.T) {
	ctx := context.Background()
	t.Run("Go 状态实例无法复制", func(t *testing.T) {
		scenario, key := testScenario()
		sim, err := NewSimulator(scenario)
		if err != nil {
			t.Fatal(err)
		}
		defer sim.Close()
		installReplayTarget(t, sim, libraryRuleID(t, scenario.Rules[0].RuleJSON, "2"))
		extracted := extractedReplay(t, sim)
		_, err = sim.ReplayTimeline(ctx, ReplayRequest{TimelineJSON: extracted.TimelineJSON, TargetRuleID: "2", ProviderLogicalNode: &key})
		if err == nil || !strings.Contains(err.Error(), "无法安全复制") {
			t.Fatalf("共享 Go Provider 未被拒绝: %v", err)
		}
	})
	t.Run("Python 使用全新进程", func(t *testing.T) {
		python := testPythonExecutable(t)
		script := writePythonProviderScript(t, `count = 0
def describe():
    return {"match": {"id": "replay.stateful", "version": "v1", "facts": [
        {"name": "memberCount", "type": "int64", "scope": "match"}]}}
def provide(method, data):
    global count
    if method == "initialize":
        count += 1
        return {"Int64Values": {"memberCount": 1 if count == 1 else -100}}
    if method == "join":
        return {"Int64Values": {"memberCount": data["MatchFactsBefore"]["Int64Values"]["memberCount"] + 1}}
    raise ValueError("unexpected method")
`)
		scenario, target := replayPairScenario(t)
		scenario.Rules[0].PythonProvider = &PythonProviderConfig{Python: python, Script: script, TimeoutMS: 1000}
		sim, err := NewSimulator(scenario)
		if err != nil {
			t.Fatal(err)
		}
		defer sim.Close()
		installReplayTarget(t, sim, target)
		key := scenario.Rules[0].LogicalNode
		for _, id := range []uint64{1, 2} {
			if _, err := sim.AddTicket(ctx, TicketInput{Rule: key.Rule, TicketID: id, StringLists: map[string][]string{"pool": {"all"}}}); err != nil {
				t.Fatal(err)
			}
		}
		if err := sim.BeginRound(ctx, 123); err != nil {
			t.Fatal(err)
		}
		if source, err := sim.ProduceOne(ctx, scenario.PhysicalNodes[0].ID); err != nil || source.Match != nil {
			t.Fatalf("源调用失败: %+v %v", source, err)
		}
		extracted := extractedReplay(t, sim)
		result, err := sim.ReplayTimeline(ctx, ReplayRequest{TimelineJSON: extracted.TimelineJSON, TargetRuleID: "2", ProviderLogicalNode: &key})
		if err != nil || result.Status != "completed" || result.Provider != "python" || result.Matches != 1 {
			t.Fatalf("Python 状态没有隔离: %+v %v", result, err)
		}
		page, err := sim.ListTickets(ctx, TicketQuery{})
		if err != nil || page.Total != 2 {
			t.Fatalf("目标 Python 调用改变源队列: %+v %v", page, err)
		}
	})
}
