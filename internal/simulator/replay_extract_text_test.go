package simulator

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"testing"

	"github.com/xianyu11-x/matchSystem/pkg/identity"
	"github.com/xianyu11-x/matchSystem/pkg/matchsystem"
)

func TestReplayExtractMixedTextAndJSONLines(t *testing.T) {
	ctx := context.Background()
	scenario, key := libraryScenario()
	sim, err := NewSimulator(scenario)
	if err != nil {
		t.Fatal(err)
	}
	defer sim.Close()
	const ticketID = uint64(9007199254740993)
	_, err = sim.AddTicket(ctx, TicketInput{Rule: key.Rule, TicketID: ticketID, ObjectFacts: FactSnapshot{StringLists: map[string][]string{"preferredRoles": {"one"}}}})
	if err != nil {
		t.Fatal(err)
	}
	owner := identity.OwnerRef{PhysicalNodeID: scenario.PhysicalNodes[0].ID, LogicalNode: key}
	if err := sim.SetObjectFacts(ctx, owner, ticketID, FactSnapshot{StringLists: map[string][]string{"preferredRoles": {"two"}}}); err != nil {
		t.Fatal(err)
	}
	baseline := extractedReplay(t, sim)
	lines := strings.Split(strings.TrimSpace(sim.replayLog.snapshot()), "\n")
	mixed := []string{
		"2026-11-24 17:25:16 INFO 匹配开始",
		`2026-11-24 17:25:16 INFO 普通消息引用了 [match_replay] 标记`,
		`{"message":"普通 JSON 日志中的 [match_replay] 标记"}`,
	}
	var eventMarked, factMarked, plainJSON bool
	for i, line := range lines {
		line = strings.TrimPrefix(line, matchsystem.TimelineLogMessage+" ")
		if strings.Contains(line, `"timelineFacts":`) {
			// 旧版外层字段和原有事件对象一起保留，不先转成 float64。
			line = strings.Replace(line, "{", `{"oldEnvelope":"保留",`, 1)
			mixed = append(mixed, "2026-11-24T17:25:16.123456789+08:00 WARN [match_replay] "+line)
			factMarked = true
		} else if i%2 == 0 {
			mixed = append(mixed, "[match_replay]\t"+line)
			eventMarked = true
		} else {
			mixed = append(mixed, line)
			plainJSON = true
		}
	}
	if !eventMarked || !factMarked || !plainJSON {
		t.Fatal("测试数据未同时覆盖事件、Fact 和 JSONL")
	}
	logs := strings.Join(mixed, "\n")
	result, err := sim.ExtractTimeline(ctx, ReplayExtractRequest{Logs: &logs, InstanceID: baseline.Instances[0].InstanceID})
	if err != nil || len(result.Instances) != 1 || !result.Instances[0].Replayable || result.TimelineJSON != baseline.TimelineJSON {
		t.Fatalf("混合日志未还原原始时间线: %+v, %v", result, err)
	}
	if !strings.Contains(result.TimelineJSON, "9007199254740993") || !strings.Contains(result.TimelineJSON, `"preferredRoles":["two"]`) {
		t.Fatalf("票据精度或 Fact 快照丢失: %s", result.TimelineJSON)
	}
}

