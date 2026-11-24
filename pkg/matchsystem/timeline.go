package matchsystem

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/xianyu11-x/matchSystem/pkg/common"
	"github.com/xianyu11-x/matchSystem/pkg/identity"
)

// TimelineVersion 标识结构化日志中时间线事件的格式。
const TimelineVersion = "match-timeline/v1"

// TimelineLogMessage 是回放消息开头的标记，后面跟一个空格和完整的单行 JSON。
const TimelineLogMessage = "[match_replay]"

// TimelineEventType 区分时间线动作及流生命周期。
type TimelineEventType string

const (
	TimelineStreamStart    TimelineEventType = "stream_start"
	TimelineStreamEnd      TimelineEventType = "stream_end"
	TimelineTicketAdd      TimelineEventType = "ticket_add"
	TimelineTicketWithdraw TimelineEventType = "ticket_withdraw"
	TimelineTicketRemove   TimelineEventType = "ticket_remove"
	TimelineRoundBegin     TimelineEventType = "round_begin"
	TimelineProduceMatch   TimelineEventType = "produce_match"
)

// TimelineResult 是一次匹配调用的结果类别。
type TimelineResult string

const (
	TimelineResultMatch   TimelineResult = "match"
	TimelineResultNoMatch TimelineResult = "no_match"
	TimelineResultError   TimelineResult = "error"
)

// TimelineConfig 开启后以 Info 级别将事件写入同一 Logger；Clock 须由宿主提供连续时钟。
// 每条消息由 TimelineLogMessage、空格和包含 timeline 的单行 JSON 组成，外围格式由宿主决定。
// 默认不录制，Clock 为空时使用 time.Now 的单调时钟计算偏移。
type TimelineConfig struct {
	Enabled bool
	Clock   func() time.Time
}

// TimelineEvent 是写在回放消息 JSON 的 timeline 字段中的独立快照。时间偏移以纳秒为单位，
// 相对该实例流头的 BaseTime；Now 和 Ticket.CreatedAt 保留宿主的业务时间单位。
// 有符号及无符号 64 位字段均为整数，消费 JSON 的宿主须避免经 float64 中转。
type TimelineEvent struct {
	Version             string                  `json:"version"`
	Type                TimelineEventType       `json:"type"`
	PhysicalNodeID      identity.PhysicalNodeID `json:"physicalNodeId,omitempty"`
	LogicalNode         identity.LogicalNodeKey `json:"logicalNode"`
	InstanceID          string                  `json:"instanceId"`
	Sequence            uint64                  `json:"sequence"`
	BaseTime            time.Time               `json:"baseTime"`
	OffsetNS            int64                   `json:"offsetNs"`
	Ticket              *common.Ticket          `json:"ticket,omitempty"`
	TicketID            common.TicketID         `json:"ticketId,omitempty"`
	AddSequence         uint64                  `json:"addSequence,omitempty"`
	Round               uint64                  `json:"round"`
	Now                 int64                   `json:"now"`
	SeedAttempts        int                     `json:"seedAttempts"`
	MatchTicketIDs      []common.TicketID       `json:"matchTicketIds,omitempty"`
	Result              TimelineResult          `json:"result,omitempty"`
	Error               string                  `json:"error,omitempty"`
	RuleJSON            json.RawMessage         `json:"ruleJson,omitempty"`
	ProviderDescriptors *ProviderDescriptorSet  `json:"providerDescriptors,omitempty"`
	InitialEmpty        bool                    `json:"initialEmpty"`
}

// timelineState 由 LogicalNode 的 owner 独占；Handler 接收的事件不持有池内数据。
type timelineState struct {
	logger         *slog.Logger
	clock          func() time.Time
	physicalNodeID identity.PhysicalNodeID
	instanceID     string
	base           time.Time
	sequence       uint64
	round          uint64
	addSequences   map[common.TicketID]uint64
	ended          bool
}

func newTimelineState(logger *slog.Logger, config TimelineConfig, physicalID identity.PhysicalNodeID) (*timelineState, error) {
	if !config.Enabled {
		return nil, nil
	}
	if logger == nil {
		return nil, fmt.Errorf("timeline recording requires a Logger")
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return nil, fmt.Errorf("generate timeline instance ID: %w", err)
	}
	clock := config.Clock
	if clock == nil {
		clock = time.Now
	}
	return &timelineState{
		logger: logger, clock: clock, physicalNodeID: physicalID,
		instanceID: hex.EncodeToString(id[:]), addSequences: make(map[common.TicketID]uint64),
	}, nil
}

func (p *LogicalNode) startTimeline() {
	if p.timeline == nil {
		return
	}
	p.timeline.base = p.timeline.clock()
	descriptors := p.providerDescriptors.Clone()
	p.timeline.emitAt(p, p.timeline.base, TimelineEvent{
		Type: TimelineStreamStart, RuleJSON: append(json.RawMessage(nil), p.ruleJSON...),
		ProviderDescriptors: &descriptors, InitialEmpty: p.Len() == 0 && !p.seedRound.initialized,
	})
}

func (s *timelineState) emitAt(node *LogicalNode, at time.Time, event TimelineEvent) uint64 {
	if s == nil || s.ended {
		return 0
	}
	event = s.reserve(node, at, event)
	s.write(event)
	return event.Sequence
}

func (s *timelineState) reserve(node *LogicalNode, at time.Time, event TimelineEvent) TimelineEvent {
	s.sequence++
	event.Version = TimelineVersion
	event.PhysicalNodeID = s.physicalNodeID
	event.LogicalNode = node.key
	event.InstanceID = s.instanceID
	event.Sequence = s.sequence
	event.BaseTime = s.base
	event.OffsetNS = at.Sub(s.base).Nanoseconds()
	return event
}

func (s *timelineState) write(event TimelineEvent) {
	if !s.logger.Enabled(context.Background(), slog.LevelInfo) {
		return
	}
	payload, err := json.Marshal(struct {
		Timeline TimelineEvent `json:"timeline"`
	}{Timeline: event})
	if err != nil {
		s.logger.Error("timeline event encoding failed", "error", err)
		return
	}
	s.logger.Info(TimelineLogMessage + " " + string(payload))
}

func (s *timelineState) emit(node *LogicalNode, event TimelineEvent) uint64 {
	if s == nil || s.ended {
		return 0
	}
	return s.emitAt(node, s.clock(), event)
}

func (p *LogicalNode) endTimeline() {
	if p.timeline == nil || p.timeline.ended {
		return
	}
	p.timeline.emit(p, TimelineEvent{Type: TimelineStreamEnd})
	p.timeline.ended = true
	p.timeline.addSequences = nil
}

func (p *LogicalNode) timelineActive() bool {
	return p != nil && p.timeline != nil && !p.timeline.ended
}
