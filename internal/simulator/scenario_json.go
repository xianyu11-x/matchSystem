package simulator

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"github.com/xianyu11-x/matchSystem/pkg/identity"
)

// MarshalJSON 为缺省版本和空场景提供规范的 JSON 表示。
func (s Scenario) MarshalJSON() ([]byte, error) {
	type scenarioJSON Scenario
	wire := scenarioJSON(s)
	if wire.SchemaVersion == "" {
		wire.SchemaVersion = ScenarioSchemaVersion
	}
	wire.PhysicalNodes = append([]PhysicalNodeSpec(nil), s.PhysicalNodes...)
	if wire.PhysicalNodes == nil {
		wire.PhysicalNodes = []PhysicalNodeSpec{}
	}
	wire.Rules = append([]RuleSpec(nil), s.Rules...)
	if wire.Rules == nil {
		wire.Rules = []RuleSpec{}
	}
	return json.Marshal(wire)
}

func (s *Scenario) UnmarshalJSON(data []byte) error {
	if s == nil {
		return fmt.Errorf("scenario receiver is nil")
	}
	type scenarioJSON Scenario
	var wire scenarioJSON
	if err := decodeStrictJSON(data, &wire); err != nil {
		return fmt.Errorf("decode simulator scenario: %w", err)
	}
	*s = Scenario(wire)
	return nil
}

func (r *RoutingConfig) UnmarshalJSON(data []byte) error {
	if r == nil {
		return fmt.Errorf("RoutingConfig receiver is nil")
	}
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return fmt.Errorf("routing must be an object")
	}
	var wire map[string]json.RawMessage
	if err := decodeStrictJSON(data, &wire); err != nil {
		return fmt.Errorf("decode RoutingConfig: %w", err)
	}
	for name := range wire {
		if name != "algorithm" && name != "ticketLimit" {
			return fmt.Errorf("decode RoutingConfig: unknown field %q", name)
		}
	}
	var config RoutingConfig
	if err := decodeOptionalNonNull(wire["algorithm"], &config.Algorithm); err != nil {
		return fmt.Errorf("decode routing algorithm: %w", err)
	}
	if err := decodeOptionalNonNull(wire["ticketLimit"], &config.TicketLimit); err != nil {
		return fmt.Errorf("decode routing ticketLimit: %w", err)
	}
	*r = config
	return nil
}

// decodeOptionalNonNull 区分缺省字段和不符合 Schema 的显式 null。
func decodeOptionalNonNull(data json.RawMessage, target any) error {
	if len(data) == 0 {
		return nil
	}
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return fmt.Errorf("value must not be null")
	}
	return json.Unmarshal(data, target)
}

func (r RuleSpec) MarshalJSON() ([]byte, error) {
	ruleJSON, err := canonicalRuleKeyJSON(r.RuleJSON)
	if err != nil {
		return nil, err
	}
	type ruleSpecJSON RuleSpec
	wire := ruleSpecJSON(r)
	wire.RuleJSON = ruleJSON
	wire.TickFacts = r.TickFacts.clone()
	wire.PythonProvider = clonePythonProviderConfig(r.PythonProvider)
	return json.Marshal(wire)
}

func (r *RuleSpec) UnmarshalJSON(data []byte) error {
	if r == nil {
		return fmt.Errorf("RuleSpec receiver is nil")
	}
	type ruleSpecJSON RuleSpec
	var wire struct {
		*ruleSpecJSON
		LogicalNode    json.RawMessage `json:"logicalNode"`
		PhysicalNodeID json.RawMessage `json:"physicalNodeId"`
	}
	wire.ruleSpecJSON = new(ruleSpecJSON)
	if err := decodeStrictJSON(data, &wire); err != nil {
		return fmt.Errorf("decode RuleSpec: %w", err)
	}
	if len(wire.LogicalNode) > 0 {
		key, err := DecodeLogicalNodeKeyJSON(wire.LogicalNode)
		if err != nil {
			return fmt.Errorf("decode RuleSpec logicalNode: %w", err)
		}
		wire.ruleSpecJSON.LogicalNode = key
	}
	if len(wire.PhysicalNodeID) > 0 {
		id, err := DecodeUint64JSON(wire.PhysicalNodeID)
		if err != nil {
			return fmt.Errorf("decode RuleSpec physicalNodeId: %w", err)
		}
		wire.ruleSpecJSON.PhysicalNodeID = identity.PhysicalNodeID(id)
	}
	wire.RuleJSON = append(json.RawMessage(nil), wire.RuleJSON...)
	wire.TickFacts = wire.TickFacts.clone()
	wire.PythonProvider = clonePythonProviderConfig(wire.PythonProvider)
	*r = RuleSpec(*wire.ruleSpecJSON)
	return nil
}

