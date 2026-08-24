package prefilter

import "sort"

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

// FactType is the declared value type of a Tick-level Fact.
type FactType uint8

const (
	FactTypeStrings FactType = iota + 1
	FactTypeInt64
	FactTypeUint64s
)

// FactSpec declares a Fact that expressions may read.
type FactSpec struct {
	Name      string
	Type      FactType
	MaxValues int
}

// Facts contains one immutable Fact layer. Tick Facts are copied into a
// TickSession; Seed Facts are borrowed for one synchronous Candidates call.
// Lists and Values use the same cardinality convention as Document.
type Facts struct {
	StringLists map[string][]string
	Uint64Lists map[string][]uint64
	Int64Values map[string]int64
}

func cloneFacts(in Facts) Facts {
	out := Facts{
		StringLists: make(map[string][]string, len(in.StringLists)),
		Uint64Lists: make(map[string][]uint64, len(in.Uint64Lists)),
		Int64Values: make(map[string]int64, len(in.Int64Values)),
	}
	for name, values := range in.StringLists {
		out.StringLists[name] = append([]string(nil), values...)
	}
	for name, values := range in.Uint64Lists {
		out.Uint64Lists[name] = append([]uint64(nil), values...)
	}
	for name, value := range in.Int64Values {
		out.Int64Values[name] = value
	}
	return out
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
