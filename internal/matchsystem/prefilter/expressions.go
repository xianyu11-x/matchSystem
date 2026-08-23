package prefilter

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

type evalContext struct {
	seed  Document
	now   int64
	facts Facts
}

// StringExpr is a closed declarative string-list expression.
type StringExpr interface {
	stringExpr()
	bindStrings(evalContext) ([]string, error)
	validateStrings(*compileContext, string) error
	canonicalStrings() string
}

type literalStringsExpr struct{ values []string }
type seedStringsExpr struct{ field string }
type factStringsExpr struct{ fact string }
type unionStringsExpr struct{ items []StringExpr }

func LiteralStrings(values ...string) StringExpr {
	return &literalStringsExpr{values: append([]string(nil), values...)}
}
func SeedStrings(field string) StringExpr { return &seedStringsExpr{field: field} }
func FactStrings(name string) StringExpr  { return &factStringsExpr{fact: name} }
func UnionStrings(items ...StringExpr) StringExpr {
	return &unionStringsExpr{items: append([]StringExpr(nil), items...)}
}

func (*literalStringsExpr) stringExpr() {}
func (*seedStringsExpr) stringExpr()    {}
func (*factStringsExpr) stringExpr()    {}
func (*unionStringsExpr) stringExpr()   {}

func (e *literalStringsExpr) bindStrings(evalContext) ([]string, error) {
	return uniqueStrings(e.values), nil
}
func (e *seedStringsExpr) bindStrings(ctx evalContext) ([]string, error) {
	values, ok := ctx.seed.StringLists[e.field]
	if !ok {
		return nil, fmt.Errorf("seed string-list field %q is missing", e.field)
	}
	return uniqueStrings(values), nil
}
func (e *factStringsExpr) bindStrings(ctx evalContext) ([]string, error) {
	values, ok := ctx.facts.StringLists[e.fact]
	if !ok {
		return nil, fmt.Errorf("fact %q is missing", e.fact)
	}
	return uniqueStrings(values), nil
}
func (e *unionStringsExpr) bindStrings(ctx evalContext) ([]string, error) {
	all := make([]string, 0)
	for _, item := range e.items {
		values, err := item.bindStrings(ctx)
		if err != nil {
			return nil, err
		}
		all = append(all, values...)
	}
	return uniqueStrings(all), nil
}

func (e *literalStringsExpr) validateStrings(*compileContext, string) error { return nil }
func (e *seedStringsExpr) validateStrings(_ *compileContext, path string) error {
	if e.field == "" {
		return compileError(path, "EMPTY_FIELD", "seed string-list field name is empty")
	}
	return nil
}
func (e *factStringsExpr) validateStrings(ctx *compileContext, path string) error {
	def, ok := ctx.factsByName[e.fact]
	if !ok {
		return compileError(path, "MISSING_FACT", "fact %q is not registered", e.fact)
	}
	if def.Type != FactTypeStrings {
		return compileError(path, "FACT_TYPE_MISMATCH", "fact %q is not strings", e.fact)
	}
	ctx.requiredFacts[e.fact] = def
	return nil
}
func (e *unionStringsExpr) validateStrings(ctx *compileContext, path string) error {
	if len(e.items) == 0 {
		return compileError(path, "EMPTY_UNION", "string union must have at least one item")
	}
	for i, item := range e.items {
		if item == nil {
			return compileError(fmt.Sprintf("%s.items[%d]", path, i), "NIL_VALUE", "string value is nil")
		}
		if err := item.validateStrings(ctx, fmt.Sprintf("%s.items[%d]", path, i)); err != nil {
			return err
		}
	}
	return nil
}

