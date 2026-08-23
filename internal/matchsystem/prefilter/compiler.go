package prefilter

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"reflect"
	"sort"
	"strings"
)

type Config struct {
	Indexes                []IndexSpec
	Facts                  []FactSpec
	Root                   Expr
	ContainsProbeThreshold uint64
}

type Fingerprint string

type Plan struct {
	fingerprint            Fingerprint
	requirements           Requirements
	root                   compiledNode
	indexSpecs             []indexSpec
	containsProbeThreshold uint64
}

func (p *Plan) Fingerprint() Fingerprint {
	if p == nil {
		return ""
	}
	return p.fingerprint
}
func (p *Plan) Requirements() Requirements {
	if p == nil {
		return Requirements{}
	}
	return cloneRequirements(p.requirements)
}

type compileContext struct {
	indexesByName   map[string]indexSpec
	slotsByName     map[string]int
	factsByName     map[string]FactSpec
	requiredIndexes map[string]RequiredIndex
	requiredFacts   map[string]FactSpec
	visiting        map[Expr]bool
}

type compiledNode interface {
	canonical() string
	canAnchor() bool
	canExecuteWithoutScope() bool
	pathName() string
}

type lookupNode struct {
	path  string
	query compiledIndexQuery
}
type andNode struct {
	path     string
	children []compiledNode
}
type orNode struct {
	path     string
	children []compiledNode
}
type excludeNode struct {
	path  string
	child compiledNode
}
type ifNode struct {
	path               string
	condition          Condition
	thenExpr, elseExpr compiledNode
}
type noneNode struct{ path string }

func (n *lookupNode) pathName() string           { return n.path }
func (n *andNode) pathName() string              { return n.path }
func (n *orNode) pathName() string               { return n.path }
func (n *excludeNode) pathName() string          { return n.path }
func (n *ifNode) pathName() string               { return n.path }
func (n *noneNode) pathName() string             { return n.path }
func (*lookupNode) canAnchor() bool              { return true }
func (*lookupNode) canExecuteWithoutScope() bool { return true }
func (n *andNode) canAnchor() bool {
	for _, child := range n.children {
		if child.canAnchor() {
			return true
		}
	}
	return false
}
func (n *andNode) canExecuteWithoutScope() bool { return n.canAnchor() || isStaticallyNone(n) }
func (n *orNode) canAnchor() bool {
	if !n.canExecuteWithoutScope() {
		return false
	}
	for _, child := range n.children {
		if child.canAnchor() {
			return true
		}
	}
	return false
}
func (n *orNode) canExecuteWithoutScope() bool {
	for _, child := range n.children {
		if !child.canExecuteWithoutScope() {
			return false
		}
	}
	return true
}
func (*excludeNode) canAnchor() bool              { return false }
func (*excludeNode) canExecuteWithoutScope() bool { return false }
func (n *ifNode) canAnchor() bool {
	return n.canExecuteWithoutScope() && (n.thenExpr.canAnchor() || n.elseExpr.canAnchor())
}
func (n *ifNode) canExecuteWithoutScope() bool {
	return n.thenExpr.canExecuteWithoutScope() && n.elseExpr.canExecuteWithoutScope()
}
func (*noneNode) canAnchor() bool              { return false }
func (*noneNode) canExecuteWithoutScope() bool { return true }

func (n *lookupNode) canonical() string  { return "lookup(" + n.query.canonical() + ")" }
func (n *andNode) canonical() string     { return canonicalChildren("and", n.children) }
func (n *orNode) canonical() string      { return canonicalChildren("or", n.children) }
func (n *excludeNode) canonical() string { return "exclude(" + n.child.canonical() + ")" }
func (n *ifNode) canonical() string {
	return "if(" + n.condition.canonicalCondition() + "," + n.thenExpr.canonical() + "," + n.elseExpr.canonical() + ")"
}
func (*noneNode) canonical() string { return "none" }

func canonicalChildren(kind string, children []compiledNode) string {
	parts := make([]string, len(children))
	for i, child := range children {
		parts[i] = child.canonical()
	}
	sort.Strings(parts)
	return kind + "(" + strings.Join(parts, ",") + ")"
}

