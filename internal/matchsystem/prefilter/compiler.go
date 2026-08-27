package prefilter

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"matchSystem/internal/matchsystem/contract"
	"matchSystem/internal/matchsystem/expression"
	"matchSystem/internal/matchsystem/fact"
	"matchSystem/internal/matchsystem/jsonstrict"
)

const prefilterSchemaVersion = "prefilter/v3"

// bitmapCompiler is intentionally private. It owns the Prefilter bitmap
// parser/compiler and the scalar operand compilation used by the runtime.
type bitmapCompiler struct {
	contract contract.Contract
	limits   expression.JSONLimits
}

type bitmapCompileState struct {
	compiler *bitmapCompiler

	nodes   []bitmapNode
	queries []bitmapQuery
	cost    bitmapCost

	requiredIndexes    map[string]RequiredIndex
	requiredFacts      map[string]fact.Spec
	requiredAttributes map[string]contract.AttributeSpec
	indexes            map[string]indexSpec
	slots              map[string]int
}

// newBitmapCompiler snapshots the shared Contract and creates one effective
// JSON/expression limit snapshot.  A caller-supplied expression.JSONLimits can only
// tighten the Contract's overlapping limits.
func newBitmapCompiler(schema contract.Contract, supplied ...expression.JSONLimits) (*bitmapCompiler, error) {
	if len(supplied) > 1 {
		return nil, compileError("jsonLimits", "INVALID_LIMIT", "at most one JSON limits value is allowed")
	}
	if err := schema.Validate(); err != nil {
		return nil, adaptContractError(err)
	}
	if len(supplied) == 1 {
		if err := validateJSONLimits(supplied[0]); err != nil {
			return nil, err
		}
	}
	var limits expression.JSONLimits
	if len(supplied) == 1 {
		limits = supplied[0]
	} else {
		// These fields are the shared Contract boundary.  The remaining scalar
		// limits are filled from expression's defaults below.
		limits = expression.JSONLimits{
			MaxBytes:       schema.Limits.MaxBytes,
			MaxDepth:       schema.Limits.MaxDepth,
			MaxChildren:    schema.Limits.MaxChildren,
			MaxValues:      schema.Limits.MaxValues,
			MaxStringBytes: schema.Limits.MaxStringBytes,
		}
	}
	limits = normalizePrefilterJSONLimits(limits)
	limits = capBitmapJSONLimits(limits, snapshotContractLimits(schema.Limits))
	return &bitmapCompiler{contract: schema.Clone(), limits: limits}, nil
}

func capBitmapJSONLimits(limits expression.JSONLimits, contractLimits contract.Limits) expression.JSONLimits {
	capLimit := func(value, maximum int) int {
		if maximum <= 0 {
			return value
		}
		if value == 0 || value > maximum {
			return maximum
		}
		return value
	}
	limits.MaxBytes = capLimit(limits.MaxBytes, contractLimits.MaxBytes)
	limits.MaxDepth = capLimit(limits.MaxDepth, contractLimits.MaxDepth)
	limits.MaxChildren = capLimit(limits.MaxChildren, contractLimits.MaxChildren)
	limits.MaxStringBytes = capLimit(limits.MaxStringBytes, contractLimits.MaxStringBytes)
	limits.MaxValues = capLimit(limits.MaxValues, contractLimits.MaxValues)
	return limits
}

