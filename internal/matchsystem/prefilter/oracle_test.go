package prefilter

import (
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

func TestIndexedResultMatchesScanOracle(t *testing.T) {
	config := Config{
		Indexes: []IndexSpec{
			NewMultiValueIndex(MultiValueIndexConfig{Name: "dimension", Field: "dimension", MaxDocumentValues: 4, MaxQueryValues: 4}),
			NewMultiValueIndex(MultiValueIndexConfig{Name: "category", Field: "category", MaxDocumentValues: 4, MaxQueryValues: 4}),
			NewInt64RangeIndex(Int64RangeIndexConfig{Name: "numeric", Field: "numeric"}),
		},
		Root: And(
			Lookup(StringQuery{Index: "dimension", Values: SeedStrings("dimension")}),
			Lookup(Int64RangeQuery{Index: "numeric", Min: LiteralInt64(-10), Max: LiteralInt64(10)}),
			Exclude(Lookup(StringQuery{Index: "category", Values: LiteralStrings("blocked")})),
		),
	}
	store := mustIndexStore(t, config)
	rng := rand.New(rand.NewSource(7))
	documents := make(map[uint32]Document)
	for id := uint32(1); id <= 500; id++ {
		dimension := []string{"a", "b", "c"}[rng.Intn(3)]
		category := "allowed"
		if rng.Intn(5) == 0 {
			category = "blocked"
		}
		document := Document{DocID: id, StringLists: map[string][]string{"dimension": {dimension}, "category": {category}}, Int64Values: map[string]int64{"numeric": int64(rng.Intn(61) - 30)}}
		if err := store.Add(document); err != nil {
			t.Fatal(err)
		}
		documents[id] = document
	}
	for id := uint32(7); id <= 500; id += 7 {
		store.Remove(id)
		delete(documents, id)
	}
	seed := documents[1]
	result, err := beginTick(t, store, Facts{}).Candidates(seed, Facts{})
	if err != nil {
		t.Fatal(err)
	}
	want := make([]uint32, 0)
	for id, document := range documents {
		if overlaps(document.StringLists["dimension"], seed.StringLists["dimension"]) && document.Int64Values["numeric"] >= -10 && document.Int64Values["numeric"] <= 10 && !containsString(document.StringLists["category"], "blocked") {
			want = append(want, id)
		}
	}
	sort.Slice(want, func(i, j int) bool { return want[i] < want[j] })
	if got := result.IDs(); !reflect.DeepEqual(got, want) {
		t.Fatalf("indexed result differs from oracle\ngot=%v\nwant=%v", got, want)
	}
}

func overlaps(left, right []string) bool {
	for _, a := range left {
		if containsString(right, a) {
			return true
		}
	}
	return false
}
func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
