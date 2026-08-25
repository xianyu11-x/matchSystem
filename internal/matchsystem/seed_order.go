package matchsystem

import (
	"container/heap"
	"fmt"
	"math/rand"
	"sort"
)

// SeedOrderPolicyKind identifies a built-in policy used to build the bounded
// seed order at the beginning of a matching round.
type SeedOrderPolicyKind string

const (
	SeedOrderArrival       SeedOrderPolicyKind = "arrival"
	SeedOrderOldest        SeedOrderPolicyKind = "oldest"
	SeedOrderInt64Priority SeedOrderPolicyKind = "int64_priority"
	SeedOrderRandom        SeedOrderPolicyKind = "random"
)

type SeedPriorityDirection string

const (
	SeedPriorityDescending SeedPriorityDirection = "descending"
	SeedPriorityAscending  SeedPriorityDirection = "ascending"
)

// SeedOrderPolicyConfig is the serializable configuration for built-in seed
// ordering policies. The zero value selects arrival order.
type SeedOrderPolicyConfig struct {
	Kind              SeedOrderPolicyKind
	PriorityField     string
	PriorityDirection SeedPriorityDirection
	RandomSeed        int64
}

const (
	defaultAttemptLimitPerProduceMatch = 500
	defaultAttemptLimitPerMatchRound   = 500
)

// SeedSchedulerConfig controls how one LogicalNode consumes seeds.
type SeedSchedulerConfig struct {
	// AttemptLimitPerProduceMatch limits the number of valid seeds consumed by
	// one ProduceMatch call. Values <= 0 use the default of 500.
	AttemptLimitPerProduceMatch int
	// AttemptLimitPerMatchRound limits the total number of valid seeds consumed
	// by this LogicalNode during one matching round. The count accumulates over
	// multiple ProduceMatch calls and resets at BeginMatchRound. Stale/deleted
	// entries in the round snapshot do not consume this budget. Values <= 0 use
	// the default of 500.
	AttemptLimitPerMatchRound int
	Order                     SeedOrderPolicyConfig
}

// SeedOrderContext is an immutable view of all active tickets captured when a
// matching round begins. MaxSeeds is the maximum number of TicketIDs that a
// policy may return for this round. Policies must not mutate or retain the
// Candidates slice or its Ticket pointers.
type SeedOrderContext struct {
	Now        int64
	Candidates []*Ticket
	MaxSeeds   int
}

// SeedOrderPolicy returns an ordered subset of active candidate TicketIDs. The
// subset must contain no more than SeedOrderContext.MaxSeeds unique IDs; the
// LogicalNode owns the round cursor and guarantees that a selected seed is
// never selected again in the same round. DocID remains private to the
// LogicalNode and is resolved only after a custom policy returns.
type SeedOrderPolicy interface {
	BuildOrder(SeedOrderContext) ([]TicketID, error)
}

// optimizedSeedOrderPolicy lets framework-owned policies build directly from
// LogicalNode state and reuse its spare order buffer. Custom policies continue
// through the validated public SeedOrderPolicy contract.
type optimizedSeedOrderPolicy interface {
	SeedOrderPolicy
	buildOrder(node *LogicalNode, spare []uint32) (order []uint32, ownsOrder bool)
}

// FuncSeedOrderPolicy adapts a function to SeedOrderPolicy.
type FuncSeedOrderPolicy func(SeedOrderContext) ([]TicketID, error)

func (f FuncSeedOrderPolicy) BuildOrder(ctx SeedOrderContext) ([]TicketID, error) {
	if f == nil {
		return nil, fmt.Errorf("seed order function is nil")
	}
	return f(ctx)
}