func isStaticallyNone(node compiledNode) bool {
	switch n := node.(type) {
	case *noneNode:
		return true
	case *andNode:
		for _, child := range n.children {
			if isStaticallyNone(child) {
				return true
			}
		}
		return false
	case *orNode:
		for _, child := range n.children {
			if !isStaticallyNone(child) {
				return false
			}
		}
		return true
	case *ifNode:
		return isStaticallyNone(n.thenExpr) && isStaticallyNone(n.elseExpr)
	default:
		return false
	}
}

func Compile(config Config) (*Plan, error) {
	if config.Root == nil || isNilInterface(config.Root) {
		return nil, compileError("plan.root", "MISSING_ROOT", "root expression is required")
	}
	if config.ContainsProbeThreshold == 0 {
		config.ContainsProbeThreshold = 4096
	}
	ctx := &compileContext{
		indexesByName: make(map[string]indexSpec), slotsByName: make(map[string]int), factsByName: make(map[string]FactSpec),
		requiredIndexes: make(map[string]RequiredIndex), requiredFacts: make(map[string]FactSpec), visiting: make(map[Expr]bool),
	}
	indexSpecs := make([]indexSpec, 0, len(config.Indexes))
	for i, definition := range config.Indexes {
		if definition == nil || isNilInterface(definition) {
			return nil, compileError(fmt.Sprintf("indexes[%d]", i), "NIL_INDEX", "index factory is nil")
		}
		spec := definition.indexSpec()
		path := fmt.Sprintf("indexes[%d]", i)
		if spec.name == "" || spec.field == "" {
			return nil, compileError(path, "INVALID_INDEX", "index name and field are required")
		}
		if _, exists := ctx.indexesByName[spec.name]; exists {
			return nil, compileError(path, "DUPLICATE_INDEX", "index %q is duplicated", spec.name)
		}
		if spec.kind == IndexTypeMultiValue && (spec.maxDocumentValues <= 0 || spec.maxQueryValues <= 0) {
			return nil, compileError(path, "INVALID_KEY_LIMIT", "multi-value limits must be positive")
		}
		if spec.kind == IndexTypeMultiValue && spec.keyType != KeyTypeString && spec.keyType != KeyTypeUint64 {
			return nil, compileError(path, "INVALID_KEY_TYPE", "multi-value index %q has unsupported key type %q", spec.name, spec.keyType)
		}
		ctx.indexesByName[spec.name] = spec
		ctx.slotsByName[spec.name] = len(indexSpecs)
		indexSpecs = append(indexSpecs, spec)
	}
	for i, fact := range config.Facts {
		path := fmt.Sprintf("facts[%d]", i)
		if fact.Name == "" {
			return nil, compileError(path, "INVALID_FACT", "fact name is required")
		}
		if fact.Type != FactTypeStrings && fact.Type != FactTypeUint64s && fact.Type != FactTypeInt64 {
			return nil, compileError(path, "INVALID_FACT", "fact %q has unknown kind", fact.Name)
		}
		if (fact.Type == FactTypeStrings || fact.Type == FactTypeUint64s) && fact.MaxValues <= 0 {
			return nil, compileError(path, "INVALID_FACT_LIMIT", "multi-value fact %q requires a positive MaxValues", fact.Name)
		}
		if _, exists := ctx.factsByName[fact.Name]; exists {
			return nil, compileError(path, "DUPLICATE_FACT", "fact %q is duplicated", fact.Name)
		}
		ctx.factsByName[fact.Name] = fact
	}
	root, err := ctx.compileExpr(config.Root, false, "plan.root")
	if err != nil {
		return nil, err
	}
	requirements := ctx.requirements()
	canonical := root.canonical() + "|" + canonicalRequirements(requirements) + fmt.Sprintf("|probe=%d", config.ContainsProbeThreshold)
	hash := sha256.Sum256([]byte(canonical))
	return &Plan{fingerprint: Fingerprint(hex.EncodeToString(hash[:])), requirements: requirements, root: root, indexSpecs: indexSpecs, containsProbeThreshold: config.ContainsProbeThreshold}, nil
}

