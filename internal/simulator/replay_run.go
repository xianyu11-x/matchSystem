package simulator

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"strconv"
	"time"

	"github.com/xianyu11-x/matchSystem/pkg/common"
	"github.com/xianyu11-x/matchSystem/pkg/identity"
	"github.com/xianyu11-x/matchSystem/pkg/matchsystem"
	"github.com/xianyu11-x/matchSystem/pkg/matchsystem/contract"
	"github.com/xianyu11-x/matchSystem/pkg/matchsystem/fact"
)

type ReplayRequest struct {
	TimelineJSON        string                   `json:"timelineJson"`
	TargetRuleID        string                   `json:"targetRuleId"`
	ProviderLogicalNode *identity.LogicalNodeKey `json:"providerLogicalNode,omitempty"`
}

type ReplayEventResult struct {
	Sequence        string   `json:"sequence"`
	Type            string   `json:"type"`
	OffsetNS        string   `json:"offsetNs"`
	Round           string   `json:"round"`
	Now             string   `json:"now"`
	Status          string   `json:"status"`
	TicketID        string   `json:"ticketId,omitempty"`
	TicketIDs       []string `json:"ticketIds,omitempty"`
	SourceTicketIDs []string `json:"sourceTicketIds,omitempty"`
	SeedAttempts    uint64   `json:"seedAttempts"`
	Error           string   `json:"error,omitempty"`
}

type ReplayResult struct {
	InstanceID    string              `json:"instanceId"`
	TargetRuleID  string              `json:"targetRuleId"`
	Status        string              `json:"status"`
	InterruptedAt string              `json:"interruptedAt,omitempty"`
	Error         string              `json:"error,omitempty"`
	Provider      string              `json:"provider"`
	Events        []ReplayEventResult `json:"events"`
	Rounds        int                 `json:"rounds"`
	Calls         int                 `json:"calls"`
	Matches       int                 `json:"matches"`
	SeedAttempts  uint64              `json:"seedAttempts"`
}

