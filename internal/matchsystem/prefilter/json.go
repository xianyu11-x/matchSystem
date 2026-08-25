package prefilter

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"
)

const JSONSchemaVersion = "prefilter/v1"

// JSONLimits bounds the amount of work accepted from one Prefilter JSON plan.
// Zero values use the defaults returned by DefaultJSONLimits.
type JSONLimits struct {
	MaxBytes         int
	MaxDepth         int
	MaxValues        int
	MaxChildren      int
	MaxLiteralValues int
	MaxSteps         int
	MaxStringBytes   int
	MaxIndexes       int
	MaxFacts         int
}

func DefaultJSONLimits() JSONLimits {
	return JSONLimits{
		MaxBytes:         1 << 20,
		MaxDepth:         64,
		MaxValues:        10000,
		MaxChildren:      128,
		MaxLiteralValues: 256,
		MaxSteps:         128,
		MaxStringBytes:   1024,
		MaxIndexes:       128,
		MaxFacts:         128,
	}
}

// LogicalNodeContract is the immutable Index/Fact design boundary shared by
// Prefilter and the later matching evaluation stages.
// Indexes and Facts are supplied directly or parsed from a separate contract
// document before plans are authored; plan JSON may only reference them.
type LogicalNodeContract struct {
	Indexes []IndexSpec
	Facts   []FactSpec
	Limits  JSONLimits
}

type JSONContract = LogicalNodeContract

// JSONCompiler parses JSON plans against one fixed index and Fact contract.
// It is immutable after construction and safe for concurrent Parse/Compile calls.
type JSONCompiler struct {
	indexes       []IndexSpec
	facts         []FactSpec
	indexesByName map[string]indexSpec
	factsByName   map[string]FactSpec
	limits        JSONLimits
}

// NewJSONCompiler validates and snapshots the available indexes and Facts.
func NewJSONCompiler(contract LogicalNodeContract) (*JSONCompiler, error) {
	indexes := append([]IndexSpec(nil), contract.Indexes...)
	facts := append([]FactSpec(nil), contract.Facts...)
	if _, err := Compile(Config{Indexes: indexes, Facts: facts, Root: None()}); err != nil {
		return nil, err
	}
	if err := validateJSONLimits(contract.Limits); err != nil {
		return nil, err
	}

	compiler := &JSONCompiler{
		indexes:       indexes,
		facts:         facts,
		indexesByName: make(map[string]indexSpec, len(indexes)),
		factsByName:   make(map[string]FactSpec, len(facts)),
		limits:        normalizeJSONLimits(contract.Limits),
	}
	for _, definition := range indexes {
		spec := definition.indexSpec()
		compiler.indexesByName[spec.name] = spec
	}
	for _, fact := range facts {
		compiler.factsByName[fact.Name] = fact
	}
	return compiler, nil
}

// Parse strictly decodes a JSON plan and produces the ordinary typed Config.
// The returned Config always contains the contract's indexes and Facts.
func (c *JSONCompiler) Parse(data []byte) (Config, error) {
	if c == nil {
		return Config{}, jsonError("$", "NIL_JSON_COMPILER", "JSON compiler is nil")
	}
	if err := validateJSONInput(data, c.limits); err != nil {
		return Config{}, err
	}

	var envelope struct {
		SchemaVersion string          `json:"schemaVersion"`
		Plan          json.RawMessage `json:"plan"`
		Runtime       struct {
			ContainsProbeThreshold uint64 `json:"containsProbeThreshold,omitempty"`
		} `json:"runtime,omitempty"`
	}
	if err := decodeStrict(data, &envelope); err != nil {
		return Config{}, structureError("$", err)
	}
	if envelope.SchemaVersion == "" {
		return Config{}, jsonError("$.schemaVersion", "MISSING_FIELD", "schemaVersion is required")
	}
	if envelope.SchemaVersion != JSONSchemaVersion {
		return Config{}, jsonError("$.schemaVersion", "UNKNOWN_SCHEMA_VERSION", "unsupported schemaVersion %q", envelope.SchemaVersion)
	}
	if len(envelope.Plan) == 0 {
		return Config{}, jsonError("$.plan", "MISSING_FIELD", "plan is required")
	}
	root, err := c.parseExpr(envelope.Plan, "$.plan", 1)
	if err != nil {
		return Config{}, err
	}
	return Config{
		Indexes:                append([]IndexSpec(nil), c.indexes...),
		Facts:                  append([]FactSpec(nil), c.facts...),
		Root:                   root,
		ContainsProbeThreshold: envelope.Runtime.ContainsProbeThreshold,
	}, nil
}

