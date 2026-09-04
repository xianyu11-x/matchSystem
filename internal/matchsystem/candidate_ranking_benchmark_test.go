package matchsystem

import (
	"context"
	"runtime"
	"testing"

	"matchSystem/internal/matchsystem/fact"
	"matchSystem/internal/matchsystem/prefilter"
)

// BenchmarkCandidateRankingHeapCapacity keeps the historical shape where
// candidateLimitPerSeed is deliberately much larger than the scoring pool.
// With the current limit>=scoringLimit fast path this exercises append+sort.
func BenchmarkCandidateRankingHeapCapacity(b *testing.B) {
	benchmarkCandidateRanking(b, 5464, 100000, 500)
}

// BenchmarkCandidateRankingBoundedHeap exercises the explicit L=50/S=500
// benchmark shape, where candidateLimitPerSeed is smaller than the scoring
// pool and topCandidates must retain the result with a bounded heap.
func BenchmarkCandidateRankingBoundedHeap(b *testing.B) {
	benchmarkCandidateRanking(b, 5464, 50, 500)
}

func benchmarkCandidateRanking(b *testing.B, candidateCount, candidateLimit, scoringLimit int) {
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
	candidates := prefilter.NewDocSet(docIDs...)
	scratch := make(candidateHeap, 0, candidateRankingScratchCapacity)
	session := &seedSession{
		evaluator: &seedEvaluator{
			candidateScoringLimit: scoringLimit,
			candidateLimit:        candidateLimit,
			store:                 store,
			scorer: func(input CandidateScoreContext) (float64, error) {
				return float64(input.Candidate.TicketID), nil
			},
		},
		frame:                   fact.NewFrame(Facts{}, 1, false),
		candidateRankingScratch: &scratch,
	}
	seed := &storedTicket{Ticket: &Ticket{TicketID: TicketID(candidateCount + 1)}}

	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		got, err := session.topCandidates(context.Background(), candidates, seed, Facts{}, Facts{})
		if err != nil {
			b.Fatal(err)
		}
		runtime.KeepAlive(got)
	}
}