// validateReplayTimeline 同时校验实例序号、源队列生命周期和原始轮次。
func validateReplayTimeline(timeline replayTimeline) error {
	if timeline.SchemaVersion != matchsystem.TimelineVersion || timeline.InstanceID == "" || len(timeline.Events) == 0 {
		return replayInvalid("$.timelineJson", "INVALID_TIMELINE", "时间线版本、实例或事件为空")
	}
	first := timeline.Events[0]
	if first.Type != matchsystem.TimelineStreamStart || first.Sequence != 1 || first.OffsetNS != 0 || !first.InitialEmpty || len(first.RuleJSON) == 0 || first.LogicalNode.Validate() != nil {
		return replayInvalid("$.timelineJson.events", "MISSING_HISTORY", "缺少空队列流头或源规则，无法独立回放")
	}
	compiled, err := matchsystem.CompileRuleJSON(first.RuleJSON)
	if err != nil || compiled.RuleKey().RuleID != first.LogicalNode.Rule.RuleID {
		return replayInvalid("$.timelineJson.events[0].ruleJson", "INVALID_SOURCE_RULE", "源规则无效或其 RuleID 与节点身份不一致")
	}
	active := make(map[uint64]uint64)
	adds := make(map[uint64]uint64)
	var round uint64
	var now int64
	var offset int64
	physical := first.PhysicalNodeID
	ended := false
	for index, event := range timeline.Events {
		path := fmt.Sprintf("$.timelineJson.events[%d]", index)
		if event.Version != matchsystem.TimelineVersion || event.InstanceID != timeline.InstanceID || event.Sequence != uint64(index+1) || event.LogicalNode != first.LogicalNode || !event.BaseTime.Equal(first.BaseTime) || event.OffsetNS < 0 || event.OffsetNS == math.MaxInt64 || (index > 0 && event.OffsetNS < offset) || ended {
			return replayInvalid(path, "INCOMPLETE_TIMELINE", "节点身份、时间、事件序号不一致或日志存在缺段")
		}
		offset = event.OffsetNS
		if physical == 0 {
			physical = event.PhysicalNodeID
		} else if event.PhysicalNodeID != physical {
			return replayInvalid(path, "INCOMPLETE_TIMELINE", "物理节点身份不一致")
		}
		switch event.Type {
		case matchsystem.TimelineStreamStart:
			if index != 0 {
				return replayInvalid(path, "DUPLICATE_STREAM_START", "时间线包含多个流头")
			}
		case matchsystem.TimelineStreamEnd:
			ended = true
		case matchsystem.TimelineTicketAdd:
			if event.Ticket == nil || event.Ticket.TicketID == 0 || active[event.Ticket.TicketID] != 0 {
				return replayInvalid(path, "INVALID_SOURCE_ADD", "源票据为空或重复入池")
			}
			active[event.Ticket.TicketID] = event.Sequence
			adds[event.Sequence] = event.Ticket.TicketID
		case matchsystem.TimelineTicketRemove, matchsystem.TimelineTicketWithdraw:
			if event.AddSequence == 0 || active[event.TicketID] != event.AddSequence {
				return replayInvalid(path, "MISSING_SOURCE_ADD", "撤票或删除缺少匹配的历史入池事件")
			}
			delete(active, event.TicketID)
		case matchsystem.TimelineRoundBegin:
			if event.Round != round+1 {
				return replayInvalid(path, "INVALID_ROUND", "轮次缺失或不连续")
			}
			round, now = event.Round, event.Now
		case matchsystem.TimelineProduceMatch:
			unstartedError := round == 0 && event.Round == 0 && event.Now == 0 && event.Result == matchsystem.TimelineResultError
			if (!unstartedError && (round == 0 || event.Round != round || event.Now != now)) || event.SeedAttempts < 0 {
				return replayInvalid(path, "MISSING_ROUND", "匹配调用缺少相应轮次或逻辑时间不一致")
			}
			switch event.Result {
			case matchsystem.TimelineResultMatch:
				if len(event.MatchTicketIDs) == 0 {
					return replayInvalid(path, "MISSING_MATCH_MEMBERS", "源成局缺少成员，无法确认票据生命周期")
				}
				for _, id := range event.MatchTicketIDs {
					if active[id] == 0 {
						return replayInvalid(path, "MISSING_SOURCE_ADD", "源成局成员缺少历史入池事件")
					}
					delete(active, id)
				}
			case matchsystem.TimelineResultNoMatch:
			case matchsystem.TimelineResultError:
			default:
				return replayInvalid(path, "INVALID_MATCH_RESULT", "源匹配结果无效")
			}
		default:
			return replayInvalid(path, "UNKNOWN_EVENT", "未知时间线事件")
		}
	}
	var order, afterSequence uint64
	for index, values := range timeline.Facts {
		if values.InstanceID != timeline.InstanceID || values.Order != order+1 || values.AfterSequence < afterSequence || values.AfterSequence < values.AddSequence || values.AfterSequence > uint64(len(timeline.Events)) || adds[values.AddSequence] != values.TicketID {
			return replayInvalid(fmt.Sprintf("$.timelineJson.facts[%d]", index), "INVALID_FACT_EVENT", "Object Fact 更新顺序或入池关联无效")
		}
		order = values.Order
		afterSequence = values.AfterSequence
	}
	return nil
}

