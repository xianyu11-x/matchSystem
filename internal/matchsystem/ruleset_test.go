package matchsystem

import "testing"

func TestGenericEvaluatorComposition(t *testing.T) {
	allow := FuncGroupEvaluator{EvaluatorFlagsValue: GroupEvaluatorJoin, AllowFn: func(GroupEvaluatorContext, []*Ticket, *Ticket) bool { return true }}
	deny := FuncGroupEvaluator{EvaluatorFlagsValue: GroupEvaluatorJoin, AllowFn: func(GroupEvaluatorContext, []*Ticket, *Ticket) bool { return false }}
	ctx := GroupEvaluatorContext{Phase: GroupEvaluatorJoin}
	group := []*Ticket{{TicketID: testTicketID("seed")}}
	candidate := &Ticket{TicketID: testTicketID("candidate")}
	cases := []struct {
		name string
		node GroupEvaluator
		want bool
	}{
		{"all", AllEvaluators(allow, deny), false}, {"any", AnyEvaluators(allow, deny), true}, {"not", NotEvaluator(deny), true},
		{"when", WhenEvaluator(GroupConditionFunc(func(GroupEvaluatorContext, []*Ticket, *Ticket) bool { return true }), allow, deny), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.node.AllowJoin(ctx, group, candidate); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestRuleSetContainsOnlyGroupEvaluators(t *testing.T) {
	rules := NewRuleSet()
	if len(rules.Evaluators()) != 0 {
		t.Fatal("new ruleset should contain only explicit evaluators")
	}
}
