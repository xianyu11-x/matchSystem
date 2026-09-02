package matchsystem

import (
	"context"
	"testing"

	"matchSystem/internal/identity"
)

func TestLogicalNodeFactProviderReceivesOwnedNodeSnapshot(t *testing.T) {
	key := identity.LogicalNodeKey{
		Rule:        identity.RuleKey{Namespace: "test", RuleID: 2},
		PlacementID: "snapshot",
	}
	var input TickFactInput
	node, err := NewLogicalNode(LogicalNodeSpec{
		Key: key,
		RuleJSON: testRuleJSON(t, key.Rule, `{
			"schemaVersion":"logical-node-contract/v3",
			"attributes":[],"facts":[{"name":"waiting-count","type":"int64","scope":"tick"}],"indexes":[]
		}`, `{
			"schemaVersion":"prefilter/v3",
			"bitmap":{"resultType":"bitmap","expr":{"op":"none"}}
		}`, `{
			"schemaVersion":"evaluation/v3",
			"canJoin":{"schemaVersion":"expression-scalar/v3","resultType":"bool","expr":{"op":"bool_literal","value":true}},
			"canComplete":{"schemaVersion":"expression-scalar/v3","resultType":"bool","expr":{"op":"int64_gte","left":{"op":"int64_ref","source":"tick_facts","name":"waiting-count"},"right":{"op":"int64_literal","value":2}}}
		}`, logicalNodeConfig{}),
		FactProviderDescriptor: &ProviderDescriptor{
			ID:      "test.tick.snapshot",
			Version: "v1",
			Facts:   []FactSpec{{Name: "waiting-count", Type: FactTypeInt64, Scope: FactScopeTick}},
		},
		FactProvider: func(_ context.Context, got TickFactInput) (Facts, error) {
			input = got
			return Facts{Int64Values: map[string]int64{"waiting-count": int64(got.Node.WaitingCount)}}, nil
		},
	})
	if err != nil {
		t.Fatalf("create LogicalNode: %v", err)
	}
	if err := node.Add(&Ticket{TicketID: 1}); err != nil {
		t.Fatalf("add first ticket: %v", err)
	}
	if err := node.Add(&Ticket{TicketID: 2}); err != nil {
		t.Fatalf("add second ticket: %v", err)
	}
	if err := node.BeginMatchRound(100); err != nil {
		t.Fatalf("begin match round: %v", err)
	}
	match, err := node.ProduceMatch(context.Background())
	if err != nil {
		t.Fatalf("produce match: %v", err)
	}
	if match == nil || len(match.Tickets) != 1 || match.Tickets[0].TicketID != 1 {
		t.Fatalf("FactProvider output was not used by evaluation: %#v", match)
	}
	if input.Now != 100 {
		t.Fatalf("FactProvider got Now=%d, want 100", input.Now)
	}
	if input.Node.Key != key {
		t.Fatalf("FactProvider got node key=%v, want %v", input.Node.Key, key)
	}
	if input.Node.State != LogicalNodeReady {
		t.Fatalf("FactProvider got node state=%q, want %q", input.Node.State, LogicalNodeReady)
	}
	if input.Node.WaitingCount != 2 {
		t.Fatalf("FactProvider got waiting count=%d, want 2", input.Node.WaitingCount)
	}
}

