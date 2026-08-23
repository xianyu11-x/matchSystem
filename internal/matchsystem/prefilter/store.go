package prefilter

import (
	"fmt"

	"github.com/RoaringBitmap/roaring/v2"
)

// IndexStore owns active DocIDs and all physical indexes for one LogicalNode.
// It is intentionally not goroutine-safe; its owning LogicalNode serializes access.
type IndexStore struct {
	plan    *Plan
	indexes []runtimeIndex
	active  *roaring.Bitmap
}

func New(plan *Plan) (*IndexStore, error) {
	if plan == nil {
		return nil, fmt.Errorf("prefilter plan is nil")
	}
	indexes := make([]runtimeIndex, len(plan.indexSpecs))
	for slot, spec := range plan.indexSpecs {
		indexes[slot] = newIndex(spec)
	}
	return &IndexStore{plan: plan, indexes: indexes, active: roaring.New()}, nil
}

func (s *IndexStore) Add(document Document) error {
	if document.DocID == 0 {
		return fmt.Errorf("candidate document DocID must be non-zero")
	}
	if s.active.Contains(document.DocID) {
		return fmt.Errorf("candidate document DocID %d already exists", document.DocID)
	}
	for _, index := range s.indexes {
		if err := index.validate(document); err != nil {
			return err
		}
	}
	for _, index := range s.indexes {
		index.add(document)
	}
	s.active.Add(document.DocID)
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
	return true
}

func (s *IndexStore) Len() uint64 {
	if s == nil || s.active == nil {
		return 0
	}
	return s.active.GetCardinality()
}

// BeginTick prepares the mutable indexes and creates one TickSession.
// IndexStore is deliberately single-goroutine state: callers must finish
// all Add/Remove operations before beginning a Tick and must not mutate the
// IndexStore until every session execution has returned. No lock or concurrent
// snapshot is provided by this package.
func (s *IndexStore) BeginTick(now int64, facts Facts) *TickSession {
	if s == nil {
		return &TickSession{}
	}
	for _, index := range s.indexes {
		index.prepare()
	}
	return &TickSession{store: s, now: now, facts: cloneFacts(facts)}
}

// TickSession fixes the Tick-level values shared by a sequence of seed
// evaluations. It is not a data snapshot: it references the IndexStore's prepared
// indexes and active membership. Single-goroutine ownership makes those
// references stable for the lifetime of the session without locks or copying
// every posting bitmap.
type TickSession struct {
	store *IndexStore
	now   int64
	facts Facts
}

type Stats struct {
	LookupCalls   uint64
	ContainsCalls uint64
	AndCalls      uint64
	OrCalls       uint64
	SubtractCalls uint64
}

func (s *TickSession) Candidates(seed Document) (*DocSet, error) {
	result, _, err := s.CandidatesWithStats(seed)
	return result, err
}

func (s *TickSession) CandidatesWithStats(seed Document) (*DocSet, Stats, error) {
	var stats Stats
	if s == nil || s.store == nil || s.store.plan == nil || s.store.plan.root == nil {
		return nil, stats, evaluationError("plan.root", "INVALID_TICK_SESSION", "tick session is not initialized")
	}
	if seed.DocID == 0 || !s.store.active.Contains(seed.DocID) {
		return nil, stats, evaluationError("seed", "INACTIVE_SEED", "seed DocID %d is not active", seed.DocID)
	}
	ctx := evalContext{seed: seed, now: s.now, facts: s.facts}
	result, err := s.eval(s.store.plan.root, nil, ctx, &stats)
	if err != nil {
		return nil, stats, err
	}
	return wrapDocSet(result), stats, nil
}

func (s *TickSession) eval(node compiledNode, scope *roaring.Bitmap, ctx evalContext, stats *Stats) (*roaring.Bitmap, error) {
	switch n := node.(type) {
	case *noneNode:
		return roaring.New(), nil
	case *lookupNode:
		return s.evalLookup(n, scope, ctx, stats)
	case *excludeNode:
		if scope == nil {
			return nil, evaluationError(n.path, "EXCLUDE_REQUIRES_SCOPE", "compiled EXCLUDE has no candidate scope")
		}
		excluded, err := s.eval(n.child, nil, ctx, stats)
		if err != nil {
			return nil, err
		}
		out := scope.Clone()
		out.AndNot(excluded)
		stats.SubtractCalls++
		return out, nil
	case *ifNode:
		selected, err := n.condition.evaluate(ctx)
		if err != nil {
			return nil, evaluationError(n.path+".if.when", "CONDITION", "%v", err)
		}
		if selected {
			return s.eval(n.thenExpr, scope, ctx, stats)
		}
		return s.eval(n.elseExpr, scope, ctx, stats)
	case *orNode:
		out := roaring.New()
		for _, child := range n.children {
			part, err := s.eval(child, scope, ctx, stats)
			if err != nil {
				return nil, err
			}
			out.Or(part)
			stats.OrCalls++
			// Every branch result is already restricted to scope, so equal
			// cardinality means the union has reached the complete scope.
			if scope != nil && out.GetCardinality() == scope.GetCardinality() {
				break
			}
		}
		return out, nil
	case *andNode:
		return s.evalAnd(n, scope, ctx, stats)
	default:
		return nil, evaluationError(node.pathName(), "UNKNOWN_NODE", "unsupported compiled node %T", node)
	}
}

