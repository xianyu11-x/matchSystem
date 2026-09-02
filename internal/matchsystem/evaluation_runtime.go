package matchsystem

import (
	"matchSystem/internal/common"
	"matchSystem/internal/matchsystem/fact"
)

// CandidateScoreContext is the complete read-only view available to one
// LogicalNode's direct scorer.  It intentionally contains no Match or
// Match-scoped Facts, so scoring cannot inspect existing members or mutate
// aggregate state.
//
// The Ticket and Fact layers are borrowed read-only views for the duration of
// the synchronous callback. A scorer must not retain or mutate them. The
// owner goroutine keeps the underlying store/slots stable for this call.
type CandidateScoreContext struct {
	Seed           *common.Ticket
	Candidate      *common.Ticket
	Now            int64
	TickFacts      fact.Values
	SeedFacts      fact.Values
	CandidateFacts fact.Values
}

// CandidateScorer computes the ranking score for one candidate. Production
// LogicalNodes receive a built-in scorer compiled from match-rule/v1; the
// function type is the internal runtime seam, not a second configuration path.
type CandidateScorer func(CandidateScoreContext) (float64, error)
