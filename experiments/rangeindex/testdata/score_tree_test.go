package prefilter

import (
	"fmt"
	"math"
	"runtime"
	"testing"

	"github.com/RoaringBitmap/roaring/v2"
	"github.com/xianyu11-x/matchSystem/pkg/common"
)

// 单字段实验：不同分数为键的平衡一维树，叶子最多 8 个键。
// 树复用当前索引的逐值 posting；可为覆盖不少于 16 个键的子树缓存位图。
// 在线改值只支持所有原有键保持非空且目标键已存在；其他情况必须整体重建。
const scoreTreeLeafKeys = 8
const scoreTreeCacheKeys = 16

type scoreTreeItem struct {
	value   int64
	posting *roaring.Bitmap
}

type scoreTreeNode struct {
	lo, hi, left, right, parent, depth int
	bitmap                             *roaring.Bitmap
}

type scoreTree struct {
	index  *int64RangeIndex
	items  []scoreTreeItem
	nodes  []scoreTreeNode
	leaves map[int64]int
	cache  bool
}

func newScoreTree(index *int64RangeIndex, cache bool) *scoreTree {
	t := &scoreTree{index: index, cache: cache, leaves: make(map[int64]int, len(index.sortedValues))}
	t.items = make([]scoreTreeItem, len(index.sortedValues))
	for j, value := range index.sortedValues {
		t.items[j] = scoreTreeItem{value, index.postingsByValue[value]}
	}
	if len(t.items) > 0 {
		t.build(0, len(t.items), -1, 0)
	}
	return t
}

func (t *scoreTree) build(lo, hi, parent, depth int) int {
	id := len(t.nodes)
	t.nodes = append(t.nodes, scoreTreeNode{lo: lo, hi: hi, left: -1, right: -1, parent: parent, depth: depth})
	if hi-lo <= scoreTreeLeafKeys {
		for _, item := range t.items[lo:hi] {
			t.leaves[item.value] = id
		}
		return id
	}
	middle := lo + (hi-lo)/2
	left := t.build(lo, middle, id, depth+1)
	right := t.build(middle, hi, id, depth+1)
	t.nodes[id].left, t.nodes[id].right = left, right
	if t.cache && hi-lo >= scoreTreeCacheKeys {
		bitmap := roaring.New()
		if t.nodes[left].bitmap != nil && t.nodes[right].bitmap != nil {
			bitmap.Or(t.nodes[left].bitmap)
			bitmap.Or(t.nodes[right].bitmap)
		} else {
			for _, item := range t.items[lo:hi] {
				bitmap.Or(item.posting)
			}
		}
		t.nodes[id].bitmap = bitmap
	}
	return id
}

func (t *scoreTree) lookup(q boundIndexQuery, useCache bool) *roaring.Bitmap {
	out := roaring.New()
	if len(t.nodes) > 0 && q.min <= q.max {
		t.lookupNode(0, q, out, useCache)
	}
	return out
}

func (t *scoreTree) lookupNode(id int, q boundIndexQuery, out *roaring.Bitmap, useCache bool) {
	n := &t.nodes[id]
	if t.items[n.hi-1].value < q.min || t.items[n.lo].value > q.max {
		return
	}
	covered := t.items[n.lo].value >= q.min && t.items[n.hi-1].value <= q.max
	if covered && useCache && n.bitmap != nil {
		out.Or(n.bitmap)
		return
	}
	if covered || n.left < 0 {
		for _, item := range t.items[n.lo:n.hi] {
			if covered || item.value >= q.min && item.value <= q.max {
				out.Or(item.posting)
			}
		}
		return
	}
	t.lookupNode(n.left, q, out, useCache)
	t.lookupNode(n.right, q, out, useCache)
}

func (t *scoreTree) estimate(q boundIndexQuery, useCache bool) uint64 {
	if len(t.nodes) == 0 || q.min > q.max {
		return 0
	}
	return t.estimateNode(0, q, useCache)
}

func (t *scoreTree) estimateNode(id int, q boundIndexQuery, useCache bool) uint64 {
	n := &t.nodes[id]
	if t.items[n.hi-1].value < q.min || t.items[n.lo].value > q.max {
		return 0
	}
	covered := t.items[n.lo].value >= q.min && t.items[n.hi-1].value <= q.max
	if covered && useCache && n.bitmap != nil {
		return n.bitmap.GetCardinality()
	}
	if covered || n.left < 0 {
		var total uint64
		for _, item := range t.items[n.lo:n.hi] {
			if covered || item.value >= q.min && item.value <= q.max {
				total += item.posting.GetCardinality()
			}
		}
		return total
	}
	return t.estimateNode(n.left, q, useCache) + t.estimateNode(n.right, q, useCache)
}

