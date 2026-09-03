package prefilter

import (
	"math/rand"
	"sort"
	"testing"

	"github.com/RoaringBitmap/roaring/v2"

	"matchSystem/internal/common"
	"matchSystem/internal/matchsystem/contract"
)

func TestInt64RangeIndexBlockLookupMatchesPostingUnion(t *testing.T) {
	values := []int64{-300, -17, -1, 0, 4, 19, 64, 128, 1024}
	for _, blockSize := range []int{8, 16, 32} {
		t.Run("block-size-"+itoaInt64RangeTest(blockSize), func(t *testing.T) {
			index := newInt64RangeTestIndex(blockSize)
			for docID := uint32(1); docID <= 900; docID++ {
				value := values[(int(docID)-1)%len(values)]
				if docID%17 == 0 {
					value = int64(docID*13) - 7000
				}
				index.add(docID, int64RangeTestTicket(value))
			}
			index.prepare()

			rng := rand.New(rand.NewSource(20260903))
			queries := []boundIndexQuery{
				{kind: boundQueryRange, min: -300, max: -300},
				{kind: boundQueryRange, min: -17, max: 19},
				{kind: boundQueryRange, min: -18, max: 18},
				{kind: boundQueryRange, min: -100000, max: 100000},
				{kind: boundQueryRange, min: 100000, max: 100001},
				{kind: boundQueryRange, min: 5, max: 4},
			}
			for queryIndex := 0; queryIndex < 250; queryIndex++ {
				min := int64(rng.Intn(16001) - 8000)
				max := int64(rng.Intn(16001) - 8000)
				if queryIndex%11 != 0 && min > max {
					min, max = max, min
				}
				queries = append(queries, boundIndexQuery{kind: boundQueryRange, min: min, max: max})
			}

			for queryIndex, query := range queries {
				expected := naiveInt64RangeDocs(index, query)
				actual, err := index.lookup(query)
				if err != nil {
					t.Fatalf("query %d lookup: %v", queryIndex, err)
				}
				assertInt64RangeBitmapEquals(t, expected, actual, "query "+itoaInt64RangeTest(queryIndex))

				estimate, err := index.estimate(query)
				if err != nil {
					t.Fatalf("query %d estimate: %v", queryIndex, err)
				}
				if estimate != uint64(len(expected)) {
					t.Fatalf("query %d estimate=%d, want %d", queryIndex, estimate, len(expected))
				}

				// lookup must return an owned bitmap. Mutating a previous result
				// cannot change postings or an aggregate block used by later calls.
				actual.Add(0xfffffffe)
				if len(expected) > 0 {
					actual.Remove(expected[0])
				}
				repeated, err := index.lookup(query)
				if err != nil {
					t.Fatalf("query %d repeated lookup: %v", queryIndex, err)
				}
				assertInt64RangeBitmapEquals(t, expected, repeated, "repeated query "+itoaInt64RangeTest(queryIndex))
			}
		})
	}
}

