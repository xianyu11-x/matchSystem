package matchsystem

import (
	"errors"
	"reflect"
	"testing"

	"matchSystem/internal/identity"
	"matchSystem/internal/matchsystem/prefilter"
)

func TestSeedFactProviderDrivesPrefilterWithoutMutatingInputs(t *testing.T) {
	config := prefilter.Config{
		Indexes: []prefilter.IndexSpec{
			prefilter.NewMultiValueIndex(prefilter.MultiValueIndexConfig{Name: "mode_index", Field: "mode", MaxDocumentValues: 2, MaxQueryValues: 2}),
			prefilter.NewMultiValueIndex(prefilter.MultiValueIndexConfig{Name: "bucket_index", Field: "bucket", KeyType: prefilter.KeyTypeUint64, MaxDocumentValues: 2, MaxQueryValues: 2}),
			prefilter.NewInt64RangeIndex(prefilter.Int64RangeIndexConfig{Name: "rating_index", Field: "rating"}),
		},
		Facts: []prefilter.FactSpec{
			{Name: "mode_keys", Type: prefilter.FactTypeStrings, MaxValues: 2},
			{Name: "bucket_keys", Type: prefilter.FactTypeUint64s, MaxValues: 2},
			{Name: "wait_millis", Type: prefilter.FactTypeInt64},
			{Name: "offset", Type: prefilter.FactTypeInt64},
		},
		Root: prefilter.And(
			prefilter.Lookup(prefilter.StringQuery{Index: "mode_index", Values: prefilter.FactStrings("mode_keys")}),
			prefilter.Lookup(prefilter.Uint64Query{Index: "bucket_index", Values: prefilter.FactUint64s("bucket_keys")}),
			prefilter.Lookup(prefilter.Int64RangeQuery{
				Index: "rating_index",
				Min: prefilter.SubInt64(
					prefilter.SeedInt64("rating"),
					prefilter.StepInt64(prefilter.FactInt64("wait_millis"),
						prefilter.Int64Step{At: 0, Value: 0},
						prefilter.Int64Step{At: 50, Value: 10},
					),
				),
				Max: prefilter.AddInt64(
					prefilter.SeedInt64("rating"),
					prefilter.StepInt64(prefilter.FactInt64("wait_millis"),
						prefilter.Int64Step{At: 0, Value: 0},
						prefilter.Int64Step{At: 50, Value: 10},
					),
				),
			}),
		),
	}

	seedFactValues := Facts{}
	providerCalls := 0
	provider := func(seed *Ticket, now int64, tickFacts Facts) (Facts, error) {
		providerCalls++
		if now != 60 || tickFacts.Int64Values["offset"] != 0 {
			t.Fatalf("unexpected provider context: now=%d facts=%#v", now, tickFacts)
		}
		values := Facts{
			StringLists: map[string][]string{"mode_keys": append([]string(nil), seed.StringLists["query_mode"]...)},
			Uint64Lists: map[string][]uint64{"bucket_keys": append([]uint64(nil), seed.Uint64Lists["query_bucket"]...)},
			Int64Values: map[string]int64{"wait_millis": now - seed.CreatedAt},
		}
		if seed.TicketID == testTicketID("seed") {
			seedFactValues = values
		}
		return values, nil
	}
	node := mustLogicalNodeWithSeedFacts(t, config, provider)
	mustAdd(t, node, &Ticket{
		TicketID:    testTicketID("seed"),
		CreatedAt:   0,
		StringLists: map[string][]string{"mode": {"ranked"}, "query_mode": {"ranked"}},
		Uint64Lists: map[string][]uint64{"bucket": {7}, "query_bucket": {7}},
		Int64Values: map[string]int64{"rating": 100},
	})
	mustAdd(t, node, &Ticket{
		TicketID:    testTicketID("candidate"),
		StringLists: map[string][]string{"mode": {"ranked"}},
		Uint64Lists: map[string][]uint64{"bucket": {7}},
		Int64Values: map[string]int64{"rating": 110},
	})
	tickFacts := Facts{Int64Values: map[string]int64{"offset": 0}}
	matches, err := produceTestRound(node, 60, tickFacts)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || len(matches[0].Tickets) != 2 {
		t.Fatalf("unexpected matches: %#v", matches)
	}
	if providerCalls != 2 {
		t.Fatalf("Object Fact provider calls=%d want=2 (seed and candidate)", providerCalls)
	}
	if !reflect.DeepEqual(tickFacts, Facts{Int64Values: map[string]int64{"offset": 0}}) {
		t.Fatalf("Tick Facts were mutated: %#v", tickFacts)
	}
	if !reflect.DeepEqual(seedFactValues, Facts{
		StringLists: map[string][]string{"mode_keys": {"ranked"}},
		Uint64Lists: map[string][]uint64{"bucket_keys": {7}},
		Int64Values: map[string]int64{"wait_millis": 60},
	}) {
		t.Fatalf("Seed Facts were mutated: %#v", seedFactValues)
	}
}