// Compile parses JSON and reuses the regular typed Prefilter compiler.
func (c *JSONCompiler) Compile(data []byte) (*Plan, error) {
	config, err := c.Parse(data)
	if err != nil {
		return nil, err
	}
	return Compile(config)
}

func (c *JSONCompiler) parseExpr(raw json.RawMessage, path string, depth int) (Expr, error) {
	if err := c.checkDepth(path, depth); err != nil {
		return nil, err
	}
	typeName, err := decodeType(raw, path)
	if err != nil {
		return nil, err
	}
	switch typeName {
	case "lookup":
		var dto struct {
			Type  string          `json:"type"`
			Query json.RawMessage `json:"query"`
		}
		if err := decodeAt(raw, path, &dto); err != nil {
			return nil, err
		}
		if len(dto.Query) == 0 {
			return nil, jsonError(path+".query", "MISSING_FIELD", "lookup query is required")
		}
		query, err := c.parseQuery(dto.Query, path+".query", depth+1)
		if err != nil {
			return nil, err
		}
		return Lookup(query), nil
	case "and", "or":
		var dto struct {
			Type     string            `json:"type"`
			Children []json.RawMessage `json:"children"`
		}
		if err := decodeAt(raw, path, &dto); err != nil {
			return nil, err
		}
		if dto.Children == nil {
			return nil, jsonError(path+".children", "MISSING_FIELD", "%s children are required", typeName)
		}
		if len(dto.Children) == 0 {
			return nil, jsonError(path+".children", "EMPTY_CHILDREN", "%s requires at least one child", typeName)
		}
		if len(dto.Children) > c.limits.MaxChildren {
			return nil, jsonError(path+".children", "CHILD_LIMIT", "%s contains %d children; maximum is %d", typeName, len(dto.Children), c.limits.MaxChildren)
		}
		children := make([]Expr, len(dto.Children))
		for i, child := range dto.Children {
			childPath := fmt.Sprintf("%s.children[%d]", path, i)
			children[i], err = c.parseExpr(child, childPath, depth+1)
			if err != nil {
				return nil, err
			}
		}
		if typeName == "and" {
			return And(children...), nil
		}
		return Or(children...), nil
	case "exclude":
		var dto struct {
			Type  string          `json:"type"`
			Child json.RawMessage `json:"child"`
		}
		if err := decodeAt(raw, path, &dto); err != nil {
			return nil, err
		}
		if len(dto.Child) == 0 {
			return nil, jsonError(path+".child", "MISSING_FIELD", "exclude child is required")
		}
		child, err := c.parseExpr(dto.Child, path+".child", depth+1)
		if err != nil {
			return nil, err
		}
		return Exclude(child), nil
	case "if":
		var dto struct {
			Type string          `json:"type"`
			When json.RawMessage `json:"when"`
			Then json.RawMessage `json:"then"`
			Else json.RawMessage `json:"else"`
		}
		if err := decodeAt(raw, path, &dto); err != nil {
			return nil, err
		}
		if len(dto.When) == 0 || len(dto.Then) == 0 || len(dto.Else) == 0 {
			return nil, jsonError(path, "MISSING_FIELD", "if requires when, then and else")
		}
		condition, err := c.parseCondition(dto.When, path+".when", depth+1)
		if err != nil {
			return nil, err
		}
		thenExpr, err := c.parseExpr(dto.Then, path+".then", depth+1)
		if err != nil {
			return nil, err
		}
		elseExpr, err := c.parseExpr(dto.Else, path+".else", depth+1)
		if err != nil {
			return nil, err
		}
		return If(condition, thenExpr, elseExpr), nil
	case "none":
		var dto struct {
			Type string `json:"type"`
		}
		if err := decodeAt(raw, path, &dto); err != nil {
			return nil, err
		}
		return None(), nil
	default:
		return nil, jsonError(path+".type", "UNKNOWN_TYPE", "unknown Prefilter expression type %q", typeName)
	}
}