func (c *bitmapCompiler) compile(data []byte) (*Plan, error) {
	if c == nil {
		return nil, jsonError("$", "NIL_JSON_COMPILER", "JSON compiler is nil")
	}
	if err := jsonstrict.ValidateWithOptions(data, jsonstrict.Options{
		MaxBytes:        c.limits.MaxBytes,
		MaxDepth:        c.limits.MaxDepth,
		MaxObjectFields: c.limits.MaxObjectFields,
		MaxArrayItems:   c.limits.MaxArrayItems,
		MaxValues:       c.limits.MaxValues,
		MaxStringBytes:  c.limits.MaxStringBytes,
	}); err != nil {
		return nil, adaptJSONStrictError(err)
	}

	envelope, err := decodeBitmapObject(data, "$")
	if err != nil {
		return nil, err
	}
	version, err := bitmapSchemaVersion(envelope)
	if err != nil {
		return nil, err
	}
	if version != prefilterSchemaVersion {
		return nil, jsonError("$.schemaVersion", "UNKNOWN_SCHEMA_VERSION", "unsupported schemaVersion %q", version)
	}
	// Reject legacy envelopes before interpreting their old wire shape. This
	// keeps the public hard switch deterministic: an unsupported version is
	// reported even when it contains a former `plan` field or another legacy
	// member.
	if err := checkBitmapFields(envelope, "$", "schemaVersion", "bitmap", "runtime"); err != nil {
		return nil, err
	}

	runtimeThreshold := uint64(4096)
	if rawRuntime, ok := envelope["runtime"]; ok {
		runtime, runtimeErr := decodeBitmapObject(rawRuntime, "$.runtime")
		if runtimeErr != nil {
			return nil, runtimeErr
		}
		if err := checkBitmapFields(runtime, "$.runtime", "containsProbeThreshold"); err != nil {
			return nil, err
		}
		if rawThreshold, ok := runtime["containsProbeThreshold"]; ok {
			if err := decodeBitmapUint64(rawThreshold, "$.runtime.containsProbeThreshold", &runtimeThreshold); err != nil {
				return nil, err
			}
			if runtimeThreshold == 0 {
				runtimeThreshold = 4096
			}
		}
	}

	rawBitmap, ok := envelope["bitmap"]
	if !ok {
		return nil, jsonError("$.bitmap", "MISSING_FIELD", "bitmap is required")
	}
	bitmap, err := decodeBitmapObject(rawBitmap, "$.bitmap")
	if err != nil {
		return nil, err
	}
	if err := checkBitmapFields(bitmap, "$.bitmap", "resultType", "expr"); err != nil {
		return nil, err
	}
	resultType, err := bitmapStringField(bitmap, "resultType", "$.bitmap.resultType")
	if err != nil {
		return nil, err
	}
	if resultType != "bitmap" {
		switch resultType {
		case "bool", "int64", "strings", "uint64s":
			return nil, jsonError("$.bitmap.resultType", "ROOT_NOT_ALLOWED", "bitmap resultType must be bitmap")
		default:
			return nil, jsonError("$.bitmap.resultType", "UNKNOWN_RESULT_TYPE", "unknown resultType %q", resultType)
		}
	}
	rawExpr, ok := bitmap["expr"]
	if !ok {
		return nil, jsonError("$.bitmap.expr", "MISSING_FIELD", "expr is required")
	}

	state := &bitmapCompileState{
		compiler:           c,
		nodes:              []bitmapNode{{}},
		requiredIndexes:    make(map[string]RequiredIndex),
		requiredFacts:      make(map[string]fact.Spec),
		requiredAttributes: make(map[string]contract.AttributeSpec),
		indexes:            make(map[string]indexSpec, len(c.contract.Indexes)),
		slots:              make(map[string]int, len(c.contract.Indexes)),
	}
	for slot, spec := range c.contract.Indexes {
		compiled := compileIndexSpec(spec)
		state.indexes[compiled.name] = compiled
		state.slots[compiled.name] = slot
	}
	root, err := state.parseBitmapNode(rawExpr, "$.bitmap.expr", 1)
	if err != nil {
		return nil, err
	}
	rootProps, _, err := state.analyzeBitmap(root, "$.bitmap.expr", make(map[bitmapNodeID]bool), make(map[bitmapNodeID]bool))
	if err != nil {
		return nil, err
	}
	if err := validateBitmapRoot(rootProps); err != nil {
		return nil, err
	}

	requirements := state.buildRequirements()
	return c.buildPlan(state, root, runtimeThreshold, requirements)
}

// bitmapSchemaVersion intentionally differs from ordinary required fields:
// an omitted envelope version is an unsupported schema, just like a legacy or
// unknown version.  The expression-scalar loader keeps its own MISSING_FIELD
// behavior and must not reuse this rule.
func bitmapSchemaVersion(object map[string]json.RawMessage) (string, error) {
	raw, ok := object["schemaVersion"]
	if !ok {
		return "", jsonError("$.schemaVersion", "UNKNOWN_SCHEMA_VERSION", "schemaVersion is required")
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return "", jsonError("$.schemaVersion", "NULL_NOT_ALLOWED", "schemaVersion must not be null")
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", jsonError("$.schemaVersion", "TYPE_MISMATCH", "string is required")
	}
	return value, nil
}

