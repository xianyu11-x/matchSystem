package simulator

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/xianyu11-x/matchSystem/pkg/identity"
)

// DecodeUint64JSON 在输入边界读取精确整数或规范十进制字符串。
func DecodeUint64JSON(data []byte) (uint64, error) {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || bytes.Equal(data, []byte("null")) {
		return 0, fmt.Errorf("uint64 identity is required")
	}
	var value string
	if data[0] == '"' {
		if err := json.Unmarshal(data, &value); err != nil {
			return 0, err
		}
	} else {
		var number json.Number
		if err := json.Unmarshal(data, &number); err != nil {
			return 0, fmt.Errorf("uint64 identity must be an integer: %w", err)
		}
		value = number.String()
	}
	if value == "" {
		return 0, fmt.Errorf("uint64 identity must be non-empty")
	}
	return parseRoutingValue(value)
}

// DecodeRuleKeyJSON 仅将 RuleID 解释为身份，保留 Namespace 原文。
func DecodeRuleKeyJSON(data []byte) (identity.RuleKey, error) {
	var wire struct {
		Namespace json.RawMessage `json:"namespace"`
		RuleID    json.RawMessage `json:"ruleId"`
	}
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return identity.RuleKey{}, fmt.Errorf("rule must be an object")
	}
	if err := decodeStrictJSON(data, &wire); err != nil {
		return identity.RuleKey{}, err
	}
	var key identity.RuleKey
	if len(wire.Namespace) > 0 {
		if bytes.Equal(bytes.TrimSpace(wire.Namespace), []byte("null")) {
			return key, fmt.Errorf("namespace must be a string")
		}
		if err := json.Unmarshal(wire.Namespace, &key.Namespace); err != nil {
			return key, fmt.Errorf("invalid namespace: %w", err)
		}
	}
	if len(wire.RuleID) > 0 {
		id, err := DecodeUint64JSON(wire.RuleID)
		if err != nil {
			return key, fmt.Errorf("invalid ruleId: %w", err)
		}
		key.RuleID = id
	}
	return key, nil
}

// DecodeLogicalNodeKeyJSON 在场景和 API 边界读取逻辑部署身份。
func DecodeLogicalNodeKeyJSON(data []byte) (identity.LogicalNodeKey, error) {
	var wire struct {
		Rule        json.RawMessage `json:"rule"`
		PlacementID json.RawMessage `json:"placementId"`
	}
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return identity.LogicalNodeKey{}, fmt.Errorf("logicalNode must be an object")
	}
	if err := decodeStrictJSON(data, &wire); err != nil {
		return identity.LogicalNodeKey{}, err
	}
	var key identity.LogicalNodeKey
	if len(wire.Rule) > 0 {
		rule, err := DecodeRuleKeyJSON(wire.Rule)
		if err != nil {
			return key, fmt.Errorf("invalid rule: %w", err)
		}
		key.Rule = rule
	}
	if len(wire.PlacementID) > 0 {
		id, err := DecodeUint64JSON(wire.PlacementID)
		if err != nil {
			return key, fmt.Errorf("invalid placementId: %w", err)
		}
		key.PlacementID = identity.PlacementID(id)
	}
	return key, nil
}
