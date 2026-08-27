package prefilter

import (
	"sort"

	"matchSystem/internal/common"
	"matchSystem/internal/matchsystem/expression"
)

type evalContext struct {
	tickFacts Facts
	seedFacts Facts
	resolver  *prefilterLookup
}

// prefilterLookup is the only Prefilter adapter to the shared scalar
// evaluator. It contains no expression dispatch. A TickSession owns and
// reuses one resolver so the hot path does not box a fresh value adapter for
// every query/condition.
type prefilterLookup struct {
	seed      *common.Ticket
	tickFacts Facts
	seedFacts Facts
}

func (l *prefilterLookup) Strings(source expression.Source, name string) ([]string, bool) {
	if l == nil {
		return nil, false
	}
	switch source {
	case expression.SourceSeedAttributes:
		if l.seed == nil {
			return nil, false
		}
		values, ok := l.seed.StringLists[name]
		return values, ok
	case expression.SourceSeedFacts:
		values, ok := l.seedFacts.StringLists[name]
		return values, ok
	case expression.SourceTickFacts:
		values, ok := l.tickFacts.StringLists[name]
		return values, ok
	default:
		return nil, false
	}
}

func (l *prefilterLookup) Uint64s(source expression.Source, name string) ([]uint64, bool) {
	if l == nil {
		return nil, false
	}
	switch source {
	case expression.SourceSeedAttributes:
		if l.seed == nil {
			return nil, false
		}
		values, ok := l.seed.Uint64Lists[name]
		return values, ok
	case expression.SourceSeedFacts:
		values, ok := l.seedFacts.Uint64Lists[name]
		return values, ok
	case expression.SourceTickFacts:
		values, ok := l.tickFacts.Uint64Lists[name]
		return values, ok
	default:
		return nil, false
	}
}

func (l *prefilterLookup) Int64(source expression.Source, name string) (int64, bool) {
	if l == nil {
		return 0, false
	}
	switch source {
	case expression.SourceSeedAttributes:
		if l.seed == nil {
			return 0, false
		}
		value, ok := l.seed.Int64Values[name]
		return value, ok
	case expression.SourceSeedFacts:
		value, ok := l.seedFacts.Int64Values[name]
		return value, ok
	case expression.SourceTickFacts:
		value, ok := l.tickFacts.Int64Values[name]
		return value, ok
	default:
		return 0, false
	}
}

var _ expression.Lookup = (*prefilterLookup)(nil)

func (c evalContext) expressionLookup() expression.Lookup {
	return c.resolver
}

func uniqueStringsRuntime(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	if len(values) == 1 {
		return []string{values[0]}
	}
	canonical := true
	for index := 1; index < len(values); index++ {
		if values[index-1] >= values[index] {
			canonical = false
			break
		}
	}
	out := append([]string(nil), values...)
	if canonical {
		return out
	}
	sort.Strings(out)
	write := 1
	for read := 1; read < len(out); read++ {
		if out[read] != out[write-1] {
			out[write] = out[read]
			write++
		}
	}
	return out[:write]
}

func uniqueUint64Runtime(values []uint64) []uint64 {
	if len(values) == 0 {
		return nil
	}
	if len(values) == 1 {
		return []uint64{values[0]}
	}
	canonical := true
	for index := 1; index < len(values); index++ {
		if values[index-1] >= values[index] {
			canonical = false
			break
		}
	}
	out := append([]uint64(nil), values...)
	if canonical {
		return out
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	write := 1
	for read := 1; read < len(out); read++ {
		if out[read] != out[write-1] {
			out[write] = out[read]
			write++
		}
	}
	return out[:write]
}
