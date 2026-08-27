package expression

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"

	"matchSystem/internal/matchsystem/fact"
)

// scalarKind is private on purpose. The wire operation names are translated
// directly into this closed set and no syntax-tree or IR handle escapes the
// package.
type scalarKind uint8

const (
	kindInvalid scalarKind = iota
	kindBoolLiteral
	kindBoolAnd
	kindBoolOr
	kindBoolNot
	kindInt64Literal
	kindInt64Lookup
	kindInt64Step
	kindInt64Clamp
	kindInt64Add
	kindInt64Sub
	kindInt64Min
	kindInt64Max
	kindStringsLiteral
	kindStringsLookup
	kindStringsUnion
	kindUint64sLiteral
	kindUint64sLookup
	kindUint64sUnion
	kindInt64Equal
	kindInt64NotEqual
	kindInt64Less
	kindInt64LessOrEqual
	kindInt64Greater
	kindInt64GreaterOrEqual
	kindStringsEqual
	kindStringsNotEqual
	kindStringsEmpty
	kindStringsContains
	kindStringsContainsAny
	kindStringsContainsAll
	kindStringsIntersects
	kindUint64sEqual
	kindUint64sNotEqual
	kindUint64sEmpty
	kindUint64sContains
	kindUint64sContainsAny
	kindUint64sContainsAll
	kindUint64sIntersects
)

func (k scalarKind) String() string {
	switch k {
	case kindBoolLiteral:
		return "bool-literal"
	case kindBoolAnd:
		return "bool-and"
	case kindBoolOr:
		return "bool-or"
	case kindBoolNot:
		return "bool-not"
	case kindInt64Literal:
		return "int64-literal"
	case kindInt64Lookup:
		return "int64-lookup"
	case kindInt64Step:
		return "int64-step"
	case kindInt64Clamp:
		return "int64-clamp"
	case kindInt64Add:
		return "int64-add"
	case kindInt64Sub:
		return "int64-sub"
	case kindInt64Min:
		return "int64-min"
	case kindInt64Max:
		return "int64-max"
	case kindStringsLiteral:
		return "strings-literal"
	case kindStringsLookup:
		return "strings-lookup"
	case kindStringsUnion:
		return "strings-union"
	case kindUint64sLiteral:
		return "uint64s-literal"
	case kindUint64sLookup:
		return "uint64s-lookup"
	case kindUint64sUnion:
		return "uint64s-union"
	case kindInt64Equal:
		return "int64-equal"
	case kindInt64NotEqual:
		return "int64-not-equal"
	case kindInt64Less:
		return "int64-less"
	case kindInt64LessOrEqual:
		return "int64-less-or-equal"
	case kindInt64Greater:
		return "int64-greater"
	case kindInt64GreaterOrEqual:
		return "int64-greater-or-equal"
	case kindStringsEqual:
		return "strings-equal"
	case kindStringsNotEqual:
		return "strings-not-equal"
	case kindStringsEmpty:
		return "strings-empty"
	case kindStringsContains:
		return "strings-contains"
	case kindStringsContainsAny:
		return "strings-contains-any"
	case kindStringsContainsAll:
		return "strings-contains-all"
	case kindStringsIntersects:
		return "strings-intersects"
	case kindUint64sEqual:
		return "uint64s-equal"
	case kindUint64sNotEqual:
		return "uint64s-not-equal"
	case kindUint64sEmpty:
		return "uint64s-empty"
	case kindUint64sContains:
		return "uint64s-contains"
	case kindUint64sContainsAny:
		return "uint64s-contains-any"
	case kindUint64sContainsAll:
		return "uint64s-contains-all"
	case kindUint64sIntersects:
		return "uint64s-intersects"
	default:
		return "invalid"
	}
}

type scalarStep struct {
	at    int64
	value int64
}