// 共同祖先包含改值前后的文档，无需更新；其他祖先的缓存逐个移除或添加。
func (t *scoreTree) change(id uint32, ticket *common.Ticket) {
	oldValue, ok := t.index.valueByDoc[id]
	newValue, valid := ticket.Int64Values["score"]
	oldLeaf, oldKnown := t.leaves[oldValue]
	newLeaf, newKnown := t.leaves[newValue]
	if !ok || !valid || !oldKnown || !newKnown {
		panic("单字段树实验只支持已有 DocID 改为已有分数")
	}
	if oldValue == newValue {
		return
	}
	if t.index.postingsByValue[oldValue].GetCardinality() == 1 {
		panic("单字段树实验不支持删除最后一个同值 DocID")
	}
	t.index.remove(id)
	t.index.add(id, ticket)
	if !t.cache {
		return
	}
	for oldLeaf != newLeaf {
		if t.nodes[oldLeaf].depth >= t.nodes[newLeaf].depth {
			if bitmap := t.nodes[oldLeaf].bitmap; bitmap != nil {
				bitmap.Remove(id)
			}
			oldLeaf = t.nodes[oldLeaf].parent
		} else {
			if bitmap := t.nodes[newLeaf].bitmap; bitmap != nil {
				bitmap.Add(id)
			}
			newLeaf = t.nodes[newLeaf].parent
		}
	}
}

func scoreTreeWant(values []int64, q boundIndexQuery) *roaring.Bitmap {
	out := roaring.New()
	for id := 1; id < len(values); id++ {
		if values[id] >= q.min && values[id] <= q.max {
			out.Add(uint32(id))
		}
	}
	return out
}

func TestScoreTreeRandomParity(t *testing.T) {
	values := experimentValues(false)
	current, prefix := newExperimentCurrent(values), newExperimentPrefix(values)
	tree := newScoreTree(current.index, true)
	var cacheBytes uint64
	var cacheCount int
	for _, node := range tree.nodes {
		if node.bitmap != nil {
			cacheCount++
			cacheBytes += node.bitmap.GetSizeInBytes()
		}
	}
	t.Logf("docs=%d distinct_keys=%d nodes=%d cache_nodes=%d bitmap_GetSizeInBytes=%d leaf_keys=%d cache_min_keys=%d", len(values)-1, len(tree.items), len(tree.nodes), cacheCount, cacheBytes, scoreTreeLeafKeys, scoreTreeCacheKeys)
	for _, width := range []int{7, 101, 301} {
		var total uint64
		for j, q := range experimentRandomWindows(width) {
			want := scoreTreeWant(values, q)
			total += want.GetCardinality()
			variants := []struct {
				name     string
				result   *roaring.Bitmap
				estimate uint64
			}{
				{"current", current.lookup(q), current.estimate(q)},
				{"prefix", prefix.lookup(q), prefix.estimate(q)},
				{"tree", tree.lookup(q, false), tree.estimate(q, false)},
				{"tree_cached", tree.lookup(q, true), tree.estimate(q, true)},
			}
			for _, variant := range variants {
				if !variant.result.Equals(want) || variant.estimate != want.GetCardinality() {
					t.Fatalf("width=%d window=%d %s result=%d estimate=%d want=%d", width, j, variant.name, variant.result.GetCardinality(), variant.estimate, want.GetCardinality())
				}
				if j == 0 {
					variant.result.Clear()
				}
			}
			if j == 0 && (!current.lookup(q).Equals(want) || !prefix.lookup(q).Equals(want) || !tree.lookup(q, false).Equals(want) || !tree.lookup(q, true).Equals(want)) {
				t.Fatal("清空查询结果影响了索引或后续查询")
			}
		}
		t.Logf("width=%d windows=1024 average_candidates=%.2f", width, float64(total)/1024)
	}
}

