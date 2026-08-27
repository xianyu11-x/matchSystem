package expression

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"matchSystem/internal/matchsystem/jsonstrict"
)

// ScalarSchemaVersion is the only accepted expression wire version.
const ScalarSchemaVersion = "expression-scalar/v3"

// JSONLimits bounds the complete scalar JSON document and the expression
// graph contained in its expr field. Zero values use DefaultJSONLimits.
type JSONLimits struct {
	MaxBytes        int
	MaxDepth        int
	MaxObjectFields int
	MaxArrayItems   int
	MaxValues       int
	MaxStringBytes  int

	MaxChildren      int
	MaxLiteralValues int
	MaxSteps         int
	MaxNodes         int
	MaxInstructions  int
}

func DefaultJSONLimits() JSONLimits {
	return JSONLimits{
		MaxBytes: 1 << 20, MaxDepth: 64, MaxObjectFields: 64, MaxArrayItems: 10000, MaxChildren: 128,
		MaxLiteralValues: 256, MaxSteps: 128, MaxStringBytes: 1024,
		MaxValues: 10000, MaxNodes: 10000, MaxInstructions: 10000,
	}
}

func profileJSONLimits(profile CompileProfile) JSONLimits {
	limits := profile.JSONLimits
	if limits.MaxDepth == 0 {
		limits.MaxDepth = profile.Limits.MaxDepth
	}
	if limits.MaxChildren == 0 {
		limits.MaxChildren = profile.Limits.MaxChildren
	}
	if limits.MaxLiteralValues == 0 {
		limits.MaxLiteralValues = profile.Limits.MaxLiteralValues
	}
	if limits.MaxSteps == 0 {
		limits.MaxSteps = profile.Limits.MaxSteps
	}
	if limits.MaxNodes == 0 {
		limits.MaxNodes = profile.Limits.MaxNodes
	}
	if limits.MaxInstructions == 0 {
		limits.MaxInstructions = profile.Limits.MaxInstructions
	}
	return limits
}

func effectiveJSONLimits(profile CompileProfile) JSONLimits {
	limits := profileJSONLimits(profile)
	defaults := DefaultJSONLimits()
	if limits.MaxBytes == 0 {
		limits.MaxBytes = defaults.MaxBytes
	}
	if limits.MaxDepth == 0 {
		limits.MaxDepth = defaults.MaxDepth
	}
	if limits.MaxObjectFields == 0 {
		limits.MaxObjectFields = defaults.MaxObjectFields
	}
	if limits.MaxArrayItems == 0 {
		limits.MaxArrayItems = defaults.MaxArrayItems
	}
	if limits.MaxChildren == 0 {
		limits.MaxChildren = defaults.MaxChildren
	}
	if limits.MaxLiteralValues == 0 {
		limits.MaxLiteralValues = defaults.MaxLiteralValues
	}
	if limits.MaxSteps == 0 {
		limits.MaxSteps = defaults.MaxSteps
	}
	if limits.MaxStringBytes == 0 {
		limits.MaxStringBytes = defaults.MaxStringBytes
	}
	if limits.MaxValues == 0 {
		limits.MaxValues = defaults.MaxValues
	}
	if limits.MaxNodes == 0 {
		limits.MaxNodes = defaults.MaxNodes
	}
	if limits.MaxInstructions == 0 {
		limits.MaxInstructions = defaults.MaxInstructions
	}
	return limits
}

func validateJSONLimits(limits JSONLimits) error {
	for _, item := range []struct {
		name  string
		value int
	}{
		{"maxBytes", limits.MaxBytes}, {"maxDepth", limits.MaxDepth},
		{"maxObjectFields", limits.MaxObjectFields}, {"maxArrayItems", limits.MaxArrayItems},
		{"maxValues", limits.MaxValues}, {"maxStringBytes", limits.MaxStringBytes},
		{"maxChildren", limits.MaxChildren}, {"maxLiteralValues", limits.MaxLiteralValues},
		{"maxSteps", limits.MaxSteps}, {"maxNodes", limits.MaxNodes},
		{"maxInstructions", limits.MaxInstructions},
	} {
		if item.value < 0 {
			return jsonError("$.limits."+item.name, "INVALID_LIMIT", "limit must not be negative")
		}
	}
	return nil
}