// NewSeedOrderPolicy compiles built-in seed ordering configuration into a
// runtime policy. Runtime policies are owned by one LogicalNode and need not be
// goroutine-safe.
func NewSeedOrderPolicy(config SeedOrderPolicyConfig) (SeedOrderPolicy, error) {
	kind := config.Kind
	if kind == "" {
		kind = SeedOrderArrival
	}
	switch kind {
	case SeedOrderArrival:
		return arrivalSeedOrderPolicy{}, nil
	case SeedOrderOldest:
		return oldestSeedOrderPolicy{}, nil
	case SeedOrderInt64Priority:
		if config.PriorityField == "" {
			return nil, fmt.Errorf("PriorityField is required for %q seed order", kind)
		}
		direction := config.PriorityDirection
		if direction == "" {
			direction = SeedPriorityDescending
		}
		if direction != SeedPriorityDescending && direction != SeedPriorityAscending {
			return nil, fmt.Errorf("unsupported seed priority direction %q", direction)
		}
		return int64PrioritySeedOrderPolicy{field: config.PriorityField, direction: direction}, nil
	case SeedOrderRandom:
		return &randomSeedOrderPolicy{random: rand.New(rand.NewSource(config.RandomSeed))}, nil
	default:
		return nil, fmt.Errorf("unsupported seed order policy %q", kind)
	}
}

type arrivalSeedOrderPolicy struct{}

func (arrivalSeedOrderPolicy) BuildOrder(ctx SeedOrderContext) ([]TicketID, error) {
	return seedTicketIDs(ctx.Candidates), nil
}

func (arrivalSeedOrderPolicy) buildOrder(node *LogicalNode, spare []uint32) ([]uint32, bool) {
	limit := node.config.SeedScheduler.AttemptLimitPerMatchRound
	if len(node.arrivalOrder) == len(node.ticketsByDocID) {
		// The slice length is the round boundary. Later Add calls only append
		// beyond it, while compaction replaces arrivalOrder with a new slice.
		if len(node.arrivalOrder) > limit {
			return node.arrivalOrder[:limit], false
		}
		return node.arrivalOrder, false
	}
	return appendActiveDocIDs(seedOrderBuffer(spare, limit, len(node.ticketsByDocID)), node, limit), true
}

type oldestSeedOrderPolicy struct{}

func (oldestSeedOrderPolicy) BuildOrder(ctx SeedOrderContext) ([]TicketID, error) {
	candidates := append([]*Ticket(nil), ctx.Candidates...)
	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].CreatedAt < candidates[j].CreatedAt
	})
	return seedTicketIDs(candidates), nil
}

func (oldestSeedOrderPolicy) buildOrder(node *LogicalNode, spare []uint32) ([]uint32, bool) {
	candidates := topSeedTickets(node, node.config.SeedScheduler.AttemptLimitPerMatchRound, func(left, right *storedTicket) bool {
		if left.CreatedAt != right.CreatedAt {
			return left.CreatedAt < right.CreatedAt
		}
		return left.arrivalIndex < right.arrivalIndex
	})
	return appendStoredDocIDs(seedOrderBuffer(spare, node.config.SeedScheduler.AttemptLimitPerMatchRound, len(node.ticketsByDocID)), candidates), true
}

type int64PrioritySeedOrderPolicy struct {
	field     string
	direction SeedPriorityDirection
}

func (p int64PrioritySeedOrderPolicy) BuildOrder(ctx SeedOrderContext) ([]TicketID, error) {
	candidates := append([]*Ticket(nil), ctx.Candidates...)
	sort.SliceStable(candidates, func(i, j int) bool {
		left, leftOK := candidates[i].Int64Values[p.field]
		right, rightOK := candidates[j].Int64Values[p.field]
		if leftOK != rightOK {
			return leftOK
		}
		if left == right {
			return false
		}
		if p.direction == SeedPriorityAscending {
			return left < right
		}
		return left > right
	})
	return seedTicketIDs(candidates), nil
}

func (p int64PrioritySeedOrderPolicy) buildOrder(node *LogicalNode, spare []uint32) ([]uint32, bool) {
	candidates := topSeedTickets(node, node.config.SeedScheduler.AttemptLimitPerMatchRound, func(left, right *storedTicket) bool {
		leftValue, leftOK := left.Int64Values[p.field]
		rightValue, rightOK := right.Int64Values[p.field]
		if leftOK != rightOK {
			return leftOK
		}
		if leftValue == rightValue {
			return left.arrivalIndex < right.arrivalIndex
		}
		if p.direction == SeedPriorityAscending {
			return leftValue < rightValue
		}
		return leftValue > rightValue
	})
	return appendStoredDocIDs(seedOrderBuffer(spare, node.config.SeedScheduler.AttemptLimitPerMatchRound, len(node.ticketsByDocID)), candidates), true
}

type randomSeedOrderPolicy struct{ random *rand.Rand }

