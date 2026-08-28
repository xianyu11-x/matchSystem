package matchsystem

import (
	"testing"

	"matchSystem/internal/identity"
)

func TestNewLogicalNodeCandidateLimit(t *testing.T) {
	tests := []struct {
		name  string
		input int
		want  int
	}{
		{name: "default", input: 0, want: 128},
		{name: "negative uses default", input: -1, want: 128},
		{name: "explicit", input: 17, want: 17},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			node, err := NewLogicalNode(LogicalNodeSpec{
				Key: identity.LogicalNodeKey{
					Rule:        identity.RuleKey{Namespace: "test-candidate-limit", RuleID: 1},
					PlacementID: identity.PlacementID(test.name),
				},
				ContractJSON: []byte(`{
					"schemaVersion":"logical-node-contract/v3",
					"attributes":[],"facts":[],"indexes":[]
				}`),
				PrefilterJSON: []byte(`{
					"schemaVersion":"prefilter/v3",
					"bitmap":{"resultType":"bitmap","expr":{"op":"none"}}
				}`),
				EvaluationJSON: []byte(`{
					"schemaVersion":"evaluation/v3",
					"canJoin":{"schemaVersion":"expression-scalar/v3","resultType":"bool","expr":{"op":"bool_literal","value":true}},
					"canComplete":{"schemaVersion":"expression-scalar/v3","resultType":"bool","expr":{"op":"bool_literal","value":true}}
				}`),
				Config: LogicalNodeConfig{
					CandidateLimitPerSeed: test.input,
				},
				CandidateScorer: func(CandidateScoreContext) (float64, error) {
					return 0, nil
				},
			})
			if err != nil {
				t.Fatalf("create LogicalNode: %v", err)
			}
			if got := node.evaluator.candidateLimit; got != test.want {
				t.Fatalf("candidate limit: got %d, want %d", got, test.want)
			}
		})
	}
}