// scalarNode is the private, immutable-at-runtime representation. Children
// refer to indexes in the enclosing ScalarProgram and are never exposed.
type scalarNode struct {
	kind   scalarKind
	result ResultType

	children []int
	left     int
	right    int
	value    int
	minimum  int
	maximum  int
	input    int

	source Source
	name   string

	boolValue  bool
	int64Value int64
	strings    []string
	uint64s    []uint64
	steps      []scalarStep

	bound      int
	boundKnown bool
}

func newScalarNode(kind scalarKind, result ResultType) scalarNode {
	return scalarNode{kind: kind, result: result, left: -1, right: -1, value: -1, minimum: -1, maximum: -1, input: -1}
}

type scalarBuilder struct {
	profile CompileProfile
	limits  JSONLimits

	nodes []scalarNode
	deps  Dependencies
}

func (b *scalarBuilder) add(node scalarNode, path string) (int, error) {
	if b.limits.MaxNodes > 0 && len(b.nodes) >= b.limits.MaxNodes {
		return -1, jsonError(path, "NODE_LIMIT", "expression contains more than %d nodes", b.limits.MaxNodes)
	}
	b.nodes = append(b.nodes, node)
	return len(b.nodes) - 1, nil
}

func (b *scalarBuilder) addLookup(result ResultType, source Source, name, path string) (int, error) {
	if name == "" {
		return -1, compileError(path+".name", "EMPTY_NAME", "lookup name is required")
	}
	if !validSource(source) {
		return -1, compileError(path+".source", "INVALID_SOURCE", "source %d is invalid", source)
	}
	if !b.profile.AllowedSources.Allows(source) {
		return -1, compileError(path, "SOURCE_NOT_ALLOWED", "source %s is not allowed", source)
	}
	wantType, ok := scalarResultFactType(result)
	if !ok {
		return -1, compileError(path, "TYPE_MISMATCH", "reference result %s is not a scalar value", result)
	}
	node := newScalarNode(lookupKind(result), result)
	node.source, node.name = source, name
	if source == SourceSeedAttributes || source == SourceCandidateAttributes {
		spec, found := findAttribute(b.profile.Attributes, name)
		if !found {
			return -1, compileError(path+".name", "MISSING_ATTRIBUTE", "attribute %q is not registered", name)
		}
		if spec.Type != wantType {
			return -1, compileError(path+".name", "ATTRIBUTE_TYPE_MISMATCH", "attribute %q has type %d; expression requires type %d", name, spec.Type, wantType)
		}
		node.bound, node.boundKnown = collectionBound(spec.MaxValues, result)
		b.deps.addAttribute(spec)
		return b.add(node, path)
	}
	spec, found := findFact(b.profile.Facts, name)
	if !found {
		return -1, compileError(path+".name", "MISSING_FACT", "fact %q is not registered", name)
	}
	if spec.Type != wantType {
		return -1, compileError(path+".name", "FACT_TYPE_MISMATCH", "fact %q has type %d; expression requires type %d", name, spec.Type, wantType)
	}
	if b.profile.FactAllowed != nil {
		allowed, recovered := callFactPolicy(b.profile.FactAllowed, source, name)
		if recovered != nil {
			return -1, compileError(path, "FACT_POLICY_PANIC", "fact policy panicked: %v", recovered)
		}
		if !allowed {
			return -1, compileError(path, "FACT_SCOPE_NOT_ALLOWED", "fact %q is not allowed from source %s", name, source)
		}
	}
	if !factScopeAllowsSource(source, spec.Scope) {
		return -1, compileError(path, "FACT_SCOPE_MISMATCH", "fact %q scope %q is not available from source %s", name, spec.Scope, source)
	}
	node.bound, node.boundKnown = collectionBound(spec.MaxValues, result)
	b.deps.addFact(spec)
	return b.add(node, path)
}

func lookupKind(result ResultType) scalarKind {
	switch result {
	case ResultInt64:
		return kindInt64Lookup
	case ResultStrings:
		return kindStringsLookup
	case ResultUint64s:
		return kindUint64sLookup
	default:
		return kindInvalid
	}
}

func collectionBound(maxValues int, result ResultType) (int, bool) {
	if result != ResultStrings && result != ResultUint64s {
		return 0, false
	}
	return maxValues, maxValues > 0
}

