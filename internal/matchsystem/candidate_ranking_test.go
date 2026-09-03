package matchsystem

import (
	"context"
	"testing"

	"matchSystem/internal/matchsystem/fact"
	"matchSystem/internal/matchsystem/prefilter"
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

func TestTopCandidatesBoundsScoringPoolBeforeTopL(t *testing.T) {
	store := newTicketStore(nil)
	for docID := uint32(1); docID <= 5; docID++ {
		ticket := &storedTicket{
			Ticket: &Ticket{TicketID: TicketID(docID), CreatedAt: int64(docID)},
			docID:  docID,
		}
		store.ticketsByDocID[docID] = ticket
	}

	var scored []TicketID
	evaluator := &seedEvaluator{
		candidateScoringLimit: 3,
		candidateLimit:        2,
		store:                 store,
		scorer: func(input CandidateScoreContext) (float64, error) {
			scored = append(scored, input.Candidate.TicketID)
			return float64(input.Candidate.CreatedAt), nil
		},
	}
	seed := &storedTicket{Ticket: &Ticket{TicketID: 100}, docID: 100}
	session := &seedSession{
		evaluator: evaluator,
		frame:     fact.NewFrame(Facts{}, 1, false),
	}

	got, err := session.topCandidates(context.Background(), prefilter.NewDocSet(1, 2, 3, 4, 5), seed, Facts{}, Facts{})
	if err != nil {
		t.Fatalf("rank candidates: %v", err)
	}
	if len(scored) != 3 {
		t.Fatalf("scored %d candidates, want scoring limit 3 (%v)", len(scored), scored)
	}
	for index, want := range []TicketID{1, 2, 3} {
		if scored[index] != want {
			t.Fatalf("scored candidate %d: got %d, want %d", index, scored[index], want)
		}
	}
	if len(got) != 2 {
		t.Fatalf("ranked %d candidates, want Top-L 2", len(got))
	}
	for index, want := range []TicketID{3, 2} {
		if got[index].TicketID != want {
			t.Fatalf("ranked candidate %d: got %d, want %d", index, got[index].TicketID, want)
		}
	}
}

func TestTopCandidatesEffectiveLimitCombinations(t *testing.T) {
	store := newTicketStore(nil)
	for docID := uint32(1); docID <= 5; docID++ {
		store.ticketsByDocID[docID] = &storedTicket{
			Ticket: &Ticket{TicketID: TicketID(docID)},
			docID:  docID,
		}
	}
	candidates := prefilter.NewDocSet(1, 2, 3, 4, 5)
	seed := &storedTicket{Ticket: &Ticket{TicketID: 100}, docID: 100}

	tests := []struct {
		name           string
		candidateLimit int
		scoringLimit   int
		want           []TicketID
	}{
		{name: "top-l smaller than scoring pool", candidateLimit: 2, scoringLimit: 3, want: []TicketID{3, 2}},
		{name: "scoring pool smaller than top-l", candidateLimit: 4, scoringLimit: 2, want: []TicketID{2, 1}},
		{name: "zero candidate limit uses default", candidateLimit: 0, scoringLimit: 3, want: []TicketID{3, 2, 1}},
		{name: "negative candidate limit uses default", candidateLimit: -1, scoringLimit: 3, want: []TicketID{3, 2, 1}},
		{name: "zero scoring limit uses default", candidateLimit: 2, scoringLimit: 0, want: []TicketID{5, 4}},
		{name: "negative scoring limit uses default", candidateLimit: 2, scoringLimit: -1, want: []TicketID{5, 4}},
		{name: "both non-positive use defaults", candidateLimit: 0, scoringLimit: 0, want: []TicketID{5, 4, 3, 2, 1}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var scored []TicketID
			evaluator := &seedEvaluator{
				candidateScoringLimit: test.scoringLimit,
				candidateLimit:        test.candidateLimit,
				store:                 store,
				scorer: func(input CandidateScoreContext) (float64, error) {
					scored = append(scored, input.Candidate.TicketID)
					return float64(input.Candidate.TicketID), nil
				},
			}
			session := &seedSession{
				evaluator: evaluator,
				frame:     fact.NewFrame(Facts{}, 1, false),
			}

			got, err := session.topCandidates(context.Background(), candidates, seed, Facts{}, Facts{})
			if err != nil {
				t.Fatalf("rank candidates: %v", err)
			}
			if len(scored) == 0 {
				t.Fatal("ranker did not score any candidates")
			}
			if len(got) != len(test.want) {
				t.Fatalf("ranked %d candidates, want %d", len(got), len(test.want))
			}
			for index, want := range test.want {
				if got[index].TicketID != want {
					t.Errorf("ranked candidate %d: got %d, want %d", index, got[index].TicketID, want)
				}
			}
		})
	}
}
