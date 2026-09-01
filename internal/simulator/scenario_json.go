package simulator

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"matchSystem/internal/identity"
	"matchSystem/internal/matchsystem"
	"matchSystem/internal/matchsystem/fact"
)

type scenarioJSON struct {
	SchemaVersion     string             `json:"schemaVersion"`
	PhysicalNodes     []PhysicalNodeSpec `json:"physicalNodes"`
	Rules             []RuleSpec         `json:"rules"`
	MatchHistoryLimit int                `json:"matchHistoryLimit,omitempty"`
}

type ruleSpecJSON struct {
	LogicalNode    logicalNodeKeyJSON      `json:"logicalNode"`
	PhysicalNodeID identity.PhysicalNodeID `json:"physicalNodeId"`
	Weight         uint32                  `json:"weight"`
	Enabled        bool                    `json:"enabled"`
	RuleJSON       json.RawMessage         `json:"rule"`
	// TickFacts is the simulator-owned runtime value layer. It is deliberately
	// separate from TickProviderDescriptor, which is only the provider-side
	// startup declaration used by the core handshake.
	TickFacts                    FactSnapshot            `json:"tickFacts,omitempty"`
	FactProviderDescriptor       *providerDescriptorJSON `json:"factProviderDescriptor,omitempty"`
	ObjectFactProviderDescriptor *providerDescriptorJSON `json:"objectFactProviderDescriptor,omitempty"`
	MatchFactProviderDescriptor  *providerDescriptorJSON `json:"matchFactProviderDescriptor,omitempty"`
}

// providerDescriptorJSON is the simulator transport representation of a
// provider handshake declaration. matchsystem.fact.Spec intentionally has no
// JSON tags and uses numeric enums internally, so descriptors must not be
// marshalled directly from the core model.
type providerDescriptorJSON struct {
	ID      string                 `json:"id"`
	Version string                 `json:"version"`
	Facts   []providerFactSpecJSON `json:"facts,omitempty"`
}

type providerFactSpecJSON struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Scope       string `json:"scope"`
	MaxValues   int    `json:"maxValues,omitempty"`
	Description string `json:"description,omitempty"`
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
	schemaVersion := s.SchemaVersion
	if schemaVersion == "" {
		schemaVersion = ScenarioSchemaVersion
	}
	physicalNodes := append([]PhysicalNodeSpec(nil), s.PhysicalNodes...)
	if physicalNodes == nil {
		physicalNodes = []PhysicalNodeSpec{}
	}
	rules := append([]RuleSpec(nil), s.Rules...)
	if rules == nil {
		rules = []RuleSpec{}
	}
	return json.Marshal(scenarioJSON{
		SchemaVersion:     schemaVersion,
		PhysicalNodes:     physicalNodes,
		Rules:             rules,
		MatchHistoryLimit: s.MatchHistoryLimit,
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
	*s = Scenario{
		SchemaVersion:     wire.SchemaVersion,
		PhysicalNodes:     wire.PhysicalNodes,
		Rules:             wire.Rules,
		MatchHistoryLimit: wire.MatchHistoryLimit,
	}
	return nil
}

func (r RuleSpec) MarshalJSON() ([]byte, error) {
	return json.Marshal(ruleSpecJSON{
		LogicalNode:                  logicalNodeKeyJSONFromIdentity(r.LogicalNode),
		PhysicalNodeID:               r.PhysicalNodeID,
		Weight:                       r.Weight,
		Enabled:                      r.Enabled,
		RuleJSON:                     append(json.RawMessage(nil), r.RuleJSON...),
		TickFacts:                    r.TickFacts.clone(),
		FactProviderDescriptor:       providerDescriptorJSONFromCore(r.FactProviderDescriptor),
		ObjectFactProviderDescriptor: providerDescriptorJSONFromCore(r.ObjectFactProviderDescriptor),
		MatchFactProviderDescriptor:  providerDescriptorJSONFromCore(r.MatchFactProviderDescriptor),
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
	tickDescriptor, err := providerDescriptorFromJSON(wire.FactProviderDescriptor)
	if err != nil {
		return fmt.Errorf("decode factProviderDescriptor: %w", err)
	}
	objectDescriptor, err := providerDescriptorFromJSON(wire.ObjectFactProviderDescriptor)
	if err != nil {
		return fmt.Errorf("decode objectFactProviderDescriptor: %w", err)
	}
	matchDescriptor, err := providerDescriptorFromJSON(wire.MatchFactProviderDescriptor)
	if err != nil {
		return fmt.Errorf("decode matchFactProviderDescriptor: %w", err)
	}
	*r = RuleSpec{
		LogicalNode:                  logicalNodeKeyFromJSON(wire.LogicalNode),
		PhysicalNodeID:               wire.PhysicalNodeID,
		Weight:                       wire.Weight,
		Enabled:                      wire.Enabled,
		RuleJSON:                     append(json.RawMessage(nil), wire.RuleJSON...),
		TickFacts:                    wire.TickFacts.clone(),
		FactProviderDescriptor:       tickDescriptor,
		ObjectFactProviderDescriptor: objectDescriptor,
		MatchFactProviderDescriptor:  matchDescriptor,
	}
	return nil
}

func providerDescriptorJSONFromCore(descriptor *matchsystem.ProviderDescriptor) *providerDescriptorJSON {
	if descriptor == nil {
		return nil
	}
	result := &providerDescriptorJSON{
		ID:      descriptor.ID,
		Version: descriptor.Version,
		Facts:   make([]providerFactSpecJSON, len(descriptor.Facts)),
	}
	for index, spec := range descriptor.Facts {
		result.Facts[index] = providerFactSpecJSON{
			Name:        spec.Name,
			Type:        factTypeJSON(spec.Type),
			Scope:       string(spec.Scope),
			MaxValues:   spec.MaxValues,
			Description: spec.Description,
		}
	}
	return result
}

func providerDescriptorFromJSON(descriptor *providerDescriptorJSON) (*matchsystem.ProviderDescriptor, error) {
	if descriptor == nil {
		return nil, nil
	}
	result := &matchsystem.ProviderDescriptor{
		ID:      descriptor.ID,
		Version: descriptor.Version,
		Facts:   make([]matchsystem.FactSpec, len(descriptor.Facts)),
	}
	for index, spec := range descriptor.Facts {
		typeValue, err := factTypeFromJSON(spec.Type)
		if err != nil {
			return nil, fmt.Errorf("facts[%d].type: %w", index, err)
		}
		result.Facts[index] = matchsystem.FactSpec{
			Name:        spec.Name,
			Type:        typeValue,
			Scope:       fact.Scope(spec.Scope),
			MaxValues:   spec.MaxValues,
			Description: spec.Description,
		}
	}
	return result, nil
}

func factTypeJSON(value fact.Type) string {
	switch value {
	case fact.TypeStrings:
		return "strings"
	case fact.TypeInt64:
		return "int64"
	case fact.TypeUint64s:
		return "uint64s"
	default:
		return ""
	}
}

func factTypeFromJSON(value string) (fact.Type, error) {
	switch value {
	case "strings":
		return fact.TypeStrings, nil
	case "int64":
		return fact.TypeInt64, nil
	case "uint64s":
		return fact.TypeUint64s, nil
	default:
		return 0, fmt.Errorf("unsupported Fact type %q", value)
	}
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
