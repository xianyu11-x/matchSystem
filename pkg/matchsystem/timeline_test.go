package matchsystem

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xianyu11-x/matchSystem/pkg/identity"
)

type timelineTestHandler struct {
	mu      sync.Mutex
	events  []TimelineEvent
	records []slog.Record
}

func (h *timelineTestHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *timelineTestHandler) Handle(_ context.Context, record slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !strings.HasPrefix(record.Message, TimelineLogMessage+" ") {
		return nil
	}
	var payload struct {
		Timeline TimelineEvent `json:"timeline"`
	}
	if err := json.Unmarshal([]byte(strings.TrimPrefix(record.Message, TimelineLogMessage+" ")), &payload); err != nil {
		return err
	}
	h.events = append(h.events, payload.Timeline)
	h.records = append(h.records, record.Clone())
	return nil
}
func (h *timelineTestHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *timelineTestHandler) WithGroup(string) slog.Handler      { return h }

func timelineTestSpec(t *testing.T, key identity.LogicalNodeKey) LogicalNodeSpec {
	t.Helper()
	return LogicalNodeSpec{Key: key, RuleJSON: integrationRuleJSON(t, key.Rule,
		`{"schemaVersion":"logical-node-contract/v3","attributes":[{"name":"names","type":"strings","maxValues":1},{"name":"ids","type":"uint64s","maxValues":1},{"name":"x","type":"uint64s","maxValues":1},{"name":"value","type":"int64"}],"facts":[],"indexes":[]}`, integrationNonePrefilterJSON,
		integrationCompleteEvaluationJSON, `{"type":"constant","params":{"value":1}}`,
		`{"type":"arrival","params":{}}`, logicalNodeConfig{SeedScheduler: seedSchedulerConfig{
			AttemptLimitPerProduceMatch: 1, AttemptLimitPerMatchRound: 10,
		}})}
}

func TestTimelineDetachedSnapshotsAndActions(t *testing.T) {
	ctx := context.Background()
	h := &timelineTestHandler{}
	now := time.Unix(1700000000, 0)
	key := identity.LogicalNodeKey{Rule: identity.RuleKey{RuleID: 51}, PlacementID: 71}
	spec := timelineTestSpec(t, key)
	spec.Logger = slog.New(h)
	spec.Timeline = TimelineConfig{Enabled: true, Clock: func() time.Time { return now }}
	node, err := NewLogicalNode(spec)
	if err != nil {
		t.Fatal(err)
	}
	if node.TimelineInstanceID() == "" {
		t.Fatal("missing instance ID")
	}
	now = now.Add(time.Millisecond)
	ticket := &Ticket{TicketID: math.MaxUint64, CreatedAt: math.MaxInt64,
		StringLists: map[string][]string{"names": {"original"}},
		Uint64Lists: map[string][]uint64{"ids": {math.MaxUint64}},
		Int64Values: map[string]int64{"value": math.MinInt64}}
	if err := node.Add(ticket); err != nil {
		t.Fatal(err)
	}
	ticket.StringLists["names"][0] = "mutated"
	ticket.Uint64Lists["ids"][0] = 1
	ticket.Int64Values["value"] = 1
	if err := node.Add(ticket); err == nil {
		t.Fatal("duplicate add succeeded")
	}
	now = now.Add(time.Millisecond)
	if err := node.BeginMatchRound(math.MaxInt64); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Millisecond)
	match, err := node.ProduceMatch(ctx)
	if err != nil || match == nil {
		t.Fatalf("produce match: %v, %#v", err, match)
	}
	match.Tickets[0].StringLists["names"][0] = "match mutated"
	if node.Withdraw(math.MaxUint64) {
		t.Fatal("matched ticket was withdrawn")
	}
	if err := node.Add(ticket); err != nil {
		t.Fatal(err)
	}
	if !node.Withdraw(math.MaxUint64) || node.Withdraw(math.MaxUint64) {
		t.Fatal("withdraw should succeed exactly once")
	}
	if err := node.Add(ticket); err != nil {
		t.Fatal(err)
	}
	if !node.Remove(math.MaxUint64) {
		t.Fatal("remove failed")
	}
	if err := node.BeginMatchRound(math.MaxInt64); err != nil {
		t.Fatal(err)
	}
	_, _, err = node.ProduceMatchWithMetrics(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = node.ProduceMatch(cancelled)
	if err == nil {
		t.Fatal("cancelled produce succeeded")
	}
	events := h.events
	wantTypes := []TimelineEventType{TimelineStreamStart, TimelineTicketAdd, TimelineRoundBegin,
		TimelineProduceMatch, TimelineTicketAdd, TimelineTicketWithdraw, TimelineTicketAdd,
		TimelineTicketRemove, TimelineRoundBegin, TimelineProduceMatch, TimelineProduceMatch}
	if len(events) != len(wantTypes) {
		t.Fatalf("events = %#v", events)
	}
	for i, event := range events {
		if event.Sequence != uint64(i+1) || event.InstanceID != node.TimelineInstanceID() || event.Type != wantTypes[i] {
			t.Fatalf("event %d = %#v", i, event)
		}
	}
	if !events[0].InitialEmpty || !bytes.Equal(events[0].RuleJSON, spec.RuleJSON) || events[1].OffsetNS != int64(time.Millisecond) {
		t.Fatalf("invalid stream header or time: %#v, %#v", events[0], events[1])
	}
	logged := events[1].Ticket
	if logged.TicketID != math.MaxUint64 || logged.CreatedAt != math.MaxInt64 || logged.StringLists["names"][0] != "original" || logged.Uint64Lists["ids"][0] != math.MaxUint64 || logged.Int64Values["value"] != math.MinInt64 {
		t.Fatalf("snapshot changed: %#v", logged)
	}
	if events[3].Result != TimelineResultMatch || events[3].SeedAttempts != 1 || events[3].Round != 1 || events[3].Now != math.MaxInt64 {
		t.Fatalf("unexpected match event: %#v", events[3])
	}
	if events[5].AddSequence != events[4].Sequence || events[7].AddSequence != events[6].Sequence {
		t.Fatalf("incorrect add association: %#v, %#v", events[5], events[7])
	}
	if events[8].Round != 2 || events[8].Now != events[2].Now || events[9].Result != TimelineResultNoMatch || events[9].SeedAttempts != 0 || events[10].Result != TimelineResultError {
		t.Fatalf("incorrect round/call events: %#v", events[8:])
	}
}

