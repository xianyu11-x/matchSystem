package matchsystem

import (
	"reflect"
	"testing"
)

func TestArrivalSeedOrderRuntimeStreamsWithoutRepeatingAndRepairsCursor(t *testing.T) {
	runtime, err := NewSeedOrderPolicy(SeedOrderPolicyConfig{Kind: SeedOrderArrival})
	if err != nil {
		t.Fatalf("create arrival runtime: %v", err)
	}
	for id := TicketID(1); id <= 4; id++ {
		runtime.Add(&Ticket{TicketID: id})
	}

	runtime.BeginRound(3)
	// Delete the current cursor before it is returned. Remove must advance the
	// list cursor before container/list severs the element links.
	runtime.Remove(1)
	if got, ok := runtime.Next(); !ok || got != 2 {
		t.Fatalf("arrival cursor after current removal: got=%d ok=%v, want 2", got, ok)
	}
	// Delete a future element; it must not leave a hole or be returned later.
	runtime.Remove(4)
	if got, ok := runtime.Next(); !ok || got != 3 {
		t.Fatalf("arrival cursor after future removal: got=%d ok=%v, want 3", got, ok)
	}
	if runtime.HasNext() {
		t.Fatal("arrival stream returned a seed after its round limit/live entries were exhausted")
	}

	// A failed seed remains active and is eligible again only after BeginRound.
	runtime.BeginRound(3)
	if got, ok := runtime.Next(); !ok || got != 2 {
		t.Fatalf("arrival failed seed was not reusable next round: got=%d ok=%v", got, ok)
	}
	if got, ok := runtime.Next(); !ok || got != 3 {
		t.Fatalf("arrival second seed next round: got=%d ok=%v", got, ok)
	}

	// Remove+Add creates a new arrival entry and is visible in the next round.
	runtime.Remove(2)
	runtime.Add(&Ticket{TicketID: 2})
	runtime.BeginRound(3)
	got := collectRuntimeSeeds(runtime)
	if want := []TicketID{3, 2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("arrival Remove+Add order: got %v, want %v", got, want)
	}
}

func TestOldestSeedOrderRuntimeMovesHeldAndRemovesBothLocations(t *testing.T) {
	runtime, err := NewSeedOrderPolicy(SeedOrderPolicyConfig{Kind: SeedOrderOldest})
	if err != nil {
		t.Fatalf("create oldest runtime: %v", err)
	}
	policy := runtime.(*oldestSeedOrderPolicy)
	runtime.Add(&Ticket{TicketID: 1, CreatedAt: 30})
	runtime.Add(&Ticket{TicketID: 2, CreatedAt: 10})
	runtime.Add(&Ticket{TicketID: 3, CreatedAt: 20})

	runtime.BeginRound(2)
	if got, ok := runtime.Next(); !ok || got != 2 {
		t.Fatalf("oldest first seed: got=%d ok=%v, want 2", got, ok)
	}
	if got, want := len(policy.held), 1; got != want {
		t.Fatalf("oldest held length=%d, want %d", got, want)
	}
	// Ticket 3 is still in the heap; Ticket 2 is held. Both must be removable
	// without leaving an inactive heap/held entry behind.
	runtime.Remove(3)
	if got, want := policy.entries.Len(), 1; got != want {
		t.Fatalf("oldest active heap length=%d, want %d", got, want)
	}
	runtime.Remove(2)
	if len(policy.held) != 0 {
		t.Fatalf("oldest held removal retained entry: %d", len(policy.held))
	}
	if got, want := policy.entries.Len(), len(policy.active); got != want {
		t.Fatalf("oldest live index mismatch: heap=%d active=%d", got, want)
	}

	runtime.BeginRound(2)
	if got, ok := runtime.Next(); !ok || got != 1 {
		t.Fatalf("oldest remaining seed: got=%d ok=%v, want 1", got, ok)
	}
	if runtime.HasNext() {
		t.Fatal("oldest returned deleted/held seed after removal")
	}

	// Re-add receives a fresh entry and is available in the next round.
	runtime.Add(&Ticket{TicketID: 2, CreatedAt: 5})
	runtime.BeginRound(2)
	if got := collectRuntimeSeeds(runtime); !reflect.DeepEqual(got, []TicketID{2, 1}) {
		t.Fatalf("oldest re-add order: got %v", got)
	}
	if got, want := policy.entries.Len()+len(policy.held), len(policy.active); got != want {
		t.Fatalf("oldest total index length=%d, active=%d", got, want)
	}
}

