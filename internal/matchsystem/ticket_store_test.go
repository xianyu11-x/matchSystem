package matchsystem

import (
	"testing"

	"matchSystem/internal/common"
	"matchSystem/internal/matchsystem/contract"
	"matchSystem/internal/matchsystem/fact"
	"matchSystem/internal/matchsystem/prefilter"
)

func newTestTicketStore(t *testing.T) *ticketStore {
	t.Helper()
	schema, err := contract.Parse([]byte(`{
		"schemaVersion":"logical-node-contract/v3",
		"attributes":[],
		"facts":[],
		"indexes":[]
	}`), contract.DefaultLimits())
	if err != nil {
		t.Fatalf("parse test contract: %v", err)
	}
	plan, err := prefilter.CompileJSON([]byte(`{
		"schemaVersion":"prefilter/v3",
		"bitmap":{"resultType":"bitmap","expr":{"op":"none"}}
	}`), schema)
	if err != nil {
		t.Fatalf("compile test prefilter: %v", err)
	}
	indexStore, err := prefilter.New(plan)
	if err != nil {
		t.Fatalf("create test prefilter store: %v", err)
	}
	return newTicketStore(indexStore)
}

func testTicket(id uint64) *Ticket {
	return &Ticket{TicketID: id, CreatedAt: int64(id)}
}

func TestTicketStoreCommitPreflightIsAtomic(t *testing.T) {
	store := newTestTicketStore(t)
	firstDoc, err := store.Add(testTicket(1))
	if err != nil {
		t.Fatalf("add first ticket: %v", err)
	}
	secondDoc, err := store.Add(testTicket(2))
	if err != nil {
		t.Fatalf("add second ticket: %v", err)
	}
	first, ok := store.lookupDocID(firstDoc)
	if !ok {
		t.Fatal("first ticket is missing")
	}
	second, ok := store.lookupDocID(secondDoc)
	if !ok {
		t.Fatal("second ticket is missing")
	}

	// The second pointer is not store-owned. Commit must reject the whole
	// Match before removing the valid first Ticket.
	bad := &Match{Tickets: []*Ticket{first.Ticket, common.CloneTicket(second.Ticket)}}
	if err := store.Commit(bad); err == nil {
		t.Fatal("expected ownership preflight error")
	}
	if got := store.Len(); got != 2 {
		t.Fatalf("atomic preflight removed tickets: got Len=%d", got)
	}
	if got := store.prefilterStore.Len(); got != 2 {
		t.Fatalf("atomic preflight removed index entries: got Len=%d", got)
	}
	if _, ok := store.lookupDocID(firstDoc); !ok {
		t.Fatal("first ticket was removed after failed preflight")
	}
	if _, ok := store.lookupDocID(secondDoc); !ok {
		t.Fatal("second ticket was removed after failed preflight")
	}

	good := &Match{Tickets: []*Ticket{first.Ticket, second.Ticket}}
	if err := store.Commit(good); err != nil {
		t.Fatalf("commit owned Match: %v", err)
	}
	if got := store.Len(); got != 0 {
		t.Fatalf("committed tickets remain active: got Len=%d", got)
	}
}

func TestTicketStoreDoesNotCreateObjectSlotWithoutObjectFacts(t *testing.T) {
	store := newTestTicketStore(t)
	docID, err := store.Add(testTicket(9))
	if err != nil {
		t.Fatalf("add Ticket: %v", err)
	}
	stored, ok := store.lookupDocID(docID)
	if !ok {
		t.Fatal("Ticket is missing")
	}
	if stored.objectFacts != nil {
		t.Fatal("rule without Object Facts allocated a per-Ticket ObjectSlot")
	}
}

func TestTicketStoreObjectSlotInvalidatesOnRemoveAndReadd(t *testing.T) {
	store := newTestTicketStore(t)
	layout, err := fact.NewObjectLayout([]fact.Spec{{Name: "label", Type: fact.TypeStrings, MaxValues: 1, Scope: fact.ScopeObject}})
	if err != nil {
		t.Fatalf("compile Object layout: %v", err)
	}
	store.objectLayout = layout
	docID, err := store.Add(testTicket(7))
	if err != nil {
		t.Fatalf("add first Ticket: %v", err)
	}
	first, ok := store.lookupDocID(docID)
	if !ok {
		t.Fatal("first Ticket is missing")
	}
	frame := fact.NewFrame(fact.Values{}, 1, false)
	if _, _, err := frame.Object(first.objectFacts, first.Ticket, 0, func(_ *common.Ticket, _ int64, _ fact.Values, out fact.Writer) error {
		return out.SetStrings("label", []string{"old"})
	}); err != nil {
		t.Fatalf("materialize first Object Facts: %v", err)
	}
	if !store.Remove(7) {
		t.Fatal("remove first Ticket failed")
	}
	if first.objectFacts.State() != fact.ObjectSlotUnseen {
		t.Fatalf("removed slot state=%v, want unseen", first.objectFacts.State())
	}
	newDocID, err := store.Add(testTicket(7))
	if err != nil {
		t.Fatalf("re-add Ticket: %v", err)
	}
	second, ok := store.lookupDocID(newDocID)
	if !ok || second == first {
		t.Fatal("re-added Ticket did not receive a fresh store entry")
	}
	if values, ok := second.objectFacts.ValuesFor(1); ok || values.StringLists != nil {
		t.Fatalf("re-added Ticket inherited old Object Facts: %#v ok=%v", values, ok)
	}
}