func TestTimelinePhysicalForwardingAndReconfigure(t *testing.T) {
	ctx := context.Background()
	h := &timelineTestHandler{}
	logger := slog.New(h)
	now := time.Unix(20, 0)
	physical, err := NewPhysicalNode(90, WithLogger(logger), WithTimeline(TimelineConfig{Enabled: true, Clock: func() time.Time { return now }}))
	if err != nil {
		t.Fatal(err)
	}
	k1 := identity.LogicalNodeKey{Rule: identity.RuleKey{RuleID: 1}, PlacementID: 2}
	k2 := identity.LogicalNodeKey{Rule: identity.RuleKey{RuleID: 2}, PlacementID: 3}
	for _, key := range []identity.LogicalNodeKey{k1, k2} {
		if err := physical.Load(ctx, timelineTestSpec(t, key)); err != nil {
			t.Fatal(err)
		}
	}
	firstID, err := physical.TimelineInstanceID(ctx, k1)
	if err != nil {
		t.Fatal(err)
	}
	secondID, err := physical.TimelineInstanceID(ctx, k2)
	if err != nil || firstID == secondID || firstID == "" || secondID == "" {
		t.Fatalf("instance IDs: %q %q %v", firstID, secondID, err)
	}
	owner := identity.OwnerRef{PhysicalNodeID: 90, LogicalNode: k1}
	if err := physical.Add(ctx, owner, &Ticket{TicketID: 10}); err != nil {
		t.Fatal(err)
	}
	if err := physical.BeginMatchRound(ctx, 123); err != nil {
		t.Fatal(err)
	}
	result, err := physical.ProduceMatch(ctx)
	if err != nil || result.Match == nil || result.LogicalNode != k1 {
		t.Fatalf("physical produce: %#v %v", result, err)
	}
	replacement, err := NewLogicalNode(timelineTestSpec(t, k1))
	if err != nil {
		t.Fatal(err)
	}
	if err := physical.Reconfigure([]identity.LogicalNodeKey{k1, k2}, []*LogicalNode{replacement}, nil); err != nil {
		t.Fatal(err)
	}
	newID, _ := physical.TimelineInstanceID(ctx, k1)
	if newID == firstID || newID == "" {
		t.Fatalf("replacement reused instance ID: %q", newID)
	}
	byID := make(map[string][]TimelineEvent)
	for _, event := range h.events {
		byID[event.InstanceID] = append(byID[event.InstanceID], event)
		if event.PhysicalNodeID != 90 {
			t.Fatalf("event has wrong physical owner: %#v", event)
		}
	}
	if got := byID[firstID]; len(got) != 5 || got[3].Type != TimelineProduceMatch || got[4].Type != TimelineStreamEnd {
		t.Fatalf("first instance: %#v", got)
	}
	if got := byID[newID]; len(got) != 2 || got[0].Type != TimelineStreamStart || got[1].Type != TimelineRoundBegin || got[1].Now != 123 {
		t.Fatalf("replacement did not inherit round: %#v", got)
	}
	for id, events := range byID {
		for i, event := range events {
			if event.Sequence != uint64(i+1) {
				t.Fatalf("%s: non-contiguous events %#v", id, events)
			}
		}
	}
}

