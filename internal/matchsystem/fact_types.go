package matchsystem

import (
	"matchSystem/internal/common"
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

// FactValidator is intended for provider contract tests and debug checks.
// Production matching trusts in-repository providers and does not invoke it
// on each Fact snapshot.
type FactValidator = fact.Validator

// MatchFactProvider computes complete Match-scoped Fact layers. MatchFact
// updates have no other production entry point.
type MatchFactProvider = fact.MatchFactProvider
type InitializeInput = fact.InitializeInput
type JoinInput = fact.JoinInput

// FactProvider runs once per ProduceMatch on the owning PhysicalNode goroutine.
// It is not cached for a whole MatchRound because committed matches may change
// dynamic Facts. It must not re-enter or mutate that PhysicalNode.
type FactProvider = fact.Provider

// ObjectFactProvider runs at most once per Ticket during one ProduceMatch and
// receives immutable inputs.
type ObjectFactProvider = fact.ObjectProvider