func TestPrioritySeedOrderRuntimeMovesHeldAndRemovesBothLocations(t *testing.T) {
	runtime, err := NewSeedOrderPolicy(SeedOrderPolicyConfig{
		Kind:              SeedOrderInt64Priority,
		PriorityField:     "priority",
		PriorityDirection: SeedPriorityDescending,
	})
	if err != nil {
		t.Fatalf("create priority runtime: %v", err)
	}
	policy := runtime.(*int64PrioritySeedOrderPolicy)
	runtime.Add(&Ticket{TicketID: 1, Int64Values: map[string]int64{"priority": 1}})
	runtime.Add(&Ticket{TicketID: 2, Int64Values: map[string]int64{"priority": 9}})
	runtime.Add(&Ticket{TicketID: 3, Int64Values: map[string]int64{"priority": 5}})

	runtime.BeginRound(2)
	if got, ok := runtime.Next(); !ok || got != 2 {
		t.Fatalf("priority first seed: got=%d ok=%v, want 2", got, ok)
	}
	runtime.Remove(3)
	runtime.Remove(2)
	if policy.entries.Len() != 1 || len(policy.held) != 0 {
		t.Fatalf("priority removal left wrong state: heap=%d held=%d", policy.entries.Len(), len(policy.held))
	}
	runtime.BeginRound(2)
	if got, ok := runtime.Next(); !ok || got != 1 {
		t.Fatalf("priority remaining seed: got=%d ok=%v, want 1", got, ok)
	}
	if runtime.HasNext() {
		t.Fatal("priority returned deleted/held seed")
	}

	runtime.Add(&Ticket{TicketID: 2, Int64Values: map[string]int64{"priority": 10}})
	runtime.BeginRound(2)
	if got := collectRuntimeSeeds(runtime); !reflect.DeepEqual(got, []TicketID{2, 1}) {
		t.Fatalf("priority re-add order: got %v", got)
	}
	if got, want := policy.entries.Len()+len(policy.held), len(policy.active); got != want {
		t.Fatalf("priority total index length=%d, active=%d", got, want)
	}
}

func TestHeapSeedOrderRuntimeFailedSeedsRestoreOnNextRound(t *testing.T) {
	configs := []SeedOrderPolicyConfig{
		{Kind: SeedOrderOldest},
		{
			Kind:              SeedOrderInt64Priority,
			PriorityField:     "priority",
			PriorityDirection: SeedPriorityDescending,
		},
	}
	for _, config := range configs {
		t.Run(string(config.Kind), func(t *testing.T) {
			runtime, err := NewSeedOrderPolicy(config)
			if err != nil {
				t.Fatalf("create runtime: %v", err)
			}
			for id := TicketID(1); id <= 3; id++ {
				runtime.Add(&Ticket{
					TicketID:    id,
					CreatedAt:   int64(id),
					Int64Values: map[string]int64{"priority": int64(id)},
				})
			}

			// Treat returned seeds as failed attempts: they stay active for the
			// next round, but are held and cannot repeat in this round.
			runtime.BeginRound(2)
			first, ok := runtime.Next()
			if !ok {
				t.Fatal("first failed seed was unavailable")
			}
			second, ok := runtime.Next()
			if !ok || second == first {
				t.Fatalf("same-round seed stream repeated or stopped: first=%d second=%d ok=%v", first, second, ok)
			}
			if runtime.HasNext() {
				t.Fatal("same round exposed a seed after its limit")
			}

			runtime.BeginRound(2)
			if got, ok := runtime.Next(); !ok || got != first {
				t.Fatalf("first failed seed was not restored: got=%d ok=%v want=%d", got, ok, first)
			}
			if got, ok := runtime.Next(); !ok || got != second {
				t.Fatalf("second failed seed was not restored in order: got=%d ok=%v want=%d", got, ok, second)
			}
		})
	}
}

