package prefilter

import (
	"errors"
	"math"
	"reflect"
	"testing"

	"matchSystem/internal/common"
)

func TestAndExcludeAndDynamicInt64Range(t *testing.T) {
	radius := StepInt64(FactInt64("wait_millis"), Int64Step{At: 0, Value: 10}, Int64Step{At: 50, Value: 50})
	config := Config{
		Indexes: []IndexSpec{
			NewMultiValueIndex(MultiValueIndexConfig{Name: "dimension", Field: "dimension", MaxDocumentValues: 8, MaxQueryValues: 8}),
			NewMultiValueIndex(MultiValueIndexConfig{Name: "category", Field: "category", MaxDocumentValues: 8, MaxQueryValues: 8}),
			NewInt64RangeIndex(Int64RangeIndexConfig{Name: "numeric", Field: "numeric_value"}),
		},
		Facts: []FactSpec{{Name: "wait_millis", Type: FactTypeInt64}},
		Root: And(
			Lookup(StringQuery{Index: "dimension", Values: SeedStrings("dimension")}),
			Lookup(Int64RangeQuery{Index: "numeric", Min: SubInt64(SeedInt64("numeric_value"), radius), Max: AddInt64(SeedInt64("numeric_value"), radius)}),
			Exclude(Lookup(StringQuery{Index: "category", Values: SeedStrings("excluded")})),
		),
	}
	store := mustIndexStore(t, config)
	docs := []indexedTestTicket{
		indexedTicket(1, &common.Ticket{CreatedAt: 0, StringLists: map[string][]string{"dimension": {"x"}, "category": {"allowed"}, "excluded": {"blocked"}}, Int64Values: map[string]int64{"numeric_value": 100}}),
		indexedTicket(2, &common.Ticket{StringLists: map[string][]string{"dimension": {"x"}, "category": {"allowed"}}, Int64Values: map[string]int64{"numeric_value": 105}}),
		indexedTicket(3, &common.Ticket{StringLists: map[string][]string{"dimension": {"x"}, "category": {"blocked"}}, Int64Values: map[string]int64{"numeric_value": 100}}),
		indexedTicket(4, &common.Ticket{StringLists: map[string][]string{"dimension": {"y"}, "category": {"allowed"}}, Int64Values: map[string]int64{"numeric_value": 100}}),
		indexedTicket(5, &common.Ticket{StringLists: map[string][]string{"dimension": {"x"}, "category": {"allowed"}}, Int64Values: map[string]int64{"numeric_value": 130}}),
	}
	addTickets(t, store, docs...)
	session := beginTick(t, store, Facts{})
	assertIDs(t, candidates(t, session, docs[0], Facts{Int64Values: map[string]int64{"wait_millis": 0}}), 1, 2)
	assertIDs(t, candidates(t, session, docs[0], Facts{Int64Values: map[string]int64{"wait_millis": 60}}), 1, 2, 5)
}

func TestOrAndIfOnlyEvaluateSelectedPath(t *testing.T) {
	config := Config{
		Indexes: []IndexSpec{NewMultiValueIndex(MultiValueIndexConfig{Name: "dimension", Field: "dimension", MaxDocumentValues: 8, MaxQueryValues: 8})},
		Facts: []FactSpec{
			{Name: "unselected", Type: FactTypeStrings, MaxValues: 4},
			{Name: "wait_millis", Type: FactTypeInt64},
		},
		Root: If(
			GreaterOrEqual(FactInt64("wait_millis"), LiteralInt64(10)),
			Or(
				Lookup(StringQuery{Index: "dimension", Values: LiteralStrings("x")}),
				Lookup(StringQuery{Index: "dimension", Values: LiteralStrings("y")}),
			),
			Lookup(StringQuery{Index: "dimension", Values: FactStrings("unselected")}),
		),
	}
	store := mustIndexStore(t, config)
	seed := indexedTicket(1, &common.Ticket{CreatedAt: 0, StringLists: map[string][]string{"dimension": {"x"}}})
	addTickets(t, store, seed, indexedTicket(2, &common.Ticket{StringLists: map[string][]string{"dimension": {"y"}}}), indexedTicket(3, &common.Ticket{StringLists: map[string][]string{"dimension": {"z"}}}))
	// The Else query would fail because its Fact is absent. It must not be bound.
	assertIDs(t, candidates(t, beginTick(t, store, Facts{}), seed, Facts{Int64Values: map[string]int64{"wait_millis": 10}}), 1, 2)
}

