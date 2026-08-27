package prefilter

import (
	"fmt"
	"sort"
	"strings"

	"matchSystem/internal/matchsystem/expression"
)

// The bitmap tree is intentionally owned by Prefilter. It is independent from
// expression's scalar compiler; scalar lookup operands are opaque programs and
// are the only values borrowed from the shared expression package.
type bitmapNodeID uint32

const invalidBitmapNodeID bitmapNodeID = 0

type bitmapQueryID uint32

const invalidBitmapQueryID bitmapQueryID = 0

type bitmapKind uint8

const (
	bitmapKindInvalid bitmapKind = iota
	bitmapKindNone
	bitmapKindAnd
	bitmapKindOr
	bitmapKindExclude
	bitmapKindIf
	bitmapKindLookupString
	bitmapKindLookupUint64
	bitmapKindLookupRange
)

func (k bitmapKind) String() string {
	switch k {
	case bitmapKindNone:
		return "none"
	case bitmapKindAnd:
		return "and"
	case bitmapKindOr:
		return "or"
	case bitmapKindExclude:
		return "exclude"
	case bitmapKindIf:
		return "if"
	case bitmapKindLookupString:
		return "lookup_string"
	case bitmapKindLookupUint64:
		return "lookup_uint64"
	case bitmapKindLookupRange:
		return "lookup_range"
	default:
		return "invalid"
	}
}

// bitmapLattice is the Prefilter execution lattice.  The flags are mutually
// constrained by Valid: a node is either scope-free or requires an incoming
// scope, and an anchor can only be scope-free.  StaticNone is an absorbing
// compile-time result that makes an otherwise scope-requiring subtree safe.
type bitmapLattice uint8

const (
	bitmapLatticeInvalid bitmapLattice = 0
	bitmapStaticNone     bitmapLattice = 1 << iota
	bitmapScopeFree
	bitmapNeedsScope
	bitmapEstablishesScope
)

func (l bitmapLattice) Valid() bool {
	if l == bitmapLatticeInvalid {
		return false
	}
	const known = bitmapStaticNone | bitmapScopeFree | bitmapNeedsScope | bitmapEstablishesScope
	if l&^known != 0 {
		return false
	}
	if l&bitmapScopeFree != 0 && l&bitmapNeedsScope != 0 {
		return false
	}
	if l&(bitmapScopeFree|bitmapNeedsScope) == 0 {
		return false
	}
	if l&bitmapStaticNone != 0 && l&bitmapScopeFree == 0 {
		return false
	}
	if l&bitmapStaticNone != 0 && l&bitmapEstablishesScope != 0 {
		return false
	}
	if l&bitmapEstablishesScope != 0 && l&bitmapScopeFree == 0 {
		return false
	}
	return true
}

func (l bitmapLattice) Has(flag bitmapLattice) bool {
	const known = bitmapStaticNone | bitmapScopeFree | bitmapNeedsScope | bitmapEstablishesScope
	return flag != bitmapLatticeInvalid && flag&^known == 0 && l&flag == flag
}

type bitmapProperties struct {
	lattice bitmapLattice
}

func (p bitmapProperties) Valid() bool { return p.lattice.Valid() }
func (p bitmapProperties) Has(flag bitmapLattice) bool {
	return p.lattice.Has(flag)
}

// bitmapNode is a closed, immutable-after-compile expression node.  Children are
// local IDs and query points to a Prefilter-owned sidecar.  Scalar operands
// never appear in the tree itself; they live in the sidecar as opaque
// expression.ScalarProgram values.
type bitmapNode struct {
	kind       bitmapKind
	children   []bitmapNodeID
	value      bitmapNodeID
	when       *expression.ScalarProgram
	then       bitmapNodeID
	elseNode   bitmapNodeID
	query      bitmapQueryID
	props      bitmapProperties
	hasExclude bool
}

type bitmapLookupKind uint8

const (
	bitmapLookupInvalid bitmapLookupKind = iota
	bitmapLookupString
	bitmapLookupUint64
	bitmapLookupRange
)

