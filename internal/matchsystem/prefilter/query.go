package prefilter

import "fmt"

// IndexQuery is the closed declarative index query interface.
type IndexQuery interface{ indexQuery() }

// StringQuery queries a multi-value index configured with string keys.
type StringQuery struct {
	Index  string
	Values StringExpr
}

func (StringQuery) indexQuery() {}

// Uint64Query queries a multi-value index configured with uint64 keys.
type Uint64Query struct {
	Index  string
	Values Uint64Expr
}

func (Uint64Query) indexQuery() {}

type Int64RangeQuery struct {
	Index string
	Min   Int64Expr
	Max   Int64Expr
}

func (Int64RangeQuery) indexQuery() {}

type boundIndexQuery interface{ boundIndexQuery() }
type boundStringQuery struct{ keys []string }
type boundUint64Query struct{ keys []uint64 }
type boundInt64RangeQuery struct{ min, max int64 }

func (boundStringQuery) boundIndexQuery()     {}
func (boundUint64Query) boundIndexQuery()     {}
func (boundInt64RangeQuery) boundIndexQuery() {}

type compiledIndexQuery interface {
	indexSlot() int
	bind(evalContext, string) (boundIndexQuery, error)
	canonical() string
}

type compiledStringQuery struct {
	slot    int
	index   string
	maxKeys int
	values  StringExpr
}

func (q *compiledStringQuery) indexSlot() int { return q.slot }
func (q *compiledStringQuery) bind(ctx evalContext, path string) (boundIndexQuery, error) {
	keys, err := q.values.bindStrings(ctx)
	if err != nil {
		return nil, evaluationError(path+".values", "QUERY_BIND", "%v", err)
	}
	if len(keys) > q.maxKeys {
		return nil, evaluationError(path+".values", "QUERY_KEY_LIMIT", "query produced %d keys; maximum is %d", len(keys), q.maxKeys)
	}
	return boundStringQuery{keys: keys}, nil
}
func (q *compiledStringQuery) canonical() string {
	return fmt.Sprintf("multi-value-string(%s,in,%s)", q.index, q.values.canonicalStrings())
}

type compiledUint64Query struct {
	slot    int
	index   string
	maxKeys int
	values  Uint64Expr
}

func (q *compiledUint64Query) indexSlot() int { return q.slot }
func (q *compiledUint64Query) bind(ctx evalContext, path string) (boundIndexQuery, error) {
	keys, err := q.values.bindUint64s(ctx)
	if err != nil {
		return nil, evaluationError(path+".values", "QUERY_BIND", "%v", err)
	}
	if len(keys) > q.maxKeys {
		return nil, evaluationError(path+".values", "QUERY_KEY_LIMIT", "query produced %d keys; maximum is %d", len(keys), q.maxKeys)
	}
	return boundUint64Query{keys: keys}, nil
}
func (q *compiledUint64Query) canonical() string {
	return fmt.Sprintf("multi-value-uint64(%s,in,%s)", q.index, q.values.canonicalUint64s())
}

type compiledInt64RangeQuery struct {
	slot     int
	index    string
	min, max Int64Expr
}

func (q *compiledInt64RangeQuery) indexSlot() int { return q.slot }
func (q *compiledInt64RangeQuery) bind(ctx evalContext, path string) (boundIndexQuery, error) {
	min, err := q.min.bindInt64(ctx)
	if err != nil {
		return nil, evaluationError(path+".min", "QUERY_BIND", "%v", err)
	}
	max, err := q.max.bindInt64(ctx)
	if err != nil {
		return nil, evaluationError(path+".max", "QUERY_BIND", "%v", err)
	}
	if min > max {
		return nil, evaluationError(path, "INVALID_RANGE", "minimum %d exceeds maximum %d", min, max)
	}
	return boundInt64RangeQuery{min: min, max: max}, nil
}
func (q *compiledInt64RangeQuery) canonical() string {
	return fmt.Sprintf("int64-range(%s,%s,%s)", q.index, q.min.canonicalInt64(), q.max.canonicalInt64())
}
