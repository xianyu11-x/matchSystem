package prefilter

import (
	"fmt"
	"math"
	"math/rand"
	"runtime"
	"sort"
	"testing"

	"github.com/RoaringBitmap/roaring/v2"
	bsi "github.com/RoaringBitmap/roaring/v2/BitSliceIndexing"
	"github.com/xianyu11-x/matchSystem/pkg/common"
	"github.com/xianyu11-x/matchSystem/pkg/matchsystem/contract"
)

// 此实验基准通过 Go overlay 加载，不参与生产构建；所有候选均输出独立 Roaring 位图。
type experiment interface {
	lookup(boundIndexQuery) *roaring.Bitmap
	estimate(boundIndexQuery) uint64
	change(uint32, *common.Ticket)
	prepare()
}

type experimentCurrent struct{ index *int64RangeIndex }

func newExperimentCurrent(values []int64) *experimentCurrent {
	e := &experimentCurrent{newInt64RangeIndex(indexSpec{name: "score", kind: contract.IndexTypeInt64Range})}
	for id := 1; id < len(values); id++ {
		e.index.add(uint32(id), experimentTicket(values[id]))
	}
	e.index.prepare()
	return e
}
func experimentTicket(value int64) *common.Ticket {
	return &common.Ticket{Int64Values: map[string]int64{"score": value}}
}
func (e *experimentCurrent) lookup(q boundIndexQuery) *roaring.Bitmap {
	r, err := e.index.lookup(q)
	if err != nil {
		panic(err)
	}
	return r
}
func (e *experimentCurrent) estimate(q boundIndexQuery) uint64 {
	r, err := e.index.estimate(q)
	if err != nil {
		panic(err)
	}
	return r
}
func (e *experimentCurrent) change(id uint32, ticket *common.Ticket) {
	e.index.remove(id)
	e.index.add(id, ticket)
}
func (e *experimentCurrent) prepare() { e.index.prepare() }

// 每个 16 值边界维护累计位图，写入后统一在 Tick barrier 重建。
type experimentPrefix struct {
	*experimentCurrent
	prefixes []*roaring.Bitmap
	counts   []uint64
}

func newExperimentPrefix(values []int64) *experimentPrefix {
	e := &experimentPrefix{experimentCurrent: newExperimentCurrent(values)}
	e.rebuild()
	return e
}
func (e *experimentPrefix) rebuild() {
	n := len(e.index.blocks)
	e.prefixes = make([]*roaring.Bitmap, n+1)
	e.counts = make([]uint64, n+1)
	e.prefixes[0] = roaring.New()
	for j := 0; j < n; j++ {
		e.prefixes[j+1] = e.prefixes[j].Clone()
		e.prefixes[j+1].Or(e.index.blocks[j].bitmap)
		e.counts[j+1] = e.prefixes[j+1].GetCardinality()
	}
}
func (e *experimentPrefix) prepare() { e.index.prepare(); e.rebuild() }
func (e *experimentPrefix) interior(q boundIndexQuery) (int, int, int, int) {
	start, end := e.index.rangeBounds(q)
	size := e.index.effectiveBlockSize()
	left := ((start + size - 1) / size) * size
	right := (end / size) * size
	return start, end, left, right
}
func (e *experimentPrefix) lookup(q boundIndexQuery) *roaring.Bitmap {
	start, end, left, right := e.interior(q)
	if start == end {
		return roaring.New()
	}
	if left >= right {
		out := roaring.New()
		e.index.orPostingRange(out, start, end)
		return out
	}
	out := e.prefixes[right/e.index.blockSize].Clone()
	if left > 0 {
		out.AndNot(e.prefixes[left/e.index.blockSize])
	}
	e.index.orPostingRange(out, start, left)
	e.index.orPostingRange(out, right, end)
	return out
}
func (e *experimentPrefix) estimate(q boundIndexQuery) uint64 {
	start, end, left, right := e.interior(q)
	if start == end {
		return 0
	}
	if left >= right {
		return e.index.estimatePostingRange(start, end)
	}
	return e.counts[right/e.index.blockSize] - e.counts[left/e.index.blockSize] +
		e.index.estimatePostingRange(start, left) + e.index.estimatePostingRange(right, end)
}

// 对照使用现有依赖中的 BSI；CompareValue(1) 与单 owner 线程一致。
type experimentBSI struct{ index *bsi.BSI }

func newExperimentBSI(values []int64) *experimentBSI {
	e := &experimentBSI{bsi.NewDefaultBSI()}
	for id := 1; id < len(values); id++ {
		e.index.SetValue(uint64(id), values[id])
	}
	return e
}
func (e *experimentBSI) lookup(q boundIndexQuery) *roaring.Bitmap {
	return e.index.CompareValue(1, bsi.RANGE, q.min, q.max, nil)
}
func (e *experimentBSI) estimate(q boundIndexQuery) uint64 { return e.lookup(q).GetCardinality() }
func (e *experimentBSI) change(id uint32, ticket *common.Ticket) {
	e.index.SetValue(uint64(id), ticket.Int64Values["score"])
}
func (e *experimentBSI) prepare() {}