func TestReplayExtractTextPrefixesAnd64BitValues(t *testing.T) {
	const event = `{"timeline":{"instanceId":"node","sequence":18446744073709551615,"ticketId":18446744073709551615,"now":-9223372036854775808}}`
	const facts = `{"timelineFacts":{"instanceId":"node","order":18446744073709551615,"ticketId":18446744073709551615,"addSequence":18446744073709551615,"facts":{"uint64s":{"ids":[18446744073709551615]},"int64s":{"value":-9223372036854775808}}}}`
	for _, prefix := range []string{
		"[match_replay] ",
		"2026-11-24 17:25:16 INFO [match_replay] ",
		"2026-11-24 17:25:16.123\tdebug\t[match_replay]\t",
		"2026-11-24T17:25:16,123456Z WARNING [match_replay] ",
		"2026-11-24T17:25:16.123456789+08:00 ERROR [match_replay] ",
		"2026-11-24 17:25:16-0800 FATAL [match_replay] ",
		"[INFO] 24/Nov/26 service | [match_replay] ",
		"2026/11/24 17:25:16 [INFO] match.node: [match_replay] ",
		"宿主前缀：[match_replay] ",
		"time=2026 level=INFO msg=[match_replay] ",
	} {
		t.Run(prefix, func(t *testing.T) {
			logs := prefix + event + "\n" + prefix + facts
			result, err := (&Simulator{}).ExtractTimeline(context.Background(), ReplayExtractRequest{Logs: &logs, InstanceID: "node"})
			if err != nil || len(result.Instances) != 1 {
				t.Fatalf("合法文本前缀被拒绝: %+v, %v", result, err)
			}
			var timeline replayTimeline
			if err := json.Unmarshal([]byte(result.TimelineJSON), &timeline); err != nil {
				t.Fatal(err)
			}
			if len(timeline.Events) != 1 || timeline.Events[0].Sequence != math.MaxUint64 || timeline.Events[0].TicketID != math.MaxUint64 || timeline.Events[0].Now != math.MinInt64 ||
				len(timeline.Facts) != 1 || timeline.Facts[0].TicketID != math.MaxUint64 || timeline.Facts[0].Facts.Uint64Lists["ids"][0] != math.MaxUint64 || timeline.Facts[0].Facts.Int64Values["value"] != math.MinInt64 {
				t.Fatalf("事件或 Fact 的 64 位值改变: %+v", timeline)
			}
		})
	}
}

func TestReplayExtractSlogHandlersAndMixedHostLogs(t *testing.T) {
	const event = `{"timeline":{"instanceId":"node","sequence":18446744073709551615,"ticketId":18446744073709551615,"now":-9223372036854775808}}`
	const facts = `{"timelineFacts":{"instanceId":"node","order":18446744073709551615,"ticketId":18446744073709551615,"addSequence":18446744073709551615,"facts":{"uint64s":{"ids":[18446744073709551615]},"int64s":{"value":-9223372036854775808}}}}`
	message := matchsystem.TimelineLogMessage + " " + event
	var jsonHandler, textHandler bytes.Buffer
	slog.New(slog.NewJSONHandler(&jsonHandler, nil)).Info(message, "other", "普通内容")
	slog.New(slog.NewTextHandler(&textHandler, nil)).Info(message, "other", "普通内容")
	encodedMessage, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	// msg 具有完整载荷时，忽略不同的顶层事件及 Fact，避免重复处理或冲突。
	conflictingWrapper := fmt.Sprintf(`{"msg":%s,"timeline":{"instanceId":"other","sequence":0},"timelineFacts":{}}`, encodedMessage)
	aliasWrapper := fmt.Sprintf(`{"message":%s}`, encodedMessage)
	oldJSONL := strings.Replace(event, "{", `{"msg":"[match_replay]",`, 1)
	combined := strings.TrimSuffix(event, "}") + "," + strings.TrimPrefix(facts, "{")
	logs := strings.Join([]string{
		"无日期格式的普通宿主日志",
		`普通记录提及 [match_replay] 标记及 [match_replay_debug] {"timeline":{}}`,
		`time=2026-11-24 level=INFO msg="普通信息" other="[match_replay] {\"timeline\":{}}"`,
		`{"message":"[match_replay] 仅作普通描述"}`,
		`{"module":"lobby"} | ` + message,
		strings.TrimSpace(jsonHandler.String()),
		strings.TrimSpace(textHandler.String()),
		conflictingWrapper,
		aliasWrapper,
		oldJSONL,
		// 重复事件和首次 Fact 同在一行时，事件去重不能跳过后面的 Fact。
		"[match_replay] " + combined,
		"又一条无标记普通记录",
	}, "\n")
	result, err := (&Simulator{}).ExtractTimeline(context.Background(), ReplayExtractRequest{Logs: &logs, InstanceID: "node"})
	if err != nil || len(result.Instances) != 1 || result.Instances[0].EventCount != 1 {
		t.Fatalf("混合宿主日志未正确提取或去重: %+v, %v", result, err)
	}
	var timeline replayTimeline
	if err := json.Unmarshal([]byte(result.TimelineJSON), &timeline); err != nil {
		t.Fatal(err)
	}
	if len(timeline.Events) != 1 || timeline.Events[0].Sequence != math.MaxUint64 || timeline.Events[0].TicketID != math.MaxUint64 || timeline.Events[0].Now != math.MinInt64 ||
		len(timeline.Facts) != 1 || timeline.Facts[0].Order != math.MaxUint64 || timeline.Facts[0].Facts.Uint64Lists["ids"][0] != math.MaxUint64 || timeline.Facts[0].Facts.Int64Values["value"] != math.MinInt64 {
		t.Fatalf("标准日志包装、去重或 64 位精度错误: %+v", timeline)
	}
}