// ScalarCompileOptions carries the source, Fact, attribute, and limit policy
// for one scalar document. The expression grammar is always closed.
type ScalarCompileOptions struct {
	Profile CompileProfile
}

// CompileScalarJSON validates, parses, and compiles exactly one
// expression-scalar/v3 envelope. The returned program is opaque: callers can
// inspect only its result type, metadata, and matching typed evaluator.
func CompileScalarJSON(data []byte, options ScalarCompileOptions) (*ScalarProgram, error) {
	profile := cloneProfile(options.Profile)
	if err := validateProfile(profile); err != nil {
		return nil, err
	}
	limits := effectiveJSONLimits(profile)
	if err := validateJSONLimits(limits); err != nil {
		return nil, err
	}
	if err := jsonstrict.ValidateWithOptions(data, jsonstrict.Options{
		MaxBytes: limits.MaxBytes, MaxDepth: limits.MaxDepth,
		MaxObjectFields: limits.MaxObjectFields, MaxArrayItems: limits.MaxArrayItems,
		MaxValues: limits.MaxValues, MaxStringBytes: limits.MaxStringBytes,
	}); err != nil {
		return nil, adaptStrictError(err)
	}
	result, expr, err := parseScalarEnvelope(data)
	if err != nil {
		return nil, err
	}
	if !allowedRoot(profile.AllowedRoots, result) {
		return nil, jsonError("$.resultType", "ROOT_NOT_ALLOWED", "root result %s is not allowed by the caller profile", result)
	}
	builder := &scalarBuilder{profile: profile, limits: limits}
	_, got, err := builder.parseNode(expr, "$.expr", 1)
	if err != nil {
		return nil, err
	}
	if got != result {
		return nil, compileError("$.expr", "ROOT_TYPE_MISMATCH", "expression produces %s but resultType declares %s", got, result)
	}
	if limits.MaxInstructions > 0 && len(builder.nodes) > limits.MaxInstructions {
		return nil, compileError("$.expr", "INSTRUCTION_LIMIT", "program contains %d instructions; maximum is %d", len(builder.nodes), limits.MaxInstructions)
	}
	program, err := buildScalarProgram(result, builder.nodes, builder.deps)
	if err != nil {
		return nil, err
	}
	return program, nil
}

func parseScalarEnvelope(data []byte) (ResultType, json.RawMessage, error) {
	object, err := decodeObject(data, "$")
	if err != nil {
		return ResultInvalid, nil, err
	}
	if err := checkFields(object, "$", "schemaVersion", "resultType", "expr"); err != nil {
		return ResultInvalid, nil, err
	}
	versionRaw, err := requiredField(object, "schemaVersion", "$.schemaVersion")
	if err != nil {
		return ResultInvalid, nil, err
	}
	version, err := parseString(versionRaw, "$.schemaVersion")
	if err != nil {
		return ResultInvalid, nil, err
	}
	if version != ScalarSchemaVersion {
		return ResultInvalid, nil, jsonError("$.schemaVersion", "UNKNOWN_SCHEMA_VERSION", "unsupported schema version %q", version)
	}
	resultRaw, err := requiredField(object, "resultType", "$.resultType")
	if err != nil {
		return ResultInvalid, nil, err
	}
	result, err := parseResult(resultRaw, "$.resultType")
	if err != nil {
		return ResultInvalid, nil, err
	}
	expr, err := requiredField(object, "expr", "$.expr")
	if err != nil {
		return ResultInvalid, nil, err
	}
	return result, expr, nil
}