func TestInt64RangeIndexBlockBoundariesAndRebuild(t *testing.T) {
	index := newInt64RangeTestIndex(2)
	for docID, value := range []int64{-9, -4, 0, 7, 12, 19} {
		index.add(uint32(docID+1), int64RangeTestTicket(value))
	}
	index.prepare()

	if got, want := index.sortedValues, []int64{-9, -4, 0, 7, 12, 19}; !equalInt64RangeValues(got, want) {
		t.Fatalf("sorted values=%v, want %v", got, want)
	}
	if got, want := len(index.blocks), 3; got != want {
		t.Fatalf("block count=%d, want %d", got, want)
	}
	for _, test := range []struct {
		name     string
		min, max int64
		want     []uint32
	}{
		{name: "single value", min: -4, max: -4, want: []uint32{2}},
		{name: "between values", min: -8, max: 6, want: []uint32{2, 3}},
		{name: "cross block", min: -4, max: 12, want: []uint32{2, 3, 4, 5}},
		{name: "missing interior", min: 1, max: 18, want: []uint32{4, 5}},
		{name: "below and above", min: -100, max: 100, want: []uint32{1, 2, 3, 4, 5, 6}},
		{name: "empty", min: 20, max: 10, want: nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			query := boundIndexQuery{kind: boundQueryRange, min: test.min, max: test.max}
			actual, err := index.lookup(query)
			if err != nil {
				t.Fatal(err)
			}
			assertInt64RangeBitmapEquals(t, test.want, actual, "boundary")
			estimate, err := index.estimate(query)
			if err != nil {
				t.Fatal(err)
			}
			if estimate != uint64(len(test.want)) {
				t.Fatalf("estimate=%d, want %d", estimate, len(test.want))
			}
		})
	}

	// Removing the only document for a value and adding a new distinct value
	// dirties the directory; Prepare rebuilds the sorted values and blocks.
	index.remove(2) // -4 disappears.
	index.remove(6) // 19 disappears.
	index.add(7, int64RangeTestTicket(1000))
	if !index.valuesDirty {
		t.Fatal("distinct-value changes did not dirty the directory")
	}
	index.prepare()
	if got, want := index.sortedValues, []int64{-9, 0, 7, 12, 1000}; !equalInt64RangeValues(got, want) {
		t.Fatalf("rebuilt sorted values=%v, want %v", got, want)
	}
	if got, want := len(index.blocks), 3; got != want {
		t.Fatalf("rebuilt block count=%d, want %d", got, want)
	}
	if got, want := len(index.blockByValue), len(index.postingsByValue); got != want {
		t.Fatalf("block directory size=%d, want active value count %d", got, want)
	}
	if got := int64RangeBlockCardinality(index); got != len(index.valueByDoc) {
		t.Fatalf("aggregate cardinality=%d, want active docs %d", got, len(index.valueByDoc))
	}

	// Existing values update their aggregate immediately, without requiring a
	// directory rebuild. This also exercises a value in the middle block.
	index.add(8, int64RangeTestTicket(7))
	query := boundIndexQuery{kind: boundQueryRange, min: 7, max: 7}
	actual, err := index.lookup(query)
	if err != nil {
		t.Fatal(err)
	}
	assertInt64RangeBitmapEquals(t, []uint32{4, 8}, actual, "existing-value add")
	if index.valuesDirty {
		t.Fatal("adding an existing value dirtied the directory")
	}
}

func TestInt64RangeIndexBlockExtremeInt64Bounds(t *testing.T) {
	index := newInt64RangeTestIndex(2)
	index.add(1, int64RangeTestTicket(-1<<63))
	index.add(2, int64RangeTestTicket(-1))
	index.add(3, int64RangeTestTicket(1<<63-1))
	index.prepare()

	for _, test := range []struct {
		name     string
		min, max int64
		want     []uint32
	}{
		{name: "minimum only", min: -1 << 63, max: -1 << 63, want: []uint32{1}},
		{name: "maximum only", min: 1<<63 - 1, max: 1<<63 - 1, want: []uint32{3}},
		{name: "minimum through maximum", min: -1 << 63, max: 1<<63 - 1, want: []uint32{1, 2, 3}},
		{name: "minimum through negative", min: -1 << 63, max: -2, want: []uint32{1}},
		{name: "positive through maximum", min: 0, max: 1<<63 - 1, want: []uint32{3}},
	} {
		t.Run(test.name, func(t *testing.T) {
			query := boundIndexQuery{kind: boundQueryRange, min: test.min, max: test.max}
			actual, err := index.lookup(query)
			if err != nil {
				t.Fatal(err)
			}
			assertInt64RangeBitmapEquals(t, test.want, actual, "extreme bound")
			estimate, err := index.estimate(query)
			if err != nil {
				t.Fatal(err)
			}
			if estimate != uint64(len(test.want)) {
				t.Fatalf("estimate=%d, want %d", estimate, len(test.want))
			}
		})
	}
}

