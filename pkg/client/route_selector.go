package client

import (
	"crypto/sha256"
	"encoding/binary"
	"math"
)

// RouteCandidate 是允许选择的目标及其正权重。
type RouteCandidate struct {
	Owner       OwnerRef
	Weight      uint32
	TicketCount int
}

// RouteSelection 是传给选择器的请求和独立候选快照；Request.AffinityKey
// 已按 RouteRequest 的约定补齐，候选顺序与当前路由表一致。更新物理节点时，
// 已有规则候选保留原位置，新增规则候选追加到对应规则的末尾。
type RouteSelection struct {
	Request    RouteRequest
	Candidates []RouteCandidate
}

// RouteSelector 从候选快照中选择一个目标。Router 会重新校验其结果。
type RouteSelector interface {
	Select(RouteSelection) (OwnerRef, error)
}

// WeightedRendezvousSelector 使用加权 Rendezvous 哈希选择候选目标。
// 得分相同时，数值更小的 PhysicalNodeID 优先。
type WeightedRendezvousSelector struct{}

// NewWeightedRendezvousSelector 创建无状态的默认路由选择器。
func NewWeightedRendezvousSelector() WeightedRendezvousSelector {
	return WeightedRendezvousSelector{}
}

func (WeightedRendezvousSelector) Select(selection RouteSelection) (OwnerRef, error) {
	var selected OwnerRef
	bestScore := -1.0
	ruleID := selection.Request.Rule.RuleID
	for _, candidate := range selection.Candidates {
		if candidate.Weight == 0 {
			continue
		}
		score := weightedRendezvousScore(ruleID, selection.Request.AffinityKey, uint64(candidate.Owner.PhysicalNodeID), candidate.Weight)
		if score > bestScore || score == bestScore && candidate.Owner.PhysicalNodeID < selected.PhysicalNodeID {
			selected = candidate.Owner
			bestScore = score
		}
	}
	if bestScore < 0 {
		return OwnerRef{}, ErrNoRoute
	}
	return selected, nil
}

func weightedRendezvousScore(ruleID, affinity, physical uint64, weight uint32) float64 {
	var input [24]byte
	binary.BigEndian.PutUint64(input[0:8], ruleID)
	binary.BigEndian.PutUint64(input[8:16], affinity)
	binary.BigEndian.PutUint64(input[16:24], physical)
	digest := sha256.Sum256(input[:])
	hash := binary.BigEndian.Uint64(digest[:8])
	u := (float64(hash>>12) + 0.5) / float64(uint64(1)<<52)
	return float64(weight) / -math.Log(u)
}
