package matchsystem

import (
	"testing"

	"github.com/xianyu11-x/matchSystem/pkg/identity"
)

func TestMatchRuleJSONLookupNearestSelectsCandidate(t *testing.T) {
	key := identity.LogicalNodeKey{
		Rule:        identity.RuleKey{RuleID: 91},
		PlacementID: 91,
	}
	contractJSON := `{
		"schemaVersion":"logical-node-contract/v3",
		"attributes":[{"name":"sequence","type":"int64"}],
		"facts":[{"name":"match-count","type":"int64","scope":"match"}],
		"indexes":[{"type":"int64_range","name":"sequence"}]
	}`
	prefilterJSON := `{
		"schemaVersion":"prefilter/v3","bitmap":{"resultType":"bitmap","expr":{
			"op":"lookup_nearest","index":"sequence","direction":"right","limit":1,
			"target":{"schemaVersion":"expression-scalar/v3","resultType":"int64",
				"expr":{"op":"int64_ref","source":"seed_attributes","name":"sequence"}}
		}}
	}`
	node := newAlgorithmIntegrationNode(t, key, contractJSON, prefilterJSON,
		integrationMatchCountEvaluationJSON, `{"type":"constant","params":{"value":0}}`,
		`{"type":"arrival","params":{}}`, logicalNodeConfig{
			CandidateLimitPerSeed: 1,
			MaxPlayers:            2,
			SeedScheduler: seedSchedulerConfig{
				AttemptLimitPerProduceMatch: 1,
				AttemptLimitPerMatchRound:   1,
			},
		}, integrationMatchFactProvider{})
	addIntegrationTickets(t, node,
		&Ticket{TicketID: 1, Int64Values: map[string]int64{"sequence": 10}},
		&Ticket{TicketID: 2, Int64Values: map[string]int64{"sequence": 100}},
		&Ticket{TicketID: 3, Int64Values: map[string]int64{"sequence": 11}},
	)
	match := produceIntegrationMatch(t, node, 100)
	if len(match.Tickets) != 2 || match.Tickets[0].TicketID != 1 || match.Tickets[1].TicketID != 3 {
		t.Fatalf("匹配结果=%v，期望 seed 1 与候选 3", match.Tickets)
	}
}