func TestScoreTreeEdges(t *testing.T) {
	values := []int64{0, math.MinInt64, -1, 0, 1, 1, 1, math.MaxInt64}
	for j := 0; j < 600; j++ {
		values = append(values, 42)
	}
	current, prefix := newExperimentCurrent(values), newExperimentPrefix(values)
	tree := newScoreTree(current.index, true)
	for _, pair := range [][2]int64{{math.MinInt64, math.MaxInt64}, {math.MinInt64, math.MinInt64}, {math.MaxInt64, math.MaxInt64}, {-1, 1}, {42, 42}, {43, 42}, {43, 99}} {
		q := boundIndexQuery{kind: boundQueryRange, min: pair[0], max: pair[1]}
		want := scoreTreeWant(values, q)
		for name, got := range map[string]*roaring.Bitmap{"current": current.lookup(q), "prefix": prefix.lookup(q), "tree": tree.lookup(q, false), "tree_cached": tree.lookup(q, true)} {
			if !got.Equals(want) {
				t.Fatalf("range=%v %s result=%v want=%v", pair, name, got.ToArray(), want.ToArray())
			}
		}
	}
	if !newScoreTree(newExperimentCurrent(nil).index, true).lookup(boundIndexQuery{kind: boundQueryRange, min: 0, max: 1}, true).IsEmpty() {
		t.Fatal("空树返回非空结果")
	}
}

func TestScoreTreeCachedInt64Bounds(t *testing.T) {
	values := []int64{0, math.MinInt64, math.MaxInt64}
	for value := int64(-50); value <= 50; value++ {
		for repeat := 0; repeat < 3; repeat++ {
			values = append(values, value)
		}
	}
	current, prefix := newExperimentCurrent(values), newExperimentPrefix(values)
	tree := newScoreTree(current.index, true)
	if len(tree.items) <= scoreTreeCacheKeys || tree.nodes[0].bitmap == nil {
		t.Fatal("测试需要足够多的键及根节点缓存")
	}
	var cacheHits func(int, boundIndexQuery) int
	cacheHits = func(id int, q boundIndexQuery) int {
		node := tree.nodes[id]
		if tree.items[node.hi-1].value < q.min || tree.items[node.lo].value > q.max {
			return 0
		}
		if node.bitmap != nil && tree.items[node.lo].value >= q.min && tree.items[node.hi-1].value <= q.max {
			return 1
		}
		if node.left < 0 {
			return 0
		}
		return cacheHits(node.left, q) + cacheHits(node.right, q)
	}
	pairs := [][2]int64{
		{math.MinInt64, math.MaxInt64}, {math.MinInt64, math.MinInt64}, {math.MaxInt64, math.MaxInt64},
		{math.MinInt64, -10}, {-33, 16}, {-1, 1}, {0, 42}, {10, math.MaxInt64}, {9, 8},
	}
	for _, pair := range pairs {
		q := boundIndexQuery{kind: boundQueryRange, min: pair[0], max: pair[1]}
		want := scoreTreeWant(values, q)
		variants := []struct {
			name     string
			result   *roaring.Bitmap
			estimate uint64
		}{
			{"current", current.lookup(q), current.estimate(q)},
			{"prefix", prefix.lookup(q), prefix.estimate(q)},
			{"tree", tree.lookup(q, false), tree.estimate(q, false)},
			{"tree_cached", tree.lookup(q, true), tree.estimate(q, true)},
		}
		for _, variant := range variants {
			if !variant.result.Equals(want) || variant.estimate != want.GetCardinality() {
				t.Fatalf("range=%v %s result=%d estimate=%d want=%d", pair, variant.name, variant.result.GetCardinality(), variant.estimate, want.GetCardinality())
			}
		}
	}
	q := boundIndexQuery{kind: boundQueryRange, min: -33, max: 16}
	if hits := cacheHits(0, q); hits == 0 {
		t.Fatal("跨负数与正数的窗口未命中缓存子树")
	} else {
		t.Logf("distinct_keys=%d cross_boundary_cache_hits=%d", len(tree.items), hits)
	}
	full := boundIndexQuery{kind: boundQueryRange, min: math.MinInt64, max: math.MaxInt64}
	result := tree.lookup(full, true)
	result.Remove(1)
	result.Add(9999)
	if !tree.lookup(full, true).Equals(scoreTreeWant(values, full)) {
		t.Fatal("修改返回位图影响了根缓存")
	}
}

