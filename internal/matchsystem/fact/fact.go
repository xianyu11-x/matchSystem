// Package fact owns the source-independent value model, contracts, providers,
// validation, and per-attempt lifecycle shared by the matching pipeline.
package fact

// Type is the declared value type of a Fact, independent of whether its value
// is supplied at Tick or object scope.
type Type uint8

const (
	TypeStrings Type = iota + 1
	TypeInt64
	TypeUint64s
)

// Scope identifies the lifecycle layer in which a Fact is supplied. The zero
// value is invalid; every Fact declaration must choose exactly one scope.
type Scope string

const (
	ScopeTick   Scope = "tick"
	ScopeObject Scope = "object"
	ScopeMatch  Scope = "match"
)

// Spec declares one Fact available to matching stages.
type Spec struct {
	Name        string
	Type        Type
	MaxValues   int
	Scope       Scope
	Description string
}

// Values contains one immutable Fact layer. Callers must treat all maps and
// slices as read-only for the lifetime documented by the receiving API.
type Values struct {
	StringLists map[string][]string
	Uint64Lists map[string][]uint64
	Int64Values map[string]int64
}

// Clone creates an owned deep copy of one Fact layer.
func Clone(in Values) Values {
	out := Values{
		StringLists: make(map[string][]string, len(in.StringLists)),
		Uint64Lists: make(map[string][]uint64, len(in.Uint64Lists)),
		Int64Values: make(map[string]int64, len(in.Int64Values)),
	}
	for name, values := range in.StringLists {
		if values == nil {
			out.StringLists[name] = nil
			continue
		}
		out.StringLists[name] = append(make([]string, 0, len(values)), values...)
	}
	for name, values := range in.Uint64Lists {
		if values == nil {
			out.Uint64Lists[name] = nil
			continue
		}
		out.Uint64Lists[name] = append(make([]uint64, 0, len(values)), values...)
	}
	for name, value := range in.Int64Values {
		out.Int64Values[name] = value
	}
	return out
}
