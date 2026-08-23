package prefilter

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

// Facts contains immutable values for one Tick. Its Lists and Values suffixes
// use the same cardinality convention as Document.
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
