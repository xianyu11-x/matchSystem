// Package expression provides the small, domain-neutral scalar expression
// language shared by Prefilter and Evaluation.  The package deliberately
// exposes only the JSON compiler and an opaque typed program; syntax trees and
// compiler IR stay private to this package.
package expression

import (
	"fmt"
	"sort"
	"unicode/utf8"

	"matchSystem/internal/matchsystem/fact"
)

// ResultType is the value produced by one scalar expression.
type ResultType uint8

const (
	ResultInvalid ResultType = iota
	ResultBool
	ResultInt64
	ResultStrings
	ResultUint64s
)

func (t ResultType) String() string {
	switch t {
	case ResultBool:
		return "bool"
	case ResultInt64:
		return "int64"
	case ResultStrings:
		return "strings"
	case ResultUint64s:
		return "uint64s"
	default:
		return "invalid"
	}
}

func validResultType(t ResultType) bool {
	return t >= ResultBool && t <= ResultUint64s
}

func scalarResultFactType(t ResultType) (fact.Type, bool) {
	switch t {
	case ResultInt64:
		return fact.TypeInt64, true
	case ResultStrings:
		return fact.TypeStrings, true
	case ResultUint64s:
		return fact.TypeUint64s, true
	default:
		return 0, false
	}
}

// Source identifies one data source visible to an expression.  Fact source
// selection is explicit; a phase never falls back to another lifecycle scope.
type Source uint8

const (
	SourceSeedAttributes Source = iota + 1
	SourceSeedFacts
	SourceTickFacts
	SourceCandidateAttributes
	SourceCandidateFacts
	SourceMatchFacts
)

func (s Source) String() string {
	switch s {
	case SourceSeedAttributes:
		return "seed_attributes"
	case SourceSeedFacts:
		return "seed_facts"
	case SourceTickFacts:
		return "tick_facts"
	case SourceCandidateAttributes:
		return "candidate_attributes"
	case SourceCandidateFacts:
		return "candidate_facts"
	case SourceMatchFacts:
		return "match_facts"
	default:
		return "invalid"
	}
}

// Capabilities are the sources a compiler permits an expression to read.
type Capabilities uint32

const (
	CapabilitySeedAttributes Capabilities = 1 << iota
	CapabilitySeedFacts
	CapabilityTickFacts
	CapabilityCandidateAttributes
	CapabilityCandidateFacts
	CapabilityMatchFacts
)

func (c Capabilities) Allows(source Source) bool {
	var required Capabilities
	switch source {
	case SourceSeedAttributes:
		required = CapabilitySeedAttributes
	case SourceSeedFacts:
		required = CapabilitySeedFacts
	case SourceTickFacts:
		required = CapabilityTickFacts
	case SourceCandidateAttributes:
		required = CapabilityCandidateAttributes
	case SourceCandidateFacts:
		required = CapabilityCandidateFacts
	case SourceMatchFacts:
		required = CapabilityMatchFacts
	default:
		return false
	}
	return c&required != 0
}

// AttributeSpec describes an attribute name available on a seed or
// candidate. MaxValues is required for collection attributes and is zero for
// scalar int64 attributes.
type AttributeSpec struct {
	Name      string
	Type      fact.Type
	MaxValues int
}

// Lookup supplies only primitive values to a compiled expression. It does
// not expose tickets, Fact maps, Match members, or mutable state.
type Lookup interface {
	Strings(source Source, name string) ([]string, bool)
	Uint64s(source Source, name string) ([]uint64, bool)
	Int64(source Source, name string) (int64, bool)
}

// Path is a compiler/runtime error path.
type Path string

func (p Path) Child(name string) Path {
	if p == "" {
		return Path(name)
	}
	return Path(string(p) + "." + name)
}

func (p Path) Item(name string, index int) Path {
	if p == "" {
		return Path(fmt.Sprintf("%s[%d]", name, index))
	}
	return Path(fmt.Sprintf("%s.%s[%d]", p, name, index))
}

// Error is the common JSON, compile, and evaluate error shape.
type Error struct {
	Phase string
	Path  Path
	Code  string
	Err   error
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Path == "" {
		return fmt.Sprintf("expression %s [%s]: %v", e.Phase, e.Code, e.Err)
	}
	return fmt.Sprintf("expression %s at %s [%s]: %v", e.Phase, e.Path, e.Code, e.Err)
}

func (e *Error) Unwrap() error { return e.Err }

