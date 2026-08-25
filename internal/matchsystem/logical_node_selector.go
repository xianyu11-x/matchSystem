package matchsystem

import (
	"fmt"

	"matchSystem/internal/identity"
)

// LogicalNodeCandidate is a read-only scheduling snapshot. Candidates are
// supplied in PhysicalNode load order, including currently ineligible nodes so
// stateful selectors can preserve stable traversal positions.
type LogicalNodeCandidate struct {
	Key             identity.LogicalNodeKey
	Eligible        bool
	TicketCount     int
	OldestCreatedAt int64
}

type LogicalNodeSelectContext struct {
	Candidates []LogicalNodeCandidate
}

// LogicalNodeSelector selects one eligible LogicalNode for a ProduceMatch
// call. It does not execute matching or own the matching-round lifecycle.
type LogicalNodeSelector interface {
	Select(LogicalNodeSelectContext) (identity.LogicalNodeKey, error)
}

// FuncLogicalNodeSelector adapts a function to LogicalNodeSelector.
type FuncLogicalNodeSelector func(LogicalNodeSelectContext) (identity.LogicalNodeKey, error)

func (f FuncLogicalNodeSelector) Select(ctx LogicalNodeSelectContext) (identity.LogicalNodeKey, error) {
	if f == nil {
		return identity.LogicalNodeKey{}, fmt.Errorf("logical node selector function is nil")
	}
	return f(ctx)
}

func NewRoundRobinLogicalNodeSelector() LogicalNodeSelector {
	return &roundRobinLogicalNodeSelector{}
}

type roundRobinLogicalNodeSelector struct {
	last    identity.LogicalNodeKey
	hasLast bool
}

func (p *roundRobinLogicalNodeSelector) Select(ctx LogicalNodeSelectContext) (identity.LogicalNodeKey, error) {
	if len(ctx.Candidates) == 0 {
		return identity.LogicalNodeKey{}, ErrNoLogicalNodeAvailable
	}
	start := 0
	if p.hasLast {
		for index, candidate := range ctx.Candidates {
			if candidate.Key == p.last {
				start = (index + 1) % len(ctx.Candidates)
				break
			}
		}
	}
	for checked := 0; checked < len(ctx.Candidates); checked++ {
		candidate := ctx.Candidates[(start+checked)%len(ctx.Candidates)]
		if !candidate.Eligible {
			continue
		}
		p.last = candidate.Key
		p.hasLast = true
		return candidate.Key, nil
	}
	return identity.LogicalNodeKey{}, ErrNoLogicalNodeAvailable
}

// NewSmoothWeightedRoundRobinLogicalNodeSelector creates a deterministic
// smooth weighted round-robin selector. Missing RuleKeys have weight 1.
func NewSmoothWeightedRoundRobinLogicalNodeSelector(weights map[identity.RuleKey]uint32) (LogicalNodeSelector, error) {
	copyWeights := make(map[identity.RuleKey]int64, len(weights))
	for key, weight := range weights {
		if err := key.Validate(); err != nil {
			return nil, err
		}
		if weight == 0 {
			return nil, fmt.Errorf("LogicalNode weight for %s must be greater than zero", key)
		}
		copyWeights[key] = int64(weight)
	}
	return &smoothWeightedRoundRobinLogicalNodeSelector{
		weights: copyWeights,
		current: make(map[identity.LogicalNodeKey]int64),
	}, nil
}

type smoothWeightedRoundRobinLogicalNodeSelector struct {
	weights map[identity.RuleKey]int64
	current map[identity.LogicalNodeKey]int64
}

func (p *smoothWeightedRoundRobinLogicalNodeSelector) Select(ctx LogicalNodeSelectContext) (identity.LogicalNodeKey, error) {
	selected := -1
	var total int64
	for index, candidate := range ctx.Candidates {
		if !candidate.Eligible {
			continue
		}
		weight := p.weights[candidate.Key.Rule]
		if weight == 0 {
			weight = 1
		}
		p.current[candidate.Key] += weight
		total += weight
		if selected < 0 || p.current[candidate.Key] > p.current[ctx.Candidates[selected].Key] {
			selected = index
		}
	}
	if selected < 0 {
		return identity.LogicalNodeKey{}, ErrNoLogicalNodeAvailable
	}
	key := ctx.Candidates[selected].Key
	p.current[key] -= total
	return key, nil
}

func NewLargestQueueLogicalNodeSelector() LogicalNodeSelector {
	return largestQueueLogicalNodeSelector{}
}

type largestQueueLogicalNodeSelector struct{}

func (largestQueueLogicalNodeSelector) Select(ctx LogicalNodeSelectContext) (identity.LogicalNodeKey, error) {
	selected := -1
	for index, candidate := range ctx.Candidates {
		if !candidate.Eligible {
			continue
		}
		if selected < 0 || candidate.TicketCount > ctx.Candidates[selected].TicketCount {
			selected = index
		}
	}
	if selected < 0 {
		return identity.LogicalNodeKey{}, ErrNoLogicalNodeAvailable
	}
	return ctx.Candidates[selected].Key, nil
}

func NewOldestWaitingLogicalNodeSelector() LogicalNodeSelector {
	return oldestWaitingLogicalNodeSelector{}
}

type oldestWaitingLogicalNodeSelector struct{}

func (oldestWaitingLogicalNodeSelector) Select(ctx LogicalNodeSelectContext) (identity.LogicalNodeKey, error) {
	selected := -1
	for index, candidate := range ctx.Candidates {
		if !candidate.Eligible {
			continue
		}
		if selected < 0 || candidate.OldestCreatedAt < ctx.Candidates[selected].OldestCreatedAt {
			selected = index
		}
	}
	if selected < 0 {
		return identity.LogicalNodeKey{}, ErrNoLogicalNodeAvailable
	}
	return ctx.Candidates[selected].Key, nil
}
