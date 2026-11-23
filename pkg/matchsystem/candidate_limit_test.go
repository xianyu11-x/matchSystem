package matchsystem

import (
	"fmt"
	"testing"

	"github.com/xianyu11-x/matchSystem/pkg/identity"
)

func TestNewLogicalNodeCandidateLimit(t *testing.T) {
	for _, test := range []struct {
		limit       int
		placementID identity.PlacementID
	}{{17, 3416516343361491725}, {128, 10355968373794145285}} {
		limit := test.limit
		t.Run(fmt.Sprintf("limit-%d", limit), func(t *testing.T) {
			key := identity.LogicalNodeKey{
				Rule:        identity.RuleKey{Namespace: "2984792146672357749", RuleID: uint64(limit)},
				PlacementID: test.placementID,
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
			if got := cap(node.candidateRankingScratch); got != candidateRankingScratchCapacity {
				t.Fatalf("candidate ranking scratch capacity: got %d, want %d", got, candidateRankingScratchCapacity)
			}
		})
	}
}

func TestNewLogicalNodeCandidateScoringLimit(t *testing.T) {
	key := identity.LogicalNodeKey{
		Rule:        identity.RuleKey{Namespace: "3178116681667369515", RuleID: 1},
		PlacementID: 17786948603876689393,
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
		}`, logicalNodeConfig{
			CandidateScoringLimitPerSeed: 37,
			CandidateLimitPerSeed:        11,
		}),
	})
	if err != nil {
		t.Fatalf("create LogicalNode: %v", err)
	}
	if got := node.evaluator.candidateScoringLimit; got != 37 {
		t.Fatalf("candidate scoring limit: got %d, want 37", got)
	}
	if got := node.evaluator.candidateLimit; got != 11 {
		t.Fatalf("candidate limit: got %d, want 11", got)
	}
}
