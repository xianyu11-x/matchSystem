package matchsystem

import (
	"context"
	"testing"
)

func TestScoreCandidatePassesBorrowedReadOnlyViews(t *testing.T) {
	seed := &Ticket{TicketID: 1, Int64Values: map[string]int64{"level": 7}}
	candidate := &Ticket{TicketID: 2, Int64Values: map[string]int64{"level": 8}}
	tickFacts := Facts{Int64Values: map[string]int64{"tick": 1}}
	seedFacts := Facts{StringLists: map[string][]string{"seed": {"s"}}}
	candidateFacts := Facts{Uint64Lists: map[string][]uint64{"candidate": {2}}}
	var received CandidateScoreContext
	evaluator := &seedEvaluator{scorer: func(input CandidateScoreContext) (float64, error) {
		received = input
		return 1, nil
	}}

	score, err := evaluator.scoreCandidate(context.Background(), 123, seed, seedFacts, tickFacts, candidate, candidateFacts)
	if err != nil || score != 1 {
		t.Fatalf("scoreCandidate: score=%v err=%v", score, err)
	}
	if received.Seed != seed || received.Candidate != candidate {
		t.Fatal("scorer did not receive borrowed Ticket pointers")
	}
	if received.TickFacts.Int64Values["tick"] != 1 || received.SeedFacts.StringLists["seed"][0] != "s" || received.CandidateFacts.Uint64Lists["candidate"][0] != 2 {
		t.Fatalf("scorer received unexpected Fact views: %#v", received)
	}
	tickFacts.Int64Values["tick"] = 9
	seedFacts.StringLists["seed"][0] = "updated"
	candidateFacts.Uint64Lists["candidate"][0] = 99
	if received.TickFacts.Int64Values["tick"] != 9 || received.SeedFacts.StringLists["seed"][0] != "updated" || received.CandidateFacts.Uint64Lists["candidate"][0] != 99 {
		t.Fatal("scorer Fact views were copied instead of borrowed")
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := evaluator.scoreCandidate(canceled, 123, seed, seedFacts, tickFacts, candidate, candidateFacts); err == nil {
		t.Fatal("scoreCandidate ignored context cancellation")
	}

}
