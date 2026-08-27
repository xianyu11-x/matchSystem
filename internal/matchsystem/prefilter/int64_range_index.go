package prefilter

import (
	"fmt"
	"sort"

	"github.com/RoaringBitmap/roaring/v2"

	"matchSystem/internal/common"
)

type int64RangeIndex struct {
	spec            indexSpec
	postingsByValue map[int64]*roaring.Bitmap
	valueByDoc      map[uint32]int64
	sortedValues    []int64
	valuesDirty     bool
}

func newInt64RangeIndex(spec indexSpec) *int64RangeIndex {
	return &int64RangeIndex{spec: spec, postingsByValue: make(map[int64]*roaring.Bitmap), valueByDoc: make(map[uint32]int64)}
}
func (*int64RangeIndex) validate(*common.Ticket) error { return nil }
func (i *int64RangeIndex) add(docID uint32, ticket *common.Ticket) {
	value, ok := ticket.Int64Values[i.spec.name]
	if !ok {
		return
	}
	posting := i.postingsByValue[value]
	if posting == nil {
		posting = roaring.New()
		i.postingsByValue[value] = posting
		i.valuesDirty = true
	}
	posting.Add(docID)
	i.valueByDoc[docID] = value
}
func (i *int64RangeIndex) remove(docID uint32) {
	value, ok := i.valueByDoc[docID]
	if !ok {
		return
	}
	posting := i.postingsByValue[value]
	if posting != nil {
		posting.Remove(docID)
		if posting.IsEmpty() {
			delete(i.postingsByValue, value)
			i.valuesDirty = true
		}
	}
	delete(i.valueByDoc, docID)
}

// prepare refreshes the sorted distinct-key directory after Add/Remove.
// The owning IndexStore calls it at the start of a TickSession while all
// mutations are stopped, so this is an explicit single-goroutine read barrier
// rather than a concurrent snapshot operation.
func (i *int64RangeIndex) prepare() {
	if i.valuesDirty {
		i.sortedValues = i.sortedValues[:0]
		for value := range i.postingsByValue {
			i.sortedValues = append(i.sortedValues, value)
		}
		sort.Slice(i.sortedValues, func(a, b int) bool { return i.sortedValues[a] < i.sortedValues[b] })
		i.valuesDirty = false
	}
}

func (i *int64RangeIndex) rangeKeys(q boundIndexQuery) []int64 {
	start := sort.Search(len(i.sortedValues), func(n int) bool { return i.sortedValues[n] >= q.min })
	end := sort.Search(len(i.sortedValues), func(n int) bool { return i.sortedValues[n] > q.max })
	return i.sortedValues[start:end]
}
func (i *int64RangeIndex) estimate(query boundIndexQuery) (uint64, error) {
	if query.kind != boundQueryRange {
		return 0, fmt.Errorf("int64 range index received incompatible query")
	}
	var estimate uint64
	for _, value := range i.rangeKeys(query) {
		estimate += i.postingsByValue[value].GetCardinality()
	}
	return estimate, nil
}
func (i *int64RangeIndex) lookup(query boundIndexQuery) (*roaring.Bitmap, error) {
	if query.kind != boundQueryRange {
		return nil, fmt.Errorf("int64 range index received incompatible query")
	}
	out := roaring.New()
	for _, value := range i.rangeKeys(query) {
		out.Or(i.postingsByValue[value])
	}
	return out, nil
}
func (i *int64RangeIndex) contains(query boundIndexQuery, docID uint32) (bool, error) {
	if query.kind != boundQueryRange {
		return false, fmt.Errorf("int64 range index received incompatible query")
	}
	value, ok := i.valueByDoc[docID]
	return ok && value >= query.min && value <= query.max, nil
}
