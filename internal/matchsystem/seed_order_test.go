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
		want   []string
	}{
		{name: "arrival", config: SeedOrderPolicyConfig{Kind: SeedOrderArrival}, want: []string{"a", "b", "c"}},
		{name: "oldest", config: SeedOrderPolicyConfig{Kind: SeedOrderOldest}, want: []string{"b", "c", "a"}},
		{name: "priority descending", config: SeedOrderPolicyConfig{Kind: SeedOrderInt64Priority, PriorityField: "priority"}, want: []string{"c", "a", "b"}},
		{name: "priority ascending", config: SeedOrderPolicyConfig{Kind: SeedOrderInt64Priority, PriorityField: "priority", PriorityDirection: SeedPriorityAscending}, want: []string{"a", "c", "b"}},
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
			seen := make(map[uint32]int)
			for node.hasUntriedSeed() {
				seed := node.nextSeed()
				if seed == nil {
					t.Fatal("hasUntriedSeed returned true but nextSeed returned nil")
				}
				seen[seed.DocID]++
			}
			if len(seen) != 3 {
				t.Fatalf("visited %d seeds, want 3", len(seen))
			}
			for docID, count := range seen {
				if count != 1 {
					t.Fatalf("DocID %d visited %d times", docID, count)
				}
			}
		})
	}
}

func TestSeedRoundIsSnapshotAndSkipsDeletedFutureSeed(t *testing.T) {
	node := seedOrderTestNode(t, SeedOrderPolicyConfig{Kind: SeedOrderArrival}, nil)
	mustAdd(t, node, &Ticket{TicketID: "a"})
	mustAdd(t, node, &Ticket{TicketID: "b"})
	if err := node.BeginMatchRound(1); err != nil {
		t.Fatal(err)
	}
	mustAdd(t, node, &Ticket{TicketID: "next-round"})
	if seed := node.nextSeed(); seed == nil || seed.TicketID != "a" {
		t.Fatalf("first seed=%#v", seed)
	}
	if !node.Remove("b") {
		t.Fatal("remove b failed")
	}
	if seed := node.nextSeed(); seed != nil {
		t.Fatalf("new or deleted ticket entered current round: %#v", seed)
	}
	if err := node.BeginMatchRound(2); err != nil {
		t.Fatal(err)
	}
	if got := roundTicketIDs(node); !reflect.DeepEqual(got, []string{"a", "next-round"}) {
		t.Fatalf("next round order=%v", got)
	}
}

func TestCustomSeedOrderMustReturnCompletePermutation(t *testing.T) {
	badOrder := FuncSeedOrderPolicy(func(ctx SeedOrderContext) ([]uint32, error) {
		return []uint32{ctx.Candidates[0].DocID, ctx.Candidates[0].DocID}, nil
	})
	node := seedOrderTestNode(t, SeedOrderPolicyConfig{}, badOrder)
	mustAdd(t, node, &Ticket{TicketID: "a"})
	mustAdd(t, node, &Ticket{TicketID: "b"})
	if err := node.BeginMatchRound(1); err == nil {
		t.Fatal("duplicate policy order was accepted")
	}
}

func TestProduceMatchRequiresRound(t *testing.T) {
	node := seedOrderTestNode(t, SeedOrderPolicyConfig{}, nil)
	mustAdd(t, node, &Ticket{TicketID: "a"})
	if _, err := node.ProduceMatch(Facts{}); !errors.Is(err, ErrMatchRoundNotStarted) {
		t.Fatalf("ProduceMatch error=%v", err)
	}
}

func seedOrderTestNode(t *testing.T, config SeedOrderPolicyConfig, policy SeedOrderPolicy) *LogicalNode {
	t.Helper()
	node, err := NewLogicalNode(LogicalNodeSpec{
		Key: identity.LogicalNodeKey{
			Rule:        identity.RuleKey{RuleID: "seed-order-test"},
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
	mustAdd(t, node, &Ticket{TicketID: "a", CreatedAt: 30, Int64Values: map[string]int64{"priority": 2}})
	mustAdd(t, node, &Ticket{TicketID: "b", CreatedAt: 10})
	mustAdd(t, node, &Ticket{TicketID: "c", CreatedAt: 20, Int64Values: map[string]int64{"priority": 5}})
}

func roundTicketIDs(node *LogicalNode) []string {
	result := make([]string, 0, len(node.seedRound.order))
	for _, docID := range node.seedRound.order {
		result = append(result, node.ticketsByDocID[docID].TicketID)
	}
	return result
}
