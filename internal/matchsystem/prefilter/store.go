package prefilter

import (
	"fmt"

	"github.com/RoaringBitmap/roaring/v2"

	"matchSystem/internal/common"
	"matchSystem/internal/matchsystem/expression"
	"matchSystem/internal/matchsystem/fact"
)

// IndexStore owns active DocIDs and all physical indexes for one immutable
// Prefilter plan. It is intentionally not goroutine-safe; its owning
// LogicalNode serializes access.
type IndexStore struct {
	plan    *Plan
	indexes []runtimeIndex
	active  *roaring.Bitmap
	// seedSnapshots owns the Contract-validated attributes used by expression
	// evaluation. The caller's Ticket may be mutated or discarded after Add;
	// this map is the only seed object visible to a TickSession.
	seedSnapshots map[uint32]*common.Ticket
}

func New(plan *Plan) (*IndexStore, error) {
	if plan == nil {
		return nil, fmt.Errorf("prefilter plan is nil")
	}
	indexes := make([]runtimeIndex, len(plan.indexSpecs))
	for slot, spec := range plan.indexSpecs {
		indexes[slot] = newIndex(spec)
	}
	return &IndexStore{plan: plan, indexes: indexes, active: roaring.New(), seedSnapshots: make(map[uint32]*common.Ticket)}, nil
}

func (s *IndexStore) Add(docID uint32, ticket *common.Ticket) error {
	if docID == 0 {
		return fmt.Errorf("candidate document DocID must be non-zero")
	}
	if ticket == nil {
		return fmt.Errorf("candidate ticket is nil")
	}
	if s.active.Contains(docID) {
		return fmt.Errorf("candidate document DocID %d already exists", docID)
	}
	if err := s.plan.attributeValidator.ValidateTicket("attributes", ticket); err != nil {
		return adaptContractError(err)
	}
	// Clone immediately after contract validation. Indexes and expression
	// lookups both consume this owned snapshot, so no caller-owned map/slice can
	// change the result of a later TickSession.
	snapshot := common.CloneTicket(ticket)
	for _, index := range s.indexes {
		if err := index.validate(snapshot); err != nil {
			return err
		}
	}
	for _, index := range s.indexes {
		index.add(docID, snapshot)
	}
	s.active.Add(docID)
	s.seedSnapshots[docID] = snapshot
	return nil
}

func (s *IndexStore) Remove(docID uint32) bool {
	if !s.active.Contains(docID) {
		return false
	}
	for _, index := range s.indexes {
		index.remove(docID)
	}
	s.active.Remove(docID)
	delete(s.seedSnapshots, docID)
	return true
}

func (s *IndexStore) Len() uint64 {
	if s == nil || s.active == nil {
		return 0
	}
	return s.active.GetCardinality()
}

// BeginTick prepares mutable range directories and borrows one immutable Fact
// layer for the returned session.
func (s *IndexStore) BeginTick(tickFacts Facts) (*TickSession, error) {
	if s == nil {
		return &TickSession{}, nil
	}
	factNames, err := s.plan.factValidator.ValidateLayer("facts.tick", tickFacts, fact.ScopeTick)
	if err != nil {
		return nil, adaptFactError(err)
	}
	for _, index := range s.indexes {
		index.prepare()
	}
	return &TickSession{store: s, tickFacts: tickFacts, tickFactNames: factNames}, nil
}

// TickSession borrows Tick-level values shared by a sequence of seed
// evaluations. It is single-goroutine and must not outlive the store mutation
// barrier documented by BeginTick.
type TickSession struct {
	store         *IndexStore
	tickFacts     Facts
	tickFactNames fact.NameSet
	resolver      prefilterLookup
}

type Stats struct {
	LookupCalls   uint64
	ContainsCalls uint64
	AndCalls      uint64
	OrCalls       uint64
	SubtractCalls uint64
}

func (s *TickSession) Candidates(seedDocID uint32, seed *common.Ticket, seedFacts Facts) (*DocSet, error) {
	result, _, err := s.CandidatesWithStats(seedDocID, seed, seedFacts)
	return result, err
}

