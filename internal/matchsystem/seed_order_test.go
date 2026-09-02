package matchsystem

import (
	"reflect"
	"testing"
)

func TestArrivalSeedOrderRuntimeIsBoundedAndReAddGetsNewEntry(t *testing.T) {
	runtime, err := NewSeedOrderPolicy(SeedOrderPolicyConfig{Kind: SeedOrderArrival})
	if err != nil {
		t.Fatalf("create arrival runtime: %v", err)
	}
	runtime.Add(&Ticket{TicketID: 1})
	runtime.Add(&Ticket{TicketID: 2})
	runtime.Add(&Ticket{TicketID: 3})

	if got, want := buildSeedIDs(t, runtime, 2), []TicketID{1, 2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("bounded arrival order: got %v, want %v", got, want)
	}

	runtime.Remove(1)
	runtime.Add(&Ticket{TicketID: 1})
	if got, want := buildSeedIDs(t, runtime, 3), []TicketID{2, 3, 1}; !reflect.DeepEqual(got, want) {
		t.Fatalf("arrival Remove+Add order: got %v, want %v", got, want)
	}
}

func TestArrivalSeedOrderRuntimeChurnPhysicallyRemovesEntries(t *testing.T) {
	runtime, err := NewSeedOrderPolicy(SeedOrderPolicyConfig{Kind: SeedOrderArrival})
	if err != nil {
		t.Fatalf("create arrival runtime: %v", err)
	}
	policy := runtime.(*arrivalSeedOrderPolicy)
	for id := TicketID(1); id <= 10000; id++ {
		runtime.Add(&Ticket{TicketID: id})
	}
	for id := TicketID(1); id <= 100; id++ {
		runtime.Remove(id)
	}
	if got, want := policy.entries.Len(), len(policy.active); got != want {
		t.Fatalf("arrival Remove retained historical entries: entries=%d active=%d", got, want)
	}
	if got, want := buildSeedIDs(t, runtime, 1), []TicketID{101}; !reflect.DeepEqual(got, want) {
		t.Fatalf("arrival churn returned wrong first live ID: got %v, want %v", got, want)
	}
	entries := policy.entries.Len()
	if got, want := buildSeedIDs(t, runtime, 1), []TicketID{101}; !reflect.DeepEqual(got, want) {
		t.Fatalf("arrival repeated bounded build: got %v, want %v", got, want)
	}
	if policy.entries.Len() != entries {
		t.Fatalf("arrival BuildRound changed active index length: before=%d after=%d", entries, policy.entries.Len())
	}

	for id := TicketID(101); id <= 10000; id++ {
		runtime.Remove(id)
	}
	if policy.entries.Len() != 0 || len(policy.active) != 0 {
		t.Fatalf("arrival churn retained historical entries: entries=%d active=%d", policy.entries.Len(), len(policy.active))
	}
}

func TestOldestSeedOrderRuntimeRemovesAndReAddsHeapEntry(t *testing.T) {
	runtime, err := NewSeedOrderPolicy(SeedOrderPolicyConfig{Kind: SeedOrderOldest})
	if err != nil {
		t.Fatalf("create oldest runtime: %v", err)
	}
	policy := runtime.(*oldestSeedOrderPolicy)
	runtime.Add(&Ticket{TicketID: 1, CreatedAt: 30})
	runtime.Add(&Ticket{TicketID: 2, CreatedAt: 10})
	runtime.Add(&Ticket{TicketID: 3, CreatedAt: 20})
	if got, want := policy.entries.Len(), 3; got != want {
		t.Fatalf("oldest heap length after Add: got %d, want %d", got, want)
	}
	if got, want := buildSeedIDs(t, runtime, 2), []TicketID{2, 3}; !reflect.DeepEqual(got, want) {
		t.Fatalf("bounded oldest order: got %v, want %v", got, want)
	}

	runtime.Remove(2)
	if got, want := policy.entries.Len(), 2; got != want {
		t.Fatalf("oldest heap retained removed entry: got %d, want %d", got, want)
	}
	runtime.Add(&Ticket{TicketID: 2, CreatedAt: 5})
	if got, want := policy.entries.Len(), 3; got != want {
		t.Fatalf("oldest heap length after re-add: got %d, want %d", got, want)
	}
	if got, want := buildSeedIDs(t, runtime, 3), []TicketID{2, 3, 1}; !reflect.DeepEqual(got, want) {
		t.Fatalf("oldest Remove+Add order: got %v, want %v", got, want)
	}
}

