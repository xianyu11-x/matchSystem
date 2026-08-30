package simulator

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"matchSystem/internal/identity"
)

type scenarioJSON struct {
	SchemaVersion string             `json:"schemaVersion"`
	PhysicalNodes []PhysicalNodeSpec `json:"physicalNodes"`
	Rules         []RuleSpec         `json:"rules"`
}

type ruleSpecJSON struct {
	LogicalNode    logicalNodeKeyJSON      `json:"logicalNode"`
	PhysicalNodeID identity.PhysicalNodeID `json:"physicalNodeId"`
	Weight         uint32                  `json:"weight"`
	Enabled        bool                    `json:"enabled"`
	RuleJSON       json.RawMessage         `json:"rule"`
	TickFacts      FactSnapshot            `json:"tickFacts,omitempty"`
}

type ruleKeyJSON struct {
	Namespace string `json:"namespace,omitempty"`
	RuleID    int32  `json:"ruleId"`
}

type logicalNodeKeyJSON struct {
	Rule        ruleKeyJSON `json:"rule"`
	PlacementID string      `json:"placementId"`
}

// MarshalJSON keeps the simulator application DTO independent from the
// internal identity and matchsystem field names. In particular, no
// PascalCase RuleID, PlacementID, or SeedScheduler fields escape to HTTP.
func (s Scenario) MarshalJSON() ([]byte, error) {
	return json.Marshal(scenarioJSON{
		SchemaVersion: s.SchemaVersion,
		PhysicalNodes: s.PhysicalNodes,
		Rules:         s.Rules,
	})
}

func (s *Scenario) UnmarshalJSON(data []byte) error {
	if s == nil {
		return fmt.Errorf("scenario receiver is nil")
	}
	var wire scenarioJSON
	if err := decodeStrictJSON(data, &wire); err != nil {
		return fmt.Errorf("decode simulator scenario: %w", err)
	}
	*s = Scenario{SchemaVersion: wire.SchemaVersion, PhysicalNodes: wire.PhysicalNodes, Rules: wire.Rules}
	return nil
}

func (r RuleSpec) MarshalJSON() ([]byte, error) {
	return json.Marshal(ruleSpecJSON{
		LogicalNode:    logicalNodeKeyJSONFromIdentity(r.LogicalNode),
		PhysicalNodeID: r.PhysicalNodeID,
		Weight:         r.Weight,
		Enabled:        r.Enabled,
		RuleJSON:       append(json.RawMessage(nil), r.RuleJSON...),
		TickFacts:      r.TickFacts.clone(),
	})
}

func (r *RuleSpec) UnmarshalJSON(data []byte) error {
	if r == nil {
		return fmt.Errorf("RuleSpec receiver is nil")
	}
	var wire ruleSpecJSON
	if err := decodeStrictJSON(data, &wire); err != nil {
		return fmt.Errorf("decode RuleSpec: %w", err)
	}
	*r = RuleSpec{
		LogicalNode:    logicalNodeKeyFromJSON(wire.LogicalNode),
		PhysicalNodeID: wire.PhysicalNodeID,
		Weight:         wire.Weight,
		Enabled:        wire.Enabled,
		RuleJSON:       append(json.RawMessage(nil), wire.RuleJSON...),
		TickFacts:      wire.TickFacts.clone(),
	}
	return nil
}

func logicalNodeKeyJSONFromIdentity(value identity.LogicalNodeKey) logicalNodeKeyJSON {
	return logicalNodeKeyJSON{
		Rule:        ruleKeyJSON{Namespace: value.Rule.Namespace, RuleID: value.Rule.RuleID},
		PlacementID: string(value.PlacementID),
	}
}

func logicalNodeKeyFromJSON(value logicalNodeKeyJSON) identity.LogicalNodeKey {
	return identity.LogicalNodeKey{
		Rule:        identity.RuleKey{Namespace: value.Rule.Namespace, RuleID: value.Rule.RuleID},
		PlacementID: identity.PlacementID(value.PlacementID),
	}
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

// MarshalScenarioJSON returns a detached JSON scenario document for a GET
// endpoint or a deterministic fixture.
func MarshalScenarioJSON(scenario Scenario) ([]byte, error) {
	return json.Marshal(scenario)
}
