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
// mutate or retain the Ticket pointers.
type SeedOrderContext struct {
	Now        int64
	Candidates []*Ticket
}

// SeedOrderPolicy returns a permutation of every candidate DocID. It decides
// order only; LogicalNode owns the round cursor and guarantees that a selected
// seed is never selected again in the same round.
type SeedOrderPolicy interface {
	BuildOrder(SeedOrderContext) ([]uint32, error)
}

// FuncSeedOrderPolicy adapts a function to SeedOrderPolicy.
type FuncSeedOrderPolicy func(SeedOrderContext) ([]uint32, error)

func (f FuncSeedOrderPolicy) BuildOrder(ctx SeedOrderContext) ([]uint32, error) {
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

func (arrivalSeedOrderPolicy) BuildOrder(ctx SeedOrderContext) ([]uint32, error) {
	return seedDocIDs(ctx.Candidates), nil
}

type oldestSeedOrderPolicy struct{}

func (oldestSeedOrderPolicy) BuildOrder(ctx SeedOrderContext) ([]uint32, error) {
	candidates := append([]*Ticket(nil), ctx.Candidates...)
	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].CreatedAt < candidates[j].CreatedAt
	})
	return seedDocIDs(candidates), nil
}

type int64PrioritySeedOrderPolicy struct {
	field     string
	direction SeedPriorityDirection
}

func (p int64PrioritySeedOrderPolicy) BuildOrder(ctx SeedOrderContext) ([]uint32, error) {
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
	return seedDocIDs(candidates), nil
}

type randomSeedOrderPolicy struct{ random *rand.Rand }

func (p *randomSeedOrderPolicy) BuildOrder(ctx SeedOrderContext) ([]uint32, error) {
	order := seedDocIDs(ctx.Candidates)
	p.random.Shuffle(len(order), func(i, j int) { order[i], order[j] = order[j], order[i] })
	return order, nil
}

func seedDocIDs(candidates []*Ticket) []uint32 {
	order := make([]uint32, len(candidates))
	for index, ticket := range candidates {
		order[index] = ticket.DocID
	}
	return order
}

type seedRound struct {
	now         int64
	order       []uint32
	cursor      int
	initialized bool
}
