package matchsystem

import (
	"errors"
	"testing"

	"matchSystem/internal/identity"
)

func TestObjectFactsReachCandidateScoreAndGroupEvaluator(t *testing.T) {
	priorities := map[string]int64{"seed": 1, "low": 10, "high": 100}
	calls := make(map[string]int)
	reused := Facts{Int64Values: map[string]int64{"priority": 0}}
	provider := func(object *Ticket, now int64, tickFacts Facts) (Facts, error) {
		calls[object.TicketID]++
		if now != 50 || tickFacts.Int64Values["tick_bonus"] != 7 {
			t.Fatalf("provider context: now=%d tick=%#v", now, tickFacts)
		}
		reused.Int64Values["priority"] = priorities[object.TicketID]
		return reused, nil
	}

	var scoreObserved, evaluatorObserved bool
	rules := NewRuleSet(FuncGroupEvaluator{
		EvaluatorFlagsValue: GroupEvaluatorJoin | GroupEvaluatorStart,
		AllowFn: func(ctx GroupEvaluatorContext, group []*Ticket, candidate *Ticket) bool {
			seedFacts, seedOK := ctx.Facts.For(ctx.Seed)
			if !seedOK || seedFacts.Int64Values["priority"] != 1 || ctx.Facts.Tick().Int64Values["tick_bonus"] != 7 {
				t.Fatalf("seed/tick Facts unavailable in evaluator: %#v", ctx)
			}
			for _, member := range group {
				if _, ok := ctx.Facts.For(member); !ok {
					t.Fatalf("group member %q Facts unavailable", member.TicketID)
				}
			}
			if candidate != nil {
				candidateFacts, ok := ctx.Facts.For(candidate)
				if !ok || candidateFacts.Int64Values["priority"] != priorities[candidate.TicketID] {
					t.Fatalf("candidate Facts unavailable: %q %#v", candidate.TicketID, candidateFacts)
				}
			}
			evaluatorObserved = true
			return len(group) >= 2 || candidate != nil
		},
	}).WithCandidateScoreContext(func(ctx CandidateScoreContext) float64 {
		seedFacts, seedOK := ctx.Facts.For(ctx.Seed)
		candidateFacts, candidateOK := ctx.Facts.For(ctx.Candidate)
		if !seedOK || !candidateOK || seedFacts.Int64Values["priority"] != 1 {
			t.Fatalf("score FactView incomplete: seed=%#v candidate=%#v", seedFacts, candidateFacts)
		}
		scoreObserved = true
		return float64(candidateFacts.Int64Values["priority"] + ctx.Facts.Tick().Int64Values["tick_bonus"])
	})

	node := mustLogicalNodeWithObjectFacts(t, rules, provider, 1)
	mustAdd(t, node, testTicket("seed", 1, "blue"))
	mustAdd(t, node, testTicket("low", 2, "blue"))
	mustAdd(t, node, testTicket("high", 3, "blue"))

	match, err := produceTestMatch(node, 50, Facts{Int64Values: map[string]int64{"tick_bonus": 7}})
	if err != nil {
		t.Fatalf("ProduceMatch: %v", err)
	}
	if match == nil || len(match.Tickets) != 2 || match.Tickets[1].TicketID != "high" {
		t.Fatalf("candidate scoring did not use Object Facts: %#v", match)
	}
	if !scoreObserved || !evaluatorObserved {
		t.Fatalf("callbacks did not observe Facts: score=%v evaluator=%v", scoreObserved, evaluatorObserved)
	}
	for _, ticketID := range []string{"seed", "low", "high"} {
		if calls[ticketID] != 1 {
			t.Fatalf("provider calls[%q]=%d want=1", ticketID, calls[ticketID])
		}
	}
}

func TestObjectFactsAreCachedOnceWhenCandidateLaterBecomesSeed(t *testing.T) {
	calls := make(map[string]int)
	provider := func(object *Ticket, _ int64, _ Facts) (Facts, error) {
		calls[object.TicketID]++
		return Facts{Int64Values: map[string]int64{"priority": int64(object.DocID)}}, nil
	}
	rules := NewRuleSet(FuncGroupEvaluator{
		EvaluatorFlagsValue: GroupEvaluatorStart,
		AllowFn:             func(GroupEvaluatorContext, []*Ticket, *Ticket) bool { return false },
	}).WithCandidateScoreContext(func(ctx CandidateScoreContext) float64 {
		values, ok := ctx.Facts.For(ctx.Candidate)
		if !ok {
			t.Fatalf("candidate facts missing for %q", ctx.Candidate.TicketID)
		}
		return float64(values.Int64Values["priority"])
	})
	node := mustLogicalNodeWithObjectFacts(t, rules, provider, 8)
	for index, ticketID := range []string{"a", "b", "c"} {
		mustAdd(t, node, testTicket(ticketID, int64(index+1), "blue"))
	}
	matches, err := produceTestRound(node, 10, Facts{})
	if err != nil || len(matches) != 0 {
		t.Fatalf("Tick: matches=%#v err=%v", matches, err)
	}
	for _, ticketID := range []string{"a", "b", "c"} {
		if calls[ticketID] != 1 {
			t.Fatalf("provider calls[%q]=%d want=1", ticketID, calls[ticketID])
		}
	}
}

