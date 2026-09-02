package matchsystem

import (
	"container/heap"
	"context"
	"errors"
	"fmt"
	"math"
	"sort"

	"matchSystem/internal/matchsystem/evaluation"
	"matchSystem/internal/matchsystem/prefilter"
)

func (s *seedSession) topCandidates(ctx context.Context, candidates *prefilter.DocSet, seed *storedTicket, seedFacts, tickFacts Facts) ([]*storedTicket, error) {
	e := s.evaluator
	rankingStart := s.trace.start()
	if s.trace != nil {
		defer func() { s.trace.addDuration(produceStageCandidateRanking, rankingStart) }()
	}
	limit := e.candidateLimit
	best := make(candidateHeap, 0, limit)
	var candidateErrors []error
	scoringFailed := false
	contextFailed := false
	candidates.ForEach(func(docID uint32) bool {
		s.trace.recordCandidateVisited()
		if contextErr := ctx.Err(); contextErr != nil {
			candidateErrors = append(candidateErrors, contextErr)
			contextFailed = true
			return false
		}
		ticket, ok := e.store.lookupDocID(docID)
		if !ok {
			return true
		}
		candidateFactStart := s.trace.start()
		s.trace.recordCandidateMaterialization()
		candidateFacts, objectAccess, err := s.frame.Object(ticket.objectFacts, ticket.Ticket, s.now, e.objectFacts)
		s.trace.recordObjectFactAccess(objectAccess)
		s.trace.addDuration(produceStageCandidateMaterialization, candidateFactStart)
		if err != nil {
			candidateErrors = append(candidateErrors, fmt.Errorf("candidate %d: create Facts: %w", ticket.TicketID, err))
			if isContextTermination(err) {
				contextFailed = true
				return false
			}
			return true
		}
		scoreStart := s.trace.start()
		s.trace.recordCandidateScore()
		score, err := e.scoreCandidate(ctx, s.now, seed.Ticket, seedFacts, tickFacts, ticket.Ticket, candidateFacts)
		s.trace.addDuration(produceStageCandidateScoring, scoreStart)
		if err != nil {
			candidateErrors = append(candidateErrors, fmt.Errorf("candidate %d: score: %w", ticket.TicketID, err))
			scoringFailed = true
			return false
		}
		entry := candidateEntry{ticket: ticket, score: score}
		if len(best) < limit {
			heap.Push(&best, entry)
		} else if betterCandidate(entry, best[0]) {
			best[0] = entry
			heap.Fix(&best, 0)
		}
		s.trace.recordRankedCandidate()
		return true
	})
	if scoringFailed || contextFailed {
		// A scorer is part of candidate selection. Once it fails, or the
		// context reaches a terminal state, fail closed instead of allowing a
		// later candidate to form a Match from a partial ranking.
		return nil, errors.Join(candidateErrors...)
	}
	sortStart := s.trace.start()
	s.trace.recordCandidateSort()
	sort.Slice(best, func(i, j int) bool { return betterCandidate(best[i], best[j]) })
	s.trace.addDuration(produceStageCandidateSort, sortStart)
	out := make([]*storedTicket, len(best))
	for i := range best {
		out[i] = best[i].ticket
	}
	return out, errors.Join(candidateErrors...)
}

func (e *seedEvaluator) scoreCandidate(ctx context.Context, now int64, seed *Ticket, seedFacts, tickFacts Facts, candidate *Ticket, candidateFacts Facts) (score float64, err error) {
	if e.scorer == nil {
		return 0, evalError("candidateScorer", "MISSING_SCORER", "CandidateScorer is nil")
	}
	if canceled := ctx.Err(); canceled != nil {
		return 0, canceled
	}
	input := CandidateScoreContext{
		// CandidateScorer receives borrowed read-only views. The owner goroutine
		// keeps the Ticket store and Fact slots stable for this synchronous call;
		// cloning here would dominate large candidate pools and is no longer an
		// isolation boundary for the internal scorer seam.
		Seed:           seed,
		Candidate:      candidate,
		Now:            now,
		TickFacts:      tickFacts,
		SeedFacts:      seedFacts,
		CandidateFacts: candidateFacts,
	}
	score, err = e.scorer(input)
	if err != nil {
		return 0, &evaluation.Error{Phase: "evaluate", Path: "candidateScorer", Code: "SCORER_ERROR", Err: err}
	}
	if math.IsNaN(score) || math.IsInf(score, 0) {
		return 0, evalError("candidateScorer", "NONFINITE_SCORE", "candidate scorer returned non-finite score")
	}
	return score, err
}

type candidateEntry struct {
	ticket *storedTicket
	score  float64
}

type candidateHeap []candidateEntry

func (h candidateHeap) Len() int { return len(h) }
func (h candidateHeap) Less(i, j int) bool {
	if h[i].score != h[j].score {
		return h[i].score < h[j].score
	}
	return h[i].ticket.docID > h[j].ticket.docID
}
func (h candidateHeap) Swap(i, j int)   { h[i], h[j] = h[j], h[i] }
func (h *candidateHeap) Push(value any) { *h = append(*h, value.(candidateEntry)) }
func (h *candidateHeap) Pop() any {
	old := *h
	value := old[len(old)-1]
	*h = old[:len(old)-1]
	return value
}

func betterCandidate(left, right candidateEntry) bool {
	if left.score != right.score {
		return left.score > right.score
	}
	return left.ticket.docID < right.ticket.docID
}
