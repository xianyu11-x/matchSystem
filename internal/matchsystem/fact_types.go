package matchsystem

import (
	"context"

	"matchSystem/internal/common"
	"matchSystem/internal/identity"
	"matchSystem/internal/matchsystem/fact"
)

// These aliases expose the single canonical Fact model at the matchsystem API
// boundary. They do not create another representation or perform conversions.
type FactType = fact.Type

const (
	FactTypeStrings = fact.TypeStrings
	FactTypeInt64   = fact.TypeInt64
	FactTypeUint64s = fact.TypeUint64s
)

type FactSpec = fact.Spec
type FactScope = fact.Scope

const (
	FactScopeTick   = fact.ScopeTick
	FactScopeObject = fact.ScopeObject
	FactScopeMatch  = fact.ScopeMatch
)

type Facts = fact.Values
type MatchFacts = common.MatchFacts
type FactError = fact.Error
type ProviderDescriptor = fact.ProviderDescriptor

// FactValidator is intended for provider contract tests and debug checks.
// Production matching trusts in-repository providers and does not invoke it
// on each Fact snapshot.
type FactValidator = fact.Validator

// MatchFactProvider computes complete Match-scoped Fact layers. MatchFact
// updates have no other production entry point.
type MatchFactProvider = fact.MatchFactProvider
type InitializeInput = fact.InitializeInput
type JoinInput = fact.JoinInput

// FactProvider runs once per ProduceMatch on the owning PhysicalNode goroutine
// and receives a value-only TickFactInput. It is not cached for a whole
// MatchRound because committed matches may change dynamic Facts. It must not
// re-enter or mutate that PhysicalNode.
//
// The provider is defined at this facade because its input intentionally
// carries LogicalNode state; the source-independent fact package remains free
// of a dependency on the owning node.
type FactProvider func(context.Context, TickFactInput) (Facts, error)

// Provide keeps the legacy function callback callable through typed wrappers.
// A FactProvider value alone does not satisfy TickProvider because it has no
// Describe method; a provider bound to LogicalNode must expose both methods on
// the same concrete value.
func (f FactProvider) Provide(ctx context.Context, input TickFactInput) (Facts, error) {
	if f == nil {
		return Facts{}, nil
	}
	return f(ctx, input)
}

// TickProvider is the self-describing Tick Fact provider port accepted by a
// LogicalNode. The descriptor is owned by the implementation and is captured
// once during the LogicalNode load handshake.
type TickProvider interface {
	Provide(context.Context, TickFactInput) (Facts, error)
	Describe() (ProviderDescriptor, error)
}

// DescribedFactProvider is the Go Provider handshake shape for Tick Facts.
// Describe is called once while the LogicalNode is loaded; the returned
// descriptor is owned by the matching runtime and is never derived from the
// Rule Contract or a runtime value layer.
type DescribedFactProvider = TickProvider

// LogicalNodeSnapshot is the immutable point-in-time state exposed to a
// FactProvider. It deliberately contains values only: a provider cannot
// re-enter the node, enumerate Tickets, or retain a mutable internal object.
// WaitingCount is the number of active Tickets owned by the LogicalNode at
// the time the provider is invoked. A successful Match has not been committed
// yet, so its seed is still included in this count.
type LogicalNodeSnapshot struct {
	Key          identity.LogicalNodeKey
	State        LogicalNodeState
	WaitingCount int
}

// TickFactInput is the call-scoped input for a FactProvider. The Node
// snapshot is captured by the LogicalNode owner immediately before invoking
// the provider and must be treated as read-only by the callback.
type TickFactInput struct {
	Now  int64
	Node LogicalNodeSnapshot
}

// ObjectFactWriter is the schema-bound, synchronous writer supplied to an
// ObjectFactProvider. Values written to it are copied into a reusable
// per-Ticket slot; the input Ticket and Tick Facts are borrowed read-only.
type ObjectFactWriter = fact.Writer

// ObjectFactProvider runs at most once per Ticket per ProduceMatch generation
// and writes into the supplied ObjectFactWriter. It must not retain or mutate
// its borrowed inputs.
type ObjectFactProvider = fact.ObjectProvider

// ObjectProvider is the Go Provider handshake shape for Object Facts. The
// callback is deliberately kept separate from the low-level fact package's
// schema-bound writer so the core can retain its owner-goroutine execution
// model.
type ObjectProvider interface {
	Provide(*common.Ticket, int64, Facts, ObjectFactWriter) error
	Describe() (ProviderDescriptor, error)
}

// DescribedObjectFactProvider is retained as a descriptive alias for callers
// that use the older name.
type DescribedObjectFactProvider = ObjectProvider

// MatchProvider is the Go Provider handshake shape for Match Facts.
// MatchFactProvider remains the execution interface for source-independent
// callers; this extension supplies the provider-owned capability descriptor at
// LogicalNode load time.
type MatchProvider interface {
	MatchFactProvider
	Describe() (ProviderDescriptor, error)
}

// DescribedMatchFactProvider is retained as a descriptive alias for callers
// that use the older name.
type DescribedMatchFactProvider = MatchProvider
