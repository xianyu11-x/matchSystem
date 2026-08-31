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
type FactView = fact.View
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

// ObjectFactProvider runs at most once per Ticket during one ProduceMatch and
// receives immutable inputs.
type ObjectFactProvider = fact.ObjectProvider
