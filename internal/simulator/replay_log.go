package simulator

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"math"
	"sync"
	"time"

	"github.com/xianyu11-x/matchSystem/pkg/common"
	"github.com/xianyu11-x/matchSystem/pkg/matchsystem"
)

// 回放消息按原样输出，普通日志保留结构化格式；内存按完整日志行淘汰旧数据。
const replayLogLimit = 32 << 20

type replayFactsRecord struct {
	InstanceID    string       `json:"instanceId"`
	AfterSequence uint64       `json:"afterSequence"`
	Order         uint64       `json:"order"`
	TicketID      uint64       `json:"ticketId"`
	AddSequence   uint64       `json:"addSequence"`
	Facts         FactSnapshot `json:"facts"`
}

type replayRecordedEnvelope struct {
	Timeline      *matchsystem.TimelineEvent `json:"timeline"`
	TimelineFacts *replayFactsRecord         `json:"timelineFacts"`
}

// decodeRecordedLine 同时识别当前标记消息和旧版顶层属性 JSONL。
func decodeRecordedLine(line []byte) (replayRecordedEnvelope, bool) {
	var entry replayRecordedEnvelope
	line = bytes.TrimSpace(line)
	if bytes.HasPrefix(line, []byte(matchsystem.TimelineLogMessage+" ")) {
		line = line[len(matchsystem.TimelineLogMessage)+1:]
	}
	if err := json.Unmarshal(line, &entry); err != nil {
		return entry, false
	}
	return entry, entry.Timeline != nil || entry.TimelineFacts != nil
}

type replayOwnerPosition struct {
	instance   string
	sequence   uint64
	adds       map[uint64]uint64
	factsOrder uint64
}

type replayLog struct {
	mu        sync.Mutex
	lines     [][]byte
	bytes     int
	limit     int
	positions map[string]*replayOwnerPosition
	logger    *slog.Logger
	output    io.Writer
	onError   func(error)
	clockMu   sync.Mutex
	clockBase time.Time
	clockWall time.Time
	virtual   *trafficRun
	virtualMS int64
}

type replayLogWriter struct{ log *replayLog }

// Write 只在 replayHandler 持锁时调用，保留完整日志行。
func (w replayLogWriter) Write(p []byte) (int, error) {
	owned := append([]byte(nil), p...)
	w.log.lines = append(w.log.lines, owned)
	w.log.bytes += len(owned)
	for w.log.bytes > w.log.limit && len(w.log.lines) > 0 {
		line := w.log.lines[0]
		w.log.bytes -= len(line)
		// 文件仍在录制时，已淘汰的入池记录仍可能需要后续 Fact 更新。
		if w.log.output == nil {
			if entry, ok := decodeRecordedLine(line); ok && entry.Timeline != nil {
				event := entry.Timeline
				position := w.log.positions[event.InstanceID]
				if string(event.Type) == "stream_start" {
					delete(w.log.positions, event.InstanceID)
				} else if position != nil && string(event.Type) == "ticket_add" && event.Ticket != nil && position.adds[event.Ticket.TicketID] == event.Sequence {
					delete(position.adds, event.Ticket.TicketID)
				}
			}
		}
		w.log.lines[0] = nil
		w.log.lines = w.log.lines[1:]
	}
	if w.log.output != nil {
		n, err := w.log.output.Write(p)
		if err == nil && n != len(p) {
			err = io.ErrShortWrite
		}
		if err != nil {
			// 磁盘发生错误后不再续写不完整的文件；内存环继续工作。
			w.log.output = nil
			w.log.rebuildPositions()
			if w.log.onError != nil {
				w.log.onError(err)
			}
		}
	}
	return len(p), nil
}

// rebuildPositions 在文件写入失败后根据仍保留的日志恢复内存环的索引。
// 调用方已持有 log.mu；文件输出期间保留的更早入池索引会在此释放。
func (log *replayLog) rebuildPositions() {
	log.positions = make(map[string]*replayOwnerPosition)
	for _, line := range log.lines {
		entry, ok := decodeRecordedLine(line)
		if !ok {
			continue
		}
		if event := entry.Timeline; event != nil {
			log.recordPosition(*event)
		}
		if facts := entry.TimelineFacts; facts != nil {
			if position := log.positions[facts.InstanceID]; position != nil && facts.Order > position.factsOrder {
				position.factsOrder = facts.Order
			}
		}
	}
}

type replayHandler struct {
	log   *replayLog
	inner slog.Handler
}

func (h replayHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h replayHandler) Handle(ctx context.Context, record slog.Record) error {
	h.log.mu.Lock()
	defer h.log.mu.Unlock()
	if bytes.HasPrefix([]byte(record.Message), []byte(matchsystem.TimelineLogMessage+" ")) {
		if entry, ok := decodeRecordedLine([]byte(record.Message)); ok {
			if entry.Timeline != nil {
				h.log.recordPosition(*entry.Timeline)
			}
			_, err := (replayLogWriter{h.log}).Write(append([]byte(record.Message), '\n'))
			return err
		}
	}
	return h.inner.Handle(ctx, record)
}

