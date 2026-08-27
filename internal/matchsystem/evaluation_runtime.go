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
// The Ticket and Fact layers are owned snapshots for the duration of the
// callback.  A scorer must not retain or mutate them.
type CandidateScoreContext struct {
	Seed           *common.Ticket
	Candidate      *common.Ticket
	Now            int64
	TickFacts      fact.Values
	SeedFacts      fact.Values
	CandidateFacts fact.Values
}

// CandidateScorer computes the ranking score for one candidate.  A scorer is
// bound directly to exactly one LogicalNodeSpec; it is not named or resolved
// through an Evaluation registry.
type CandidateScorer func(CandidateScoreContext) (float64, error)
