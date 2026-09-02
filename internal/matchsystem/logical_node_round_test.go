package matchsystem

import (
	"container/list"
	"testing"
)

func TestRoundTicketIDSnapshotSkipsRemovedTicket(t *testing.T) {
	store := newTestTicketStore(t)
	node := &LogicalNode{
		config: logicalNodeConfig{SeedScheduler: seedSchedulerConfig{
			AttemptLimitPerMatchRound: 8,
		}},
		store: store,
		seedOrderRuntime: &arrivalSeedOrderPolicy{
			entries: list.New(),
			active:  make(map[TicketID]*list.Element),
		},
	}
	if err := node.Add(testTicket(1)); err != nil {
		t.Fatalf("add first seed: %v", err)
	}
	if err := node.Add(testTicket(2)); err != nil {
		t.Fatalf("add second seed: %v", err)
	}
	if err := node.BeginMatchRound(100); err != nil {
		t.Fatalf("begin round: %v", err)
	}
	if got, want := node.seedRound.order, []TicketID{1, 2}; !equalSeedIDs(got, want) {
		t.Fatalf("round snapshot: got %v, want %v", got, want)
	}
	snapshot := append([]TicketID(nil), node.seedRound.order...)
	if !node.Remove(1) {
		t.Fatal("remove snapshot seed: not found")
	}
	if !equalSeedIDs(node.seedRound.order, snapshot) {
		t.Fatalf("removing a ticket mutated the installed round snapshot: got %v, want %v", node.seedRound.order, snapshot)
	}

	seed := node.nextSeed()
	if seed == nil || seed.TicketID != 2 {
		t.Fatalf("nextSeed did not skip removed TicketID: %#v", seed)
	}
	if seed := node.nextSeed(); seed != nil {
		t.Fatalf("round returned more seeds after snapshot exhaustion: %d", seed.TicketID)
	}
}

func TestBeginMatchRoundBuildsSnapshotUpToAttemptLimit(t *testing.T) {
	store := newTestTicketStore(t)
	node := &LogicalNode{
		config: logicalNodeConfig{SeedScheduler: seedSchedulerConfig{
			AttemptLimitPerMatchRound: 3,
		}},
		store: store,
		seedOrderRuntime: &arrivalSeedOrderPolicy{
			entries: list.New(),
			active:  make(map[TicketID]*list.Element),
		},
	}
	for id := TicketID(1); id <= 5; id++ {
		if err := node.Add(testTicket(id)); err != nil {
			t.Fatalf("add ticket %d: %v", id, err)
		}
	}
	if err := node.BeginMatchRound(100); err != nil {
		t.Fatalf("begin match round: %v", err)
	}
	if got, want := node.seedRound.order, []TicketID{1, 2, 3}; !equalSeedIDs(got, want) {
		t.Fatalf("round snapshot: got %v, want first %d seeds %v", got, 3, want)
	}
	node.config.SeedScheduler.AttemptLimitPerMatchRound = 8
	if err := node.BeginMatchRound(101); err != nil {
		t.Fatalf("begin match round with limit above pool size: %v", err)
	}
	if got, want := node.seedRound.order, []TicketID{1, 2, 3, 4, 5}; !equalSeedIDs(got, want) {
		t.Fatalf("round snapshot above pool size: got %v, want all %d tickets %v", got, 5, want)
	}
}

func equalSeedIDs(left, right []TicketID) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