func isNilInterface(value any) bool {
	v := reflect.ValueOf(value)
	return v.Kind() == reflect.Pointer && v.IsNil()
}

func (ctx *compileContext) compileExpr(expr Expr, scope bool, path string) (compiledNode, error) {
	if expr == nil || isNilInterface(expr) {
		return nil, compileError(path, "NIL_NODE", "candidate expression is nil")
	}
	if ctx.visiting[expr] {
		return nil, compileError(path, "CYCLE", "prefilter expression contains a cycle")
	}
	ctx.visiting[expr] = true
	defer delete(ctx.visiting, expr)
	switch node := expr.(type) {
	case *lookupExpr:
		query, err := ctx.compileQuery(node.query, path+".lookup")
		if err != nil {
			return nil, err
		}
		return &lookupNode{path: path, query: query}, nil
	case *noneExpr:
		return &noneNode{path: path}, nil
	case *excludeExpr:
		if !scope {
			return nil, compileError(path, "EXCLUDE_REQUIRES_SCOPE", "EXCLUDE requires a positive candidate scope")
		}
		child, err := ctx.compileExpr(node.child, false, path+".exclude")
		if err != nil {
			return nil, err
		}
		return &excludeNode{path: path, child: child}, nil
	case *andExpr:
		if len(node.children) == 0 {
			return nil, compileError(path, "EMPTY_AND", "And requires at least one child")
		}
		anchor := -1
		if !scope {
			for i, child := range node.children {
				if sourceCanAnchor(child) {
					anchor = i
					break
				}
			}
		}
		children := make([]compiledNode, len(node.children))
		for i, childExpr := range node.children {
			childScope := scope || anchor >= 0
			if i == anchor {
				childScope = false
			}
			child, err := ctx.compileExpr(childExpr, childScope, fmt.Sprintf("%s.and[%d]", path, i))
			if err != nil {
				return nil, err
			}
			children[i] = child
		}
		return &andNode{path: path, children: children}, nil
	case *orExpr:
		if len(node.children) == 0 {
			return nil, compileError(path, "EMPTY_OR", "Or requires at least one child")
		}
		children := make([]compiledNode, len(node.children))
		for i, childExpr := range node.children {
			child, err := ctx.compileExpr(childExpr, scope, fmt.Sprintf("%s.or[%d]", path, i))
			if err != nil {
				return nil, err
			}
			children[i] = child
		}
		return &orNode{path: path, children: children}, nil
	case *ifExpr:
		if node.condition == nil || isNilInterface(node.condition) {
			return nil, compileError(path+".if.when", "MISSING_CONDITION", "If condition is required")
		}
		if err := node.condition.validateCondition(ctx, path+".if.when"); err != nil {
			return nil, err
		}
		thenExpr, err := ctx.compileExpr(node.thenExpr, scope, path+".if.then")
		if err != nil {
			return nil, err
		}
		elseExpr, err := ctx.compileExpr(node.elseExpr, scope, path+".if.else")
		if err != nil {
			return nil, err
		}
		return &ifNode{path: path, condition: node.condition, thenExpr: thenExpr, elseExpr: elseExpr}, nil
	default:
		return nil, compileError(path, "UNKNOWN_NODE", "unsupported candidate expression %T", expr)
	}
}

func sourceCanAnchor(expr Expr) bool {
	return sourceCanAnchorVisit(expr, make(map[Expr]bool))
}

func sourceCanAnchorVisit(expr Expr, visiting map[Expr]bool) bool {
	if expr == nil || isNilInterface(expr) {
		return false
	}
	if visiting[expr] {
		return false
	}
	visiting[expr] = true
	defer delete(visiting, expr)
	switch node := expr.(type) {
	case *lookupExpr:
		return true
	case *andExpr:
		for _, child := range node.children {
			if sourceCanAnchorVisit(child, visiting) {
				return true
			}
		}
		return false
	case *orExpr:
		for _, child := range node.children {
			if !sourceCanRunWithoutScopeVisit(child, make(map[Expr]bool)) {
				return false
			}
		}
		for _, child := range node.children {
			if sourceCanAnchorVisit(child, visiting) {
				return true
			}
		}
		return false
	case *ifExpr:
		thenAnchor := sourceCanAnchorVisit(node.thenExpr, visiting)
		elseAnchor := sourceCanAnchorVisit(node.elseExpr, visiting)
		return sourceCanRunWithoutScopeVisit(node.thenExpr, make(map[Expr]bool)) &&
			sourceCanRunWithoutScopeVisit(node.elseExpr, make(map[Expr]bool)) &&
			(thenAnchor || elseAnchor)
	default:
		return false
	}
}