func (c *JSONCompiler) parseQuery(raw json.RawMessage, path string, depth int) (IndexQuery, error) {
	if err := c.checkDepth(path, depth); err != nil {
		return nil, err
	}
	typeName, err := decodeType(raw, path)
	if err != nil {
		return nil, err
	}
	switch typeName {
	case "multi_value":
		var dto struct {
			Type   string          `json:"type"`
			Index  string          `json:"index"`
			Values json.RawMessage `json:"values"`
		}
		if err := decodeAt(raw, path, &dto); err != nil {
			return nil, err
		}
		spec, err := c.resolveJSONIndex(dto.Index, IndexTypeMultiValue, path+".index")
		if err != nil {
			return nil, err
		}
		if len(dto.Values) == 0 {
			return nil, jsonError(path+".values", "MISSING_FIELD", "multi_value values are required")
		}
		if spec.keyType == KeyTypeString {
			values, err := c.parseStringExpr(dto.Values, path+".values", depth+1)
			if err != nil {
				return nil, err
			}
			return StringQuery{Index: dto.Index, Values: values}, nil
		}
		values, err := c.parseUint64Expr(dto.Values, path+".values", depth+1)
		if err != nil {
			return nil, err
		}
		return Uint64Query{Index: dto.Index, Values: values}, nil
	case "int64_range":
		var dto struct {
			Type       string          `json:"type"`
			Index      string          `json:"index"`
			Min        json.RawMessage `json:"min"`
			Max        json.RawMessage `json:"max"`
			IncludeMin *bool           `json:"includeMin,omitempty"`
			IncludeMax *bool           `json:"includeMax,omitempty"`
		}
		if err := decodeAt(raw, path, &dto); err != nil {
			return nil, err
		}
		if _, err := c.resolveJSONIndex(dto.Index, IndexTypeInt64Range, path+".index"); err != nil {
			return nil, err
		}
		if dto.IncludeMin != nil && !*dto.IncludeMin || dto.IncludeMax != nil && !*dto.IncludeMax {
			return nil, jsonError(path, "UNSUPPORTED_RANGE_BOUND", "int64_range currently requires inclusive bounds")
		}
		if len(dto.Min) == 0 || len(dto.Max) == 0 {
			return nil, jsonError(path, "MISSING_FIELD", "int64_range requires min and max")
		}
		min, err := c.parseInt64Expr(dto.Min, path+".min", depth+1)
		if err != nil {
			return nil, err
		}
		max, err := c.parseInt64Expr(dto.Max, path+".max", depth+1)
		if err != nil {
			return nil, err
		}
		return Int64RangeQuery{Index: dto.Index, Min: min, Max: max}, nil
	default:
		return nil, jsonError(path+".type", "UNKNOWN_TYPE", "unknown query type %q", typeName)
	}
}