func TestNestedOrWithExcludeKeepsInheritedScope(t *testing.T) {
	config := Config{
		Indexes: []IndexSpec{
			NewMultiValueIndex(MultiValueIndexConfig{Name: "scope", Field: "scope", MaxDocumentValues: 4, MaxQueryValues: 4}),
			NewMultiValueIndex(MultiValueIndexConfig{Name: "excluded", Field: "excluded", MaxDocumentValues: 4, MaxQueryValues: 4}),
			NewMultiValueIndex(MultiValueIndexConfig{Name: "extra", Field: "extra", MaxDocumentValues: 4, MaxQueryValues: 4}),
		},
		Root: And(
			Lookup(StringQuery{Index: "scope", Values: LiteralStrings("yes")}),
			Or(
				Exclude(Lookup(StringQuery{Index: "excluded", Values: LiteralStrings("yes")})),
				Lookup(StringQuery{Index: "extra", Values: LiteralStrings("yes")}),
			),
		),
	}
	store := mustIndexStore(t, config)
	docs := []indexedTestTicket{
		indexedTicket(1, &common.Ticket{StringLists: map[string][]string{"scope": {"yes"}}}),
		indexedTicket(2, &common.Ticket{StringLists: map[string][]string{"scope": {"yes"}, "excluded": {"yes"}}}),
		indexedTicket(3, &common.Ticket{StringLists: map[string][]string{"scope": {"yes"}, "excluded": {"yes"}, "extra": {"yes"}}}),
	}
	addTickets(t, store, docs...)
	assertIDs(t, candidates(t, beginTick(t, store, Facts{}), docs[0]), 1, 3)
}

func TestSmallAccumulatorUsesContainsProbe(t *testing.T) {
	config := Config{
		ContainsProbeThreshold: 2,
		Indexes: []IndexSpec{
			NewMultiValueIndex(MultiValueIndexConfig{Name: "broad", Field: "broad", MaxDocumentValues: 4, MaxQueryValues: 4}),
			NewMultiValueIndex(MultiValueIndexConfig{Name: "narrow", Field: "narrow", MaxDocumentValues: 4, MaxQueryValues: 4}),
		},
		Root: And(
			Lookup(StringQuery{Index: "broad", Values: LiteralStrings("yes")}),
			Lookup(StringQuery{Index: "narrow", Values: LiteralStrings("yes")}),
		),
	}
	store := mustIndexStore(t, config)
	docs := []indexedTestTicket{
		indexedTicket(1, &common.Ticket{StringLists: map[string][]string{"broad": {"yes"}, "narrow": {"yes"}}}),
		indexedTicket(2, &common.Ticket{StringLists: map[string][]string{"broad": {"yes"}}}),
		indexedTicket(3, &common.Ticket{StringLists: map[string][]string{"broad": {"yes"}}}),
	}
	addTickets(t, store, docs...)
	result, stats, err := beginTick(t, store, Facts{}).CandidatesWithStats(docs[0].docID, docs[0].Ticket, Facts{})
	if err != nil {
		t.Fatal(err)
	}
	assertIDs(t, result, 1)
	if stats.ContainsCalls == 0 || stats.LookupCalls == 0 {
		t.Fatalf("expected lookup and Contains probe, got %#v", stats)
	}
}

