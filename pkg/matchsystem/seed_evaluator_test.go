package matchsystem

import (
	"context"
	"testing"
)

type testMatchFactProvider struct {
	values Facts
}

func (p testMatchFactProvider) Initialize(context.Context, InitializeInput) (Facts, error) {
	return p.values, nil
}

func (p testMatchFactProvider) OnJoin(context.Context, JoinInput) (Facts, error) {
	return p.values, nil
}

func TestSeedEvaluatorOwnsTrustedMatchFactsWithoutRuntimeValidation(t *testing.T) {
	providerValues := Facts{
		// This is intentionally not a valid Match Fact contract snapshot. A
		// provider contract test would validate it with fact.Validator; the
		// evaluator only owns the result and preserves provider error handling.
		StringLists: map[string][]string{"match-count": {"not-an-int64"}},
	}
	evaluator := &seedEvaluator{matchFacts: testMatchFactProvider{values: providerValues}}
	got, err := evaluator.initializeMatchFacts(context.Background(), 123, &Ticket{TicketID: 1}, Facts{}, Facts{})
	if err != nil {
		t.Fatalf("trusted Match Fact provider was rejected: %v", err)
	}
	providerValues.StringLists["match-count"][0] = "provider-mutated"
	if got.StringLists["match-count"][0] != "not-an-int64" {
		t.Fatal("seed evaluator Match Facts alias provider result")
	}

	joined, err := evaluator.onJoinMatchFacts(context.Background(), 123, &Ticket{TicketID: 1}, Facts{}, Facts{}, &Ticket{TicketID: 2}, Facts{}, got)
	if err != nil {
		t.Fatalf("trusted Match Fact OnJoin provider was rejected: %v", err)
	}
	if joined.StringLists["match-count"][0] != "provider-mutated" {
		t.Fatalf("unexpected OnJoin Match Facts: %#v", joined)
	}
}