func (c *JSONCompiler) parseStringExpr(raw json.RawMessage, path string, depth int) (StringExpr, error) {
	if err := c.checkDepth(path, depth); err != nil {
		return nil, err
	}
	typeName, err := decodeType(raw, path)
	if err != nil {
		return nil, err
	}
	switch typeName {
	case "literal_strings":
		var dto struct {
			Type   string   `json:"type"`
			Values []string `json:"values"`
		}
		if err := decodeAt(raw, path, &dto); err != nil {
			return nil, err
		}
		if dto.Values == nil {
			return nil, jsonError(path+".values", "MISSING_FIELD", "literal string values are required")
		}
		if err := c.checkLiteralCount(path+".values", len(dto.Values)); err != nil {
			return nil, err
		}
		for i, value := range dto.Values {
			if err := c.checkString(fmt.Sprintf("%s.values[%d]", path, i), value); err != nil {
				return nil, err
			}
		}
		return LiteralStrings(dto.Values...), nil
	case "seed_strings":
		var dto struct {
			Type  string `json:"type"`
			Field string `json:"field"`
		}
		if err := decodeAt(raw, path, &dto); err != nil {
			return nil, err
		}
		if err := c.requireName(path+".field", dto.Field); err != nil {
			return nil, err
		}
		return SeedStrings(dto.Field), nil
	case "fact_strings":
		var dto struct {
			Type string `json:"type"`
			Fact string `json:"fact"`
		}
		if err := decodeAt(raw, path, &dto); err != nil {
			return nil, err
		}
		if err := c.resolveJSONFact(dto.Fact, FactTypeStrings, path+".fact"); err != nil {
			return nil, err
		}
		return FactStrings(dto.Fact), nil
	case "union_strings":
		var dto struct {
			Type  string            `json:"type"`
			Items []json.RawMessage `json:"items"`
		}
		if err := decodeAt(raw, path, &dto); err != nil {
			return nil, err
		}
		if dto.Items == nil || len(dto.Items) == 0 {
			return nil, jsonError(path+".items", "EMPTY_VALUES", "union_strings requires at least one item")
		}
		if err := c.checkLiteralCount(path+".items", len(dto.Items)); err != nil {
			return nil, err
		}
		items := make([]StringExpr, len(dto.Items))
		for i, item := range dto.Items {
			items[i], err = c.parseStringExpr(item, fmt.Sprintf("%s.items[%d]", path, i), depth+1)
			if err != nil {
				return nil, err
			}
		}
		return UnionStrings(items...), nil
	default:
		return nil, jsonError(path+".type", "EXPRESSION_TYPE_MISMATCH", "expression type %q does not produce strings", typeName)
	}
}

func (c *JSONCompiler) parseUint64Expr(raw json.RawMessage, path string, depth int) (Uint64Expr, error) {
	if err := c.checkDepth(path, depth); err != nil {
		return nil, err
	}
	typeName, err := decodeType(raw, path)
	if err != nil {
		return nil, err
	}
	switch typeName {
	case "literal_uint64s":
		var dto struct {
			Type   string   `json:"type"`
			Values []uint64 `json:"values"`
		}
		if err := decodeAt(raw, path, &dto); err != nil {
			return nil, err
		}
		if dto.Values == nil {
			return nil, jsonError(path+".values", "MISSING_FIELD", "literal uint64 values are required")
		}
		if err := c.checkLiteralCount(path+".values", len(dto.Values)); err != nil {
			return nil, err
		}
		return LiteralUint64s(dto.Values...), nil
	case "seed_uint64s":
		var dto struct {
			Type  string `json:"type"`
			Field string `json:"field"`
		}
		if err := decodeAt(raw, path, &dto); err != nil {
			return nil, err
		}
		if err := c.requireName(path+".field", dto.Field); err != nil {
			return nil, err
		}
		return SeedUint64s(dto.Field), nil
	case "fact_uint64s":
		var dto struct {
			Type string `json:"type"`
			Fact string `json:"fact"`
		}
		if err := decodeAt(raw, path, &dto); err != nil {
			return nil, err
		}
		if err := c.resolveJSONFact(dto.Fact, FactTypeUint64s, path+".fact"); err != nil {
			return nil, err
		}
		return FactUint64s(dto.Fact), nil
	case "union_uint64s":
		var dto struct {
			Type  string            `json:"type"`
			Items []json.RawMessage `json:"items"`
		}
		if err := decodeAt(raw, path, &dto); err != nil {
			return nil, err
		}
		if dto.Items == nil || len(dto.Items) == 0 {
			return nil, jsonError(path+".items", "EMPTY_VALUES", "union_uint64s requires at least one item")
		}
		if err := c.checkLiteralCount(path+".items", len(dto.Items)); err != nil {
			return nil, err
		}
		items := make([]Uint64Expr, len(dto.Items))
		for i, item := range dto.Items {
			items[i], err = c.parseUint64Expr(item, fmt.Sprintf("%s.items[%d]", path, i), depth+1)
			if err != nil {
				return nil, err
			}
		}
		return UnionUint64s(items...), nil
	default:
		return nil, jsonError(path+".type", "EXPRESSION_TYPE_MISMATCH", "expression type %q does not produce uint64 values", typeName)
	}
}