func (s *bitmapCompileState) parseBitmapNode(raw json.RawMessage, path string, depth int) (bitmapNodeID, error) {
	if s == nil || s.compiler == nil {
		return invalidBitmapNodeID, compileError(path, "INVALID_BITMAP", "bitmap compiler state is nil")
	}
	if s.compiler.limits.MaxDepth > 0 && depth > s.compiler.limits.MaxDepth {
		return invalidBitmapNodeID, jsonError(path, "DEPTH_LIMIT", "bitmap depth exceeds %d", s.compiler.limits.MaxDepth)
	}
	object, err := decodeBitmapObject(raw, path)
	if err != nil {
		return invalidBitmapNodeID, err
	}
	op, err := bitmapStringField(object, "op", path+".op")
	if err != nil {
		return invalidBitmapNodeID, err
	}
	kind := bitmapKindForOp(op)
	if kind == bitmapKindInvalid {
		return invalidBitmapNodeID, jsonError(path+".op", "UNKNOWN_OP", "unknown bitmap op %q", op)
	}
	if count, fixed := bitmapFixedChildCount(kind); fixed {
		if err := s.checkBitmapChildCount(count, path); err != nil {
			return invalidBitmapNodeID, err
		}
	}
	if err := s.reserveBitmapNode(path); err != nil {
		return invalidBitmapNodeID, err
	}

	node := bitmapNode{kind: kind}
	switch kind {
	case bitmapKindNone:
		if err := checkBitmapFields(object, path, "op"); err != nil {
			return invalidBitmapNodeID, err
		}
	case bitmapKindAnd, bitmapKindOr:
		if err := checkBitmapFields(object, path, "op", "children"); err != nil {
			return invalidBitmapNodeID, err
		}
		children, err := s.parseBitmapChildren(object, "children", path, depth)
		if err != nil {
			return invalidBitmapNodeID, err
		}
		node.children = children
	case bitmapKindExclude:
		if err := checkBitmapFields(object, path, "op", "value"); err != nil {
			return invalidBitmapNodeID, err
		}
		rawValue, err := bitmapRequiredField(object, "value", path+".value")
		if err != nil {
			return invalidBitmapNodeID, err
		}
		value, err := s.parseBitmapNode(rawValue, path+".value", depth+1)
		if err != nil {
			return invalidBitmapNodeID, err
		}
		node.value = value
	case bitmapKindIf:
		if err := checkBitmapFields(object, path, "op", "when", "then", "else"); err != nil {
			return invalidBitmapNodeID, err
		}
		rawWhen, err := bitmapRequiredField(object, "when", path+".when")
		if err != nil {
			return invalidBitmapNodeID, err
		}
		when, err := s.compileScalar(rawWhen, path+".when", expression.ResultBool)
		if err != nil {
			return invalidBitmapNodeID, err
		}
		rawThen, err := bitmapRequiredField(object, "then", path+".then")
		if err != nil {
			return invalidBitmapNodeID, err
		}
		thenNode, err := s.parseBitmapNode(rawThen, path+".then", depth+1)
		if err != nil {
			return invalidBitmapNodeID, err
		}
		rawElse, err := bitmapRequiredField(object, "else", path+".else")
		if err != nil {
			return invalidBitmapNodeID, err
		}
		elseNode, err := s.parseBitmapNode(rawElse, path+".else", depth+1)
		if err != nil {
			return invalidBitmapNodeID, err
		}
		node.when, node.then, node.elseNode = when, thenNode, elseNode
	case bitmapKindLookupString, bitmapKindLookupUint64, bitmapKindLookupRange:
		allowed := []string{"op", "index"}
		if kind == bitmapKindLookupRange {
			allowed = append(allowed, "min", "max")
		} else {
			allowed = append(allowed, "values")
		}
		if err := checkBitmapFields(object, path, allowed...); err != nil {
			return invalidBitmapNodeID, err
		}
		indexName, err := bitmapStringField(object, "index", path+".index")
		if err != nil {
			return invalidBitmapNodeID, err
		}
		query, err := s.compileLookup(kind, indexName, object, path, depth)
		if err != nil {
			return invalidBitmapNodeID, err
		}
		node.query = bitmapQueryID(len(s.queries) + 1)
		node.props = query.properties
		s.queries = append(s.queries, *query)
	default:
		return invalidBitmapNodeID, jsonError(path+".op", "UNKNOWN_OP", "unknown bitmap op %q", op)
	}

	s.nodes = append(s.nodes, node)
	return bitmapNodeID(len(s.nodes) - 1), nil
}

