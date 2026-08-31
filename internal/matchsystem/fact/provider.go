package fact

import (
	"context"

	"matchSystem/internal/common"
)

// ProviderDescriptor is the startup contract advertised by one Fact
// provider. Facts must describe the complete set of values produced by the
// provider for the provider's scope. The matching rule remains the source of
// truth; the descriptor is the provider-side declaration used during the
// LogicalNode startup handshake.
//
// ID is a stable implementation identifier and Version identifies the
// provider contract implementation. Both are required when the associated
// rule declares at least one Fact for the provider's scope.
type ProviderDescriptor struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Facts   []Spec `json:"facts"`
}

// Clone returns an owned copy of the descriptor and its Fact declarations.
func (d ProviderDescriptor) Clone() ProviderDescriptor {
	d.Facts = append([]Spec(nil), d.Facts...)
	return d
}

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