func (c *JSONCompiler) parseInt64Expr(raw json.RawMessage, path string, depth int) (Int64Expr, error) {
	if err := c.checkDepth(path, depth); err != nil {
		return nil, err
	}
	typeName, err := decodeType(raw, path)
	if err != nil {
		return nil, err
	}
	switch typeName {
	case "literal_int64":
		var dto struct {
			Type  string `json:"type"`
			Value *int64 `json:"value"`
		}
		if err := decodeAt(raw, path, &dto); err != nil {
			return nil, err
		}
		if dto.Value == nil {
			return nil, jsonError(path+".value", "MISSING_FIELD", "literal int64 value is required")
		}
		return LiteralInt64(*dto.Value), nil
	case "seed_int64":
		var dto struct {
			Type  string `json:"type"`
			Field string `json:"field"`
		}
		if err := decodeAt(raw, path, &dto); err != nil {
			return nil, err
		}
		if err := c.requireName(path+".field", dto.Field); err != nil {
			return nil, err
		}
		return SeedInt64(dto.Field), nil
	case "fact_int64":
		var dto struct {
			Type string `json:"type"`
			Fact string `json:"fact"`
		}
		if err := decodeAt(raw, path, &dto); err != nil {
			return nil, err
		}
		if err := c.resolveJSONFact(dto.Fact, FactTypeInt64, path+".fact"); err != nil {
			return nil, err
		}
		return FactInt64(dto.Fact), nil
	case "step_int64":
		var dto struct {
			Type  string          `json:"type"`
			Input json.RawMessage `json:"input"`
			Steps []struct {
				At    *int64 `json:"at"`
				Value *int64 `json:"value"`
			} `json:"steps"`
		}
		if err := decodeAt(raw, path, &dto); err != nil {
			return nil, err
		}
		if len(dto.Input) == 0 {
			return nil, jsonError(path+".input", "MISSING_FIELD", "step_int64 input is required")
		}
		if len(dto.Steps) == 0 {
			return nil, jsonError(path+".steps", "EMPTY_STEPS", "step_int64 requires at least one step")
		}
		if len(dto.Steps) > c.limits.MaxSteps {
			return nil, jsonError(path+".steps", "STEP_LIMIT", "step_int64 contains %d steps; maximum is %d", len(dto.Steps), c.limits.MaxSteps)
		}
		input, err := c.parseInt64Expr(dto.Input, path+".input", depth+1)
		if err != nil {
			return nil, err
		}
		steps := make([]Int64Step, len(dto.Steps))
		for i, step := range dto.Steps {
			if step.At == nil || step.Value == nil {
				return nil, jsonError(fmt.Sprintf("%s.steps[%d]", path, i), "MISSING_FIELD", "step requires at and value")
			}
			steps[i] = Int64Step{At: *step.At, Value: *step.Value}
		}
		for i := 1; i < len(steps); i++ {
			if steps[i].At <= steps[i-1].At {
				return nil, jsonError(path+".steps", "INVALID_STEPS", "step thresholds must be strictly increasing")
			}
		}
		return StepInt64(input, steps...), nil
	case "clamp_int64":
		var dto struct {
			Type  string          `json:"type"`
			Value json.RawMessage `json:"value"`
			Min   json.RawMessage `json:"min"`
			Max   json.RawMessage `json:"max"`
		}
		if err := decodeAt(raw, path, &dto); err != nil {
			return nil, err
		}
		if len(dto.Value) == 0 || len(dto.Min) == 0 || len(dto.Max) == 0 {
			return nil, jsonError(path, "MISSING_FIELD", "clamp_int64 requires value, min and max")
		}
		value, err := c.parseInt64Expr(dto.Value, path+".value", depth+1)
		if err != nil {
			return nil, err
		}
		min, err := c.parseInt64Expr(dto.Min, path+".min", depth+1)
		if err != nil {
			return nil, err
		}
		max, err := c.parseInt64Expr(dto.Max, path+".max", depth+1)
		if err != nil {
			return nil, err
		}
		return ClampInt64(value, min, max), nil
	case "add_int64", "sub_int64":
		var dto struct {
			Type  string          `json:"type"`
			Left  json.RawMessage `json:"left"`
			Right json.RawMessage `json:"right"`
		}
		if err := decodeAt(raw, path, &dto); err != nil {
			return nil, err
		}
		if len(dto.Left) == 0 || len(dto.Right) == 0 {
			return nil, jsonError(path, "MISSING_FIELD", "%s requires left and right", typeName)
		}
		left, err := c.parseInt64Expr(dto.Left, path+".left", depth+1)
		if err != nil {
			return nil, err
		}
		right, err := c.parseInt64Expr(dto.Right, path+".right", depth+1)
		if err != nil {
			return nil, err
		}
		if typeName == "add_int64" {
			return AddInt64(left, right), nil
		}
		return SubInt64(left, right), nil
	default:
		return nil, jsonError(path+".type", "EXPRESSION_TYPE_MISMATCH", "expression type %q does not produce int64", typeName)
	}
}