func TestSeedFactProviderErrorSkipsOnlyCurrentSeed(t *testing.T) {
	providerErr := errors.New("seed facts unavailable")
	provider := func(seed *Ticket, _ int64, _ Facts) (Facts, error) {
		if seed.TicketID == testTicketID("bad") {
			return Facts{}, providerErr
		}
		return Facts{}, nil
	}
	node := mustLogicalNodeWithSeedFacts(t, prefilterConfigForField("partition"), provider)
	mustAdd(t, node, &Ticket{TicketID: testTicketID("bad")})
	mustAdd(t, node, testTicket("a", 1, "blue"))
	mustAdd(t, node, testTicket("b", 2, "blue"))

	match, err := produceTestMatch(node, 100, Facts{})
	if err != nil {
		t.Fatalf("successful later seed returned earlier provider error: %v", err)
	}
	if match == nil || len(match.Tickets) != 2 || match.Tickets[0].TicketID != testTicketID("a") {
		t.Fatalf("unexpected match: %#v", match)
	}
	if node.Len() != 1 {
		t.Fatalf("failed seed should remain: %d tickets", node.Len())
	}
}

func TestSeedFactCollisionSkipsOnlyCurrentSeed(t *testing.T) {
	provider := func(seed *Ticket, _ int64, _ Facts) (Facts, error) {
		if seed.TicketID == testTicketID("bad") {
			return Facts{Int64Values: map[string]int64{"shared": 1}}, nil
		}
		return Facts{}, nil
	}
	config := prefilterConfigForField("partition")
	config.Facts = append(config.Facts, prefilter.FactSpec{Name: "shared", Type: prefilter.FactTypeInt64})
	node := mustLogicalNodeWithSeedFacts(t, config, provider)
	mustAdd(t, node, &Ticket{TicketID: testTicketID("bad")})
	mustAdd(t, node, testTicket("a", 1, "blue"))
	mustAdd(t, node, testTicket("b", 2, "blue"))

	match, err := produceTestMatch(node, 100, Facts{Int64Values: map[string]int64{"shared": 2}})
	if err != nil {
		t.Fatalf("successful later seed returned earlier collision: %v", err)
	}
	if match == nil || len(match.Tickets) != 2 || match.Tickets[0].TicketID != testTicketID("a") {
		t.Fatalf("unexpected match: %#v", match)
	}
}

func mustLogicalNodeWithSeedFacts(t *testing.T, config prefilter.Config, provider SeedFactProvider) *LogicalNode {
	t.Helper()
	node, err := NewLogicalNode(LogicalNodeSpec{
		Key:              identity.LogicalNodeKey{Rule: identity.RuleKey{RuleID: 1}, PlacementID: "test-placement"},
		Config:           LogicalNodeConfig{MaxPlayers: 2, Prefilter: config},
		Rules:            NewRuleSet(minimumGroupSize(2)),
		SeedFactProvider: provider,
	})
	if err != nil {
		t.Fatalf("NewLogicalNode: %v", err)
	}
	return node
}