// ReplayTimeline 使用规则库快照和独立 LogicalNode 顺序回放，不改变在线队列。
func (s *Simulator) ReplayTimeline(ctx context.Context, request ReplayRequest) (ReplayResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return ReplayResult{}, err
	}
	if s == nil {
		return ReplayResult{}, ErrSimulatorClosed
	}
	if len(request.TimelineJSON) > replayLogLimit {
		return ReplayResult{}, replayInvalid("$.timelineJson", "TIMELINE_LIMIT", "时间线超过 32 MiB 限制")
	}
	var timeline replayTimeline
	if err := decodeReplayJSON([]byte(request.TimelineJSON), &timeline); err != nil {
		return ReplayResult{}, replayInvalid("$.timelineJson", "INVALID_TIMELINE", err.Error())
	}
	if err := validateReplayTimeline(timeline); err != nil {
		return ReplayResult{}, err
	}
	ruleID, err := strconv.ParseUint(request.TargetRuleID, 10, 64)
	if err != nil || ruleID == 0 || strconv.FormatUint(ruleID, 10) != request.TargetRuleID {
		return ReplayResult{}, replayInvalid("$.targetRuleId", "INVALID_RULE_ID", "目标规则 ID 必须是规范十进制字符串")
	}
	s.mu.RLock()
	runtime, err := s.runtimeReadLocked()
	if err != nil {
		s.mu.RUnlock()
		return ReplayResult{}, err
	}
	raw, exists := s.ruleLibraryIndex[identity.RuleID(ruleID)]
	raw = append(json.RawMessage(nil), raw...)
	var selected RuleSpec
	if request.ProviderLogicalNode != nil {
		selected, exists = runtime.rules[logicalIndex(*request.ProviderLogicalNode)]
		if !exists {
			s.mu.RUnlock()
			return ReplayResult{}, replayInvalid("$.providerLogicalNode", "PROVIDER_NOT_FOUND", "指定 Provider 部署不存在")
		}
		selected = (Scenario{Rules: []RuleSpec{selected}}).Clone().Rules[0]
	}
	s.mu.RUnlock()
	if len(raw) == 0 {
		return ReplayResult{}, replayInvalid("$.targetRuleId", "RULE_NOT_IN_LIBRARY", "目标规则不在规则库中")
	}
	if providerValuePresent(selected.FactProvider) || providerValuePresent(selected.ObjectFactProvider) || providerValuePresent(selected.MatchFactProvider) {
		return ReplayResult{}, replayInvalid("$.providerLogicalNode", "PROVIDER_NOT_ISOLATABLE", "进程内 Go Provider 无法安全复制，请使用可独立启动的 Provider")
	}
	source, err := matchsystem.CompileRuleJSON(timeline.Events[0].RuleJSON)
	if err != nil {
		return ReplayResult{}, replayInvalid("$.timelineJson.events[0].ruleJson", "INVALID_SOURCE_RULE", err.Error())
	}
	if source.RuleKey().RuleID != timeline.Events[0].LogicalNode.Rule.RuleID {
		return ReplayResult{}, replayInvalid("$.timelineJson.events[0].ruleJson", "SOURCE_RULE_MISMATCH", "源规则 ID 与逻辑节点身份不一致")
	}
	target, err := matchsystem.CompileRuleJSON(raw)
	if err != nil {
		return ReplayResult{}, replayInvalid("$.targetRuleId", "INVALID_TARGET_RULE", err.Error())
	}
	sourceContract, targetContract := source.Contract(), target.Contract()
	if err := validateReplayContracts(timeline, sourceContract, targetContract, selected.PythonProvider != nil); err != nil {
		return ReplayResult{}, err
	}

	key := identity.LogicalNodeKey{Rule: target.RuleKey(), PlacementID: timeline.Events[0].LogicalNode.PlacementID}
	owner := identity.OwnerRef{PhysicalNodeID: 1, LogicalNode: key}
	registry := NewObservationRegistry()
	selected.LogicalNode, selected.PhysicalNodeID, selected.RuleJSON = key, owner.PhysicalNodeID, raw
	validator, err := fact.NewValidator(targetContract.FactSpecs())
	if err != nil {
		return ReplayResult{}, err
	}
	if err := validateFactSnapshot("tickFacts", validator, selected.TickFacts, fact.ScopeTick); err != nil {
		return ReplayResult{}, replayInvalid("$.providerLogicalNode", "INVALID_PROVIDER_FACT_INPUT", err.Error())
	}
	providerName := "builtin"
	var worker *PythonWorker
	if selected.PythonProvider != nil {
		worker, err = startPythonProvider(*selected.PythonProvider, targetContract.FactSpecs(), newPythonProviderStartupInput(targetContract.FactSpecs(), selected.TickFacts.values()))
		if err != nil {
			return ReplayResult{}, replayInvalid("$.providerLogicalNode", "PROVIDER_START_FAILED", err.Error())
		}
		defer worker.Close()
		selected.FactProvider, selected.ObjectFactProvider, selected.MatchFactProvider = pythonProviderCallbacks(worker, registry, owner)
		providerName = "python"
	}
	spec := runtimeLogicalNodeSpec(selected, targetContract, registry, owner, validator)
	virtualTime := timeline.Events[0].BaseTime
	recording := &replayTargetHandler{}
	spec.Logger = slog.New(recording)
	spec.Timeline = matchsystem.TimelineConfig{Enabled: true, Clock: func() time.Time { return virtualTime }}
	node, err := matchsystem.NewLogicalNode(spec)
	if err != nil {
		return ReplayResult{}, replayInvalid("$.providerLogicalNode", "PROVIDER_INCOMPATIBLE", err.Error())
	}
	if err := validateReplayProviderBindings(timeline.Events[0].ProviderDescriptors, node.ProviderDescriptors(), targetContract); err != nil {
		return ReplayResult{}, err
	}
	result := ReplayResult{InstanceID: timeline.InstanceID, TargetRuleID: request.TargetRuleID, Status: "completed", Provider: providerName, Events: make([]ReplayEventResult, 0, len(timeline.Events))}
	active := make(map[uint64]uint64)
	factIndex := 0
	for _, event := range timeline.Events {
		virtualTime = timeline.Events[0].BaseTime.Add(time.Duration(event.OffsetNS))
		previousRecordCount := len(recording.events)
		entry := ReplayEventResult{Sequence: strconv.FormatUint(event.Sequence, 10), Type: string(event.Type), OffsetNS: strconv.FormatInt(event.OffsetNS, 10), Round: strconv.FormatUint(event.Round, 10), Now: strconv.FormatInt(event.Now, 10), Status: "applied"}
		if event.Ticket != nil {
			entry.TicketID = strconv.FormatUint(event.Ticket.TicketID, 10)
		} else if event.TicketID != 0 {
			entry.TicketID = strconv.FormatUint(event.TicketID, 10)
		}
		for _, id := range event.MatchTicketIDs {
			entry.SourceTicketIDs = append(entry.SourceTicketIDs, strconv.FormatUint(id, 10))
		}
		if err := ctx.Err(); err != nil {
			entry.Error = err.Error()
		} else {
			switch event.Type {
			case matchsystem.TimelineTicketAdd:
				if active[event.Ticket.TicketID] != 0 {
					entry.Error = fmt.Sprintf("目标队列仍有相同 TicketID %d，无法再次入池", event.Ticket.TicketID)
				} else if err := node.Add(event.Ticket); err != nil {
					entry.Error = err.Error()
				} else {
					active[event.Ticket.TicketID] = event.Sequence
					_, err := registry.RecordTicket(owner, common.RouteDecision{Owner: owner}, event.Ticket, FactSnapshot{})
					if err != nil {
						entry.Error = err.Error()
					}
				}
			case matchsystem.TimelineTicketWithdraw, matchsystem.TimelineTicketRemove:
				if active[event.TicketID] != event.AddSequence {
					entry.Status = "already_left"
				} else {
					node.Remove(event.TicketID)
					registry.RemoveTicket(owner, event.TicketID)
					delete(active, event.TicketID)
				}
			case matchsystem.TimelineRoundBegin:
				if err := node.BeginMatchRound(event.Now); err != nil {
					entry.Error = err.Error()
				} else {
					result.Rounds++
				}
			case matchsystem.TimelineProduceMatch:
				result.Calls++
				match, metrics, err := node.ProduceMatchWithMetrics(ctx)
				entry.SeedAttempts = metrics.SeedAttempts
				result.SeedAttempts += metrics.SeedAttempts
				if err != nil {
					entry.Error = err.Error()
				} else if match == nil {
					entry.Status = "no_match"
				} else {
					entry.Status = "matched"
					result.Matches++
					for _, ticket := range match.Tickets {
						entry.TicketIDs = append(entry.TicketIDs, strconv.FormatUint(ticket.TicketID, 10))
						delete(active, ticket.TicketID)
						registry.RemoveTicket(owner, ticket.TicketID)
					}
				}
			}
		}
		for factIndex < len(timeline.Facts) && timeline.Facts[factIndex].AfterSequence == event.Sequence {
			values := timeline.Facts[factIndex]
			if active[values.TicketID] == values.AddSequence {
				if err := registry.SetObjectFacts(owner, values.TicketID, values.Facts); err != nil && entry.Error == "" {
					entry.Error = err.Error()
				}
			}
			factIndex++
		}
		if len(recording.events) > previousRecordCount {
			actual := recording.events[len(recording.events)-1]
			entry.OffsetNS = strconv.FormatInt(actual.OffsetNS, 10)
			if actual.OffsetNS != event.OffsetNS && entry.Error == "" {
				entry.Error = "目标节点记录的虚拟事件时间与源时间线不一致"
			}
		}
		if entry.Error != "" {
			entry.Status, result.Status, result.InterruptedAt, result.Error = "error", "interrupted", entry.Sequence, entry.Error
		}
		result.Events = append(result.Events, entry)
		if result.Status == "interrupted" {
			break
		}
	}
	return result, nil
}