func sourceCanRunWithoutScopeVisit(expr Expr, visiting map[Expr]bool) bool {
	if expr == nil || isNilInterface(expr) || visiting[expr] {
		return false
	}
	visiting[expr] = true
	defer delete(visiting, expr)
	switch node := expr.(type) {
	case *lookupExpr, *noneExpr:
		return true
	case *excludeExpr:
		return false
	case *andExpr:
		return sourceCanAnchorVisit(node, make(map[Expr]bool)) || sourceIsStaticallyNone(node)
	case *orExpr:
		for _, child := range node.children {
			if !sourceCanRunWithoutScopeVisit(child, visiting) {
				return false
			}
		}
		return true
	case *ifExpr:
		return sourceCanRunWithoutScopeVisit(node.thenExpr, visiting) && sourceCanRunWithoutScopeVisit(node.elseExpr, visiting)
	default:
		return false
	}
}

func sourceIsStaticallyNone(expr Expr) bool {
	return sourceIsStaticallyNoneVisit(expr, make(map[Expr]bool))
}

func sourceIsStaticallyNoneVisit(expr Expr, visiting map[Expr]bool) bool {
	if expr == nil || isNilInterface(expr) || visiting[expr] {
		return false
	}
	visiting[expr] = true
	defer delete(visiting, expr)
	switch node := expr.(type) {
	case *noneExpr:
		return true
	case *andExpr:
		for _, child := range node.children {
			if sourceIsStaticallyNoneVisit(child, visiting) {
				return true
			}
		}
		return false
	case *orExpr:
		if len(node.children) == 0 {
			return false
		}
		for _, child := range node.children {
			if !sourceIsStaticallyNoneVisit(child, visiting) {
				return false
			}
		}
		return true
	case *ifExpr:
		return sourceIsStaticallyNoneVisit(node.thenExpr, visiting) && sourceIsStaticallyNoneVisit(node.elseExpr, visiting)
	default:
		return false
	}
}