func TestObjectFactProviderErrorSkipsCandidateAndContinues(t *testing.T) {
	providerErr := errors.New("candidate facts unavailable")
	provider := func(object *Ticket, _ int64, _ Facts) (Facts, error) {
		if object.TicketID == "bad" {
			return Facts{}, providerErr
		}
		return Facts{Int64Values: map[string]int64{"priority": int64(object.DocID)}}, nil
	}
	rules := NewRuleSet(minimumGroupSize(2)).WithCandidateScoreContext(func(ctx CandidateScoreContext) float64 {
		values, ok := ctx.Facts.For(ctx.Candidate)
		if !ok {
			t.Fatalf("candidate facts missing for %q", ctx.Candidate.TicketID)
		}
		return float64(values.Int64Values["priority"])
	})
	node := mustLogicalNodeWithObjectFacts(t, rules, provider, 8)
	mustAdd(t, node, testTicket("seed", 1, "blue"))
	mustAdd(t, node, testTicket("bad", 2, "blue"))
	mustAdd(t, node, testTicket("good", 3, "blue"))

	match, err := produceTestMatch(node, 10, Facts{})
	if err != nil {
		t.Fatalf("successful match returned skipped candidate error: %v", err)
	}
	if match == nil || len(match.Tickets) != 2 || match.Tickets[1].TicketID != "good" {
		t.Fatalf("unexpected match: %#v", match)
	}
}

func TestFactFrameValidatesNodeWideContract(t *testing.T) {
	_, err := newFactFrame(Facts{Int64Values: map[string]int64{"missing": 1}}, nil)
	requireFactError(t, err, "UNDECLARED_FACT")

	frame, err := newFactFrame(Facts{Int64Values: map[string]int64{"shared": 1}}, []FactSpec{{Name: "shared", Type: FactTypeInt64}})
	if err != nil {
		t.Fatalf("newFactFrame: %v", err)
	}
	_, err = frame.object(&Ticket{DocID: 1, TicketID: "one"}, 0, func(*Ticket, int64, Facts) (Facts, error) {
		return Facts{Int64Values: map[string]int64{"shared": 2}}, nil
	})
	requireFactError(t, err, "FACT_SCOPE_COLLISION")

	frame, err = newFactFrame(Facts{}, []FactSpec{{Name: "keys", Type: FactTypeStrings, MaxValues: 1}})
	if err != nil {
		t.Fatalf("newFactFrame: %v", err)
	}
	_, err = frame.object(&Ticket{DocID: 1, TicketID: "one"}, 0, func(*Ticket, int64, Facts) (Facts, error) {
		return Facts{StringLists: map[string][]string{"keys": {"a", "b"}}}, nil
	})
	requireFactError(t, err, "FACT_VALUE_LIMIT")

	providerErr := errors.New("unavailable")
	providerCalls := 0
	frame, err = newFactFrame(Facts{}, nil)
	if err != nil {
		t.Fatalf("newFactFrame: %v", err)
	}
	object := &Ticket{DocID: 2, TicketID: "two"}
	failedProvider := func(*Ticket, int64, Facts) (Facts, error) {
		providerCalls++
		return Facts{}, providerErr
	}
	_, _ = frame.object(object, 0, failedProvider)
	_, _ = frame.object(object, 0, failedProvider)
	if providerCalls != 1 {
		t.Fatalf("failed provider calls=%d want=1", providerCalls)
	}
}

func TestLogicalNodeRejectsConflictingFactConfiguration(t *testing.T) {
	key := identity.LogicalNodeKey{Rule: identity.RuleKey{RuleID: "fact-config-test"}, PlacementID: "test-placement"}
	provider := func(*Ticket, int64, Facts) (Facts, error) { return Facts{}, nil }
	_, err := NewLogicalNode(LogicalNodeSpec{
		Key:                key,
		Config:             LogicalNodeConfig{Prefilter: prefilterConfigForField("partition")},
		ObjectFactProvider: provider,
		SeedFactProvider:   provider,
	})
	if err == nil {
		t.Fatal("expected ObjectFactProvider/SeedFactProvider conflict")
	}

	config := prefilterConfigForField("partition")
	config.Facts = []FactSpec{{Name: "one", Type: FactTypeInt64}}
	_, err = NewLogicalNode(LogicalNodeSpec{
		Key: key,
		Config: LogicalNodeConfig{
			Facts:     []FactSpec{{Name: "two", Type: FactTypeInt64}},
			Prefilter: config,
		},
	})
	if err == nil {
		t.Fatal("expected node/Prefilter Fact contract conflict")
	}
}

func mustLogicalNodeWithObjectFacts(t *testing.T, rules *RuleSet, provider ObjectFactProvider, candidateLimit int) *LogicalNode {
	t.Helper()
	config := prefilterConfigForField("partition")
	node, err := NewLogicalNode(LogicalNodeSpec{
		Key: identity.LogicalNodeKey{Rule: identity.RuleKey{RuleID: "object-facts-test"}, PlacementID: "test-placement"},
		Config: LogicalNodeConfig{
			MaxPlayers:   2,
			GroupBuilder: GroupBuilderConfig{CandidateLimitPerSeed: candidateLimit},
			Facts: []FactSpec{
				{Name: "tick_bonus", Type: FactTypeInt64},
				{Name: "priority", Type: FactTypeInt64},
			},
			Prefilter: config,
		},
		Rules:              rules,
		ObjectFactProvider: provider,
	})
	if err != nil {
		t.Fatalf("NewLogicalNode: %v", err)
	}
	return node
}

func requireFactError(t *testing.T, err error, code string) {
	t.Helper()
	var target *FactError
	if !errors.As(err, &target) || target.Code != code {
		t.Fatalf("error=%v, want FactError code %s", err, code)
	}
}
