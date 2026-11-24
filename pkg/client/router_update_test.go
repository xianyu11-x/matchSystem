package client

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/xianyu11-x/matchSystem/pkg/identity"
)

func TestUpdatePhysicalNodeSnapshotAndOrder(t *testing.T) {
	table := mustRouteTable(t, testRouteConfig())
	var candidates []RouteCandidate
	selector := WithRouteSelector(routeSelectorFunc(func(selection RouteSelection) (OwnerRef, error) {
		candidates = append([]RouteCandidate(nil), selection.Candidates...)
		return selection.Candidates[1].Owner, nil
	}))
	router := mustRouter(t, table, selector)
	untouched := mustRouter(t, table, selector)

	newOwner := OwnerRef{LogicalNode: LogicalNodeKey{Rule: testRule, PlacementID: 90}, PhysicalNodeID: 9}
	newOtherOwner := OwnerRef{LogicalNode: LogicalNodeKey{Rule: owner11.LogicalNode.Rule, PlacementID: 90}, PhysicalNodeID: 9}
	physical := PhysicalRoute{PhysicalNodeID: 9, Endpoint: "new-nine", Enabled: true}
	routes := []RuleRoute{
		{LogicalNode: newOwner.LogicalNode, PhysicalNodeID: 9, Weight: 17, Enabled: true},
		{LogicalNode: newOtherOwner.LogicalNode, PhysicalNodeID: 9, Weight: 4, Enabled: true},
	}
	if err := router.UpdatePhysicalNode(physical, routes); err != nil {
		t.Fatal(err)
	}
	// 更新完成后调用方修改输入切片，不应改变路由快照。
	routes[0].Weight = 99

	checkSelection := func(r *Router, rule identity.RuleKey, want []RouteCandidate, wantOwner OwnerRef, wantEndpoint string) {
		t.Helper()
		decision, err := r.RouteNew(context.Background(), RouteRequest{Rule: rule, TicketID: 1})
		if err != nil || decision.Owner != wantOwner || string(decision.Endpoint) != wantEndpoint {
			t.Fatalf("路由目标不正确: %+v, %v", decision, err)
		}
		if !reflect.DeepEqual(candidates, want) {
			t.Fatalf("候选顺序或权重不正确: 得到 %+v，期望 %+v", candidates, want)
		}
	}
	wantRule := []RouteCandidate{{Owner: owner8, Weight: 3}, {Owner: newOwner, Weight: 17}, {Owner: owner7, Weight: 2}}
	checkSelection(router, testRule, wantRule, newOwner, "new-nine")
	checkSelection(router, owner11.LogicalNode.Rule, []RouteCandidate{{Owner: owner11, Weight: 1}, {Owner: newOtherOwner, Weight: 4}}, newOtherOwner, "new-nine")
	if result, err := router.ResolveOwner(newOwner); err != nil || result.Endpoint != "new-nine" {
		t.Fatalf("新目标应解析到新端点: %+v, %v", result, err)
	}
	if _, err := router.ResolveOwner(owner9); !errors.Is(err, ErrOwnerNotFound) {
		t.Fatalf("替换逻辑节点后旧目标应失效: %v", err)
	}
	checkSelection(untouched, testRule, []RouteCandidate{{Owner: owner8, Weight: 3}, {Owner: owner7, Weight: 2}}, owner7, "seven")
	if result, err := untouched.ResolveOwner(owner9); err != nil || result.Endpoint != "nine" {
		t.Fatalf("共享旧路由表的路由器不应受影响: %+v, %v", result, err)
	}
	if _, err := untouched.ResolveOwner(newOwner); !errors.Is(err, ErrOwnerNotFound) {
		t.Fatalf("旧快照不应包含新目标: %v", err)
	}

	// 相同更新重复执行时，已有候选和新增候选均保持顺序。
	routes[0].Weight = 17
	if err := router.UpdatePhysicalNode(physical, routes); err != nil {
		t.Fatal(err)
	}
	checkSelection(router, testRule, wantRule, newOwner, "new-nine")
	checkSelection(router, owner11.LogicalNode.Rule, []RouteCandidate{{Owner: owner11, Weight: 1}, {Owner: newOtherOwner, Weight: 4}}, newOtherOwner, "new-nine")
	if err := router.UpdatePhysicalNode(physical, routes[:1]); err != nil {
		t.Fatal(err)
	}
	if _, err := router.ResolveOwner(newOtherOwner); !errors.Is(err, ErrOwnerNotFound) {
		t.Fatalf("替换列表中省略的规则应被移除: %v", err)
	}
	checkSelection(router, testRule, wantRule, newOwner, "new-nine")
	if result, err := router.ResolveOwner(owner11); err != nil || result.Endpoint != "eleven" {
		t.Fatalf("移除规则不应影响其他物理节点: %+v, %v", result, err)
	}
}

