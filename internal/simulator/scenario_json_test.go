package simulator

import (
	"encoding/json"
	"strings"
	"testing"

	"matchSystem/internal/matchsystem"
)

func TestScenarioJSONUsesTransportNamesAndRoundTrips(t *testing.T) {
	scenario, key := testScenario()
	scenario.Rules[0].Config = matchsystem.LogicalNodeConfig{
		CandidateLimitPerSeed: 17,
		MaxPlayers:            3,
		SeedScheduler: matchsystem.SeedSchedulerConfig{
			AttemptLimitPerProduceMatch: 4,
			AttemptLimitPerMatchRound:   9,
			Order: matchsystem.SeedOrderPolicyConfig{
				Kind:              matchsystem.SeedOrderInt64Priority,
				PriorityField:     "priority",
				PriorityDirection: matchsystem.SeedPriorityAscending,
				RandomSeed:        5,
			},
		},
	}
	data, err := json.Marshal(scenario)
	if err != nil {
		t.Fatalf("Marshal scenario: %v", err)
	}
	text := string(data)
	for _, leaked := range []string{"\"Rule\"", "\"Namespace\"", "\"RuleID\"", "\"PlacementID\"", "\"SeedScheduler\"", "\"CandidateLimitPerSeed\""} {
		if strings.Contains(text, leaked) {
			t.Fatalf("wire JSON leaks internal field %s: %s", leaked, text)
		}
	}
	for _, expected := range []string{"\"logicalNode\"", "\"ruleId\"", "\"placementId\"", "\"seedScheduler\"", "\"candidateLimitPerSeed\"", "\"attemptLimitPerMatchRound\""} {
		if !strings.Contains(text, expected) {
			t.Fatalf("wire JSON lacks %s: %s", expected, text)
		}
	}
	var roundTrip Scenario
	if err := json.Unmarshal(data, &roundTrip); err != nil {
		t.Fatalf("Unmarshal scenario: %v", err)
	}
	if roundTrip.Rules[0].LogicalNode != key || roundTrip.Rules[0].Config != scenario.Rules[0].Config {
		t.Fatalf("scenario did not round-trip identity/config: %#v", roundTrip.Rules[0])
	}
	if _, err := ParseScenarioJSON(append(data, []byte(" {}")...)); err == nil {
		t.Fatal("ParseScenarioJSON accepted trailing JSON")
	}
	if err := json.Unmarshal([]byte(`{"schemaVersion":"simulator-scenario/v1","physicalNodes":[],"rules":[],"unknown":true}`), &Scenario{}); err == nil {
		t.Fatal("Scenario accepted an unknown top-level field")
	}
}