func TestInt64RangeUsesSparseDistinctKeysAndRemove(t *testing.T) {
	config := Config{
		Indexes: []IndexSpec{NewInt64RangeIndex(Int64RangeIndexConfig{Name: "numeric", Field: "value"})},
		Root:    Lookup(Int64RangeQuery{Index: "numeric", Min: LiteralInt64(math.MinInt64), Max: LiteralInt64(math.MaxInt64)}),
	}
	store := mustIndexStore(t, config)
	docs := []indexedTestTicket{
		indexedTicket(1, &common.Ticket{Int64Values: map[string]int64{"value": math.MinInt64}}),
		indexedTicket(2, &common.Ticket{Int64Values: map[string]int64{"value": math.MaxInt64}}),
	}
	addTickets(t, store, docs...)
	assertIDs(t, candidates(t, beginTick(t, store, Facts{}), docs[0]), 1, 2)
	if !store.Remove(2) || store.Remove(2) {
		t.Fatal("Remove should succeed once")
	}
	assertIDs(t, candidates(t, beginTick(t, store, Facts{}), docs[0]), 1)
}

func TestRuntimeQueryKeyLimitIsError(t *testing.T) {
	config := Config{
		Indexes: []IndexSpec{NewMultiValueIndex(MultiValueIndexConfig{Name: "dimension", Field: "dimension", MaxDocumentValues: 4, MaxQueryValues: 1})},
		Root:    Lookup(StringQuery{Index: "dimension", Values: SeedStrings("query")}),
	}
	store := mustIndexStore(t, config)
	seed := indexedTicket(1, &common.Ticket{StringLists: map[string][]string{"dimension": {"x"}, "query": {"x", "y"}}})
	addTickets(t, store, seed)
	if _, err := beginTick(t, store, Facts{}).Candidates(seed.docID, seed.Ticket, Facts{}); err == nil {
		t.Fatal("query key overflow was not rejected")
	}
}

func TestUint64QueryWithSeedFactAndLiteralUnion(t *testing.T) {
	config := Config{
		Indexes: []IndexSpec{NewMultiValueIndex(MultiValueIndexConfig{Name: "uint64_dimension", Field: "uint64_dimension", KeyType: KeyTypeUint64, MaxDocumentValues: 4, MaxQueryValues: 5})},
		Facts:   []FactSpec{{Name: "extra_uint64", Type: FactTypeUint64s, MaxValues: 2}},
		Root: Lookup(Uint64Query{
			Index: "uint64_dimension",
			Values: UnionUint64s(
				SeedUint64s("query_uint64"),
				FactUint64s("extra_uint64"),
				LiteralUint64s(0, math.MaxUint64, math.MaxUint64),
			),
		}),
	}
	store := mustIndexStore(t, config)
	docs := []indexedTestTicket{
		indexedTicket(1, &common.Ticket{Uint64Lists: map[string][]uint64{"uint64_dimension": {1}, "query_uint64": {1, 2, 2}}}),
		indexedTicket(2, &common.Ticket{Uint64Lists: map[string][]uint64{"uint64_dimension": {2, 2, 3}}}),
		indexedTicket(3, &common.Ticket{Uint64Lists: map[string][]uint64{"uint64_dimension": {7}}}),
		indexedTicket(4, &common.Ticket{Uint64Lists: map[string][]uint64{"uint64_dimension": {math.MaxUint64}}}),
		indexedTicket(5, &common.Ticket{Uint64Lists: map[string][]uint64{"uint64_dimension": {0}}}),
	}
	addTickets(t, store, docs...)
	facts := Facts{Uint64Lists: map[string][]uint64{"extra_uint64": {7, 7}}}
	session := beginTick(t, store, facts)
	assertIDs(t, candidates(t, session, docs[0]), 1, 2, 3, 4, 5)
	if !store.Remove(3) {
		t.Fatal("Remove failed")
	}
	// The previous session is no longer used, so its borrowed layer may now be
	// changed before creating the next session.
	facts.Uint64Lists["extra_uint64"] = []uint64{999, 999}
	assertIDs(t, candidates(t, beginTick(t, store, facts), docs[0]), 1, 2, 4, 5)
}

