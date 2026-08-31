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
		FactProvider: func(_ context.Context, got TickFactInput) (Facts, error) {
			input = got
			return Facts{Int64Values: map[string]int64{"waiting-count": int64(got.Node.WaitingCount)}}, nil
		},
	})
	if err != nil {
		t.Fatalf("create LogicalNode: %v", err)
	}
	if _, err := node.Add(&Ticket{TicketID: 1}); err != nil {
		t.Fatalf("add first ticket: %v", err)
	}
	if _, err := node.Add(&Ticket{TicketID: 2}); err != nil {
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
	if _, err := node.Add(&Ticket{TicketID: 42}); err != nil {
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
}
