package prefilter

import (
	"sort"

	"matchSystem/internal/matchsystem/fact"
)

// Document is the immutable projection indexed by the prefilter. StringLists
// and Uint64Lists hold multiple values per field; Int64Values holds one scalar
// per field.
type Document struct {
	DocID       uint32
	CreatedAt   int64
	StringLists map[string][]string
	Uint64Lists map[string][]uint64
	Int64Values map[string]int64
}

type FactType = fact.Type

const (
	FactTypeStrings = fact.TypeStrings
	FactTypeInt64   = fact.TypeInt64
	FactTypeUint64s = fact.TypeUint64s
)

type FactSpec = fact.Spec

// Facts contains one immutable Fact layer. Tick Facts are copied into a
// TickSession; Seed Facts are borrowed for one synchronous Candidates call.
// Lists and Values use the same cardinality convention as Document.
type Facts = fact.Values

func cloneFacts(in Facts) Facts {
	return fact.Clone(in)
}

func validateFactTypes(path string, facts Facts) (map[string]struct{}, error) {
	names := make(map[string]struct{}, len(facts.StringLists)+len(facts.Uint64Lists)+len(facts.Int64Values))
	for _, group := range []struct {
		kind string
		keys []string
	}{
		{kind: "strings", keys: sortedStringKeys(facts.StringLists)},
		{kind: "uint64s", keys: sortedStringKeys(facts.Uint64Lists)},
		{kind: "int64", keys: sortedStringKeys(facts.Int64Values)},
	} {
		for _, name := range group.keys {
			if _, exists := names[name]; exists {
				return nil, evaluationError(path+"."+name, "FACT_TYPE_COLLISION", "fact %q is present in multiple type maps", name)
			}
			names[name] = struct{}{}
		}
	}
	return names, nil
}

func validateFactScopes(tickNames, seedNames map[string]struct{}) error {
	names := make([]string, 0, len(seedNames))
	for name := range seedNames {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if _, exists := tickNames[name]; exists {
			return evaluationError("facts.seed."+name, "FACT_SCOPE_COLLISION", "fact %q is present in both Tick and Seed Facts", name)
		}
	}
	return nil
}

func sortedStringKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