// 简化的稠密 DocID 对照：Tick 批量重排，查询以 value 二分并把 ID 添入位图。
type experimentPair struct {
	value int64
	doc   uint32
}
type experimentSorted struct {
	values []int64
	pairs  []experimentPair
}

func newExperimentSorted(values []int64) *experimentSorted {
	e := &experimentSorted{values: append([]int64(nil), values...)}
	e.prepare()
	return e
}
func (e *experimentSorted) prepare() {
	e.pairs = e.pairs[:0]
	for id := 1; id < len(e.values); id++ {
		e.pairs = append(e.pairs, experimentPair{e.values[id], uint32(id)})
	}
	sort.Slice(e.pairs, func(i, j int) bool {
		if e.pairs[i].value == e.pairs[j].value {
			return e.pairs[i].doc < e.pairs[j].doc
		}
		return e.pairs[i].value < e.pairs[j].value
	})
}
func (e *experimentSorted) bounds(q boundIndexQuery) (int, int) {
	return sort.Search(len(e.pairs), func(i int) bool { return e.pairs[i].value >= q.min }),
		sort.Search(len(e.pairs), func(i int) bool { return e.pairs[i].value > q.max })
}
func (e *experimentSorted) lookup(q boundIndexQuery) *roaring.Bitmap {
	start, end := e.bounds(q)
	out := roaring.New()
	for _, p := range e.pairs[start:end] {
		out.Add(p.doc)
	}
	return out
}
func (e *experimentSorted) estimate(q boundIndexQuery) uint64 {
	a, b := e.bounds(q)
	return uint64(b - a)
}
func (e *experimentSorted) change(id uint32, ticket *common.Ticket) {
	e.values[id] = ticket.Int64Values["score"]
}

func experimentValues(high bool) []int64 {
	const n = 100000
	rng := rand.New(rand.NewSource(100000*7919 + 17))
	values := make([]int64, n+1)
	if high {
		for id, val := range rng.Perm(n) {
			values[id+1] = int64(val + 1)
		}
	} else {
		for id := 1; id <= n; id++ {
			values[id] = int64(rng.Intn(500) + 1)
		}
	}
	return values
}

type experimentQuery struct {
	name string
	q    boundIndexQuery
}

func experimentQueries(high bool) []experimentQuery {
	rangeQuery := func(name string, min, max int64) experimentQuery {
		return experimentQuery{name, boundIndexQuery{kind: boundQueryRange, min: min, max: max}}
	}
	if high {
		return []experimentQuery{rangeQuery("narrow", 50000, 50006), rangeQuery("medium", 40000, 50099), rangeQuery("wide", 20000, 80000)}
	}
	return []experimentQuery{rangeQuery("narrow", 17, 23), rangeQuery("medium", 200, 300), rangeQuery("wide", 100, 400)}
}
func experimentNames(high bool) []string {
	if high {
		return []string{"current", "bsi", "sorted"}
	}
	return []string{"current", "prefix", "bsi", "sorted"}
}
func makeExperiment(name string, values []int64) experiment {
	switch name {
	case "current":
		return newExperimentCurrent(values)
	case "prefix":
		return newExperimentPrefix(values)
	case "bsi":
		return newExperimentBSI(values)
	case "sorted":
		return newExperimentSorted(values)
	default:
		panic(name)
	}
}

func TestExperimentRangeAlternativesParity(t *testing.T) {
	for _, high := range []bool{false, true} {
		values := experimentValues(high)
		indexes := map[string]experiment{}
		for _, name := range experimentNames(high) {
			indexes[name] = makeExperiment(name, values)
		}
		for step := 0; step < 2; step++ {
			if step == 1 {
				for id := uint32(1); id <= 100; id++ {
					next := values[id]%500 + 1
					if high {
						next = values[id] + 100000
					}
					for _, e := range indexes {
						e.change(id, experimentTicket(next))
					}
					values[id] = next
				}
				for _, e := range indexes {
					e.prepare()
				}
			}
			for _, test := range experimentQueries(high) {
				want := roaring.New()
				for id := 1; id < len(values); id++ {
					if values[id] >= test.q.min && values[id] <= test.q.max {
						want.Add(uint32(id))
					}
				}
				for name, e := range indexes {
					got := e.lookup(test.q)
					if !got.Equals(want) || e.estimate(test.q) != want.GetCardinality() {
						t.Fatalf("high=%v step=%d query=%s implementation=%s got=%d estimate=%d want=%d", high, step, test.name, name, got.GetCardinality(), e.estimate(test.q), want.GetCardinality())
					}
					got.Clear()
					if !e.lookup(test.q).Equals(want) {
						t.Fatalf("%s returned internal bitmap", name)
					}
				}
			}
		}
	}
}