// bitmapQuery contains only physical index metadata and opaque scalar
// operands.  It deliberately has no expression instruction handle.
type bitmapQuery struct {
	kind           bitmapLookupKind
	index          indexSpec
	slot           int
	maxQueryValues int
	values         *expression.ScalarProgram
	min            *expression.ScalarProgram
	max            *expression.ScalarProgram
	staticStrings  []string
	staticUint64s  []uint64
	staticMin      int64
	staticMax      int64
	staticValues   bool
	staticRange    bool
	staticNone     bool
	canonical      string
	properties     bitmapProperties
}

func (p *Plan) query(id bitmapQueryID) (*bitmapQuery, bool) {
	if p == nil || id == invalidBitmapQueryID || int(id) > len(p.queries) {
		return nil, false
	}
	return &p.queries[id-1], true
}

type bitmapCost struct {
	scalarLiteralValues int
	scalarSteps         int

	// TotalNodes/TotalInstructions are the shared document budgets.  Bitmap
	// nodes count toward both budgets because one compile document and
	// must not allow a bitmap wrapper plus scalar subdocuments to bypass it.
	totalNodes        int
	totalInstructions int
}

func (c *bitmapCost) addBitmapNode() {
	if c == nil {
		return
	}
	c.totalNodes++
	c.totalInstructions++
}

func (c *bitmapCost) addScalar(program *expression.ScalarProgram) {
	if c == nil || program == nil {
		return
	}
	cost := program.Cost()
	c.scalarLiteralValues += cost.LiteralValues
	c.scalarSteps += cost.Steps
	c.totalNodes += cost.Nodes
	c.totalInstructions += cost.Instructions
}

func combineBitmapAnd(states []bitmapLattice) bitmapProperties {
	anyNone, anyAnchor, allScopeFree := false, false, true
	for _, state := range states {
		anyNone = anyNone || state&bitmapStaticNone != 0
		anyAnchor = anyAnchor || state&bitmapEstablishesScope != 0
		allScopeFree = allScopeFree && state&bitmapScopeFree != 0
	}
	if anyNone {
		return bitmapProperties{lattice: bitmapStaticNone | bitmapScopeFree}
	}
	if anyAnchor {
		return bitmapProperties{lattice: bitmapScopeFree | bitmapEstablishesScope}
	}
	if allScopeFree {
		return bitmapProperties{lattice: bitmapScopeFree}
	}
	return bitmapProperties{lattice: bitmapNeedsScope}
}

func combineBitmapOr(states []bitmapLattice) bitmapProperties {
	allNone, allScopeFree, anyAnchor := true, true, false
	for _, state := range states {
		allNone = allNone && state&bitmapStaticNone != 0
		allScopeFree = allScopeFree && state&bitmapScopeFree != 0
		anyAnchor = anyAnchor || state&bitmapEstablishesScope != 0
	}
	if allNone {
		return bitmapProperties{lattice: bitmapStaticNone | bitmapScopeFree}
	}
	if allScopeFree {
		state := bitmapScopeFree
		if anyAnchor {
			state |= bitmapEstablishesScope
		}
		return bitmapProperties{lattice: state}
	}
	return bitmapProperties{lattice: bitmapNeedsScope}
}

func combineBitmapIf(thenState, elseState bitmapLattice) bitmapProperties {
	if thenState&bitmapStaticNone != 0 && elseState&bitmapStaticNone != 0 {
		return bitmapProperties{lattice: bitmapStaticNone | bitmapScopeFree}
	}
	if thenState&bitmapScopeFree != 0 && elseState&bitmapScopeFree != 0 {
		state := bitmapScopeFree
		if thenState&bitmapEstablishesScope != 0 || elseState&bitmapEstablishesScope != 0 {
			state |= bitmapEstablishesScope
		}
		return bitmapProperties{lattice: state}
	}
	return bitmapProperties{lattice: bitmapNeedsScope}
}

