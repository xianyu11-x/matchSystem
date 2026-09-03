package matchsystem

import (
	"context"
	"errors"
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

func TestTopCandidatesReusesScratchAcrossCallsWithoutAliasingResults(t *testing.T) {
	store := newTicketStore(nil)
	for docID := uint32(1); docID <= 3; docID++ {
		store.ticketsByDocID[docID] = &storedTicket{
			Ticket: &Ticket{TicketID: TicketID(docID)},
			docID:  docID,
		}
	}
	scratch := make(candidateHeap, 0, candidateRankingScratchCapacity)
	evaluator := &seedEvaluator{
		candidateScoringLimit: 3,
		candidateLimit:        2,
		store:                 store,
		scorer: func(input CandidateScoreContext) (float64, error) {
			return float64(input.Candidate.TicketID), nil
		},
	}
	session := &seedSession{
		evaluator:               evaluator,
		frame:                   fact.NewFrame(Facts{}, 1, false),
		candidateRankingScratch: &scratch,
	}
	seed := &storedTicket{Ticket: &Ticket{TicketID: 100}}

	first, err := session.topCandidates(context.Background(), prefilter.NewDocSet(1, 2, 3), seed, Facts{}, Facts{})
	if err != nil {
		t.Fatalf("first ranking: %v", err)
	}
	if got := candidateIDs(first); len(got) != 2 || got[0] != 3 || got[1] != 2 {
		t.Fatalf("first ranking=%v, want [3 2]", got)
	}
	assertCandidateRankingScratchCleared(t, scratch)

	second, err := session.topCandidates(context.Background(), prefilter.NewDocSet(1, 2), seed, Facts{}, Facts{})
	if err != nil {
		t.Fatalf("second ranking: %v", err)
	}
	if got := candidateIDs(second); len(got) != 2 || got[0] != 2 || got[1] != 1 {
		t.Fatalf("second ranking=%v, want [2 1]", got)
	}
	if got := candidateIDs(first); len(got) != 2 || got[0] != 3 || got[1] != 2 {
		t.Fatalf("first result was changed by the second ranking: %v", got)
	}
	assertCandidateRankingScratchCleared(t, scratch)
}

func TestTopCandidatesScratchFallsBackWithoutTruncatingAboveFiveHundred(t *testing.T) {
	const candidateCount = candidateRankingScratchCapacity + 100
	store := newTicketStore(nil)
	docIDs := make([]uint32, candidateCount)
	for index := range docIDs {
		docID := uint32(index + 1)
		docIDs[index] = docID
		store.ticketsByDocID[docID] = &storedTicket{
			Ticket: &Ticket{TicketID: TicketID(docID)},
			docID:  docID,
		}
	}
	scratch := make(candidateHeap, 0, candidateRankingScratchCapacity)
	session := &seedSession{
		evaluator: &seedEvaluator{
			candidateScoringLimit: candidateCount,
			candidateLimit:        candidateCount,
			store:                 store,
			scorer: func(input CandidateScoreContext) (float64, error) {
				return float64(input.Candidate.TicketID), nil
			},
		},
		frame:                   fact.NewFrame(Facts{}, 1, false),
		candidateRankingScratch: &scratch,
	}
	seed := &storedTicket{Ticket: &Ticket{TicketID: TicketID(candidateCount + 1)}}

	got, err := session.topCandidates(context.Background(), prefilter.NewDocSet(docIDs...), seed, Facts{}, Facts{})
	if err != nil {
		t.Fatalf("ranking above scratch capacity: %v", err)
	}
	if len(got) != candidateCount {
		t.Fatalf("ranked %d candidates, want %d; scratch fallback truncated the result", len(got), candidateCount)
	}
	if got[0].TicketID != TicketID(candidateCount) || got[len(got)-1].TicketID != 1 {
		t.Fatalf("unexpected fallback ranking endpoints: first=%d last=%d", got[0].TicketID, got[len(got)-1].TicketID)
	}
	if gotCapacity := cap(scratch); gotCapacity != candidateRankingScratchCapacity {
		t.Fatalf("scratch capacity grew to %d, want fixed %d", gotCapacity, candidateRankingScratchCapacity)
	}
	assertCandidateRankingScratchCleared(t, scratch)
}

func TestTopCandidatesScratchClearsAfterError(t *testing.T) {
	store := newTicketStore(nil)
	for docID := uint32(1); docID <= 5; docID++ {
		store.ticketsByDocID[docID] = &storedTicket{
			Ticket: &Ticket{TicketID: TicketID(docID)},
			docID:  docID,
		}
	}
	scratch := make(candidateHeap, 0, candidateRankingScratchCapacity)
	failAt := 4
	calls := 0
	session := &seedSession{
		evaluator: &seedEvaluator{
			candidateScoringLimit: 5,
			candidateLimit:        5,
			store:                 store,
			scorer: func(input CandidateScoreContext) (float64, error) {
				calls++
				if calls == failAt {
					return 0, errors.New("synthetic ranking failure")
				}
				return float64(input.Candidate.TicketID), nil
			},
		},
		frame:                   fact.NewFrame(Facts{}, 1, false),
		candidateRankingScratch: &scratch,
	}
	seed := &storedTicket{Ticket: &Ticket{TicketID: 100}}

	if _, err := session.topCandidates(context.Background(), prefilter.NewDocSet(1, 2, 3, 4, 5), seed, Facts{}, Facts{}); err == nil {
		t.Fatal("ranking error was swallowed")
	}
	assertCandidateRankingScratchCleared(t, scratch)

	failAt = 0
	calls = 0
	got, err := session.topCandidates(context.Background(), prefilter.NewDocSet(1, 2, 3, 4, 5), seed, Facts{}, Facts{})
	if err != nil {
		t.Fatalf("ranking after error: %v", err)
	}
	if IDs := candidateIDs(got); len(IDs) != 5 || IDs[0] != 5 || IDs[4] != 1 {
		t.Fatalf("ranking after error=%v, want [5 4 3 2 1]", IDs)
	}
	assertCandidateRankingScratchCleared(t, scratch)
}

func assertCandidateRankingScratchCleared(t *testing.T, scratch candidateHeap) {
	t.Helper()
	if len(scratch) != 0 {
		t.Fatalf("candidate ranking scratch len=%d, want 0", len(scratch))
	}
	for index, entry := range scratch[:cap(scratch)] {
		if entry.ticket != nil || entry.score != 0 {
			t.Fatalf("candidate ranking scratch entry %d was not cleared: %#v", index, entry)
		}
	}
}

func candidateIDs(tickets []*storedTicket) []TicketID {
	ids := make([]TicketID, len(tickets))
	for index, ticket := range tickets {
		ids[index] = ticket.TicketID
	}
	return ids
}