func (b *scalarBuilder) parseNode(raw json.RawMessage, path string, depth int) (int, ResultType, error) {
	if b.limits.MaxDepth > 0 && depth > b.limits.MaxDepth {
		return -1, ResultInvalid, jsonError(path, "DEPTH_LIMIT", "expression depth exceeds %d", b.limits.MaxDepth)
	}
	object, err := decodeObject(raw, path)
	if err != nil {
		return -1, ResultInvalid, err
	}
	opRaw, err := requiredField(object, "op", path+".op")
	if err != nil {
		return -1, ResultInvalid, err
	}
	op, err := parseString(opRaw, path+".op")
	if err != nil {
		return -1, ResultInvalid, err
	}
	child := func(name string, want ResultType) (int, error) {
		rawChild, childErr := requiredField(object, name, path+"."+name)
		if childErr != nil {
			return -1, childErr
		}
		id, got, parseErr := b.parseNode(rawChild, path+"."+name, depth+1)
		if parseErr != nil {
			return -1, parseErr
		}
		if got != want {
			return -1, jsonError(path+"."+name, "TYPE_MISMATCH", "child produces %s; expected %s", got, want)
		}
		return id, nil
	}
	children := func(name string, want ResultType) ([]int, error) {
		rawChildren, childErr := requiredField(object, name, path+"."+name)
		if childErr != nil {
			return nil, childErr
		}
		items, arrayErr := parseArray(rawChildren, path+"."+name)
		if arrayErr != nil {
			return nil, arrayErr
		}
		if len(items) == 0 {
			return nil, jsonError(path+"."+name, "EMPTY_CHILDREN", "%s requires at least one child", op)
		}
		if b.limits.MaxChildren > 0 && len(items) > b.limits.MaxChildren {
			return nil, jsonError(path+"."+name, "CHILD_LIMIT", "too many children")
		}
		ids := make([]int, len(items))
		for index, item := range items {
			id, got, parseErr := b.parseNode(item, fmt.Sprintf("%s.%s[%d]", path, name, index), depth+1)
			if parseErr != nil {
				return nil, parseErr
			}
			if got != want {
				return nil, jsonError(fmt.Sprintf("%s.%s[%d]", path, name, index), "TYPE_MISMATCH", "child produces %s; expected %s", got, want)
			}
			ids[index] = id
		}
		return ids, nil
	}
	fixed := func(count int) error {
		if b.limits.MaxChildren > 0 && count > b.limits.MaxChildren {
			return jsonError(path, "CHILD_LIMIT", "node contains %d children; maximum is %d", count, b.limits.MaxChildren)
		}
		return nil
	}
	add := func(node scalarNode) (int, ResultType, error) {
		id, addErr := b.add(node, path)
		if addErr != nil {
			return -1, ResultInvalid, addErr
		}
		return id, node.result, nil
	}

	switch op {
	case "bool_literal":
		if err := checkFields(object, path, "op", "value"); err != nil {
			return -1, ResultInvalid, err
		}
		valueRaw, fieldErr := requiredField(object, "value", path+".value")
		if fieldErr != nil {
			return -1, ResultInvalid, fieldErr
		}
		value, parseErr := parseBool(valueRaw, path+".value")
		if parseErr != nil {
			return -1, ResultInvalid, parseErr
		}
		node := newScalarNode(kindBoolLiteral, ResultBool)
		node.boolValue = value
		return add(node)
	case "bool_and", "bool_or":
		if err := checkFields(object, path, "op", "children"); err != nil {
			return -1, ResultInvalid, err
		}
		items, childErr := children("children", ResultBool)
		if childErr != nil {
			return -1, ResultInvalid, childErr
		}
		kind := kindBoolAnd
		if op == "bool_or" {
			kind = kindBoolOr
		}
		node := newScalarNode(kind, ResultBool)
		node.children = items
		return add(node)
	case "bool_not":
		if err := checkFields(object, path, "op", "value"); err != nil {
			return -1, ResultInvalid, err
		}
		if err := fixed(1); err != nil {
			return -1, ResultInvalid, err
		}
		value, childErr := child("value", ResultBool)
		if childErr != nil {
			return -1, ResultInvalid, childErr
		}
		node := newScalarNode(kindBoolNot, ResultBool)
		node.value = value
		return add(node)
	case "int64_literal":
		if err := checkFields(object, path, "op", "value"); err != nil {
			return -1, ResultInvalid, err
		}
		valueRaw, fieldErr := requiredField(object, "value", path+".value")
		if fieldErr != nil {
			return -1, ResultInvalid, fieldErr
		}
		value, parseErr := parseInt64(valueRaw, path+".value")
		if parseErr != nil {
			return -1, ResultInvalid, parseErr
		}
		node := newScalarNode(kindInt64Literal, ResultInt64)
		node.int64Value = value
		return add(node)
	case "int64_ref", "strings_ref", "uint64s_ref":
		if err := checkFields(object, path, "op", "source", "name"); err != nil {
			return -1, ResultInvalid, err
		}
		source, name, refErr := parseReference(object, path)
		if refErr != nil {
			return -1, ResultInvalid, refErr
		}
		result := ResultInt64
		if op == "strings_ref" {
			result = ResultStrings
		} else if op == "uint64s_ref" {
			result = ResultUint64s
		}
		id, addErr := b.addLookup(result, source, name, path)
		if addErr != nil {
			return -1, ResultInvalid, addErr
		}
		return id, result, nil
	case "strings_literal":
		if err := checkFields(object, path, "op", "values"); err != nil {
			return -1, ResultInvalid, err
		}
		values, valueErr := parseStrings(object["values"], path+".values", b.limits)
		if valueErr != nil {
			return -1, ResultInvalid, valueErr
		}
		node := newScalarNode(kindStringsLiteral, ResultStrings)
		node.strings = values
		return add(node)
	case "strings_union":
		if err := checkFields(object, path, "op", "items"); err != nil {
			return -1, ResultInvalid, err
		}
		items, childErr := children("items", ResultStrings)
		if childErr != nil {
			return -1, ResultInvalid, childErr
		}
		node := newScalarNode(kindStringsUnion, ResultStrings)
		node.children = items
		return add(node)
	case "uint64s_literal":
		if err := checkFields(object, path, "op", "values"); err != nil {
			return -1, ResultInvalid, err
		}
		values, valueErr := parseUint64s(object["values"], path+".values", b.limits)
		if valueErr != nil {
			return -1, ResultInvalid, valueErr
		}
		node := newScalarNode(kindUint64sLiteral, ResultUint64s)
		node.uint64s = values
		return add(node)
	case "uint64s_union":
		if err := checkFields(object, path, "op", "items"); err != nil {
			return -1, ResultInvalid, err
		}
		items, childErr := children("items", ResultUint64s)
		if childErr != nil {
			return -1, ResultInvalid, childErr
		}
		node := newScalarNode(kindUint64sUnion, ResultUint64s)
		node.children = items
		return add(node)
	case "int64_add", "int64_sub", "int64_min", "int64_max":
		if err := checkFields(object, path, "op", "left", "right"); err != nil {
			return -1, ResultInvalid, err
		}
		if err := fixed(2); err != nil {
			return -1, ResultInvalid, err
		}
		left, leftErr := child("left", ResultInt64)
		if leftErr != nil {
			return -1, ResultInvalid, leftErr
		}
		right, rightErr := child("right", ResultInt64)
		if rightErr != nil {
			return -1, ResultInvalid, rightErr
		}
		kind := map[string]scalarKind{"int64_add": kindInt64Add, "int64_sub": kindInt64Sub, "int64_min": kindInt64Min, "int64_max": kindInt64Max}[op]
		node := newScalarNode(kind, ResultInt64)
		node.left, node.right = left, right
		return add(node)
	case "int64_step":
		if err := checkFields(object, path, "op", "input", "steps"); err != nil {
			return -1, ResultInvalid, err
		}
		if err := fixed(1); err != nil {
			return -1, ResultInvalid, err
		}
		input, inputErr := child("input", ResultInt64)
		if inputErr != nil {
			return -1, ResultInvalid, inputErr
		}
		steps, stepErr := parseSteps(object["steps"], path+".steps", b.limits)
		if stepErr != nil {
			return -1, ResultInvalid, stepErr
		}
		node := newScalarNode(kindInt64Step, ResultInt64)
		node.input, node.steps = input, steps
		return add(node)
	case "int64_clamp":
		if err := checkFields(object, path, "op", "value", "min", "max"); err != nil {
			return -1, ResultInvalid, err
		}
		if err := fixed(3); err != nil {
			return -1, ResultInvalid, err
		}
		value, valueErr := child("value", ResultInt64)
		if valueErr != nil {
			return -1, ResultInvalid, valueErr
		}
		minimum, minErr := child("min", ResultInt64)
		if minErr != nil {
			return -1, ResultInvalid, minErr
		}
		maximum, maxErr := child("max", ResultInt64)
		if maxErr != nil {
			return -1, ResultInvalid, maxErr
		}
		node := newScalarNode(kindInt64Clamp, ResultInt64)
		node.value, node.minimum, node.maximum = value, minimum, maximum
		return add(node)
	case "int64_eq", "int64_neq", "int64_lt", "int64_lte", "int64_gt", "int64_gte":
		if err := checkFields(object, path, "op", "left", "right"); err != nil {
			return -1, ResultInvalid, err
		}
		if err := fixed(2); err != nil {
			return -1, ResultInvalid, err
		}
		left, leftErr := child("left", ResultInt64)
		if leftErr != nil {
			return -1, ResultInvalid, leftErr
		}
		right, rightErr := child("right", ResultInt64)
		if rightErr != nil {
			return -1, ResultInvalid, rightErr
		}
		kind := map[string]scalarKind{
			"int64_eq": kindInt64Equal, "int64_neq": kindInt64NotEqual,
			"int64_lt": kindInt64Less, "int64_lte": kindInt64LessOrEqual,
			"int64_gt": kindInt64Greater, "int64_gte": kindInt64GreaterOrEqual,
		}[op]
		node := newScalarNode(kind, ResultBool)
		node.left, node.right = left, right
		return add(node)
	case "strings_eq", "strings_neq", "strings_is_empty", "strings_contains", "strings_contains_any", "strings_contains_all", "strings_intersects":
		return b.parseStringPredicate(object, op, path, depth, child, fixed)
	case "uint64s_eq", "uint64s_neq", "uint64s_is_empty", "uint64s_contains", "uint64s_contains_any", "uint64s_contains_all", "uint64s_intersects":
		return b.parseUint64Predicate(object, op, path, depth, child, fixed)
	default:
		return -1, ResultInvalid, jsonError(path+".op", "UNKNOWN_OP", "unknown expression op %q", op)
	}
}