func TestTimelineJSONPreservesIntegerTokens(t *testing.T) {
	var buffer bytes.Buffer
	now := time.Unix(1, 0)
	key := identity.LogicalNodeKey{Rule: identity.RuleKey{RuleID: math.MaxUint64}, PlacementID: 3}
	spec := timelineTestSpec(t, key)
	spec.Logger = slog.New(slog.NewJSONHandler(&buffer, nil))
	spec.Timeline = TimelineConfig{Enabled: true, Clock: func() time.Time { return now }}
	node, err := NewLogicalNode(spec)
	if err != nil {
		t.Fatal(err)
	}
	if err := node.Add(&Ticket{TicketID: math.MaxUint64, CreatedAt: math.MinInt64, Uint64Lists: map[string][]uint64{"x": {math.MaxUint64}}}); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(buffer.String()), "\n")
	var outer struct {
		Message string `json:"msg"`
	}
	if err := json.Unmarshal([]byte(lines[1]), &outer); err != nil {
		t.Fatal(err)
	}
	var record struct {
		Timeline TimelineEvent `json:"timeline"`
	}
	if err := json.Unmarshal([]byte(strings.TrimPrefix(outer.Message, TimelineLogMessage+" ")), &record); err != nil {
		t.Fatal(err)
	}
	if got := record.Timeline.Ticket; got.TicketID != math.MaxUint64 || got.CreatedAt != math.MinInt64 || got.Uint64Lists["x"][0] != math.MaxUint64 {
		t.Fatalf("JSON corrupted ticket: %#v", got)
	}
}

func TestTimelineReplayLogMessage(t *testing.T) {
	if TimelineLogMessage != "[match_replay]" {
		t.Fatalf("unexpected replay marker: %q", TimelineLogMessage)
	}
	var output bytes.Buffer
	key := identity.LogicalNodeKey{Rule: identity.RuleKey{RuleID: 10}, PlacementID: 11}
	spec := timelineTestSpec(t, key)
	spec.Logger = slog.New(slog.NewJSONHandler(&output, nil)).With("host", "service")
	spec.Timeline = TimelineConfig{Enabled: true}
	node, err := NewLogicalNode(spec)
	if err != nil {
		t.Fatal(err)
	}
	if err := node.Add(&Ticket{TicketID: 1}); err != nil {
		t.Fatal(err)
	}
	if err := node.Add(&Ticket{TicketID: 1}); err == nil {
		t.Fatal("duplicate ticket accepted")
	}
	var timelineCount, ordinaryCount int
	for _, line := range strings.Split(strings.TrimSpace(output.String()), "\n") {
		var record struct {
			Message  string         `json:"msg"`
			Timeline *TimelineEvent `json:"timeline"`
			Level    string         `json:"level"`
			Host     string         `json:"host"`
		}
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(record.Message, TimelineLogMessage+" ") {
			timelineCount++
			var payload struct {
				Timeline *TimelineEvent `json:"timeline"`
			}
			if record.Timeline != nil || record.Level != "INFO" || record.Host != "service" || strings.ContainsAny(record.Message, "\r\n") || json.Unmarshal([]byte(strings.TrimPrefix(record.Message, TimelineLogMessage+" ")), &payload) != nil || payload.Timeline == nil {
				t.Fatalf("invalid timeline message or duplicate attribute: %s", line)
			}
		} else {
			ordinaryCount++
			if strings.Contains(record.Message, TimelineLogMessage) {
				t.Fatalf("ordinary log has replay marker: %q", record.Message)
			}
		}
	}
	if timelineCount != 2 || ordinaryCount != 1 {
		t.Fatalf("unexpected log counts: timeline=%d ordinary=%d", timelineCount, ordinaryCount)
	}
}

