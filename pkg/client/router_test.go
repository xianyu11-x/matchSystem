package client

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"testing"

	"github.com/xianyu11-x/matchSystem/pkg/common"
	"github.com/xianyu11-x/matchSystem/pkg/identity"
)

type routeSelectorFunc func(RouteSelection) (OwnerRef, error)

func (f routeSelectorFunc) Select(selection RouteSelection) (OwnerRef, error) {
	return f(selection)
}

func TestClientAndCoreIdentityTypesAreInterchangeable(t *testing.T) {
	var coreRule identity.RuleKey = RuleKey{Namespace: "1", RuleID: 2}
	var routeRule RuleKey = coreRule
	var coreOwner identity.OwnerRef = OwnerRef{LogicalNode: LogicalNodeKey{Rule: routeRule, PlacementID: 3}, PhysicalNodeID: 4}
	var routeOwner OwnerRef = coreOwner
	if routeOwner != coreOwner || routeOwner.LogicalNode.Rule != coreRule {
		t.Fatal("客户端与核心应共享同一套身份类型")
	}
}

var (
	testRule = identity.RuleKey{RuleID: 2}
	owner7   = OwnerRef{LogicalNode: LogicalNodeKey{Rule: testRule, PlacementID: 6}, PhysicalNodeID: 7}
	owner8   = OwnerRef{LogicalNode: LogicalNodeKey{Rule: testRule, PlacementID: 8}, PhysicalNodeID: 8}
	owner9   = OwnerRef{LogicalNode: LogicalNodeKey{Rule: testRule, PlacementID: 9}, PhysicalNodeID: 9}
	owner10  = OwnerRef{LogicalNode: LogicalNodeKey{Rule: testRule, PlacementID: 10}, PhysicalNodeID: 10}
	owner11  = OwnerRef{LogicalNode: LogicalNodeKey{Rule: identity.RuleKey{RuleID: 3}, PlacementID: 11}, PhysicalNodeID: 11}
)

func testRouteConfig() RouteTableConfig {
	return RouteTableConfig{
		PhysicalNodes: []PhysicalRoute{
			{PhysicalNodeID: 7, Endpoint: "seven", Enabled: true},
			{PhysicalNodeID: 8, Endpoint: "eight", Enabled: true},
			{PhysicalNodeID: 9, Endpoint: "nine", Enabled: true},
			{PhysicalNodeID: 10, Endpoint: "ten", Enabled: false},
			{PhysicalNodeID: 11, Endpoint: "eleven", Enabled: true},
		},
		Rules: []RuleRoute{
			{LogicalNode: owner8.LogicalNode, PhysicalNodeID: 8, Weight: 3, Enabled: true},
			{LogicalNode: owner9.LogicalNode, PhysicalNodeID: 9, Weight: 1, Enabled: false},
			{LogicalNode: owner10.LogicalNode, PhysicalNodeID: 10, Weight: 1, Enabled: true},
			{LogicalNode: owner7.LogicalNode, PhysicalNodeID: 7, Weight: 2, Enabled: true},
			{LogicalNode: owner11.LogicalNode, PhysicalNodeID: 11, Weight: 1, Enabled: true},
		},
	}
}

func mustRouteTable(t *testing.T, config RouteTableConfig) *RouteTable {
	t.Helper()
	table, err := NewRouteTable(config)
	if err != nil {
		t.Fatalf("创建路由表: %v", err)
	}
	return table
}

func mustRouter(t *testing.T, table *RouteTable, options ...RouterOption) *Router {
	t.Helper()
	router, err := NewRouter(table, options...)
	if err != nil {
		t.Fatalf("创建路由器: %v", err)
	}
	return router
}