func TestReplayExtractMarkedLogErrorsWithMixedText(t *testing.T) {
	const valid = `[match_replay] {"timeline":{"instanceId":"node","sequence":1}}`
	for _, test := range []struct {
		name string
		line string
		code string
	}{
		{"任意前缀的事件截断", `[INFO] 24/Nov/26 svc | [match_replay] {"timeline":`, "INVALID_LOG_JSON"},
		{"JSON 前缀的事件截断", `{"module":"lobby"} | [match_replay] {"timelineFacts":`, "INVALID_LOG_JSON"},
		{"任意前缀非法事件", `[INFO] 24/Nov/26 svc | [match_replay] {"timeline":{}}`, "INVALID_TIMELINE_EVENT"},
		{"任意前缀非法 Fact", `[INFO] 24/Nov/26 svc | [match_replay] {"timelineFacts":{}}`, "INVALID_FACT_EVENT"},
		{"JSONHandler msg 载荷截断", `{"msg":"[match_replay] {\"timeline\":"}`, "INVALID_LOG_JSON"},
		{"JSON message 载荷截断", `{"message":"[match_replay] {\"timeline\":"}`, "INVALID_LOG_JSON"},
		{"TextHandler msg 引号截断", `time=2026 level=INFO msg="[match_replay] {\"timeline\":`, "INVALID_LOG_JSON"},
		{"TextHandler msg 事件非法", `time=2026 level=INFO msg="[match_replay] {\"timeline\":{}}"`, "INVALID_TIMELINE_EVENT"},
		{"行首标记缺少载荷", `[match_replay]`, "INVALID_LOG_JSON"},
		{"旧前缀后标记载荷损坏", `2026-11-24 17:25:16 INFO [match_replay] {`, "INVALID_LOG_JSON"},
		{"JSONL 标记 msg 截断", `{"msg":"[match_replay] {\"timeline\":`, "INVALID_LOG_JSON"},
	} {
		t.Run(test.name, func(t *testing.T) {
			logs := "混入普通宿主信息\n" + test.line + "\n" + valid
			_, err := (&Simulator{}).ExtractTimeline(context.Background(), ReplayExtractRequest{Logs: &logs})
			if err == nil || !strings.Contains(err.Error(), "第 2 行") || !strings.Contains(err.Error(), fmt.Sprintf("[%s]", test.code)) {
				t.Fatalf("损坏的记录未报告行号与预期错误: %v", err)
			}
		})
	}
}

func TestReplayExtractJSONMessageAliasBoundaries(t *testing.T) {
	const valid = `[match_replay] {"timeline":{"instanceId":"node","sequence":1}}`
	logs := strings.Join([]string{
		valid,
		`{"message":"[match_replay] 普通描述"}`,
		`{"message":"[match_replay] {\"other\":123}"}`,
		`{"message":"引用 [match_replay] {\"timeline\":{}}"}`,
		`{"msg":"普通消息","message":"[match_replay] {\"timeline\":{}}"}`,
		`{"msg":null,"message":"[match_replay] {\"timeline\":{}}"}`,
		`{"msg":"[match_replay_debug]","message":"[match_replay] {\"timeline\":{}}"}`,
		`{"msg":"普通说明 [match_replay] {\"timeline\":{}}`,
	}, "\n")
	result, err := (&Simulator{}).ExtractTimeline(context.Background(), ReplayExtractRequest{Logs: &logs})
	if err != nil || len(result.Instances) != 1 || result.Instances[0].EventCount != 1 {
		t.Fatalf("普通 message、相近标记或 msg 优先级处理错误: %+v, %v", result, err)
	}
}