func (b *scalarBuilder) parseStringPredicate(object map[string]json.RawMessage, op, path string, depth int, child func(string, ResultType) (int, error), fixed func(int) error) (int, ResultType, error) {
	fields := []string{"op", "values"}
	if op == "strings_eq" || op == "strings_neq" || op == "strings_contains_any" || op == "strings_contains_all" || op == "strings_intersects" {
		fields = append(fields, "other")
	}
	if op == "strings_contains" {
		fields = append(fields, "needle")
	}
	if err := checkFields(object, path, fields...); err != nil {
		return -1, ResultInvalid, err
	}
	childCount := 2
	if op == "strings_is_empty" || op == "strings_contains" {
		childCount = 1
	}
	if err := fixed(childCount); err != nil {
		return -1, ResultInvalid, err
	}
	values, err := child("values", ResultStrings)
	if err != nil {
		return -1, ResultInvalid, err
	}
	node := newScalarNode(stringPredicateKind(op), ResultBool)
	node.value = values
	if op == "strings_contains" {
		needle, parseErr := parseString(object["needle"], path+".needle")
		if parseErr != nil {
			return -1, ResultInvalid, parseErr
		}
		node.name = needle
	} else if op != "strings_is_empty" {
		other, childErr := child("other", ResultStrings)
		if childErr != nil {
			return -1, ResultInvalid, childErr
		}
		node.right = other
	}
	id, addErr := b.add(node, path)
	if addErr != nil {
		return -1, ResultInvalid, addErr
	}
	return id, ResultBool, nil
}

