package matchsystem

import (
	"errors"
	"reflect"
	"testing"

	"matchSystem/internal/identity"
	"matchSystem/internal/matchsystem/prefilter"
)

func TestBuiltInSeedOrderPolicies(t *testing.T) {
	tests := []struct {
		name   string
		config SeedOrderPolicyConfig
		want   []TicketID
	}{
		{name: "arrival", config: SeedOrderPolicyConfig{Kind: SeedOrderArrival}, want: []TicketID{testTicketID("a"), testTicketID("b"), testTicketID("c")}},
		{name: "oldest", config: SeedOrderPolicyConfig{Kind: SeedOrderOldest}, want: []TicketID{testTicketID("b"), testTicketID("c"), testTicketID("a")}},
		{name: "priority descending", config: SeedOrderPolicyConfig{Kind: SeedOrderInt64Priority, PriorityField: "priority"}, want: []TicketID{testTicketID("c"), testTicketID("a"), testTicketID("b")}},
		{name: "priority ascending", config: SeedOrderPolicyConfig{Kind: SeedOrderInt64Priority, PriorityField: "priority", PriorityDirection: SeedPriorityAscending}, want: []TicketID{testTicketID("a"), testTicketID("c"), testTicketID("b")}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			node := seedOrderTestNode(t, test.config, nil)
			addSeedOrderTickets(t, node)
			if err := node.BeginMatchRound(100); err != nil {
				t.Fatal(err)
			}
			if got := roundTicketIDs(node); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("seed order=%v want=%v", got, test.want)
			}
		})
	}
}

func TestRandomSeedOrderIsDeterministicForConfiguredSeed(t *testing.T) {
	config := SeedOrderPolicyConfig{Kind: SeedOrderRandom, RandomSeed: 42}
	first := seedOrderTestNode(t, config, nil)
	second := seedOrderTestNode(t, config, nil)
	addSeedOrderTickets(t, first)
	addSeedOrderTickets(t, second)
	if err := first.BeginMatchRound(1); err != nil {
		t.Fatal(err)
	}
	if err := second.BeginMatchRound(1); err != nil {
		t.Fatal(err)
	}
	if left, right := roundTicketIDs(first), roundTicketIDs(second); !reflect.DeepEqual(left, right) {
		t.Fatalf("same random seed produced different orders: %v and %v", left, right)
	}
}

func TestEverySeedOrderAdvancesWithoutRepeatingInOneRound(t *testing.T) {
	configs := []SeedOrderPolicyConfig{
		{Kind: SeedOrderArrival},
		{Kind: SeedOrderOldest},
		{Kind: SeedOrderInt64Priority, PriorityField: "priority"},
		{Kind: SeedOrderRandom, RandomSeed: 7},
	}
	for _, config := range configs {
		t.Run(string(config.Kind), func(t *testing.T) {
			node := seedOrderTestNode(t, config, nil)
			addSeedOrderTickets(t, node)
			if err := node.BeginMatchRound(100); err != nil {
				t.Fatal(err)
			}
			seen := make(map[TicketID]int)
			for node.hasUntriedSeed() {
				seed := node.nextSeed()
				if seed == nil {
					t.Fatal("hasUntriedSeed returned true but nextSeed returned nil")
				}
				seen[seed.TicketID]++
			}
			if len(seen) != 3 {
				t.Fatalf("visited %d seeds, want 3", len(seen))
			}
			for ticketID, count := range seen {
				if count != 1 {
					t.Fatalf("TicketID %d visited %d times", ticketID, count)
				}
			}
		})
	}
}

func TestSeedRoundIsSnapshotAndSkipsDeletedFutureSeed(t *testing.T) {
	node := seedOrderTestNode(t, SeedOrderPolicyConfig{Kind: SeedOrderArrival}, nil)
	mustAdd(t, node, &Ticket{TicketID: testTicketID("a")})
	mustAdd(t, node, &Ticket{TicketID: testTicketID("b")})
	if err := node.BeginMatchRound(1); err != nil {
		t.Fatal(err)
	}
	mustAdd(t, node, &Ticket{TicketID: testTicketID("next-round")})
	if seed := node.nextSeed(); seed == nil || seed.TicketID != testTicketID("a") {
		t.Fatalf("first seed=%#v", seed)
	}
	if !node.Remove(testTicketID("b")) {
		t.Fatal("remove b failed")
	}
	if seed := node.nextSeed(); seed != nil {
		t.Fatalf("new or deleted ticket entered current round: %#v", seed)
	}
	if err := node.BeginMatchRound(2); err != nil {
		t.Fatal(err)
	}
	if got := roundTicketIDs(node); !reflect.DeepEqual(got, []TicketID{testTicketID("a"), testTicketID("next-round")}) {
		t.Fatalf("next round order=%v", got)
	}
}

func TestHasUntriedSeedAdvancesPastStaleSuffix(t *testing.T) {
	node := seedOrderTestNode(t, SeedOrderPolicyConfig{Kind: SeedOrderArrival}, nil)
	mustAdd(t, node, &Ticket{TicketID: testTicketID("a")})
	mustAdd(t, node, &Ticket{TicketID: testTicketID("b")})
	mustAdd(t, node, &Ticket{TicketID: testTicketID("c")})
	if err := node.BeginMatchRound(1); err != nil {
		t.Fatal(err)
	}
	if seed := node.nextSeed(); seed == nil || seed.TicketID != testTicketID("a") {
		t.Fatalf("first seed=%#v", seed)
	}
	node.Remove(testTicketID("b"))
	node.Remove(testTicketID("c"))
	if node.hasUntriedSeed() {
		t.Fatal("stale suffix was reported as an untried seed")
	}
	if node.seedRound.cursor != len(node.seedRound.order) {
		t.Fatalf("stale cursor=%d want=%d", node.seedRound.cursor, len(node.seedRound.order))
	}
}