func (p *PhysicalNodeSpec) UnmarshalJSON(data []byte) error {
	type physicalNodeJSON PhysicalNodeSpec
	var wire struct {
		*physicalNodeJSON
		ID                    json.RawMessage `json:"id"`
		RouteUpdateIntervalMS json.RawMessage `json:"routeUpdateIntervalMs"`
	}
	wire.physicalNodeJSON = new(physicalNodeJSON)
	if err := decodeStrictJSON(data, &wire); err != nil {
		return fmt.Errorf("decode PhysicalNodeSpec: %w", err)
	}
	if len(wire.ID) > 0 {
		id, err := DecodeUint64JSON(wire.ID)
		if err != nil {
			return fmt.Errorf("decode PhysicalNodeSpec id: %w", err)
		}
		wire.physicalNodeJSON.ID = identity.PhysicalNodeID(id)
	}
	if err := decodeOptionalNonNull(wire.RouteUpdateIntervalMS, &wire.physicalNodeJSON.RouteUpdateIntervalMS); err != nil {
		return fmt.Errorf("decode PhysicalNodeSpec routeUpdateIntervalMs: %w", err)
	}
	*p = PhysicalNodeSpec(*wire.physicalNodeJSON)
	return nil
}

func clonePythonProviderConfig(config *PythonProviderConfig) *PythonProviderConfig {
	if config == nil {
		return nil
	}
	copy := *config
	return &copy
}

// canonicalRuleKeyJSON 仅替换顶层 ruleKey 的值，保留其他原始规则字段及其顺序。
func canonicalRuleKeyJSON(data json.RawMessage) (json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil {
		return nil, fmt.Errorf("decode rule JSON: %w", err)
	}
	if token != json.Delim('{') {
		return nil, fmt.Errorf("rule JSON must be an object")
	}
	type replacement struct {
		start, end int
		data       []byte
	}
	var replacements []replacement
	for decoder.More() {
		name, err := decoder.Token()
		if err != nil {
			return nil, fmt.Errorf("decode rule JSON field: %w", err)
		}
		start := int(decoder.InputOffset())
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, fmt.Errorf("decode rule JSON value: %w", err)
		}
		if name != "ruleKey" {
			continue
		}
		for start < len(data) && (data[start] == ':' || data[start] == ' ' || data[start] == '\n' || data[start] == '\r' || data[start] == '\t') {
			start++
		}
		key, err := DecodeRuleKeyJSON(value)
		if err != nil {
			return nil, fmt.Errorf("decode ruleKey: %w", err)
		}
		canonical, err := json.Marshal(key)
		if err != nil {
			return nil, fmt.Errorf("encode ruleKey: %w", err)
		}
		replacements = append(replacements, replacement{start: start, end: int(decoder.InputOffset()), data: canonical})
	}
	if _, err := decoder.Token(); err != nil {
		return nil, fmt.Errorf("decode rule JSON: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("trailing JSON value in rule")
		}
		return nil, fmt.Errorf("decode rule JSON: %w", err)
	}
	result := append(json.RawMessage(nil), data...)
	for i := len(replacements) - 1; i >= 0; i-- {
		item := replacements[i]
		result = append(append(append(json.RawMessage(nil), result[:item.start]...), item.data...), result[item.end:]...)
	}
	return result, nil
}

func decodeStrictJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("trailing JSON value")
		}
		return err
	}
	return nil
}

// ParseScenarioJSON decodes one complete scenario DTO and runs the same
// semantic validation used by NewSimulator. Unknown fields and trailing JSON
// values are rejected before any runtime is constructed.
func ParseScenarioJSON(data []byte) (Scenario, error) {
	var scenario Scenario
	if err := decodeStrictJSON(data, &scenario); err != nil {
		return Scenario{}, fmt.Errorf("decode simulator scenario: %w", err)
	}
	report := ValidateScenario(scenario)
	if err := report.Err(); err != nil {
		return Scenario{}, err
	}
	if scenario.SchemaVersion == "" {
		scenario.SchemaVersion = ScenarioSchemaVersion
	}
	return scenario.Clone(), nil
}