func findFact(specs []fact.Spec, name string) (fact.Spec, bool) {
	for _, spec := range specs {
		if spec.Name == name {
			return spec, true
		}
	}
	return fact.Spec{}, false
}

func findAttribute(specs []AttributeSpec, name string) (AttributeSpec, bool) {
	for _, spec := range specs {
		if spec.Name == name {
			return spec, true
		}
	}
	return AttributeSpec{}, false
}

func callFactPolicy(policy func(Source, string) bool, source Source, name string) (allowed bool, recovered any) {
	defer func() { recovered = recover() }()
	return policy(source, name), nil
}

func validateProfile(profile CompileProfile) error {
	for _, item := range []struct {
		name  string
		value int
	}{
		{"maxDepth", profile.Limits.MaxDepth}, {"maxChildren", profile.Limits.MaxChildren},
		{"maxLiteralValues", profile.Limits.MaxLiteralValues}, {"maxSteps", profile.Limits.MaxSteps},
		{"maxNodes", profile.Limits.MaxNodes}, {"maxInstructions", profile.Limits.MaxInstructions},
	} {
		if item.value < 0 {
			return compileError("profile.limits."+item.name, "INVALID_LIMIT", "limit must not be negative")
		}
	}
	known := CapabilitySeedAttributes | CapabilitySeedFacts | CapabilityTickFacts | CapabilityCandidateAttributes | CapabilityCandidateFacts | CapabilityMatchFacts
	if profile.AllowedSources&^known != 0 {
		return compileError("profile.allowedSources", "INVALID_CAPABILITIES", "profile contains unknown source capability bits")
	}
	seenRoots := make(map[ResultType]struct{}, len(profile.AllowedRoots))
	for index, root := range profile.AllowedRoots {
		if !validResultType(root) {
			return compileError(fmt.Sprintf("profile.allowedRoots[%d]", index), "INVALID_RESULT", "root result type is invalid")
		}
		if _, exists := seenRoots[root]; exists {
			return compileError(fmt.Sprintf("profile.allowedRoots[%d]", index), "DUPLICATE_ROOT", "root result type is duplicated")
		}
		seenRoots[root] = struct{}{}
	}
	seenFacts := make(map[string]struct{}, len(profile.Facts))
	for index, spec := range profile.Facts {
		path := fmt.Sprintf("profile.facts[%d]", index)
		if !validFactSpec(spec) {
			return compileError(path, "INVALID_FACT", "fact %q has an invalid declaration", spec.Name)
		}
		if _, exists := seenFacts[spec.Name]; exists {
			return compileError(path, "DUPLICATE_FACT", "fact %q is duplicated", spec.Name)
		}
		seenFacts[spec.Name] = struct{}{}
	}
	seenAttributes := make(map[string]struct{}, len(profile.Attributes))
	for index, spec := range profile.Attributes {
		path := fmt.Sprintf("profile.attributes[%d]", index)
		if !validAttributeSpec(spec) {
			return compileError(path, "INVALID_ATTRIBUTE", "attribute %q has an invalid declaration", spec.Name)
		}
		if _, exists := seenAttributes[spec.Name]; exists {
			return compileError(path, "DUPLICATE_ATTRIBUTE", "attribute %q is duplicated", spec.Name)
		}
		seenAttributes[spec.Name] = struct{}{}
	}
	for name := range seenFacts {
		if _, exists := seenAttributes[name]; exists {
			return compileError("profile", "DUPLICATE_NAME", "Fact %q collides with an attribute", name)
		}
	}
	return nil
}

// ProgramCost is the stable resource summary exposed by ScalarProgram. The
// summary contains no graph handles or executable representation.
type ProgramCost struct {
	Nodes        int
	Instructions int

	MaxDepth    int
	MaxChildren int

	LiteralValues    int
	MaxLiteralValues int
	Steps            int
	MaxSteps         int
	Values           int
	StringBytes      int
}

