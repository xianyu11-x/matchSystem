package simulator

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/xianyu11-x/matchSystem/pkg/identity"
	"github.com/xianyu11-x/matchSystem/pkg/matchsystem"
)

// ReplayExtractRequest 中的 Logs 支持 JSON Lines 和带标记的文本日志；空值读取当前应用的录制日志。
type ReplayExtractRequest struct {
	Logs       *string `json:"logs,omitempty"`
	InstanceID string  `json:"instanceId,omitempty"`
}

type ReplayInstance struct {
	InstanceID     string                  `json:"instanceId"`
	PhysicalNodeID string                  `json:"physicalNodeId"`
	LogicalNode    identity.LogicalNodeKey `json:"logicalNode"`
	EventCount     int                     `json:"eventCount"`
	FirstSequence  string                  `json:"firstSequence"`
	LastSequence   string                  `json:"lastSequence"`
	Replayable     bool                    `json:"replayable"`
	Ended          bool                    `json:"ended"`
	Issue          string                  `json:"issue,omitempty"`
}

type ReplayExtractResult struct {
	Instances    []ReplayInstance `json:"instances"`
	TimelineJSON string           `json:"timelineJson,omitempty"`
}

type replayTimeline struct {
	SchemaVersion string                      `json:"schemaVersion"`
	InstanceID    string                      `json:"instanceId"`
	Events        []matchsystem.TimelineEvent `json:"events"`
	Facts         []replayFactsRecord         `json:"facts,omitempty"`
}

type replayLogRecord struct {
	Timeline      json.RawMessage `json:"timeline"`
	TimelineFacts json.RawMessage `json:"timelineFacts"`
	Message       json.RawMessage `json:"msg"`
	MessageAlias  json.RawMessage `json:"message"`
}

// 旧文本格式的前缀仅用于识别普通日志和报告显式损坏，不限制外部宿主前缀。
var replayTextPrefix = regexp.MustCompile(`^([0-9]{4}-[0-9]{2}-[0-9]{2}[ T][0-9]{2}:[0-9]{2}:[0-9]{2}([.,][0-9]{1,9})?(Z|[+-][0-9]{2}:?[0-9]{2})?)[ \t]+(?i:TRACE|DEBUG|INFO|WARN|WARNING|ERROR|FATAL|CRITICAL|PANIC)[ \t]+`)

// 部分截断的事件仍可能已写出顶层字段，不能作为普通宿主日志忽略。
var replayPayloadField = regexp.MustCompile(`"(?:timeline|timelineFacts)"[ \t]*:`)

func replayLegacyTextPrefixEnd(text []byte) int {
	indices := replayTextPrefix.FindSubmatchIndex(text)
	if indices == nil {
		return 0
	}
	timestamp := string(text[indices[2]:indices[3]])
	layout := "2006-01-02 15:04:05"
	if timestamp[10] == 'T' {
		layout = "2006-01-02T15:04:05"
	}
	zoneStart, zoneEnd := indices[6], indices[7]
	if zoneStart >= 0 {
		zone := string(text[zoneStart:zoneEnd])
		if zone == "Z" || strings.Contains(zone, ":") {
			layout += "Z07:00"
		} else {
			layout += "Z0700"
		}
	}
	if _, err := time.Parse(layout, timestamp); err != nil {
		return 0
	}
	return indices[1]
}

func replayHasValue(raw json.RawMessage) bool {
	return len(raw) != 0 && !bytes.Equal(raw, []byte("null"))
}

// 一般日志字段 message 只有带明确事件字段时才作为回放正文。
func replayMessageHasEventPayload(message []byte) bool {
	if !replayMessageMarked(message) {
		return false
	}
	payload := bytes.TrimLeftFunc(message[len(matchsystem.TimelineLogMessage):], unicode.IsSpace)
	if len(payload) == 0 || payload[0] != '{' {
		return false
	}
	var record replayLogRecord
	if json.Unmarshal(payload, &record) != nil {
		return replayPayloadField.Match(payload)
	}
	return len(record.Timeline) != 0 || len(record.TimelineFacts) != 0
}