func TestLogicalNodeProduceMatchCommitsEvaluatorResult(t *testing.T) {
	key := identity.LogicalNodeKey{
		Rule:        identity.RuleKey{Namespace: "test", RuleID: 1},
		PlacementID: "default",
	}
	node, err := NewLogicalNode(LogicalNodeSpec{
		Key: key,
		RuleJSON: testRuleJSON(t, key.Rule, `{
			"schemaVersion":"logical-node-contract/v3",
			"attributes":[],"facts":[],"indexes":[]
		}`, `{
			"schemaVersion":"prefilter/v3",
			"bitmap":{"resultType":"bitmap","expr":{"op":"none"}}
		}`, `{
			"schemaVersion":"evaluation/v3",
			"canJoin":{"schemaVersion":"expression-scalar/v3","resultType":"bool","expr":{"op":"bool_literal","value":true}},
			"canComplete":{"schemaVersion":"expression-scalar/v3","resultType":"bool","expr":{"op":"bool_literal","value":true}}
		}`, logicalNodeConfig{}),
	})
	if err != nil {
		t.Fatalf("create LogicalNode: %v", err)
	}
	if err := node.Add(&Ticket{TicketID: 42}); err != nil {
		t.Fatalf("add seed: %v", err)
	}
	if got := node.store.prefilterStore.Len(); got != 1 {
		t.Fatalf("prefilter did not index seed: got Len=%d", got)
	}
	if err := node.BeginMatchRound(100); err != nil {
		t.Fatalf("begin match round: %v", err)
	}

	match, err := node.ProduceMatch(context.Background())
	if err != nil {
		t.Fatalf("produce match: %v", err)
	}
	if match == nil || len(match.Tickets) != 1 || match.Tickets[0].TicketID != 42 {
		t.Fatalf("unexpected match: %#v", match)
	}
	if got := node.Len(); got != 0 {
		t.Fatalf("evaluator result was not committed to ticket store: Len=%d", got)
	}
	if got := node.store.prefilterStore.Len(); got != 0 {
		t.Fatalf("committed seed remains in prefilter store: Len=%d", got)
	}
	if _, ok := node.Get(42); ok {
		t.Fatal("committed seed remains accessible from LogicalNode")
	}
	if order, err := node.seedOrderRuntime.BuildRound(1); err != nil {
		t.Fatalf("build seed runtime after commit: %v", err)
	} else if len(order) != 0 {
		t.Fatalf("committed seed remains in seed runtime: %v", order)
	}
	if err := node.Add(&Ticket{TicketID: 43}); err != nil {
		t.Fatalf("add ticket after commit: %v", err)
	}
	if order, err := node.seedOrderRuntime.BuildRound(1); err != nil {
		t.Fatalf("build seed runtime after post-commit Add: %v", err)
	} else if len(order) != 1 || order[0] != 43 {
		t.Fatalf("seed runtime did not remove committed ID before post-commit Add: %v", order)
	}
}