func TestTimelineRecordCarriesStandaloneMessageOnly(t *testing.T) {
	h := &timelineTestHandler{}
	key := identity.LogicalNodeKey{Rule: identity.RuleKey{RuleID: 10}, PlacementID: 11}
	spec := timelineTestSpec(t, key)
	spec.Logger = slog.New(h)
	spec.Timeline = TimelineConfig{Enabled: true}
	node, err := NewLogicalNode(spec)
	if err != nil {
		t.Fatal(err)
	}
	const input = "含换行\n和引号\"的值"
	if err := node.Add(&Ticket{TicketID: 1, StringLists: map[string][]string{"names": {input}}}); err != nil {
		t.Fatal(err)
	}
	if len(h.records) != 2 || h.records[0].Level != slog.LevelInfo || h.records[0].NumAttrs() != 0 || h.events[0].Type != TimelineStreamStart || h.records[1].NumAttrs() != 0 || strings.ContainsAny(h.records[1].Message, "\r\n") || h.events[1].Ticket.StringLists["names"][0] != input {
		t.Fatalf("invalid standalone record: %+v", h.records)
	}
}

func TestTimelineEncodingFailureIsReportedWithoutMalformedReplay(t *testing.T) {
	var output bytes.Buffer
	key := identity.LogicalNodeKey{Rule: identity.RuleKey{RuleID: 10}, PlacementID: 11}
	spec := timelineTestSpec(t, key)
	spec.Logger = slog.New(slog.NewJSONHandler(&output, nil))
	spec.Timeline = TimelineConfig{Enabled: true, Clock: func() time.Time { return time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC) }}
	if _, err := NewLogicalNode(spec); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "timeline event encoding failed") || strings.Contains(output.String(), TimelineLogMessage) {
		t.Fatalf("encoding failure was not safely reported: %s", output.String())
	}
}

func TestLoggerReportsFailuresWithoutRecording(t *testing.T) {
	var output bytes.Buffer
	key := identity.LogicalNodeKey{Rule: identity.RuleKey{RuleID: 10}, PlacementID: 11}
	spec := timelineTestSpec(t, key)
	spec.Logger = slog.New(slog.NewJSONHandler(&output, nil))
	node, err := NewLogicalNode(spec)
	if err != nil {
		t.Fatal(err)
	}
	if node.TimelineInstanceID() != "" {
		t.Fatal("timeline should be disabled")
	}
	if err := node.Add(&Ticket{TicketID: 1}); err != nil {
		t.Fatal(err)
	}
	if err := node.Add(&Ticket{TicketID: 1}); err == nil {
		t.Fatal("duplicate ticket accepted")
	}
	if _, err := node.ProduceMatch(context.Background()); err == nil {
		t.Fatal("missing round accepted")
	}
	if !strings.Contains(output.String(), "ticket add rejected") || !strings.Contains(output.String(), "match production failed") || strings.Contains(output.String(), `"timeline"`) {
		t.Fatalf("unexpected logging: %s", output.String())
	}
}