func (c ProgramCost) Fits(limits Limits) bool {
	return limitFits(c.MaxDepth, limits.MaxDepth) &&
		limitFits(c.MaxChildren, limits.MaxChildren) &&
		limitFits(c.MaxLiteralValues, limits.MaxLiteralValues) &&
		limitFits(c.MaxSteps, limits.MaxSteps) &&
		limitFits(c.Nodes, limits.MaxNodes) &&
		limitFits(c.Instructions, limits.MaxInstructions)
}

func (c ProgramCost) Within(limits Limits) bool { return c.Fits(limits) }

func limitFits(value, limit int) bool { return limit <= 0 || value <= limit }

func buildScalarProgram(root ResultType, nodes []scalarNode, deps Dependencies) (*ScalarProgram, error) {
	if len(nodes) == 0 {
		return nil, compileError("root", "INVALID_PROGRAM", "scalar expression has no nodes")
	}
	if nodes[len(nodes)-1].result != root {
		return nil, compileError("root", "TYPE_MISMATCH", "expression produces %s but resultType declares %s", nodes[len(nodes)-1].result, root)
	}
	cost := scalarCost(nodes)
	if root == ResultStrings || root == ResultUint64s {
		// A collection root always has a computable bound for literals and
		// unions of bounded children. References carry their contract bound.
		bound, known := collectionUpperBound(nodes, len(nodes)-1, root, make(map[int]bool))
		return &ScalarProgram{
			nodes: nodes, root: len(nodes) - 1, result: root, deps: deps,
			cost: cost, upperBound: bound, upperBoundKnown: known,
			canonical: canonicalProgram(root, nodes, len(nodes)-1, deps),
		}, nil
	}
	return &ScalarProgram{
		nodes: nodes, root: len(nodes) - 1, result: root, deps: deps,
		cost: cost, canonical: canonicalProgram(root, nodes, len(nodes)-1, deps),
	}, nil
}

func scalarCost(nodes []scalarNode) ProgramCost {
	cost := ProgramCost{Nodes: len(nodes), Instructions: len(nodes)}
	for _, node := range nodes {
		if count := scalarChildCount(node); count > cost.MaxChildren {
			cost.MaxChildren = count
		}
		switch node.kind {
		case kindStringsLiteral:
			count := len(node.strings)
			cost.LiteralValues += count
			cost.Values += count
			if count > cost.MaxLiteralValues {
				cost.MaxLiteralValues = count
			}
			for _, value := range node.strings {
				cost.StringBytes += len(value)
			}
		case kindUint64sLiteral:
			count := len(node.uint64s)
			cost.LiteralValues += count
			cost.Values += count
			if count > cost.MaxLiteralValues {
				cost.MaxLiteralValues = count
			}
		case kindInt64Step:
			count := len(node.steps)
			cost.Steps += count
			if count > cost.MaxSteps {
				cost.MaxSteps = count
			}
		}
	}
	cost.MaxDepth = scalarDepth(nodes, len(nodes)-1, make(map[int]bool))
	return cost
}

func scalarChildCount(node scalarNode) int {
	if len(node.children) != 0 {
		return len(node.children)
	}
	switch node.kind {
	case kindBoolNot, kindInt64Step, kindStringsEmpty, kindStringsContains, kindUint64sEmpty, kindUint64sContains:
		return 1
	case kindInt64Clamp:
		return 3
	case kindInt64Add, kindInt64Sub, kindInt64Min, kindInt64Max,
		kindInt64Equal, kindInt64NotEqual, kindInt64Less, kindInt64LessOrEqual, kindInt64Greater, kindInt64GreaterOrEqual,
		kindStringsEqual, kindStringsNotEqual, kindStringsContainsAny, kindStringsContainsAll, kindStringsIntersects,
		kindUint64sEqual, kindUint64sNotEqual, kindUint64sContainsAny, kindUint64sContainsAll, kindUint64sIntersects:
		return 2
	default:
		return 0
	}
}