func stringPredicateKind(op string) scalarKind {
	switch op {
	case "strings_eq":
		return kindStringsEqual
	case "strings_neq":
		return kindStringsNotEqual
	case "strings_is_empty":
		return kindStringsEmpty
	case "strings_contains":
		return kindStringsContains
	case "strings_contains_any":
		return kindStringsContainsAny
	case "strings_contains_all":
		return kindStringsContainsAll
	default:
		return kindStringsIntersects
	}
}

func (b *scalarBuilder) parseUint64Predicate(object map[string]json.RawMessage, op, path string, depth int, child func(string, ResultType) (int, error), fixed func(int) error) (int, ResultType, error) {
	fields := []string{"op", "values"}
	if op == "uint64s_eq" || op == "uint64s_neq" || op == "uint64s_contains_any" || op == "uint64s_contains_all" || op == "uint64s_intersects" {
		fields = append(fields, "other")
	}
	if op == "uint64s_contains" {
		fields = append(fields, "needle")
	}
	if err := checkFields(object, path, fields...); err != nil {
		return -1, ResultInvalid, err
	}
	childCount := 2
	if op == "uint64s_is_empty" || op == "uint64s_contains" {
		childCount = 1
	}
	if err := fixed(childCount); err != nil {
		return -1, ResultInvalid, err
	}
	values, err := child("values", ResultUint64s)
	if err != nil {
		return -1, ResultInvalid, err
	}
	node := newScalarNode(uint64PredicateKind(op), ResultBool)
	node.value = values
	if op == "uint64s_contains" {
		needle, parseErr := parseUint64(object["needle"], path+".needle")
		if parseErr != nil {
			return -1, ResultInvalid, parseErr
		}
		node.uint64s = []uint64{needle}
	} else if op != "uint64s_is_empty" {
		other, childErr := child("other", ResultUint64s)
		if childErr != nil {
			return -1, ResultInvalid, childErr
		}
		node.right = other
	}
	id, addErr := b.add(node, path)
	if addErr != nil {
		return -1, ResultInvalid, addErr
	}
	return id, ResultBool, nil
}

