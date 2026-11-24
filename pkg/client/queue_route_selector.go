package client

import (
	"fmt"
	"math/rand/v2"
)

// queueWeightedRandomSelector 按上报队列长度的倒数加权随机选择。
type queueWeightedRandomSelector struct {
	randomFloat64 func() float64
}

// NewQueueWeightedRandomSelector 创建队列加权随机选择器。
// 每个候选的权重为 1/(1+TicketCount)，不使用配置权重或亲和键。
func NewQueueWeightedRandomSelector() RouteSelector {
	return queueWeightedRandomSelector{randomFloat64: rand.Float64}
}

func (s queueWeightedRandomSelector) Select(selection RouteSelection) (OwnerRef, error) {
	if len(selection.Candidates) == 0 {
		return OwnerRef{}, ErrNoRoute
	}
	total := 0.0
	for _, candidate := range selection.Candidates {
		if candidate.TicketCount < 0 {
			return OwnerRef{}, fmt.Errorf("TicketCount must be non-negative for %+v", candidate.Owner)
		}
		total += 1 / (1 + float64(candidate.TicketCount))
	}
	target := s.randomFloat64() * total
	cumulative := 0.0
	for _, candidate := range selection.Candidates {
		cumulative += 1 / (1 + float64(candidate.TicketCount))
		if target < cumulative {
			return candidate.Owner, nil
		}
	}
	// 浮点累计出现边界舍入时仍返回合法候选。
	return selection.Candidates[len(selection.Candidates)-1].Owner, nil
}

type weightedPrioritySelector struct {
	ticketLimit int
}

// NewWeightedPrioritySelector 创建权重优先选择器，ticketLimit 必须为正。
// 低于阈值时优先选权重最大者；全部达到阈值时优先选队列最短者，
// 同优先级时依次按权重较大、PhysicalNodeID 较小决定。
func NewWeightedPrioritySelector(ticketLimit int) (RouteSelector, error) {
	if ticketLimit <= 0 {
		return nil, fmt.Errorf("ticketLimit must be positive")
	}
	return weightedPrioritySelector{ticketLimit: ticketLimit}, nil
}

func (s weightedPrioritySelector) Select(selection RouteSelection) (OwnerRef, error) {
	if len(selection.Candidates) == 0 {
		return OwnerRef{}, ErrNoRoute
	}
	selected := -1
	for i, candidate := range selection.Candidates {
		if candidate.TicketCount < 0 {
			return OwnerRef{}, fmt.Errorf("TicketCount must be non-negative for %+v", candidate.Owner)
		}
		if selected < 0 || s.better(candidate, selection.Candidates[selected]) {
			selected = i
		}
	}
	return selection.Candidates[selected].Owner, nil
}

func (s weightedPrioritySelector) better(candidate, current RouteCandidate) bool {
	candidateBelowLimit := candidate.TicketCount < s.ticketLimit
	currentBelowLimit := current.TicketCount < s.ticketLimit
	if candidateBelowLimit != currentBelowLimit {
		return candidateBelowLimit
	}
	if !candidateBelowLimit && candidate.TicketCount != current.TicketCount {
		return candidate.TicketCount < current.TicketCount
	}
	if candidate.Weight != current.Weight {
		return candidate.Weight > current.Weight
	}
	return candidate.Owner.PhysicalNodeID < current.Owner.PhysicalNodeID
}
