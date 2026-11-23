package matchsystem

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/xianyu11-x/matchSystem/pkg/identity"
)

const integrationPartitionContractJSON = `{
	"schemaVersion":"logical-node-contract/v3",
	"attributes":[{"name":"partition","type":"strings","maxValues":1},{"name":"rating","type":"int64"}],
	"facts":[{"name":"match-count","type":"int64","scope":"match"}],
	"indexes":[{"type":"multi_value","name":"partition","keyType":"string","maxDocumentValues":1,"maxQueryValues":1}]
}`

const integrationPriorityContractJSON = `{
	"schemaVersion":"logical-node-contract/v3",
	"attributes":[{"name":"priority","type":"int64"}],
	"facts":[],
	"indexes":[]
}`

const integrationEmptyContractJSON = `{
	"schemaVersion":"logical-node-contract/v3",
	"attributes":[],
	"facts":[],
	"indexes":[]
}`

const integrationBluePrefilterJSON = `{
	"schemaVersion":"prefilter/v3",
	"bitmap":{"resultType":"bitmap","expr":{
		"op":"lookup_string","index":"partition","values":{
			"schemaVersion":"expression-scalar/v3","resultType":"strings",
			"expr":{"op":"strings_literal","values":["blue"]}
		}
	}}
}`

const integrationNonePrefilterJSON = `{
	"schemaVersion":"prefilter/v3",
	"bitmap":{"resultType":"bitmap","expr":{"op":"none"}}
}`

const integrationMatchCountEvaluationJSON = `{
	"schemaVersion":"evaluation/v3",
	"canJoin":{"schemaVersion":"expression-scalar/v3","resultType":"bool","expr":{"op":"bool_literal","value":true}},
	"canComplete":{"schemaVersion":"expression-scalar/v3","resultType":"bool","expr":{
		"op":"int64_gte",
		"left":{"op":"int64_ref","source":"match_facts","name":"match-count"},
		"right":{"op":"int64_literal","value":2}
	}}
}`

const integrationCompleteEvaluationJSON = `{
	"schemaVersion":"evaluation/v3",
	"canJoin":{"schemaVersion":"expression-scalar/v3","resultType":"bool","expr":{"op":"bool_literal","value":true}},
	"canComplete":{"schemaVersion":"expression-scalar/v3","resultType":"bool","expr":{"op":"bool_literal","value":true}}
}`

// Test-only MatchFactProvider makes a candidate join observable as a complete
// Match while keeping the production rule's Match Fact boundary intact.
type integrationMatchFactProvider struct{}

func (integrationMatchFactProvider) Describe() (ProviderDescriptor, error) {
	return ProviderDescriptor{
		ID:      "test.integration.match",
		Version: "v1",
		Facts:   []FactSpec{{Name: "match-count", Type: FactTypeInt64, Scope: FactScopeMatch}},
	}, nil
}

func (integrationMatchFactProvider) Initialize(context.Context, InitializeInput) (Facts, error) {
	return Facts{Int64Values: map[string]int64{"match-count": 1}}, nil
}

func (integrationMatchFactProvider) OnJoin(_ context.Context, input JoinInput) (Facts, error) {
	return Facts{Int64Values: map[string]int64{
		"match-count": input.MatchFactsBefore.Int64Values["match-count"] + 1,
	}}, nil
}

