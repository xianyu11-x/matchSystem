package fact

import (
	"context"

	"matchSystem/internal/common"
)

// MatchFactProvider is the sole owner of Match-scoped Fact computation.
//
// Implementations return a complete Match Fact Values layer from both methods;
// they do not return patches. LogicalNode passes owned callback snapshots for
// every Ticket and Values field, so a provider cannot mutate matching state by
// changing its input. Providers must still treat those snapshots as call-scoped
// and must not retain them.
type MatchFactProvider interface {
	Initialize(context.Context, InitializeInput) (Values, error)
	OnJoin(context.Context, JoinInput) (Values, error)
}

// InitializeInput contains the only values a provider may read while creating
// the initial Match Fact layer for a seed.
type InitializeInput struct {
	Now            int64
	SeedAttributes *common.Ticket
	SeedFacts      Values
	TickFacts      Values
}

// JoinInput contains the only values a provider may read while creating the
// next complete Match Fact layer for one candidate. MatchFactsBefore is the
// complete layer immediately before the candidate is joined.
type JoinInput struct {
	Now              int64
	SeedAttributes   *common.Ticket
	SeedFacts        Values
	TickFacts        Values
	Candidate        *common.Ticket
	CandidateFacts   Values
	MatchFactsBefore Values
}
