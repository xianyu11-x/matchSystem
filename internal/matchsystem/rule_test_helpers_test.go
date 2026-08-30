package matchsystem

import (
	"encoding/json"
	"testing"

	"matchSystem/internal/identity"
)

func testRuleJSON(t *testing.T, key identity.RuleKey, contractJSON, prefilterJSON, evaluationJSON string, config logicalNodeConfig) []byte {
	t.Helper()
	if config.CandidateLimitPerSeed <= 0 {
		config.CandidateLimitPerSeed = 128
	}
	if config.MaxPlayers <= 0 {
		config.MaxPlayers = 8
	}
	if config.SeedScheduler.AttemptLimitPerProduceMatch <= 0 {
		config.SeedScheduler.AttemptLimitPerProduceMatch = 500
	}
	if config.SeedScheduler.AttemptLimitPerMatchRound <= 0 {
		config.SeedScheduler.AttemptLimitPerMatchRound = 500
	}
	document := struct {
		SchemaVersion string `json:"schemaVersion"`
		RuleKey       struct {
			Namespace string `json:"namespace,omitempty"`
			RuleID    int32  `json:"ruleId"`
		} `json:"ruleKey"`
		Contract      json.RawMessage `json:"contract"`
		Prefilter     json.RawMessage `json:"prefilter"`
		Evaluation    json.RawMessage `json:"evaluation"`
		Scoring       json.RawMessage `json:"scoring"`
		SeedSelection json.RawMessage `json:"seedSelection"`
		Runtime       struct {
			CandidateLimitPerSeed       int `json:"candidateLimitPerSeed"`
			MaxPlayers                  int `json:"maxPlayers"`
			AttemptLimitPerProduceMatch int `json:"attemptLimitPerProduceMatch"`
			AttemptLimitPerMatchRound   int `json:"attemptLimitPerMatchRound"`
		} `json:"runtime"`
	}{
		SchemaVersion: RuleJSONSchemaVersion,
		Contract:      json.RawMessage(contractJSON),
		Prefilter:     json.RawMessage(prefilterJSON),
		Evaluation:    json.RawMessage(evaluationJSON),
		Scoring:       json.RawMessage(`{"type":"constant","params":{"value":0}}`),
		SeedSelection: json.RawMessage(`{"type":"arrival","params":{}}`),
	}
	document.RuleKey.Namespace = key.Namespace
	document.RuleKey.RuleID = key.RuleID
	document.Runtime.CandidateLimitPerSeed = config.CandidateLimitPerSeed
	document.Runtime.MaxPlayers = config.MaxPlayers
	document.Runtime.AttemptLimitPerProduceMatch = config.SeedScheduler.AttemptLimitPerProduceMatch
	document.Runtime.AttemptLimitPerMatchRound = config.SeedScheduler.AttemptLimitPerMatchRound
	data, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("marshal test Rule JSON: %v", err)
	}
	return data
}