func TestScoreTreeUpdates(t *testing.T) {
	values := experimentValues(false)
	current, prefix := newExperimentCurrent(values), newExperimentPrefix(values)
	plainIndex, cachedIndex := newExperimentCurrent(values), newExperimentCurrent(values)
	plain, cached := newScoreTree(plainIndex.index, false), newScoreTree(cachedIndex.index, true)
	var tickets [2][100]*common.Ticket
	for j := 0; j < 100; j++ {
		v := values[j+1]
		tickets[0][j], tickets[1][j] = experimentTicket(v%500+1), experimentTicket(v)
	}
	for step := 0; step < 2; step++ {
		for j := 0; j < 100; j++ {
			id, ticket := uint32(j+1), tickets[step][j]
			current.change(id, ticket)
			prefix.change(id, ticket)
			plain.change(id, ticket)
			cached.change(id, ticket)
			values[id] = ticket.Int64Values["score"]
		}
		current.prepare()
		prefix.prepare()
		plain.index.prepare()
		cached.index.prepare()
		for _, width := range []int{7, 101, 301} {
			for j, q := range experimentRandomWindows(width) {
				want := current.lookup(q)
				for name, got := range map[string]*roaring.Bitmap{"prefix": prefix.lookup(q), "tree": plain.lookup(q, false), "tree_cached": cached.lookup(q, true)} {
					if !got.Equals(want) {
						t.Fatalf("step=%d width=%d window=%d variant=%s", step, width, j, name)
					}
				}
				if plain.estimate(q, false) != want.GetCardinality() || cached.estimate(q, true) != want.GetCardinality() || prefix.estimate(q) != want.GetCardinality() {
					t.Fatalf("step=%d width=%d window=%d estimate 不一致", step, width, j)
				}
				if j < 8 && !want.Equals(scoreTreeWant(values, q)) {
					t.Fatalf("step=%d width=%d window=%d 扫描结果不一致", step, width, j)
				}
			}
		}
	}
}

func BenchmarkScoreTreeWindows(b *testing.B) {
	values := experimentValues(false)
	current, prefix := newExperimentCurrent(values), newExperimentPrefix(values)
	tree := newScoreTree(current.index, true)
	for _, width := range []int{7, 101, 301} {
		windows := experimentRandomWindows(width)
		for _, variant := range []string{"current", "prefix", "tree", "tree_cached"} {
			for _, operation := range []string{"lookup", "estimate"} {
				b.Run(fmt.Sprintf("width%d/%s/%s", width, variant, operation), func(b *testing.B) {
					b.ReportAllocs()
					b.ResetTimer()
					for j := 0; j < b.N; j++ {
						q := windows[j%len(windows)]
						if operation == "lookup" {
							var result *roaring.Bitmap
							switch variant {
							case "current":
								result = current.lookup(q)
							case "prefix":
								result = prefix.lookup(q)
							case "tree":
								result = tree.lookup(q, false)
							case "tree_cached":
								result = tree.lookup(q, true)
							}
							runtime.KeepAlive(result)
						} else {
							var result uint64
							switch variant {
							case "current":
								result = current.estimate(q)
							case "prefix":
								result = prefix.estimate(q)
							case "tree":
								result = tree.estimate(q, false)
							case "tree_cached":
								result = tree.estimate(q, true)
							}
							runtime.KeepAlive(result)
						}
					}
				})
			}
		}
	}
}

func BenchmarkScoreTreeBuildAuxiliary(b *testing.B) {
	base := newExperimentCurrent(experimentValues(false))
	for _, variant := range []string{"prefix", "tree", "tree_cached"} {
		b.Run(variant, func(b *testing.B) {
			b.ReportAllocs()
			for j := 0; j < b.N; j++ {
				if variant == "prefix" {
					p := &experimentPrefix{experimentCurrent: base}
					p.rebuild()
					runtime.KeepAlive(p)
				} else {
					runtime.KeepAlive(newScoreTree(base.index, variant == "tree_cached"))
				}
			}
		})
	}
}

func BenchmarkScoreTreeUpdate100(b *testing.B) {
	for _, variant := range []string{"current", "prefix", "tree", "tree_cached"} {
		b.Run(variant, func(b *testing.B) {
			values := experimentValues(false)
			current := newExperimentCurrent(values)
			var prefix *experimentPrefix
			var tree *scoreTree
			if variant == "prefix" {
				prefix = &experimentPrefix{experimentCurrent: current}
				prefix.rebuild()
			} else if variant == "tree" || variant == "tree_cached" {
				tree = newScoreTree(current.index, variant == "tree_cached")
			}
			var tickets [2][100]*common.Ticket
			for j := 0; j < 100; j++ {
				v := values[j+1]
				tickets[0][j], tickets[1][j] = experimentTicket(v%500+1), experimentTicket(v)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for n := 0; n < b.N; n++ {
				for j := 0; j < 100; j++ {
					id, ticket := uint32(j+1), tickets[n%2][j]
					if tree == nil {
						current.change(id, ticket)
					} else {
						tree.change(id, ticket)
					}
				}
				if prefix != nil {
					prefix.prepare()
				} else {
					current.prepare()
				}
			}
		})
	}
}