func validateBitmapRoot(properties bitmapProperties) error {
	if !properties.Valid() {
		return compileError("$.bitmap.expr", "INVALID_BITMAP", "bitmap root state is invalid")
	}
	if properties.Has(bitmapStaticNone) {
		return nil
	}
	if !properties.Has(bitmapScopeFree) {
		return compileError("$.bitmap.expr", "INVALID_BITMAP", "bitmap root cannot start without candidate scope")
	}
	if !properties.Has(bitmapEstablishesScope) {
		return compileError("$.bitmap.expr", "INVALID_BITMAP", "bitmap root has no legal anchor")
	}
	return nil
}

func canonicalBitmapNode(p *Plan, id bitmapNodeID) string {
	if p == nil || id == invalidBitmapNodeID || int(id) >= len(p.nodes) {
		return "invalid"
	}
	node := p.nodes[id]
	child := func(childID bitmapNodeID) string { return canonicalBitmapNode(p, childID) }
	switch node.kind {
	case bitmapKindNone:
		return "none"
	case bitmapKindAnd, bitmapKindOr:
		parts := make([]string, len(node.children))
		for i, childID := range node.children {
			parts[i] = child(childID)
		}
		return node.kind.String() + "([" + strings.Join(parts, ",") + "])"
	case bitmapKindExclude:
		return "exclude(" + child(node.value) + ")"
	case bitmapKindIf:
		when := "nil"
		if node.when != nil {
			when = node.when.Canonical()
		}
		return "if(" + when + "," + child(node.then) + "," + child(node.elseNode) + ")"
	case bitmapKindLookupString, bitmapKindLookupUint64, bitmapKindLookupRange:
		if query, ok := p.query(node.query); ok {
			return query.canonical
		}
		return fmt.Sprintf("%s(invalid)", node.kind)
	default:
		return "invalid"
	}
}

// canonicalRequirements keeps requirement sorting deterministic without
// exposing any expression IR.
func canonicalRequirements(requirements Requirements) string {
	parts := make([]string, 0, len(requirements.Indexes)+len(requirements.Facts)+len(requirements.Attributes))
	for _, index := range requirements.Indexes {
		parts = append(parts, fmt.Sprintf("index:%q:%s:%s:%d:%d", index.Name, index.Type, index.KeyType, index.MaxDocumentValues, index.MaxQueryValues))
	}
	for _, fact := range requirements.Facts {
		parts = append(parts, fmt.Sprintf("fact:%q:%d:%d:%s", fact.Name, fact.Type, fact.MaxValues, fact.Scope))
	}
	for _, attribute := range requirements.Attributes {
		parts = append(parts, fmt.Sprintf("attribute:%q:%d:%d", attribute.Name, attribute.Type, attribute.MaxValues))
	}
	sort.Strings(parts)
	return strings.Join(parts, "|")
}

func requiredIndex(spec indexSpec) RequiredIndex {
	return RequiredIndex{
		Name: spec.name, Type: spec.kind, KeyType: spec.keyType,
		MaxDocumentValues: spec.maxDocumentValues, MaxQueryValues: spec.maxQueryValues,
	}
}

func lookupKindForBitmap(kind bitmapKind) bitmapLookupKind {
	switch kind {
	case bitmapKindLookupString:
		return bitmapLookupString
	case bitmapKindLookupUint64:
		return bitmapLookupUint64
	case bitmapKindLookupRange:
		return bitmapLookupRange
	default:
		return bitmapLookupInvalid
	}
}

func bitmapKindForOp(op string) bitmapKind {
	switch op {
	case "none":
		return bitmapKindNone
	case "and":
		return bitmapKindAnd
	case "or":
		return bitmapKindOr
	case "exclude":
		return bitmapKindExclude
	case "if":
		return bitmapKindIf
	case "lookup_string":
		return bitmapKindLookupString
	case "lookup_uint64":
		return bitmapKindLookupUint64
	case "lookup_range":
		return bitmapKindLookupRange
	default:
		return bitmapKindInvalid
	}
}