// msg 存在时优先使用它，避免同一外层记录被处理两次。
func replayJSONMessage(record replayLogRecord) ([]byte, bool) {
	var message string
	if len(record.Message) > 0 {
		if json.Unmarshal(record.Message, &message) == nil && replayMessageMarked([]byte(message)) {
			return []byte(message), true
		}
		return nil, false
	}
	if json.Unmarshal(record.MessageAlias, &message) == nil && replayMessageHasEventPayload([]byte(message)) {
		return []byte(message), true
	}
	return nil, false
}

// 只把完整标记视为标记；例如 [match_replay_debug] 是另一个普通词。
func replayMessageMarked(message []byte) bool {
	marker := []byte(matchsystem.TimelineLogMessage)
	if !bytes.HasPrefix(message, marker) {
		return false
	}
	if len(message) == len(marker) {
		return true
	}
	ch, _ := utf8.DecodeRune(message[len(marker):])
	return ch == '{' || unicode.IsSpace(ch)
}

// slog.TextHandler 使用 Go 引号编码值。只读取独立的 msg 字段，并跳过其他带引号的字段。
func replaySlogTextMessage(text []byte) (message []byte, found, malformed bool) {
	for i := 0; i < len(text); {
		for i < len(text) && (text[i] == ' ' || text[i] == '\t') {
			i++
		}
		start := i
		quoted, escaped := false, false
		for i < len(text) {
			ch := text[i]
			if !quoted && (ch == ' ' || ch == '\t') {
				break
			}
			if quoted && escaped {
				escaped = false
			} else if quoted && ch == '\\' {
				escaped = true
			} else if ch == '"' {
				quoted = !quoted
			}
			i++
		}
		token := text[start:i]
		if !bytes.HasPrefix(token, []byte("msg=")) {
			continue
		}
		value := token[len("msg="):]
		if len(value) > 0 && value[0] == '"' {
			decoded, err := strconv.Unquote(string(value))
			if err != nil {
				return nil, true, replayMessageMarked(value[1:])
			}
			return []byte(decoded), true, false
		}
		// 有些宿主将未加引号的 msg= 作为前缀，正文仍是标记加 JSON 对象。
		if bytes.Equal(value, []byte(matchsystem.TimelineLogMessage)) {
			remaining := bytes.TrimLeftFunc(text[i:], unicode.IsSpace)
			if len(remaining) > 0 && remaining[0] == '{' {
				return text[start+len("msg="):], true, false
			}
		}
		return value, true, false
	}
	return nil, false, false
}

// 外部文本允许任意前缀。未知前缀后的标记必须携带明确的事件字段；行首和旧前缀保持严格诊断。
func replayTextMessage(text []byte) ([]byte, bool) {
	marker := []byte(matchsystem.TimelineLogMessage)
	legacyEnd := replayLegacyTextPrefixEnd(text)
	for offset := 0; offset < len(text); {
		i := bytes.Index(text[offset:], marker)
		if i < 0 {
			break
		}
		i += offset
		offset = i + len(marker)
		if i > 0 {
			previous, _ := utf8.DecodeLastRune(text[:i])
			if previous == '_' || unicode.IsLetter(previous) || unicode.IsDigit(previous) {
				continue
			}
		}
		message := text[i:]
		if !replayMessageMarked(message) {
			continue
		}
		if i == 0 || i == legacyEnd {
			return message, true
		}
		payload := bytes.TrimLeftFunc(message[len(marker):], unicode.IsSpace)
		if len(payload) == 0 || payload[0] != '{' {
			continue
		}
		var record replayLogRecord
		if json.Unmarshal(payload, &record) == nil {
			if len(record.Timeline) > 0 || len(record.TimelineFacts) > 0 {
				return message, true
			}
		} else if replayPayloadField.Match(payload) {
			return message, true
		}
	}
	return nil, false
}

