package evaluation

import (
	"testing"

	"matchSystem/internal/matchsystem/contract"
	"matchSystem/internal/matchsystem/fact"
)

func TestPredicatesDoNotRevalidateTrustedFactSnapshots(t *testing.T) {
	schema := contract.Contract{
		Facts: []contract.FactSpec{{Name: "match-count", Type: fact.TypeInt64, Scope: fact.ScopeMatch}},
	}
	predicates, err := CompileJSON([]byte(`{
		"schemaVersion":"evaluation/v3",
		"canJoin":{"schemaVersion":"expression-scalar/v3","resultType":"bool","expr":{"op":"bool_literal","value":true}},
		"canComplete":{"schemaVersion":"expression-scalar/v3","resultType":"bool","expr":{"op":"bool_literal","value":true}}
	}`), schema)
	if err != nil {
		t.Fatalf("compile predicates: %v", err)
	}

	// This snapshot has the right name in the wrong typed map. A provider
	// contract test would reject it through fact.Validator, but the production
	// evaluator must not repeat that check before evaluating a predicate.
	malformed := fact.Values{StringLists: map[string][]string{"match-count": {"not-an-int64"}}}
	if result, err := predicates.CanComplete(CanCompleteInput{MatchFacts: malformed}); err != nil || !result {
		t.Fatalf("CanComplete revalidated trusted Facts: result=%v err=%v", result, err)
	}
	if result, err := predicates.CanJoin(CanJoinInput{MatchFactsBefore: malformed}); err != nil || !result {
		t.Fatalf("CanJoin revalidated trusted Facts: result=%v err=%v", result, err)
	}
}
