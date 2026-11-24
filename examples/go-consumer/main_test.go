package main

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"sort"
	"testing"

	"github.com/xianyu11-x/matchSystem/pkg/client"
	"github.com/xianyu11-x/matchSystem/pkg/common"
	"github.com/xianyu11-x/matchSystem/pkg/identity"
	"github.com/xianyu11-x/matchSystem/pkg/matchsystem"
)

type recordingSelector struct {
	candidates []client.RouteCandidate
}

func (s *recordingSelector) Select(selection client.RouteSelection) (client.OwnerRef, error) {
	s.candidates = append([]client.RouteCandidate(nil), selection.Candidates...)
	if len(s.candidates) == 0 {
		return client.OwnerRef{}, client.ErrNoRoute
	}
	return s.candidates[0].Owner, nil
}

func TestRoutedTicketsAreMatchedAndConsumed(t *testing.T) {
	ctx := context.Background()
	selector := &recordingSelector{}
	consumer, err := newConsumer(ctx, client.WithRouteSelector(selector))
	if err != nil {
		t.Fatalf("创建消费者: %v", err)
	}
	wantOwner := identity.OwnerRef{LogicalNode: consumer.key, PhysicalNodeID: consumer.physical.ID()}
	for i, id := range []uint64{1001, 1002} {
		owner, err := consumer.submit(ctx, &common.Ticket{
			TicketID: id, CreatedAt: int64(id), StringLists: map[string][]string{"partition": {"blue"}},
		})
		if err != nil {
			t.Fatalf("投递 Ticket %d: %v", id, err)
		}
		if owner.String() != wantOwner.String() {
			t.Fatalf("路由结果: 得到 %v，期望 %v", owner, wantOwner)
		}
		if len(selector.candidates) != 1 || selector.candidates[0].TicketCount != i || selector.candidates[0].Weight != 1 {
			t.Fatalf("投递前的路由候选应反映已发布的队列数量和权重: %+v", selector.candidates)
		}
		if _, found, err := consumer.physical.Get(ctx, owner, id); err != nil || !found {
			t.Fatalf("匹配前查询 Ticket %d: found=%v, error=%v", id, found, err)
		}
	}
	if _, err := consumer.router.RouteNew(ctx, client.RouteRequest{Rule: consumer.key.Rule, TicketID: 1003}); err != nil {
		t.Fatalf("查询投递后的路由状态: %v", err)
	}
	if len(selector.candidates) != 1 || selector.candidates[0].TicketCount != 2 {
		t.Fatalf("投递后应发布队列数量 2: %+v", selector.candidates)
	}
	if err := consumer.physical.BeginMatchRound(ctx, 2000); err != nil {
		t.Fatalf("开始匹配轮次: %v", err)
	}
	result, err := consumer.produceMatch(ctx)
	if err != nil {
		t.Fatalf("生成 Match: %v", err)
	}
	if result.LogicalNode.String() != consumer.key.String() || result.Match == nil || len(result.Match.Tickets) != 2 {
		t.Fatalf("Match 节点或人数不正确: %+v", result)
	}
	ids := make([]uint64, 0, 2)
	for _, ticket := range result.Match.Tickets {
		if ticket == nil {
			t.Fatal("Match 包含空 Ticket")
		}
		ids = append(ids, ticket.TicketID)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	if !reflect.DeepEqual(ids, []uint64{1001, 1002}) {
		t.Fatalf("Match 成员: 得到 %v，期望 [1001 1002]", ids)
	}
	if got := result.Match.Facts.Int64Values["count"]; got != 2 {
		t.Fatalf("Match Fact 人数: 得到 %d，期望 2", got)
	}
	if _, err := consumer.router.RouteNew(ctx, client.RouteRequest{Rule: consumer.key.Rule, TicketID: 1004}); err != nil {
		t.Fatalf("查询匹配后的路由状态: %v", err)
	}
	if len(selector.candidates) != 1 || selector.candidates[0].TicketCount != 0 {
		t.Fatalf("匹配后应发布队列数量 0: %+v", selector.candidates)
	}
	for _, id := range ids {
		if _, found, err := consumer.physical.Get(ctx, wantOwner, id); err != nil || found {
			t.Fatalf("匹配后查询 Ticket %d: found=%v, error=%v", id, found, err)
		}
	}
	if duplicate, err := consumer.produceMatch(ctx); !errors.Is(err, matchsystem.ErrNoLogicalNodeAvailable) || duplicate.Match != nil {
		t.Fatalf("本轮再次匹配: Match=%v, error=%v", duplicate.Match, err)
	}
	if err := consumer.physical.BeginMatchRound(ctx, 2001); err != nil {
		t.Fatalf("开始下一匹配轮次: %v", err)
	}
	if duplicate, err := consumer.produceMatch(ctx); !errors.Is(err, matchsystem.ErrNoLogicalNodeAvailable) || duplicate.Match != nil {
		t.Fatalf("下一轮再次匹配: Match=%v, error=%v", duplicate.Match, err)
	}
	for _, id := range ids {
		if _, found, err := consumer.physical.Get(ctx, wantOwner, id); err != nil || found {
			t.Fatalf("下一轮查询已消费的 Ticket %d: found=%v, error=%v", id, found, err)
		}
	}
}

func TestDifferentPartitionsRemainQueued(t *testing.T) {
	ctx := context.Background()
	consumer, err := newConsumer(ctx)
	if err != nil {
		t.Fatalf("创建消费者: %v", err)
	}
	var owner identity.OwnerRef
	for i, partition := range []string{"blue", "red"} {
		owner, err = consumer.submit(ctx, &common.Ticket{
			TicketID: uint64(i + 1), CreatedAt: int64(i + 1), StringLists: map[string][]string{"partition": {partition}},
		})
		if err != nil {
			t.Fatalf("投递分区 %s 的 Ticket: %v", partition, err)
		}
	}
	if err := consumer.physical.BeginMatchRound(ctx, 100); err != nil {
		t.Fatalf("开始匹配轮次: %v", err)
	}
	result, err := consumer.produceMatch(ctx)
	if err != nil || result.Match != nil {
		t.Fatalf("不同分区不应匹配: Match=%v, error=%v", result.Match, err)
	}
	for _, id := range []uint64{1, 2} {
		if _, found, err := consumer.physical.Get(ctx, owner, id); err != nil || !found {
			t.Fatalf("未匹配的 Ticket %d 应继续等待: found=%v, error=%v", id, found, err)
		}
	}
}

func TestRouteSnapshotLifecycle(t *testing.T) {
	ctx := context.Background()
	consumer, err := newConsumer(ctx)
	if err != nil {
		t.Fatalf("创建消费者: %v", err)
	}
	owner := consumer.routeOwner
	request := client.RouteRequest{Rule: consumer.key.Rule, TicketID: 1003}

	// 没有节点上报时，空路由表不能选择或解析目标。
	if err := consumer.router.UpdatePhysicalNode(client.PhysicalRoute{PhysicalNodeID: consumer.physical.ID()}, nil); err != nil {
		t.Fatalf("清除上报: %v", err)
	}
	if _, err := consumer.router.RouteNew(ctx, request); !errors.Is(err, client.ErrNoRoute) {
		t.Fatalf("无上报时不应有新路由: %v", err)
	}
	if _, err := consumer.router.ResolveOwner(owner); !errors.Is(err, client.ErrOwnerNotFound) {
		t.Fatalf("无上报时不应解析 Owner: %v", err)
	}
	if err := consumer.publishRoutes(); err != nil {
		t.Fatalf("发布节点快照: %v", err)
	}
	decision, err := consumer.router.RouteNew(ctx, request)
	if err != nil || decision.Owner.String() != owner.String() || decision.Endpoint != consumer.deployment.endpoint {
		t.Fatalf("发布后应能选中节点: decision=%+v, error=%v", decision, err)
	}

	for _, id := range []uint64{1001, 1002} {
		if _, err := consumer.submit(ctx, &common.Ticket{
			TicketID: id, CreatedAt: int64(id), StringLists: map[string][]string{"partition": {"blue"}},
		}); err != nil {
			t.Fatalf("投递 Ticket %d: %v", id, err)
		}
	}
	if err := consumer.physical.BeginDrain(ctx, consumer.key); err != nil {
		t.Fatalf("开始排空: %v", err)
	}
	if err := consumer.publishRoutes(); err != nil {
		t.Fatalf("发布排空状态: %v", err)
	}
	if _, err := consumer.router.RouteNew(ctx, request); !errors.Is(err, client.ErrNoRoute) {
		t.Fatalf("排空中的节点不应接收新路由: %v", err)
	}
	if resolved, err := consumer.router.ResolveOwner(owner); err != nil || resolved.Endpoint != consumer.deployment.endpoint {
		t.Fatalf("排空时应继续解析已有 Owner: resolved=%+v, error=%v", resolved, err)
	}
	if err := consumer.physical.BeginMatchRound(ctx, 2000); err != nil {
		t.Fatalf("开始排空期间的匹配轮次: %v", err)
	}
	result, err := consumer.produceMatch(ctx)
	if err != nil || result.Match == nil || len(result.Match.Tickets) != 2 {
		t.Fatalf("排空期间应消费已有 Ticket: result=%+v, error=%v", result, err)
	}
	if resolved, err := consumer.router.ResolveOwner(owner); err != nil || resolved.Endpoint != consumer.deployment.endpoint {
		t.Fatalf("队列排空后停止前仍应解析 Owner: resolved=%+v, error=%v", resolved, err)
	}
	if err := consumer.physical.Stop(ctx, consumer.key); err != nil {
		t.Fatalf("停止已排空的节点: %v", err)
	}
	if err := consumer.publishRoutes(); err != nil {
		t.Fatalf("发布停止后的空快照: %v", err)
	}
	if _, err := consumer.router.ResolveOwner(owner); !errors.Is(err, client.ErrOwnerNotFound) {
		t.Fatalf("停止并下线后不应解析 Owner: %v", err)
	}
	if _, err := consumer.router.RouteNew(ctx, request); !errors.Is(err, client.ErrNoRoute) {
		t.Fatalf("停止并下线后不应有新路由: %v", err)
	}
}

func TestRouteSnapshotRequiresEnabledDeployment(t *testing.T) {
	for _, test := range []struct {
		name    string
		disable func(*deploymentConfig)
	}{
		{name: "physical", disable: func(config *deploymentConfig) { config.physicalEnabled = false }},
		{name: "rule", disable: func(config *deploymentConfig) { config.ruleEnabled = false }},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			consumer, err := newConsumer(ctx)
			if err != nil {
				t.Fatalf("创建消费者: %v", err)
			}
			test.disable(&consumer.deployment)
			if err := consumer.publishRoutes(); err != nil {
				t.Fatalf("发布禁用配置: %v", err)
			}
			if _, err := consumer.router.RouteNew(ctx, client.RouteRequest{Rule: consumer.key.Rule, TicketID: 1}); !errors.Is(err, client.ErrNoRoute) {
				t.Fatalf("禁用部署不应接收新路由: %v", err)
			}
			if _, err := consumer.router.ResolveOwner(consumer.routeOwner); err != nil {
				t.Fatalf("禁用部署应保留已有 Owner: %v", err)
			}
		})
	}
}

