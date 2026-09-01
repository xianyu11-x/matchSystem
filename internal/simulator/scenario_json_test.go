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
	for _, expected := range []string{"\"objectFactProviderDescriptor\"", "\"id\":\"test.object-facts\"", "\"facts\":[{\"name\":\"object_tag\""} {
		if !strings.Contains(text, expected) {
			t.Fatalf("wire JSON lacks explicit provider descriptor %s: %s", expected, text)
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
	if roundTrip.Rules[0].ObjectFactProviderDescriptor == nil ||
		roundTrip.Rules[0].ObjectFactProviderDescriptor.ID != "test.object-facts" ||
		len(roundTrip.Rules[0].ObjectFactProviderDescriptor.Facts) != 1 {
		t.Fatalf("provider descriptor did not round-trip independently: %#v", roundTrip.Rules[0].ObjectFactProviderDescriptor)
	}
	if _, err := ParseScenarioJSON(append(data, []byte(" {}")...)); err == nil {
		t.Fatal("ParseScenarioJSON accepted trailing JSON")
	}
	if err := json.Unmarshal([]byte(`{"schemaVersion":"simulator-scenario/v1","physicalNodes":[],"rules":[],"unknown":true}`), &Scenario{}); err == nil {
		t.Fatal("Scenario accepted an unknown top-level field")
	}
}

func TestScenarioJSONUsesCanonicalDefaultsForEmptyScenario(t *testing.T) {
	data, err := json.Marshal(Scenario{})
	if err != nil {
		t.Fatalf("Marshal empty scenario: %v", err)
	}
	if got, want := string(data), `{"schemaVersion":"simulator-scenario/v1","physicalNodes":[],"rules":[]}`; got != want {
		t.Fatalf("empty scenario JSON=%s, want %s", got, want)
	}
	var roundTrip Scenario
	if err := json.Unmarshal(data, &roundTrip); err != nil {
		t.Fatalf("Unmarshal canonical empty scenario: %v", err)
	}
	if roundTrip.SchemaVersion != ScenarioSchemaVersion || roundTrip.PhysicalNodes == nil || roundTrip.Rules == nil {
		t.Fatalf("canonical empty scenario did not round-trip: %#v", roundTrip)
	}
}