func scalarChildren(node scalarNode) []int {
	if len(node.children) != 0 {
		return node.children
	}
	result := make([]int, 0, scalarChildCount(node))
	switch node.kind {
	case kindBoolNot, kindStringsEmpty, kindStringsContains, kindUint64sEmpty, kindUint64sContains:
		result = append(result, node.value)
	case kindInt64Step:
		result = append(result, node.input)
	case kindInt64Clamp:
		result = append(result, node.value, node.minimum, node.maximum)
	case kindInt64Add, kindInt64Sub, kindInt64Min, kindInt64Max,
		kindInt64Equal, kindInt64NotEqual, kindInt64Less, kindInt64LessOrEqual, kindInt64Greater, kindInt64GreaterOrEqual,
		kindStringsEqual, kindStringsNotEqual, kindStringsContainsAny, kindStringsContainsAll, kindStringsIntersects,
		kindUint64sEqual, kindUint64sNotEqual, kindUint64sContainsAny, kindUint64sContainsAll, kindUint64sIntersects:
		result = append(result, node.left, node.right)
	}
	return result
}

func scalarDepth(nodes []scalarNode, id int, visiting map[int]bool) int {
	if id < 0 || id >= len(nodes) || visiting[id] {
		return 0
	}
	visiting[id] = true
	maxChild := 0
	for _, child := range scalarChildren(nodes[id]) {
		if depth := scalarDepth(nodes, child, visiting); depth > maxChild {
			maxChild = depth
		}
	}
	delete(visiting, id)
	return maxChild + 1
}

func collectionUpperBound(nodes []scalarNode, id int, result ResultType, visiting map[int]bool) (int, bool) {
	if id < 0 || id >= len(nodes) || visiting[id] {
		return 0, false
	}
	node := nodes[id]
	if node.result != result {
		return 0, false
	}
	switch node.kind {
	case kindStringsLiteral:
		return len(uniqueStrings(node.strings)), true
	case kindUint64sLiteral:
		return len(uniqueUint64s(node.uint64s)), true
	case kindStringsLookup, kindUint64sLookup:
		return node.bound, node.boundKnown
	case kindStringsUnion, kindUint64sUnion:
		visiting[id] = true
		defer delete(visiting, id)
		total := 0
		for _, child := range node.children {
			part, known := collectionUpperBound(nodes, child, result, visiting)
			if !known || part < 0 || total > math.MaxInt-part {
				return 0, false
			}
			total += part
		}
		return total, len(node.children) != 0
	default:
		return 0, false
	}
}

func canonicalProgram(root ResultType, nodes []scalarNode, rootID int, deps Dependencies) string {
	data := []byte{'E', '2'}
	data = appendCanonicalString(data, root.String())
	data = appendCanonicalBytes(data, canonicalNode(nodes, rootID, make(map[int]bool)))
	data = appendCanonicalBytes(data, canonicalDependencies(deps))
	return hex.EncodeToString(data)
}