func TestUpdatePhysicalNodeDisabledAndEmptyRoutes(t *testing.T) {
	var candidates []RouteCandidate
	selector := WithRouteSelector(routeSelectorFunc(func(selection RouteSelection) (OwnerRef, error) {
		candidates = append([]RouteCandidate(nil), selection.Candidates...)
		return selection.Candidates[len(selection.Candidates)-1].Owner, nil
	}))
	original := mustRouteTable(t, testRouteConfig())
	router := mustRouter(t, original, selector)
	untouched := mustRouter(t, original)
	physical := PhysicalRoute{PhysicalNodeID: 9, Endpoint: "updated-nine", Enabled: false}
	route := RuleRoute{LogicalNode: owner9.LogicalNode, PhysicalNodeID: 9, Weight: 3, Enabled: false}
	otherOwner := OwnerRef{LogicalNode: LogicalNodeKey{Rule: owner11.LogicalNode.Rule, PlacementID: 90}, PhysicalNodeID: 9}
	otherRoute := RuleRoute{LogicalNode: otherOwner.LogicalNode, PhysicalNodeID: 9, Weight: 4, Enabled: true}
	if err := router.UpdatePhysicalNode(physical, []RuleRoute{route, otherRoute}); err != nil {
		t.Fatal(err)
	}
	if result, err := router.ResolveOwner(owner9); err != nil || result.Endpoint != "updated-nine" {
		t.Fatalf("节点和规则禁用时原目标仍应可解析: %+v, %v", result, err)
	}
	if result, err := router.ResolveOwner(otherOwner); err != nil || result.Endpoint != "updated-nine" {
		t.Fatalf("禁用的物理节点上其他规则仍应可解析: %+v, %v", result, err)
	}
	if result, err := router.ResolveOwner(owner8); err != nil || result.Endpoint != "eight" {
		t.Fatalf("其他物理节点不应受影响: %+v, %v", result, err)
	}
	if err := router.UpdatePhysicalNode(PhysicalRoute{PhysicalNodeID: 9}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := router.ResolveOwner(owner9); !errors.Is(err, ErrOwnerNotFound) {
		t.Fatalf("节点下线后原目标应失效: %v", err)
	}
	if _, err := router.ResolveOwner(otherOwner); !errors.Is(err, ErrOwnerNotFound) {
		t.Fatalf("节点下线后其他规则的目标应失效: %v", err)
	}
	if _, ok := router.table.physical(9); ok {
		t.Fatal("节点下线应移除物理节点")
	}
	if result, err := untouched.ResolveOwner(owner9); err != nil || result.Endpoint != "nine" {
		t.Fatalf("共享旧快照的路由器不应受节点下线影响: %+v, %v", result, err)
	}
	if result, err := router.ResolveOwner(owner11); err != nil || result.Endpoint != "eleven" {
		t.Fatalf("节点下线不应影响其他规则: %+v, %v", result, err)
	}
	physical.Enabled = true
	route.Enabled = true
	if err := router.UpdatePhysicalNode(physical, []RuleRoute{route}); err != nil {
		t.Fatalf("下线后物理节点应可重新上线: %v", err)
	}
	if result, err := router.ResolveOwner(owner9); err != nil || result.Endpoint != "updated-nine" {
		t.Fatalf("重新配置后目标应可解析: %+v, %v", result, err)
	}
	if decision, err := router.RouteNew(context.Background(), RouteRequest{Rule: testRule, TicketID: 1}); err != nil || decision.Owner != owner9 {
		t.Fatalf("重新上线的目标应可参与路由: %+v, %v", decision, err)
	}
	want := []RouteCandidate{{Owner: owner8, Weight: 3}, {Owner: owner7, Weight: 2}, {Owner: owner9, Weight: 3}}
	if !reflect.DeepEqual(candidates, want) {
		t.Fatalf("重新上线的节点应追加到候选列表末尾: 得到 %+v，期望 %+v", candidates, want)
	}
}

func TestUpdatePhysicalNodeAddsNode(t *testing.T) {
	owner := OwnerRef{LogicalNode: LogicalNodeKey{Rule: testRule, PlacementID: 99}, PhysicalNodeID: 99}
	physical := PhysicalRoute{PhysicalNodeID: 99, Endpoint: "ninety-nine", Enabled: true}
	routes := []RuleRoute{{LogicalNode: owner.LogicalNode, PhysicalNodeID: 99, Weight: 5, Enabled: true}}

	var empty Router
	if err := empty.UpdatePhysicalNode(physical, routes); err != nil {
		t.Fatalf("零值路由器应能新增节点: %v", err)
	}
	decision, err := empty.RouteNew(context.Background(), RouteRequest{Rule: testRule, TicketID: 1})
	if err != nil || decision.Owner != owner || decision.Endpoint != "ninety-nine" {
		t.Fatalf("零值路由器新增节点后应可路由: %+v, %v", decision, err)
	}
	if resolved, err := empty.ResolveOwner(owner); err != nil || resolved.Endpoint != "ninety-nine" {
		t.Fatalf("零值路由器新增的目标应可解析: %+v, %v", resolved, err)
	}
	emptyTableRouter := mustRouter(t, mustRouteTable(t, RouteTableConfig{}))
	if err := emptyTableRouter.UpdatePhysicalNode(physical, routes); err != nil {
		t.Fatalf("空路由表应能新增节点: %v", err)
	}
	if resolved, err := emptyTableRouter.ResolveOwner(owner); err != nil || resolved.Endpoint != "ninety-nine" {
		t.Fatalf("空路由表新增的目标应可解析: %+v, %v", resolved, err)
	}

	var candidates []RouteCandidate
	selector := WithRouteSelector(routeSelectorFunc(func(selection RouteSelection) (OwnerRef, error) {
		candidates = append([]RouteCandidate(nil), selection.Candidates...)
		return selection.Candidates[len(selection.Candidates)-1].Owner, nil
	}))
	original := mustRouteTable(t, testRouteConfig())
	router := mustRouter(t, original, selector)
	untouched := mustRouter(t, original)
	if err := router.UpdatePhysicalNode(physical, routes); err != nil {
		t.Fatalf("应能新增物理节点: %v", err)
	}
	decision, err = router.RouteNew(context.Background(), RouteRequest{Rule: testRule, TicketID: 1})
	if err != nil || decision.Owner != owner || decision.Endpoint != "ninety-nine" {
		t.Fatalf("新增目标应可参与路由: %+v, %v", decision, err)
	}
	want := []RouteCandidate{{Owner: owner8, Weight: 3}, {Owner: owner7, Weight: 2}, {Owner: owner, Weight: 5}}
	if !reflect.DeepEqual(candidates, want) {
		t.Fatalf("新增节点应追加到候选列表末尾: 得到 %+v，期望 %+v", candidates, want)
	}
	if resolved, err := router.ResolveOwner(owner8); err != nil || resolved.Endpoint != "eight" {
		t.Fatalf("新增节点不应影响已有目标: %+v, %v", resolved, err)
	}
	if _, err := untouched.ResolveOwner(owner); !errors.Is(err, ErrOwnerNotFound) {
		t.Fatalf("共享旧快照的路由器不应看到新目标: %v", err)
	}
}

func TestUpdatePhysicalNodeInvalidIsAtomic(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*PhysicalRoute, *[]RuleRoute)
	}{
		{"disabled_empty_endpoint", func(p *PhysicalRoute, _ *[]RuleRoute) { p.Enabled = false; p.Endpoint = "" }},
		{"foreign_physical", func(_ *PhysicalRoute, r *[]RuleRoute) { (*r)[0].PhysicalNodeID = 7 }},
		{"zero_route_physical", func(_ *PhysicalRoute, r *[]RuleRoute) { (*r)[0].PhysicalNodeID = 0 }},
		{"unknown_route_physical", func(_ *PhysicalRoute, r *[]RuleRoute) { (*r)[0].PhysicalNodeID = 99 }},
		{"zero_weight", func(_ *PhysicalRoute, r *[]RuleRoute) { (*r)[0].Weight = 0 }},
		{"negative_ticket_count", func(_ *PhysicalRoute, r *[]RuleRoute) { (*r)[0].TicketCount = -1 }},
		{"invalid_rule", func(_ *PhysicalRoute, r *[]RuleRoute) { (*r)[0].LogicalNode.Rule.RuleID = 0 }},
		{"invalid_placement", func(_ *PhysicalRoute, r *[]RuleRoute) { (*r)[0].LogicalNode.PlacementID = 0 }},
		{"logical_collision", func(_ *PhysicalRoute, r *[]RuleRoute) { (*r)[0].LogicalNode = owner7.LogicalNode }},
		{"duplicate_logical", func(_ *PhysicalRoute, r *[]RuleRoute) { *r = append(*r, (*r)[0]) }},
		{"duplicate_rule_on_node", func(_ *PhysicalRoute, r *[]RuleRoute) {
			duplicate := (*r)[0]
			duplicate.LogicalNode.PlacementID = 99
			duplicate.Enabled = false
			*r = append(*r, duplicate)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			original := mustRouteTable(t, testRouteConfig())
			want := mustRouteTable(t, testRouteConfig())
			router := mustRouter(t, original)
			physical := PhysicalRoute{PhysicalNodeID: 8, Endpoint: "changed-eight", Enabled: true}
			routes := []RuleRoute{{LogicalNode: owner8.LogicalNode, PhysicalNodeID: 8, Weight: 5, Enabled: true}}
			tc.mutate(&physical, &routes)
			if err := router.UpdatePhysicalNode(physical, routes); err == nil {
				t.Fatal("应拒绝无效的整体更新")
			}
			if router.table != original || !reflect.DeepEqual(router.table, want) {
				t.Fatal("无效更新不应改变路由表或已发布的快照")
			}
			if result, err := router.ResolveOwner(owner8); err != nil || result.Endpoint != "eight" {
				t.Fatalf("无效更新不应改变目标端点: %+v, %v", result, err)
			}
		})
	}
}