func (s *bitmapCompileState) parseBitmapChildren(object map[string]json.RawMessage, name, path string, depth int) ([]bitmapNodeID, error) {
	raw, err := bitmapRequiredField(object, name, path+"."+name)
	if err != nil {
		return nil, err
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil || items == nil {
		return nil, jsonError(path+"."+name, "TYPE_MISMATCH", "children must be an array")
	}
	if len(items) == 0 {
		return nil, jsonError(path+"."+name, "EMPTY_CHILDREN", "%s requires at least one child", object["op"])
	}
	if err := s.checkBitmapChildCount(len(items), path+"."+name); err != nil {
		return nil, err
	}
	children := make([]bitmapNodeID, len(items))
	for i, item := range items {
		child, childErr := s.parseBitmapNode(item, fmt.Sprintf("%s.%s[%d]", path, name, i), depth+1)
		if childErr != nil {
			return nil, childErr
		}
		children[i] = child
	}
	return children, nil
}

func (s *bitmapCompileState) compileLookup(kind bitmapKind, indexName string, object map[string]json.RawMessage, path string, depth int) (*bitmapQuery, error) {
	if indexName == "" {
		return nil, compileError(path+".index", "MISSING_INDEX", "index is required")
	}
	index, ok := s.indexes[indexName]
	if !ok {
		return nil, compileError(path+".index", "MISSING_INDEX", "index %q is not registered", indexName)
	}
	query := &bitmapQuery{
		kind:           lookupKindForBitmap(kind),
		index:          index,
		slot:           s.slots[indexName],
		maxQueryValues: index.maxQueryValues,
	}
	if _, exists := s.slots[indexName]; !exists {
		// The map is derived from Contract.Indexes, so this is an internal
		// consistency error rather than a user-facing missing index.
		return nil, compileError(path+".index", "MISSING_INDEX", "index %q has no slot", indexName)
	}

	switch kind {
	case bitmapKindLookupString:
		if index.kind != contract.IndexTypeMultiValue || index.keyType != contract.KeyTypeString {
			return nil, compileError(path+".index", "QUERY_INDEX_MISMATCH", "index %q is not a string multi_value index", indexName)
		}
		raw, err := bitmapRequiredField(object, "values", path+".values")
		if err != nil {
			return nil, err
		}
		program, err := s.compileScalar(raw, path+".values", expression.ResultStrings)
		if err != nil {
			return nil, err
		}
		if err := validateBitmapCollectionUpperBound(program, path+".values", index); err != nil {
			return nil, err
		}
		static, values, staticErr := staticStrings(program)
		if staticErr != nil {
			return nil, adaptBitmapStaticExpressionError(staticErr, path+".values")
		}
		if static {
			query.staticValues = true
			query.staticStrings = uniqueStringsRuntime(values)
			query.staticNone = len(query.staticStrings) == 0
			if len(query.staticStrings) > index.maxQueryValues {
				return nil, compileError(path+".values", "QUERY_KEY_CONTRACT", "query produces %d keys; index %q allows %d", len(query.staticStrings), indexName, index.maxQueryValues)
			}
		} else {
			query.values = program
		}
		query.properties = bitmapQueryProperties(query.staticNone)
		operand := program.Canonical()
		if static {
			operand = canonicalBitmapStrings(query.staticStrings)
		}
		query.canonical = canonicalBitmapQuery("lookup_string", index, operand)
	case bitmapKindLookupUint64:
		if index.kind != contract.IndexTypeMultiValue || index.keyType != contract.KeyTypeUint64 {
			return nil, compileError(path+".index", "QUERY_INDEX_MISMATCH", "index %q is not a uint64 multi_value index", indexName)
		}
		raw, err := bitmapRequiredField(object, "values", path+".values")
		if err != nil {
			return nil, err
		}
		program, err := s.compileScalar(raw, path+".values", expression.ResultUint64s)
		if err != nil {
			return nil, err
		}
		if err := validateBitmapCollectionUpperBound(program, path+".values", index); err != nil {
			return nil, err
		}
		static, values, staticErr := staticUint64s(program)
		if staticErr != nil {
			return nil, adaptBitmapStaticExpressionError(staticErr, path+".values")
		}
		if static {
			query.staticValues = true
			query.staticUint64s = uniqueUint64Runtime(values)
			query.staticNone = len(query.staticUint64s) == 0
			if len(query.staticUint64s) > index.maxQueryValues {
				return nil, compileError(path+".values", "QUERY_KEY_CONTRACT", "query produces %d keys; index %q allows %d", len(query.staticUint64s), indexName, index.maxQueryValues)
			}
		} else {
			query.values = program
		}
		query.properties = bitmapQueryProperties(query.staticNone)
		operand := program.Canonical()
		if static {
			operand = canonicalBitmapUint64s(query.staticUint64s)
		}
		query.canonical = canonicalBitmapQuery("lookup_uint64", index, operand)
	case bitmapKindLookupRange:
		if index.kind != contract.IndexTypeInt64Range {
			return nil, compileError(path+".index", "QUERY_INDEX_MISMATCH", "index %q is not an int64_range index", indexName)
		}
		rawMin, err := bitmapRequiredField(object, "min", path+".min")
		if err != nil {
			return nil, err
		}
		rawMax, err := bitmapRequiredField(object, "max", path+".max")
		if err != nil {
			return nil, err
		}
		minimum, err := s.compileScalar(rawMin, path+".min", expression.ResultInt64)
		if err != nil {
			return nil, err
		}
		maximum, err := s.compileScalar(rawMax, path+".max", expression.ResultInt64)
		if err != nil {
			return nil, err
		}
		minStatic, minValue, minErr := staticInt64(minimum)
		if minErr != nil {
			return nil, adaptBitmapStaticExpressionError(minErr, path+".min")
		}
		maxStatic, maxValue, maxErr := staticInt64(maximum)
		if maxErr != nil {
			return nil, adaptBitmapStaticExpressionError(maxErr, path+".max")
		}
		query.staticRange = minStatic && maxStatic
		if minStatic {
			query.staticMin = minValue
		} else {
			query.min = minimum
		}
		if maxStatic {
			query.staticMax = maxValue
		} else {
			query.max = maximum
		}
		if query.staticRange && query.staticMin > query.staticMax {
			return nil, compileError(path, "INVALID_RANGE", "minimum %d exceeds maximum %d", query.staticMin, query.staticMax)
		}
		query.properties = bitmapQueryProperties(false)
		operand := minimum.Canonical() + "," + maximum.Canonical()
		if query.staticRange {
			operand = fmt.Sprintf("[%d,%d]", query.staticMin, query.staticMax)
		}
		query.canonical = canonicalBitmapQuery("lookup_range", index, operand)
	default:
		return nil, compileError(path, "INVALID_BITMAP", "unsupported lookup kind %s", kind)
	}
	stateIndex := requiredIndex(index)
	s.requiredIndexes[index.name] = stateIndex
	for _, attribute := range s.compiler.contract.Attributes {
		if attribute.Name == index.name {
			s.requiredAttributes[attribute.Name] = attribute
			break
		}
	}
	return query, nil
}

func bitmapQueryProperties(staticNone bool) bitmapProperties {
	if staticNone {
		return bitmapProperties{lattice: bitmapStaticNone | bitmapScopeFree}
	}
	return bitmapProperties{lattice: bitmapScopeFree | bitmapEstablishesScope}
}

func bitmapFixedChildCount(kind bitmapKind) (int, bool) {
	switch kind {
	case bitmapKindLookupString, bitmapKindLookupUint64, bitmapKindExclude:
		return 1, true
	case bitmapKindLookupRange:
		return 2, true
	case bitmapKindIf:
		return 3, true
	default:
		return 0, false
	}
}

func (s *bitmapCompileState) checkBitmapChildCount(count int, path string) error {
	if s == nil || s.compiler == nil || count < 0 {
		return nil
	}
	if max := s.compiler.limits.MaxChildren; max > 0 && count > max {
		return jsonError(path, "CHILD_LIMIT", "bitmap node contains %d children; maximum is %d", count, max)
	}
	return nil
}

func (s *bitmapCompileState) reserveBitmapNode(path string) error {
	s.cost.addBitmapNode()
	if max := s.compiler.limits.MaxNodes; max > 0 && s.cost.totalNodes > max {
		return jsonError("$", "NODE_LIMIT", "prefilter document contains %d nodes; maximum is %d", s.cost.totalNodes, max)
	}
	if max := s.compiler.limits.MaxInstructions; max > 0 && s.cost.totalInstructions > max {
		return jsonError("$", "INSTRUCTION_LIMIT", "prefilter document contains %d instructions; maximum is %d", s.cost.totalInstructions, max)
	}
	return nil
}

func (s *bitmapCompileState) compileScalar(raw json.RawMessage, path string, expected expression.ResultType) (*expression.ScalarProgram, error) {
	profile, err := s.scalarProfile()
	if err != nil {
		return nil, err
	}
	program, err := expression.CompileScalarJSON(raw, expression.ScalarCompileOptions{Profile: profile})
	if err != nil {
		return nil, adaptBitmapExpressionError(err, path)
	}
	if program == nil {
		return nil, compileError(path, "INVALID_PROGRAM", "scalar operand did not compile")
	}
	if program.ResultType() != expected {
		return nil, jsonError(path+".resultType", "DYNAMIC_RESULT_MISMATCH", "operand declares %s; expected %s", program.ResultType(), expected)
	}
	s.cost.addScalar(program)
	if max := s.compiler.limits.MaxNodes; max > 0 && s.cost.totalNodes > max {
		return nil, jsonError("$", "NODE_LIMIT", "prefilter document contains %d nodes; maximum is %d", s.cost.totalNodes, max)
	}
	if max := s.compiler.limits.MaxInstructions; max > 0 && s.cost.totalInstructions > max {
		return nil, jsonError("$", "INSTRUCTION_LIMIT", "prefilter document contains %d instructions; maximum is %d", s.cost.totalInstructions, max)
	}
	if max := s.compiler.limits.MaxLiteralValues; max > 0 && s.cost.scalarLiteralValues > max {
		return nil, jsonError("$", "VALUE_LIMIT", "prefilter scalar operands contain %d literal values; maximum is %d", s.cost.scalarLiteralValues, max)
	}
	if max := s.compiler.limits.MaxSteps; max > 0 && s.cost.scalarSteps > max {
		return nil, jsonError("$", "STEP_LIMIT", "prefilter scalar operands contain %d steps; maximum is %d", s.cost.scalarSteps, max)
	}
	for _, factSpec := range program.Dependencies().Facts() {
		s.requiredFacts[factSpec.Name] = factSpec
	}
	for _, attributeSpec := range program.Dependencies().Attributes() {
		s.requiredAttributes[attributeSpec.Name] = contract.AttributeSpec(attributeSpec)
	}
	return program, nil
}

func (s *bitmapCompileState) scalarProfile() (expression.CompileProfile, error) {
	limits := s.compiler.limits
	remaining := func(max, used int, name string) (int, error) {
		if max <= 0 {
			return 0, nil
		}
		left := max - used
		if left <= 0 {
			return 0, jsonError("$", limitCode(name), "prefilter document exceeds %s limit", name)
		}
		return left, nil
	}
	var err error
	limits.MaxNodes, err = remaining(limits.MaxNodes, s.cost.totalNodes, "nodes")
	if err != nil {
		return expression.CompileProfile{}, err
	}
	limits.MaxInstructions, err = remaining(limits.MaxInstructions, s.cost.totalInstructions, "instructions")
	if err != nil {
		return expression.CompileProfile{}, err
	}
	limits.MaxLiteralValues, err = remaining(limits.MaxLiteralValues, s.cost.scalarLiteralValues, "literal values")
	if err != nil {
		return expression.CompileProfile{}, err
	}
	limits.MaxSteps, err = remaining(limits.MaxSteps, s.cost.scalarSteps, "steps")
	if err != nil {
		return expression.CompileProfile{}, err
	}
	profile := expression.CompileProfile{
		AllowedSources: expression.CapabilitySeedAttributes | expression.CapabilitySeedFacts | expression.CapabilityTickFacts,
		Attributes:     s.compiler.contract.AttributeSpecs(),
		Facts:          s.compiler.contract.FactSpecs(),
		Limits: expression.Limits{
			MaxDepth: limits.MaxDepth, MaxChildren: limits.MaxChildren,
			MaxLiteralValues: limits.MaxLiteralValues, MaxSteps: limits.MaxSteps,
			MaxNodes: limits.MaxNodes, MaxInstructions: limits.MaxInstructions,
		},
		JSONLimits: limits,
	}
	profile.FactAllowed = func(source expression.Source, name string) bool {
		for _, spec := range profile.Facts {
			if spec.Name != name {
				continue
			}
			switch spec.Scope {
			case fact.ScopeObject:
				return source == expression.SourceSeedFacts
			case fact.ScopeTick:
				return source == expression.SourceTickFacts
			default:
				return false
			}
		}
		return false
	}
	return profile, nil
}

func limitCode(name string) string {
	switch name {
	case "nodes":
		return "NODE_LIMIT"
	case "instructions":
		return "INSTRUCTION_LIMIT"
	case "literal values":
		return "VALUE_LIMIT"
	case "steps":
		return "STEP_LIMIT"
	default:
		return "INVALID_LIMIT"
	}
}

func (s *bitmapCompileState) analyzeBitmap(id bitmapNodeID, path string, visiting, visited map[bitmapNodeID]bool) (bitmapProperties, bool, error) {
	if id == invalidBitmapNodeID || int(id) >= len(s.nodes) {
		return bitmapProperties{}, false, compileError(path, "INVALID_BITMAP", "bitmap node %d is invalid", id)
	}
	if visited[id] {
		node := s.nodes[id]
		return node.props, node.hasExclude, nil
	}
	if visiting[id] {
		return bitmapProperties{}, false, compileError(path, "INVALID_BITMAP", "bitmap expression contains a cycle")
	}
	visiting[id] = true
	defer delete(visiting, id)
	node := &s.nodes[id]
	var props bitmapProperties
	hasExclude := false
	switch node.kind {
	case bitmapKindNone:
		props = bitmapProperties{lattice: bitmapStaticNone | bitmapScopeFree}
	case bitmapKindLookupString, bitmapKindLookupUint64, bitmapKindLookupRange:
		query, ok := s.query(node.query)
		if !ok {
			return bitmapProperties{}, false, compileError(path, "INVALID_BITMAP", "bitmap query %d is invalid", node.query)
		}
		props = query.properties
	case bitmapKindExclude:
		childProps, childHasExclude, err := s.analyzeBitmap(node.value, path+".value", visiting, visited)
		if err != nil {
			return bitmapProperties{}, false, err
		}
		if childHasExclude {
			return bitmapProperties{}, false, compileError(path, "INVALID_BITMAP", "nested exclude is not legal")
		}
		if !childProps.Has(bitmapScopeFree) {
			return bitmapProperties{}, false, compileError(path, "INVALID_BITMAP", "exclude child must be scope-free")
		}
		hasExclude = true
		// EXCLUDE is a scope transform even when its child is statically none:
		// subtracting the empty set preserves the incoming scope, but cannot
		// establish one for a root by itself.
		props = bitmapProperties{lattice: bitmapNeedsScope}
	case bitmapKindAnd, bitmapKindOr:
		if len(node.children) == 0 {
			return bitmapProperties{}, false, compileError(path, "INVALID_BITMAP", "%s requires children", node.kind)
		}
		states := make([]bitmapLattice, 0, len(node.children))
		for index, childID := range node.children {
			childProps, childHasExclude, err := s.analyzeBitmap(childID, fmt.Sprintf("%s.%s[%d]", path, node.kind, index), visiting, visited)
			if err != nil {
				return bitmapProperties{}, false, err
			}
			states = append(states, childProps.lattice)
			hasExclude = hasExclude || childHasExclude
		}
		if node.kind == bitmapKindAnd {
			props = combineBitmapAnd(states)
		} else {
			props = combineBitmapOr(states)
		}
	case bitmapKindIf:
		thenProps, thenHasExclude, err := s.analyzeBitmap(node.then, path+".then", visiting, visited)
		if err != nil {
			return bitmapProperties{}, false, err
		}
		elseProps, elseHasExclude, err := s.analyzeBitmap(node.elseNode, path+".else", visiting, visited)
		if err != nil {
			return bitmapProperties{}, false, err
		}
		props = combineBitmapIf(thenProps.lattice, elseProps.lattice)
		hasExclude = thenHasExclude || elseHasExclude
	default:
		return bitmapProperties{}, false, compileError(path, "INVALID_BITMAP", "unsupported bitmap node %s", node.kind)
	}
	if !props.Valid() {
		return bitmapProperties{}, false, compileError(path, "INVALID_BITMAP", "bitmap node %s has invalid lattice state", node.kind)
	}
	node.props, node.hasExclude = props, hasExclude
	visited[id] = true
	return props, hasExclude, nil
}

func (s *bitmapCompileState) query(id bitmapQueryID) (*bitmapQuery, bool) {
	if s == nil || id == invalidBitmapQueryID || int(id) > len(s.queries) {
		return nil, false
	}
	return &s.queries[id-1], true
}

func (s *bitmapCompileState) buildRequirements() Requirements {
	out := Requirements{}
	for _, index := range s.requiredIndexes {
		out.Indexes = append(out.Indexes, index)
	}
	for _, factSpec := range s.requiredFacts {
		out.Facts = append(out.Facts, factSpec)
	}
	for _, attribute := range s.requiredAttributes {
		out.Attributes = append(out.Attributes, attribute)
	}
	sort.Slice(out.Indexes, func(i, j int) bool { return out.Indexes[i].Name < out.Indexes[j].Name })
	sort.Slice(out.Facts, func(i, j int) bool { return out.Facts[i].Name < out.Facts[j].Name })
	sort.Slice(out.Attributes, func(i, j int) bool { return out.Attributes[i].Name < out.Attributes[j].Name })
	return out
}

func staticStrings(program *expression.ScalarProgram) (bool, []string, error) {
	if program == nil || len(program.Dependencies().Facts()) != 0 || len(program.Dependencies().Attributes()) != 0 {
		return false, nil, nil
	}
	values, err := program.EvaluateStrings(nil)
	return true, values, err
}

func staticUint64s(program *expression.ScalarProgram) (bool, []uint64, error) {
	if program == nil || len(program.Dependencies().Facts()) != 0 || len(program.Dependencies().Attributes()) != 0 {
		return false, nil, nil
	}
	values, err := program.EvaluateUint64s(nil)
	return true, values, err
}

func staticInt64(program *expression.ScalarProgram) (bool, int64, error) {
	if program == nil || len(program.Dependencies().Facts()) != 0 || len(program.Dependencies().Attributes()) != 0 {
		return false, 0, nil
	}
	value, err := program.EvaluateInt64(nil)
	return true, value, err
}

func validateBitmapCollectionUpperBound(program *expression.ScalarProgram, path string, index indexSpec) error {
	if program == nil {
		return compileError(path, "INVALID_PROGRAM", "collection operand is nil")
	}
	upper, known := program.CollectionUpperBound()
	if known && upper > index.maxQueryValues {
		return compileError(path, "QUERY_KEY_CONTRACT", "query may produce up to %d keys; index %q allows %d", upper, index.name, index.maxQueryValues)
	}
	return nil
}

func adaptBitmapExpressionError(err error, prefix string) error {
	if err == nil {
		return nil
	}
	var expressionErr *expression.Error
	if errors.As(err, &expressionErr) {
		path := prefixBitmapErrorPath(prefix, string(expressionErr.Path))
		code := expressionErr.Code
		// Scalar expressions reject Bitmap roots before returning a program. At a
		// Prefilter lookup this is a dynamic result mismatch, not a second
		// scalar language error.
		if code == "ROOT_NOT_ALLOWED" && strings.HasSuffix(path, ".resultType") {
			code = "DYNAMIC_RESULT_MISMATCH"
		}
		return &Error{Phase: expressionErr.Phase, Path: path, Code: code, Err: expressionErr.Err}
	}
	return err
}

func adaptBitmapStaticExpressionError(err error, prefix string) error {
	if err == nil {
		return nil
	}
	adapted := adaptBitmapExpressionError(err, prefix)
	var prefilterErr *Error
	if errors.As(adapted, &prefilterErr) {
		copy := *prefilterErr
		copy.Phase = "compile"
		return &copy
	}
	return compileError(prefix, "STATIC_EVALUATION", "%v", adapted)
}

func prefixBitmapErrorPath(prefix, path string) string {
	if prefix == "" {
		return path
	}
	if path == "" || path == "$" || path == "root" {
		return prefix
	}
	if strings.HasPrefix(path, "$.") {
		return prefix + path[1:]
	}
	if strings.HasPrefix(path, "root.") || strings.HasPrefix(path, "root[") {
		return prefix + path[len("root"):]
	}
	if strings.HasPrefix(path, ".") {
		return prefix + path
	}
	return prefix + "." + path
}

func decodeBitmapObject(raw []byte, path string) (map[string]json.RawMessage, error) {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, jsonError(path, "NULL_NOT_ALLOWED", "object must not be null")
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil || object == nil {
		return nil, jsonError(path, "TYPE_MISMATCH", "object is required")
	}
	return object, nil
}

func bitmapRequiredField(object map[string]json.RawMessage, name, path string) (json.RawMessage, error) {
	raw, ok := object[name]
	if !ok {
		return nil, jsonError(path, "MISSING_FIELD", "%s is required", name)
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, jsonError(path, "NULL_NOT_ALLOWED", "%s must not be null", name)
	}
	return raw, nil
}

func bitmapStringField(object map[string]json.RawMessage, name, path string) (string, error) {
	raw, err := bitmapRequiredField(object, name, path)
	if err != nil {
		return "", err
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", jsonError(path, "TYPE_MISMATCH", "string is required")
	}
	return value, nil
}

func decodeBitmapUint64(raw []byte, path string, target *uint64) error {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return jsonError(path, "NULL_NOT_ALLOWED", "uint64 must not be null")
	}
	var value uint64
	if err := json.Unmarshal(raw, &value); err != nil {
		return jsonError(path, "TYPE_MISMATCH", "non-negative integer is required")
	}
	if target != nil {
		*target = value
	}
	return nil
}