func (c *JSONCompiler) parseCondition(raw json.RawMessage, path string, depth int) (Condition, error) {
	if err := c.checkDepth(path, depth); err != nil {
		return nil, err
	}
	typeName, err := decodeType(raw, path)
	if err != nil {
		return nil, err
	}
	if typeName != "gte_int64" {
		return nil, jsonError(path+".type", "UNKNOWN_TYPE", "unknown condition type %q", typeName)
	}
	var dto struct {
		Type  string          `json:"type"`
		Left  json.RawMessage `json:"left"`
		Right json.RawMessage `json:"right"`
	}
	if err := decodeAt(raw, path, &dto); err != nil {
		return nil, err
	}
	if len(dto.Left) == 0 || len(dto.Right) == 0 {
		return nil, jsonError(path, "MISSING_FIELD", "gte_int64 requires left and right")
	}
	left, err := c.parseInt64Expr(dto.Left, path+".left", depth+1)
	if err != nil {
		return nil, err
	}
	right, err := c.parseInt64Expr(dto.Right, path+".right", depth+1)
	if err != nil {
		return nil, err
	}
	return GreaterOrEqual(left, right), nil
}

func (c *JSONCompiler) resolveJSONIndex(name string, expected IndexType, path string) (indexSpec, error) {
	if err := c.requireName(path, name); err != nil {
		return indexSpec{}, err
	}
	spec, ok := c.indexesByName[name]
	if !ok {
		return indexSpec{}, jsonError(path, "UNAVAILABLE_INDEX", "index %q is not available in the fixed JSON contract", name)
	}
	if spec.kind != expected {
		return indexSpec{}, jsonError(path, "QUERY_INDEX_MISMATCH", "index %q has type %q; query requires %q", name, spec.kind, expected)
	}
	return spec, nil
}

func (c *JSONCompiler) resolveJSONFact(name string, expected FactType, path string) error {
	if err := c.requireName(path, name); err != nil {
		return err
	}
	fact, ok := c.factsByName[name]
	if !ok {
		return jsonError(path, "UNAVAILABLE_FACT", "fact %q is not available in the fixed JSON contract", name)
	}
	if fact.Type != expected {
		return jsonError(path, "FACT_TYPE_MISMATCH", "fact %q has type %s; expression requires %s", name, factTypeName(fact.Type), factTypeName(expected))
	}
	return nil
}

func factTypeName(value FactType) string {
	switch value {
	case FactTypeStrings:
		return "strings"
	case FactTypeUint64s:
		return "uint64s"
	case FactTypeInt64:
		return "int64"
	default:
		return fmt.Sprintf("unknown(%d)", value)
	}
}

func (c *JSONCompiler) requireName(path, value string) error {
	return requireJSONName(c.limits, path, value)
}

func (c *JSONCompiler) checkString(path, value string) error {
	if len(value) > c.limits.MaxStringBytes {
		return jsonError(path, "STRING_SIZE_LIMIT", "string contains %d bytes; maximum is %d", len(value), c.limits.MaxStringBytes)
	}
	return nil
}