func TestMatchRuleJSONScoringCreatedAtControlsCandidateTop1(t *testing.T) {
	tests := []struct {
		name        string
		direction   string
		wantID      TicketID
		placementID identity.PlacementID
	}{
		{name: "ascending", direction: "ascending", wantID: 102, placementID: 17918979904228871755},
		{name: "descending", direction: "descending", wantID: 103, placementID: 2432492588830630307},
	}

	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			key := identity.LogicalNodeKey{
				Rule:        identity.RuleKey{Namespace: "9226410116366564406", RuleID: uint64(index + 1)},
				PlacementID: test.placementID,
			}
			scoringJSON := fmt.Sprintf(`{"type":"created_at","params":{"direction":%q}}`, test.direction)
			node := newAlgorithmIntegrationNode(t, key, integrationPartitionContractJSON,
				integrationBluePrefilterJSON, integrationMatchCountEvaluationJSON,
				scoringJSON, `{"type":"arrival","params":{}}`, logicalNodeConfig{
					CandidateLimitPerSeed: 1,
					MaxPlayers:            2,
					SeedScheduler: seedSchedulerConfig{
						AttemptLimitPerProduceMatch: 1,
						AttemptLimitPerMatchRound:   1,
					},
				}, integrationMatchFactProvider{})

			addIntegrationTickets(t, node,
				&Ticket{TicketID: 101, CreatedAt: 100, StringLists: map[string][]string{"partition": {"blue"}}},
				&Ticket{TicketID: 102, CreatedAt: 10, StringLists: map[string][]string{"partition": {"blue"}}},
				&Ticket{TicketID: 103, CreatedAt: 20, StringLists: map[string][]string{"partition": {"blue"}}},
			)
			match := produceIntegrationMatch(t, node, 500)
			if len(match.Tickets) != 2 {
				t.Fatalf("unexpected Match size: got %d, want 2", len(match.Tickets))
			}
			if got := match.Tickets[1].TicketID; got != test.wantID {
				t.Fatalf("JSON created_at %s scorer selected TicketID %d, want %d", test.direction, got, test.wantID)
			}
		})
	}
}

func TestMatchRuleJSONScoringInt64FieldControlsMissingScoreAndDirection(t *testing.T) {
	tests := []struct {
		name          string
		scoringJSON   string
		tickets       []*Ticket
		wantCandidate TicketID
		placementID   identity.PlacementID
	}{
		{
			name:        "descending-missing-score-keeps-missing-last",
			placementID: 14535901016590431860,
			scoringJSON: `{"type":"int64_field","params":{"field":"rating","direction":"descending","missingScore":-100}}`,
			tickets: []*Ticket{
				{TicketID: 202, StringLists: map[string][]string{"partition": {"blue"}}},
				{TicketID: 203, StringLists: map[string][]string{"partition": {"blue"}}, Int64Values: map[string]int64{"rating": 5}},
			},
			wantCandidate: 203,
		},
		{
			name:        "ascending-chooses-smallest-present-value",
			placementID: 6915772482503861377,
			scoringJSON: `{"type":"int64_field","params":{"field":"rating","direction":"ascending","missingScore":-100}}`,
			tickets: []*Ticket{
				{TicketID: 212, StringLists: map[string][]string{"partition": {"blue"}}},
				{TicketID: 213, StringLists: map[string][]string{"partition": {"blue"}}, Int64Values: map[string]int64{"rating": 5}},
				{TicketID: 214, StringLists: map[string][]string{"partition": {"blue"}}, Int64Values: map[string]int64{"rating": 1}},
			},
			wantCandidate: 214,
		},
		{
			name:        "custom-missing-score-can-prefer-missing",
			placementID: 11707112872320090725,
			scoringJSON: `{"type":"int64_field","params":{"field":"rating","direction":"descending","missingScore":100}}`,
			tickets: []*Ticket{
				{TicketID: 222, StringLists: map[string][]string{"partition": {"blue"}}},
				{TicketID: 223, StringLists: map[string][]string{"partition": {"blue"}}, Int64Values: map[string]int64{"rating": 5}},
			},
			wantCandidate: 222,
		},
	}

	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			key := identity.LogicalNodeKey{
				Rule:        identity.RuleKey{Namespace: "9914941855679688086", RuleID: uint64(index + 1)},
				PlacementID: test.placementID,
			}
			node := newAlgorithmIntegrationNode(t, key, integrationPartitionContractJSON,
				integrationBluePrefilterJSON, integrationMatchCountEvaluationJSON,
				test.scoringJSON, `{"type":"arrival","params":{}}`, logicalNodeConfig{
					CandidateLimitPerSeed: 1,
					MaxPlayers:            2,
					SeedScheduler: seedSchedulerConfig{
						AttemptLimitPerProduceMatch: 1,
						AttemptLimitPerMatchRound:   1,
					},
				}, integrationMatchFactProvider{})

			seed := &Ticket{TicketID: test.tickets[0].TicketID - 1, CreatedAt: 100, StringLists: map[string][]string{"partition": {"blue"}}}
			addIntegrationTickets(t, node, append([]*Ticket{seed}, test.tickets...)...)
			match := produceIntegrationMatch(t, node, 600)
			if len(match.Tickets) != 2 {
				t.Fatalf("unexpected Match size: got %d, want 2", len(match.Tickets))
			}
			if got := match.Tickets[1].TicketID; got != test.wantCandidate {
				t.Fatalf("JSON int64_field scorer selected TicketID %d, want %d", got, test.wantCandidate)
			}
		})
	}
}