func TestRouteSnapshotRequiresMatchingDeploymentConfig(t *testing.T) {
	ctx := context.Background()
	consumer, err := newConsumer(ctx)
	if err != nil {
		t.Fatalf("创建消费者: %v", err)
	}
	consumer.key.Rule.Namespace = "另一展示别名"
	if err := consumer.publishRoutes(); err != nil {
		t.Fatalf("Namespace 展示别名不同不应影响部署身份: %v", err)
	}
	consumer.key.PlacementID++
	if err := consumer.publishRoutes(); err == nil {
		t.Fatal("缺少逻辑节点部署配置时不应发布")
	}
	if _, err := consumer.router.ResolveOwner(consumer.routeOwner); err != nil {
		t.Fatalf("配置缺失不应破坏已发布的路由: %v", err)
	}
}

func TestRoutePublishFailureReturnsCommittedResult(t *testing.T) {
	ctx := context.Background()
	selector := &recordingSelector{}
	consumer, err := newConsumer(ctx, client.WithRouteSelector(selector))
	if err != nil {
		t.Fatalf("创建消费者: %v", err)
	}

	consumer.deployment.weight = 0
	owner, err := consumer.submit(ctx, &common.Ticket{
		TicketID: 1001, CreatedAt: 1, StringLists: map[string][]string{"partition": {"blue"}},
	})
	if err == nil || owner.String() != consumer.routeOwner.String() {
		t.Fatalf("上报失败应返回已投递 Ticket 的 Owner 和错误: owner=%+v, error=%v", owner, err)
	}
	if _, found, err := consumer.physical.Get(ctx, owner, 1001); err != nil || !found {
		t.Fatalf("上报失败不应撤销已投递的 Ticket: found=%v, error=%v", found, err)
	}
	consumer.deployment.weight = 1
	if err := consumer.publishRoutes(); err != nil {
		t.Fatalf("修复配置并重新发布: %v", err)
	}
	if _, err := consumer.submit(ctx, &common.Ticket{
		TicketID: 1002, CreatedAt: 2, StringLists: map[string][]string{"partition": {"blue"}},
	}); err != nil {
		t.Fatalf("投递第二张 Ticket: %v", err)
	}
	if err := consumer.physical.BeginMatchRound(ctx, 100); err != nil {
		t.Fatalf("开始匹配轮次: %v", err)
	}
	consumer.deployment.weight = 0
	result, err := consumer.produceMatch(ctx)
	if err == nil || result.Match == nil || len(result.Match.Tickets) != 2 {
		t.Fatalf("上报失败应保留已生成的 Match 和错误: result=%+v, error=%v", result, err)
	}
	consumer.deployment.weight = 1
	if err := consumer.publishRoutes(); err != nil {
		t.Fatalf("修复配置并重新发布匹配状态: %v", err)
	}
	if _, err := consumer.router.RouteNew(ctx, client.RouteRequest{Rule: consumer.key.Rule, TicketID: 1003}); err != nil {
		t.Fatalf("查询修复后的路由: %v", err)
	}
	if len(selector.candidates) != 1 || selector.candidates[0].TicketCount != 0 {
		t.Fatalf("重新发布后队列数量应为 0: %+v", selector.candidates)
	}
}

func TestRunOutput(t *testing.T) {
	var output bytes.Buffer
	if err := run(context.Background(), &output); err != nil {
		t.Fatalf("运行示例: %v", err)
	}
	if got, want := output.String(), "matched: 1001,1002\nremaining: 0\nduplicate: false\n"; got != want {
		t.Fatalf("输出: 得到 %q，期望 %q", got, want)
	}
}
