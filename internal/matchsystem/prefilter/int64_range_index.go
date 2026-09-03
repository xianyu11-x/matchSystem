package prefilter

import (
	"fmt"
	"sort"

	"github.com/RoaringBitmap/roaring/v2"

	"matchSystem/internal/common"
)

// int64RangeBlockSize is the number of sorted distinct values represented by
// one aggregate bitmap. It is deliberately small because range boundaries
// still scan their partial blocks value-by-value; the middle blocks are
// consumed as one bitmap Or operation.
const int64RangeBlockSize = 16

type int64RangeIndex struct {
	spec            indexSpec
	postingsByValue map[int64]*roaring.Bitmap
	valueByDoc      map[uint32]int64
	sortedValues    []int64
	valuesDirty     bool

	blockSize    int
	blocks       []int64RangeBlock
	blockByValue map[int64]int
}

type int64RangeBlock struct {
	bitmap *roaring.Bitmap
}

func newInt64RangeIndex(spec indexSpec) *int64RangeIndex {
	return &int64RangeIndex{
		spec:            spec,
		postingsByValue: make(map[int64]*roaring.Bitmap),
		valueByDoc:      make(map[uint32]int64),
		blockSize:       int64RangeBlockSize,
		blockByValue:    make(map[int64]int),
	}
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
	if blockIndex, ok := i.blockByValue[value]; ok && blockIndex < len(i.blocks) {
		i.blocks[blockIndex].bitmap.Add(docID)
	}
}

func (i *int64RangeIndex) remove(docID uint32) {
	value, ok := i.valueByDoc[docID]
	if !ok {
		return
	}
	posting := i.postingsByValue[value]
	if posting != nil {
		posting.Remove(docID)
		if blockIndex, ok := i.blockByValue[value]; ok && blockIndex < len(i.blocks) {
			i.blocks[blockIndex].bitmap.Remove(docID)
		}
		if posting.IsEmpty() {
			delete(i.postingsByValue, value)
			i.valuesDirty = true
		}
	}
	delete(i.valueByDoc, docID)
}

// prepare refreshes the sorted distinct-key directory and aggregate block
// bitmaps after a distinct value is added or removed. Existing aggregate
// bitmaps are cleared and reused when possible; callers invoke prepare at the
// Tick barrier after all owner mutations have stopped.
func (i *int64RangeIndex) prepare() {
	if !i.valuesDirty {
		return
	}
	i.sortedValues = i.sortedValues[:0]
	for value := range i.postingsByValue {
		i.sortedValues = append(i.sortedValues, value)
	}
	sort.Slice(i.sortedValues, func(a, b int) bool { return i.sortedValues[a] < i.sortedValues[b] })

	blockSize := i.effectiveBlockSize()
	blockCount := len(i.sortedValues) / blockSize
	if len(i.sortedValues)%blockSize != 0 {
		blockCount++
	}
	oldBlocks := i.blocks
	i.blocks = make([]int64RangeBlock, blockCount)
	if i.blockByValue == nil {
		i.blockByValue = make(map[int64]int, len(i.sortedValues))
	} else {
		clear(i.blockByValue)
	}
	for index, value := range i.sortedValues {
		blockIndex := index / blockSize
		block := &i.blocks[blockIndex]
		if block.bitmap == nil {
			if blockIndex < len(oldBlocks) && oldBlocks[blockIndex].bitmap != nil {
				block.bitmap = oldBlocks[blockIndex].bitmap
				block.bitmap.Clear()
			} else {
				block.bitmap = roaring.New()
			}
		}
		block.bitmap.Or(i.postingsByValue[value])
		i.blockByValue[value] = blockIndex
	}
	i.valuesDirty = false
}

func (i *int64RangeIndex) effectiveBlockSize() int {
	if i.blockSize > 0 {
		return i.blockSize
	}
	return int64RangeBlockSize
}

func (i *int64RangeIndex) rangeBounds(q boundIndexQuery) (int, int) {
	if q.min > q.max {
		return 0, 0
	}
	start := sort.Search(len(i.sortedValues), func(n int) bool { return i.sortedValues[n] >= q.min })
	end := sort.Search(len(i.sortedValues), func(n int) bool { return i.sortedValues[n] > q.max })
	if start >= end {
		return 0, 0
	}
	return start, end
}

func (i *int64RangeIndex) estimate(query boundIndexQuery) (uint64, error) {
	if query.kind != boundQueryRange {
		return 0, fmt.Errorf("int64 range index received incompatible query")
	}
	start, end := i.rangeBounds(query)
	if start == end {
		return 0, nil
	}
	blockSize := i.effectiveBlockSize()
	firstBlock := start / blockSize
	lastBlock := (end - 1) / blockSize
	if firstBlock == lastBlock {
		return i.estimatePostingRange(start, end), nil
	}
	estimate := i.estimatePostingRange(start, minInt64Range(end, (firstBlock+1)*blockSize))
	for blockIndex := firstBlock + 1; blockIndex < lastBlock; blockIndex++ {
		estimate += i.blocks[blockIndex].bitmap.GetCardinality()
	}
	estimate += i.estimatePostingRange(lastBlock*blockSize, end)
	return estimate, nil
}

func (i *int64RangeIndex) estimatePostingRange(start, end int) uint64 {
	var estimate uint64
	for index := start; index < end; index++ {
		if posting := i.postingsByValue[i.sortedValues[index]]; posting != nil {
			estimate += posting.GetCardinality()
		}
	}
	return estimate
}

func (i *int64RangeIndex) lookup(query boundIndexQuery) (*roaring.Bitmap, error) {
	if query.kind != boundQueryRange {
		return nil, fmt.Errorf("int64 range index received incompatible query")
	}
	out := roaring.New()
	start, end := i.rangeBounds(query)
	if start == end {
		return out, nil
	}
	blockSize := i.effectiveBlockSize()
	firstBlock := start / blockSize
	lastBlock := (end - 1) / blockSize
	if firstBlock == lastBlock {
		i.orPostingRange(out, start, end)
		return out, nil
	}
	i.orPostingRange(out, start, minInt64Range(end, (firstBlock+1)*blockSize))
	for blockIndex := firstBlock + 1; blockIndex < lastBlock; blockIndex++ {
		out.Or(i.blocks[blockIndex].bitmap)
	}
	i.orPostingRange(out, lastBlock*blockSize, end)
	return out, nil
}

func (i *int64RangeIndex) orPostingRange(out *roaring.Bitmap, start, end int) {
	for index := start; index < end; index++ {
		if posting := i.postingsByValue[i.sortedValues[index]]; posting != nil {
			out.Or(posting)
		}
	}
}

func minInt64Range(left, right int) int {
	if left < right {
		return left
	}
	return right
}

func (i *int64RangeIndex) contains(query boundIndexQuery, docID uint32) (bool, error) {
	if query.kind != boundQueryRange {
		return false, fmt.Errorf("int64 range index received incompatible query")
	}
	value, ok := i.valueByDoc[docID]
	return ok && value >= query.min && value <= query.max, nil
}