func TestMatchRuleJSONSeedSelectionControlsFirstSeed(t *testing.T) {
	tests := []struct {
		name          string
		seedSelection string
		contractJSON  string
		tickets       []*Ticket
		wantSeed      TicketID
		placementID   identity.PlacementID
	}{
		{
			name:          "oldest",
			placementID:   17180811456043180029,
			seedSelection: `{"type":"oldest","params":{}}`,
			contractJSON:  integrationEmptyContractJSON,
			tickets: []*Ticket{
				{TicketID: 301, CreatedAt: 30},
				{TicketID: 302, CreatedAt: 10},
			},
			wantSeed: 302,
		},
		{
			name:          "int64-priority",
			placementID:   4355242246776132345,
			seedSelection: `{"type":"int64_priority","params":{"field":"priority","direction":"descending"}}`,
			contractJSON:  integrationPriorityContractJSON,
			tickets: []*Ticket{
				{TicketID: 311, Int64Values: map[string]int64{"priority": 1}},
				{TicketID: 312, Int64Values: map[string]int64{"priority": 9}},
			},
			wantSeed: 312,
		},
	}

	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			key := identity.LogicalNodeKey{
				Rule:        identity.RuleKey{Namespace: "8407903203132159160", RuleID: uint64(index + 1)},
				PlacementID: test.placementID,
			}
			node := newAlgorithmIntegrationNode(t, key, test.contractJSON,
				integrationNonePrefilterJSON, integrationCompleteEvaluationJSON,
				`{"type":"constant","params":{"value":0}}`, test.seedSelection, logicalNodeConfig{
					CandidateLimitPerSeed: 1,
					MaxPlayers:            1,
					SeedScheduler: seedSchedulerConfig{
						AttemptLimitPerProduceMatch: 1,
						AttemptLimitPerMatchRound:   1,
					},
				}, nil)
			addIntegrationTickets(t, node, test.tickets...)
			match := produceIntegrationMatch(t, node, 700)
			if len(match.Tickets) != 1 || match.Tickets[0].TicketID != test.wantSeed {
				var got TicketID
				if len(match.Tickets) > 0 && match.Tickets[0] != nil {
					got = match.Tickets[0].TicketID
				}
				t.Fatalf("JSON %s seed selection chose TicketID %d, want %d (Match=%#v)", test.name, got, test.wantSeed, match)
			}
		})
	}
}

func TestNewLogicalNodeRejectsRuleJSONRuleKeyMismatch(t *testing.T) {
	declared := identity.RuleKey{Namespace: "7748313037379561909", RuleID: 801}
	configured := identity.LogicalNodeKey{
		Rule:        identity.RuleKey{Namespace: "7748313037379561909", RuleID: 802},
		PlacementID: 4010717984327297149,
	}
	data := integrationRuleJSON(t, declared, integrationEmptyContractJSON,
		integrationNonePrefilterJSON, integrationCompleteEvaluationJSON,
		`{"type":"constant","params":{"value":0}}`,
		`{"type":"arrival","params":{}}`, logicalNodeConfig{
			CandidateLimitPerSeed: 1,
			MaxPlayers:            1,
			SeedScheduler: seedSchedulerConfig{
				AttemptLimitPerProduceMatch: 1,
				AttemptLimitPerMatchRound:   1,
			},
		})

	_, err := NewLogicalNode(LogicalNodeSpec{Key: configured, RuleJSON: data})
	if err == nil {
		t.Fatal("NewLogicalNode accepted a Rule JSON RuleKey different from LogicalNodeSpec.Key.Rule")
	}
	var configErr *RuleConfigError
	if !errors.As(err, &configErr) {
		t.Fatalf("mismatch error is not RuleConfigError: %T %v", err, err)
	}
	if configErr.Code != "RULE_KEY_MISMATCH" {
		t.Fatalf("mismatch error code: got %q, want RULE_KEY_MISMATCH (%v)", configErr.Code, err)
	}
}

