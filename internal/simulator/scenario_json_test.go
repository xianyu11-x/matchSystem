package simulator

import (
	"encoding/json"
	"strings"
	"testing"

	"matchSystem/internal/matchsystem"
)

func TestScenarioJSONUsesTransportNamesAndRoundTrips(t *testing.T) {
	scenario, key := testScenario()
	scenario.MatchHistoryLimit = 37
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
	for _, expected := range []string{"\"logicalNode\"", "\"ruleId\"", "\"placementId\"", "\"rule\"", "\"match-rule/v1\"", "\"candidateLimitPerSeed\"", "\"attemptLimitPerMatchRound\""} {
		if !strings.Contains(text, expected) {
			t.Fatalf("wire JSON lacks %s: %s", expected, text)
		}
	}
	var roundTrip Scenario
	if err := json.Unmarshal(data, &roundTrip); err != nil {
		t.Fatalf("Unmarshal scenario: %v", err)
	}
	wantRule, wantErr := matchsystem.CompileRuleJSON(scenario.Rules[0].RuleJSON)
	gotRule, gotErr := matchsystem.CompileRuleJSON(roundTrip.Rules[0].RuleJSON)
	if roundTrip.Rules[0].LogicalNode != key || wantErr != nil || gotErr != nil || wantRule.Fingerprint() != gotRule.Fingerprint() {
		t.Fatalf("scenario did not round-trip identity/config: %#v", roundTrip.Rules[0])
	}
	if roundTrip.MatchHistoryLimit != scenario.MatchHistoryLimit {
		t.Fatalf("scenario did not round-trip matchHistoryLimit: got %d, want %d", roundTrip.MatchHistoryLimit, scenario.MatchHistoryLimit)
	}
	if _, err := ParseScenarioJSON(append(data, []byte(" {}")...)); err == nil {
		t.Fatal("ParseScenarioJSON accepted trailing JSON")
	}
	if err := json.Unmarshal([]byte(`{"schemaVersion":"simulator-scenario/v1","physicalNodes":[],"rules":[],"unknown":true}`), &Scenario{}); err == nil {
		t.Fatal("Scenario accepted an unknown top-level field")
	}
}
