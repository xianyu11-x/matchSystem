package prefilter

import (
	"strings"
	"testing"

	"matchSystem/internal/common"
)

func TestStepInt64BindsArbitraryInput(t *testing.T) {
	expr := StepInt64(
		SeedInt64("source"),
		Int64Step{At: -10, Value: 1},
		Int64Step{At: 0, Value: 2},
		Int64Step{At: 10, Value: 3},
	)

	tests := []struct {
		input int64
		want  int64
	}{
		{input: -20, want: 1},
		{input: -10, want: 1},
		{input: -1, want: 1},
		{input: 0, want: 2},
		{input: 9, want: 2},
		{input: 10, want: 3},
		{input: 20, want: 3},
	}
	for _, tt := range tests {
		value, err := expr.bindInt64(evalContext{seed: &common.Ticket{Int64Values: map[string]int64{"source": tt.input}}})
		if err != nil {
			t.Fatalf("bind input %d: %v", tt.input, err)
		}
		if value != tt.want {
			t.Fatalf("bind input %d = %d, want %d", tt.input, value, tt.want)
		}
	}
}

func TestStepInt64BindsSeedFact(t *testing.T) {
	expr := StepInt64(
		FactInt64("wait_millis"),
		Int64Step{At: 0, Value: 10},
		Int64Step{At: 50, Value: 20},
	)
	for _, input := range []int64{0, 49, 50, 100} {
		value, err := expr.bindInt64(evalContext{seedFacts: Facts{Int64Values: map[string]int64{"wait_millis": input}}})
		if err != nil {
			t.Fatal(err)
		}
		want := int64(10)
		if input >= 50 {
			want = 20
		}
		if value != want {
			t.Fatalf("input %d: value=%d want=%d", input, value, want)
		}
	}
}

func TestStepInt64CopiesSteps(t *testing.T) {
	steps := []Int64Step{{At: 0, Value: 10}, {At: 5, Value: 20}}
	step := StepInt64(LiteralInt64(5), steps...)
	steps[1].Value = 99

	value, err := step.bindInt64(evalContext{})
	if err != nil {
		t.Fatal(err)
	}
	if value != 20 {
		t.Fatalf("step retained caller slice: got %d", value)
	}
}

func TestClampInt64BindsBounds(t *testing.T) {
	expr := ClampInt64(SeedInt64("value"), FactInt64("min"), FactInt64("max"))
	facts := Facts{Int64Values: map[string]int64{"min": 10, "max": 20}}
	for _, tt := range []struct {
		input int64
		want  int64
	}{{5, 10}, {10, 10}, {15, 15}, {20, 20}, {25, 20}} {
		value, err := expr.bindInt64(evalContext{
			seed:      &common.Ticket{Int64Values: map[string]int64{"value": tt.input}},
			tickFacts: facts,
		})
		if err != nil {
			t.Fatalf("bind input %d: %v", tt.input, err)
		}
		if value != tt.want {
			t.Fatalf("bind input %d = %d, want %d", tt.input, value, tt.want)
		}
	}

	_, err := expr.bindInt64(evalContext{
		seed:      &common.Ticket{Int64Values: map[string]int64{"value": 15}},
		tickFacts: Facts{Int64Values: map[string]int64{"min": 20, "max": 10}},
	})
	if err == nil || !strings.Contains(err.Error(), "minimum 20 exceeds maximum 10") {
		t.Fatalf("expected invalid dynamic clamp bounds, got %v", err)
	}
}

func TestStepAndClampDriveInt64RangeQuery(t *testing.T) {
	radius := ClampInt64(
		StepInt64(
			SeedInt64("tier"),
			Int64Step{At: 0, Value: 5},
			Int64Step{At: 10, Value: 20},
		),
		LiteralInt64(0),
		LiteralInt64(10),
	)
	store := mustIndexStore(t, Config{
		Indexes: []IndexSpec{NewInt64RangeIndex(Int64RangeIndexConfig{Name: "numeric", Field: "value"})},
		Root: Lookup(Int64RangeQuery{
			Index: "numeric",
			Min:   SubInt64(SeedInt64("value"), radius),
			Max:   AddInt64(SeedInt64("value"), radius),
		}),
	})
	seed := indexedTicket(1, &common.Ticket{Int64Values: map[string]int64{"value": 100, "tier": 10}})
	addTickets(t, store,
		seed,
		indexedTicket(2, &common.Ticket{Int64Values: map[string]int64{"value": 90}}),
		indexedTicket(3, &common.Ticket{Int64Values: map[string]int64{"value": 89}}),
		indexedTicket(4, &common.Ticket{Int64Values: map[string]int64{"value": 110}}),
		indexedTicket(5, &common.Ticket{Int64Values: map[string]int64{"value": 111}}),
	)
	assertIDs(t, candidates(t, beginTick(t, store, Facts{}), seed), 1, 2, 4)
}