type replayTargetHandler struct{ events []matchsystem.TimelineEvent }

func (*replayTargetHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *replayTargetHandler) Handle(_ context.Context, record slog.Record) error {
	if !bytes.HasPrefix([]byte(record.Message), []byte(matchsystem.TimelineLogMessage+" ")) {
		return nil
	}
	if entry, ok := decodeRecordedLine([]byte(record.Message)); ok && entry.Timeline != nil {
		h.events = append(h.events, *entry.Timeline)
	}
	return nil
}
func (h *replayTargetHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *replayTargetHandler) WithGroup(string) slog.Handler      { return h }

func validateReplayContracts(timeline replayTimeline, source, target contract.Contract, python bool) error {
	sourceValidator, err := source.CompileAttributeValidator()
	if err != nil {
		return err
	}
	targetValidator, err := target.CompileAttributeValidator()
	if err != nil {
		return err
	}
	factValidator, err := fact.NewValidator(source.FactSpecs())
	if err != nil {
		return err
	}
	sourceFacts := make(map[string]fact.Spec, len(source.Facts))
	sourceAttributes := make(map[string]contract.AttributeSpec, len(source.Attributes))
	for _, item := range source.Attributes {
		sourceAttributes[item.Name] = item
	}
	for _, item := range target.Attributes {
		previous, ok := sourceAttributes[item.Name]
		if !ok || previous.Type != item.Type || previous.MaxValues > item.MaxValues {
			return replayInvalid("$.targetRuleId", "ATTRIBUTE_CONTRACT_MISMATCH", fmt.Sprintf("属性 %q 无法由源契约以兼容类型和数量提供", item.Name))
		}
	}
	for _, item := range source.Facts {
		sourceFacts[item.Name] = item
	}
	for _, item := range target.Facts {
		previous, ok := sourceFacts[item.Name]
		if !ok || previous.Type != item.Type || previous.Scope != item.Scope || previous.MaxValues != item.MaxValues {
			return replayInvalid("$.targetRuleId", "FACT_CONTRACT_MISMATCH", fmt.Sprintf("Fact %q 与源契约的类型、scope 或 maxValues 不兼容", item.Name))
		}
		if !python {
			supported := (item.Scope == fact.ScopeTick && item.Name == WaitingCountFactName) ||
				(item.Scope == fact.ScopeObject && (item.Name == WaitingTimeFactName || item.Name == "latencyMs" || item.Name == "preferredRoles")) ||
				(item.Scope == fact.ScopeMatch && item.Name == MemberCountFactName)
			if !supported {
				return replayInvalid("$.providerLogicalNode", "UNAVAILABLE_FACT", fmt.Sprintf("内置 Provider 无法可靠重建 Fact %q", item.Name))
			}
		}
	}
	initialFacts := make(map[uint64]FactSnapshot)
	for index, item := range timeline.Facts {
		if err := validateFactSnapshot("facts", factValidator, item.Facts, fact.ScopeObject); err != nil {
			return replayInvalid(fmt.Sprintf("$.timelineJson.facts[%d]", index), "INVALID_SOURCE_FACT", err.Error())
		}
		if !python {
			for _, spec := range target.Facts {
				if spec.Scope != fact.ScopeObject {
					continue
				}
				if spec.Name == "latencyMs" {
					if _, present := item.Facts.Int64Values[spec.Name]; !present {
						return replayInvalid(fmt.Sprintf("$.timelineJson.facts[%d]", index), "MISSING_OBJECT_FACT_INPUT", "Object Fact 更新缺少 latencyMs")
					}
				}
				if spec.Name == "preferredRoles" {
					if _, present := item.Facts.StringLists[spec.Name]; !present {
						return replayInvalid(fmt.Sprintf("$.timelineJson.facts[%d]", index), "MISSING_OBJECT_FACT_INPUT", "Object Fact 更新缺少 preferredRoles")
					}
				}
			}
		}
		if item.AfterSequence == item.AddSequence {
			if _, exists := initialFacts[item.AddSequence]; !exists {
				initialFacts[item.AddSequence] = item.Facts
			}
		}
	}
	for index, event := range timeline.Events {
		if event.Type != matchsystem.TimelineTicketAdd {
			continue
		}
		path := fmt.Sprintf("$.timelineJson.events[%d].ticket", index)
		if err := sourceValidator.ValidateTicket("ticket", event.Ticket); err != nil {
			return replayInvalid(path, "INVALID_SOURCE_ATTRIBUTE", err.Error())
		}
		if err := targetValidator.ValidateTicket("ticket", event.Ticket); err != nil {
			return replayInvalid(path, "TARGET_ATTRIBUTE_MISMATCH", err.Error())
		}
		values, hasValues := initialFacts[event.Sequence]
		for _, spec := range target.Facts {
			if spec.Scope != fact.ScopeObject {
				continue
			}
			if python && !hasValues {
				return replayInvalid(path, "MISSING_OBJECT_FACT_INPUT", "日志缺少 Python Provider 所需的宿主 Object Fact 入池快照")
			}
			if !python && spec.Name == "latencyMs" {
				if _, ok := values.Int64Values[spec.Name]; !ok {
					return replayInvalid(path, "MISSING_OBJECT_FACT_INPUT", "日志缺少 latencyMs 的入池快照")
				}
			}
			if !python && spec.Name == "preferredRoles" {
				if _, ok := values.StringLists[spec.Name]; !ok {
					return replayInvalid(path, "MISSING_OBJECT_FACT_INPUT", "日志缺少 preferredRoles 的入池快照")
				}
			}
		}
	}
	return nil
}