func (e *literalStringsExpr) canonicalStrings() string {
	return "literal(" + strings.Join(uniqueStrings(e.values), "\x1f") + ")"
}
func (e *seedStringsExpr) canonicalStrings() string { return "seed-strings(" + e.field + ")" }
func (e *factStringsExpr) canonicalStrings() string { return "fact-strings(" + e.fact + ")" }
func (e *unionStringsExpr) canonicalStrings() string {
	parts := make([]string, len(e.items))
	for i, item := range e.items {
		parts[i] = item.canonicalStrings()
	}
	sort.Strings(parts)
	return "union(" + strings.Join(parts, ",") + ")"
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func declaredStringValueCount(value StringExpr, facts map[string]FactSpec) (int, bool) {
	switch expression := value.(type) {
	case *literalStringsExpr:
		return len(uniqueStrings(expression.values)), true
	case *factStringsExpr:
		definition, ok := facts[expression.fact]
		return definition.MaxValues, ok
	case *unionStringsExpr:
		total := 0
		for _, item := range expression.items {
			count, known := declaredStringValueCount(item, facts)
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
		// Seed fields have no separate schema in the current Go configuration.
		// Their concrete unique key count is checked during query binding.
		return 0, false
	}
}

// Int64Expr is a closed declarative int64 expression.
type Int64Expr interface {
	int64Expr()
	bindInt64(evalContext) (int64, error)
	validateInt64(*compileContext, string) error
	canonicalInt64() string
}

type literalInt64Expr struct{ value int64 }
type seedInt64Expr struct{ field string }
type seedWaitMillisExpr struct{}
type factInt64Expr struct{ fact string }
type stepInt64Expr struct {
	input          Int64Expr
	steps          []Int64Step
	waitThresholds bool
}
type clampInt64Expr struct {
	value Int64Expr
	min   Int64Expr
	max   Int64Expr
}
type binaryInt64Expr struct {
	op          byte
	left, right Int64Expr
}

// Int64Step maps an input threshold to a value. Steps must be ordered by At.
type Int64Step struct {
	At    int64
	Value int64
}

// WaitStep maps a seed wait-time threshold to a value.
type WaitStep struct {
	WaitMillis int64
	Value      int64
}

func LiteralInt64(value int64) Int64Expr { return &literalInt64Expr{value: value} }
func SeedInt64(field string) Int64Expr   { return &seedInt64Expr{field: field} }
func SeedWaitMillis() Int64Expr          { return &seedWaitMillisExpr{} }
func FactInt64(name string) Int64Expr    { return &factInt64Expr{fact: name} }
func StepInt64(input Int64Expr, steps ...Int64Step) Int64Expr {
	return &stepInt64Expr{input: input, steps: append([]Int64Step(nil), steps...)}
}
func WaitSteps(steps ...WaitStep) Int64Expr {
	converted := make([]Int64Step, len(steps))
	for i, step := range steps {
		converted[i] = Int64Step{At: step.WaitMillis, Value: step.Value}
	}
	return &stepInt64Expr{input: SeedWaitMillis(), steps: converted, waitThresholds: true}
}
func ClampInt64(value, min, max Int64Expr) Int64Expr {
	return &clampInt64Expr{value: value, min: min, max: max}
}
func AddInt64(left, right Int64Expr) Int64Expr {
	return &binaryInt64Expr{op: '+', left: left, right: right}
}
func SubInt64(left, right Int64Expr) Int64Expr {
	return &binaryInt64Expr{op: '-', left: left, right: right}
}

func (*literalInt64Expr) int64Expr()   {}
func (*seedInt64Expr) int64Expr()      {}
func (*seedWaitMillisExpr) int64Expr() {}
func (*factInt64Expr) int64Expr()      {}
func (*stepInt64Expr) int64Expr()      {}
func (*clampInt64Expr) int64Expr()     {}
func (*binaryInt64Expr) int64Expr()    {}

func (e *literalInt64Expr) bindInt64(evalContext) (int64, error) { return e.value, nil }
func (e *seedInt64Expr) bindInt64(ctx evalContext) (int64, error) {
	value, ok := ctx.seed.Int64Values[e.field]
	if !ok {
		return 0, fmt.Errorf("seed int64 field %q is missing", e.field)
	}
	return value, nil
}
func (*seedWaitMillisExpr) bindInt64(ctx evalContext) (int64, error) {
	if ctx.now <= ctx.seed.CreatedAt {
		return 0, nil
	}
	return saturatingSub(ctx.now, ctx.seed.CreatedAt), nil
}
func (e *factInt64Expr) bindInt64(ctx evalContext) (int64, error) {
	value, ok := ctx.facts.Int64Values[e.fact]
	if !ok {
		return 0, fmt.Errorf("fact %q is missing", e.fact)
	}
	return value, nil
}
func (e *stepInt64Expr) bindInt64(ctx evalContext) (int64, error) {
	input, err := e.input.bindInt64(ctx)
	if err != nil {
		return 0, err
	}
	position := sort.Search(len(e.steps), func(i int) bool { return e.steps[i].At > input })
	if position == 0 {
		return e.steps[0].Value, nil
	}
	return e.steps[position-1].Value, nil
}
func (e *clampInt64Expr) bindInt64(ctx evalContext) (int64, error) {
	value, err := e.value.bindInt64(ctx)
	if err != nil {
		return 0, err
	}
	min, err := e.min.bindInt64(ctx)
	if err != nil {
		return 0, err
	}
	max, err := e.max.bindInt64(ctx)
	if err != nil {
		return 0, err
	}
	if min > max {
		return 0, fmt.Errorf("clamp int64 minimum %d exceeds maximum %d", min, max)
	}
	if value < min {
		return min, nil
	}
	if value > max {
		return max, nil
	}
	return value, nil
}
func (e *binaryInt64Expr) bindInt64(ctx evalContext) (int64, error) {
	left, err := e.left.bindInt64(ctx)
	if err != nil {
		return 0, err
	}
	right, err := e.right.bindInt64(ctx)
	if err != nil {
		return 0, err
	}
	if e.op == '+' {
		return saturatingAdd(left, right), nil
	}
	return saturatingSub(left, right), nil
}

func (*literalInt64Expr) validateInt64(*compileContext, string) error { return nil }
func (e *seedInt64Expr) validateInt64(_ *compileContext, path string) error {
	if e.field == "" {
		return compileError(path, "EMPTY_FIELD", "seed int64 field name is empty")
	}
	return nil
}
func (*seedWaitMillisExpr) validateInt64(*compileContext, string) error { return nil }
func (e *factInt64Expr) validateInt64(ctx *compileContext, path string) error {
	def, ok := ctx.factsByName[e.fact]
	if !ok {
		return compileError(path, "MISSING_FACT", "fact %q is not registered", e.fact)
	}
	if def.Type != FactTypeInt64 {
		return compileError(path, "FACT_TYPE_MISMATCH", "fact %q is not int64", e.fact)
	}
	ctx.requiredFacts[e.fact] = def
	return nil
}
func (e *stepInt64Expr) validateInt64(ctx *compileContext, path string) error {
	if e.input == nil {
		return compileError(path+".input", "NIL_VALUE", "step input must not be nil")
	}
	if err := e.input.validateInt64(ctx, path+".input"); err != nil {
		return err
	}
	if len(e.steps) == 0 {
		if e.waitThresholds {
			return compileError(path+".steps", "EMPTY_WAIT_STEPS", "wait steps must not be empty")
		}
		return compileError(path+".steps", "EMPTY_STEPS", "int64 steps must not be empty")
	}
	var last int64
	for i, step := range e.steps {
		if e.waitThresholds && step.At < 0 {
			return compileError(path+".steps", "INVALID_WAIT_STEPS", "wait steps must be non-negative and strictly increasing")
		}
		if i > 0 && step.At <= last {
			if e.waitThresholds {
				return compileError(path+".steps", "INVALID_WAIT_STEPS", "wait steps must be non-negative and strictly increasing")
			}
			return compileError(path+".steps", "INVALID_STEPS", "int64 step thresholds must be strictly increasing")
		}
		last = step.At
	}
	return nil
}
func (e *clampInt64Expr) validateInt64(ctx *compileContext, path string) error {
	if e.value == nil || e.min == nil || e.max == nil {
		return compileError(path, "NIL_VALUE", "clamp value and bounds must not be nil")
	}
	if err := e.value.validateInt64(ctx, path+".value"); err != nil {
		return err
	}
	if err := e.min.validateInt64(ctx, path+".min"); err != nil {
		return err
	}
	if err := e.max.validateInt64(ctx, path+".max"); err != nil {
		return err
	}
	min, minIsLiteral := e.min.(*literalInt64Expr)
	max, maxIsLiteral := e.max.(*literalInt64Expr)
	if minIsLiteral && maxIsLiteral && min.value > max.value {
		return compileError(path, "INVALID_CLAMP_BOUNDS", "clamp minimum %d exceeds maximum %d", min.value, max.value)
	}
	return nil
}
func (e *binaryInt64Expr) validateInt64(ctx *compileContext, path string) error {
	if e.left == nil || e.right == nil {
		return compileError(path, "NIL_VALUE", "binary int64 operands must not be nil")
	}
	if err := e.left.validateInt64(ctx, path+".left"); err != nil {
		return err
	}
	return e.right.validateInt64(ctx, path+".right")
}

func (e *literalInt64Expr) canonicalInt64() string { return fmt.Sprintf("int(%d)", e.value) }
func (e *seedInt64Expr) canonicalInt64() string    { return "seed-int64(" + e.field + ")" }
func (*seedWaitMillisExpr) canonicalInt64() string { return "seed-wait-millis" }
func (e *factInt64Expr) canonicalInt64() string    { return "fact-int64(" + e.fact + ")" }
func (e *stepInt64Expr) canonicalInt64() string {
	parts := make([]string, len(e.steps))
	for i, step := range e.steps {
		parts[i] = fmt.Sprintf("%d:%d", step.At, step.Value)
	}
	if e.waitThresholds {
		return "wait-steps(" + strings.Join(parts, ",") + ")"
	}
	return "step(" + e.input.canonicalInt64() + ";" + strings.Join(parts, ",") + ")"
}
func (e *clampInt64Expr) canonicalInt64() string {
	return "clamp(" + e.value.canonicalInt64() + "," + e.min.canonicalInt64() + "," + e.max.canonicalInt64() + ")"
}
func (e *binaryInt64Expr) canonicalInt64() string {
	return fmt.Sprintf("%c(%s,%s)", e.op, e.left.canonicalInt64(), e.right.canonicalInt64())
}

func saturatingAdd(left, right int64) int64 {
	if right > 0 && left > math.MaxInt64-right {
		return math.MaxInt64
	}
	if right < 0 && left < math.MinInt64-right {
		return math.MinInt64
	}
	return left + right
}
func saturatingSub(left, right int64) int64 {
	if right > 0 && left < math.MinInt64+right {
		return math.MinInt64
	}
	if right < 0 && left > math.MaxInt64+right {
		return math.MaxInt64
	}
	return left - right
}

// Condition is a closed If condition.
type Condition interface {
	condition()
	evaluate(evalContext) (bool, error)
	validateCondition(*compileContext, string) error
	canonicalCondition() string
}

type compareInt64Condition struct {
	op          string
	left, right Int64Expr
}

func GreaterOrEqual(left, right Int64Expr) Condition {
	return &compareInt64Condition{op: ">=", left: left, right: right}
}

func (*compareInt64Condition) condition() {}
func (p *compareInt64Condition) evaluate(ctx evalContext) (bool, error) {
	left, err := p.left.bindInt64(ctx)
	if err != nil {
		return false, err
	}
	right, err := p.right.bindInt64(ctx)
	if err != nil {
		return false, err
	}
	return left >= right, nil
}
func (p *compareInt64Condition) validateCondition(ctx *compileContext, path string) error {
	if p.left == nil || p.right == nil {
		return compileError(path, "NIL_CONDITION_VALUE", "condition operands must not be nil")
	}
	if err := p.left.validateInt64(ctx, path+".left"); err != nil {
		return err
	}
	return p.right.validateInt64(ctx, path+".right")
}
func (p *compareInt64Condition) canonicalCondition() string {
	return p.op + "(" + p.left.canonicalInt64() + "," + p.right.canonicalInt64() + ")"
}
