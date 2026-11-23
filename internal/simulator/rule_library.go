package simulator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/xianyu11-x/matchSystem/pkg/identity"
	"github.com/xianyu11-x/matchSystem/pkg/matchsystem"
)

// ErrRuleLibraryConflict 表示规则库已被其他请求修改。
var ErrRuleLibraryConflict = errors.New("simulator rule library revision conflict")

// RuleLibrarySnapshot 是与运行场景独立的规则定义快照。
// Revision 以十进制字符串传输，避免 JavaScript 整数精度丢失。
type RuleLibrarySnapshot struct {
	Revision uint64            `json:"revision,string"`
	Rules    []json.RawMessage `json:"rules"`
}

// MarshalJSON 保留规则原始内容，同时将规则身份输出为无损十进制字符串。
func (snapshot RuleLibrarySnapshot) MarshalJSON() ([]byte, error) {
	type snapshotJSON RuleLibrarySnapshot
	wire := snapshotJSON{Revision: snapshot.Revision, Rules: make([]json.RawMessage, len(snapshot.Rules))}
	for i, raw := range snapshot.Rules {
		canonical, err := canonicalRuleKeyJSON(raw)
		if err != nil {
			return nil, fmt.Errorf("encode rule library rule %d: %w", i, err)
		}
		wire.Rules[i] = canonical
	}
	return json.Marshal(wire)
}

func (snapshot RuleLibrarySnapshot) clone() RuleLibrarySnapshot {
	result := RuleLibrarySnapshot{Revision: snapshot.Revision, Rules: make([]json.RawMessage, len(snapshot.Rules))}
	for i, rule := range snapshot.Rules {
		result.Rules[i] = append(json.RawMessage(nil), rule...)
	}
	return result
}

// initialRuleLibrary 仅在创建模拟器时复制一次初始场景中的规则。
func initialRuleLibrary(scenario Scenario) (RuleLibrarySnapshot, map[identity.RuleID]json.RawMessage) {
	// 每次服务启动选取新的版本起点，避免旧客户端误用上一次进程的 revision。
	revision := time.Now().UnixNano()
	if revision <= 0 {
		revision = 1
	}
	result := RuleLibrarySnapshot{Revision: uint64(revision), Rules: make([]json.RawMessage, 0, len(scenario.Rules))}
	index := make(map[identity.RuleID]json.RawMessage, len(scenario.Rules))
	for _, rule := range scenario.Rules {
		if _, exists := index[rule.LogicalNode.Rule.RuleID]; exists {
			continue
		}
		raw := append(json.RawMessage(nil), rule.RuleJSON...)
		index[rule.LogicalNode.Rule.RuleID] = raw
		result.Rules = append(result.Rules, raw)
	}
	return result, index
}

// RuleLibrary 返回独立规则库的深拷贝。
func (s *Simulator) RuleLibrary(ctx context.Context) (RuleLibrarySnapshot, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return RuleLibrarySnapshot{}, err
	}
	if s == nil {
		return RuleLibrarySnapshot{}, ErrSimulatorClosed
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return RuleLibrarySnapshot{}, ErrSimulatorClosed
	}
	if err := ctx.Err(); err != nil {
		return RuleLibrarySnapshot{}, err
	}
	return s.ruleLibrary.clone(), nil
}

// ReplaceRuleLibrary 在版本一致且全部规则合法、身份唯一时原子替换规则库。
// 规则库的写入不会触碰已部署规则或运行中的节点。
func (s *Simulator) ReplaceRuleLibrary(ctx context.Context, expectedRevision uint64, rules []json.RawMessage) (RuleLibrarySnapshot, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return RuleLibrarySnapshot{}, err
	}
	if s == nil {
		return RuleLibrarySnapshot{}, ErrSimulatorClosed
	}
	owned := make([]json.RawMessage, len(rules))
	seen := make(map[identity.RuleID]int, len(rules))
	index := make(map[identity.RuleID]json.RawMessage, len(rules))
	issues := make([]ValidationIssue, 0)
	for i, rule := range rules {
		owned[i] = append(json.RawMessage(nil), rule...)
		compiled, err := matchsystem.CompileRuleJSON(owned[i])
		if err != nil {
			issues = append(issues, issueAt(fmt.Sprintf("$.rules[%d]", i), "INVALID_RULE", err))
			continue
		}
		key := compiled.RuleKey()
		if first, exists := seen[key.RuleID]; exists {
			issues = append(issues, ValidationIssue{Path: fmt.Sprintf("$.rules[%d].ruleKey", i), Code: "DUPLICATE_RULE_KEY", Message: fmt.Sprintf("RuleKey %s is already declared at index %d", key, first)})
			continue
		}
		seen[key.RuleID] = i
		index[key.RuleID] = owned[i]
	}
	if len(issues) > 0 {
		return RuleLibrarySnapshot{}, &ValidationError{Issues: issues}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return RuleLibrarySnapshot{}, ErrSimulatorClosed
	}
	if err := ctx.Err(); err != nil {
		return RuleLibrarySnapshot{}, err
	}
	if s.ruleLibrary.Revision != expectedRevision {
		return RuleLibrarySnapshot{}, ErrRuleLibraryConflict
	}
	if s.ruleLibrary.Revision == math.MaxUint64 {
		return RuleLibrarySnapshot{}, errors.New("simulator rule library revision exhausted")
	}
	s.ruleLibrary = RuleLibrarySnapshot{Revision: s.ruleLibrary.Revision + 1, Rules: owned}
	s.ruleLibraryIndex = index
	return s.ruleLibrary.clone(), nil
}