func uint64PredicateKind(op string) scalarKind {
	switch op {
	case "uint64s_eq":
		return kindUint64sEqual
	case "uint64s_neq":
		return kindUint64sNotEqual
	case "uint64s_is_empty":
		return kindUint64sEmpty
	case "uint64s_contains":
		return kindUint64sContains
	case "uint64s_contains_any":
		return kindUint64sContainsAny
	case "uint64s_contains_all":
		return kindUint64sContainsAll
	default:
		return kindUint64sIntersects
	}
}

func parseReference(object map[string]json.RawMessage, path string) (Source, string, error) {
	sourceRaw, err := requiredField(object, "source", path+".source")
	if err != nil {
		return 0, "", err
	}
	sourceName, err := parseString(sourceRaw, path+".source")
	if err != nil {
		return 0, "", err
	}
	source, err := parseSource(sourceName, path+".source")
	if err != nil {
		return 0, "", err
	}
	nameRaw, err := requiredField(object, "name", path+".name")
	if err != nil {
		return 0, "", err
	}
	name, err := parseString(nameRaw, path+".name")
	if err != nil {
		return 0, "", err
	}
	if name == "" {
		return 0, "", jsonError(path+".name", "EMPTY_NAME", "reference name is required")
	}
	return source, name, nil
}