func TestTimelineClearAndUnrecordedPrehistory(t *testing.T) {
	ctx := context.Background()
	h := &timelineTestHandler{}
	key := identity.LogicalNodeKey{Rule: identity.RuleKey{RuleID: 4}, PlacementID: 5}
	prepared, err := NewLogicalNode(timelineTestSpec(t, key))
	if err != nil {
		t.Fatal(err)
	}
	if err := prepared.Add(&Ticket{TicketID: 7}); err != nil {
		t.Fatal(err)
	}
	physical, err := NewPhysicalNode(6, WithLogger(slog.New(h)), WithTimeline(TimelineConfig{Enabled: true}))
	if err != nil {
		t.Fatal(err)
	}
	if err := physical.Reconfigure([]identity.LogicalNodeKey{key}, []*LogicalNode{prepared}, nil); err != nil {
		t.Fatal(err)
	}
	if count, err := physical.ClearTickets(ctx); err != nil || count != 1 {
		t.Fatalf("clear: %d %v", count, err)
	}
	if len(h.events) != 2 || h.events[0].InitialEmpty || h.events[1].Type != TimelineTicketRemove || h.events[1].AddSequence != 0 {
		t.Fatalf("unrecorded prehistory not identified: %#v", h.events)
	}
}

func TestTimelineProviderDescriptorIsDetached(t *testing.T) {
	h := &timelineTestHandler{}
	key := identity.LogicalNodeKey{Rule: identity.RuleKey{RuleID: 9}, PlacementID: 8}
	spec := timelineTestSpec(t, key)
	descriptor := ProviderDescriptor{ID: "provider", Version: "v1", Facts: []FactSpec{{Name: "extra", Type: FactTypeInt64, Scope: FactScopeTick}}}
	spec.FactProvider = describedTickProvider(func(context.Context, TickFactInput) (Facts, error) { return Facts{}, nil }, descriptor)
	spec.Logger = slog.New(h)
	spec.Timeline = TimelineConfig{Enabled: true}
	node, err := NewLogicalNode(spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(h.events) != 1 || h.events[0].ProviderDescriptors == nil || h.events[0].ProviderDescriptors.Tick.Facts[0].Name != "extra" {
		t.Fatalf("missing descriptor: %#v", h.events)
	}
	descriptor.Facts[0].Name = "source mutation"
	h.events[0].ProviderDescriptors.Tick.Facts[0].Name = "handler mutation"
	if got := node.ProviderDescriptors().Tick.Facts[0].Name; got != "extra" {
		t.Fatalf("descriptor snapshot changed: %q", got)
	}
}

func TestPhysicalNodeEndTimelinesIsIdempotentAndPreservesMatching(t *testing.T) {
	ctx := context.Background()
	h := &timelineTestHandler{}
	physical, err := NewPhysicalNode(100, WithLogger(slog.New(h)), WithTimeline(TimelineConfig{Enabled: true}))
	if err != nil {
		t.Fatal(err)
	}
	keys := []identity.LogicalNodeKey{
		{Rule: identity.RuleKey{RuleID: 1}, PlacementID: 2},
		{Rule: identity.RuleKey{RuleID: 3}, PlacementID: 4},
	}
	for _, key := range keys {
		if err := physical.Load(ctx, timelineTestSpec(t, key)); err != nil {
			t.Fatal(err)
		}
	}
	owner := identity.OwnerRef{PhysicalNodeID: 100, LogicalNode: keys[0]}
	if err := physical.Add(ctx, owner, &Ticket{TicketID: 7}); err != nil {
		t.Fatal(err)
	}
	physical.EndTimelines()
	physical.EndTimelines()
	endCount := make(map[string]int)
	for _, event := range h.events {
		if event.Type == TimelineStreamEnd {
			endCount[event.InstanceID]++
		}
	}
	if len(endCount) != 2 {
		t.Fatalf("missing stream endings: %#v", h.events)
	}
	for id, count := range endCount {
		if count != 1 {
			t.Fatalf("instance %s ended %d times", id, count)
		}
	}
	count := len(h.events)
	if err := physical.BeginMatchRound(ctx, 10); err != nil {
		t.Fatal(err)
	}
	result, err := physical.ProduceMatch(ctx)
	if err != nil || result.Match == nil || result.Match.Tickets[0].TicketID != 7 {
		t.Fatalf("matching changed after end: %#v %v", result, err)
	}
	if err := physical.Add(ctx, owner, &Ticket{TicketID: 8}); err != nil {
		t.Fatal(err)
	}
	if removed, err := physical.Withdraw(ctx, owner, 8); err != nil || !removed {
		t.Fatalf("withdraw after end: %v %v", removed, err)
	}
	if len(h.events) != count {
		t.Fatalf("events emitted after stream ending: %#v", h.events[count:])
	}
}