func TestRouteSelectionSnapshotAndDecisionID(t *testing.T) {
	table := mustRouteTable(t, testRouteConfig())
	request := RouteRequest{Rule: testRule, TicketID: 3, RequestID: 5}
	selector := routeSelectorFunc(func(selection RouteSelection) (OwnerRef, error) {
		wantRequest := request
		wantRequest.AffinityKey = 3
		if selection.Request != wantRequest {
			t.Fatalf("选择器请求: 得到 %+v，期望 %+v", selection.Request, wantRequest)
		}
		want := []RouteCandidate{{Owner: owner8, Weight: 3}, {Owner: owner7, Weight: 2}}
		if !reflect.DeepEqual(selection.Candidates, want) {
			t.Fatalf("候选顺序或权重不正确: 得到 %+v，期望 %+v", selection.Candidates, want)
		}
		return owner7, nil
	})
	router := mustRouter(t, table, WithRouteSelector(selector))
	decision, err := router.RouteNew(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	// SHA-256(大端 uint64: 2, 3, 3, 5, 6, 7) 的前 64 位。
	if decision.DecisionID != 9325484210889104247 || decision.Owner != owner7 || decision.Endpoint != "seven" {
		t.Fatalf("路由决策不正确: %+v", decision)
	}
	request.AffinityKey = 3
	if repeated, err := router.RouteNew(context.Background(), request); err != nil || repeated != decision {
		t.Fatalf("显式和默认亲和键应等价: %+v, %v", repeated, err)
	}
}

func TestRuleAliasDoesNotAffectRouting(t *testing.T) {
	config := testRouteConfig()
	for index := range config.Rules {
		if config.Rules[index].LogicalNode.Rule.RuleID == testRule.RuleID {
			config.Rules[index].LogicalNode.Rule.Namespace = "配置别名"
		}
	}
	router := mustRouter(t, mustRouteTable(t, config), WithRouteSelector(routeSelectorFunc(func(selection RouteSelection) (OwnerRef, error) {
		if selection.Request.Rule.Namespace != "" {
			t.Fatalf("选择器请求不应包含别名: %+v", selection.Request)
		}
		for _, candidate := range selection.Candidates {
			if candidate.Owner.LogicalNode.Rule.Namespace != "" {
				t.Fatalf("选择器候选不应包含别名: %+v", candidate)
			}
		}
		owner := selection.Candidates[0].Owner
		owner.LogicalNode.Rule.Namespace = "伪造别名"
		return owner, nil
	})))
	first, err := router.RouteNew(context.Background(), RouteRequest{Rule: identity.RuleKey{Namespace: "请求别名", RuleID: 2}, TicketID: 5})
	if err != nil || first.Owner.LogicalNode.Rule.Namespace != "配置别名" {
		t.Fatalf("路由应返回配置别名: %+v, %v", first, err)
	}
	other, err := router.RouteNew(context.Background(), RouteRequest{Rule: identity.RuleKey{Namespace: "另一个别名", RuleID: 2}, TicketID: 5})
	if err != nil || other != first {
		t.Fatalf("请求别名不应影响路由或摘要: %+v, %v", other, err)
	}
	requestedOwner := first.Owner
	requestedOwner.LogicalNode.Rule.Namespace = "历史别名"
	resolved, err := router.ResolveOwner(requestedOwner)
	if err != nil || resolved.Owner != first.Owner {
		t.Fatalf("历史别名应解析到配置中的目标: %+v, %v", resolved, err)
	}

	baseline := mustRouter(t, mustRouteTable(t, config))
	decision, err := baseline.RouteNew(context.Background(), RouteRequest{Rule: identity.RuleKey{Namespace: "first", RuleID: 2}, TicketID: 5})
	if err != nil {
		t.Fatal(err)
	}
	for index := range config.Rules {
		config.Rules[index].LogicalNode.Rule.Namespace = "新配置别名"
	}
	renamed := mustRouter(t, mustRouteTable(t, config))
	changed, err := renamed.RouteNew(context.Background(), RouteRequest{Rule: identity.RuleKey{Namespace: "second", RuleID: 2}, TicketID: 5})
	if err != nil || changed.Owner.LogicalNode.Rule.Namespace != "新配置别名" || changed.Owner.String() != decision.Owner.String() || changed.DecisionID != decision.DecisionID {
		t.Fatalf("配置别名变更不应影响选择和摘要: 原=%+v，新=%+v，%v", decision, changed, err)
	}
}

func TestCustomSelectorCannotBypassEligibility(t *testing.T) {
	config := testRouteConfig()
	table := mustRouteTable(t, config)
	request := RouteRequest{Rule: testRule, TicketID: 1}
	invalid := []OwnerRef{
		{},
		owner9,  // 规则路由禁用。
		owner10, // 物理节点禁用。
		owner11, // 属于其他规则。
		{LogicalNode: owner8.LogicalNode, PhysicalNodeID: 7},
		{LogicalNode: LogicalNodeKey{Rule: testRule, PlacementID: 99}, PhysicalNodeID: 7},
	}
	for _, target := range invalid {
		t.Run(fmtOwner(target), func(t *testing.T) {
			router := mustRouter(t, table, WithRouteSelector(routeSelectorFunc(func(selection RouteSelection) (OwnerRef, error) {
				selection.Candidates[0].Owner = target
				selection.Candidates[0].Weight = math.MaxUint32
				return selection.Candidates[0].Owner, nil
			})))
			if _, err := router.RouteNew(context.Background(), request); !errors.Is(err, ErrInvalidRouteSelection) {
				t.Fatalf("应拒绝选择目标 %+v: %v", target, err)
			}
		})
	}
	// 修改配置切片或选择器快照均不能修改已创建的路由表。
	config.Rules[0].Enabled = false
	config.PhysicalNodes[1].Endpoint = "changed"
	router := mustRouter(t, table, WithRouteSelector(routeSelectorFunc(func(selection RouteSelection) (OwnerRef, error) {
		return selection.Candidates[0].Owner, nil
	})))
	if decision, err := router.RouteNew(context.Background(), request); err != nil || decision.Owner != owner8 || decision.Endpoint != "eight" {
		t.Fatalf("路由表快照被修改: %+v, %v", decision, err)
	}
}

func fmtOwner(owner OwnerRef) string {
	return fmt.Sprintf("rule_%d_placement_%d_physical_%d", owner.LogicalNode.Rule.RuleID, owner.LogicalNode.PlacementID, owner.PhysicalNodeID)
}

func TestRouteSelectorErrorsAndValidation(t *testing.T) {
	table := mustRouteTable(t, testRouteConfig())
	selectorErr := errors.New("自定义选择失败")
	called := false
	selector := routeSelectorFunc(func(RouteSelection) (OwnerRef, error) {
		called = true
		return OwnerRef{}, selectorErr
	})
	router := mustRouter(t, table, WithRouteSelector(selector))
	request := RouteRequest{Rule: testRule, TicketID: 1}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := router.RouteNew(ctx, request); !errors.Is(err, context.Canceled) || called {
		t.Fatalf("取消后不应选择路由: %v", err)
	}
	for _, bad := range []RouteRequest{{Rule: testRule}, {TicketID: 1}, {Rule: identity.RuleKey{RuleID: 99}, TicketID: 1}} {
		if _, err := router.RouteNew(context.Background(), bad); err == nil || called {
			t.Fatalf("无效或无路由请求不应调用选择器: %+v, %v", bad, err)
		}
	}
	if _, err := router.RouteNew(context.Background(), request); !errors.Is(err, selectorErr) || !called {
		t.Fatalf("应返回选择器错误: %v", err)
	}
	if _, err := NewRouter(nil); err == nil {
		t.Fatal("应拒绝空路由表")
	}
	if _, err := NewRouter(table, nil); err == nil {
		t.Fatal("应拒绝空选项")
	}
	if _, err := NewRouter(table, WithRouteSelector(nil)); err == nil {
		t.Fatal("应拒绝空选择器")
	}
}

func TestDefaultSelectorAndRouteReplacement(t *testing.T) {
	config := testRouteConfig()
	table := mustRouteTable(t, config)
	var router Router
	request := RouteRequest{Rule: testRule, TicketID: 3, RequestID: 5}
	if _, err := router.RouteNew(context.Background(), request); !errors.Is(err, ErrNoRoute) {
		t.Fatalf("空路由器应无可用路由: %v", err)
	}
	if err := router.Replace(table); err != nil {
		t.Fatal(err)
	}
	// 根据固定 SHA-256 输入，亲和键 1 选择 7，亲和键 4 选择 8。
	request.AffinityKey = 1
	decision, err := router.RouteNew(context.Background(), request)
	if err != nil || decision.Owner != owner7 || decision.DecisionID != 7121595354373995787 {
		t.Fatalf("固定路由向量不正确: %+v, %v", decision, err)
	}
	request.AffinityKey = 4
	if next, err := router.RouteNew(context.Background(), request); err != nil || next.Owner != owner8 {
		t.Fatalf("亲和键变化后目标不正确: %+v, %v", next, err)
	}
	for _, owner := range []OwnerRef{owner9, owner10} {
		if result, err := router.ResolveOwner(owner); err != nil || result.Owner != owner || result.Endpoint == "" {
			t.Fatalf("历史目标应可解析: %+v, %v", result, err)
		}
	}
	config.PhysicalNodes[0].Endpoint = common.Endpoint("new-seven")
	config.Rules = config.Rules[3:4]
	if err := router.Replace(mustRouteTable(t, config)); err != nil {
		t.Fatal(err)
	}
	if result, err := router.ResolveOwner(owner7); err != nil || result.Endpoint != "new-seven" {
		t.Fatalf("替换后应解析新端点: %+v, %v", result, err)
	}
	if _, err := router.ResolveOwner(owner8); !errors.Is(err, ErrOwnerNotFound) {
		t.Fatalf("已移除目标应找不到: %v", err)
	}
	if err := router.Replace(nil); err == nil {
		t.Fatal("应拒绝空替换表")
	}
	if result, err := router.ResolveOwner(owner7); err != nil || result.Endpoint != "new-seven" {
		t.Fatalf("无效替换不应改变路由表: %+v, %v", result, err)
	}
}

func TestReplacementKeepsCustomSelector(t *testing.T) {
	config := testRouteConfig()
	var calls int
	router := mustRouter(t, mustRouteTable(t, config), WithRouteSelector(routeSelectorFunc(func(selection RouteSelection) (OwnerRef, error) {
		calls++
		return selection.Candidates[len(selection.Candidates)-1].Owner, nil
	})))
	request := RouteRequest{Rule: testRule, TicketID: 4}
	if result, err := router.RouteNew(context.Background(), request); err != nil || result.Owner != owner7 {
		t.Fatalf("自定义选择失败: %+v, %v", result, err)
	}
	config.Rules = config.Rules[:1]
	if err := router.Replace(mustRouteTable(t, config)); err != nil {
		t.Fatal(err)
	}
	if result, err := router.RouteNew(context.Background(), request); err != nil || result.Owner != owner8 || calls != 2 {
		t.Fatalf("替换路由表后应继续使用自定义选择器: %+v, calls=%d, %v", result, calls, err)
	}
}

func TestNumericBoundsAndRouteTableValidation(t *testing.T) {
	max := uint64(math.MaxUint64)
	owner := OwnerRef{LogicalNode: LogicalNodeKey{Rule: identity.RuleKey{RuleID: max}, PlacementID: identity.PlacementID(max)}, PhysicalNodeID: identity.PhysicalNodeID(max)}
	config := RouteTableConfig{
		PhysicalNodes: []PhysicalRoute{{PhysicalNodeID: identity.PhysicalNodeID(max), Endpoint: "max", Enabled: true}},
		Rules:         []RuleRoute{{LogicalNode: owner.LogicalNode, PhysicalNodeID: identity.PhysicalNodeID(max), Weight: math.MaxUint32, Enabled: true}},
	}
	router := mustRouter(t, mustRouteTable(t, config))
	decision, err := router.RouteNew(context.Background(), RouteRequest{Rule: owner.LogicalNode.Rule, TicketID: max, AffinityKey: max, RequestID: max})
	if err != nil || decision.Owner != owner || decision.DecisionID == 0 {
		t.Fatalf("数值边界路由失败: %+v, %v", decision, err)
	}
	maxNamespace := config
	maxNamespace.Rules = append([]RuleRoute(nil), config.Rules...)
	maxNamespace.Rules[0].LogicalNode.Rule.Namespace = "最大规则别名"
	maxOwner := owner
	maxOwner.LogicalNode.Rule.Namespace = "最大规则别名"
	maxRouter := mustRouter(t, mustRouteTable(t, maxNamespace))
	if result, err := maxRouter.RouteNew(context.Background(), RouteRequest{Rule: maxOwner.LogicalNode.Rule, TicketID: max}); err != nil || result.Owner != maxOwner || result.DecisionID == 0 {
		t.Fatalf("最大命名空间路由失败: %+v, %v", result, err)
	}
	cases := []struct {
		name   string
		mutate func(*RouteTableConfig)
	}{
		{"zero_physical", func(c *RouteTableConfig) { c.PhysicalNodes[0].PhysicalNodeID = 0 }},
		{"empty_endpoint", func(c *RouteTableConfig) { c.PhysicalNodes[0].Endpoint = "" }},
		{"duplicate_physical", func(c *RouteTableConfig) { c.PhysicalNodes = append(c.PhysicalNodes, c.PhysicalNodes[0]) }},
		{"zero_rule", func(c *RouteTableConfig) { c.Rules[0].LogicalNode.Rule.RuleID = 0 }},
		{"zero_placement", func(c *RouteTableConfig) { c.Rules[0].LogicalNode.PlacementID = 0 }},
		{"zero_route_physical", func(c *RouteTableConfig) { c.Rules[0].PhysicalNodeID = 0 }},
		{"unknown_physical", func(c *RouteTableConfig) { c.Rules[0].PhysicalNodeID = 1 }},
		{"zero_weight", func(c *RouteTableConfig) { c.Rules[0].Weight = 0 }},
		{"negative_ticket_count", func(c *RouteTableConfig) { c.Rules[0].TicketCount = -1 }},
		{"duplicate_logical", func(c *RouteTableConfig) { c.Rules = append(c.Rules, c.Rules[0]) }},
		{"duplicate_logical_other_alias", func(c *RouteTableConfig) {
			second := c.Rules[0]
			second.LogicalNode.Rule.Namespace = "另一个别名"
			c.Rules = append(c.Rules, second)
		}},
		{"duplicate_rule_physical", func(c *RouteTableConfig) {
			second := c.Rules[0]
			second.LogicalNode.PlacementID = 1
			second.LogicalNode.Rule.Namespace = "另一个别名"
			c.Rules = append(c.Rules, second)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			candidate := RouteTableConfig{PhysicalNodes: append([]PhysicalRoute(nil), config.PhysicalNodes...), Rules: append([]RuleRoute(nil), config.Rules...)}
			tc.mutate(&candidate)
			if _, err := NewRouteTable(candidate); err == nil {
				t.Fatal("应拒绝无效路由表")
			}
		})
	}
}