// 此测试预期失败，用于复现 BSI 对负数及 int64 极值范围查询的结果不一致。
func TestExperimentBSIInt64Bounds(t *testing.T) {
	values := []int64{0, math.MinInt64, -999, -1, 0, 1, 999, math.MaxInt64}
	explicit := &experimentBSI{bsi.NewBSI(math.MaxInt64, math.MinInt64)}
	for id := 1; id < len(values); id++ {
		explicit.index.SetValue(uint64(id), values[id])
	}
	indexes := map[string]experiment{"current": newExperimentCurrent(values), "auto": newExperimentBSI(values), "explicit": explicit}
	for _, pair := range [][2]int64{{math.MinInt64, math.MaxInt64}, {math.MinInt64, -1}, {-999, 0}, {-1, 1}, {0, 999}, {1, math.MaxInt64}, {math.MaxInt64, math.MaxInt64}} {
		q := boundIndexQuery{kind: boundQueryRange, min: pair[0], max: pair[1]}
		want := indexes["current"].lookup(q)
		for _, name := range []string{"auto", "explicit"} {
			got := indexes[name].lookup(q)
			if !got.Equals(want) {
				t.Errorf("configuration=%s range=%v current=%v bsi=%v", name, pair, want.ToArray(), got.ToArray())
			}
		}
	}
}

func BenchmarkExperimentRanges(b *testing.B) {
	for _, high := range []bool{false, true} {
		dataset := "low500"
		if high {
			dataset = "high100k"
		}
		values := experimentValues(high)
		for _, name := range experimentNames(high) {
			e := makeExperiment(name, values)
			for _, test := range experimentQueries(high) {
				for _, operation := range []string{"lookup", "estimate"} {
					b.Run(fmt.Sprintf("%s/%s/%s/%s", dataset, name, test.name, operation), func(b *testing.B) {
						b.ReportAllocs()
						b.ResetTimer()
						for n := 0; n < b.N; n++ {
							if operation == "lookup" {
								runtime.KeepAlive(e.lookup(test.q))
							} else {
								runtime.KeepAlive(e.estimate(test.q))
							}
						}
					})
				}
			}
		}
	}
}

func BenchmarkExperimentUpdate100AndPrepare(b *testing.B) {
	for _, high := range []bool{false, true} {
		dataset := "low500-existing"
		if high {
			dataset = "high100k-new-values"
		}
		values := experimentValues(high)
		for _, name := range experimentNames(high) {
			b.Run(fmt.Sprintf("%s/%s", dataset, name), func(b *testing.B) {
				e := makeExperiment(name, values)
				var tickets [2][101]*common.Ticket
				for id := 1; id <= 100; id++ {
					old := values[id]
					next := old%500 + 1
					if high {
						next = old + 100000
					}
					tickets[0][id], tickets[1][id] = experimentTicket(next), experimentTicket(old)
				}
				b.ReportAllocs()
				b.ResetTimer()
				for n := 0; n < b.N; n++ {
					for id := uint32(1); id <= 100; id++ {
						e.change(id, tickets[n%2][id])
					}
					e.prepare()
				}
			})
		}
	}
}

// 使用固定随机种子生成不同起点的范围窗口，便于重复比较查询结果。
func experimentRandomWindows(width int) []boundIndexQuery {
	rng := rand.New(rand.NewSource(314159))
	queries := make([]boundIndexQuery, 1024)
	for j := range queries {
		min := int64(rng.Intn(500-width+1) + 1)
		queries[j] = boundIndexQuery{kind: boundQueryRange, min: min, max: min + int64(width) - 1}
	}
	return queries
}

func TestExperimentRandomWindowParity(t *testing.T) {
	values := experimentValues(false)
	current, prefix := newExperimentCurrent(values), newExperimentPrefix(values)
	for _, width := range []int{101, 301} {
		for _, q := range experimentRandomWindows(width) {
			a, b := current.lookup(q), prefix.lookup(q)
			if !a.Equals(b) || current.estimate(q) != prefix.estimate(q) {
				t.Fatalf("width=%d min=%d max=%d current=%d prefix=%d", width, q.min, q.max, a.GetCardinality(), b.GetCardinality())
			}
		}
	}
}

func BenchmarkExperimentRandomWindows(b *testing.B) {
	values := experimentValues(false)
	indexes := map[string]experiment{"current": newExperimentCurrent(values), "prefix": newExperimentPrefix(values)}
	for _, width := range []int{101, 301} {
		queries := experimentRandomWindows(width)
		for _, name := range []string{"current", "prefix"} {
			e := indexes[name]
			b.Run(fmt.Sprintf("width%d/%s", width, name), func(b *testing.B) {
				b.ReportAllocs()
				b.ResetTimer()
				for n := 0; n < b.N; n++ {
					runtime.KeepAlive(e.lookup(queries[n%len(queries)]))
				}
			})
		}
	}
}