func newAlgorithmIntegrationNode(t *testing.T, key identity.LogicalNodeKey, contractJSON, prefilterJSON, evaluationJSON, scoringJSON, seedSelectionJSON string, config logicalNodeConfig, matchFacts MatchProvider) *LogicalNode {
	t.Helper()
	node, err := NewLogicalNode(LogicalNodeSpec{
		Key:               key,
		RuleJSON:          integrationRuleJSON(t, key.Rule, contractJSON, prefilterJSON, evaluationJSON, scoringJSON, seedSelectionJSON, config),
		MatchFactProvider: matchFacts,
	})
	if err != nil {
		t.Fatalf("create LogicalNode from match-rule/v1: %v", err)
	}
	return node
}

func addIntegrationTickets(t *testing.T, node *LogicalNode, tickets ...*Ticket) {
	t.Helper()
	for _, ticket := range tickets {
		if err := node.Add(ticket); err != nil {
			t.Fatalf("add Ticket %d: %v", ticket.TicketID, err)
		}
	}
}

func produceIntegrationMatch(t *testing.T, node *LogicalNode, now int64) *Match {
	t.Helper()
	if err := node.BeginMatchRound(now); err != nil {
		t.Fatalf("begin match round: %v", err)
	}
	match, err := node.ProduceMatch(context.Background())
	if err != nil {
		t.Fatalf("produce Match: %v", err)
	}
	if match == nil {
		t.Fatal("expected a Match, got nil")
	}
	return match
}

func integrationRuleJSON(t *testing.T, key identity.RuleKey, contractJSON, prefilterJSON, evaluationJSON, scoringJSON, seedSelectionJSON string, config logicalNodeConfig) []byte {
	t.Helper()
	if config.CandidateScoringLimitPerSeed <= 0 {
		config.CandidateScoringLimitPerSeed = defaultCandidateScoringLimitPerSeed
	}
	if config.CandidateLimitPerSeed <= 0 {
		config.CandidateLimitPerSeed = defaultCandidateLimitPerSeed
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
		SchemaVersion string           `json:"schemaVersion"`
		RuleKey       identity.RuleKey `json:"ruleKey"`
		Contract      json.RawMessage  `json:"contract"`
		Prefilter     json.RawMessage  `json:"prefilter"`
		Evaluation    json.RawMessage  `json:"evaluation"`
		Scoring       json.RawMessage  `json:"scoring"`
		SeedSelection json.RawMessage  `json:"seedSelection"`
		Runtime       struct {
			CandidateScoringLimitPerSeed int `json:"candidateScoringLimitPerSeed"`
			CandidateLimitPerSeed        int `json:"candidateLimitPerSeed"`
			MaxPlayers                   int `json:"maxPlayers"`
			AttemptLimitPerProduceMatch  int `json:"attemptLimitPerProduceMatch"`
			AttemptLimitPerMatchRound    int `json:"attemptLimitPerMatchRound"`
		} `json:"runtime"`
	}{
		SchemaVersion: RuleJSONSchemaVersion,
		Contract:      json.RawMessage(contractJSON),
		Prefilter:     json.RawMessage(prefilterJSON),
		Evaluation:    json.RawMessage(evaluationJSON),
		Scoring:       json.RawMessage(scoringJSON),
		SeedSelection: json.RawMessage(seedSelectionJSON),
	}
	document.RuleKey.Namespace = key.Namespace
	document.RuleKey.RuleID = key.RuleID
	document.Runtime.CandidateScoringLimitPerSeed = config.CandidateScoringLimitPerSeed
	document.Runtime.CandidateLimitPerSeed = config.CandidateLimitPerSeed
	document.Runtime.MaxPlayers = config.MaxPlayers
	document.Runtime.AttemptLimitPerProduceMatch = config.SeedScheduler.AttemptLimitPerProduceMatch
	document.Runtime.AttemptLimitPerMatchRound = config.SeedScheduler.AttemptLimitPerMatchRound
	data, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("marshal match-rule/v1 fixture: %v", err)
	}
	return data
}
