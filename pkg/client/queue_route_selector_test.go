package client

import (
	"context"
	"errors"
	"math"
	"testing"

	"github.com/xianyu11-x/matchSystem/pkg/common"
)

func TestQueueWeightedRandomSelectorUsesQueueAndRandomInput(t *testing.T) {
	selection := RouteSelection{Candidates: []RouteCandidate{
		{Owner: owner7, Weight: 1, TicketCount: 0},
		{Owner: owner8, Weight: math.MaxUint32, TicketCount: 1},
	}}
	for _, tc := range []struct {
		name   string
		sample float64
		want   OwnerRef
	}{
		{"零值", 0, owner7},
		{"第一区间", 0.5, owner7},
		{"第二区间", 0.75, owner8},
		{"接近一", math.Nextafter(1, 0), owner8},
	} {
		t.Run(tc.name, func(t *testing.T) {
			selector := queueWeightedRandomSelector{randomFloat64: func() float64 { return tc.sample }}
			got, err := selector.Select(selection)
			if err != nil || got != tc.want {
				t.Fatalf("随机值 %v: 得到 %+v，期望 %+v，错误 %v", tc.sample, got, tc.want, err)
			}
			changed := selection
			changed.Request = RouteRequest{Rule: testRule, TicketID: 2, AffinityKey: 42, RequestID: 99}
			changed.Candidates = append([]RouteCandidate(nil), selection.Candidates...)
			changed.Candidates[0].Weight, changed.Candidates[1].Weight = math.MaxUint32, 1
			if got, err := selector.Select(changed); err != nil || got != tc.want {
				t.Fatalf("配置权重或亲和键影响了随机区间: %+v，%v", got, err)
			}
		})
	}
}

func TestQueueWeightedRandomSelectorBoundariesAndDistribution(t *testing.T) {
	selection := RouteSelection{Candidates: []RouteCandidate{
		{Owner: owner7, Weight: 1, TicketCount: 0},
		{Owner: owner8, Weight: 1, TicketCount: 1},
	}}
	const samples = 6000
	index := 0
	selector := queueWeightedRandomSelector{randomFloat64: func() float64 {
		value := (float64(index) + 0.5) / samples
		index++
		return value
	}}
	counts := make(map[OwnerRef]int)
	for i := 0; i < samples; i++ {
		got, err := selector.Select(selection)
		if err != nil {
			t.Fatal(err)
		}
		counts[got]++
	}
	if counts[owner7] != 4000 || counts[owner8] != 2000 {
		t.Fatalf("固定随机序列的队列权重分布错误: %+v", counts)
	}

	selection.Candidates[0].TicketCount = math.MaxInt
	selection.Candidates[1].TicketCount = math.MaxInt
	for _, tc := range []struct {
		sample float64
		want   OwnerRef
	}{
		{math.Nextafter(0.5, 0), owner7},
		{0.5, owner8},
	} {
		selector := queueWeightedRandomSelector{randomFloat64: func() float64 { return tc.sample }}
		if got, err := selector.Select(selection); err != nil || got != tc.want {
			t.Fatalf("最大队列和区间边界选择错误: %+v，%v", got, err)
		}
	}

	if _, err := NewQueueWeightedRandomSelector().Select(RouteSelection{}); !errors.Is(err, ErrNoRoute) {
		t.Fatalf("空候选应无可用路由: %v", err)
	}
	selection.Candidates[1].TicketCount = -1
	if _, err := (queueWeightedRandomSelector{}).Select(selection); err == nil {
		t.Fatal("直接调用选择器时应拒绝负数队列")
	}
}

func TestQueueWeightedRandomSelectorRouter(t *testing.T) {
	config := testRouteConfig()
	config.Rules = config.Rules[3:4]
	config.Rules[0].TicketCount = math.MaxInt
	router := mustRouter(t, mustRouteTable(t, config), WithRouteSelector(NewQueueWeightedRandomSelector()))
	decision, err := router.RouteNew(context.Background(), RouteRequest{Rule: testRule, TicketID: 1})
	if err != nil || decision.Owner != owner7 {
		t.Fatalf("公开随机选择器应能处理最大队列并路由: %+v，%v", decision, err)
	}
}

