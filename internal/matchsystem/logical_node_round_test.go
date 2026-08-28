package matchsystem

import "testing"

func TestRoundDocIDQuarantineUntilNextRound(t *testing.T) {
	store := newTestTicketStore(t)
	node := &LogicalNode{
		config: LogicalNodeConfig{SeedScheduler: SeedSchedulerConfig{
			AttemptLimitPerMatchRound: 8,
		}},
		store:           store,
		seedOrderPolicy: arrivalSeedOrderPolicy{},
	}

	oldDocID, err := store.Add(testTicket(1))
	if err != nil {
		t.Fatalf("add old seed: %v", err)
	}
	if err := node.BeginMatchRound(100); err != nil {
		t.Fatalf("begin first round: %v", err)
	}
	if !store.Remove(1) {
		t.Fatal("remove old seed: not found")
	}

	newDocID, err := store.Add(testTicket(2))
	if err != nil {
		t.Fatalf("add same-round ticket: %v", err)
	}
	if newDocID == oldDocID {
		t.Fatalf("same-round Add reused quarantined DocID %d", oldDocID)
	}
	if got := node.nextSeed(); got != nil {
		t.Fatalf("same-round ticket became selectable: TicketID=%d", got.TicketID)
	}

	if err := node.BeginMatchRound(200); err != nil {
		t.Fatalf("begin second round: %v", err)
	}
	seed := node.nextSeed()
	if seed == nil || seed.TicketID != 2 {
		t.Fatalf("next round did not expose new ticket: %#v", seed)
	}

	// The old ID is now released by installing the second round and is safe for
	// a later Add. The new Ticket still cannot enter the already-installed order.
	reusedDocID, err := store.Add(testTicket(3))
	if err != nil {
		t.Fatalf("add after next round: %v", err)
	}
	if reusedDocID != oldDocID {
		t.Fatalf("released DocID was not reusable: got %d, want %d", reusedDocID, oldDocID)
	}
}
