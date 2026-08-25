package matchsystem

import (
	"fmt"
	"math/rand"
	"sort"
)

// SeedOrderPolicyKind identifies a built-in policy used to build one complete
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

// SeedSchedulerConfig controls how one LogicalNode consumes seeds. The attempt
// limit applies to one ProduceMatch call, not to the whole matching round.
type SeedSchedulerConfig struct {
	AttemptLimitPerProduceMatch int
	Order                       SeedOrderPolicyConfig
}

// SeedOrderContext is an immutable view of the active tickets captured when a
// matching round begins. Candidates are in arrival order. Policies must not
// mutate or retain the Candidates slice or its Ticket pointers.
type SeedOrderContext struct {
	Now        int64
	Candidates []*Ticket
}

// SeedOrderPolicy returns a permutation of every candidate TicketID. It decides
// order only; LogicalNode owns the round cursor and guarantees that a selected
// seed is never selected again in the same round. DocID remains private to the
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
	if len(node.arrivalOrder) == len(node.ticketsByDocID) {
		// The slice length is the round boundary. Later Add calls only append
		// beyond it, while compaction replaces arrivalOrder with a new slice.
		return node.arrivalOrder, false
	}
	return appendActiveDocIDs(spare[:0], node), true
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
	candidates := activeTicketsInArrivalOrder(node)
	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].CreatedAt < candidates[j].CreatedAt
	})
	return appendStoredDocIDs(spare[:0], candidates), true
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
	candidates := activeTicketsInArrivalOrder(node)
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
	return appendStoredDocIDs(spare[:0], candidates), true
}

type randomSeedOrderPolicy struct{ random *rand.Rand }

func (p *randomSeedOrderPolicy) BuildOrder(ctx SeedOrderContext) ([]TicketID, error) {
	order := seedTicketIDs(ctx.Candidates)
	p.random.Shuffle(len(order), func(i, j int) { order[i], order[j] = order[j], order[i] })
	return order, nil
}

func (p *randomSeedOrderPolicy) buildOrder(node *LogicalNode, spare []uint32) ([]uint32, bool) {
	order := appendActiveDocIDs(spare[:0], node)
	p.random.Shuffle(len(order), func(i, j int) { order[i], order[j] = order[j], order[i] })
	return order, true
}

func activeTicketsInArrivalOrder(node *LogicalNode) []*storedTicket {
	node.storedSeedCandidates = node.storedSeedCandidates[:0]
	for _, docID := range node.arrivalOrder {
		if ticket := node.ticketsByDocID[docID]; ticket != nil {
			node.storedSeedCandidates = append(node.storedSeedCandidates, ticket)
		}
	}
	return node.storedSeedCandidates
}

func appendActiveDocIDs(order []uint32, node *LogicalNode) []uint32 {
	for _, docID := range node.arrivalOrder {
		if node.ticketsByDocID[docID] != nil {
			order = append(order, docID)
		}
	}
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

type seedRound struct {
	now         int64
	order       []uint32
	cursor      int
	ownsOrder   bool
	initialized bool
}
