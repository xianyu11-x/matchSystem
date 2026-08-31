package matchsystem

import (
	"testing"

	"matchSystem/internal/identity"
)

func TestLogicalNodeFactSpecsReturnsOwnedMetadata(t *testing.T) {
	key := identity.LogicalNodeKey{
		Rule:        identity.RuleKey{Namespace: "facts", RuleID: 1},
		PlacementID: "default",
	}
	node, err := NewLogicalNode(LogicalNodeSpec{
		Key: key,
		RuleJSON: testRuleJSON(t, key.Rule, `{
			"schemaVersion":"logical-node-contract/v3",
			"attributes":[],
			"facts":[{"name":"party-size","type":"int64","scope":"match","description":"number of players in the current match"}],
			"indexes":[]
		}`, `{
			"schemaVersion":"prefilter/v3",
			"bitmap":{"resultType":"bitmap","expr":{"op":"none"}}
		}`, `{
			"schemaVersion":"evaluation/v3",
			"canJoin":{"schemaVersion":"expression-scalar/v3","resultType":"bool","expr":{"op":"bool_literal","value":true}},
			"canComplete":{"schemaVersion":"expression-scalar/v3","resultType":"bool","expr":{"op":"bool_literal","value":true}}
		}`, logicalNodeConfig{}),
		MatchFactProvider:           testMatchFactProvider{},
		MatchFactProviderDescriptor: &ProviderDescriptor{ID: "facts.test", Version: "v1", Facts: []FactSpec{{Name: "party-size", Type: FactTypeInt64, Scope: FactScopeMatch, Description: "provider-specific wording"}}},
	})
	if err != nil {
		t.Fatalf("NewLogicalNode: %v", err)
	}
	facts := node.FactSpecs()
	if len(facts) != 1 || facts[0].Name != "party-size" || facts[0].Description != "number of players in the current match" {
		t.Fatalf("FactSpecs=%#v", facts)
	}
	facts[0].Name = "mutated"
	facts[0].Description = "mutated"
	again := node.FactSpecs()
	if again[0].Name != "party-size" || again[0].Description != "number of players in the current match" {
		t.Fatalf("FactSpecs exposed internal metadata: %#v", again)
	}
}
