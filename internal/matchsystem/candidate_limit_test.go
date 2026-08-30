package matchsystem

import (
	"fmt"
	"testing"

	"matchSystem/internal/identity"
)

func TestNewLogicalNodeCandidateLimit(t *testing.T) {
	for _, limit := range []int{17, 128} {
		t.Run(fmt.Sprintf("limit-%d", limit), func(t *testing.T) {
			key := identity.LogicalNodeKey{
				Rule:        identity.RuleKey{Namespace: "test-candidate-limit", RuleID: int32(limit)},
				PlacementID: identity.PlacementID(fmt.Sprintf("limit-%d", limit)),
			}
			node, err := NewLogicalNode(LogicalNodeSpec{
				Key: key,
				RuleJSON: testRuleJSON(t, key.Rule, `{
					"schemaVersion":"logical-node-contract/v3",
					"attributes":[],"facts":[],"indexes":[]
				}`, `{
					"schemaVersion":"prefilter/v3",
					"bitmap":{"resultType":"bitmap","expr":{"op":"none"}}
				}`, `{
					"schemaVersion":"evaluation/v3",
					"canJoin":{"schemaVersion":"expression-scalar/v3","resultType":"bool","expr":{"op":"bool_literal","value":true}},
					"canComplete":{"schemaVersion":"expression-scalar/v3","resultType":"bool","expr":{"op":"bool_literal","value":true}}
				}`, logicalNodeConfig{CandidateLimitPerSeed: limit}),
			})
			if err != nil {
				t.Fatalf("create LogicalNode: %v", err)
			}
			if got := node.evaluator.candidateLimit; got != limit {
				t.Fatalf("candidate limit: got %d, want %d", got, limit)
			}
		})
	}
}
