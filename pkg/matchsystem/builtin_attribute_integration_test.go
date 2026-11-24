package matchsystem

import (
	"testing"

	"github.com/xianyu11-x/matchSystem/pkg/common"
	"github.com/xianyu11-x/matchSystem/pkg/identity"
)

func TestBuiltinCreateTimeAcrossMatchingPipeline(t *testing.T) {
	contractJSON := `{
		"schemaVersion":"logical-node-contract/v3","attributes":[],
		"facts":[{"name":"match-count","type":"int64","scope":"match"}],
		"indexes":[{"type":"int64_range","name":"createTime"}]
	}`
	prefilterJSON := `{
		"schemaVersion":"prefilter/v3","bitmap":{"resultType":"bitmap","expr":{
			"op":"lookup_nearest","index":"createTime","direction":"right","limit":3,
			"target":{"schemaVersion":"expression-scalar/v3","resultType":"int64",
				"expr":{"op":"int64_ref","source":"seed_attributes","name":"createTime"}}
		}}
	}`
	evaluationJSON := `{
		"schemaVersion":"evaluation/v3",
		"canJoin":{"schemaVersion":"expression-scalar/v3","resultType":"bool","expr":{
			"op":"int64_gte",
			"left":{"op":"int64_ref","source":"candidate_attributes","name":"createTime"},
			"right":{"op":"int64_ref","source":"seed_attributes","name":"createTime"}
		}},
		"canComplete":{"schemaVersion":"expression-scalar/v3","resultType":"bool","expr":{
			"op":"int64_gte","left":{"op":"int64_ref","source":"match_facts","name":"match-count"},
			"right":{"op":"int64_literal","value":2}
		}}
	}`
	for _, test := range []struct {
		name, seedSelection, scoring string
	}{
		{"oldest and created_at", `{"type":"oldest","params":{}}`, `{"type":"created_at","params":{"direction":"ascending"}}`},
		{"priority and int64_field", `{"type":"int64_priority","params":{"field":"createTime","direction":"ascending"}}`, `{"type":"int64_field","params":{"field":"createTime","direction":"ascending"}}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			key := identity.LogicalNodeKey{Rule: identity.RuleKey{RuleID: 92}, PlacementID: 92}
			node := newAlgorithmIntegrationNode(t, key, contractJSON, prefilterJSON, evaluationJSON,
				test.scoring, test.seedSelection, logicalNodeConfig{
					CandidateLimitPerSeed: 3,
					MaxPlayers:            2,
					SeedScheduler: seedSchedulerConfig{
						AttemptLimitPerProduceMatch: 1,
						AttemptLimitPerMatchRound:   1,
					},
				}, integrationMatchFactProvider{})
			tickets := []*Ticket{{TicketID: 1, CreatedAt: 10}, {TicketID: 2, CreatedAt: -5}, {TicketID: 3, CreatedAt: 0}, {TicketID: 4, CreatedAt: 5}}
			addIntegrationTickets(t, node, tickets...)
			stored, ok := node.Get(2)
			if !ok || stored.CreatedAt != -5 {
				t.Fatalf("读取 Ticket 失败: %#v", stored)
			}
			if _, exists := stored.Int64Values[common.CreateTimeAttributeName]; exists {
				t.Fatal("读取 Ticket 包含不应物化的内置属性")
			}
			match := produceIntegrationMatch(t, node, 100)
			if len(match.Tickets) != 2 || match.Tickets[0].TicketID != 2 || match.Tickets[1].TicketID != 3 {
				t.Fatalf("匹配成员=%v，期望 2 和 3", match.Tickets)
			}
			for _, ticket := range append(tickets, match.Tickets...) {
				if _, exists := ticket.Int64Values[common.CreateTimeAttributeName]; exists {
					t.Fatal("调用方或成局 Ticket 包含物化的内置属性")
				}
			}
		})
	}
}