func TestRandomSeedOrderRuntimeUsesDenseActiveAndHeldSwapRemove(t *testing.T) {
	runtime, err := NewSeedOrderPolicy(SeedOrderPolicyConfig{Kind: SeedOrderRandom, RandomSeed: 7})
	if err != nil {
		t.Fatalf("create random runtime: %v", err)
	}
	policy := runtime.(*randomSeedOrderPolicy)
	for id := TicketID(1); id <= 20; id++ {
		runtime.Add(&Ticket{TicketID: id})
	}
	runtime.BeginRound(8)
	first := collectRuntimeSeeds(runtime)
	if len(first) != 8 || !uniqueSeedIDs(first) {
		t.Fatalf("random bounded stream: %v", first)
	}
	if got, want := len(policy.ticketIDs)+len(policy.held), len(policy.positions)+len(policy.heldPositions); got != want {
		t.Fatalf("random active/held index mismatch: entries=%d positions=%d", got, want)
	}

	// Delete one held and one active element, exercising both swap-remove paths.
	runtime.Remove(first[3])
	activeID := policy.ticketIDs[len(policy.ticketIDs)/2]
	runtime.Remove(activeID)
	if got, want := len(policy.ticketIDs)+len(policy.held), len(policy.positions)+len(policy.heldPositions); got != want {
		t.Fatalf("random index mismatch after removals: entries=%d positions=%d", got, want)
	}
	runtime.BeginRound(20)
	second := collectRuntimeSeeds(runtime)
	if !uniqueSeedIDs(second) || containsSeedID(second, first[3]) || containsSeedID(second, activeID) {
		t.Fatalf("random removed IDs returned after next round: %v", second)
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
		return runtime
	}
	left, right := newRuntime(), newRuntime()
	for round := 0; round < 5; round++ {
		left.BeginRound(6)
		right.BeginRound(6)
		leftOrder := collectRuntimeSeeds(left)
		rightOrder := collectRuntimeSeeds(right)
		if !reflect.DeepEqual(leftOrder, rightOrder) {
			t.Fatalf("same random seed/lifecycle diverged at round %d: left=%v right=%v", round, leftOrder, rightOrder)
		}
	}
}

func TestSeedOrderRuntimeRoundLimitCapsHeldAtFiveHundred(t *testing.T) {
	configs := []SeedOrderPolicyConfig{
		{Kind: SeedOrderArrival},
		{Kind: SeedOrderOldest},
		{Kind: SeedOrderInt64Priority, PriorityField: "priority"},
		{Kind: SeedOrderRandom, RandomSeed: 23},
	}
	for _, config := range configs {
		t.Run(string(config.Kind), func(t *testing.T) {
			runtime, err := NewSeedOrderPolicy(config)
			if err != nil {
				t.Fatalf("create runtime: %v", err)
			}
			for id := TicketID(1); id <= 1000; id++ {
				runtime.Add(&Ticket{
					TicketID:    id,
					CreatedAt:   int64(id),
					Int64Values: map[string]int64{"priority": int64(id)},
				})
			}
			runtime.BeginRound(500)
			ids := collectRuntimeSeeds(runtime)
			if len(ids) != 500 || !uniqueSeedIDs(ids) {
				t.Fatalf("round stream len=%d unique=%v", len(ids), uniqueSeedIDs(ids))
			}
			active, indexed := seedRuntimeIndexSizes(runtime)
			if active != 1000 || indexed != 1000 {
				t.Fatalf("runtime index sizes after 500 seeds: active=%d indexed=%d", active, indexed)
			}
			if runtime.HasNext() {
				t.Fatal("runtime exposed more than its 500-seed round limit")
			}
		})
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

func collectRuntimeSeeds(runtime SeedOrderRuntime) []TicketID {
	var result []TicketID
	for runtime.HasNext() {
		ticketID, ok := runtime.Next()
		if !ok {
			break
		}
		result = append(result, ticketID)
	}
	return result
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
	for index, entry := range policy.held {
		if entry == nil || entry.heldIndex != index {
			t.Fatalf("oldest held index mismatch at %d: %#v", index, entry)
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
	for index, entry := range policy.held {
		if entry == nil || entry.heldIndex != index {
			t.Fatalf("priority held index mismatch at %d: %#v", index, entry)
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
		return len(policy.active), policy.entries.Len() + len(policy.held)
	case *int64PrioritySeedOrderPolicy:
		return len(policy.active), policy.entries.Len() + len(policy.held)
	case *randomSeedOrderPolicy:
		return len(policy.positions) + len(policy.heldPositions), len(policy.ticketIDs) + len(policy.held)
	default:
		return 0, 0
	}
}