func TestArrivalSeedOrderBorrowsDenseArrivalOrder(t *testing.T) {
	node := seedOrderTestNode(t, SeedOrderPolicyConfig{Kind: SeedOrderArrival}, nil)
	addSeedOrderTickets(t, node)
	if err := node.BeginMatchRound(1); err != nil {
		t.Fatal(err)
	}
	if &node.seedRound.order[0] != &node.arrivalOrder[0] {
		t.Fatal("dense arrival order was copied")
	}
	if node.seedRound.ownsOrder {
		t.Fatal("borrowed arrival order was marked as owned")
	}
}

func TestOptimizedSeedOrderReusesSpareBuffer(t *testing.T) {
	node := seedOrderTestNode(t, SeedOrderPolicyConfig{Kind: SeedOrderOldest}, nil)
	addSeedOrderTickets(t, node)
	if err := node.BeginMatchRound(1); err != nil {
		t.Fatal(err)
	}
	firstBuffer := &node.seedRound.order[0]
	if err := node.BeginMatchRound(2); err != nil {
		t.Fatal(err)
	}
	if err := node.BeginMatchRound(3); err != nil {
		t.Fatal(err)
	}
	if &node.seedRound.order[0] != firstBuffer {
		t.Fatal("owned seed order did not reuse the spare round buffer")
	}
}

func TestCustomSeedOrderRejectsDuplicateTicketID(t *testing.T) {
	badOrder := FuncSeedOrderPolicy(func(ctx SeedOrderContext) ([]TicketID, error) {
		return []TicketID{ctx.Candidates[0].TicketID, ctx.Candidates[0].TicketID}, nil
	})
	node := seedOrderTestNode(t, SeedOrderPolicyConfig{}, badOrder)
	mustAdd(t, node, &Ticket{TicketID: testTicketID("a")})
	mustAdd(t, node, &Ticket{TicketID: testTicketID("b")})
	if err := node.BeginMatchRound(1); err == nil {
		t.Fatal("duplicate policy order was accepted")
	}
}

func TestCustomSeedOrderResolvesTicketIDsToPrivateDocIDs(t *testing.T) {
	policy := FuncSeedOrderPolicy(func(ctx SeedOrderContext) ([]TicketID, error) {
		return []TicketID{ctx.Candidates[2].TicketID, ctx.Candidates[0].TicketID, ctx.Candidates[1].TicketID}, nil
	})
	node := seedOrderTestNode(t, SeedOrderPolicyConfig{}, policy)
	addSeedOrderTickets(t, node)
	if err := node.BeginMatchRound(1); err != nil {
		t.Fatal(err)
	}
	if got := roundTicketIDs(node); !reflect.DeepEqual(got, []TicketID{testTicketID("c"), testTicketID("a"), testTicketID("b")}) {
		t.Fatalf("custom TicketID order=%v", got)
	}
}

func TestProduceMatchRequiresRound(t *testing.T) {
	node := seedOrderTestNode(t, SeedOrderPolicyConfig{}, nil)
	mustAdd(t, node, &Ticket{TicketID: testTicketID("a")})
	if _, err := node.ProduceMatch(Facts{}); !errors.Is(err, ErrMatchRoundNotStarted) {
		t.Fatalf("ProduceMatch error=%v", err)
	}
}

func seedOrderTestNode(t *testing.T, config SeedOrderPolicyConfig, policy SeedOrderPolicy) *LogicalNode {
	t.Helper()
	node, err := NewLogicalNode(LogicalNodeSpec{
		Key: identity.LogicalNodeKey{
			Rule:        identity.RuleKey{RuleID: 1},
			PlacementID: "test-placement",
		},
		Config: LogicalNodeConfig{
			SeedScheduler: SeedSchedulerConfig{
				AttemptLimitPerProduceMatch: 1,
				Order:                       config,
			},
			Prefilter: prefilter.Config{Root: prefilter.None()},
		},
		SeedOrderPolicy: policy,
	})
	if err != nil {
		t.Fatal(err)
	}
	return node
}

func addSeedOrderTickets(t *testing.T, node *LogicalNode) {
	t.Helper()
	mustAdd(t, node, &Ticket{TicketID: testTicketID("a"), CreatedAt: 30, Int64Values: map[string]int64{"priority": 2}})
	mustAdd(t, node, &Ticket{TicketID: testTicketID("b"), CreatedAt: 10})
	mustAdd(t, node, &Ticket{TicketID: testTicketID("c"), CreatedAt: 20, Int64Values: map[string]int64{"priority": 5}})
}

func roundTicketIDs(node *LogicalNode) []TicketID {
	result := make([]TicketID, 0, len(node.seedRound.order))
	for _, docID := range node.seedRound.order {
		result = append(result, node.ticketsByDocID[docID].TicketID)
	}
	return result
}
