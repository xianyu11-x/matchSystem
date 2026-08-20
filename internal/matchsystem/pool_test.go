package matchsystem

import "testing"

func TestGreedyMatchesThroughGenericExtensions(t *testing.T) {
	rules := NewRuleSet(
		attributeFilter("partition"),
		minimumGroupSize(2),
	)
	pool := NewMatchPool(PoolConfig{MaxPlayers: 2}, rules)
	pool.Add(testTicket("a", 1, "blue"))
	pool.Add(testTicket("b", 2, "blue"))
	pool.Add(testTicket("c", 3, "green"))

	matches := pool.Tick(100)
	if len(matches) != 1 || len(matches[0].Tickets) != 2 {
		t.Fatalf("expected one two-ticket match, got %#v", matches)
	}
	if matches[0].Tickets[0].TicketID != "a" || matches[0].Tickets[1].TicketID != "b" {
		t.Fatalf("unexpected greedy group: %s, %s", matches[0].Tickets[0].TicketID, matches[0].Tickets[1].TicketID)
	}
	if pool.Len() != 1 {
		t.Fatalf("expected one waiting ticket, got %d", pool.Len())
	}
}

func TestEmptyRuleSetDoesNotStartImplicitMatches(t *testing.T) {
	pool := NewMatchPool(PoolConfig{}, nil)
	pool.Add(testTicket("a", 1, ""))
	if matches := pool.Tick(100); len(matches) != 0 {
		t.Fatalf("empty rules must not start matches: %#v", matches)
	}
	if pool.Len() != 1 {
		t.Fatalf("ticket should remain in the pool")
	}
}

func TestGenericForceStartExtension(t *testing.T) {
	forceAfter := FuncGroupEvaluator{
		EvaluatorFlagsValue: GroupEvaluatorForceStart,
		AllowFn: func(ctx GroupEvaluatorContext, _ []*Ticket, _ *Ticket) bool {
			return ctx.Now-ctx.Seed.CreatedAt >= 50
		},
	}
	pool := NewMatchPool(PoolConfig{}, NewRuleSet(forceAfter))
	pool.Add(testTicket("a", 10, ""))
	if matches := pool.Tick(59); len(matches) != 0 {
		t.Fatalf("force-started too early: %#v", matches)
	}
	if matches := pool.Tick(60); len(matches) != 1 || len(matches[0].Tickets) != 1 {
		t.Fatalf("expected one forced match, got %#v", matches)
	}
}

func TestGreedyUsesCustomCandidateScore(t *testing.T) {
	rules := NewRuleSet(minimumGroupSize(2)).WithCandidateScore(
		func(_ *Ticket, candidate *Ticket, _ int64) float64 {
			return float64(candidate.Numeric["priority"])
		},
	)
	pool := NewMatchPool(PoolConfig{MaxPlayers: 2}, rules)
	pool.Add(&Ticket{TicketID: "seed", CreatedAt: 1})
	pool.Add(&Ticket{TicketID: "low", CreatedAt: 2, Numeric: map[string]int64{"priority": 1}})
	pool.Add(&Ticket{TicketID: "high", CreatedAt: 3, Numeric: map[string]int64{"priority": 10}})

	matches := pool.Tick(100)
	if len(matches) != 1 || matches[0].Tickets[1].TicketID != "high" {
		t.Fatalf("custom score was not applied: %#v", matches)
	}
}

func TestRemoveAndIndexCleanup(t *testing.T) {
	pool := NewMatchPool(PoolConfig{}, NewRuleSet())
	docID := pool.Add(&Ticket{
		TicketID:   "a",
		Attributes: map[string]string{"partition": "blue"},
		Numeric:    map[string]int64{"rank": 7},
	})
	if !pool.Remove("a") || pool.Remove("a") {
		t.Fatalf("remove should succeed exactly once")
	}
	ctx := CandidateFilterContext{Pool: pool}
	if _, ok := ctx.Ticket(docID); ok || ctx.AttributeEquals("partition", "blue").Len() != 0 || ctx.NumericRange("rank", 7, 7).Len() != 0 {
		t.Fatalf("removed ticket remained in a generic index")
	}
}

func attributeFilter(field string) FuncCandidateFilter {
	return FuncCandidateFilter{
		EstimateFn: func(ctx CandidateFilterContext, candidates TicketSet) CandidateFilterEstimate {
			value, ok := ctx.Seed.Attributes[field]
			if !ok {
				return CandidateFilterEstimate{EstimatedOut: 0, Cost: 1, SupportsIndex: true, PreferIndex: true}
			}
			return CandidateFilterEstimate{
				EstimatedOut:  ctx.EstimateAttributeEquals(field, value),
				Cost:          1,
				SupportsIndex: true,
				PreferIndex:   true,
			}
		},
		FilterFn: func(ctx CandidateFilterContext, candidates TicketSet, source CandidateFilterSource) TicketSet {
			value, ok := ctx.Seed.Attributes[field]
			if !ok {
				return NewTicketSet()
			}
			if source == CandidateFilterFromIndex {
				return ctx.AttributeEquals(field, value)
			}
			out := NewTicketSet()
			for docID := range candidates {
				if ticket, exists := ctx.Ticket(docID); exists && ticket.Attributes[field] == value {
					out.Add(docID)
				}
			}
			return out
		},
	}
}

func minimumGroupSize(size int) FuncGroupEvaluator {
	return FuncGroupEvaluator{
		EvaluatorFlagsValue: GroupEvaluatorStart,
		AllowFn: func(_ GroupEvaluatorContext, group []*Ticket, _ *Ticket) bool {
			return len(group) >= size
		},
	}
}

func testTicket(id string, createdAt int64, partition string) *Ticket {
	attributes := map[string]string{}
	if partition != "" {
		attributes["partition"] = partition
	}
	return &Ticket{TicketID: id, CreatedAt: createdAt, Attributes: attributes}
}
