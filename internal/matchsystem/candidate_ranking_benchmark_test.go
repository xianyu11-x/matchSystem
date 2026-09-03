package matchsystem

import (
	"context"
	"runtime"
	"testing"

	"matchSystem/internal/matchsystem/fact"
	"matchSystem/internal/matchsystem/prefilter"
)

// BenchmarkCandidateRankingHeapCapacity exercises the benchmark shape where
// candidateLimitPerSeed is deliberately much larger than the scoring pool.
// The heap only retains the best scoringLimit entries, so its initial backing
// array should be bounded by that effective retained count.
func BenchmarkCandidateRankingHeapCapacity(b *testing.B) {
	const (
		candidateCount = 5464
		candidateLimit = 100000
		scoringLimit   = 500
	)

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
	session := &seedSession{
		evaluator: &seedEvaluator{
			candidateScoringLimit: scoringLimit,
			candidateLimit:        candidateLimit,
			store:                 store,
			scorer: func(input CandidateScoreContext) (float64, error) {
				return float64(input.Candidate.TicketID), nil
			},
		},
		frame: fact.NewFrame(Facts{}, 1, false),
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
