package prefilter

import (
	"math"
	"reflect"
	"testing"
)

func TestAndExcludeAndDynamicInt64Range(t *testing.T) {
	radius := WaitSteps(WaitStep{WaitMillis: 0, Value: 10}, WaitStep{WaitMillis: 50, Value: 50})
	config := Config{
		Indexes: []IndexSpec{
			NewMultiValueIndex(MultiValueIndexConfig{Name: "dimension", Field: "dimension", MaxDocumentValues: 8, MaxQueryValues: 8}),
			NewMultiValueIndex(MultiValueIndexConfig{Name: "category", Field: "category", MaxDocumentValues: 8, MaxQueryValues: 8}),
			NewInt64RangeIndex(Int64RangeIndexConfig{Name: "numeric", Field: "numeric_value"}),
		},
		Root: And(
			Lookup(StringQuery{Index: "dimension", Values: SeedStrings("dimension")}),
			Lookup(Int64RangeQuery{Index: "numeric", Min: SubInt64(SeedInt64("numeric_value"), radius), Max: AddInt64(SeedInt64("numeric_value"), radius)}),
			Exclude(Lookup(StringQuery{Index: "category", Values: SeedStrings("excluded")})),
		),
	}
	store := mustIndexStore(t, config)
	docs := []Document{
		{DocID: 1, CreatedAt: 0, StringLists: map[string][]string{"dimension": {"x"}, "category": {"allowed"}, "excluded": {"blocked"}}, Int64Values: map[string]int64{"numeric_value": 100}},
		{DocID: 2, StringLists: map[string][]string{"dimension": {"x"}, "category": {"allowed"}}, Int64Values: map[string]int64{"numeric_value": 105}},
		{DocID: 3, StringLists: map[string][]string{"dimension": {"x"}, "category": {"blocked"}}, Int64Values: map[string]int64{"numeric_value": 100}},
		{DocID: 4, StringLists: map[string][]string{"dimension": {"y"}, "category": {"allowed"}}, Int64Values: map[string]int64{"numeric_value": 100}},
		{DocID: 5, StringLists: map[string][]string{"dimension": {"x"}, "category": {"allowed"}}, Int64Values: map[string]int64{"numeric_value": 130}},
	}
	addDocuments(t, store, docs...)
	assertIDs(t, candidates(t, store.BeginTick(0, Facts{}), docs[0]), 1, 2)
	assertIDs(t, candidates(t, store.BeginTick(60, Facts{}), docs[0]), 1, 2, 5)
}

func TestOrAndIfOnlyEvaluateSelectedPath(t *testing.T) {
	config := Config{
		Indexes: []IndexSpec{NewMultiValueIndex(MultiValueIndexConfig{Name: "dimension", Field: "dimension", MaxDocumentValues: 8, MaxQueryValues: 8})},
		Facts:   []FactSpec{{Name: "unselected", Type: FactTypeStrings, MaxValues: 4}},
		Root: If(
			GreaterOrEqual(SeedWaitMillis(), LiteralInt64(10)),
			Or(
				Lookup(StringQuery{Index: "dimension", Values: LiteralStrings("x")}),
				Lookup(StringQuery{Index: "dimension", Values: LiteralStrings("y")}),
			),
			Lookup(StringQuery{Index: "dimension", Values: FactStrings("unselected")}),
		),
	}
	store := mustIndexStore(t, config)
	seed := Document{DocID: 1, CreatedAt: 0, StringLists: map[string][]string{"dimension": {"x"}}}
	addDocuments(t, store, seed, Document{DocID: 2, StringLists: map[string][]string{"dimension": {"y"}}}, Document{DocID: 3, StringLists: map[string][]string{"dimension": {"z"}}})
	// The Else query would fail because its Fact is absent. It must not be bound.
	assertIDs(t, candidates(t, store.BeginTick(10, Facts{}), seed), 1, 2)
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
	docs := []Document{
		{DocID: 1, StringLists: map[string][]string{"scope": {"yes"}}},
		{DocID: 2, StringLists: map[string][]string{"scope": {"yes"}, "excluded": {"yes"}}},
		{DocID: 3, StringLists: map[string][]string{"scope": {"yes"}, "excluded": {"yes"}, "extra": {"yes"}}},
	}
	addDocuments(t, store, docs...)
	assertIDs(t, candidates(t, store.BeginTick(0, Facts{}), docs[0]), 1, 3)
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
	docs := []Document{
		{DocID: 1, StringLists: map[string][]string{"broad": {"yes"}, "narrow": {"yes"}}},
		{DocID: 2, StringLists: map[string][]string{"broad": {"yes"}}},
		{DocID: 3, StringLists: map[string][]string{"broad": {"yes"}}},
	}
	addDocuments(t, store, docs...)
	result, stats, err := store.BeginTick(0, Facts{}).CandidatesWithStats(docs[0])
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
	docs := []Document{{DocID: 1, Int64Values: map[string]int64{"value": math.MinInt64}}, {DocID: 2, Int64Values: map[string]int64{"value": math.MaxInt64}}}
	addDocuments(t, store, docs...)
	assertIDs(t, candidates(t, store.BeginTick(0, Facts{}), docs[0]), 1, 2)
	if !store.Remove(2) || store.Remove(2) {
		t.Fatal("Remove should succeed once")
	}
	assertIDs(t, candidates(t, store.BeginTick(0, Facts{}), docs[0]), 1)
}

