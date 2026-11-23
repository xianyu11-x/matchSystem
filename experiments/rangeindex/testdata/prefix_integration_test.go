package prefilter

import (
	"fmt"
	"testing"

	"github.com/RoaringBitmap/roaring/v2"
)

// 前缀方案沿用 range_experiment_benchmark_test.go 的分块累计逻辑。
// 此包装器仅供试验，prepare 每次都会重建前缀，与原型保持一致。
type compositionPrefixIndex struct {
	*int64RangeIndex
	prefixes []*roaring.Bitmap
	counts   []uint64
}

func (p *compositionPrefixIndex) rebuild() {
	n := len(p.blocks)
	p.prefixes = make([]*roaring.Bitmap, n+1)
	p.counts = make([]uint64, n+1)
	p.prefixes[0] = roaring.New()
	for j := 0; j < n; j++ {
		p.prefixes[j+1] = p.prefixes[j].Clone()
		p.prefixes[j+1].Or(p.blocks[j].bitmap)
		p.counts[j+1] = p.prefixes[j+1].GetCardinality()
	}
}

func (p *compositionPrefixIndex) prepare() {
	p.int64RangeIndex.prepare()
	p.rebuild()
}

func (p *compositionPrefixIndex) interior(q boundIndexQuery) (int, int, int, int) {
	start, end := p.rangeBounds(q)
	size := p.effectiveBlockSize()
	left := ((start + size - 1) / size) * size
	right := (end / size) * size
	return start, end, left, right
}

func (p *compositionPrefixIndex) lookup(q boundIndexQuery) (*roaring.Bitmap, error) {
	if q.kind != boundQueryRange {
		return nil, fmt.Errorf("int64 range index received incompatible query")
	}
	start, end, left, right := p.interior(q)
	if start == end {
		return roaring.New(), nil
	}
	if left >= right {
		out := roaring.New()
		p.orPostingRange(out, start, end)
		return out, nil
	}
	out := p.prefixes[right/p.blockSize].Clone()
	if left > 0 {
		out.AndNot(p.prefixes[left/p.blockSize])
	}
	p.orPostingRange(out, start, left)
	p.orPostingRange(out, right, end)
	return out, nil
}

func (p *compositionPrefixIndex) estimate(q boundIndexQuery) (uint64, error) {
	if q.kind != boundQueryRange {
		return 0, fmt.Errorf("int64 range index received incompatible query")
	}
	start, end, left, right := p.interior(q)
	if start == end {
		return 0, nil
	}
	if left >= right {
		return p.estimatePostingRange(start, end), nil
	}
	return p.counts[right/p.blockSize] - p.counts[left/p.blockSize] +
		p.estimatePostingRange(start, left) + p.estimatePostingRange(right, end), nil
}

// 只在功能验证中统计调用；计时阶段不使用包装。
type compositionSpy struct {
	runtimeIndex
	lookups       int
	estimates     int
	containsCalls int
}

func (s *compositionSpy) lookup(q boundIndexQuery) (*roaring.Bitmap, error) {
	s.lookups++
	return s.runtimeIndex.lookup(q)
}

func (s *compositionSpy) estimate(q boundIndexQuery) (uint64, error) {
	s.estimates++
	return s.runtimeIndex.estimate(q)
}

func (s *compositionSpy) contains(q boundIndexQuery, docID uint32) (bool, error) {
	s.containsCalls++
	return s.runtimeIndex.contains(q, docID)
}

func installCompositionPrefix(tb testing.TB, workload prefilterBenchmarkWorkload, spy bool) *compositionSpy {
	tb.Helper()
	count := 0
	var trace *compositionSpy
	for slot, raw := range workload.session.store.indexes {
		index, ok := raw.(*int64RangeIndex)
		if !ok || index.spec.name != "score" {
			continue
		}
		wrapped := &compositionPrefixIndex{int64RangeIndex: index}
		wrapped.rebuild()
		if spy {
			trace = &compositionSpy{runtimeIndex: wrapped}
			workload.session.store.indexes[slot] = trace
		} else {
			workload.session.store.indexes[slot] = wrapped
		}
		count++
	}
	if count != 1 {
		tb.Fatalf("expected exactly one score range slot, found %d", count)
	}
	return trace
}

func TestCompositionPrefixParity(t *testing.T) {
	workload := mustPreparePrefilterBenchmark(t, benchmarkPrefilterCurrent)
	baseline, baselineStats, err := workload.session.CandidatesWithStats(workload.seedDocID, workload.seed, Facts{})
	if err != nil {
		t.Fatal(err)
	}
	baseline.Remove(workload.seedDocID)
	trace := installCompositionPrefix(t, workload, true)
	candidate, stats, err := workload.session.CandidatesWithStats(workload.seedDocID, workload.seed, Facts{})
	if err != nil {
		t.Fatal(err)
	}
	candidate.Remove(workload.seedDocID)
	if candidate.Count() != prefilterBenchmarkCandidateCount || stats != prefilterBenchmarkStats {
		t.Fatalf("prefix count=%d stats=%+v", candidate.Count(), stats)
	}
	if baselineStats != stats || !candidate.data.Equals(baseline.data) {
		t.Fatal("prefix changed candidate IDs or stats")
	}
	if trace.lookups == 0 || trace.estimates == 0 {
		t.Fatalf("score prefix was not used for lookup and estimate: %+v", trace)
	}
	t.Logf("equal candidate IDs count=%d stats=%+v; score lookup=%d estimate=%d contains=%d", candidate.Count(), stats, trace.lookups, trace.estimates, trace.containsCalls)
}

func BenchmarkCompositionPrefixAB(b *testing.B) {
	for _, variant := range []string{"current", "prefix"} {
		b.Run(variant, func(b *testing.B) {
			workload := mustPreparePrefilterBenchmark(b, benchmarkPrefilterCurrent)
			if variant == "prefix" {
				installCompositionPrefix(b, workload, false)
			}
			validatePrefilterBenchmarkStats(b, workload)
			benchmarkPrefilterCandidates(b, workload)
		})
	}
}
