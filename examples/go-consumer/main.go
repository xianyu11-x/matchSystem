package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/xianyu11-x/matchSystem/pkg/client"
	"github.com/xianyu11-x/matchSystem/pkg/common"
	"github.com/xianyu11-x/matchSystem/pkg/identity"
	"github.com/xianyu11-x/matchSystem/pkg/matchsystem"
)

const ruleJSON = `{
  "schemaVersion": "match-rule/v1",
  "ruleKey": {"namespace": "1", "ruleId": "1"},
  "contract": {
    "schemaVersion": "logical-node-contract/v3",
    "attributes": [{"name": "partition", "type": "strings", "maxValues": 1}],
    "facts": [{"name": "count", "type": "int64", "scope": "match"}],
    "indexes": [{"type": "multi_value", "name": "partition", "keyType": "string", "maxDocumentValues": 1, "maxQueryValues": 1}]
  },
  "prefilter": {
    "schemaVersion": "prefilter/v3",
    "bitmap": {"resultType": "bitmap", "expr": {
      "op": "lookup_string", "index": "partition",
      "values": {"schemaVersion": "expression-scalar/v3", "resultType": "strings", "expr": {
        "op": "strings_ref", "source": "seed_attributes", "name": "partition"
      }}
    }}
  },
  "evaluation": {
    "schemaVersion": "evaluation/v3",
    "canJoin": {"schemaVersion": "expression-scalar/v3", "resultType": "bool", "expr": {"op": "bool_literal", "value": true}},
    "canComplete": {"schemaVersion": "expression-scalar/v3", "resultType": "bool", "expr": {
      "op": "int64_gte", "left": {"op": "int64_ref", "source": "match_facts", "name": "count"},
      "right": {"op": "int64_literal", "value": 2}
    }}
  },
  "scoring": {"type": "constant", "params": {"value": 0}},
  "seedSelection": {"type": "arrival", "params": {}},
  "runtime": {"candidateScoringLimitPerSeed": 2, "candidateLimitPerSeed": 2, "maxPlayers": 2, "attemptLimitPerProduceMatch": 2, "attemptLimitPerMatchRound": 2}
}`

// countProvider 描述并维护整组匹配的完整人数快照。
type countProvider struct{}

var _ matchsystem.MatchProvider = countProvider{}

func (countProvider) Describe() (matchsystem.ProviderDescriptor, error) {
	return matchsystem.ProviderDescriptor{
		ID:      "consumer.match-count",
		Version: "v1",
		Facts: []matchsystem.FactSpec{{
			Name: "count", Type: matchsystem.FactTypeInt64, Scope: matchsystem.FactScopeMatch,
		}},
	}, nil
}

func (countProvider) Initialize(context.Context, matchsystem.InitializeInput) (matchsystem.Facts, error) {
	return matchsystem.Facts{Int64Values: map[string]int64{"count": 1}}, nil
}

func (countProvider) OnJoin(_ context.Context, input matchsystem.JoinInput) (matchsystem.Facts, error) {
	return matchsystem.Facts{Int64Values: map[string]int64{
		"count": input.MatchFactsBefore.Int64Values["count"] + 1,
	}}, nil
}

type consumer struct {
	key        identity.LogicalNodeKey
	routeOwner client.OwnerRef
	physical   *matchsystem.PhysicalNode
	router     *client.Router
}

func newConsumer(ctx context.Context) (*consumer, error) {
	key := identity.LogicalNodeKey{
		Rule:        identity.RuleKey{Namespace: "1", RuleID: 1},
		PlacementID: 1,
	}
	physical, err := matchsystem.NewPhysicalNode(1)
	if err != nil {
		return nil, fmt.Errorf("创建 PhysicalNode: %w", err)
	}
	if err := physical.Load(ctx, matchsystem.LogicalNodeSpec{
		Key:                   key,
		RuleJSON:              []byte(ruleJSON),
		MatchFactProvider:     countProvider{},
		MatchFactSnapshotMode: matchsystem.MatchFactSnapshotModeDeepCopy,
	}); err != nil {
		return nil, fmt.Errorf("加载 LogicalNode: %w", err)
	}

	// 路由器和匹配核心共用同一个完整的数值身份。
	routeOwner := client.OwnerRef{
		LogicalNode:    key,
		PhysicalNodeID: physical.ID(),
	}
	table, err := client.NewRouteTable(client.RouteTableConfig{
		PhysicalNodes: []client.PhysicalRoute{{
			PhysicalNodeID: routeOwner.PhysicalNodeID, Endpoint: common.Endpoint("inproc://consumer-node"), Enabled: true,
		}},
		Rules: []client.RuleRoute{{
			LogicalNode: routeOwner.LogicalNode, PhysicalNodeID: routeOwner.PhysicalNodeID, Weight: 1, Enabled: true,
		}},
	})
	if err != nil {
		return nil, fmt.Errorf("创建路由表: %w", err)
	}
	router, err := client.NewRouter(table)
	if err != nil {
		return nil, fmt.Errorf("创建路由器: %w", err)
	}
	return &consumer{key: key, routeOwner: routeOwner, physical: physical, router: router}, nil
}

