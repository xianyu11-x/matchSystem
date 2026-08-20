package matchsystem

import "testing"

func TestGenericEvaluatorComposition(t *testing.T) {
	allow := FuncGroupEvaluator{EvaluatorFlagsValue: GroupEvaluatorJoin, AllowFn: func(GroupEvaluatorContext, []*Ticket, *Ticket) bool { return true }}
	deny := FuncGroupEvaluator{EvaluatorFlagsValue: GroupEvaluatorJoin, AllowFn: func(GroupEvaluatorContext, []*Ticket, *Ticket) bool { return false }}
	ctx := GroupEvaluatorContext{Phase: GroupEvaluatorJoin}
	group := []*Ticket{{TicketID: "seed"}}
	candidate := &Ticket{TicketID: "candidate"}

	cases := []struct {
		name string
		node GroupEvaluator
		want bool
	}{
		{"all", AllEvaluators(allow, deny), false},
		{"any", AnyEvaluators(allow, deny), true},
		{"not", NotEvaluator(deny), true},
		{"when", WhenEvaluator(GroupConditionFunc(func(GroupEvaluatorContext, []*Ticket, *Ticket) bool { return true }), allow, deny), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.node.AllowJoin(ctx, group, candidate); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCandidateFiltersRunByEstimate(t *testing.T) {
	order := make([]string, 0, 2)
	filter := func(name string, estimated int) FuncCandidateFilter {
		return FuncCandidateFilter{
			EstimateFn: func(CandidateFilterContext, TicketSet) CandidateFilterEstimate {
				return CandidateFilterEstimate{EstimatedOut: estimated, Cost: 1}
			},
			FilterFn: func(_ CandidateFilterContext, candidates TicketSet, _ CandidateFilterSource) TicketSet {
				order = append(order, name)
				return candidates.Clone()
			},
		}
	}
	pool := NewMatchPool(PoolConfig{}, NewRuleSet(filter("large", 10), filter("small", 1)))
	seed := testTicket("seed", 1, "")
	pool.Add(seed)
	pool.rules.CandidateSet(pool, seed, 10)
	if len(order) != 2 || order[0] != "small" || order[1] != "large" {
		t.Fatalf("unexpected filter order: %v", order)
	}
}