func (ctx *compileContext) compileQuery(query IndexQuery, path string) (compiledIndexQuery, error) {
	if query == nil || isNilInterface(query) {
		return nil, compileError(path, "MISSING_QUERY", "Lookup query is required")
	}
	switch q := query.(type) {
	case StringQuery:
		spec, slot, err := ctx.resolveIndex(q.Index, IndexTypeMultiValue, path)
		if err != nil {
			return nil, err
		}
		if spec.keyType != KeyTypeString {
			return nil, compileError(path+".index", "QUERY_KEY_TYPE_MISMATCH", "index %q uses %q keys; StringQuery requires string keys", q.Index, spec.keyType)
		}
		if q.Values == nil || isNilInterface(q.Values) {
			return nil, compileError(path+".values", "MISSING_VALUE", "multi-value values are required")
		}
		if err := q.Values.validateStrings(ctx, path+".values"); err != nil {
			return nil, err
		}
		if count, known := declaredStringValueCount(q.Values, ctx.factsByName); known && count > spec.maxQueryValues {
			return nil, compileError(path+".values", "QUERY_KEY_CONTRACT", "declared values may produce %d keys; index %q allows %d", count, q.Index, spec.maxQueryValues)
		}
		return &compiledStringQuery{slot: slot, index: q.Index, maxKeys: spec.maxQueryValues, values: q.Values}, nil
	case Uint64Query:
		spec, slot, err := ctx.resolveIndex(q.Index, IndexTypeMultiValue, path)
		if err != nil {
			return nil, err
		}
		if spec.keyType != KeyTypeUint64 {
			return nil, compileError(path+".index", "QUERY_KEY_TYPE_MISMATCH", "index %q uses %q keys; Uint64Query requires uint64 keys", q.Index, spec.keyType)
		}
		if q.Values == nil || isNilInterface(q.Values) {
			return nil, compileError(path+".values", "MISSING_VALUE", "uint64 multi-value values are required")
		}
		if err := q.Values.validateUint64s(ctx, path+".values"); err != nil {
			return nil, err
		}
		if count, known := declaredUint64ValueCount(q.Values, ctx.factsByName); known && count > spec.maxQueryValues {
			return nil, compileError(path+".values", "QUERY_KEY_CONTRACT", "declared values may produce %d keys; index %q allows %d", count, q.Index, spec.maxQueryValues)
		}
		return &compiledUint64Query{slot: slot, index: q.Index, maxKeys: spec.maxQueryValues, values: q.Values}, nil
	case Int64RangeQuery:
		_, slot, err := ctx.resolveIndex(q.Index, IndexTypeInt64Range, path)
		if err != nil {
			return nil, err
		}
		if q.Min == nil || q.Max == nil || isNilInterface(q.Min) || isNilInterface(q.Max) {
			return nil, compileError(path, "MISSING_RANGE", "int64 range Min and Max are required")
		}
		if err := q.Min.validateInt64(ctx, path+".min"); err != nil {
			return nil, err
		}
		if err := q.Max.validateInt64(ctx, path+".max"); err != nil {
			return nil, err
		}
		return &compiledInt64RangeQuery{slot: slot, index: q.Index, min: q.Min, max: q.Max}, nil
	default:
		return nil, compileError(path, "UNKNOWN_QUERY", "unsupported index query %T", query)
	}
}

func (ctx *compileContext) resolveIndex(name string, kind IndexType, path string) (indexSpec, int, error) {
	spec, ok := ctx.indexesByName[name]
	if !ok {
		return indexSpec{}, 0, compileError(path+".index", "MISSING_INDEX", "index %q is not registered", name)
	}
	if spec.kind != kind {
		return indexSpec{}, 0, compileError(path+".index", "QUERY_INDEX_MISMATCH", "index %q has kind %q, query requires %q", name, spec.kind, kind)
	}
	ctx.requiredIndexes[name] = RequiredIndex{Name: spec.name, Field: spec.field, Type: spec.kind, KeyType: spec.keyType, MaxDocumentValues: spec.maxDocumentValues, MaxQueryValues: spec.maxQueryValues}
	return spec, ctx.slotsByName[name], nil
}

func (ctx *compileContext) requirements() Requirements {
	requirements := Requirements{}
	for _, req := range ctx.requiredIndexes {
		requirements.Indexes = append(requirements.Indexes, req)
	}
	for _, fact := range ctx.requiredFacts {
		requirements.Facts = append(requirements.Facts, fact)
	}
	sort.Slice(requirements.Indexes, func(i, j int) bool { return requirements.Indexes[i].Name < requirements.Indexes[j].Name })
	sort.Slice(requirements.Facts, func(i, j int) bool { return requirements.Facts[i].Name < requirements.Facts[j].Name })
	return requirements
}

func canonicalRequirements(requirements Requirements) string {
	parts := make([]string, 0, len(requirements.Indexes)+len(requirements.Facts))
	for _, index := range requirements.Indexes {
		parts = append(parts, fmt.Sprintf("index:%s:%s:%s:%s:%d:%d", index.Name, index.Type, index.KeyType, index.Field, index.MaxDocumentValues, index.MaxQueryValues))
	}
	for _, fact := range requirements.Facts {
		parts = append(parts, fmt.Sprintf("fact:%s:%d:%d", fact.Name, fact.Type, fact.MaxValues))
	}
	return strings.Join(parts, "|")
}

func cloneRequirements(in Requirements) Requirements {
	return Requirements{Indexes: append([]RequiredIndex(nil), in.Indexes...), Facts: append([]FactSpec(nil), in.Facts...)}
}