func validateReplayProviderBindings(source *matchsystem.ProviderDescriptorSet, target matchsystem.ProviderDescriptorSet, targetContract contract.Contract) error {
	if len(targetContract.Facts) == 0 {
		return nil
	}
	if source == nil {
		return replayInvalid("$.timelineJson.events[0].providerDescriptors", "MISSING_SOURCE_PROVIDER", "源日志缺少实际 Provider 握手描述")
	}
	for _, spec := range targetContract.Facts {
		var before, after *matchsystem.ProviderDescriptor
		switch spec.Scope {
		case fact.ScopeTick:
			before, after = source.Tick, target.Tick
		case fact.ScopeObject:
			before, after = source.Object, target.Object
		case fact.ScopeMatch:
			before, after = source.Match, target.Match
		}
		if before == nil || after == nil || before.ID != after.ID || before.Version != after.Version {
			return replayInvalid("$.providerLogicalNode", "PROVIDER_BINDING_MISMATCH", fmt.Sprintf("Fact %q 的源与目标 Provider 标识或版本不一致", spec.Name))
		}
		found := false
		for _, capability := range before.Facts {
			if capability.Name == spec.Name && capability.Type == spec.Type && capability.Scope == spec.Scope && capability.MaxValues == spec.MaxValues {
				found = true
				break
			}
		}
		if !found {
			return replayInvalid("$.timelineJson.events[0].providerDescriptors", "SOURCE_PROVIDER_INCOMPATIBLE", fmt.Sprintf("源 Provider 不支持 Fact %q 的声明", spec.Name))
		}
	}
	return nil
}

// decodeReplayJSON 接受必要的 null，同时拒绝重复键、未知字段和尾随值。
func decodeReplayJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := scanReplayJSON(decoder, 0); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return fmt.Errorf("包含尾随 JSON 值")
		}
		return err
	}
	return decodeStrictJSON(data, target)
}

func scanReplayJSON(decoder *json.Decoder, depth int) error {
	if depth > 96 {
		return fmt.Errorf("JSON 嵌套过深")
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, composite := token.(json.Delim)
	if !composite {
		return nil
	}
	switch delim {
	case '{':
		keys := make(map[string]bool)
		for decoder.More() {
			key, err := decoder.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok || keys[name] {
				return fmt.Errorf("对象包含重复或无效字段 %q", name)
			}
			keys[name] = true
			if err := scanReplayJSON(decoder, depth+1); err != nil {
				return err
			}
		}
	case '[':
		for decoder.More() {
			if err := scanReplayJSON(decoder, depth+1); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("无效 JSON 定界符")
	}
	_, err = decoder.Token()
	return err
}
