package prefilter

import (
	"testing"

	"matchSystem/internal/common"
	"matchSystem/internal/matchsystem/contract"
	"matchSystem/internal/matchsystem/fact"
)

func TestTickSessionDoesNotRevalidateTrustedFactSnapshots(t *testing.T) {
	schema := contract.Contract{
		Facts: []contract.FactSpec{
			{Name: "tick-count", Type: fact.TypeInt64, Scope: fact.ScopeTick},
			{Name: "seed-count", Type: fact.TypeInt64, Scope: fact.ScopeObject},
		},
	}
	plan, err := CompileJSON([]byte(`{
		"schemaVersion":"prefilter/v3",
		"bitmap":{"resultType":"bitmap","expr":{"op":"none"}}
	}`), schema)
	if err != nil {
		t.Fatalf("compile prefilter: %v", err)
	}
	store, err := New(plan)
	if err != nil {
		t.Fatalf("create prefilter store: %v", err)
	}
	ticket := &common.Ticket{TicketID: 1}
	if err := store.Add(1, ticket); err != nil {
		t.Fatalf("add ticket: %v", err)
	}

	// The values intentionally use the wrong typed map. Provider contract
	// tests own this check; BeginTick and Candidates only consume trusted
	// snapshots in the production path.
	badTick := Facts{StringLists: map[string][]string{"tick-count": {"not-an-int64"}}}
	session, err := store.BeginTick(badTick)
	if err != nil {
		t.Fatalf("BeginTick revalidated trusted Tick Facts: %v", err)
	}
	badSeed := Facts{StringLists: map[string][]string{"seed-count": {"not-an-int64"}}}
	if _, err := session.Candidates(1, ticket, badSeed); err != nil {
		t.Fatalf("Candidates revalidated trusted Object Facts: %v", err)
	}
}
