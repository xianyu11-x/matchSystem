package matchsystem

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"testing"

	"matchSystem/internal/matchsystem/fact"
	"matchSystem/internal/matchsystem/prefilter"
)

func TestTopCandidatesAppendSortMatchesReferenceRanking(t *testing.T) {
	store := newTicketStore(nil)
	for docID := uint32(1); docID <= 5; docID++ {
		store.ticketsByDocID[docID] = &storedTicket{
			Ticket: &Ticket{TicketID: TicketID(docID)},
			docID:  docID,
		}
	}
	scores := map[TicketID]float64{
		1: 3,
		2: 9,
		3: 9,
		4: 1,
		5: 9,
	}
	tests := []struct {
		name           string
		docIDs         []uint32
		candidateLimit int
		scoringLimit   int
	}{
		{name: "equal limits use append sort", docIDs: []uint32{1, 2, 3, 4, 5}, candidateLimit: 5, scoringLimit: 5},
		{name: "top-l larger than scoring pool", docIDs: []uint32{1, 2, 3, 4, 5}, candidateLimit: 8, scoringLimit: 5},
		{name: "candidate set shorter than scoring pool", docIDs: []uint32{1, 2, 3}, candidateLimit: 5, scoringLimit: 8},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			scratch := make(candidateHeap, 0, candidateRankingScratchCapacity)
			session := &seedSession{
				evaluator: &seedEvaluator{
					candidateScoringLimit: test.scoringLimit,
					candidateLimit:        test.candidateLimit,
					store:                 store,
					scorer: func(input CandidateScoreContext) (float64, error) {
						return scores[input.Candidate.TicketID], nil
					},
				},
				frame:                   fact.NewFrame(Facts{}, 1, false),
				candidateRankingScratch: &scratch,
			}
			seed := &storedTicket{Ticket: &Ticket{TicketID: 100}}

			got, err := session.topCandidates(context.Background(), prefilter.NewDocSet(test.docIDs...), seed, Facts{}, Facts{})
			if err != nil {
				t.Fatalf("fast-path ranking: %v", err)
			}
			want := referenceCandidateRankingIDs(store, test.docIDs, test.candidateLimit, test.scoringLimit, scores)
			if IDs := candidateIDs(got); !reflect.DeepEqual(IDs, want) {
				t.Fatalf("fast-path ranking=%v, reference=%v", IDs, want)
			}
			assertCandidateRankingScratchCleared(t, scratch)
		})
	}
}

func TestTopCandidatesFastPathPreservesErrorAndCancelSemantics(t *testing.T) {
	store := newTicketStore(nil)
	for docID := uint32(1); docID <= 3; docID++ {
		store.ticketsByDocID[docID] = &storedTicket{
			Ticket: &Ticket{TicketID: TicketID(docID)},
			docID:  docID,
		}
	}
	scratch := make(candidateHeap, 0, candidateRankingScratchCapacity)
	calls := 0
	failAt := 2
	session := &seedSession{
		evaluator: &seedEvaluator{
			candidateScoringLimit: 3,
			candidateLimit:        3,
			store:                 store,
			scorer: func(input CandidateScoreContext) (float64, error) {
				calls++
				if calls == failAt {
					return 0, errors.New("synthetic score failure")
				}
				return float64(input.Candidate.TicketID), nil
			},
		},
		frame:                   fact.NewFrame(Facts{}, 1, false),
		candidateRankingScratch: &scratch,
	}
	seed := &storedTicket{Ticket: &Ticket{TicketID: 100}}
	candidates := prefilter.NewDocSet(1, 2, 3)

	if _, err := session.topCandidates(context.Background(), candidates, seed, Facts{}, Facts{}); err == nil {
		t.Fatal("fast path swallowed scorer error")
	}
	if calls != failAt {
		t.Fatalf("scorer calls after error=%d, want %d", calls, failAt)
	}
	assertCandidateRankingScratchCleared(t, scratch)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	failAt = 0
	calls = 0
	if _, err := session.topCandidates(ctx, candidates, seed, Facts{}, Facts{}); err == nil {
		t.Fatal("fast path ignored canceled context")
	}
	if calls != 0 {
		t.Fatalf("scorer ran %d times for canceled context, want 0", calls)
	}
	assertCandidateRankingScratchCleared(t, scratch)
}

func TestTopCandidatesFastPathUsesDocIDTieBreak(t *testing.T) {
	store := newTicketStore(nil)
	store.ticketsByDocID[10] = &storedTicket{Ticket: &Ticket{TicketID: 300}, docID: 10}
	store.ticketsByDocID[20] = &storedTicket{Ticket: &Ticket{TicketID: 100}, docID: 20}
	store.ticketsByDocID[30] = &storedTicket{Ticket: &Ticket{TicketID: 200}, docID: 30}
	scratch := make(candidateHeap, 0, candidateRankingScratchCapacity)
	session := &seedSession{
		evaluator: &seedEvaluator{
			candidateScoringLimit: 3,
			candidateLimit:        3,
			store:                 store,
			scorer: func(CandidateScoreContext) (float64, error) {
				return 1, nil
			},
		},
		frame:                   fact.NewFrame(Facts{}, 1, false),
		candidateRankingScratch: &scratch,
	}
	seed := &storedTicket{Ticket: &Ticket{TicketID: 999}}

	got, err := session.topCandidates(context.Background(), prefilter.NewDocSet(10, 20, 30), seed, Facts{}, Facts{})
	if err != nil {
		t.Fatalf("tie-break ranking: %v", err)
	}
	if IDs := candidateIDs(got); !reflect.DeepEqual(IDs, []TicketID{300, 100, 200}) {
		t.Fatalf("tie-break ranking=%v, want [300 100 200]", IDs)
	}
	assertCandidateRankingScratchCleared(t, scratch)
}

func referenceCandidateRankingIDs(store *ticketStore, docIDs []uint32, candidateLimit, scoringLimit int, scores map[TicketID]float64) []TicketID {
	entries := make(candidateHeap, 0, len(docIDs))
	for index, docID := range docIDs {
		if index >= scoringLimit {
			break
		}
		ticket, ok := store.lookupDocID(docID)
		if !ok {
			continue
		}
		entries = append(entries, candidateEntry{ticket: ticket, score: scores[ticket.TicketID]})
	}
	sort.Slice(entries, func(i, j int) bool { return betterCandidate(entries[i], entries[j]) })
	if len(entries) > candidateLimit {
		entries = entries[:candidateLimit]
	}
	result := make([]TicketID, len(entries))
	for index, entry := range entries {
		result[index] = entry.ticket.TicketID
	}
	return result
}