func (s *TickSession) CandidatesWithStats(seedDocID uint32, seed *common.Ticket, seedFacts Facts) (*DocSet, Stats, error) {
	var stats Stats
	if s == nil || s.store == nil || s.store.plan == nil || s.store.plan.root == invalidBitmapNodeID {
		return nil, stats, evaluationError("root", "INVALID_TICK_SESSION", "tick session is not initialized")
	}
	if seed == nil {
		return nil, stats, evaluationError("seed", "NIL_TICKET", "seed ticket is nil")
	}
	if seedDocID == 0 || !s.store.active.Contains(seedDocID) {
		return nil, stats, evaluationError("seed", "INACTIVE_SEED", "seed DocID %d is not active", seedDocID)
	}
	snapshot := s.store.seedSnapshots[seedDocID]
	if snapshot == nil {
		return nil, stats, evaluationError("seed", "MISSING_SEED_SNAPSHOT", "seed DocID %d has no stored ticket snapshot", seedDocID)
	}
	if seed.TicketID != snapshot.TicketID {
		return nil, stats, evaluationError("seed", "SEED_TICKET_MISMATCH", "seed ticket %d does not belong to DocID %d", seed.TicketID, seedDocID)
	}
	seedFactNames, err := s.store.plan.factValidator.ValidateLayer("facts.seed", seedFacts, fact.ScopeObject)
	if err != nil {
		return nil, stats, adaptFactError(err)
	}
	if err := validateFactScopes(s.tickFactNames, seedFactNames); err != nil {
		return nil, stats, err
	}
	s.resolver = prefilterLookup{seed: snapshot, tickFacts: s.tickFacts, seedFacts: seedFacts}
	ctx := evalContext{tickFacts: s.tickFacts, seedFacts: seedFacts, resolver: &s.resolver}
	result, err := s.evalBitmap(s.store.plan.root, nil, ctx, &stats, "root")
	if err != nil {
		return nil, stats, err
	}
	return wrapDocSet(result), stats, nil
}

func (s *TickSession) node(id bitmapNodeID, path string) (*bitmapNode, error) {
	if s == nil || s.store == nil || s.store.plan == nil || id == invalidBitmapNodeID || int(id) >= len(s.store.plan.nodes) {
		return nil, evaluationError(path, "INVALID_NODE", "bitmap node %d is invalid", id)
	}
	return &s.store.plan.nodes[id], nil
}

func (s *TickSession) evalBitmap(id bitmapNodeID, scope *roaring.Bitmap, ctx evalContext, stats *Stats, path string) (*roaring.Bitmap, error) {
	return s.evalBitmapBound(id, scope, ctx, stats, path, boundIndexQuery{})
}

func (s *TickSession) evalBitmapBound(id bitmapNodeID, scope *roaring.Bitmap, ctx evalContext, stats *Stats, path string, bound boundIndexQuery) (*roaring.Bitmap, error) {
	node, err := s.node(id, path)
	if err != nil {
		return nil, err
	}
	switch node.kind {
	case bitmapKindNone:
		return roaring.New(), nil
	case bitmapKindLookupString, bitmapKindLookupUint64, bitmapKindLookupRange:
		return s.evalLookup(node, scope, ctx, stats, path, bound)
	case bitmapKindExclude:
		if scope == nil {
			return nil, evaluationError(path, "EXCLUDE_REQUIRES_SCOPE", "EXCLUDE requires a positive candidate scope")
		}
		excluded, childErr := s.evalBitmapBound(node.value, nil, ctx, stats, path+".exclude", boundIndexQuery{})
		if childErr != nil {
			return nil, childErr
		}
		out := scope.Clone()
		out.AndNot(excluded)
		stats.SubtractCalls++
		return out, nil
	case bitmapKindIf:
		selected, conditionErr := s.evalCondition(node.when, ctx, path+".if.when")
		if conditionErr != nil {
			return nil, conditionErr
		}
		if selected {
			return s.evalBitmapBound(node.then, scope, ctx, stats, path+".if.then", boundIndexQuery{})
		}
		return s.evalBitmapBound(node.elseNode, scope, ctx, stats, path+".if.else", boundIndexQuery{})
	case bitmapKindOr:
		out := roaring.New()
		for index, child := range node.children {
			part, childErr := s.evalBitmapBound(child, scope, ctx, stats, fmt.Sprintf("%s.or[%d]", path, index), boundIndexQuery{})
			if childErr != nil {
				return nil, childErr
			}
			out.Or(part)
			stats.OrCalls++
			if scope != nil && out.GetCardinality() == scope.GetCardinality() {
				break
			}
		}
		return out, nil
	case bitmapKindAnd:
		return s.evalAnd(node, scope, ctx, stats, path)
	default:
		return nil, evaluationError(path, "UNKNOWN_NODE", "unsupported bitmap node %s", node.kind)
	}
}