func TestOldestSeedOrderRuntimeArbitraryPositionRemovalsKeepHeapBounded(t *testing.T) {
	runtime, err := NewSeedOrderPolicy(SeedOrderPolicyConfig{Kind: SeedOrderOldest})
	if err != nil {
		t.Fatalf("create oldest runtime: %v", err)
	}
	policy := runtime.(*oldestSeedOrderPolicy)
	const count = 2048
	for id := TicketID(1); id <= count; id++ {
		runtime.Add(&Ticket{TicketID: id, CreatedAt: int64(id % 31)})
	}

	// The permutation removes leaves, internal nodes, and the root in an order
	// unrelated to heap order. Each removal must physically unlink one entry.
	for step := 0; step < count; step++ {
		id := TicketID((step*37)%count + 1)
		runtime.Remove(id)
		if got, want := policy.entries.Len(), len(policy.active); got != want {
			t.Fatalf("oldest heap grew a stale entry at step %d: entries=%d active=%d", step, got, want)
		}
		assertOldestHeapIndexes(t, policy)
	}
	if policy.entries.Len() != 0 || len(policy.active) != 0 {
		t.Fatalf("oldest heap retained entries after arbitrary removals: entries=%d active=%d", policy.entries.Len(), len(policy.active))
	}

	// Reusing every TicketID must create a fresh bounded entry, not revive any
	// removed heap node.
	for id := TicketID(1); id <= count; id++ {
		runtime.Add(&Ticket{TicketID: id, CreatedAt: int64(count - id)})
	}
	if policy.entries.Len() != count || len(policy.active) != count {
		t.Fatalf("oldest heap capacity after re-add: entries=%d active=%d", policy.entries.Len(), len(policy.active))
	}
	assertOldestHeapIndexes(t, policy)
}

func TestPrioritySeedOrderRuntimeCopiesConfiguredValueAndRemovesReAddedEntry(t *testing.T) {
	runtime, err := NewSeedOrderPolicy(SeedOrderPolicyConfig{
		Kind:              SeedOrderInt64Priority,
		PriorityField:     "priority",
		PriorityDirection: SeedPriorityDescending,
	})
	if err != nil {
		t.Fatalf("create priority runtime: %v", err)
	}
	policy := runtime.(*int64PrioritySeedOrderPolicy)
	ticket := &Ticket{TicketID: 1, Int64Values: map[string]int64{"priority": 1}}
	runtime.Add(ticket)
	runtime.Add(&Ticket{TicketID: 2, Int64Values: map[string]int64{"priority": 9}})
	runtime.Add(&Ticket{TicketID: 3})
	if got, want := policy.entries.Len(), 3; got != want {
		t.Fatalf("priority heap length after Add: got %d, want %d", got, want)
	}

	// Add copies the configured scalar; mutating the caller's Ticket after the
	// lifecycle event cannot change the persistent policy index.
	ticket.Int64Values["priority"] = 100
	if got, want := buildSeedIDs(t, runtime, 3), []TicketID{2, 1, 3}; !reflect.DeepEqual(got, want) {
		t.Fatalf("priority order retained mutable Ticket state: got %v, want %v", got, want)
	}

	runtime.Remove(2)
	if got, want := policy.entries.Len(), 2; got != want {
		t.Fatalf("priority heap retained removed entry: got %d, want %d", got, want)
	}
	runtime.Add(&Ticket{TicketID: 2, Int64Values: map[string]int64{"priority": 10}})
	if got, want := policy.entries.Len(), 3; got != want {
		t.Fatalf("priority heap length after re-add: got %d, want %d", got, want)
	}
	if got, want := buildSeedIDs(t, runtime, 3), []TicketID{2, 1, 3}; !reflect.DeepEqual(got, want) {
		t.Fatalf("priority Remove+Add order: got %v, want %v", got, want)
	}
}

func TestPrioritySeedOrderRuntimeArbitraryPositionRemovalsKeepHeapBounded(t *testing.T) {
	runtime, err := NewSeedOrderPolicy(SeedOrderPolicyConfig{
		Kind:              SeedOrderInt64Priority,
		PriorityField:     "priority",
		PriorityDirection: SeedPriorityAscending,
	})
	if err != nil {
		t.Fatalf("create priority runtime: %v", err)
	}
	policy := runtime.(*int64PrioritySeedOrderPolicy)
	const count = 2048
	for id := TicketID(1); id <= count; id++ {
		runtime.Add(&Ticket{
			TicketID:    id,
			Int64Values: map[string]int64{"priority": int64(id % 47)},
		})
	}
	for step := 0; step < count; step++ {
		id := TicketID((step*37)%count + 1)
		runtime.Remove(id)
		if got, want := policy.entries.Len(), len(policy.active); got != want {
			t.Fatalf("priority heap grew a stale entry at step %d: entries=%d active=%d", step, got, want)
		}
		assertPriorityHeapIndexes(t, policy)
	}
	if policy.entries.Len() != 0 || len(policy.active) != 0 {
		t.Fatalf("priority heap retained entries after arbitrary removals: entries=%d active=%d", policy.entries.Len(), len(policy.active))
	}
}

