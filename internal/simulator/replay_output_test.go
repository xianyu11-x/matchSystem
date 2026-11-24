package simulator

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xianyu11-x/matchSystem/pkg/common"
	"github.com/xianyu11-x/matchSystem/pkg/identity"
	"github.com/xianyu11-x/matchSystem/pkg/matchsystem"
)

func TestReplayOutputWritesCompleteFileAndVirtualTime(t *testing.T) {
	ctx := context.Background()
	scenario, key := libraryScenario()
	file, err := os.Create(filepath.Join(t.TempDir(), "record.log"))
	if err != nil {
		t.Fatal(err)
	}
	sim, err := NewSimulatorWithReplayLogOutput(scenario, file, func(err error) { t.Errorf("写入失败: %v", err) })
	if err != nil {
		t.Fatal(err)
	}
	installReplayTarget(t, sim, libraryRuleID(t, scenario.Rules[0].RuleJSON, "2"))
	owner := identity.OwnerRef{PhysicalNodeID: scenario.PhysicalNodes[0].ID, LogicalNode: key}
	_, err = sim.AddTicket(ctx, TicketInput{Rule: key.Rule, TicketID: 1, ObjectFacts: FactSnapshot{StringLists: map[string][]string{"preferredRoles": {"one"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := sim.SetObjectFacts(ctx, owner, 1, FactSnapshot{StringLists: map[string][]string{"preferredRoles": {"two"}}}); err != nil {
		t.Fatal(err)
	}
	const start = int64(1_700_000_000_000)
	_, err = sim.StartTraffic(TrafficConfig{
		Mode: TrafficModeFixed, Distribution: "burst", Rate: 1, BurstSize: 1,
		BurstIntervalMS: 100, MatchIntervalMS: 100, MaxMatches: 1,
		Steps: 3, StepMS: 100, StartTimeMS: start,
	}, BatchGeneratorSpec{Rule: key.Rule, FirstTicketID: 10, ObjectFacts: FactSnapshot{StringLists: map[string][]string{"preferredRoles": {"traffic"}}}})
	if err != nil {
		t.Fatal(err)
	}
	status := awaitTraffic(t, sim, func(status TrafficStatus) bool { return status.State == "completed" || status.State == "failed" })
	if status.State != "completed" {
		t.Fatalf("虚拟流量失败: %+v", status)
	}
	before, err := os.ReadFile(file.Name())
	if err != nil || string(before) != sim.replayLog.snapshot() {
		t.Fatalf("运行中磁盘和内存录制不同: %v", err)
	}
	if err := sim.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(file.Name())
	if err != nil {
		t.Fatal(err)
	}
	reader, err := NewSimulator(scenario)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	installReplayTarget(t, reader, libraryRuleID(t, scenario.Rules[0].RuleJSON, "2"))
	logs := string(data)
	var timelineCount, factsCount, ordinaryCount int
	for _, line := range bytes.Split(data, []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}
		if bytes.HasPrefix(line, []byte(matchsystem.TimelineLogMessage+" ")) {
			entry, ok := decodeRecordedLine(line)
			if !ok {
				t.Fatalf("回放记录不是完整单行 JSON: %s", line)
			}
			if entry.Timeline != nil {
				timelineCount++
			}
			if entry.TimelineFacts != nil {
				factsCount++
			}
		} else {
			ordinaryCount++
			var record struct {
				Message string `json:"msg"`
			}
			if err := json.Unmarshal(line, &record); err != nil {
				t.Fatalf("普通日志不是完整 JSON: %v", err)
			}
			if strings.Contains(record.Message, matchsystem.TimelineLogMessage) {
				t.Fatalf("普通日志带有回放标记: %s", line)
			}
		}
	}
	if timelineCount == 0 || factsCount < 2 || ordinaryCount == 0 {
		t.Fatalf("录制未包含预期日志: 时间线=%d Fact=%d 普通日志=%d", timelineCount, factsCount, ordinaryCount)
	}
	list, err := reader.ExtractTimeline(ctx, ReplayExtractRequest{Logs: &logs})
	if err != nil || len(list.Instances) != 1 || !list.Instances[0].Replayable || !list.Instances[0].Ended {
		t.Fatalf("磁盘录制无法完整提取: %+v %v", list, err)
	}
	extracted, err := reader.ExtractTimeline(ctx, ReplayExtractRequest{Logs: &logs, InstanceID: list.Instances[0].InstanceID})
	if err != nil {
		t.Fatal(err)
	}
	var timeline replayTimeline
	if err := json.Unmarshal([]byte(extracted.TimelineJSON), &timeline); err != nil {
		t.Fatal(err)
	}
	if len(timeline.Facts) < 2 || timeline.Facts[0].Facts.StringLists["preferredRoles"][0] != "one" || timeline.Facts[1].Facts.StringLists["preferredRoles"][0] != "two" {
		t.Fatalf("宿主 Fact 历史不完整: %+v", timeline.Facts)
	}
	var rounds []matchsystem.TimelineEvent
	for _, event := range timeline.Events {
		if event.Type == matchsystem.TimelineRoundBegin {
			rounds = append(rounds, event)
		}
	}
	if len(rounds) != 3 || rounds[0].Now != start || rounds[2].Now != start+200 || rounds[2].OffsetNS-rounds[0].OffsetNS != int64(200*time.Millisecond) {
		t.Fatalf("虚拟时钟录制不正确: %+v", rounds)
	}
	result, err := reader.ReplayTimeline(ctx, ReplayRequest{TimelineJSON: extracted.TimelineJSON, TargetRuleID: "2"})
	if err != nil || result.Status != "completed" {
		t.Fatalf("磁盘录制无法回放: %+v %v", result, err)
	}
}

func writeReplayTestEvent(t *testing.T, log *replayLog, event matchsystem.TimelineEvent) {
	t.Helper()
	payload, err := json.Marshal(struct {
		Timeline matchsystem.TimelineEvent `json:"timeline"`
	}{Timeline: event})
	if err != nil {
		t.Fatal(err)
	}
	log.logger.Info(matchsystem.TimelineLogMessage + " " + string(payload))
}

func TestReplayOutputKeepsActiveIndexesAfterRingEviction(t *testing.T) {
	var output bytes.Buffer
	log := newReplayLogWithOutput(&output, nil)
	log.limit = 512
	for _, instance := range []string{"a", "b"} {
		writeReplayTestEvent(t, log, matchsystem.TimelineEvent{InstanceID: instance, Type: matchsystem.TimelineStreamStart, Sequence: 1})
		writeReplayTestEvent(t, log, matchsystem.TimelineEvent{InstanceID: instance, Type: matchsystem.TimelineTicketAdd, Sequence: 2, Ticket: &common.Ticket{TicketID: 7}})
	}
	log.logger.Info("padding", slog.String("value", strings.Repeat("x", 1024)))
	if len(log.lines) != 0 {
		t.Fatal("测试数据未淘汰内存环中的旧记录")
	}
	log.recordFacts("a", 7, FactSnapshot{Int64Values: map[string]int64{"version": 1}})
	log.recordFacts("b", 7, FactSnapshot{Int64Values: map[string]int64{"version": 2}})
	writeReplayTestEvent(t, log, matchsystem.TimelineEvent{InstanceID: "a", Type: matchsystem.TimelineTicketWithdraw, Sequence: 3, TicketID: 7})
	writeReplayTestEvent(t, log, matchsystem.TimelineEvent{InstanceID: "a", Type: matchsystem.TimelineTicketAdd, Sequence: 4, Ticket: &common.Ticket{TicketID: 7}})
	writeReplayTestEvent(t, log, matchsystem.TimelineEvent{InstanceID: "b", Type: matchsystem.TimelineProduceMatch, Sequence: 3, MatchTicketIDs: []common.TicketID{7}})
	log.recordFacts("a", 7, FactSnapshot{Int64Values: map[string]int64{"version": 3}})
	log.recordFacts("b", 7, FactSnapshot{Int64Values: map[string]int64{"version": 4}})
	if len(log.positions["b"].adds) != 0 || log.positions["a"].adds[7] != 4 {
		t.Fatalf("票据索引未按实例及生命周期释放: %+v", log.positions)
	}
	writeReplayTestEvent(t, log, matchsystem.TimelineEvent{InstanceID: "a", Type: matchsystem.TimelineStreamEnd, Sequence: 5})
	if _, exists := log.positions["a"]; exists {
		t.Fatal("实例结束后未释放索引")
	}
	var records []replayFactsRecord
	for _, line := range bytes.Split(output.Bytes(), []byte{'\n'}) {
		if item, ok := decodeRecordedLine(line); ok && item.TimelineFacts != nil {
			records = append(records, *item.TimelineFacts)
		}
	}
	if len(records) != 3 || records[0].InstanceID != "a" || records[1].InstanceID != "b" || records[2].AddSequence != 4 {
		t.Fatalf("淘汰后磁盘中的 Fact 关联不完整: %+v", records)
	}
}

func TestReplayMemoryEvictionDoesNotKeepDiscardedEventIndex(t *testing.T) {
	log := newReplayLog()
	log.limit = 1
	writeReplayTestEvent(t, log, matchsystem.TimelineEvent{InstanceID: "a", Type: matchsystem.TimelineStreamStart, Sequence: 1})
	if len(log.lines) != 0 || len(log.positions) != 0 {
		t.Fatalf("超限日志已被淘汰，不能保留实例索引: %+v", log.positions)
	}
}

func TestReplayOutputFailureRestoresSurvivingEventAndFactIndexes(t *testing.T) {
	writer := &replayFailWriter{}
	var reports []error
	log := newReplayLogWithOutput(writer, func(err error) { reports = append(reports, err) })
	writeReplayTestEvent(t, log, matchsystem.TimelineEvent{InstanceID: "a", Type: matchsystem.TimelineStreamStart, Sequence: 1})
	writeReplayTestEvent(t, log, matchsystem.TimelineEvent{InstanceID: "a", Type: matchsystem.TimelineTicketAdd, Sequence: 2, Ticket: &common.Ticket{TicketID: 7}})
	log.recordFacts("a", 7, FactSnapshot{Int64Values: map[string]int64{"version": 1}})
	writer.fail = true
	writeReplayTestEvent(t, log, matchsystem.TimelineEvent{InstanceID: "a", Type: matchsystem.TimelineRoundBegin, Sequence: 3})
	position := log.positions["a"]
	if len(reports) != 1 || position == nil || position.sequence != 3 || position.adds[7] != 2 || position.factsOrder != 1 {
		t.Fatalf("磁盘故障后未恢复幸存的事件和 Fact 索引: reports=%v position=%+v", reports, position)
	}
	log.recordFacts("a", 7, FactSnapshot{Int64Values: map[string]int64{"version": 2}})
	var facts []replayFactsRecord
	for _, line := range log.lines {
		if entry, ok := decodeRecordedLine(line); ok && entry.TimelineFacts != nil {
			facts = append(facts, *entry.TimelineFacts)
		}
	}
	if len(facts) != 2 || facts[0].Order != 1 || facts[1].Order != 2 || facts[1].AfterSequence != 3 || facts[1].AddSequence != 2 {
		t.Fatalf("故障后 Fact 录制顺序错误: %+v", facts)
	}
}

func TestDecodeRecordedLineSupportsLegacyAndMarkedEnvelopes(t *testing.T) {
	for _, line := range []string{
		`{"msg":"[match_replay]","timeline":{"instanceId":"a","sequence":3}}`,
		`[match_replay] {"timeline":{"instanceId":"a","sequence":3}}`,
	} {
		entry, ok := decodeRecordedLine([]byte(line))
		if !ok || entry.Timeline == nil || entry.Timeline.InstanceID != "a" || entry.Timeline.Sequence != 3 {
			t.Fatalf("无法恢复索引: %s", line)
		}
	}
}

type replayFailWriter struct {
	writes int
	fail   bool
	short  bool
}

func (w *replayFailWriter) Write(p []byte) (int, error) {
	w.writes++
	if w.fail {
		if w.short {
			return len(p) - 1, nil
		}
		return 0, errors.New("disk failed")
	}
	return len(p), nil
}

func TestReplayOutputFailureFallsBackToBoundedMemory(t *testing.T) {
	for _, short := range []bool{false, true} {
		t.Run(map[bool]string{false: "写入错误", true: "短写"}[short], func(t *testing.T) {
			writer := &replayFailWriter{short: short}
			var reports []error
			scenario, key := libraryScenario()
			sim, err := NewSimulatorWithReplayLogOutput(scenario, writer, func(err error) { reports = append(reports, err) })
			if err != nil {
				t.Fatal(err)
			}
			defer sim.Close()
			ctx := context.Background()
			owner := identity.OwnerRef{PhysicalNodeID: scenario.PhysicalNodes[0].ID, LogicalNode: key}
			if _, err := sim.AddTicket(ctx, TicketInput{Rule: key.Rule, TicketID: 1}); err != nil {
				t.Fatal(err)
			}
			sim.replayLog.limit = 512
			sim.replayLog.logger.Info("padding", slog.String("value", strings.Repeat("x", 1024)))
			if len(sim.replayLog.positions) != 1 {
				t.Fatal("磁盘录制应保留活动票据索引")
			}
			writer.fail = true
			sim.replayLog.logger.Info("故障")
			writes := writer.writes
			if len(reports) != 1 || (short && !errors.Is(reports[0], io.ErrShortWrite)) || len(sim.replayLog.positions) != 0 {
				t.Fatalf("故障回退未恢复内存索引: reports=%v positions=%v", reports, sim.replayLog.positions)
			}
			sim.replayLog.limit = 4096
			if err := sim.SetObjectFacts(ctx, owner, 1, FactSnapshot{StringLists: map[string][]string{"preferredRoles": {"old-update"}}}); err != nil {
				t.Fatal(err)
			}
			if _, err := sim.AddTicket(ctx, TicketInput{Rule: key.Rule, TicketID: 2, ObjectFacts: FactSnapshot{StringLists: map[string][]string{"preferredRoles": {"new-value"}}}}); err != nil {
				t.Fatal(err)
			}
			if writer.writes != writes || len(reports) != 1 || strings.Contains(sim.replayLog.snapshot(), "old-update") || !strings.Contains(sim.replayLog.snapshot(), "new-value") {
				t.Fatalf("故障后未停止文件输出或内存录制错误: reports=%v snapshot=%s", reports, sim.replayLog.snapshot())
			}
		})
	}
}
