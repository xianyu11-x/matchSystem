package client

import (
	"errors"
	"testing"
)

func TestWeightedRendezvousSelectorDistributionAndOrder(t *testing.T) {
	selector := NewWeightedRendezvousSelector()
	selection := RouteSelection{
		Request:    RouteRequest{Rule: testRule, TicketID: 1},
		Candidates: []RouteCandidate{{Owner: owner7, Weight: 2}, {Owner: owner8, Weight: 3}},
	}
	counts := map[OwnerRef]int{}
	for affinity := uint64(1); affinity <= 4000; affinity++ {
		selection.Request.AffinityKey = affinity
		owner, err := selector.Select(selection)
		if err != nil {
			t.Fatal(err)
		}
		counts[owner]++
		selection.Candidates[0], selection.Candidates[1] = selection.Candidates[1], selection.Candidates[0]
		reordered, err := selector.Select(selection)
		if err != nil || reordered != owner {
			t.Fatalf("候选顺序不应影响选择: %+v, %+v, %v", owner, reordered, err)
		}
	}
	// 3:2 的权重应在大量确定性亲和键上体现为相近比例。
	if counts[owner8] < 2200 || counts[owner8] > 2600 || counts[owner7]+counts[owner8] != 4000 {
		t.Fatalf("加权分布异常: %+v", counts)
	}
	selection.Candidates = nil
	if _, err := selector.Select(selection); !errors.Is(err, ErrNoRoute) {
		t.Fatalf("空候选应无可用路由: %v", err)
	}
}
