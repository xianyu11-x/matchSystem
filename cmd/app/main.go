package main

import (
	"context"
	"errors"
	"fmt"
	"log"

	"matchSystem/internal/common"
	"matchSystem/internal/identity"
	"matchSystem/internal/matchsystem"
)

func main() {
	if err := runOneMatchRound(context.Background()); err != nil {
		log.Fatal(err)
	}
}

func runOneMatchRound(ctx context.Context) error {
	// LargestQueue 会优先把一次 ProduceMatch 机会分配给积压更多的
	// LogicalNode；替换这里的 selector 即可切换负载均衡策略。
	physical, err := matchsystem.NewPhysicalNode(
		"physical-demo",
		matchsystem.WithLogicalNodeSelector(matchsystem.NewLargestQueueLogicalNodeSelector()),
	)
	if err != nil {
		return err
	}

	ranked := identity.LogicalNodeKey{
		Rule:        identity.RuleKey{Namespace: "demo", RuleID: 1},
		PlacementID: "placement-a",
	}
	casual := identity.LogicalNodeKey{
		Rule:        identity.RuleKey{Namespace: "demo", RuleID: 2},
		PlacementID: "placement-a",
	}

	for _, key := range []identity.LogicalNodeKey{ranked, casual} {
		if err := physical.Load(ctx, logicalNodeSpec(key)); err != nil {
			return fmt.Errorf("load LogicalNode %s: %w", key, err)
		}
	}

	// ranked 有 3 个 Ticket、casual 有 2 个。每个合法组需要 2 人，
	// 因而本轮会产出两个组，并在 ranked 留下一个等待中的 Ticket。
	tickets := []struct {
		key       identity.LogicalNodeKey
		ticketID  uint64
		partition string
		createdAt int64
	}{
		{ranked, 1001, "blue", 1},
		{ranked, 1002, "blue", 2},
		{ranked, 1003, "green", 3},
		{casual, 2001, "open", 4},
		{casual, 2002, "open", 5},
	}
	for _, item := range tickets {
		owner := identity.OwnerRef{LogicalNode: item.key, PhysicalNodeID: physical.ID()}
		_, err := physical.Add(ctx, owner, &common.Ticket{
			TicketID:    item.ticketID,
			CreatedAt:   item.createdAt,
			StringLists: map[string][]string{"partition": {item.partition}},
		})
		if err != nil {
			return fmt.Errorf("add Ticket %d: %w", item.ticketID, err)
		}
	}

	const (
		roundNow   = int64(100)
		matchLimit = 10
	)
	if err := physical.BeginMatchRound(ctx, roundNow); err != nil {
		return fmt.Errorf("begin match round: %w", err)
	}

	produced := 0
	for produced < matchLimit {
		result, err := physical.ProduceMatch(ctx)
		if errors.Is(err, matchsystem.ErrNoLogicalNodeAvailable) {
			break
		}
		if err != nil {
			return fmt.Errorf("produce match: %w", err)
		}
		if result.Match == nil {
			continue
		}

		produced++
		fmt.Printf("match %d from %s:", produced, result.LogicalNode)
		for _, ticket := range result.Match.Tickets {
			fmt.Printf(" %d", ticket.TicketID)
		}
		fmt.Println()
	}

	fmt.Printf("round finished: produced=%d, remaining=%v\n", produced, physical.Describe())
	return nil
}

func logicalNodeSpec(key identity.LogicalNodeKey) matchsystem.LogicalNodeSpec {
	return matchsystem.LogicalNodeSpec{
		Key: key,
		ContractJSON: []byte(`{
			"schemaVersion":"logical-node-contract/v3",
			"attributes":[{"name":"partition","type":"strings","maxValues":1}],
			"facts":[{"name":"count","type":"int64","scope":"match"}],
			"indexes":[{"type":"multi_value","name":"partition","keyType":"string","maxDocumentValues":1,"maxQueryValues":1}]
		}`),
		CandidateScorer: func(ctx matchsystem.CandidateScoreContext) (float64, error) {
			return float64(ctx.Candidate.CreatedAt), nil
		},
		MatchFactProvider: demoMatchFactProvider{},
		EvaluationJSON: []byte(`{
			"schemaVersion":"evaluation/v3",
			"canJoin":{
				"schemaVersion":"expression-scalar/v3",
				"resultType":"bool",
				"expr":{"op":"bool_literal","value":true}
			},
			"canComplete":{
				"schemaVersion":"expression-scalar/v3",
				"resultType":"bool",
				"expr":{"op":"int64_gte","left":{"op":"int64_ref","source":"match_facts","name":"count"},"right":{"op":"int64_literal","value":2}}
			}
		}`),
		Config: matchsystem.LogicalNodeConfig{
			MaxPlayers: 2,
			SeedScheduler: matchsystem.SeedSchedulerConfig{
				AttemptLimitPerProduceMatch: 2,
				AttemptLimitPerMatchRound:   500,
				Order: matchsystem.SeedOrderPolicyConfig{
					Kind: matchsystem.SeedOrderOldest,
				},
			},
		},
		PrefilterJSON: []byte(`{
			"schemaVersion":"prefilter/v3",
			"bitmap":{
				"resultType":"bitmap",
				"expr":{
					"op":"lookup_string",
					"index":"partition",
					"values":{
						"schemaVersion":"expression-scalar/v3",
						"resultType":"strings",
						"expr":{"op":"strings_ref","source":"seed_attributes","name":"partition"}
					}
				}
			}
		}`),
	}
}

// demoMatchFactProvider owns the complete aggregate state for the sample
// Match. LogicalNode validates and atomically commits these snapshots.
type demoMatchFactProvider struct{}

func (demoMatchFactProvider) Initialize(context.Context, matchsystem.InitializeInput) (matchsystem.Facts, error) {
	return matchsystem.Facts{Int64Values: map[string]int64{"count": 1}}, nil
}

func (demoMatchFactProvider) OnJoin(_ context.Context, input matchsystem.JoinInput) (matchsystem.Facts, error) {
	return matchsystem.Facts{Int64Values: map[string]int64{"count": input.MatchFactsBefore.Int64Values["count"] + 1}}, nil
}