func TestUpdatePhysicalNodeInvalidNewNodeIsAtomic(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*PhysicalRoute, *[]RuleRoute)
	}{
		{"zero_physical", func(p *PhysicalRoute, r *[]RuleRoute) { p.PhysicalNodeID = 0; (*r)[0].PhysicalNodeID = 0 }},
		{"empty_endpoint", func(p *PhysicalRoute, _ *[]RuleRoute) { p.Endpoint = "" }},
		{"foreign_physical", func(_ *PhysicalRoute, r *[]RuleRoute) { (*r)[0].PhysicalNodeID = 7 }},
		{"zero_weight", func(_ *PhysicalRoute, r *[]RuleRoute) { (*r)[0].Weight = 0 }},
		{"invalid_rule", func(_ *PhysicalRoute, r *[]RuleRoute) { (*r)[0].LogicalNode.Rule.RuleID = 0 }},
		{"logical_collision", func(_ *PhysicalRoute, r *[]RuleRoute) { (*r)[0].LogicalNode = owner7.LogicalNode }},
		{"duplicate_logical", func(_ *PhysicalRoute, r *[]RuleRoute) { *r = append(*r, (*r)[0]) }},
		{"duplicate_rule_on_node", func(_ *PhysicalRoute, r *[]RuleRoute) {
			duplicate := (*r)[0]
			duplicate.LogicalNode.PlacementID++
			*r = append(*r, duplicate)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			original := mustRouteTable(t, testRouteConfig())
			want := mustRouteTable(t, testRouteConfig())
			router := mustRouter(t, original)
			owner := OwnerRef{LogicalNode: LogicalNodeKey{Rule: testRule, PlacementID: 99}, PhysicalNodeID: 99}
			physical := PhysicalRoute{PhysicalNodeID: 99, Endpoint: "ninety-nine", Enabled: true}
			routes := []RuleRoute{{LogicalNode: owner.LogicalNode, PhysicalNodeID: 99, Weight: 5, Enabled: true}}
			tc.mutate(&physical, &routes)
			if err := router.UpdatePhysicalNode(physical, routes); err == nil {
				t.Fatal("应拒绝无效的新节点")
			}
			if router.table != original || !reflect.DeepEqual(router.table, want) {
				t.Fatal("无效新增不应发布路由表或修改已有快照")
			}
			if _, ok := router.table.physical(99); ok {
				t.Fatal("无效新增不应发布节点端点")
			}
			if _, err := router.ResolveOwner(owner); !errors.Is(err, ErrOwnerNotFound) {
				t.Fatalf("无效新增不应发布目标规则: %v", err)
			}
			if resolved, err := router.ResolveOwner(owner8); err != nil || resolved.Endpoint != "eight" {
				t.Fatalf("无效新增不应影响已有目标: %+v, %v", resolved, err)
			}
		})
	}
}