func parseReplayMessage(message []byte, line int) (replayLogRecord, error) {
	var record replayLogRecord
	rest := message[len(matchsystem.TimelineLogMessage):]
	payload := bytes.TrimLeftFunc(rest, unicode.IsSpace)
	if len(payload) == len(rest) || len(payload) == 0 {
		return record, replayInvalid("$.logs", "INVALID_LOG_JSON", fmt.Sprintf("第 %d 行的回放标记后必须以空白分隔 JSON 对象", line))
	}
	if payload[0] != '{' {
		return record, replayInvalid("$.logs", "INVALID_LOG_JSON", fmt.Sprintf("第 %d 行的回放标记载荷不是有效 JSON 对象", line))
	}
	if err := json.Unmarshal(payload, &record); err != nil {
		return record, replayInvalid("$.logs", "INVALID_LOG_JSON", fmt.Sprintf("第 %d 行的回放标记载荷不是有效 JSON 对象：%v", line, err))
	}
	if !replayHasValue(record.Timeline) && !replayHasValue(record.TimelineFacts) {
		return record, replayInvalid("$.logs", "INVALID_LOG_JSON", fmt.Sprintf("第 %d 行的回放标记载荷缺少 timeline 或 timelineFacts", line))
	}
	return record, nil
}

// 只在行首对象已经完整结束时，将其后的内容作为宿主文本前缀。
func replayAfterJSONPrefix(text []byte) ([]byte, bool) {
	decoder := json.NewDecoder(bytes.NewReader(text))
	var prefix json.RawMessage
	if err := decoder.Decode(&prefix); err != nil {
		return nil, false
	}
	offset := int(decoder.InputOffset())
	if offset >= len(text) {
		return nil, false
	}
	return bytes.TrimSpace(text[offset:]), true
}

// 先判断整份输入是否包含明确回放消息，才能忽略消息之前或之后的普通宿主文本。
func replayMarkedLine(text []byte) bool {
	if !bytes.Contains(text, []byte(matchsystem.TimelineLogMessage)) {
		return false
	}
	if replayMessageMarked(text) {
		return true
	}
	if len(text) > 0 && text[0] == '{' {
		var record replayLogRecord
		if json.Unmarshal(text, &record) == nil {
			_, marked := replayJSONMessage(record)
			return marked
		}
		var ok bool
		text, ok = replayAfterJSONPrefix(text)
		if !ok {
			return false
		}
	}
	if json.Valid(text) {
		return false
	}
	if message, found, malformed := replaySlogTextMessage(text); found {
		return malformed || replayMessageMarked(message)
	}
	_, marked := replayTextMessage(text)
	return marked
}

func parseReplayLogLine(text []byte, line int, allowText bool) (replayLogRecord, bool, error) {
	var record replayLogRecord
	if replayMessageMarked(text) {
		parsed, err := parseReplayMessage(text, line)
		return parsed, false, err
	}
	jsonErr := json.Unmarshal(text, &record)
	if jsonErr == nil {
		if message, marked := replayJSONMessage(record); marked {
			// 历史 JSONHandler 只将标记作为 msg，事件位于顶层字段。
			if string(message) == matchsystem.TimelineLogMessage && (replayHasValue(record.Timeline) || replayHasValue(record.TimelineFacts)) {
				return record, false, nil
			}
			parsed, err := parseReplayMessage(message, line)
			return parsed, false, err
		}
		return record, false, nil
	}
	// 完整 JSON 的其他字符串字段不能被误识别为文本回放消息。
	if json.Valid(text) {
		if allowText {
			return record, true, nil
		}
		return record, false, replayInvalid("$.logs", "INVALID_LOG_JSON", fmt.Sprintf("第 %d 行不是有效 JSON 日志：%v", line, jsonErr))
	}
	// 以对象起始的行保留 JSONL 损坏诊断，已识别的回放字段始终严格检查。
	if len(text) > 0 && text[0] == '{' {
		if suffix, ok := replayAfterJSONPrefix(text); ok {
			text = suffix
		} else {
			if allowText && !replayPayloadField.Match(text) && !bytes.Contains(text, []byte(`"[match_replay]`)) {
				return record, true, nil
			}
			return record, false, replayInvalid("$.logs", "INVALID_LOG_JSON", fmt.Sprintf("第 %d 行不是有效 JSON：%v", line, jsonErr))
		}
	}
	if message, found, malformed := replaySlogTextMessage(text); found {
		if malformed {
			return record, false, replayInvalid("$.logs", "INVALID_LOG_JSON", fmt.Sprintf("第 %d 行的 msg 字段不是有效引号字符串", line))
		}
		if replayMessageMarked(message) {
			parsed, err := parseReplayMessage(message, line)
			return parsed, false, err
		}
		if allowText || replayLegacyTextPrefixEnd(text) != 0 {
			return record, true, nil
		}
		return record, false, replayInvalid("$.logs", "INVALID_LOG_JSON", fmt.Sprintf("第 %d 行不是有效 JSON：%v", line, jsonErr))
	}
	if message, marked := replayTextMessage(text); marked {
		parsed, err := parseReplayMessage(message, line)
		return parsed, false, err
	}
	if allowText || replayLegacyTextPrefixEnd(text) != 0 {
		return record, true, nil
	}
	return record, false, replayInvalid("$.logs", "INVALID_LOG_JSON", fmt.Sprintf("第 %d 行不是有效 JSON：%v", line, jsonErr))
}