func checkBitmapFields(object map[string]json.RawMessage, path string, allowed ...string) error {
	set := make(map[string]struct{}, len(allowed))
	for _, field := range allowed {
		set[field] = struct{}{}
	}
	unknown := make([]string, 0)
	for field := range object {
		if _, ok := set[field]; !ok {
			unknown = append(unknown, field)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	sort.Strings(unknown)
	return jsonError(path+"."+unknown[0], "UNKNOWN_FIELD", "unknown field %q", unknown[0])
}

func canonicalBitmapQuery(op string, index indexSpec, operand string) string {
	return fmt.Sprintf("%s(%q,%s,%q,%d,%d,%s)", op, index.name, index.kind, index.keyType,
		index.maxDocumentValues, index.maxQueryValues, operand)
}

func canonicalBitmapStrings(values []string) string {
	parts := make([]string, len(values))
	for i, value := range values {
		parts[i] = fmt.Sprintf("%q", value)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func canonicalBitmapUint64s(values []uint64) string {
	parts := make([]string, len(values))
	for i, value := range values {
		parts[i] = strconv.FormatUint(value, 10)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func canonicalBitmapLimits(limits expression.JSONLimits) string {
	return fmt.Sprintf("%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d",
		limits.MaxBytes, limits.MaxDepth, limits.MaxObjectFields, limits.MaxArrayItems,
		limits.MaxValues, limits.MaxStringBytes, limits.MaxChildren, limits.MaxLiteralValues,
		limits.MaxSteps, limits.MaxNodes, limits.MaxInstructions)
}