func parseResult(raw json.RawMessage, path string) (ResultType, error) {
	value, err := parseString(raw, path)
	if err != nil {
		return ResultInvalid, err
	}
	// The old non-scalar root spelling is recognized only to produce the
	// scalar boundary error. It is not represented by ResultType anymore.
	if value == "bitmap" {
		return ResultInvalid, jsonError(path, "ROOT_NOT_ALLOWED", "scalar expressions cannot produce %q", value)
	}
	switch value {
	case "bool":
		return ResultBool, nil
	case "int64":
		return ResultInt64, nil
	case "strings":
		return ResultStrings, nil
	case "uint64s":
		return ResultUint64s, nil
	default:
		return ResultInvalid, jsonError(path, "UNKNOWN_RESULT_TYPE", "unknown resultType %q", value)
	}
}

func parseSource(value, path string) (Source, error) {
	switch value {
	case "seed_attributes":
		return SourceSeedAttributes, nil
	case "seed_facts":
		return SourceSeedFacts, nil
	case "tick_facts":
		return SourceTickFacts, nil
	case "candidate_attributes":
		return SourceCandidateAttributes, nil
	case "candidate_facts":
		return SourceCandidateFacts, nil
	case "match_facts":
		return SourceMatchFacts, nil
	default:
		return 0, compileError(path, "UNKNOWN_SOURCE", "unknown source %q", value)
	}
}

func parseSteps(raw json.RawMessage, path string, limits JSONLimits) ([]scalarStep, error) {
	items, err := parseArray(raw, path)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, jsonError(path, "EMPTY_STEPS", "steps are required")
	}
	if limits.MaxSteps > 0 && len(items) > limits.MaxSteps {
		return nil, jsonError(path, "STEP_LIMIT", "too many steps")
	}
	steps := make([]scalarStep, len(items))
	for index, item := range items {
		itemPath := fmt.Sprintf("%s[%d]", path, index)
		object, objectErr := decodeObject(item, itemPath)
		if objectErr != nil {
			return nil, objectErr
		}
		if err := checkFields(object, itemPath, "at", "value"); err != nil {
			return nil, err
		}
		atRaw, fieldErr := requiredField(object, "at", itemPath+".at")
		if fieldErr != nil {
			return nil, fieldErr
		}
		valueRaw, fieldErr := requiredField(object, "value", itemPath+".value")
		if fieldErr != nil {
			return nil, fieldErr
		}
		at, parseErr := parseInt64(atRaw, itemPath+".at")
		if parseErr != nil {
			return nil, parseErr
		}
		value, parseErr := parseInt64(valueRaw, itemPath+".value")
		if parseErr != nil {
			return nil, parseErr
		}
		if index > 0 && at <= steps[index-1].at {
			return nil, jsonError(itemPath+".at", "INVALID_STEPS", "step thresholds must be strictly increasing")
		}
		steps[index] = scalarStep{at: at, value: value}
	}
	return steps, nil
}

func parseStrings(raw json.RawMessage, path string, limits JSONLimits) ([]string, error) {
	items, err := parseArray(raw, path)
	if err != nil {
		return nil, err
	}
	if limits.MaxLiteralValues > 0 && len(items) > limits.MaxLiteralValues {
		return nil, jsonError(path, "VALUE_LIMIT", "too many literal values")
	}
	values := make([]string, len(items))
	for index, item := range items {
		value, parseErr := parseString(item, fmt.Sprintf("%s[%d]", path, index))
		if parseErr != nil {
			return nil, parseErr
		}
		values[index] = value
	}
	return values, nil
}

func parseUint64s(raw json.RawMessage, path string, limits JSONLimits) ([]uint64, error) {
	items, err := parseArray(raw, path)
	if err != nil {
		return nil, err
	}
	if limits.MaxLiteralValues > 0 && len(items) > limits.MaxLiteralValues {
		return nil, jsonError(path, "VALUE_LIMIT", "too many literal values")
	}
	values := make([]uint64, len(items))
	for index, item := range items {
		value, parseErr := parseUint64(item, fmt.Sprintf("%s[%d]", path, index))
		if parseErr != nil {
			return nil, parseErr
		}
		values[index] = value
	}
	return values, nil
}