// Dependencies is the immutable set of Facts and attributes referenced by a
// compiled scalar program. Accessors return deterministic name-sorted copies.
type Dependencies struct {
	facts      map[string]fact.Spec
	attributes map[string]AttributeSpec
}

func (d Dependencies) Facts() []fact.Spec {
	if len(d.facts) == 0 {
		return nil
	}
	names := make([]string, 0, len(d.facts))
	for name := range d.facts {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]fact.Spec, 0, len(names))
	for _, name := range names {
		out = append(out, d.facts[name])
	}
	return out
}

func (d *Dependencies) addFact(spec fact.Spec) {
	if d.facts == nil {
		d.facts = make(map[string]fact.Spec)
	}
	d.facts[spec.Name] = spec
}

func (d Dependencies) Attributes() []AttributeSpec {
	if len(d.attributes) == 0 {
		return nil
	}
	names := make([]string, 0, len(d.attributes))
	for name := range d.attributes {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]AttributeSpec, 0, len(names))
	for _, name := range names {
		out = append(out, d.attributes[name])
	}
	return out
}

func (d *Dependencies) addAttribute(spec AttributeSpec) {
	if d.attributes == nil {
		d.attributes = make(map[string]AttributeSpec)
	}
	d.attributes[spec.Name] = spec
}

// Limits bounds the scalar expression graph after JSON structure has been
// validated.
type Limits struct {
	MaxDepth         int
	MaxChildren      int
	MaxLiteralValues int
	MaxSteps         int
	MaxNodes         int
	MaxInstructions  int
}

// CompileProfile closes the scalar language for one phase. The node grammar
// itself is closed and cannot be extended; the profile only selects allowed
// roots, sources, namespaces, Facts, and resource limits.
type CompileProfile struct {
	AllowedRoots   []ResultType
	AllowedSources Capabilities

	Attributes  []AttributeSpec
	Facts       []fact.Spec
	FactAllowed func(source Source, name string) bool

	Limits     Limits
	JSONLimits JSONLimits
}

// ProfileForRoots returns a profile with an explicit set of legal scalar
// roots. An empty profile remains useful for CompileScalarJSON: its envelope
// selects the root and the compiler still applies the closed scalar grammar.
func ProfileForRoots(roots ...ResultType) CompileProfile {
	profile := CompileProfile{}
	profile.AllowedRoots = append([]ResultType(nil), roots...)
	return profile
}

// StrictProfile is the concise form for a phase that accepts one root type.
func StrictProfile(result ResultType) CompileProfile {
	return ProfileForRoots(result)
}

func cloneProfile(profile CompileProfile) CompileProfile {
	profile.AllowedRoots = append([]ResultType(nil), profile.AllowedRoots...)
	profile.Attributes = append([]AttributeSpec(nil), profile.Attributes...)
	profile.Facts = append([]fact.Spec(nil), profile.Facts...)
	return profile
}

func allowedRoot(roots []ResultType, result ResultType) bool {
	if len(roots) == 0 {
		return true
	}
	for _, root := range roots {
		if root == result {
			return true
		}
	}
	return false
}

func validSource(source Source) bool {
	return source >= SourceSeedAttributes && source <= SourceMatchFacts
}

func factScopeAllowsSource(source Source, scope fact.Scope) bool {
	switch scope {
	case fact.ScopeTick:
		return source == SourceTickFacts
	case fact.ScopeObject:
		return source == SourceSeedFacts || source == SourceCandidateFacts
	case fact.ScopeMatch:
		return source == SourceMatchFacts
	default:
		return false
	}
}

func validFactSpec(spec fact.Spec) bool {
	if spec.Name == "" || !utf8.ValidString(spec.Name) {
		return false
	}
	switch spec.Scope {
	case fact.ScopeTick, fact.ScopeObject, fact.ScopeMatch:
		// These are the only lifecycle scopes represented by the scalar
		// expression contract. Validate declarations even when a fact is not
		// referenced so invalid profile data cannot remain latent.
	default:
		return false
	}
	switch spec.Type {
	case fact.TypeInt64:
		return spec.MaxValues == 0
	case fact.TypeStrings, fact.TypeUint64s:
		return spec.MaxValues > 0
	default:
		return false
	}
}

func validAttributeSpec(spec AttributeSpec) bool {
	if spec.Name == "" || !utf8.ValidString(spec.Name) {
		return false
	}
	switch spec.Type {
	case fact.TypeInt64:
		return spec.MaxValues == 0
	case fact.TypeStrings, fact.TypeUint64s:
		return spec.MaxValues > 0
	default:
		return false
	}
}