func TestLogicalNodeProduceMatchMetricsAggregateStages(t *testing.T) {
	t.Run("complete-seed", func(t *testing.T) {
		key := identity.LogicalNodeKey{
			Rule:        identity.RuleKey{Namespace: "test-metrics", RuleID: 1},
			PlacementID: "complete",
		}
		node, err := NewLogicalNode(LogicalNodeSpec{
			Key: key,
			RuleJSON: testRuleJSON(t, key.Rule, `{
				"schemaVersion":"logical-node-contract/v3",
				"attributes":[],"facts":[],"indexes":[]
			}`, `{
				"schemaVersion":"prefilter/v3",
				"bitmap":{"resultType":"bitmap","expr":{"op":"none"}}
			}`, `{
				"schemaVersion":"evaluation/v3",
				"canJoin":{"schemaVersion":"expression-scalar/v3","resultType":"bool","expr":{"op":"bool_literal","value":true}},
				"canComplete":{"schemaVersion":"expression-scalar/v3","resultType":"bool","expr":{"op":"bool_literal","value":true}}
			}`, logicalNodeConfig{}),
		})
		if err != nil {
			t.Fatalf("create LogicalNode: %v", err)
		}
		if err := node.Add(&Ticket{TicketID: 1}); err != nil {
			t.Fatalf("add seed: %v", err)
		}
		if err := node.BeginMatchRound(100); err != nil {
			t.Fatalf("begin match round: %v", err)
		}

		match, metrics, err := node.ProduceMatchWithMetrics(context.Background())
		if err != nil {
			t.Fatalf("produce match: %v", err)
		}
		if match == nil || len(match.Tickets) != 1 {
			t.Fatalf("unexpected match: %#v", match)
		}
		if metrics.SeedAttempts != 1 || metrics.CanCompleteCalls != 1 || metrics.CommitCalls != 1 {
			t.Fatalf("unexpected metrics counters: %+v", metrics)
		}
		if metrics.PrefilterCalls != 0 || metrics.CandidateVisited != 0 || metrics.CanJoinCalls != 0 {
			t.Fatalf("complete seed should not run candidate stages: %+v", metrics)
		}
		if metrics.MatchSize != 1 || metrics.Duration < 0 || metrics.MatchBuild < 0 || metrics.Commit < 0 {
			t.Fatalf("unexpected metrics durations/result: %+v", metrics)
		}
	})

	t.Run("candidate-pipeline", func(t *testing.T) {
		key := identity.LogicalNodeKey{
			Rule:        identity.RuleKey{Namespace: "test-metrics", RuleID: 2},
			PlacementID: "candidate",
		}
		node, err := NewLogicalNode(LogicalNodeSpec{
			Key: key,
			RuleJSON: testRuleJSON(t, key.Rule, `{
				"schemaVersion":"logical-node-contract/v3",
				"attributes":[{"name":"partition","type":"strings","maxValues":1}],
				"facts":[],"indexes":[{"type":"multi_value","name":"partition","keyType":"string","maxDocumentValues":1,"maxQueryValues":1}]
			}`, `{
				"schemaVersion":"prefilter/v3",
				"bitmap":{"resultType":"bitmap","expr":{"op":"lookup_string","index":"partition","values":{"schemaVersion":"expression-scalar/v3","resultType":"strings","expr":{"op":"strings_literal","values":["blue"]}}}}
			}`, `{
				"schemaVersion":"evaluation/v3",
				"canJoin":{"schemaVersion":"expression-scalar/v3","resultType":"bool","expr":{"op":"bool_literal","value":true}},
				"canComplete":{"schemaVersion":"expression-scalar/v3","resultType":"bool","expr":{"op":"bool_literal","value":false}}
			}`, logicalNodeConfig{
				CandidateLimitPerSeed: 4,
				SeedScheduler: seedSchedulerConfig{
					AttemptLimitPerProduceMatch: 1,
					AttemptLimitPerMatchRound:   1,
				},
			}),
		})
		if err != nil {
			t.Fatalf("create LogicalNode: %v", err)
		}
		for id := TicketID(1); id <= 2; id++ {
			if err := node.Add(&Ticket{TicketID: id, StringLists: map[string][]string{"partition": {"blue"}}}); err != nil {
				t.Fatalf("add ticket %d: %v", id, err)
			}
		}
		if err := node.BeginMatchRound(100); err != nil {
			t.Fatalf("begin match round: %v", err)
		}

		match, metrics, err := node.ProduceMatchWithMetrics(context.Background())
		if err != nil {
			t.Fatalf("produce match: %v", err)
		}
		if match != nil {
			t.Fatalf("canComplete=false produced a match: %#v", match)
		}
		if metrics.SeedAttempts != 1 || metrics.PrefilterCalls != 1 || metrics.PrefilterCandidates != 1 {
			t.Fatalf("unexpected prefilter metrics: %+v", metrics)
		}
		if metrics.CandidateVisited != 1 || metrics.CandidateScoringCalls != 1 || metrics.RankedCandidates != 1 {
			t.Fatalf("unexpected ranking metrics: %+v", metrics)
		}
		if metrics.CanJoinCalls != 1 || metrics.JoinedCandidates != 1 || metrics.CanCompleteCalls != 2 {
			t.Fatalf("unexpected evaluation metrics: %+v", metrics)
		}
		if metrics.CommitCalls != 0 || metrics.MatchSize != 0 {
			t.Fatalf("unsuccessful ProduceMatch should not commit/build: %+v", metrics)
		}
	})
}