// recordPosition 只在已持有 log.mu 时调用。
func (log *replayLog) recordPosition(event matchsystem.TimelineEvent) {
	position := log.positions[event.InstanceID]
	if position == nil {
		position = &replayOwnerPosition{instance: event.InstanceID, adds: make(map[uint64]uint64)}
		log.positions[event.InstanceID] = position
	}
	position.sequence = event.Sequence
	if event.Type == matchsystem.TimelineTicketAdd && event.Ticket != nil {
		position.adds[event.Ticket.TicketID] = event.Sequence
	}
	if event.Type == matchsystem.TimelineTicketWithdraw || event.Type == matchsystem.TimelineTicketRemove {
		delete(position.adds, event.TicketID)
	}
	for _, ticketID := range event.MatchTicketIDs {
		delete(position.adds, ticketID)
	}
	if event.Type == matchsystem.TimelineStreamEnd {
		delete(log.positions, event.InstanceID)
	}
}

func (h replayHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return replayHandler{log: h.log, inner: h.inner.WithAttrs(attrs)}
}

func (h replayHandler) WithGroup(name string) slog.Handler {
	return replayHandler{log: h.log, inner: h.inner.WithGroup(name)}
}

func newReplayLog() *replayLog {
	return newReplayLogWithOutput(nil, nil)
}

func newReplayLogWithOutput(output io.Writer, onError func(error)) *replayLog {
	log := &replayLog{limit: replayLogLimit, positions: make(map[string]*replayOwnerPosition), output: output, onError: onError}
	log.clockBase = time.Now()
	log.clockWall = log.clockBase
	log.logger = slog.New(replayHandler{log: log, inner: slog.NewJSONHandler(replayLogWriter{log}, nil)})
	return log
}

func (log *replayLog) recordFacts(instance string, ticketID common.TicketID, values FactSnapshot) {
	if log == nil {
		return
	}
	log.mu.Lock()
	position := log.positions[instance]
	var record replayFactsRecord
	if position != nil && position.adds[ticketID] != 0 {
		position.factsOrder++
		record = replayFactsRecord{InstanceID: position.instance, AfterSequence: position.sequence, Order: position.factsOrder, TicketID: ticketID, AddSequence: position.adds[ticketID], Facts: values.clone()}
	}
	log.mu.Unlock()
	if record.InstanceID != "" && record.AddSequence != 0 {
		payload, err := json.Marshal(struct {
			TimelineFacts replayFactsRecord `json:"timelineFacts"`
		}{TimelineFacts: record})
		if err != nil {
			log.logger.Error("timeline facts encoding failed", "error", err)
			return
		}
		log.logger.Info(matchsystem.TimelineLogMessage + " " + string(payload))
	}
}

func (log *replayLog) snapshot() string {
	if log == nil {
		return ""
	}
	log.mu.Lock()
	defer log.mu.Unlock()
	data := make([]byte, 0, log.bytes)
	for _, line := range log.lines {
		data = append(data, line...)
	}
	return string(data)
}

// 虚拟流量使用连续的录制时钟；未运行虚拟流量时以单调墙钟推进。
func (log *replayLog) clockTime() time.Time {
	if log == nil {
		return time.Now()
	}
	log.clockMu.Lock()
	defer log.clockMu.Unlock()
	if log.virtual != nil {
		return log.clockBase
	}
	return log.clockBase.Add(time.Since(log.clockWall))
}

func (log *replayLog) startVirtual(run *trafficRun, now int64) {
	if log == nil {
		return
	}
	log.clockMu.Lock()
	defer log.clockMu.Unlock()
	if log.virtual == run {
		return
	}
	if log.virtual == nil {
		log.clockBase = log.clockBase.Add(time.Since(log.clockWall))
	}
	log.virtual, log.virtualMS = run, now
}

func (log *replayLog) advanceVirtual(run *trafficRun, now int64) {
	if log == nil {
		return
	}
	log.clockMu.Lock()
	defer log.clockMu.Unlock()
	if log.virtual != run || now <= log.virtualMS {
		return
	}
	// 超大虚拟步长不能装入纳秒 duration，写入饱和值供提取器明确拒绝。
	delta := uint64(now) - uint64(log.virtualMS)
	if delta > uint64(math.MaxInt64/int64(time.Millisecond)) {
		log.clockBase = log.clockBase.Add(time.Duration(math.MaxInt64))
	} else {
		log.clockBase = log.clockBase.Add(time.Duration(delta) * time.Millisecond)
	}
	log.virtualMS = now
}

func (log *replayLog) endVirtual(run *trafficRun) {
	if log == nil {
		return
	}
	log.clockMu.Lock()
	defer log.clockMu.Unlock()
	if log.virtual == run {
		log.virtual = nil
		log.clockWall = time.Now()
	}
}

var _ io.Writer = replayLogWriter{}