func canonicalNode(nodes []scalarNode, id int, visiting map[int]bool) []byte {
	if id < 0 || id >= len(nodes) || visiting[id] {
		return appendCanonicalString(nil, "invalid-cycle")
	}
	visiting[id] = true
	defer delete(visiting, id)
	node := nodes[id]
	data := []byte{'N'}
	data = appendCanonicalString(data, node.result.String())
	data = appendCanonicalString(data, node.kind.String())
	// Only variadic nodes carry their children in the list field. Fixed-arity
	// nodes encode their operands below, matching the canonical wire identity
	// used before the private representation was introduced.
	children := node.children
	childBytes := make([][]byte, 0, len(children))
	for _, child := range children {
		childBytes = append(childBytes, canonicalNode(nodes, child, visiting))
	}
	// Child order is part of the expression identity. Bool conjunction and
	// disjunction short-circuit, so reordering changes both observable result
	// and which lookup error is returned. Collection unions are also evaluated
	// left-to-right and must retain the first failing child/path; literals are
	// normalized separately because their values are an unordered set.
	data = appendCanonicalList(data, childBytes)
	child := func(childID int) { data = appendCanonicalBytes(data, canonicalNode(nodes, childID, visiting)) }
	switch node.kind {
	case kindBoolLiteral:
		if node.boolValue {
			data = append(data, 1)
		} else {
			data = append(data, 0)
		}
	case kindBoolNot:
		child(node.value)
	case kindInt64Literal:
		data = appendCanonicalInt64(data, node.int64Value)
	case kindInt64Lookup, kindStringsLookup, kindUint64sLookup:
		data = append(data, byte(node.source))
		data = appendCanonicalString(data, node.name)
	case kindInt64Step:
		child(node.input)
		data = appendCanonicalUint(data, uint64(len(node.steps)))
		for _, step := range node.steps {
			data = appendCanonicalInt64(data, step.at)
			data = appendCanonicalInt64(data, step.value)
		}
	case kindInt64Clamp:
		child(node.value)
		child(node.minimum)
		child(node.maximum)
	case kindInt64Add, kindInt64Sub, kindInt64Min, kindInt64Max,
		kindInt64Equal, kindInt64NotEqual, kindInt64Less, kindInt64LessOrEqual, kindInt64Greater, kindInt64GreaterOrEqual,
		kindStringsEqual, kindStringsNotEqual, kindStringsContainsAny, kindStringsContainsAll, kindStringsIntersects,
		kindUint64sEqual, kindUint64sNotEqual, kindUint64sContainsAny, kindUint64sContainsAll, kindUint64sIntersects:
		child(node.left)
		child(node.right)
	case kindStringsLiteral:
		values := uniqueStrings(node.strings)
		data = appendCanonicalUint(data, uint64(len(values)))
		for _, value := range values {
			data = appendCanonicalString(data, value)
		}
	case kindUint64sLiteral:
		values := uniqueUint64s(node.uint64s)
		data = appendCanonicalUint(data, uint64(len(values)))
		for _, value := range values {
			data = appendCanonicalUint(data, value)
		}
	case kindStringsEmpty, kindStringsContains:
		child(node.value)
		data = appendCanonicalString(data, node.name)
	case kindUint64sEmpty, kindUint64sContains:
		child(node.value)
		values := uniqueUint64s(node.uint64s)
		data = appendCanonicalUint(data, uint64(len(values)))
		for _, value := range values {
			data = appendCanonicalUint(data, value)
		}
	}
	return data
}

func canonicalDependencies(deps Dependencies) []byte {
	data := []byte{'D'}
	facts := deps.Facts()
	data = appendCanonicalUint(data, uint64(len(facts)))
	for _, spec := range facts {
		data = appendCanonicalString(data, spec.Name)
		data = appendCanonicalUint(data, uint64(spec.Type))
		data = appendCanonicalUint(data, uint64(spec.MaxValues))
		data = appendCanonicalString(data, string(spec.Scope))
	}
	attributes := deps.Attributes()
	data = appendCanonicalUint(data, uint64(len(attributes)))
	for _, spec := range attributes {
		data = appendCanonicalString(data, spec.Name)
		data = appendCanonicalUint(data, uint64(spec.Type))
		data = appendCanonicalUint(data, uint64(spec.MaxValues))
	}
	return data
}

func appendCanonicalUint(dst []byte, value uint64) []byte {
	var buf [10]byte
	n := binary.PutUvarint(buf[:], value)
	return append(dst, buf[:n]...)
}

func appendCanonicalInt64(dst []byte, value int64) []byte {
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], uint64(value))
	return append(dst, buf[:]...)
}

func appendCanonicalString(dst []byte, value string) []byte {
	dst = appendCanonicalUint(dst, uint64(len(value)))
	return append(dst, value...)
}

func appendCanonicalBytes(dst, value []byte) []byte {
	dst = appendCanonicalUint(dst, uint64(len(value)))
	return append(dst, value...)
}

func appendCanonicalList(dst []byte, values [][]byte) []byte {
	dst = appendCanonicalUint(dst, uint64(len(values)))
	for _, value := range values {
		dst = appendCanonicalBytes(dst, value)
	}
	return dst
}
