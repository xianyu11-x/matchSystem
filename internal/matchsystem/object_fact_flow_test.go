package matchsystem

import (
	"context"
	"testing"

	"matchSystem/internal/identity"
)

func TestObjectFactMetricsReportRefreshCacheAndWriterErrors(t *testing.T) {
	key := identity.LogicalNodeKey{
		Rule:        identity.RuleKey{Namespace: "test-object-metrics", RuleID: 1},
		PlacementID: "default",
	}
	node, err := NewLogicalNode(LogicalNodeSpec{
		Key: key,
		RuleJSON: testRuleJSON(t, key.Rule, `{
			"schemaVersion":"logical-node-contract/v3",
			"attributes":[{"name":"partition","type":"strings","maxValues":1}],
			"facts":[{"name":"object-label","type":"strings","scope":"object","maxValues":1}],
			"indexes":[{"type":"multi_value","name":"partition","keyType":"string","maxDocumentValues":1,"maxQueryValues":1}]
		}`, `{
			"schemaVersion":"prefilter/v3",
			"bitmap":{"resultType":"bitmap","expr":{"op":"lookup_string","index":"partition","values":{"schemaVersion":"expression-scalar/v3","resultType":"strings","expr":{"op":"strings_literal","values":["blue"]}}}}
		}`, `{
			"schemaVersion":"evaluation/v3",
			"canJoin":{"schemaVersion":"expression-scalar/v3","resultType":"bool","expr":{"op":"bool_literal","value":true}},
			"canComplete":{"schemaVersion":"expression-scalar/v3","resultType":"bool","expr":{"op":"bool_literal","value":false}}
		}`, logicalNodeConfig{
			CandidateLimitPerSeed: 2,
			SeedScheduler: seedSchedulerConfig{
				AttemptLimitPerProduceMatch: 1,
				AttemptLimitPerMatchRound:   1,
			},
		}),
		ObjectFactProviderDescriptor: &ProviderDescriptor{
			ID: "test.object-writer", Version: "v1",
			Facts: []FactSpec{{Name: "object-label", Type: FactTypeStrings, Scope: FactScopeObject, MaxValues: 1}},
		},
		ObjectFactProvider: func(ticket *Ticket, _ int64, _ Facts, out ObjectFactWriter) error {
			return out.SetStrings("object-label", []string{"ticket"})
		},
	})
	if err != nil {
		t.Fatalf("create Object Fact node: %v", err)
	}
	for id := TicketID(1); id <= 2; id++ {
		if err := node.Add(&Ticket{TicketID: id, StringLists: map[string][]string{"partition": {"blue"}}}); err != nil {
			t.Fatalf("add Ticket %d: %v", id, err)
		}
	}
	if err := node.BeginMatchRound(1); err != nil {
		t.Fatalf("begin round: %v", err)
	}
	match, metrics, err := node.ProduceMatchWithMetrics(context.Background())
	if err != nil || match != nil {
		t.Fatalf("ProduceMatch: match=%#v err=%v", match, err)
	}
	if metrics.ObjectFactProviderCalls != 2 || metrics.ObjectFactRefreshes != 2 || metrics.ObjectFactCacheHits != 1 {
		t.Fatalf("unexpected Object Fact lifecycle metrics: %+v", metrics)
	}
	if metrics.ObjectFactProvider < 0 || metrics.ObjectFactRefresh < 0 {
		t.Fatalf("Object Fact durations were not recorded: %+v", metrics)
	}

	badKey := identity.LogicalNodeKey{Rule: identity.RuleKey{Namespace: "test-object-metrics", RuleID: 2}, PlacementID: "bad"}
	badNode, err := NewLogicalNode(LogicalNodeSpec{
		Key: badKey,
		RuleJSON: testRuleJSON(t, badKey.Rule, `{
			"schemaVersion":"logical-node-contract/v3","attributes":[],
			"facts":[{"name":"object-label","type":"strings","scope":"object","maxValues":1}],"indexes":[]
		}`, `{"schemaVersion":"prefilter/v3","bitmap":{"resultType":"bitmap","expr":{"op":"none"}}}`, `{
			"schemaVersion":"evaluation/v3",
			"canJoin":{"schemaVersion":"expression-scalar/v3","resultType":"bool","expr":{"op":"bool_literal","value":true}},
			"canComplete":{"schemaVersion":"expression-scalar/v3","resultType":"bool","expr":{"op":"bool_literal","value":true}}
		}`, logicalNodeConfig{}),
		ObjectFactProviderDescriptor: &ProviderDescriptor{ID: "test.object-bad", Version: "v1", Facts: []FactSpec{{Name: "object-label", Type: FactTypeStrings, Scope: FactScopeObject, MaxValues: 1}}},
		ObjectFactProvider: func(_ *Ticket, _ int64, _ Facts, out ObjectFactWriter) error {
			return out.SetStrings("object-label", []string{"too-many", "values"})
		},
	})
	if err != nil {
		t.Fatalf("create bad Object Fact node: %v", err)
	}
	if err := badNode.Add(&Ticket{TicketID: 1}); err != nil {
		t.Fatalf("add bad Object Fact Ticket: %v", err)
	}
	if err := badNode.BeginMatchRound(1); err != nil {
		t.Fatalf("begin bad Object Fact round: %v", err)
	}
	_, badMetrics, err := badNode.ProduceMatchWithMetrics(context.Background())
	if err == nil || badMetrics.ObjectFactErrors != 1 || badMetrics.ObjectFactProviderCalls != 1 {
		t.Fatalf("writer error metrics: err=%v metrics=%+v", err, badMetrics)
	}
}