// submit 通过路由器选择 OwnerRef，再向对应 PhysicalNode 投递 Ticket。
func (c *consumer) submit(ctx context.Context, ticket *common.Ticket) (identity.OwnerRef, error) {
	if ticket == nil {
		return identity.OwnerRef{}, errors.New("Ticket 不能为空")
	}
	decision, err := c.router.RouteNew(ctx, client.RouteRequest{Rule: c.routeOwner.LogicalNode.Rule, TicketID: ticket.TicketID})
	if err != nil {
		return identity.OwnerRef{}, fmt.Errorf("路由 Ticket %d: %w", ticket.TicketID, err)
	}
	if decision.Owner.String() != c.routeOwner.String() {
		return identity.OwnerRef{}, fmt.Errorf("路由目标没有对应的核心节点: %+v", decision.Owner)
	}
	owner := decision.Owner
	if err := c.physical.Add(ctx, owner, ticket); err != nil {
		return identity.OwnerRef{}, fmt.Errorf("投递 Ticket %d: %w", ticket.TicketID, err)
	}
	return owner, nil
}

func run(ctx context.Context, output io.Writer) error {
	consumer, err := newConsumer(ctx)
	if err != nil {
		return err
	}
	tickets := []*common.Ticket{
		{TicketID: 1001, CreatedAt: 1, StringLists: map[string][]string{"partition": {"blue"}}},
		{TicketID: 1002, CreatedAt: 2, StringLists: map[string][]string{"partition": {"blue"}}},
	}
	var owner identity.OwnerRef
	for i, ticket := range tickets {
		routed, err := consumer.submit(ctx, ticket)
		if err != nil {
			return err
		}
		if i > 0 && routed.String() != owner.String() {
			return errors.New("示例 Ticket 被路由到不同节点")
		}
		owner = routed
	}

	if err := consumer.physical.BeginMatchRound(ctx, 100); err != nil {
		return fmt.Errorf("开始匹配轮次: %w", err)
	}
	result, err := consumer.physical.ProduceMatch(ctx)
	if err != nil {
		return fmt.Errorf("生成 Match: %w", err)
	}
	if result.LogicalNode.String() != consumer.key.String() || result.Match == nil || len(result.Match.Tickets) != 2 {
		return errors.New("未生成包含两张 Ticket 的完整 Match")
	}
	ids := make([]uint64, 0, len(result.Match.Tickets))
	for _, ticket := range result.Match.Tickets {
		if ticket == nil {
			return errors.New("Match 包含空 Ticket")
		}
		ids = append(ids, ticket.TicketID)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	if ids[0] != 1001 || ids[1] != 1002 || result.Match.Facts.Int64Values["count"] != 2 {
		return fmt.Errorf("Match 成员或人数不正确: %v", ids)
	}
	for _, ticket := range tickets {
		_, found, err := consumer.physical.Get(ctx, owner, ticket.TicketID)
		if err != nil {
			return fmt.Errorf("查询 Ticket %d: %w", ticket.TicketID, err)
		}
		if found {
			return fmt.Errorf("匹配后的 Ticket %d 仍在节点中", ticket.TicketID)
		}
	}
	if duplicate, err := consumer.physical.ProduceMatch(ctx); !errors.Is(err, matchsystem.ErrNoLogicalNodeAvailable) || duplicate.Match != nil {
		return fmt.Errorf("本轮重复匹配检查失败: Match=%v, error=%v", duplicate.Match, err)
	}
	if err := consumer.physical.BeginMatchRound(ctx, 101); err != nil {
		return fmt.Errorf("开始下一匹配轮次: %w", err)
	}
	if duplicate, err := consumer.physical.ProduceMatch(ctx); !errors.Is(err, matchsystem.ErrNoLogicalNodeAvailable) || duplicate.Match != nil {
		return fmt.Errorf("下一轮重复匹配检查失败: Match=%v, error=%v", duplicate.Match, err)
	}
	for _, ticket := range tickets {
		_, found, err := consumer.physical.Get(ctx, owner, ticket.TicketID)
		if err != nil {
			return fmt.Errorf("下一轮查询 Ticket %d: %w", ticket.TicketID, err)
		}
		if found {
			return fmt.Errorf("已消费的 Ticket %d 在下一轮恢复", ticket.TicketID)
		}
	}
	_, err = fmt.Fprintf(output, "matched: %d,%d\nremaining: 0\nduplicate: false\n", ids[0], ids[1])
	return err
}

func main() {
	if err := run(context.Background(), os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