func TestInt64RangeIndexBlockChurnKeepsAggregatesExact(t *testing.T) {
	index := newInt64RangeTestIndex(4)
	active := make(map[uint32]int64)
	for operation := 0; operation < 600; operation++ {
		docID := uint32(operation%73 + 1)
		if _, ok := active[docID]; ok {
			index.remove(docID)
			delete(active, docID)
		}
		value := int64((operation*29)%127 - 63)
		index.add(docID, int64RangeTestTicket(value))
		active[docID] = value
		if operation%9 == 0 {
			index.prepare()
			assertInt64RangeIndexConsistent(t, index, active)
			min := int64((operation*31)%201 - 100)
			max := int64((operation*47)%201 - 100)
			if operation%2 != 0 && min > max {
				min, max = max, min
			}
			query := boundIndexQuery{kind: boundQueryRange, min: min, max: max}
			expected := naiveInt64RangeDocs(index, query)
			actual, err := index.lookup(query)
			if err != nil {
				t.Fatalf("churn operation %d lookup: %v", operation, err)
			}
			assertInt64RangeBitmapEquals(t, expected, actual, "churn operation "+itoaInt64RangeTest(operation))
		}
	}

	for docID := range active {
		if docID%3 == 0 {
			index.remove(docID)
			delete(active, docID)
		}
	}
	index.prepare()
	assertInt64RangeIndexConsistent(t, index, active)
	if got, want := len(index.blockByValue), len(index.postingsByValue); got != want {
		t.Fatalf("block directory size=%d, want %d after churn", got, want)
	}
}

func newInt64RangeTestIndex(blockSize int) *int64RangeIndex {
	index := newInt64RangeIndex(indexSpec{
		name: "score",
		kind: contract.IndexTypeInt64Range,
	})
	index.blockSize = blockSize
	return index
}

func int64RangeTestTicket(value int64) *common.Ticket {
	return &common.Ticket{Int64Values: map[string]int64{"score": value}}
}

func naiveInt64RangeDocs(index *int64RangeIndex, query boundIndexQuery) []uint32 {
	if query.min > query.max {
		return nil
	}
	result := make([]uint32, 0)
	for docID, value := range index.valueByDoc {
		if value >= query.min && value <= query.max {
			result = append(result, docID)
		}
	}
	sort.Slice(result, func(left, right int) bool { return result[left] < result[right] })
	return result
}

func assertInt64RangeBitmapEquals(t *testing.T, want []uint32, got *roaring.Bitmap, context string) {
	t.Helper()
	actual := got.ToArray()
	if len(actual) != len(want) {
		t.Fatalf("%s: ids=%v, want %v", context, actual, want)
	}
	for index := range want {
		if actual[index] != want[index] {
			t.Fatalf("%s: ids=%v, want %v", context, actual, want)
		}
	}
}

func assertInt64RangeIndexConsistent(t *testing.T, index *int64RangeIndex, active map[uint32]int64) {
	t.Helper()
	if len(index.valueByDoc) != len(active) {
		t.Fatalf("valueByDoc size=%d, want %d", len(index.valueByDoc), len(active))
	}
	for docID, value := range active {
		if got, ok := index.valueByDoc[docID]; !ok || got != value {
			t.Fatalf("valueByDoc[%d]=%d,%v, want %d,true", docID, got, ok, value)
		}
		posting := index.postingsByValue[value]
		if posting == nil || !posting.Contains(docID) {
			t.Fatalf("posting for value %d does not contain doc %d", value, docID)
		}
	}
	if got := int64RangeBlockCardinality(index); got != len(active) {
		t.Fatalf("aggregate cardinality=%d, want %d", got, len(active))
	}
}

func int64RangeBlockCardinality(index *int64RangeIndex) int {
	total := 0
	for _, block := range index.blocks {
		if block.bitmap != nil {
			total += int(block.bitmap.GetCardinality())
		}
	}
	return total
}

func equalInt64RangeValues(left, right []int64) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func itoaInt64RangeTest(value int) string {
	if value == 0 {
		return "0"
	}
	result := ""
	if value < 0 {
		result = "-"
		value = -value
	}
	digits := [20]byte{}
	position := len(digits)
	for value > 0 {
		position--
		digits[position] = byte('0' + value%10)
		value /= 10
	}
	return result + string(digits[position:])
}