func TestRandomSeedOrderRuntimeUsesDenseSwapRemove(t *testing.T) {
	runtime, err := NewSeedOrderPolicy(SeedOrderPolicyConfig{Kind: SeedOrderRandom, RandomSeed: 7})
	if err != nil {
		t.Fatalf("create random runtime: %v", err)
	}
	policy := runtime.(*randomSeedOrderPolicy)
	for id := TicketID(1); id <= 4; id++ {
		runtime.Add(&Ticket{TicketID: id})
	}
	if got := buildSeedIDs(t, runtime, 2); len(got) != 2 || !uniqueSeedIDs(got) {
		t.Fatalf("bounded random order: %v", got)
	}

	runtime.Remove(2)
	if got, want := len(policy.ticketIDs), len(policy.positions); got != want {
		t.Fatalf("random dense index retained a removed entry: entries=%d active=%d", got, want)
	}
	if got := buildSeedIDs(t, runtime, 4); len(got) != 3 || !uniqueSeedIDs(got) || containsSeedID(got, 2) {
		t.Fatalf("random swap-remove left stale TicketID: %v", got)
	}
	runtime.Add(&Ticket{TicketID: 2})
	if got, want := len(policy.ticketIDs), len(policy.positions); got != want {
		t.Fatalf("random dense index did not restore one live entry: entries=%d active=%d", got, want)
	}
	if got := buildSeedIDs(t, runtime, 4); len(got) != 4 || !uniqueSeedIDs(got) {
		t.Fatalf("random re-add did not restore one live entry: %v", got)
	}
}

func TestRandomSeedOrderRuntimeIsDeterministicForSameSeedAndLifecycle(t *testing.T) {
	newRuntime := func() SeedOrderRuntime {
		runtime, err := NewSeedOrderPolicy(SeedOrderPolicyConfig{Kind: SeedOrderRandom, RandomSeed: 19})
		if err != nil {
			t.Fatalf("create random runtime: %v", err)
		}
		for id := TicketID(1); id <= 20; id++ {
			runtime.Add(&Ticket{TicketID: id})
		}
		runtime.Remove(7)
		runtime.Add(&Ticket{TicketID: 7})
		return runtime
	}
	left, right := newRuntime(), newRuntime()
	for round := 0; round < 5; round++ {
		leftOrder := buildSeedIDs(t, left, 6)
		rightOrder := buildSeedIDs(t, right, 6)
		if !reflect.DeepEqual(leftOrder, rightOrder) {
			t.Fatalf("same random seed/lifecycle diverged at round %d: left=%v right=%v", round, leftOrder, rightOrder)
		}
	}
}

func TestSeedOrderRuntimeLongChurnKeepsIndexSizeBounded(t *testing.T) {
	configs := []SeedOrderPolicyConfig{
		{Kind: SeedOrderArrival},
		{Kind: SeedOrderOldest},
		{Kind: SeedOrderInt64Priority, PriorityField: "priority"},
		{Kind: SeedOrderRandom, RandomSeed: 23},
	}
	for _, config := range configs {
		runtime, err := NewSeedOrderPolicy(config)
		if err != nil {
			t.Fatalf("create %q runtime: %v", config.Kind, err)
		}
		for cycle := 0; cycle < 10000; cycle++ {
			id := TicketID(cycle%64 + 1)
			runtime.Add(&Ticket{
				TicketID:    id,
				CreatedAt:   int64(cycle),
				Int64Values: map[string]int64{"priority": int64(cycle)},
			})
			runtime.Remove(id)
			active, entries := seedRuntimeIndexSizes(runtime)
			if active != 0 || entries != 0 {
				t.Fatalf("%q retained state after churn cycle %d: entries=%d active=%d", config.Kind, cycle, entries, active)
			}
		}
	}
}

func buildSeedIDs(t *testing.T, runtime SeedOrderRuntime, limit int) []TicketID {
	t.Helper()
	order, err := runtime.BuildRound(limit)
	if err != nil {
		t.Fatalf("build seed round: %v", err)
	}
	return order
}

func uniqueSeedIDs(ids []TicketID) bool {
	seen := make(map[TicketID]struct{}, len(ids))
	for _, id := range ids {
		if _, exists := seen[id]; exists {
			return false
		}
		seen[id] = struct{}{}
	}
	return true
}

func containsSeedID(ids []TicketID, want TicketID) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

func assertOldestHeapIndexes(t *testing.T, policy *oldestSeedOrderPolicy) {
	t.Helper()
	for index, entry := range policy.entries {
		if entry == nil || entry.heapIndex != index {
			t.Fatalf("oldest heap index mismatch at %d: %#v", index, entry)
		}
	}
}

func assertPriorityHeapIndexes(t *testing.T, policy *int64PrioritySeedOrderPolicy) {
	t.Helper()
	for index, entry := range policy.entries.entries {
		if entry == nil || entry.heapIndex != index {
			t.Fatalf("priority heap index mismatch at %d: %#v", index, entry)
		}
	}
}

func seedRuntimeIndexSizes(runtime SeedOrderRuntime) (active, entries int) {
	switch policy := runtime.(type) {
	case *arrivalSeedOrderPolicy:
		if policy.entries != nil {
			entries = policy.entries.Len()
		}
		return len(policy.active), entries
	case *oldestSeedOrderPolicy:
		return len(policy.active), policy.entries.Len()
	case *int64PrioritySeedOrderPolicy:
		return len(policy.active), policy.entries.Len()
	case *randomSeedOrderPolicy:
		return len(policy.positions), len(policy.ticketIDs)
	default:
		return 0, 0
	}
}
