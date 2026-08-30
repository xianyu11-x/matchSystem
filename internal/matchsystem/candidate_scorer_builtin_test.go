package matchsystem

import (
	"math"
	"strings"
	"testing"

	"matchSystem/internal/matchsystem/contract"
	"matchSystem/internal/matchsystem/fact"
)

func TestNewCandidateScorerConstant(t *testing.T) {
	scorer, err := NewCandidateScorer(CandidateScorerConfig{
		Type:   CandidateScorerConstant,
		Params: []byte(`{"value":2.5}`),
	}, contract.Contract{})
	if err != nil {
		t.Fatalf("create constant scorer: %v", err)
	}
	score, err := scorer(CandidateScoreContext{})
	if err != nil {
		t.Fatalf("score constant candidate: %v", err)
	}
	if score != 2.5 {
		t.Fatalf("constant score: got %v, want 2.5", score)
	}
}

func TestNewCandidateScorerCreatedAtDirectionAndWeight(t *testing.T) {
	tests := []struct {
		name   string
		params string
		want   float64
	}{
		{name: "descending", params: `{"direction":"descending"}`, want: 10},
		{name: "ascending", params: `{"direction":"ascending"}`, want: -10},
		{name: "weighted", params: `{"direction":"descending","weight":2.5}`, want: 25},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			scorer, err := NewCandidateScorer(CandidateScorerConfig{
				Type:   CandidateScorerCreatedAt,
				Params: []byte(test.params),
			}, contract.Contract{})
			if err != nil {
				t.Fatalf("create created_at scorer: %v", err)
			}
			score, err := scorer(CandidateScoreContext{Candidate: &Ticket{CreatedAt: 10}})
			if err != nil {
				t.Fatalf("score created_at candidate: %v", err)
			}
			if score != test.want {
				t.Fatalf("created_at score: got %v, want %v", score, test.want)
			}
		})
	}
}

func TestNewCandidateScorerInt64Field(t *testing.T) {
	schema := contract.Contract{Attributes: []contract.AttributeSpec{
		{Name: "rating", Type: fact.TypeInt64},
	}}
	const descending = `{"field":"rating","direction":"descending","weight":2}`
	scorer, err := NewCandidateScorer(CandidateScorerConfig{
		Type:   CandidateScorerInt64Field,
		Params: []byte(descending),
	}, schema)
	if err != nil {
		t.Fatalf("create int64_field scorer: %v", err)
	}
	for _, test := range []struct {
		name   string
		ticket *Ticket
		want   float64
	}{
		{name: "present", ticket: &Ticket{Int64Values: map[string]int64{"rating": 7}}, want: 14},
		{name: "missing", ticket: &Ticket{Int64Values: map[string]int64{}}, want: -math.MaxFloat64},
	} {
		t.Run(test.name, func(t *testing.T) {
			score, err := scorer(CandidateScoreContext{Candidate: test.ticket})
			if err != nil {
				t.Fatalf("score int64_field candidate: %v", err)
			}
			if score != test.want {
				t.Fatalf("int64_field score: got %v, want %v", score, test.want)
			}
		})
	}

	ascending, err := NewCandidateScorer(CandidateScorerConfig{
		Type:   CandidateScorerInt64Field,
		Params: []byte(`{"field":"rating","direction":"ascending"}`),
	}, schema)
	if err != nil {
		t.Fatalf("create ascending int64_field scorer: %v", err)
	}
	score, err := ascending(CandidateScoreContext{Candidate: &Ticket{Int64Values: map[string]int64{"rating": 7}}})
	if err != nil || score != -7 {
		t.Fatalf("ascending int64_field score: got %v, err %v; want -7, nil", score, err)
	}
}

func TestNewCandidateScorerStrictParams(t *testing.T) {
	schema := contract.Contract{Attributes: []contract.AttributeSpec{{Name: "rating", Type: fact.TypeInt64}}}
	tests := []struct {
		name   string
		typeID CandidateScorerType
		params string
	}{
		{name: "missing params", typeID: CandidateScorerConstant, params: ""},
		{name: "array params", typeID: CandidateScorerConstant, params: `[]`},
		{name: "null params", typeID: CandidateScorerConstant, params: `null`},
		{name: "trailing value", typeID: CandidateScorerConstant, params: `{"value":1} {}`},
		{name: "unknown constant field", typeID: CandidateScorerConstant, params: `{"value":1,"extra":true}`},
		{name: "invalid constant value", typeID: CandidateScorerConstant, params: `{"value":1e999}`},
		{name: "missing created_at direction", typeID: CandidateScorerCreatedAt, params: `{}`},
		{name: "invalid created_at direction", typeID: CandidateScorerCreatedAt, params: `{"direction":"sideways"}`},
		{name: "zero weight", typeID: CandidateScorerCreatedAt, params: `{"direction":"descending","weight":0}`},
		{name: "negative weight", typeID: CandidateScorerCreatedAt, params: `{"direction":"descending","weight":-1}`},
		{name: "overflowing weight", typeID: CandidateScorerCreatedAt, params: `{"direction":"descending","weight":1e300}`},
		{name: "unknown int64 field param", typeID: CandidateScorerInt64Field, params: `{"field":"rating","direction":"descending","extra":1}`},
		{name: "missing int64 direction", typeID: CandidateScorerInt64Field, params: `{"field":"rating"}`},
		{name: "missing int64 field", typeID: CandidateScorerInt64Field, params: `{"direction":"descending"}`},
		{name: "missingScore nonfinite", typeID: CandidateScorerInt64Field, params: `{"field":"rating","direction":"descending","missingScore":1e999}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewCandidateScorer(CandidateScorerConfig{Type: test.typeID, Params: []byte(test.params)}, schema); err == nil {
				t.Fatal("invalid scorer configuration was accepted")
			}
		})
	}
}

func TestNewCandidateScorerInt64FieldRequiresContractInt64Attribute(t *testing.T) {
	for _, schema := range []contract.Contract{
		{},
		{Attributes: []contract.AttributeSpec{{Name: "rating", Type: fact.TypeStrings, MaxValues: 1}}},
	} {
		_, err := NewCandidateScorer(CandidateScorerConfig{
			Type:   CandidateScorerInt64Field,
			Params: []byte(`{"field":"rating","direction":"descending"}`),
		}, schema)
		if err == nil {
			t.Fatal("int64_field scorer accepted a non-int64 or undeclared attribute")
		}
	}
}

func TestNewCandidateScorerRejectsUnknownTypeAndNilCandidate(t *testing.T) {
	if _, err := NewCandidateScorer(CandidateScorerConfig{Type: "future", Params: []byte(`{}`)}, contract.Contract{}); err == nil {
		t.Fatal("unknown scorer type was accepted")
	}
	scorer, err := NewCandidateScorer(CandidateScorerConfig{
		Type:   CandidateScorerCreatedAt,
		Params: []byte(`{"direction":"descending"}`),
	}, contract.Contract{})
	if err != nil {
		t.Fatalf("create scorer: %v", err)
	}
	if _, err := scorer(CandidateScoreContext{}); err == nil || !strings.Contains(err.Error(), "nil candidate") {
		t.Fatalf("nil candidate error: %v", err)
	}
}
