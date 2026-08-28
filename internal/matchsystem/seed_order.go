package matchsystem

import (
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
// LogicalNode and is resolved only after a policy returns.
type SeedOrderPolicy interface {
	BuildOrder(SeedOrderContext) ([]TicketID, error)
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
	return limitSeedOrder(seedTicketIDs(ctx.Candidates), ctx.MaxSeeds), nil
}

type oldestSeedOrderPolicy struct{}

func (oldestSeedOrderPolicy) BuildOrder(ctx SeedOrderContext) ([]TicketID, error) {
	candidates := append([]*Ticket(nil), ctx.Candidates...)
	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].CreatedAt < candidates[j].CreatedAt
	})
	return limitSeedOrder(seedTicketIDs(candidates), ctx.MaxSeeds), nil
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
	return limitSeedOrder(seedTicketIDs(candidates), ctx.MaxSeeds), nil
}

type randomSeedOrderPolicy struct{ random *rand.Rand }

func (p *randomSeedOrderPolicy) BuildOrder(ctx SeedOrderContext) ([]TicketID, error) {
	order := seedTicketIDs(ctx.Candidates)
	p.random.Shuffle(len(order), func(i, j int) { order[i], order[j] = order[j], order[i] })
	return limitSeedOrder(order, ctx.MaxSeeds), nil
}

func limitSeedOrder(order []TicketID, limit int) []TicketID {
	if limit <= 0 {
		return nil
	}
	if len(order) > limit {
		return order[:limit]
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
