package prefilter

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// Uint64Expr is a closed declarative uint64-list expression.
type Uint64Expr interface {
	uint64Expr()
	bindUint64s(evalContext) ([]uint64, error)
	validateUint64s(*compileContext, string) error
	canonicalUint64s() string
}

type literalUint64sExpr struct{ values []uint64 }
type seedUint64sExpr struct{ field string }
type factUint64sExpr struct{ fact string }
type unionUint64sExpr struct{ items []Uint64Expr }

func LiteralUint64s(values ...uint64) Uint64Expr {
	return &literalUint64sExpr{values: append([]uint64(nil), values...)}
}
func SeedUint64s(field string) Uint64Expr { return &seedUint64sExpr{field: field} }
func FactUint64s(name string) Uint64Expr  { return &factUint64sExpr{fact: name} }
func UnionUint64s(items ...Uint64Expr) Uint64Expr {
	return &unionUint64sExpr{items: append([]Uint64Expr(nil), items...)}
}

func (*literalUint64sExpr) uint64Expr() {}
func (*seedUint64sExpr) uint64Expr()    {}
func (*factUint64sExpr) uint64Expr()    {}
func (*unionUint64sExpr) uint64Expr()   {}

func (e *literalUint64sExpr) bindUint64s(evalContext) ([]uint64, error) {
	return uniqueUint64s(e.values), nil
}
func (e *seedUint64sExpr) bindUint64s(ctx evalContext) ([]uint64, error) {
	values, ok := ctx.seed.Uint64Lists[e.field]
	if !ok {
		return nil, fmt.Errorf("seed uint64 field %q is missing", e.field)
	}
	return uniqueUint64s(values), nil
}
func (e *factUint64sExpr) bindUint64s(ctx evalContext) ([]uint64, error) {
	values, ok := ctx.facts.Uint64Lists[e.fact]
	if !ok {
		return nil, fmt.Errorf("fact %q is missing", e.fact)
	}
	return uniqueUint64s(values), nil
}
func (e *unionUint64sExpr) bindUint64s(ctx evalContext) ([]uint64, error) {
	all := make([]uint64, 0)
	for _, item := range e.items {
		values, err := item.bindUint64s(ctx)
		if err != nil {
			return nil, err
		}
		all = append(all, values...)
	}
	return uniqueUint64s(all), nil
}

func (*literalUint64sExpr) validateUint64s(*compileContext, string) error { return nil }
func (e *seedUint64sExpr) validateUint64s(_ *compileContext, path string) error {
	if e.field == "" {
		return compileError(path, "EMPTY_FIELD", "seed uint64 field name is empty")
	}
	return nil
}
func (e *factUint64sExpr) validateUint64s(ctx *compileContext, path string) error {
	def, ok := ctx.factsByName[e.fact]
	if !ok {
		return compileError(path, "MISSING_FACT", "fact %q is not registered", e.fact)
	}
	if def.Type != FactTypeUint64s {
		return compileError(path, "FACT_TYPE_MISMATCH", "fact %q is not uint64 values", e.fact)
	}
	ctx.requiredFacts[e.fact] = def
	return nil
}
func (e *unionUint64sExpr) validateUint64s(ctx *compileContext, path string) error {
	if len(e.items) == 0 {
		return compileError(path, "EMPTY_UNION", "uint64 union must have at least one item")
	}
	for i, item := range e.items {
		itemPath := fmt.Sprintf("%s.items[%d]", path, i)
		if item == nil || isNilInterface(item) {
			return compileError(itemPath, "NIL_VALUE", "uint64 value is nil")
		}
		if err := item.validateUint64s(ctx, itemPath); err != nil {
			return err
		}
	}
	return nil
}

func (e *literalUint64sExpr) canonicalUint64s() string {
	return "uint64-literal(" + joinUint64s(uniqueUint64s(e.values)) + ")"
}
func (e *seedUint64sExpr) canonicalUint64s() string { return "seed-uint64s(" + e.field + ")" }
func (e *factUint64sExpr) canonicalUint64s() string { return "fact-uint64s(" + e.fact + ")" }
func (e *unionUint64sExpr) canonicalUint64s() string {
	parts := make([]string, len(e.items))
	for i, item := range e.items {
		parts[i] = item.canonicalUint64s()
	}
	sort.Strings(parts)
	return "uint64-union(" + strings.Join(parts, ",") + ")"
}

func uniqueUint64s(values []uint64) []uint64 {
	if len(values) == 0 {
		return nil
	}
	out := append([]uint64(nil), values...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	write := 1
	for read := 1; read < len(out); read++ {
		if out[read] != out[write-1] {
			out[write] = out[read]
			write++
		}
	}
	return out[:write]
}

func joinUint64s(values []uint64) string {
	parts := make([]string, len(values))
	for i, value := range values {
		parts[i] = strconv.FormatUint(value, 10)
	}
	return strings.Join(parts, ",")
}

func declaredUint64ValueCount(value Uint64Expr, facts map[string]FactSpec) (int, bool) {
	switch expression := value.(type) {
	case *literalUint64sExpr:
		return len(uniqueUint64s(expression.values)), true
	case *factUint64sExpr:
		definition, ok := facts[expression.fact]
		return definition.MaxValues, ok
	case *unionUint64sExpr:
		total := 0
		for _, item := range expression.items {
			count, known := declaredUint64ValueCount(item, facts)
			if !known {
				return 0, false
			}
			if count > math.MaxInt-total {
				return math.MaxInt, true
			}
			total += count
		}
		return total, true
	default:
		return 0, false
	}
}