func (s *TickSession) evalLookup(node *lookupNode, scope *roaring.Bitmap, ctx evalContext, stats *Stats) (*roaring.Bitmap, error) {
	query, err := node.query.bind(ctx, node.path)
	if err != nil {
		return nil, err
	}
	index := s.store.indexes[node.query.indexSlot()]
	if scope != nil && scope.GetCardinality() <= s.store.plan.containsProbeThreshold {
		out := roaring.New()
		it := scope.Iterator()
		for it.HasNext() {
			docID := it.Next()
			matched, containsErr := index.contains(query, docID)
			stats.ContainsCalls++
			if containsErr != nil {
				return nil, evaluationError(node.path, "INDEX_CONTAINS", "%v", containsErr)
			}
			if matched {
				out.Add(docID)
			}
		}
		return out, nil
	}
	out, err := index.lookup(query)
	stats.LookupCalls++
	if err != nil {
		return nil, evaluationError(node.path, "INDEX_LOOKUP", "%v", err)
	}
	if scope != nil {
		out.And(scope)
		stats.AndCalls++
	}
	return out, nil
}

func (s *TickSession) evalAnd(node *andNode, scope *roaring.Bitmap, ctx evalContext, stats *Stats) (*roaring.Bitmap, error) {
	var accumulator *roaring.Bitmap
	anchor := -1
	if scope != nil {
		accumulator = scope.Clone()
	} else {
		var best uint64
		for i, child := range node.children {
			if !child.canAnchor() {
				continue
			}
			estimate, err := s.estimate(child, ctx)
			if err != nil {
				return nil, err
			}
			if anchor < 0 || estimate < best {
				anchor, best = i, estimate
			}
		}
		if anchor >= 0 {
			var err error
			accumulator, err = s.eval(node.children[anchor], nil, ctx, stats)
			if err != nil {
				return nil, err
			}
		}
	}
	if accumulator == nil {
		accumulator = roaring.New()
	}
	if accumulator.IsEmpty() && anchor >= 0 {
		return accumulator, nil
	}
	for i, child := range node.children {
		if i == anchor {
			continue
		}
		if _, empty := child.(*noneNode); empty {
			return roaring.New(), nil
		}
		if scope == nil && anchor < 0 {
			part, err := s.eval(child, nil, ctx, stats)
			if err != nil {
				return nil, err
			}
			accumulator = part
		} else {
			part, err := s.eval(child, accumulator, ctx, stats)
			if err != nil {
				return nil, err
			}
			accumulator = part
		}
		if accumulator.IsEmpty() {
			return accumulator, nil
		}
	}
	return accumulator, nil
}

func (s *TickSession) estimate(node compiledNode, ctx evalContext) (uint64, error) {
	switch n := node.(type) {
	case *lookupNode:
		query, err := n.query.bind(ctx, n.path)
		if err != nil {
			return 0, err
		}
		estimate, err := s.store.indexes[n.query.indexSlot()].estimate(query)
		if err != nil {
			return 0, evaluationError(n.path, "INDEX_ESTIMATE", "%v", err)
		}
		return estimate, nil
	case *noneNode:
		return 0, nil
	case *andNode:
		var best uint64
		found := false
		for _, child := range n.children {
			if !child.canAnchor() {
				continue
			}
			value, err := s.estimate(child, ctx)
			if err != nil {
				return 0, err
			}
			if !found || value < best {
				best, found = value, true
			}
		}
		return best, nil
	case *orNode:
		var total uint64
		for _, child := range n.children {
			if !child.canAnchor() {
				continue
			}
			value, err := s.estimate(child, ctx)
			if err != nil {
				return 0, err
			}
			if ^uint64(0)-total < value {
				return ^uint64(0), nil
			}
			total += value
		}
		return total, nil
	case *ifNode:
		selected, err := n.condition.evaluate(ctx)
		if err != nil {
			return 0, evaluationError(n.path+".if.when", "CONDITION", "%v", err)
		}
		if selected {
			return s.estimate(n.thenExpr, ctx)
		}
		return s.estimate(n.elseExpr, ctx)
	default:
		return 0, evaluationError(node.pathName(), "INVALID_ESTIMATE", "node cannot establish a positive scope")
	}
}
