package matchsystem

import (
	"context"
	"errors"
	"testing"

	"matchSystem/internal/identity"
	"matchSystem/internal/matchsystem/evaluation"
)

type e2eTrustedFactsProvider struct {
	omitCount bool
}

func (p e2eTrustedFactsProvider) Initialize(context.Context, InitializeInput) (Facts, error) {
	values := Facts{
		StringLists: map[string][]string{
			// These are deliberately the wrong type for the declared int64 Fact
			// and include an undeclared field. Provider contract tests catch such
			// mistakes; production evaluation must not revalidate them.
			"match-extra":      {"wrong-type"},
			"undeclared-match": {"not-in-contract"},
		},
		Int64Values: map[string]int64{},
	}
	if !p.omitCount {
		values.Int64Values["match-count"] = 1
	}
	return values, nil
}

func (p e2eTrustedFactsProvider) OnJoin(_ context.Context, input JoinInput) (Facts, error) {
	values := Facts{
		StringLists: map[string][]string{
			"match-extra":      {"wrong-type"},
			"undeclared-match": {"not-in-contract"},
		},
		Int64Values: map[string]int64{},
	}
	if !p.omitCount {
		values.Int64Values["match-count"] = input.MatchFactsBefore.Int64Values["match-count"] + 1
	}
	return values, nil
}

func trustedFactFlowSpec(t *testing.T, provider MatchFactProvider) LogicalNodeSpec {
	t.Helper()
	key := identityKeyForTrustedFactFlow()
	return LogicalNodeSpec{
		Key: key,
		RuleJSON: testRuleJSON(t, key.Rule, `{
			"schemaVersion":"logical-node-contract/v3",
			"attributes":[{"name":"partition","type":"strings","maxValues":1}],
			"facts":[
				{"name":"tick-extra","type":"int64","scope":"tick"},
				{"name":"object-extra","type":"int64","scope":"object"},
				{"name":"match-count","type":"int64","scope":"match"},
				{"name":"match-extra","type":"int64","scope":"match"}
			],
			"indexes":[{"type":"multi_value","name":"partition","keyType":"string","maxDocumentValues":1,"maxQueryValues":1}]
		}`, `{
			"schemaVersion":"prefilter/v3",
			"bitmap":{"resultType":"bitmap","expr":{
				"op":"lookup_string","index":"partition","values":{
					"schemaVersion":"expression-scalar/v3","resultType":"strings",
					"expr":{"op":"strings_literal","values":["blue"]}
				}
			}}
		}`, `{
			"schemaVersion":"evaluation/v3",
			"canJoin":{"schemaVersion":"expression-scalar/v3","resultType":"bool","expr":{"op":"bool_literal","value":true}},
			"canComplete":{"schemaVersion":"expression-scalar/v3","resultType":"bool","expr":{
				"op":"int64_gte","left":{"op":"int64_ref","source":"match_facts","name":"match-count"},"right":{"op":"int64_literal","value":2}
			}}
		}`, logicalNodeConfig{
			MaxPlayers: 2,
			SeedScheduler: seedSchedulerConfig{
				AttemptLimitPerProduceMatch: 2,
				AttemptLimitPerMatchRound:   4,
			},
		}),
		FactProvider: func(context.Context, int64) (Facts, error) {
			return Facts{
				StringLists: map[string][]string{
					"tick-extra":      {"wrong-type"},
					"undeclared-tick": {"not-in-contract"},
				},
				Int64Values: map[string]int64{},
			}, nil
		},
		ObjectFactProvider: func(*Ticket, int64, Facts) (Facts, error) {
			return Facts{
				StringLists: map[string][]string{
					"object-extra":      {"wrong-type"},
					"undeclared-object": {"not-in-contract"},
				},
				Int64Values: map[string]int64{},
			}, nil
		},
		MatchFactProvider: provider,
	}
}

func identityKeyForTrustedFactFlow() (key identity.LogicalNodeKey) {
	return identity.LogicalNodeKey{
		Rule:        identity.RuleKey{Namespace: "test-trusted-facts", RuleID: 1},
		PlacementID: "default",
	}
}

func TestLogicalNodeTrustedFactProvidersFlowWithoutRuntimeValidator(t *testing.T) {
	node, err := NewLogicalNode(trustedFactFlowSpec(t, e2eTrustedFactsProvider{}))
	if err != nil {
		t.Fatalf("create trusted Fact flow node: %v", err)
	}
	for _, ticket := range []*Ticket{
		{TicketID: 1, CreatedAt: 1, StringLists: map[string][]string{"partition": {"blue"}}},
		{TicketID: 2, CreatedAt: 2, StringLists: map[string][]string{"partition": {"blue"}}},
	} {
		if _, err := node.Add(ticket); err != nil {
			t.Fatalf("add Ticket %d: %v", ticket.TicketID, err)
		}
	}
	if err := node.BeginMatchRound(100); err != nil {
		t.Fatalf("begin match round: %v", err)
	}

	match, err := node.ProduceMatch(context.Background())
	if err != nil {
		t.Fatalf("trusted Fact flow was rejected by runtime validation: %v", err)
	}
	if match == nil || len(match.Tickets) != 2 {
		t.Fatalf("unexpected trusted Fact flow Match: %#v", match)
	}
	if got := match.Facts.Int64Values["match-count"]; got != 2 {
		t.Fatalf("unexpected Match Fact count: got %d, want 2", got)
	}
	if got := node.Len(); got != 0 {
		t.Fatalf("successful trusted Fact flow did not commit Tickets: Len=%d", got)
	}

	missingNode, err := NewLogicalNode(trustedFactFlowSpec(t, e2eTrustedFactsProvider{omitCount: true}))
	if err != nil {
		t.Fatalf("create missing Fact flow node: %v", err)
	}
	if _, err := missingNode.Add(&Ticket{TicketID: 3, StringLists: map[string][]string{"partition": {"blue"}}}); err != nil {
		t.Fatalf("add missing Fact Ticket: %v", err)
	}
	if err := missingNode.BeginMatchRound(100); err != nil {
		t.Fatalf("begin missing Fact match round: %v", err)
	}
	if _, err := missingNode.ProduceMatch(context.Background()); err == nil {
		t.Fatal("missing expression Fact did not fail")
	} else {
		var evaluationErr *evaluation.Error
		if !errors.As(err, &evaluationErr) {
			t.Fatalf("missing Fact error is not structured evaluation error: %v", err)
		}
		if evaluationErr.Code != "MISSING_VALUE" {
			t.Fatalf("missing Fact error code: got %q, want MISSING_VALUE", evaluationErr.Code)
		}
	}
}