func TestUint64QueryLimits(t *testing.T) {
	config := Config{
		Indexes: []IndexSpec{NewMultiValueIndex(MultiValueIndexConfig{Name: "u", Field: "u", KeyType: KeyTypeUint64, MaxDocumentValues: 2, MaxQueryValues: 1})},
		Root:    Lookup(Uint64Query{Index: "u", Values: SeedUint64s("query")}),
	}
	store := mustIndexStore(t, config)
	if err := store.Add(1, &common.Ticket{Uint64Lists: map[string][]uint64{"u": {1, 2, 3}, "query": {1}}}); err == nil {
		t.Fatal("uint64 document key overflow was accepted")
	}
	seed := indexedTicket(2, &common.Ticket{Uint64Lists: map[string][]uint64{"u": {1}, "query": {1, 2}}})
	addTickets(t, store, seed)
	if _, err := beginTick(t, store, Facts{}).Candidates(seed.docID, seed.Ticket, Facts{}); err == nil {
		t.Fatal("uint64 query key overflow was accepted")
	}
}

func TestUint64QueryUsesContainsProbe(t *testing.T) {
	config := Config{
		ContainsProbeThreshold: 2,
		Indexes: []IndexSpec{
			NewMultiValueIndex(MultiValueIndexConfig{Name: "uint64_broad", Field: "uint64_broad", KeyType: KeyTypeUint64, MaxDocumentValues: 2, MaxQueryValues: 2}),
			NewMultiValueIndex(MultiValueIndexConfig{Name: "uint64_narrow", Field: "uint64_narrow", KeyType: KeyTypeUint64, MaxDocumentValues: 2, MaxQueryValues: 2}),
		},
		Root: And(
			Lookup(Uint64Query{Index: "uint64_broad", Values: LiteralUint64s(1)}),
			Lookup(Uint64Query{Index: "uint64_narrow", Values: LiteralUint64s(2)}),
		),
	}
	store := mustIndexStore(t, config)
	docs := []indexedTestTicket{
		indexedTicket(1, &common.Ticket{Uint64Lists: map[string][]uint64{"uint64_broad": {1}, "uint64_narrow": {2}}}),
		indexedTicket(2, &common.Ticket{Uint64Lists: map[string][]uint64{"uint64_broad": {1}}}),
		indexedTicket(3, &common.Ticket{Uint64Lists: map[string][]uint64{"uint64_broad": {1}}}),
	}
	addTickets(t, store, docs...)
	result, stats, err := beginTick(t, store, Facts{}).CandidatesWithStats(docs[0].docID, docs[0].Ticket, Facts{})
	if err != nil {
		t.Fatal(err)
	}
	assertIDs(t, result, 1)
	if stats.ContainsCalls == 0 {
		t.Fatalf("uint64 Contains probe was not used: %#v", stats)
	}
}

func mustIndexStore(t *testing.T, config Config) *IndexStore {
	t.Helper()
	plan, err := Compile(config)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	store, err := New(plan)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return store
}
func addTickets(t *testing.T, store *IndexStore, docs ...indexedTestTicket) {
	t.Helper()
	for _, doc := range docs {
		if err := store.Add(doc.docID, doc.Ticket); err != nil {
			t.Fatalf("Add(%d): %v", doc.docID, err)
		}
	}
}
func beginTick(t *testing.T, store *IndexStore, facts Facts) *TickSession {
	t.Helper()
	session, err := store.BeginTick(facts)
	if err != nil {
		t.Fatalf("BeginTick: %v", err)
	}
	return session
}

func TestBeginTickBorrowsTickFacts(t *testing.T) {
	store := mustIndexStore(t, Config{Root: None()})
	facts := Facts{Int64Values: map[string]int64{"capacity": 1}}
	session := beginTick(t, store, facts)

	// The caller contract keeps facts immutable while session is alive. Mutating
	// here is only an identity check that guards against reintroducing a clone.
	facts.Int64Values["capacity"] = 2
	if got := session.tickFacts.Int64Values["capacity"]; got != 2 {
		t.Fatalf("BeginTick copied Tick Facts: capacity=%d", got)
	}
}

func TestTicketInputsMustBeNonNil(t *testing.T) {
	store := mustIndexStore(t, Config{Root: None()})
	if err := store.Add(1, nil); err == nil {
		t.Fatal("nil Add Ticket was accepted")
	}
	seed := indexedTicket(1, &common.Ticket{})
	addTickets(t, store, seed)
	_, err := beginTick(t, store, Facts{}).Candidates(seed.docID, nil, Facts{})
	assertPrefilterErrorCode(t, err, "NIL_TICKET")
}

