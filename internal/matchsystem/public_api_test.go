package matchsystem_test

import (
	"testing"

	ms "matchSystem/internal/matchsystem"
)

func TestPublicExtensionSurface(t *testing.T) {
	filter := ms.FuncCandidateFilter{
		EstimateFn: func(ctx ms.CandidateFilterContext, candidates ms.TicketSet) ms.CandidateFilterEstimate {
			return ms.CandidateFilterEstimate{
				EstimatedOut:  ctx.EstimateNumericRange("value", 0, 10),
				Cost:          1,
				SupportsIndex: true,
				PreferIndex:   true,
			}
		},
		FilterFn: func(ctx ms.CandidateFilterContext, _ ms.TicketSet, source ms.CandidateFilterSource) ms.TicketSet {
			if source != ms.CandidateFilterFromIndex {
				t.Fatalf("expected the public index path")
			}
			return ctx.NumericRange("value", 0, 10)
		},
	}
	start := ms.FuncGroupEvaluator{
		EvaluatorFlagsValue: ms.GroupEvaluatorStart,
		AllowFn: func(_ ms.GroupEvaluatorContext, group []*ms.Ticket, _ *ms.Ticket) bool {
			return len(group) >= 2
		},
	}
	pool := ms.NewMatchPool(ms.PoolConfig{MaxPlayers: 2}, ms.NewRuleSet(filter, start))
	pool.Add(&ms.Ticket{TicketID: "a", Numeric: map[string]int64{"value": 1}})
	pool.Add(&ms.Ticket{TicketID: "b", Numeric: map[string]int64{"value": 2}})
	if matches := pool.Tick(1); len(matches) != 1 {
		t.Fatalf("expected one match through public extensions, got %d", len(matches))
	}
}