func parseArray(raw json.RawMessage, path string) ([]json.RawMessage, error) {
	if isNull(raw) {
		return nil, jsonError(path, "NULL_FIELD", "array must not be null")
	}
	var values []json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil || values == nil && !bytes.Equal(bytes.TrimSpace(raw), []byte("[]")) {
		return nil, jsonError(path, "TYPE_MISMATCH", "array is required")
	}
	return values, nil
}

func parseString(raw json.RawMessage, path string) (string, error) {
	if isNull(raw) {
		return "", jsonError(path, "NULL_FIELD", "string must not be null")
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", jsonError(path, "TYPE_MISMATCH", "string is required")
	}
	return value, nil
}

func parseBool(raw json.RawMessage, path string) (bool, error) {
	if isNull(raw) {
		return false, jsonError(path, "NULL_FIELD", "bool must not be null")
	}
	var value bool
	if err := json.Unmarshal(raw, &value); err != nil {
		return false, jsonError(path, "TYPE_MISMATCH", "bool is required")
	}
	return value, nil
}

func parseInt64(raw json.RawMessage, path string) (int64, error) {
	if isNull(raw) {
		return 0, jsonError(path, "NULL_FIELD", "int64 must not be null")
	}
	value, err := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
	if err != nil {
		return 0, jsonError(path, "INVALID_NUMBER", "invalid int64 value")
	}
	return value, nil
}

func parseUint64(raw json.RawMessage, path string) (uint64, error) {
	if isNull(raw) {
		return 0, jsonError(path, "NULL_FIELD", "uint64 must not be null")
	}
	value, err := strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 64)
	if err != nil {
		return 0, jsonError(path, "INVALID_NUMBER", "invalid uint64 value")
	}
	return value, nil
}

func decodeObject(raw []byte, path string) (map[string]json.RawMessage, error) {
	if isNull(raw) {
		return nil, jsonError(path, "NULL_NODE", "object must not be null")
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil || object == nil {
		return nil, jsonError(path, "INVALID_OBJECT", "object is required")
	}
	return object, nil
}

func requiredField(object map[string]json.RawMessage, name, path string) (json.RawMessage, error) {
	value, ok := object[name]
	if !ok {
		return nil, jsonError(path, "MISSING_FIELD", "%s is required", name)
	}
	if isNull(value) {
		return nil, jsonError(path, "NULL_FIELD", "%s must not be null", name)
	}
	return value, nil
}

func checkFields(object map[string]json.RawMessage, path string, fields ...string) error {
	allowed := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		allowed[field] = struct{}{}
	}
	unknown := make([]string, 0)
	for key := range object {
		if _, ok := allowed[key]; !ok {
			unknown = append(unknown, key)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	sort.Strings(unknown)
	return jsonError(path+"."+unknown[0], "UNKNOWN_FIELD", "unknown field %q", unknown[0])
}

func isNull(raw []byte) bool { return bytes.Equal(bytes.TrimSpace(raw), []byte("null")) }

func adaptStrictError(err error) error {
	var structural *jsonstrict.Error
	if errors.As(err, &structural) {
		return jsonError(structural.Path, structural.Code, "%v", structural.Err)
	}
	return jsonError("$", "INVALID_JSON", "%v", err)
}

func jsonError(path, code, format string, args ...any) error {
	return &Error{Phase: "json", Path: Path(path), Code: code, Err: fmt.Errorf(format, args...)}
}

func compileError(path, code, format string, args ...any) error {
	return &Error{Phase: "compile", Path: Path(path), Code: code, Err: fmt.Errorf(format, args...)}
}

func evaluateError(path, code, format string, args ...any) error {
	return &Error{Phase: "evaluate", Path: Path(path), Code: code, Err: fmt.Errorf(format, args...)}
}
