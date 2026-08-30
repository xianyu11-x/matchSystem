package matchsystem

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"matchSystem/internal/identity"
)

func TestCompileRuleJSON(t *testing.T) {
	compiled, err := CompileRuleJSON([]byte(validRuleJSON()))
	if err != nil {
		t.Fatalf("compile complete rule: %v", err)
	}
	if got, want := compiled.RuleKey(), (identity.RuleKey{Namespace: "demo", RuleID: 1001}); got != want {
		t.Fatalf("RuleKey: got %#v, want %#v", got, want)
	}
	if compiled.plan == nil || compiled.scorer == nil || compiled.seedPolicy == nil {
		t.Fatalf("compiled rule did not retain all runtime products: %#v", compiled)
	}
	if got, want := compiled.config.CandidateLimitPerSeed, 128; got != want {
		t.Fatalf("candidate limit: got %d, want %d", got, want)
	}
	if got, want := compiled.config.SeedScheduler.AttemptLimitPerProduceMatch, 10; got != want {
		t.Fatalf("produce attempt limit: got %d, want %d", got, want)
	}
	if got, want := compiled.config.SeedScheduler.AttemptLimitPerMatchRound, 20; got != want {
		t.Fatalf("round attempt limit: got %d, want %d", got, want)
	}
	if len(compiled.Fingerprint()) != 64 {
		t.Fatalf("fingerprint should be a SHA-256 hex digest: %q", compiled.Fingerprint())
	}

	owned := compiled.Contract()
	owned.Attributes[0].Name = "changed"
	if got := compiled.Contract().Attributes[0].Name; got != "priority" {
		t.Fatalf("Contract returned a mutable internal value: %q", got)
	}
}

func TestCompileRuleJSONFingerprintCanonicalizesObjectOrder(t *testing.T) {
	first, err := CompileRuleJSON([]byte(validRuleJSON()))
	if err != nil {
		t.Fatalf("compile first rule: %v", err)
	}
	reordered := reorderRuleJSON(t, validRuleJSON())
	second, err := CompileRuleJSON([]byte(reordered))
	if err != nil {
		t.Fatalf("compile reordered rule: %v\njson=%s", err, reordered)
	}
	if first.Fingerprint() != second.Fingerprint() {
		t.Fatalf("equivalent JSON has different fingerprints: %q != %q", first.Fingerprint(), second.Fingerprint())
	}
}

func TestCompileRuleJSONStrictAggregateBoundary(t *testing.T) {
	tests := []struct {
		name string
		data string
		path string
		code string
	}{
		{
			name: "unknown top-level field",
			data: strings.TrimSuffix(validRuleJSON(), "}") + `,"unexpected":true}`,
			path: "$.unexpected",
			code: "UNKNOWN_FIELD",
		},
		{
			name: "missing schema version",
			data: strings.Replace(validRuleJSON(), `"schemaVersion":"match-rule/v1",`, "", 1),
			path: "$.schemaVersion",
			code: "UNKNOWN_SCHEMA_VERSION",
		},
		{
			name: "duplicate top-level field",
			data: strings.Replace(validRuleJSON(), `"schemaVersion":"match-rule/v1",`, `"schemaVersion":"match-rule/v1","schemaVersion":"match-rule/v1",`, 1),
			path: "$.schemaVersion",
			code: "DUPLICATE_KEY",
		},
		{
			name: "trailing JSON",
			data: validRuleJSON() + ` {}`,
			path: "$",
			code: "TRAILING_JSON",
		},
		{
			name: "null nested value",
			data: strings.Replace(validRuleJSON(), `"scoring":{"type":"constant","params":{"value":1}}`, `"scoring":null`, 1),
			path: "$.scoring",
			code: "NULL_NOT_ALLOWED",
		},
		{
			name: "missing top-level field",
			data: strings.Replace(validRuleJSON(), `,"runtime":{"candidateLimitPerSeed":128,"maxPlayers":8,"attemptLimitPerProduceMatch":10,"attemptLimitPerMatchRound":20}`, "", 1),
			path: "$.runtime",
			code: "MISSING_FIELD",
		},
		{
			name: "params must be object",
			data: strings.Replace(validRuleJSON(), `"params":{"value":1}`, `"params":[]`, 1),
			path: "$.scoring.params",
			code: "TYPE_MISMATCH",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := CompileRuleJSON([]byte(test.data))
			expectRuleConfigError(t, err, test.path, test.code)
		})
	}
}

