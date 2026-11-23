package main

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"sort"
	"testing"

	"github.com/xianyu11-x/matchSystem/pkg/common"
	"github.com/xianyu11-x/matchSystem/pkg/identity"
	"github.com/xianyu11-x/matchSystem/pkg/matchsystem"
)

func TestRoutedTicketsAreMatchedAndConsumed(t *testing.T) {
	ctx := context.Background()
	consumer, err := newConsumer(ctx)
	if err != nil {
		t.Fatalf("创建消费者: %v", err)
	}
	wantOwner := identity.OwnerRef{LogicalNode: consumer.key, PhysicalNodeID: consumer.physical.ID()}
	for _, id := range []uint64{1001, 1002} {
		owner, err := consumer.submit(ctx, &common.Ticket{
			TicketID: id, CreatedAt: int64(id), StringLists: map[string][]string{"partition": {"blue"}},
		})
		if err != nil {
			t.Fatalf("投递 Ticket %d: %v", id, err)
		}
		if owner.String() != wantOwner.String() {
			t.Fatalf("路由结果: 得到 %v，期望 %v", owner, wantOwner)
		}
		if _, found, err := consumer.physical.Get(ctx, owner, id); err != nil || !found {
			t.Fatalf("匹配前查询 Ticket %d: found=%v, error=%v", id, found, err)
		}
	}
	if err := consumer.physical.BeginMatchRound(ctx, 2000); err != nil {
		t.Fatalf("开始匹配轮次: %v", err)
	}
	result, err := consumer.physical.ProduceMatch(ctx)
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
	for _, id := range ids {
		if _, found, err := consumer.physical.Get(ctx, wantOwner, id); err != nil || found {
			t.Fatalf("匹配后查询 Ticket %d: found=%v, error=%v", id, found, err)
		}
	}
	if duplicate, err := consumer.physical.ProduceMatch(ctx); !errors.Is(err, matchsystem.ErrNoLogicalNodeAvailable) || duplicate.Match != nil {
		t.Fatalf("本轮再次匹配: Match=%v, error=%v", duplicate.Match, err)
	}
	if err := consumer.physical.BeginMatchRound(ctx, 2001); err != nil {
		t.Fatalf("开始下一匹配轮次: %v", err)
	}
	if duplicate, err := consumer.physical.ProduceMatch(ctx); !errors.Is(err, matchsystem.ErrNoLogicalNodeAvailable) || duplicate.Match != nil {
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
	result, err := consumer.physical.ProduceMatch(ctx)
	if err != nil || result.Match != nil {
		t.Fatalf("不同分区不应匹配: Match=%v, error=%v", result.Match, err)
	}
	for _, id := range []uint64{1, 2} {
		if _, found, err := consumer.physical.Get(ctx, owner, id); err != nil || !found {
			t.Fatalf("未匹配的 Ticket %d 应继续等待: found=%v, error=%v", id, found, err)
		}
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