func TestWeightedPrioritySelector(t *testing.T) {
	if _, err := NewWeightedPrioritySelector(0); err == nil {
		t.Fatal("应拒绝零阈值")
	}
	if _, err := NewWeightedPrioritySelector(-1); err == nil {
		t.Fatal("应拒绝负数阈值")
	}
	selector, err := NewWeightedPrioritySelector(10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := selector.Select(RouteSelection{}); !errors.Is(err, ErrNoRoute) {
		t.Fatalf("空候选应无可用路由: %v", err)
	}
	for _, tc := range []struct {
		name       string
		candidates []RouteCandidate
		want       OwnerRef
	}{
		{"阈值内按权重优先", []RouteCandidate{
			{Owner: owner7, Weight: 2, TicketCount: 9},
			{Owner: owner8, Weight: 100, TicketCount: 10},
			{Owner: owner9, Weight: 1, TicketCount: 0},
		}, owner7},
		{"阈值内权重相同按节点编号", []RouteCandidate{
			{Owner: owner8, Weight: 3, TicketCount: 0},
			{Owner: owner7, Weight: 3, TicketCount: 9},
		}, owner7},
		{"全部达到阈值时先看队列", []RouteCandidate{
			{Owner: owner7, Weight: 1, TicketCount: 10},
			{Owner: owner8, Weight: 100, TicketCount: 11},
		}, owner7},
		{"队列相同时先看权重", []RouteCandidate{
			{Owner: owner7, Weight: 1, TicketCount: 10},
			{Owner: owner8, Weight: 3, TicketCount: 10},
		}, owner8},
		{"队列权重相同按节点编号", []RouteCandidate{
			{Owner: owner8, Weight: 3, TicketCount: 10},
			{Owner: owner7, Weight: 3, TicketCount: 10},
		}, owner7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for i := 0; i < 2; i++ {
				got, err := selector.Select(RouteSelection{Candidates: tc.candidates})
				if err != nil || got != tc.want {
					t.Fatalf("得到 %+v，期望 %+v，错误 %v", got, tc.want, err)
				}
				for left, right := 0, len(tc.candidates)-1; left < right; left, right = left+1, right-1 {
					tc.candidates[left], tc.candidates[right] = tc.candidates[right], tc.candidates[left]
				}
			}
		})
	}
	if _, err := selector.Select(RouteSelection{Candidates: []RouteCandidate{{Owner: owner7}, {Owner: owner8, TicketCount: -1}}}); err == nil {
		t.Fatal("直接调用选择器时应拒绝负数队列")
	}
	maxSelector, err := NewWeightedPrioritySelector(math.MaxInt)
	if err != nil {
		t.Fatal(err)
	}
	got, err := maxSelector.Select(RouteSelection{Candidates: []RouteCandidate{
		{Owner: owner7, Weight: 1, TicketCount: math.MaxInt - 1},
		{Owner: owner8, Weight: math.MaxUint32, TicketCount: math.MaxInt},
	}})
	if err != nil || got != owner7 {
		t.Fatalf("最大整数阈值和队列边界错误: %+v，%v", got, err)
	}
}

func TestRouteQueueSnapshotUpdatesAndRecovery(t *testing.T) {
	config := testRouteConfig()
	config.Rules[0].TicketCount = 9
	config.Rules[3].TicketCount = 9
	table := mustRouteTable(t, config)
	selector, err := NewWeightedPrioritySelector(10)
	if err != nil {
		t.Fatal(err)
	}
	router := mustRouter(t, table, WithRouteSelector(selector))
	oldRouter := mustRouter(t, table, WithRouteSelector(selector))
	ctx := context.Background()
	check := func(r *Router, want OwnerRef) {
		t.Helper()
		for ticketID := uint64(1); ticketID <= 20; ticketID++ {
			got, err := r.RouteNew(ctx, RouteRequest{Rule: testRule, TicketID: ticketID})
			if err != nil || got.Owner != want {
				t.Fatalf("路由目标错误: 得到 %+v，期望 %+v，错误 %v", got, want, err)
			}
		}
	}
	// 多次选路不会修改上报的队列长度，修改原始配置也不会改变快照。
	config.Rules[0].TicketCount = 99
	check(router, owner8)
	update := func(owner OwnerRef, endpoint string, weight uint32, count int) error {
		return router.UpdatePhysicalNode(
			PhysicalRoute{PhysicalNodeID: owner.PhysicalNodeID, Endpoint: common.Endpoint(endpoint), Enabled: true},
			[]RuleRoute{{LogicalNode: owner.LogicalNode, PhysicalNodeID: owner.PhysicalNodeID, Weight: weight, Enabled: true, TicketCount: count}},
		)
	}
	if err := update(owner8, "eight", 3, -1); err == nil || router.table != table {
		t.Fatalf("负数队列更新不应发布: %v", err)
	}
	check(router, owner8)
	if err := update(owner8, "eight", 3, 10); err != nil {
		t.Fatal(err)
	}
	check(router, owner7)
	check(oldRouter, owner8)
	if err := update(owner7, "seven", 2, 11); err != nil {
		t.Fatal(err)
	}
	check(router, owner8)
	if err := update(owner8, "eight", 3, 13); err != nil {
		t.Fatal(err)
	}
	check(router, owner7)
	if err := update(owner8, "eight", 3, 0); err != nil {
		t.Fatal(err)
	}
	check(router, owner8)
	if resolved, err := router.ResolveOwner(owner7); err != nil || resolved.Owner != owner7 {
		t.Fatalf("更新队列不应影响已有 Owner 解析: %+v，%v", resolved, err)
	}
}

func TestRouteQueueCandidateSnapshotIsIsolated(t *testing.T) {
	config := testRouteConfig()
	config.Rules[0].TicketCount = 4
	config.Rules[3].TicketCount = 9
	selector := routeSelectorFunc(func(selection RouteSelection) (OwnerRef, error) {
		if len(selection.Candidates) != 2 || selection.Candidates[0].TicketCount != 4 || selection.Candidates[1].TicketCount != 9 {
			t.Fatalf("候选队列快照错误: %+v", selection.Candidates)
		}
		selection.Candidates[0].TicketCount = 999
		return selection.Candidates[0].Owner, nil
	})
	router := mustRouter(t, mustRouteTable(t, config), WithRouteSelector(selector))
	for i := 0; i < 2; i++ {
		if _, err := router.RouteNew(context.Background(), RouteRequest{Rule: testRule, TicketID: 1}); err != nil {
			t.Fatal(err)
		}
	}
}