func TestFactLayersRejectTypeAndScopeCollisions(t *testing.T) {
	store := mustIndexStore(t, Config{Root: None()})
	seed := indexedTicket(1, &common.Ticket{})
	addTickets(t, store, seed)

	_, err := store.BeginTick(Facts{
		StringLists: map[string][]string{"duplicate": {"x"}},
		Int64Values: map[string]int64{"duplicate": 1},
	})
	assertPrefilterErrorCode(t, err, "FACT_TYPE_COLLISION")

	session := beginTick(t, store, Facts{Int64Values: map[string]int64{"shared": 1}})
	_, err = session.Candidates(seed.docID, seed.Ticket, Facts{
		StringLists: map[string][]string{"duplicate": {"x"}},
		Uint64Lists: map[string][]uint64{"duplicate": {1}},
	})
	assertPrefilterErrorCode(t, err, "FACT_TYPE_COLLISION")

	seedFacts := Facts{StringLists: map[string][]string{"shared": {"seed"}}}
	_, err = session.Candidates(seed.docID, seed.Ticket, seedFacts)
	assertPrefilterErrorCode(t, err, "FACT_SCOPE_COLLISION")
	if got := seedFacts.StringLists["shared"][0]; got != "seed" {
		t.Fatalf("Candidates mutated Seed Facts: %q", got)
	}
}

func TestSeedFactsTakePartInGenericFactBinding(t *testing.T) {
	store := mustIndexStore(t, Config{
		Indexes: []IndexSpec{NewInt64RangeIndex(Int64RangeIndexConfig{Name: "numeric", Field: "value"})},
		Facts:   []FactSpec{{Name: "radius", Type: FactTypeInt64}},
		Root: Lookup(Int64RangeQuery{
			Index: "numeric",
			Min:   SubInt64(SeedInt64("value"), FactInt64("radius")),
			Max:   AddInt64(SeedInt64("value"), FactInt64("radius")),
		}),
	})
	seed := indexedTicket(1, &common.Ticket{Int64Values: map[string]int64{"value": 100}})
	addTickets(t, store, seed,
		indexedTicket(2, &common.Ticket{Int64Values: map[string]int64{"value": 90}}),
		indexedTicket(3, &common.Ticket{Int64Values: map[string]int64{"value": 80}}),
	)
	session := beginTick(t, store, Facts{})
	_, err := session.Candidates(seed.docID, seed.Ticket, Facts{})
	assertPrefilterErrorCode(t, err, "QUERY_BIND")
	_, err = session.Candidates(seed.docID, seed.Ticket, Facts{StringLists: map[string][]string{"radius": {"wrong-type"}}})
	assertPrefilterErrorCode(t, err, "QUERY_BIND")
	assertIDs(t, candidates(t, session, seed, Facts{Int64Values: map[string]int64{"radius": 10}}), 1, 2)
	assertIDs(t, candidates(t, session, seed, Facts{Int64Values: map[string]int64{"radius": 20}}), 1, 2, 3)
}
func candidates(t *testing.T, session *TickSession, seed indexedTestTicket, seedFacts ...Facts) *DocSet {
	t.Helper()
	facts := Facts{}
	if len(seedFacts) > 0 {
		facts = seedFacts[0]
	}
	result, err := session.Candidates(seed.docID, seed.Ticket, facts)
	if err != nil {
		t.Fatalf("Candidates: %v", err)
	}
	return result
}
func assertIDs(t *testing.T, bitmap *DocSet, want ...uint32) {
	t.Helper()
	if got := bitmap.IDs(); !reflect.DeepEqual(got, want) {
		t.Fatalf("IDs=%v want=%v", got, want)
	}
}

func assertPrefilterErrorCode(t *testing.T, err error, code string) {
	t.Helper()
	var prefilterErr *Error
	if !errors.As(err, &prefilterErr) || prefilterErr.Code != code {
		t.Fatalf("error=%v, want prefilter code %s", err, code)
	}
}