func (s *TickSession) evalLookup(node *bitmapNode, scope *roaring.Bitmap, ctx evalContext, stats *Stats, path string, prebound boundIndexQuery) (*roaring.Bitmap, error) {
	if node.props.Has(bitmapStaticNone) {
		return roaring.New(), nil
	}
	if node.query == invalidBitmapQueryID || int(node.query) > len(s.store.plan.queries) {
		return nil, evaluationError(path, "INVALID_LEAF_HANDLE", "bitmap query %d is invalid", node.query)
	}
	query := &s.store.plan.queries[node.query-1]
	if query.slot < 0 || query.slot >= len(s.store.indexes) {
		return nil, evaluationError(path, "INVALID_INDEX_SLOT", "bitmap leaf index slot %d is invalid", query.slot)
	}
	index := s.store.indexes[query.slot]
	bound := prebound
	if bound.kind == boundQueryInvalid {
		var bindErr error
		bound, bindErr = query.bind(ctx, path)
		if bindErr != nil {
			return nil, bindErr
		}
	}
	if scope != nil && scope.GetCardinality() <= s.store.plan.containsProbeThreshold {
		out := roaring.New()
		it := scope.Iterator()
		for it.HasNext() {
			docID := it.Next()
			matched, containsErr := index.contains(bound, docID)
			stats.ContainsCalls++
			if containsErr != nil {
				return nil, evaluationError(path, "INDEX_CONTAINS", "%v", containsErr)
			}
			if matched {
				out.Add(docID)
			}
		}
		return out, nil
	}
	out, lookupErr := index.lookup(bound)
	stats.LookupCalls++
	if lookupErr != nil {
		return nil, evaluationError(path, "INDEX_LOOKUP", "%v", lookupErr)
	}
	if scope != nil {
		out.And(scope)
		stats.AndCalls++
	}
	return out, nil
}

func (s *TickSession) evalAnd(node *bitmapNode, scope *roaring.Bitmap, ctx evalContext, stats *Stats, path string) (*roaring.Bitmap, error) {
	// Static-none is absorbing. Check it before selecting/evaluating an anchor
	// so a scope-requiring sibling cannot run first and report a false error.
	for _, child := range node.children {
		childNode, childErr := s.node(child, path)
		if childErr != nil {
			return nil, childErr
		}
		if childNode.props.Has(bitmapStaticNone) {
			return roaring.New(), nil
		}
	}
	var accumulator *roaring.Bitmap
	var err error
	anchor := invalidBitmapNodeID
	var anchorBound boundIndexQuery
	if scope != nil {
		accumulator = scope.Clone()
	} else {
		var best uint64
		for _, child := range node.children {
			childNode, nodeErr := s.node(child, path)
			if nodeErr != nil {
				return nil, nodeErr
			}
			if !childNode.props.Has(bitmapEstablishesScope) {
				continue
			}
			estimate, bound, estimateErr := s.estimate(child, ctx, fmt.Sprintf("%s.anchor", path))
			if estimateErr != nil {
				return nil, estimateErr
			}
			if anchor == invalidBitmapNodeID || estimate < best {
				anchor, anchorBound, best = child, bound, estimate
			}
		}
		if anchor != invalidBitmapNodeID {
			accumulator, err = s.evalBitmapBound(anchor, nil, ctx, stats, path+".anchor", anchorBound)
			if err != nil {
				return nil, err
			}
		}
	}
	if accumulator == nil {
		accumulator = roaring.New()
	}
	if accumulator.IsEmpty() && anchor != invalidBitmapNodeID {
		return accumulator, nil
	}
	for index, child := range node.children {
		if child == anchor {
			continue
		}
		if _, nodeErr := s.node(child, fmt.Sprintf("%s.and[%d]", path, index)); nodeErr != nil {
			return nil, nodeErr
		}
		var part *roaring.Bitmap
		if scope == nil && anchor == invalidBitmapNodeID {
			part, err = s.evalBitmapBound(child, nil, ctx, stats, fmt.Sprintf("%s.and[%d]", path, index), boundIndexQuery{})
		} else {
			part, err = s.evalBitmapBound(child, accumulator, ctx, stats, fmt.Sprintf("%s.and[%d]", path, index), boundIndexQuery{})
		}
		if err != nil {
			return nil, err
		}
		accumulator = part
		if accumulator.IsEmpty() {
			return accumulator, nil
		}
	}
	return accumulator, nil
}

