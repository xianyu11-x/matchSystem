package matchsystem

import (
	"testing"

	"matchSystem/internal/identity"
	"matchSystem/internal/matchsystem/prefilter"
)

func TestGreedyMatchesThroughPrefilter(t *testing.T) {
	node := mustLogicalNode(t, prefilterConfigForField("partition"), NewRuleSet(minimumGroupSize(2)), LogicalNodeConfig{MaxPlayers: 2})
	mustAdd(t, node, testTicket("a", 1, "blue"))
	mustAdd(t, node, testTicket("b", 2, "blue"))
	mustAdd(t, node, testTicket("c", 3, "green"))
	matches, err := produceTestRound(node, 100, Facts{})
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(matches) != 1 || len(matches[0].Tickets) != 2 {
		t.Fatalf("expected one two-ticket match, got %#v", matches)
	}
	if matches[0].Tickets[0].TicketID != "a" || matches[0].Tickets[1].TicketID != "b" {
		t.Fatalf("unexpected group: %#v", matches[0])
	}
	if node.Len() != 1 {
		t.Fatalf("expected one waiting ticket, got %d", node.Len())
	}
}

func TestProduceMatchStopsAfterFirstMatch(t *testing.T) {
	node := mustLogicalNode(t, prefilterConfigForField("partition"), NewRuleSet(minimumGroupSize(2)), LogicalNodeConfig{MaxPlayers: 2})
	for _, id := range []string{"a", "b", "c", "d"} {
		mustAdd(t, node, testTicket(id, 1, "blue"))
	}
	match, err := produceTestMatch(node, 100, Facts{})
	if err != nil {
		t.Fatal(err)
	}
	if match == nil || len(match.Tickets) != 2 {
		t.Fatalf("expected one two-ticket match, got %#v", match)
	}
	if node.Len() != 2 {
		t.Fatalf("ProduceMatch removed more than one group: %d tickets remain", node.Len())
	}
}

func TestProduceMatchSuccessfulMatchDoesNotReturnEarlierSeedError(t *testing.T) {
	node := mustLogicalNode(t, prefilterConfigForField("partition"), NewRuleSet(minimumGroupSize(2)), LogicalNodeConfig{MaxPlayers: 2})
	mustAdd(t, node, &Ticket{TicketID: "missing", CreatedAt: 1})
	mustAdd(t, node, testTicket("a", 2, "blue"))
	mustAdd(t, node, testTicket("b", 3, "blue"))
	match, err := produceTestMatch(node, 100, Facts{})
	if err != nil {
		t.Fatalf("successful committed match returned an ambiguous error: %v", err)
	}
	if match == nil || len(match.Tickets) != 2 {
		t.Fatalf("expected one successful match, got %#v", match)
	}
	if node.Len() != 1 {
		t.Fatalf("expected only the failed seed to remain, got %d tickets", node.Len())
	}
}

func TestEmptyPlanDoesNotStartImplicitMatches(t *testing.T) {
	node := mustLogicalNode(t, prefilter.Config{Root: prefilter.None()}, nil, LogicalNodeConfig{})
	mustAdd(t, node, testTicket("a", 1, ""))
	matches, err := produceTestRound(node, 100, Facts{})
	if err != nil || len(matches) != 0 {
		t.Fatalf("empty plan result: matches=%#v err=%v", matches, err)
	}
	if node.Len() != 1 {
		t.Fatal("ticket should remain")
	}
}

func TestGenericForceStartExtension(t *testing.T) {
	forceAfter := FuncGroupEvaluator{EvaluatorFlagsValue: GroupEvaluatorForceStart, AllowFn: func(ctx GroupEvaluatorContext, _ []*Ticket, _ *Ticket) bool { return ctx.Now-ctx.Seed.CreatedAt >= 50 }}
	node := mustLogicalNode(t, prefilter.Config{Root: prefilter.None()}, NewRuleSet(forceAfter), LogicalNodeConfig{})
	mustAdd(t, node, testTicket("a", 10, ""))
	if matches, err := produceTestRound(node, 59, Facts{}); err != nil || len(matches) != 0 {
		t.Fatalf("force-started early: %#v, %v", matches, err)
	}
	if matches, err := produceTestRound(node, 60, Facts{}); err != nil || len(matches) != 1 || len(matches[0].Tickets) != 1 {
		t.Fatalf("expected forced match: %#v, %v", matches, err)
	}
}

