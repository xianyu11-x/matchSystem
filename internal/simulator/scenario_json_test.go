package simulator

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/xianyu11-x/matchSystem/pkg/matchsystem"
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
	for _, removed := range []string{"factProviderDescriptor", "objectFactProviderDescriptor", "matchFactProviderDescriptor", "descriptorSource"} {
		if strings.Contains(text, removed) {
			t.Fatalf("wire JSON retains removed provider declaration %q: %s", removed, text)
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
	if err := json.Unmarshal([]byte(`{"schemaVersion":"simulator-scenario/v1","physicalNodes":[],"rules":[{"logicalNode":{"rule":{"namespace":"11495104353665842533","ruleId":1},"placementId":"17745680053102999587"},"physicalNodeId":"17745680053102999587","rule":{},"objectFactProviderDescriptor":{}}]}`), &Scenario{}); err == nil {
		t.Fatal("Scenario accepted removed provider descriptor field")
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

func TestRuleSpecJSONPreservesProviderAndFactWireBehavior(t *testing.T) {
	scenario, _ := testScenario()
	rule := scenario.Rules[0]
	rule.PythonProvider = &PythonProviderConfig{Script: "provider.py", TimeoutMS: 250}
	rule.TickFacts = FactSnapshot{StringLists: map[string][]string{"empty": {}}}

	data, err := json.Marshal(rule)
	if err != nil {
		t.Fatalf("序列化规则失败: %v", err)
	}
	var wire struct {
		TickFacts struct {
			Strings map[string]json.RawMessage `json:"strings"`
		} `json:"tickFacts"`
		PythonProvider *PythonProviderConfig `json:"pythonProvider"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatal(err)
	}
	if string(wire.TickFacts.Strings["empty"]) != "null" || wire.PythonProvider == nil || *wire.PythonProvider != *rule.PythonProvider {
		t.Fatalf("Fact 空值或 Python Provider 的 JSON 表示改变: %s", data)
	}
	if strings.Contains(strings.ToLower(string(data)), "objectfactprovider") {
		t.Fatalf("进程内 Provider 不应出现在 JSON 中: %s", data)
	}

	input := strings.Replace(string(data), `"empty":null`, `"empty":[]`, 1)
	var decoded RuleSpec
	if err := json.Unmarshal([]byte(input), &decoded); err != nil {
		t.Fatalf("解析规则失败: %v", err)
	}
	if decoded.TickFacts.StringLists["empty"] != nil || decoded.TickFacts.Uint64Lists == nil || decoded.TickFacts.Int64Values == nil || decoded.PythonProvider == nil || *decoded.PythonProvider != *rule.PythonProvider {
		t.Fatalf("Fact 空值或 Python Provider 解析结果改变: %+v", decoded)
	}
	if decoded.ObjectFactProvider != nil {
		t.Fatal("解析 JSON 不应恢复进程内 Provider")
	}
}