func TestReplayExtractUnmarkedJSONLErrorsRemainStrict(t *testing.T) {
	for _, logs := range []string{
		"{\"msg\":\"ordinary\"}\n{\"timeline\":",
		"{\"msg\":\"ordinary\"}\n无标记的任意文本",
	} {
		_, err := (&Simulator{}).ExtractTimeline(context.Background(), ReplayExtractRequest{Logs: &logs})
		if err == nil || !strings.Contains(err.Error(), "第 2 行") || !strings.Contains(err.Error(), "[INVALID_LOG_JSON]") {
			t.Fatalf("无标记 JSONL 的损坏行应报告行号: %v", err)
		}
	}
}

func TestReplayExtractRejectsInvalidMarkedAndUnknownLines(t *testing.T) {
	for _, test := range []struct {
		name string
		line string
		code string
	}{
		{"缺载荷", "[match_replay]", "INVALID_LOG_JSON"},
		{"缺分隔", `[match_replay]{"timeline":{}}`, "INVALID_LOG_JSON"},
		{"载荷损坏", "2026-11-24 17:25:16 INFO [match_replay] {", "INVALID_LOG_JSON"},
		{"非对象", "[match_replay] []", "INVALID_LOG_JSON"},
		{"空对象", "[match_replay] {}", "INVALID_LOG_JSON"},
		{"空载荷", `[match_replay] {"timeline":null,"timelineFacts":null}`, "INVALID_LOG_JSON"},
		{"无时间线", `[match_replay] {"message":"普通日志"}`, "INVALID_LOG_JSON"},
		{"非法事件", `[match_replay] {"timeline":{}}`, "INVALID_TIMELINE_EVENT"},
		{"非法Fact", `[match_replay] {"timelineFacts":{}}`, "INVALID_FACT_EVENT"},
		{"损坏的JSONL", `{"timeline":`, "INVALID_LOG_JSON"},
		{"其他文本格式", "普通日志 [match_replay] {}", "INVALID_LOG_JSON"},
		{"日期不合法", "2026-13-24 17:25:16 INFO 普通日志", "INVALID_LOG_JSON"},
		{"顶层JSON字符串", `"[match_replay] {\"timeline\":{}}"`, "INVALID_LOG_JSON"},
	} {
		t.Run(test.name, func(t *testing.T) {
			logs := "\n2026-11-24 17:25:16 INFO 普通日志\n" + test.line
			_, err := (&Simulator{}).ExtractTimeline(context.Background(), ReplayExtractRequest{Logs: &logs})
			if err == nil || !strings.Contains(err.Error(), "第 3 行") || !strings.Contains(err.Error(), fmt.Sprintf("[%s]", test.code)) {
				t.Fatalf("非法日志未以预期的行号和类型报错: %v", err)
			}
		})
	}
}

func TestReplayExtractIgnoresOrdinaryLogMessages(t *testing.T) {
	for _, logs := range []string{
		"",
		"2026-11-24 17:25:16 INFO 匹配开始\n2026-11-24 17:25:16.123 WARN 模板 [match_replay] {}",
		`{"message":"[match_replay] 只是 JSON 字符串中的内容"}`,
	} {
		result, err := (&Simulator{}).ExtractTimeline(context.Background(), ReplayExtractRequest{Logs: &logs})
		if err != nil || len(result.Instances) != 0 {
			t.Fatalf("普通日志不应产生实例或错误: %+v, %v", result, err)
		}
	}
}
