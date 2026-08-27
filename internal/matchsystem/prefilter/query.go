package prefilter

import (
	"errors"
	"strings"

	"matchSystem/internal/matchsystem/expression"
)

func adaptExpressionEvaluateError(err error, path, code string) error {
	if err == nil {
		return nil
	}
	var expressionErr *expression.Error
	if errors.As(err, &expressionErr) {
		if expressionErr.Path != "" {
			expressionPath := strings.TrimPrefix(string(expressionErr.Path), "root")
			expressionPath = strings.TrimPrefix(expressionPath, ".")
			if expressionPath != "" {
				path = strings.TrimSuffix(path, ".") + "." + expressionPath
			}
		}
		return evaluationError(path, code+"_"+expressionErr.Code, "%v", expressionErr.Err)
	}
	return evaluationError(path, code, "%v", err)
}

// bind resolves one Prefilter-owned lookup sidecar. Scalar operands are
// opaque ScalarPrograms; no expression IR handle crosses the runtime
// boundary.
func (q *bitmapQuery) bind(ctx evalContext, path string) (boundIndexQuery, error) {
	if q == nil {
		return boundIndexQuery{}, evaluationError(path, "INVALID_QUERY", "compiled query sidecar is nil")
	}
	switch q.kind {
	case bitmapLookupString:
		keys := q.staticStrings
		if q.values != nil {
			var err error
			keys, err = q.values.EvaluateStrings(ctx.expressionLookup())
			if err != nil {
				return boundIndexQuery{}, adaptExpressionEvaluateError(err, path+".values", "QUERY_BIND")
			}
		}
		if len(keys) > q.maxQueryValues {
			return boundIndexQuery{}, evaluationError(path+".values", "QUERY_KEY_LIMIT", "query produced %d keys; maximum is %d", len(keys), q.maxQueryValues)
		}
		return boundIndexQuery{kind: boundQueryString, strings: keys}, nil
	case bitmapLookupUint64:
		keys := q.staticUint64s
		if q.values != nil {
			var err error
			keys, err = q.values.EvaluateUint64s(ctx.expressionLookup())
			if err != nil {
				return boundIndexQuery{}, adaptExpressionEvaluateError(err, path+".values", "QUERY_BIND")
			}
		}
		if len(keys) > q.maxQueryValues {
			return boundIndexQuery{}, evaluationError(path+".values", "QUERY_KEY_LIMIT", "query produced %d keys; maximum is %d", len(keys), q.maxQueryValues)
		}
		return boundIndexQuery{kind: boundQueryUint64, uint64s: keys}, nil
	case bitmapLookupRange:
		minimum, maximum := q.staticMin, q.staticMax
		if q.min != nil {
			var err error
			minimum, err = q.min.EvaluateInt64(ctx.expressionLookup())
			if err != nil {
				return boundIndexQuery{}, adaptExpressionEvaluateError(err, path+".min", "QUERY_BIND")
			}
		}
		if q.max != nil {
			var err error
			maximum, err = q.max.EvaluateInt64(ctx.expressionLookup())
			if err != nil {
				return boundIndexQuery{}, adaptExpressionEvaluateError(err, path+".max", "QUERY_BIND")
			}
		}
		if minimum > maximum {
			return boundIndexQuery{}, evaluationError(path, "INVALID_RANGE", "minimum %d exceeds maximum %d", minimum, maximum)
		}
		return boundIndexQuery{kind: boundQueryRange, min: minimum, max: maximum}, nil
	default:
		return boundIndexQuery{}, evaluationError(path, "INVALID_QUERY", "compiled query kind is invalid")
	}
}

// boundIndexQuery is a concrete tagged query. Its zero kind means "not
// bound"; passing the value directly through runtimeIndex avoids interface
// boxing/type assertions on every estimate, lookup or contains call.
type boundQueryKind uint8

const (
	boundQueryInvalid boundQueryKind = iota
	boundQueryString
	boundQueryUint64
	boundQueryRange
)

type boundIndexQuery struct {
	kind    boundQueryKind
	strings []string
	uint64s []uint64
	min     int64
	max     int64
}