func (c *JSONCompiler) checkDepth(path string, depth int) error {
	if depth > c.limits.MaxDepth {
		return jsonError(path, "DEPTH_LIMIT", "expression depth exceeds %d", c.limits.MaxDepth)
	}
	return nil
}

func (c *JSONCompiler) checkLiteralCount(path string, count int) error {
	if count > c.limits.MaxLiteralValues {
		return jsonError(path, "VALUE_LIMIT", "array contains %d values; maximum is %d", count, c.limits.MaxLiteralValues)
	}
	return nil
}

func normalizeJSONLimits(in JSONLimits) JSONLimits {
	defaults := DefaultJSONLimits()
	if in.MaxBytes <= 0 {
		in.MaxBytes = defaults.MaxBytes
	}
	if in.MaxDepth <= 0 {
		in.MaxDepth = defaults.MaxDepth
	}
	if in.MaxValues <= 0 {
		in.MaxValues = defaults.MaxValues
	}
	if in.MaxChildren <= 0 {
		in.MaxChildren = defaults.MaxChildren
	}
	if in.MaxLiteralValues <= 0 {
		in.MaxLiteralValues = defaults.MaxLiteralValues
	}
	if in.MaxSteps <= 0 {
		in.MaxSteps = defaults.MaxSteps
	}
	if in.MaxStringBytes <= 0 {
		in.MaxStringBytes = defaults.MaxStringBytes
	}
	if in.MaxIndexes <= 0 {
		in.MaxIndexes = defaults.MaxIndexes
	}
	if in.MaxFacts <= 0 {
		in.MaxFacts = defaults.MaxFacts
	}
	return in
}

func validateJSONLimits(limits JSONLimits) error {
	values := []struct {
		name  string
		value int
	}{
		{name: "maxBytes", value: limits.MaxBytes},
		{name: "maxDepth", value: limits.MaxDepth},
		{name: "maxValues", value: limits.MaxValues},
		{name: "maxChildren", value: limits.MaxChildren},
		{name: "maxLiteralValues", value: limits.MaxLiteralValues},
		{name: "maxSteps", value: limits.MaxSteps},
		{name: "maxStringBytes", value: limits.MaxStringBytes},
		{name: "maxIndexes", value: limits.MaxIndexes},
		{name: "maxFacts", value: limits.MaxFacts},
	}
	for _, item := range values {
		if item.value < 0 {
			return compileError("jsonContract.limits."+item.name, "INVALID_JSON_LIMIT", "JSON limit must not be negative")
		}
	}
	return nil
}

func validateJSONInput(data []byte, limits JSONLimits) error {
	if len(data) > limits.MaxBytes {
		return jsonError("$", "JSON_SIZE_LIMIT", "JSON contains %d bytes; maximum is %d", len(data), limits.MaxBytes)
	}
	if !utf8.Valid(data) {
		return jsonError("$", "INVALID_UTF8", "JSON must be valid UTF-8")
	}
	return validateJSONShape(data, limits)
}

func requireJSONName(limits JSONLimits, path, value string) error {
	if value == "" {
		return jsonError(path, "MISSING_FIELD", "name is required")
	}
	if len(value) > limits.MaxStringBytes {
		return jsonError(path, "STRING_SIZE_LIMIT", "string contains %d bytes; maximum is %d", len(value), limits.MaxStringBytes)
	}
	return nil
}

func decodeType(raw []byte, path string) (string, error) {
	var header struct {
		Type string `json:"type"`
	}
	// Unknown fields are intentionally allowed for this first discriminator pass;
	// the selected concrete DTO is decoded strictly immediately afterwards.
	if err := json.Unmarshal(raw, &header); err != nil {
		return "", structureError(path, err)
	}
	if header.Type == "" {
		return "", jsonError(path+".type", "MISSING_FIELD", "type is required")
	}
	return header.Type, nil
}

func decodeAt(raw []byte, path string, target any) error {
	if err := decodeStrict(raw, target); err != nil {
		return structureError(path, err)
	}
	return nil
}