func TestBoundedTopLUsesCustomCandidateScore(t *testing.T) {
	rules := NewRuleSet(minimumGroupSize(2)).WithCandidateScore(func(_ *Ticket, candidate *Ticket, _ int64) float64 { return float64(candidate.Int64Values["priority"]) })
	config := LogicalNodeConfig{MaxPlayers: 2, GroupBuilder: GroupBuilderConfig{CandidateLimitPerSeed: 1}}
	node := mustLogicalNode(t, prefilterConfigForField("common"), rules, config)
	mustAdd(t, node, &Ticket{TicketID: "seed", CreatedAt: 1, StringLists: map[string][]string{"common": {"x"}}})
	mustAdd(t, node, &Ticket{TicketID: "low", CreatedAt: 2, StringLists: map[string][]string{"common": {"x"}}, Int64Values: map[string]int64{"priority": 1}})
	mustAdd(t, node, &Ticket{TicketID: "high", CreatedAt: 3, StringLists: map[string][]string{"common": {"x"}}, Int64Values: map[string]int64{"priority": 10}})
	matches, err := produceTestRound(node, 100, Facts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || matches[0].Tickets[1].TicketID != "high" {
		t.Fatalf("Top-L did not retain highest score: %#v", matches)
	}
}

func TestAddCopiesTicketAndRemoveCleansIndexes(t *testing.T) {
	node := mustLogicalNode(t, prefilterConfigForField("partition"), NewRuleSet(), LogicalNodeConfig{})
	ticket := testTicket("a", 1, "blue")
	docID := mustAdd(t, node, ticket)
	ticket.StringLists["partition"][0] = "mutated"
	if stored := node.ticketsByDocID[docID]; stored.StringLists["partition"][0] != "blue" {
		t.Fatal("LogicalNode did not deep-copy ticket")
	}
	if !node.Remove("a") || node.Remove("a") {
		t.Fatal("remove should succeed exactly once")
	}
	if node.prefilterStore.Len() != 0 {
		t.Fatal("removed ticket remained active")
	}
}

func TestGetReturnsTicketCopy(t *testing.T) {
	node := mustLogicalNode(t, prefilterConfigForField("partition"), NewRuleSet(), LogicalNodeConfig{})
	mustAdd(t, node, testTicket("a", 1, "blue"))
	first, ok := node.Get("a")
	if !ok {
		t.Fatal("ticket was not found")
	}
	first.StringLists["partition"][0] = "mutated"
	second, _ := node.Get("a")
	if second.StringLists["partition"][0] != "blue" {
		t.Fatal("Get exposed mutable LogicalNode state")
	}
}

func TestPoolCopiesRuleSetContainer(t *testing.T) {
	rules := NewRuleSet()
	node := mustLogicalNode(t, prefilter.Config{Root: prefilter.None()}, rules, LogicalNodeConfig{})
	rules.Use(FuncGroupEvaluator{
		EvaluatorFlagsValue: GroupEvaluatorForceStart,
		AllowFn:             func(_ GroupEvaluatorContext, _ []*Ticket, _ *Ticket) bool { return true },
	})
	mustAdd(t, node, &Ticket{TicketID: "a"})
	match, err := produceTestMatch(node, 1, Facts{})
	if err != nil {
		t.Fatal(err)
	}
	if match != nil {
		t.Fatal("mutating the source RuleSet changed an existing LogicalNode")
	}
}

func TestPoolSupportsUint64PrefilterAndCopiesLists(t *testing.T) {
	config := prefilter.Config{
		Indexes: []prefilter.IndexSpec{prefilter.NewMultiValueIndex(prefilter.MultiValueIndexConfig{Name: "uint64_index", Field: "uint64_dimension", KeyType: prefilter.KeyTypeUint64, MaxDocumentValues: 4, MaxQueryValues: 4})},
		Root:    prefilter.Lookup(prefilter.Uint64Query{Index: "uint64_index", Values: prefilter.SeedUint64s("uint64_dimension")}),
	}
	node := mustLogicalNode(t, config, NewRuleSet(minimumGroupSize(2)), LogicalNodeConfig{MaxPlayers: 2})
	seed := &Ticket{TicketID: "seed", Uint64Lists: map[string][]uint64{"uint64_dimension": {10, 20}}}
	mustAdd(t, node, seed)
	seed.Uint64Lists["uint64_dimension"][0] = 999
	mustAdd(t, node, &Ticket{TicketID: "candidate", Uint64Lists: map[string][]uint64{"uint64_dimension": {20}}})
	matches, err := produceTestRound(node, 1, Facts{})
	if err != nil || len(matches) != 1 {
		t.Fatalf("uint64 LogicalNode match failed: matches=%v err=%v", matches, err)
	}
}

func TestAddRejectsDuplicateAndDocumentKeyOverflow(t *testing.T) {
	config := prefilterConfigForField("partition")
	config.Indexes = []prefilter.IndexSpec{prefilter.NewMultiValueIndex(prefilter.MultiValueIndexConfig{Name: "partition_index", Field: "partition", MaxDocumentValues: 1, MaxQueryValues: 2})}
	node := mustLogicalNode(t, config, NewRuleSet(), LogicalNodeConfig{})
	mustAdd(t, node, testTicket("a", 1, "blue"))
	if _, err := node.Add(testTicket("a", 2, "blue")); err == nil {
		t.Fatal("duplicate TicketID was accepted")
	}
	if _, err := node.Add(&Ticket{TicketID: "b", StringLists: map[string][]string{"partition": {"x", "y"}}}); err == nil {
		t.Fatal("document key overflow was accepted")
	}
}

func TestRuntimeQueryErrorRetainsSeed(t *testing.T) {
	node := mustLogicalNode(t, prefilterConfigForField("partition"), NewRuleSet(minimumGroupSize(2)), LogicalNodeConfig{})
	mustAdd(t, node, &Ticket{TicketID: "missing", CreatedAt: 1})
	if matches, err := produceTestRound(node, 10, Facts{}); err == nil || len(matches) != 0 {
		t.Fatalf("expected query error, got matches=%v err=%v", matches, err)
	}
	if node.Len() != 1 {
		t.Fatal("seed with query error must remain")
	}
}

func prefilterConfigForField(field string) prefilter.Config {
	name := field + "_index"
	return prefilter.Config{
		Indexes: []prefilter.IndexSpec{prefilter.NewMultiValueIndex(prefilter.MultiValueIndexConfig{Name: name, Field: field, MaxDocumentValues: 64, MaxQueryValues: 64})},
		Root:    prefilter.Lookup(prefilter.StringQuery{Index: name, Values: prefilter.SeedStrings(field)}),
	}
}

func mustLogicalNode(t *testing.T, filterConfig prefilter.Config, rules *RuleSet, config LogicalNodeConfig) *LogicalNode {
	t.Helper()
	config.Prefilter = filterConfig
	node, err := NewLogicalNode(LogicalNodeSpec{
		Key:    identity.LogicalNodeKey{Rule: identity.RuleKey{RuleID: "test-rule"}, PlacementID: "test-placement"},
		Config: config,
		Rules:  rules,
	})
	if err != nil {
		t.Fatalf("NewLogicalNode: %v", err)
	}
	return node
}
func mustAdd(t *testing.T, node *LogicalNode, ticket *Ticket) uint32 {
	t.Helper()
	id, err := node.Add(ticket)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	return id
}
func minimumGroupSize(size int) FuncGroupEvaluator {
	return FuncGroupEvaluator{EvaluatorFlagsValue: GroupEvaluatorStart, AllowFn: func(_ GroupEvaluatorContext, group []*Ticket, _ *Ticket) bool { return len(group) >= size }}
}
func testTicket(id string, createdAt int64, partition string) *Ticket {
	fields := map[string][]string{}
	if partition != "" {
		fields["partition"] = []string{partition}
	}
	return &Ticket{TicketID: id, CreatedAt: createdAt, StringLists: fields}
}
