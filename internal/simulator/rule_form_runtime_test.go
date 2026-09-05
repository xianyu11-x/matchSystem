package simulator

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"matchSystem/internal/common"
	"matchSystem/internal/matchsystem"
)

// Exercise the same Scenario -> CompileRuleJSON -> LogicalNode path used by
// the form's PUT /scenario, observing actual match members instead of config.
func formRuntimeScenario(t *testing.T, seed, scoring string, pair bool) Scenario {
	t.Helper()
	scenario, _ := testScenario()
	var rule map[string]any
	if err := json.Unmarshal(scenario.Rules[0].RuleJSON, &rule); err != nil {
		t.Fatal(err)
	}
	decode := func(value string) any {
		var result any
		if err := json.Unmarshal([]byte(value), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	rule["contract"] = decode(`{"schemaVersion":"logical-node-contract/v3","attributes":[{"name":"rating","type":"int64"},{"name":"pool","type":"strings","maxValues":1}],"facts":[],"indexes":[{"name":"pool","type":"multi_value","keyType":"string","maxDocumentValues":1,"maxQueryValues":1}]}`)
	rule["seedSelection"] = decode(seed)
	rule["scoring"] = decode(scoring)
	rule["runtime"] = decode(`{"maxPlayers":2,"candidateScoringLimitPerSeed":10,"candidateLimitPerSeed":1,"attemptLimitPerProduceMatch":10,"attemptLimitPerMatchRound":10}`)
	scenario.Rules[0].ObjectFactProviderDescriptor = nil
	if pair {
		rule["contract"].(map[string]any)["facts"] = decode(`[{"name":"memberCount","type":"int64","scope":"match"}]`)
		rule["prefilter"] = decode(`{"schemaVersion":"prefilter/v3","bitmap":{"resultType":"bitmap","expr":{"op":"lookup_string","index":"pool","values":{"schemaVersion":"expression-scalar/v3","resultType":"strings","expr":{"op":"strings_literal","values":["all"]}}}}}`)
		rule["evaluation"].(map[string]any)["canComplete"] = decode(`{"schemaVersion":"expression-scalar/v3","resultType":"bool","expr":{"op":"int64_gte","left":{"op":"int64_ref","source":"match_facts","name":"memberCount"},"right":{"op":"int64_literal","value":2}}}`)
		scenario.Rules[0].MatchFactProviderDescriptor = &matchsystem.ProviderDescriptor{ID: "test.match", Version: "1", Facts: []matchsystem.FactSpec{{Name: "memberCount", Type: matchsystem.FactTypeInt64, Scope: matchsystem.FactScopeMatch}}}
	}
	encoded, err := json.Marshal(rule)
	if err != nil {
		t.Fatal(err)
	}
	scenario.Rules[0].RuleJSON = encoded
	return scenario
}

func formRuntimeMembers(t *testing.T, scenario Scenario, count int) []common.TicketID {
	t.Helper()
	service, err := NewService(scenario)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	ctx := context.Background()
	for index, created := range []int64{400, 300, 200} {
		_, err := service.AddTicket(ctx, TicketInput{Rule: scenario.Rules[0].LogicalNode.Rule, TicketID: common.TicketID(index + 1), CreatedAt: created, StringLists: map[string][]string{"pool": {"all"}}, Int64Values: map[string]int64{"rating": int64(index * 10)}})
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := service.BeginRound(ctx, 1000); err != nil {
		t.Fatal(err)
	}
	var members []common.TicketID
	for range count {
		result, err := service.ProduceMatch(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if result.Match == nil {
			t.Fatal("expected a match")
		}
		for _, ticket := range result.Match.Tickets {
			members = append(members, ticket.TicketID)
		}
	}
	return members
}

func TestRuleFormSeedSelectionReachesRuntime(t *testing.T) {
	for _, test := range []struct {
		name, seed string
		want       []common.TicketID
	}{
		{"arrival", `{"type":"arrival","params":{}}`, []common.TicketID{1, 2, 3}},
		{"oldest", `{"type":"oldest","params":{}}`, []common.TicketID{3, 2, 1}},
		{"priority ascending", `{"type":"int64_priority","params":{"field":"rating","direction":"ascending"}}`, []common.TicketID{1, 2, 3}},
		{"priority descending", `{"type":"int64_priority","params":{"field":"rating","direction":"descending"}}`, []common.TicketID{3, 2, 1}},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := formRuntimeMembers(t, formRuntimeScenario(t, test.seed, `{"type":"constant","params":{"value":0}}`, false), 3)
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("members=%v want %v", got, test.want)
			}
		})
	}
	scenario := formRuntimeScenario(t, `{"type":"random","params":{"randomSeed":57}}`, `{"type":"constant","params":{"value":0}}`, false)
	first, second := formRuntimeMembers(t, scenario, 3), formRuntimeMembers(t, scenario, 3)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("random seed did not reproduce: %v / %v", first, second)
	}
}

func TestRuleFormCandidateScoringReachesRuntime(t *testing.T) {
	for _, test := range []struct {
		name, scoring string
		candidate     common.TicketID
	}{
		{"constant", `{"type":"constant","params":{"value":3.5}}`, 2},
		{"created ascending", `{"type":"created_at","params":{"direction":"ascending","weight":2}}`, 3},
		{"created descending", `{"type":"created_at","params":{"direction":"descending","weight":2}}`, 2},
		{"field ascending", `{"type":"int64_field","params":{"field":"rating","direction":"ascending","weight":2,"missingScore":-100}}`, 2},
		{"field descending", `{"type":"int64_field","params":{"field":"rating","direction":"descending","weight":2,"missingScore":-100}}`, 3},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := formRuntimeMembers(t, formRuntimeScenario(t, `{"type":"arrival","params":{}}`, test.scoring, true), 1)
			if !reflect.DeepEqual(got, []common.TicketID{1, test.candidate}) {
				t.Fatalf("members=%v, expected seed 1 and candidate %d", got, test.candidate)
			}
		})
	}
}