func decodeStrict(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return fmt.Errorf("multiple JSON values")
		}
		return err
	}
	return nil
}

func structureError(path string, err error) error {
	code := "INVALID_STRUCTURE"
	const unknownPrefix = "json: unknown field "
	if strings.HasPrefix(err.Error(), unknownPrefix) {
		code = "UNKNOWN_FIELD"
		if field, unquoteErr := strconv.Unquote(strings.TrimPrefix(err.Error(), unknownPrefix)); unquoteErr == nil {
			path = jsonFieldPath(path, field)
		}
	}
	return jsonError(path, code, "%v", err)
}

func validateJSONShape(data []byte, limits JSONLimits) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	first, err := decoder.Token()
	if err != nil {
		return jsonError("$", "INVALID_JSON", "%v", err)
	}
	if first != json.Delim('{') {
		return jsonError("$", "INVALID_ROOT", "Prefilter JSON root must be an object")
	}
	values := 1
	if err := visitJSONObject(decoder, "$", 1, limits, &values); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return jsonError("$", "TRAILING_JSON", "a second JSON value is not allowed")
		}
		return jsonError("$", "INVALID_JSON", "%v", err)
	}
	return nil
}

func visitJSONValue(decoder *json.Decoder, path string, depth int, limits JSONLimits, values *int) error {
	if depth > limits.MaxDepth {
		return jsonError(path, "DEPTH_LIMIT", "JSON depth exceeds %d", limits.MaxDepth)
	}
	token, err := decoder.Token()
	if err != nil {
		return jsonError(path, "INVALID_JSON", "%v", err)
	}
	(*values)++
	if *values > limits.MaxValues {
		return jsonError(path, "JSON_VALUE_LIMIT", "JSON contains more than %d values", limits.MaxValues)
	}
	if token == nil {
		return jsonError(path, "NULL_NOT_ALLOWED", "null is not allowed")
	}
	if text, ok := token.(string); ok && len(text) > limits.MaxStringBytes {
		return jsonError(path, "STRING_SIZE_LIMIT", "string contains %d bytes; maximum is %d", len(text), limits.MaxStringBytes)
	}
	switch token {
	case json.Delim('{'):
		return visitJSONObject(decoder, path, depth, limits, values)
	case json.Delim('['):
		index := 0
		for decoder.More() {
			if err := visitJSONValue(decoder, fmt.Sprintf("%s[%d]", path, index), depth+1, limits, values); err != nil {
				return err
			}
			index++
		}
		_, err := decoder.Token()
		if err != nil {
			return jsonError(path, "INVALID_JSON", "%v", err)
		}
	}
	return nil
}

func visitJSONObject(decoder *json.Decoder, path string, depth int, limits JSONLimits, values *int) error {
	seen := make(map[string]struct{})
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return jsonError(path, "INVALID_JSON", "%v", err)
		}
		key, ok := keyToken.(string)
		if !ok {
			return jsonError(path, "INVALID_JSON", "object key is not a string")
		}
		childPath := jsonFieldPath(path, key)
		if len(key) > limits.MaxStringBytes {
			return jsonError(childPath, "STRING_SIZE_LIMIT", "object key contains %d bytes; maximum is %d", len(key), limits.MaxStringBytes)
		}
		if _, exists := seen[key]; exists {
			return jsonError(childPath, "DUPLICATE_KEY", "object key %q is duplicated", key)
		}
		seen[key] = struct{}{}
		if err := visitJSONValue(decoder, childPath, depth+1, limits, values); err != nil {
			return err
		}
	}
	_, err := decoder.Token()
	if err != nil {
		return jsonError(path, "INVALID_JSON", "%v", err)
	}
	return nil
}

func jsonFieldPath(parent, field string) string {
	for i, r := range field {
		if !(r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || i > 0 && r >= '0' && r <= '9') {
			encoded, _ := json.Marshal(field)
			return parent + "[" + string(encoded) + "]"
		}
	}
	if field == "" {
		return parent + "[\"\"]"
	}
	return parent + "." + field
}