func TestCompileRejectsInvalidStepAndClampExpressions(t *testing.T) {
	cases := []struct {
		name string
		expr Int64Expr
		code string
	}{
		{"nil step input", StepInt64(nil, Int64Step{At: 0, Value: 1}), "NIL_VALUE"},
		{"empty steps", StepInt64(LiteralInt64(0)), "EMPTY_STEPS"},
		{"duplicate thresholds", StepInt64(LiteralInt64(0), Int64Step{At: 1}, Int64Step{At: 1}), "INVALID_STEPS"},
		{"descending thresholds", StepInt64(LiteralInt64(0), Int64Step{At: 1}, Int64Step{At: 0}), "INVALID_STEPS"},
		{"nil clamp value", ClampInt64(nil, LiteralInt64(0), LiteralInt64(1)), "NIL_VALUE"},
		{"nil clamp minimum", ClampInt64(LiteralInt64(0), nil, LiteralInt64(1)), "NIL_VALUE"},
		{"nil clamp maximum", ClampInt64(LiteralInt64(0), LiteralInt64(0), nil), "NIL_VALUE"},
		{"invalid literal clamp bounds", ClampInt64(LiteralInt64(0), LiteralInt64(2), LiteralInt64(1)), "INVALID_CLAMP_BOUNDS"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Compile(numericExpressionConfig(tc.expr))
			if err == nil || !strings.Contains(err.Error(), tc.code) {
				t.Fatalf("expected %s, got %v", tc.code, err)
			}
		})
	}
}

func TestCompileAcceptsNegativeGenericStepThreshold(t *testing.T) {
	_, err := Compile(numericExpressionConfig(StepInt64(
		LiteralInt64(-5),
		Int64Step{At: -10, Value: 1},
		Int64Step{At: 0, Value: 2},
	)))
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
}

func TestStepAndClampAffectFingerprint(t *testing.T) {
	left, err := Compile(numericExpressionConfig(ClampInt64(
		StepInt64(SeedInt64("value"), Int64Step{At: 0, Value: 10}),
		LiteralInt64(0),
		LiteralInt64(100),
	)))
	if err != nil {
		t.Fatal(err)
	}
	right, err := Compile(numericExpressionConfig(ClampInt64(
		StepInt64(SeedInt64("value"), Int64Step{At: 0, Value: 20}),
		LiteralInt64(0),
		LiteralInt64(100),
	)))
	if err != nil {
		t.Fatal(err)
	}
	if left.Fingerprint() == right.Fingerprint() {
		t.Fatalf("different step values produced the same fingerprint %s", left.Fingerprint())
	}
}

func TestStepAndClampRecordFactDependencies(t *testing.T) {
	config := numericExpressionConfig(ClampInt64(
		StepInt64(FactInt64("input"), Int64Step{At: 0, Value: 10}),
		FactInt64("min"),
		FactInt64("max"),
	))
	config.Facts = []FactSpec{
		{Name: "max", Type: FactTypeInt64},
		{Name: "input", Type: FactTypeInt64},
		{Name: "min", Type: FactTypeInt64},
	}
	plan, err := Compile(config)
	if err != nil {
		t.Fatal(err)
	}
	facts := plan.Requirements().Facts
	if len(facts) != 3 || facts[0].Name != "input" || facts[1].Name != "max" || facts[2].Name != "min" {
		t.Fatalf("unexpected fact contract: %#v", facts)
	}
}

func numericExpressionConfig(expr Int64Expr) Config {
	return Config{
		Indexes: []IndexSpec{NewInt64RangeIndex(Int64RangeIndexConfig{Name: "numeric", Field: "numeric"})},
		Root: Lookup(Int64RangeQuery{
			Index: "numeric",
			Min:   expr,
			Max:   LiteralInt64(100),
		}),
	}
}
