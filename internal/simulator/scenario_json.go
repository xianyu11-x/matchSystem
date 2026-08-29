package simulator

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"matchSystem/internal/identity"
	"matchSystem/internal/matchsystem"
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
	ContractJSON   json.RawMessage         `json:"contract"`
	PrefilterJSON  json.RawMessage         `json:"prefilter"`
	EvaluationJSON json.RawMessage         `json:"evaluation"`
	Config         logicalNodeConfigJSON   `json:"config"`
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

type logicalNodeConfigJSON struct {
	SeedScheduler         seedSchedulerConfigJSON `json:"seedScheduler"`
	CandidateLimitPerSeed int                     `json:"candidateLimitPerSeed"`
	MaxPlayers            int                     `json:"maxPlayers"`
}

type seedSchedulerConfigJSON struct {
	AttemptLimitPerProduceMatch int                       `json:"attemptLimitPerProduceMatch"`
	AttemptLimitPerMatchRound   int                       `json:"attemptLimitPerMatchRound"`
	Order                       seedOrderPolicyConfigJSON `json:"order"`
}

type seedOrderPolicyConfigJSON struct {
	Kind              string `json:"kind"`
	PriorityField     string `json:"priorityField"`
	PriorityDirection string `json:"priorityDirection"`
	RandomSeed        int64  `json:"randomSeed"`
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
		ContractJSON:   append(json.RawMessage(nil), r.ContractJSON...),
		PrefilterJSON:  append(json.RawMessage(nil), r.PrefilterJSON...),
		EvaluationJSON: append(json.RawMessage(nil), r.EvaluationJSON...),
		Config:         logicalNodeConfigJSONFromCore(r.Config),
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
		ContractJSON:   append(json.RawMessage(nil), wire.ContractJSON...),
		PrefilterJSON:  append(json.RawMessage(nil), wire.PrefilterJSON...),
		EvaluationJSON: append(json.RawMessage(nil), wire.EvaluationJSON...),
		Config:         logicalNodeConfigToCore(wire.Config),
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

func logicalNodeConfigJSONFromCore(value matchsystem.LogicalNodeConfig) logicalNodeConfigJSON {
	return logicalNodeConfigJSON{
		SeedScheduler: seedSchedulerConfigJSON{
			AttemptLimitPerProduceMatch: value.SeedScheduler.AttemptLimitPerProduceMatch,
			AttemptLimitPerMatchRound:   value.SeedScheduler.AttemptLimitPerMatchRound,
			Order: seedOrderPolicyConfigJSON{
				Kind:              string(value.SeedScheduler.Order.Kind),
				PriorityField:     value.SeedScheduler.Order.PriorityField,
				PriorityDirection: string(value.SeedScheduler.Order.PriorityDirection),
				RandomSeed:        value.SeedScheduler.Order.RandomSeed,
			},
		},
		CandidateLimitPerSeed: value.CandidateLimitPerSeed,
		MaxPlayers:            value.MaxPlayers,
	}
}

func logicalNodeConfigToCore(value logicalNodeConfigJSON) matchsystem.LogicalNodeConfig {
	return matchsystem.LogicalNodeConfig{
		SeedScheduler: matchsystem.SeedSchedulerConfig{
			AttemptLimitPerProduceMatch: value.SeedScheduler.AttemptLimitPerProduceMatch,
			AttemptLimitPerMatchRound:   value.SeedScheduler.AttemptLimitPerMatchRound,
			Order: matchsystem.SeedOrderPolicyConfig{
				Kind:              matchsystem.SeedOrderPolicyKind(value.SeedScheduler.Order.Kind),
				PriorityField:     value.SeedScheduler.Order.PriorityField,
				PriorityDirection: matchsystem.SeedPriorityDirection(value.SeedScheduler.Order.PriorityDirection),
				RandomSeed:        value.SeedScheduler.Order.RandomSeed,
			},
		},
		CandidateLimitPerSeed: value.CandidateLimitPerSeed,
		MaxPlayers:            value.MaxPlayers,
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