func TestCompileRuleJSONSeedSelectionValidation(t *testing.T) {
	priority := strings.Replace(validRuleJSON(), `"seedSelection":{"type":"arrival","params":{}}`, `"seedSelection":{"type":"int64_priority","params":{"field":"priority","direction":"descending"}}`, 1)
	if _, err := CompileRuleJSON([]byte(priority)); err != nil {
		t.Fatalf("int64 priority seed selection should compile: %v", err)
	}

	missingAttribute := strings.Replace(priority, `"field":"priority"`, `"field":"missing"`, 1)
	_, err := CompileRuleJSON([]byte(missingAttribute))
	expectRuleConfigError(t, err, "$.seedSelection.params.field", "ATTRIBUTE_NOT_FOUND")

	wrongType := strings.Replace(validRuleJSON(), `"seedSelection":{"type":"arrival","params":{}}`, `"seedSelection":{"type":"int64_priority","params":{"field":"tags","direction":"descending"}}`, 1)
	wrongType = strings.Replace(wrongType, `{"name":"priority","type":"int64"}`, `{"name":"tags","type":"strings","maxValues":1}`, 1)
	_, err = CompileRuleJSON([]byte(wrongType))
	expectRuleConfigError(t, err, "$.seedSelection.params.field", "ATTRIBUTE_TYPE_MISMATCH")

	unknownParam := strings.Replace(validRuleJSON(), `"seedSelection":{"type":"arrival","params":{}}`, `"seedSelection":{"type":"arrival","params":{"extra":true}}`, 1)
	_, err = CompileRuleJSON([]byte(unknownParam))
	expectRuleConfigError(t, err, "$.seedSelection.params.extra", "UNKNOWN_FIELD")

	unknownType := strings.Replace(validRuleJSON(), `"seedSelection":{"type":"arrival","params":{}}`, `"seedSelection":{"type":"future","params":{}}`, 1)
	_, err = CompileRuleJSON([]byte(unknownType))
	expectRuleConfigError(t, err, "$.seedSelection.type", "UNKNOWN_SEED_SELECTION_TYPE")
}

func TestCompileRuleJSONScoringFieldValidation(t *testing.T) {
	fieldScoring := strings.Replace(validRuleJSON(), `"scoring":{"type":"constant","params":{"value":1}}`, `"scoring":{"type":"int64_field","params":{"field":"priority","direction":"descending"}}`, 1)
	if _, err := CompileRuleJSON([]byte(fieldScoring)); err != nil {
		t.Fatalf("int64_field scoring should compile: %v", err)
	}

	missing := strings.Replace(fieldScoring, `"field":"priority"`, `"field":"missing"`, 1)
	_, err := CompileRuleJSON([]byte(missing))
	expectRuleConfigError(t, err, "$.scoring.params.field", "ATTRIBUTE_NOT_FOUND")

	wrongType := strings.Replace(fieldScoring, `{"name":"priority","type":"int64"}`, `{"name":"priority","type":"strings","maxValues":1}`, 1)
	_, err = CompileRuleJSON([]byte(wrongType))
	expectRuleConfigError(t, err, "$.scoring.params.field", "ATTRIBUTE_TYPE_MISMATCH")

	empty := strings.Replace(fieldScoring, `"field":"priority"`, `"field":""`, 1)
	_, err = CompileRuleJSON([]byte(empty))
	expectRuleConfigError(t, err, "$.scoring.params.field", "INVALID_VALUE")
}

func TestCompileRuleJSONRuntimeValidation(t *testing.T) {
	zero := strings.Replace(validRuleJSON(), `"candidateLimitPerSeed":128`, `"candidateLimitPerSeed":0`, 1)
	_, err := CompileRuleJSON([]byte(zero))
	expectRuleConfigError(t, err, "$.runtime.candidateLimitPerSeed", "INVALID_VALUE")

	badOrder := strings.Replace(validRuleJSON(), `"attemptLimitPerProduceMatch":10,"attemptLimitPerMatchRound":20`, `"attemptLimitPerProduceMatch":21,"attemptLimitPerMatchRound":20`, 1)
	_, err = CompileRuleJSON([]byte(badOrder))
	expectRuleConfigError(t, err, "$.runtime.attemptLimitPerProduceMatch", "INVALID_VALUE")
}

func expectRuleConfigError(t *testing.T, err error, path, code string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected %s at %s, got nil", code, path)
	}
	var configErr *RuleConfigError
	if !errors.As(err, &configErr) {
		t.Fatalf("error is not RuleConfigError: %T %v", err, err)
	}
	if configErr.Path != path || configErr.Code != code {
		t.Fatalf("RuleConfigError: got path=%q code=%q, want path=%q code=%q (%v)", configErr.Path, configErr.Code, path, code, err)
	}
}

func validRuleJSON() string {
	return `{"schemaVersion":"match-rule/v1","ruleKey":{"namespace":"demo","ruleId":1001},"contract":{"schemaVersion":"logical-node-contract/v3","attributes":[{"name":"priority","type":"int64"}],"facts":[],"indexes":[]},"prefilter":{"schemaVersion":"prefilter/v3","bitmap":{"resultType":"bitmap","expr":{"op":"none"}}},"evaluation":{"schemaVersion":"evaluation/v3","canJoin":{"schemaVersion":"expression-scalar/v3","resultType":"bool","expr":{"op":"bool_literal","value":true}},"canComplete":{"schemaVersion":"expression-scalar/v3","resultType":"bool","expr":{"op":"bool_literal","value":true}}},"scoring":{"type":"constant","params":{"value":1}},"seedSelection":{"type":"arrival","params":{}},"runtime":{"candidateLimitPerSeed":128,"maxPlayers":8,"attemptLimitPerProduceMatch":10,"attemptLimitPerMatchRound":20}}`
}

func reorderRuleJSON(t *testing.T, data string) string {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(data), &fields); err != nil {
		t.Fatalf("decode rule fixture: %v", err)
	}
	order := []string{"runtime", "seedSelection", "scoring", "evaluation", "prefilter", "contract", "ruleKey", "schemaVersion"}
	var builder strings.Builder
	builder.WriteByte('{')
	for index, name := range order {
		if index != 0 {
			builder.WriteByte(',')
		}
		builder.WriteString(strconvQuote(name))
		builder.WriteByte(':')
		builder.Write(fields[name])
	}
	builder.WriteByte('}')
	return builder.String()
}

func strconvQuote(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