func replayInvalid(path, code, message string) error {
	return &ValidationError{Issues: []ValidationIssue{{Path: path, Code: code, Message: message}}}
}

// ExtractTimeline 从结构化或带标记的文本日志行按实例分组；原始数字始终由 Go 整数解析。
func (s *Simulator) ExtractTimeline(ctx context.Context, request ReplayExtractRequest) (ReplayExtractResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return ReplayExtractResult{}, err
	}
	if s == nil {
		return ReplayExtractResult{}, ErrSimulatorClosed
	}
	logs := ""
	if request.Logs == nil {
		s.mu.RLock()
		if s.closed {
			s.mu.RUnlock()
			return ReplayExtractResult{}, ErrSimulatorClosed
		}
		log := s.replayLog
		logs = log.snapshot()
		s.mu.RUnlock()
	} else {
		logs = *request.Logs
	}
	if len(logs) > replayLogLimit {
		return ReplayExtractResult{}, replayInvalid("$.logs", "LOG_LIMIT", "日志超过 32 MiB 限制")
	}
	markedInput := false
	probe := bufio.NewScanner(strings.NewReader(logs))
	probe.Buffer(make([]byte, 4096), replayLogLimit)
	for probe.Scan() {
		if err := ctx.Err(); err != nil {
			return ReplayExtractResult{}, err
		}
		if replayMarkedLine(bytes.TrimSpace(probe.Bytes())) {
			markedInput = true
			break
		}
	}
	if err := probe.Err(); err != nil {
		return ReplayExtractResult{}, replayInvalid("$.logs", "INVALID_LOG", err.Error())
	}
	groups := make(map[string]*replayTimeline)
	sequences := make(map[string]map[uint64][]byte)
	factOrders := make(map[string]map[uint64][]byte)
	scanner := bufio.NewScanner(strings.NewReader(logs))
	scanner.Buffer(make([]byte, 4096), replayLogLimit)
	for line := 1; scanner.Scan(); line++ {
		if err := ctx.Err(); err != nil {
			return ReplayExtractResult{}, err
		}
		text := bytes.TrimSpace(scanner.Bytes())
		if len(text) == 0 {
			continue
		}
		record, skip, err := parseReplayLogLine(text, line, markedInput)
		if err != nil {
			return ReplayExtractResult{}, err
		}
		if skip {
			continue
		}
		if len(record.Timeline) > 0 && !bytes.Equal(record.Timeline, []byte("null")) {
			var event matchsystem.TimelineEvent
			if err := decodeReplayJSON(record.Timeline, &event); err != nil || event.InstanceID == "" || event.Sequence == 0 {
				return ReplayExtractResult{}, replayInvalid("$.logs", "INVALID_TIMELINE_EVENT", fmt.Sprintf("第 %d 行的时间线事件无效", line))
			}
			group := groups[event.InstanceID]
			if group == nil {
				group = &replayTimeline{SchemaVersion: matchsystem.TimelineVersion, InstanceID: event.InstanceID}
				groups[event.InstanceID] = group
				sequences[event.InstanceID] = make(map[uint64][]byte)
				factOrders[event.InstanceID] = make(map[uint64][]byte)
			}
			canonical, _ := json.Marshal(event)
			if previous, exists := sequences[event.InstanceID][event.Sequence]; exists {
				if !bytes.Equal(previous, canonical) {
					return ReplayExtractResult{}, replayInvalid("$.logs", "CONFLICTING_EVENT", fmt.Sprintf("实例 %s 的序号 %d 存在冲突", event.InstanceID, event.Sequence))
				}
			} else {
				sequences[event.InstanceID][event.Sequence] = canonical
				group.Events = append(group.Events, event)
			}
		}
		if len(record.TimelineFacts) > 0 && !bytes.Equal(record.TimelineFacts, []byte("null")) {
			var values replayFactsRecord
			if err := decodeReplayJSON(record.TimelineFacts, &values); err != nil || values.InstanceID == "" || values.AddSequence == 0 || values.Order == 0 {
				return ReplayExtractResult{}, replayInvalid("$.logs", "INVALID_FACT_EVENT", fmt.Sprintf("第 %d 行的 Object Fact 事件无效", line))
			}
			group := groups[values.InstanceID]
			if group == nil {
				group = &replayTimeline{SchemaVersion: matchsystem.TimelineVersion, InstanceID: values.InstanceID}
				groups[values.InstanceID] = group
				sequences[values.InstanceID] = make(map[uint64][]byte)
				factOrders[values.InstanceID] = make(map[uint64][]byte)
			}
			canonical, _ := json.Marshal(values)
			if previous, exists := factOrders[values.InstanceID][values.Order]; exists {
				if !bytes.Equal(previous, canonical) {
					return ReplayExtractResult{}, replayInvalid("$.logs", "CONFLICTING_FACT_EVENT", fmt.Sprintf("实例 %s 的 Fact 更新序号 %d 存在冲突", values.InstanceID, values.Order))
				}
				continue
			}
			factOrders[values.InstanceID][values.Order] = canonical
			group.Facts = append(group.Facts, values)
		}
	}
	if err := scanner.Err(); err != nil {
		return ReplayExtractResult{}, replayInvalid("$.logs", "INVALID_LOG", err.Error())
	}
	ids := make([]string, 0, len(groups))
	for id := range groups {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	result := ReplayExtractResult{Instances: make([]ReplayInstance, 0, len(ids))}
	for _, id := range ids {
		group := groups[id]
		sort.Slice(group.Events, func(i, j int) bool { return group.Events[i].Sequence < group.Events[j].Sequence })
		sort.SliceStable(group.Facts, func(i, j int) bool { return group.Facts[i].Order < group.Facts[j].Order })
		if len(group.Events) == 0 {
			continue
		}
		first, last := group.Events[0], group.Events[len(group.Events)-1]
		item := ReplayInstance{InstanceID: id, PhysicalNodeID: strconv.FormatUint(uint64(last.PhysicalNodeID), 10), LogicalNode: last.LogicalNode, EventCount: len(group.Events), FirstSequence: strconv.FormatUint(first.Sequence, 10), LastSequence: strconv.FormatUint(last.Sequence, 10), Ended: last.Type == matchsystem.TimelineStreamEnd}
		if err := validateReplayTimeline(*group); err != nil {
			item.Issue = err.Error()
		} else {
			item.Replayable = true
		}
		result.Instances = append(result.Instances, item)
		if request.InstanceID == id {
			data, err := json.Marshal(group)
			if err != nil {
				return ReplayExtractResult{}, err
			}
			result.TimelineJSON = string(data)
		}
	}
	if request.InstanceID != "" && result.TimelineJSON == "" {
		return ReplayExtractResult{}, replayInvalid("$.instanceId", "INSTANCE_NOT_FOUND", "找不到指定的节点实例")
	}
	return result, nil
}