func (p *randomSeedOrderPolicy) BuildOrder(ctx SeedOrderContext) ([]TicketID, error) {
	order := seedTicketIDs(ctx.Candidates)
	p.random.Shuffle(len(order), func(i, j int) { order[i], order[j] = order[j], order[i] })
	return order, nil
}

func (p *randomSeedOrderPolicy) buildOrder(node *LogicalNode, spare []uint32) ([]uint32, bool) {
	limit := node.config.SeedScheduler.AttemptLimitPerMatchRound
	order := appendRandomActiveDocIDs(seedOrderBuffer(spare, limit, len(node.ticketsByDocID)), node, limit, p.random)
	return order, true
}

// seedTopHeap keeps the best limit tickets while scanning the active arrival
// order. Its root is the worst selected ticket, so the heap never needs to
// materialize a pointer slice larger than the round seed limit.
type seedTopHeap struct {
	items  []*storedTicket
	better func(left, right *storedTicket) bool
}

func (h seedTopHeap) Len() int { return len(h.items) }
func (h seedTopHeap) Less(i, j int) bool {
	return h.better(h.items[j], h.items[i])
}
func (h seedTopHeap) Swap(i, j int) { h.items[i], h.items[j] = h.items[j], h.items[i] }
func (h *seedTopHeap) Push(value any) {
	h.items = append(h.items, value.(*storedTicket))
}
func (h *seedTopHeap) Pop() any {
	last := len(h.items) - 1
	value := h.items[last]
	h.items = h.items[:last]
	return value
}

func topSeedTickets(node *LogicalNode, limit int, better func(left, right *storedTicket) bool) []*storedTicket {
	if limit <= 0 {
		return nil
	}
	selected := seedTopHeap{
		items:  make([]*storedTicket, 0, minInt(limit, len(node.ticketsByDocID))),
		better: better,
	}
	for _, docID := range node.arrivalOrder {
		ticket := node.ticketsByDocID[docID]
		if ticket == nil {
			continue
		}
		if len(selected.items) < limit {
			heap.Push(&selected, ticket)
			continue
		}
		if better(ticket, selected.items[0]) {
			selected.items[0] = ticket
			heap.Fix(&selected, 0)
		}
	}
	sort.SliceStable(selected.items, func(i, j int) bool {
		return better(selected.items[i], selected.items[j])
	})
	return selected.items
}

func appendActiveDocIDs(order []uint32, node *LogicalNode, limit int) []uint32 {
	for _, docID := range node.arrivalOrder {
		if node.ticketsByDocID[docID] == nil {
			continue
		}
		order = append(order, docID)
		if len(order) == limit {
			break
		}
	}
	return order
}

// seedOrderBuffer returns a reusable order buffer sized to the smaller of the
// configured round limit and the current active Ticket count. A larger
// historical buffer is deliberately replaced so a transient large
// configuration cannot stay retained by future rounds.
func seedOrderBuffer(spare []uint32, limit, activeCount int) []uint32 {
	capacity := minInt(limit, activeCount)
	if capacity <= 0 {
		return nil
	}
	if cap(spare) != capacity {
		return make([]uint32, 0, capacity)
	}
	return spare[:0]
}

func appendRandomActiveDocIDs(order []uint32, node *LogicalNode, limit int, random *rand.Rand) []uint32 {
	if limit <= 0 {
		return order
	}
	seen := 0
	for _, docID := range node.arrivalOrder {
		if node.ticketsByDocID[docID] == nil {
			continue
		}
		seen++
		if len(order) < limit {
			order = append(order, docID)
			continue
		}
		index := random.Intn(seen)
		if index < limit {
			order[index] = docID
		}
	}
	random.Shuffle(len(order), func(i, j int) { order[i], order[j] = order[j], order[i] })
	return order
}

func appendStoredDocIDs(order []uint32, candidates []*storedTicket) []uint32 {
	for _, ticket := range candidates {
		order = append(order, ticket.docID)
	}
	return order
}

func seedTicketIDs(candidates []*Ticket) []TicketID {
	order := make([]TicketID, len(candidates))
	for index, ticket := range candidates {
		order[index] = ticket.TicketID
	}
	return order
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}

type seedRound struct {
	now            int64
	order          []uint32
	cursor         int
	attemptedSeeds int
	ownsOrder      bool
	initialized    bool
}