func (s *TickSession) estimate(id bitmapNodeID, ctx evalContext, path string) (uint64, boundIndexQuery, error) {
	node, err := s.node(id, path)
	if err != nil {
		return 0, boundIndexQuery{}, err
	}
	switch node.kind {
	case bitmapKindLookupString, bitmapKindLookupUint64, bitmapKindLookupRange:
		if node.query == invalidBitmapQueryID || int(node.query) > len(s.store.plan.queries) {
			return 0, boundIndexQuery{}, evaluationError(path, "INVALID_LEAF_HANDLE", "bitmap query %d is invalid", node.query)
		}
		query := &s.store.plan.queries[node.query-1]
		bound, bindErr := query.bind(ctx, path)
		if bindErr != nil {
			return 0, boundIndexQuery{}, bindErr
		}
		estimate, estimateErr := s.store.indexes[query.slot].estimate(bound)
		return estimate, bound, estimateErr
	case bitmapKindNone:
		return 0, boundIndexQuery{}, nil
	case bitmapKindAnd:
		var best uint64
		found := false
		for _, child := range node.children {
			childNode, childErr := s.node(child, path)
			if childErr != nil {
				return 0, boundIndexQuery{}, childErr
			}
			if !childNode.props.Has(bitmapEstablishesScope) {
				continue
			}
			value, _, estimateErr := s.estimate(child, ctx, path+".and")
			if estimateErr != nil {
				return 0, boundIndexQuery{}, estimateErr
			}
			if !found || value < best {
				best, found = value, true
			}
		}
		return best, boundIndexQuery{}, nil
	case bitmapKindOr:
		var total uint64
		for _, child := range node.children {
			childNode, childErr := s.node(child, path)
			if childErr != nil {
				return 0, boundIndexQuery{}, childErr
			}
			if !childNode.props.Has(bitmapEstablishesScope) {
				continue
			}
			value, _, estimateErr := s.estimate(child, ctx, path+".or")
			if estimateErr != nil {
				return 0, boundIndexQuery{}, estimateErr
			}
			if ^uint64(0)-total < value {
				return ^uint64(0), boundIndexQuery{}, nil
			}
			total += value
		}
		return total, boundIndexQuery{}, nil
	case bitmapKindIf:
		selected, conditionErr := s.evalCondition(node.when, ctx, path+".if.when")
		if conditionErr != nil {
			return 0, boundIndexQuery{}, conditionErr
		}
		if selected {
			return s.estimate(node.then, ctx, path+".if.then")
		}
		return s.estimate(node.elseNode, ctx, path+".if.else")
	default:
		return 0, boundIndexQuery{}, evaluationError(path, "INVALID_ESTIMATE", "bitmap node cannot establish a positive scope")
	}
}

func (s *TickSession) evalCondition(program *expression.ScalarProgram, ctx evalContext, path string) (bool, error) {
	if program == nil {
		return false, evaluationError(path, "INVALID_PROGRAM", "bitmap condition program is unavailable")
	}
	value, err := program.EvaluateBool(ctx.expressionLookup())
	if err != nil {
		return false, adaptExpressionEvaluateError(err, path, "CONDITION")
	}
	return value, nil
}
