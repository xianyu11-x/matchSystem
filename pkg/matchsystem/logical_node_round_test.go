package matchsystem

import (
	"container/list"
	"testing"
)

func TestLogicalNodeNextSeedSkipsRemovedTicketAndDoesNotSpendBudget(t *testing.T) {
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
	for id := TicketID(1); id <= 3; id++ {
		if err := node.Add(testTicket(id)); err != nil {
			t.Fatalf("add Ticket %d: %v", id, err)
		}
	}
	if err := node.BeginMatchRound(100); err != nil {
		t.Fatalf("begin round: %v", err)
	}

	if seed := node.nextSeed(); seed == nil || seed.TicketID != 1 {
		t.Fatalf("first nextSeed=%#v, want TicketID 1", seed)
	}
	if !node.Remove(2) {
		t.Fatal("remove future seed: not found")
	}
	if seed := node.nextSeed(); seed == nil || seed.TicketID != 3 {
		t.Fatalf("nextSeed did not skip removed TicketID: %#v", seed)
	}
	if got, want := node.seedRound.attemptedSeeds, 2; got != want {
		t.Fatalf("attempted seed count=%d, want %d; stale removal spent budget", got, want)
	}
}

func TestLogicalNodeFailedSeedIsNotRepeatedUntilNextRound(t *testing.T) {
	store := newTestTicketStore(t)
	node := &LogicalNode{
		config: logicalNodeConfig{SeedScheduler: seedSchedulerConfig{
			AttemptLimitPerMatchRound: 2,
		}},
		store: store,
		seedOrderRuntime: &arrivalSeedOrderPolicy{
			entries: list.New(),
			active:  make(map[TicketID]*list.Element),
		},
	}
	for id := TicketID(1); id <= 2; id++ {
		if err := node.Add(testTicket(id)); err != nil {
			t.Fatalf("add Ticket %d: %v", id, err)
		}
	}
	if err := node.BeginMatchRound(100); err != nil {
		t.Fatalf("begin first round: %v", err)
	}
	if seed := node.nextSeed(); seed == nil || seed.TicketID != 1 {
		t.Fatalf("first round seed=%#v, want TicketID 1", seed)
	}
	if seed := node.nextSeed(); seed == nil || seed.TicketID != 2 {
		t.Fatalf("second round seed=%#v, want TicketID 2", seed)
	}
	if seed := node.nextSeed(); seed != nil {
		t.Fatalf("round budget returned seed after exhaustion: %#v", seed)
	}

	if err := node.BeginMatchRound(101); err != nil {
		t.Fatalf("begin second round: %v", err)
	}
	if got, want := node.seedRound.attemptedSeeds, 0; got != want {
		t.Fatalf("new round attempted count=%d, want %d", got, want)
	}
	if seed := node.nextSeed(); seed == nil || seed.TicketID != 1 {
		t.Fatalf("failed seed was not reusable next round: %#v", seed)
	}
}

func TestLogicalNodeBeginMatchRoundStoresOnlyGenericState(t *testing.T) {
	store := newTestTicketStore(t)
	runtime, err := NewSeedOrderPolicy(SeedOrderPolicyConfig{Kind: SeedOrderArrival})
	if err != nil {
		t.Fatalf("create seed runtime: %v", err)
	}
	node := &LogicalNode{
		config: logicalNodeConfig{SeedScheduler: seedSchedulerConfig{
			AttemptLimitPerMatchRound: 500,
		}},
		store:            store,
		seedOrderRuntime: runtime,
	}
	if err := node.Add(testTicket(1)); err != nil {
		t.Fatalf("add Ticket: %v", err)
	}
	if err := node.BeginMatchRound(123); err != nil {
		t.Fatalf("begin round: %v", err)
	}
	if !node.seedRound.initialized || node.seedRound.now != 123 || node.seedRound.attemptedSeeds != 0 {
		t.Fatalf("unexpected generic round state: %+v", node.seedRound)
	}
	if seed := node.nextSeed(); seed == nil || seed.TicketID != 1 {
		t.Fatalf("stream did not provide seed: %#v", seed)
	}
}