func TestUpdatePhysicalNodeOfflineIsIdempotent(t *testing.T) {
	var empty Router
	if err := empty.UpdatePhysicalNode(PhysicalRoute{PhysicalNodeID: 9}, nil); err != nil {
		t.Fatalf("空路由器下线不存在的节点应成功: %v", err)
	}
	if empty.table != nil {
		t.Fatal("空路由器重复下线不应发布路由表")
	}
	table := mustRouteTable(t, testRouteConfig())
	router := mustRouter(t, table)
	if err := router.UpdatePhysicalNode(PhysicalRoute{PhysicalNodeID: 99, Endpoint: "ignored", Enabled: true}, []RuleRoute{}); err != nil {
		t.Fatalf("下线不存在的物理节点应成功: %v", err)
	}
	if router.table != table {
		t.Fatal("下线不存在的物理节点不应改变路由表")
	}
	if err := router.UpdatePhysicalNode(PhysicalRoute{PhysicalNodeID: 9}, nil); err != nil {
		t.Fatal(err)
	}
	afterOffline := router.table
	if err := router.UpdatePhysicalNode(PhysicalRoute{PhysicalNodeID: 9}, nil); err != nil || router.table != afterOffline {
		t.Fatalf("重复下线同一节点应成功且保持快照: %v", err)
	}
	for _, current := range []*Router{&empty, router} {
		before := current.table
		if err := current.UpdatePhysicalNode(PhysicalRoute{}, nil); err == nil || current.table != before {
			t.Fatalf("应拒绝零物理节点标识且保持快照: %v", err)
		}
	}
}
