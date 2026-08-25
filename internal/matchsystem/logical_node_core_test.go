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
	if matches[0].Tickets[0].TicketID != testTicketID("a") || matches[0].Tickets[1].TicketID != testTicketID("b") {
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
	mustAdd(t, node, &Ticket{TicketID: testTicketID("missing"), CreatedAt: 1})
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
	mustAdd(t, node, &Ticket{TicketID: testTicketID("seed"), CreatedAt: 1, StringLists: map[string][]string{"common": {"x"}}})
	mustAdd(t, node, &Ticket{TicketID: testTicketID("low"), CreatedAt: 2, StringLists: map[string][]string{"common": {"x"}}, Int64Values: map[string]int64{"priority": 1}})
	mustAdd(t, node, &Ticket{TicketID: testTicketID("high"), CreatedAt: 3, StringLists: map[string][]string{"common": {"x"}}, Int64Values: map[string]int64{"priority": 10}})
	matches, err := produceTestRound(node, 100, Facts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || matches[0].Tickets[1].TicketID != testTicketID("high") {
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
	if !node.Remove(testTicketID("a")) || node.Remove(testTicketID("a")) {
		t.Fatal("remove should succeed exactly once")
	}
	if node.prefilterStore.Len() != 0 {
		t.Fatal("removed ticket remained active")
	}
}

func TestGetReturnsIndependentTicketCopy(t *testing.T) {
	node := mustLogicalNode(t, prefilterConfigForField("partition"), NewRuleSet(), LogicalNodeConfig{})
	mustAdd(t, node, testTicket("a", 1, "blue"))
	first, ok := node.Get(testTicketID("a"))
	if !ok {
		t.Fatal("ticket was not found")
	}
	first.StringLists["partition"][0] = "mutated"
	first.StringLists["added"] = []string{"only-in-get-result"}
	second, ok := node.Get(testTicketID("a"))
	if !ok {
		t.Fatal("ticket was not found on the second Get")
	}
	if first == second {
		t.Fatal("Get returned the LogicalNode-owned Ticket")
	}
	if second.StringLists["partition"][0] != "blue" {
		t.Fatalf("mutating Get result changed pool state: %#v", second)
	}
	if _, exists := second.StringLists["added"]; exists {
		t.Fatalf("mutating Get result map changed pool state: %#v", second)
	}
}

func TestPoolCopiesRuleSetContainer(t *testing.T) {
	rules := NewRuleSet()
	node := mustLogicalNode(t, prefilter.Config{Root: prefilter.None()}, rules, LogicalNodeConfig{})
	rules.Use(FuncGroupEvaluator{
		EvaluatorFlagsValue: GroupEvaluatorForceStart,
		AllowFn:             func(_ GroupEvaluatorContext, _ []*Ticket, _ *Ticket) bool { return true },
	})
	mustAdd(t, node, &Ticket{TicketID: testTicketID("a")})
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
	seed := &Ticket{TicketID: testTicketID("seed"), Uint64Lists: map[string][]uint64{"uint64_dimension": {10, 20}}}
	mustAdd(t, node, seed)
	seed.Uint64Lists["uint64_dimension"][0] = 999
	mustAdd(t, node, &Ticket{TicketID: testTicketID("candidate"), Uint64Lists: map[string][]uint64{"uint64_dimension": {20}}})
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
	if _, err := node.Add(&Ticket{TicketID: testTicketID("b"), StringLists: map[string][]string{"partition": {"x", "y"}}}); err == nil {
		t.Fatal("document key overflow was accepted")
	}
}

func TestDocIDIsReusedAfterTicketLeavesPool(t *testing.T) {
	node := mustLogicalNode(t, prefilter.Config{Root: prefilter.None()}, NewRuleSet(), LogicalNodeConfig{})
	firstDocID := mustAdd(t, node, testTicket("first", 1, ""))
	secondDocID := mustAdd(t, node, testTicket("second", 2, ""))
	if !node.Remove(testTicketID("first")) {
		t.Fatal("remove first ticket failed")
	}
	reusedDocID := mustAdd(t, node, testTicket("replacement", 3, ""))
	if reusedDocID != firstDocID {
		t.Fatalf("reused DocID=%d want=%d", reusedDocID, firstDocID)
	}
	if reusedDocID == secondDocID {
		t.Fatal("allocator reused an active DocID")
	}
	if err := node.BeginMatchRound(10); err != nil {
		t.Fatal(err)
	}
	seen := make(map[TicketID]int)
	for node.hasUntriedSeed() {
		seed := node.nextSeed()
		seen[seed.TicketID]++
	}
	if len(seen) != 2 || seen[testTicketID("second")] != 1 || seen[testTicketID("replacement")] != 1 {
		t.Fatalf("reused DocID corrupted arrival order: %#v", seen)
	}
}

func TestMatchedDocIDIsReusableBeforeNextRound(t *testing.T) {
	force := FuncGroupEvaluator{
		EvaluatorFlagsValue: GroupEvaluatorForceStart,
		AllowFn:             func(GroupEvaluatorContext, []*Ticket, *Ticket) bool { return true },
	}
	node := mustLogicalNode(t, prefilter.Config{Root: prefilter.None()}, NewRuleSet(force), LogicalNodeConfig{})
	docID := mustAdd(t, node, testTicket("matched", 1, ""))
	match, err := produceTestMatch(node, 1, Facts{})
	if err != nil || match == nil {
		t.Fatalf("ProduceMatch: match=%#v err=%v", match, err)
	}
	reusedDocID := mustAdd(t, node, testTicket("next-round", 2, ""))
	if reusedDocID != docID {
		t.Fatalf("reused DocID=%d want=%d", reusedDocID, docID)
	}
	if err := node.BeginMatchRound(2); err != nil {
		t.Fatal(err)
	}
	if seed := node.nextSeed(); seed == nil || seed.TicketID != testTicketID("next-round") {
		t.Fatalf("next round seed=%#v", seed)
	}
}

func TestRejectedAddReturnsAllocatedDocID(t *testing.T) {
	config := prefilterConfigForField("partition")
	config.Indexes = []prefilter.IndexSpec{prefilter.NewMultiValueIndex(prefilter.MultiValueIndexConfig{
		Name: "partition_index", Field: "partition", MaxDocumentValues: 1, MaxQueryValues: 2,
	})}
	node := mustLogicalNode(t, config, NewRuleSet(), LogicalNodeConfig{})
	if _, err := node.Add(&Ticket{
		TicketID:    testTicketID("invalid"),
		StringLists: map[string][]string{"partition": {"x", "y"}},
	}); err == nil {
		t.Fatal("invalid Ticket was accepted")
	}
	docID := mustAdd(t, node, testTicket("valid", 1, "x"))
	if docID != 1 {
		t.Fatalf("DocID after rejected Add=%d want=1", docID)
	}
}

func TestRuntimeQueryErrorRetainsSeed(t *testing.T) {
	node := mustLogicalNode(t, prefilterConfigForField("partition"), NewRuleSet(minimumGroupSize(2)), LogicalNodeConfig{})
	mustAdd(t, node, &Ticket{TicketID: testTicketID("missing"), CreatedAt: 1})
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
		Key:    identity.LogicalNodeKey{Rule: identity.RuleKey{RuleID: 1}, PlacementID: "test-placement"},
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
	return &Ticket{TicketID: testTicketID(id), CreatedAt: createdAt, StringLists: fields}
}

func testTicketID(label string) TicketID {
	const offset64 = uint64(14695981039346656037)
	const prime64 = uint64(1099511628211)
	value := offset64
	for index := 0; index < len(label); index++ {
		value ^= uint64(label[index])
		value *= prime64
	}
	if value == 0 {
		return 1
	}
	return value
}