func TestRuntimeQueryKeyLimitIsError(t *testing.T) {
	config := Config{
		Indexes: []IndexSpec{NewMultiValueIndex(MultiValueIndexConfig{Name: "dimension", Field: "dimension", MaxDocumentValues: 4, MaxQueryValues: 1})},
		Root:    Lookup(StringQuery{Index: "dimension", Values: SeedStrings("query")}),
	}
	store := mustIndexStore(t, config)
	seed := Document{DocID: 1, StringLists: map[string][]string{"dimension": {"x"}, "query": {"x", "y"}}}
	addDocuments(t, store, seed)
	if _, err := store.BeginTick(0, Facts{}).Candidates(seed); err == nil {
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
	docs := []Document{
		{DocID: 1, Uint64Lists: map[string][]uint64{"uint64_dimension": {1}, "query_uint64": {1, 2, 2}}},
		{DocID: 2, Uint64Lists: map[string][]uint64{"uint64_dimension": {2, 2, 3}}},
		{DocID: 3, Uint64Lists: map[string][]uint64{"uint64_dimension": {7}}},
		{DocID: 4, Uint64Lists: map[string][]uint64{"uint64_dimension": {math.MaxUint64}}},
		{DocID: 5, Uint64Lists: map[string][]uint64{"uint64_dimension": {0}}},
	}
	addDocuments(t, store, docs...)
	facts := Facts{Uint64Lists: map[string][]uint64{"extra_uint64": {7, 7}}}
	session := store.BeginTick(0, facts)
	facts.Uint64Lists["extra_uint64"][0] = 999
	assertIDs(t, candidates(t, session, docs[0]), 1, 2, 3, 4, 5)
	if !store.Remove(3) {
		t.Fatal("Remove failed")
	}
	facts.Uint64Lists["extra_uint64"][0] = 7
	assertIDs(t, candidates(t, store.BeginTick(0, facts), docs[0]), 1, 2, 4, 5)
}

func TestUint64QueryLimits(t *testing.T) {
	config := Config{
		Indexes: []IndexSpec{NewMultiValueIndex(MultiValueIndexConfig{Name: "u", Field: "u", KeyType: KeyTypeUint64, MaxDocumentValues: 2, MaxQueryValues: 1})},
		Root:    Lookup(Uint64Query{Index: "u", Values: SeedUint64s("query")}),
	}
	store := mustIndexStore(t, config)
	if err := store.Add(Document{DocID: 1, Uint64Lists: map[string][]uint64{"u": {1, 2, 3}, "query": {1}}}); err == nil {
		t.Fatal("uint64 document key overflow was accepted")
	}
	seed := Document{DocID: 2, Uint64Lists: map[string][]uint64{"u": {1}, "query": {1, 2}}}
	addDocuments(t, store, seed)
	if _, err := store.BeginTick(0, Facts{}).Candidates(seed); err == nil {
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
	docs := []Document{
		{DocID: 1, Uint64Lists: map[string][]uint64{"uint64_broad": {1}, "uint64_narrow": {2}}},
		{DocID: 2, Uint64Lists: map[string][]uint64{"uint64_broad": {1}}},
		{DocID: 3, Uint64Lists: map[string][]uint64{"uint64_broad": {1}}},
	}
	addDocuments(t, store, docs...)
	result, stats, err := store.BeginTick(0, Facts{}).CandidatesWithStats(docs[0])
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
func addDocuments(t *testing.T, store *IndexStore, docs ...Document) {
	t.Helper()
	for _, doc := range docs {
		if err := store.Add(doc); err != nil {
			t.Fatalf("Add(%d): %v", doc.DocID, err)
		}
	}
}
func candidates(t *testing.T, session *TickSession, seed Document) *DocSet {
	t.Helper()
	result, err := session.Candidates(seed)
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
