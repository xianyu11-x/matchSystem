package prefilter

import (
	"errors"
	"strings"
	"testing"

	"matchSystem/internal/common"
)

func TestJSONCompilerMatchesTypedConfigAndExecutes(t *testing.T) {
	indexes := []IndexSpec{
		NewMultiValueIndex(MultiValueIndexConfig{Name: "mode", Field: "mode", MaxDocumentValues: 2, MaxQueryValues: 4}),
		NewMultiValueIndex(MultiValueIndexConfig{Name: "bucket", Field: "bucket", KeyType: KeyTypeUint64, MaxDocumentValues: 2, MaxQueryValues: 2}),
		NewInt64RangeIndex(Int64RangeIndexConfig{Name: "rating", Field: "rating"}),
	}
	facts := []FactSpec{
		{Name: "mode_keys", Type: FactTypeStrings, MaxValues: 2},
		{Name: "bucket_keys", Type: FactTypeUint64s, MaxValues: 2},
		{Name: "wait_millis", Type: FactTypeInt64},
	}
	compiler, err := NewJSONCompiler(JSONContract{Indexes: indexes, Facts: facts})
	if err != nil {
		t.Fatalf("NewJSONCompiler: %v", err)
	}

	data := []byte(`{
  "schemaVersion": "prefilter/v1",
  "plan": {
    "type": "and",
    "children": [
      {
        "type": "lookup",
        "query": {
          "type": "multi_value",
          "index": "mode",
          "values": {
            "type": "union_strings",
            "items": [
              {"type": "seed_strings", "field": "mode"},
              {"type": "fact_strings", "fact": "mode_keys"}
            ]
          }
        }
      },
      {
        "type": "lookup",
        "query": {
          "type": "multi_value",
          "index": "bucket",
          "values": {"type": "fact_uint64s", "fact": "bucket_keys"}
        }
      },
      {
        "type": "lookup",
        "query": {
          "type": "int64_range",
          "index": "rating",
          "min": {
            "type": "sub_int64",
            "left": {"type": "seed_int64", "field": "rating"},
            "right": {
              "type": "step_int64",
              "input": {"type": "fact_int64", "fact": "wait_millis"},
              "steps": [{"at": 0, "value": 10}, {"at": 30000, "value": 50}]
            }
          },
          "max": {
            "type": "add_int64",
            "left": {"type": "seed_int64", "field": "rating"},
            "right": {
              "type": "step_int64",
              "input": {"type": "fact_int64", "fact": "wait_millis"},
              "steps": [{"at": 0, "value": 10}, {"at": 30000, "value": 50}]
            }
          },
          "includeMin": true,
          "includeMax": true
        }
      }
    ]
  },
  "runtime": {"containsProbeThreshold": 17}
}`)

	jsonConfig, err := compiler.Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(jsonConfig.Indexes) != len(indexes) || len(jsonConfig.Facts) != len(facts) {
		t.Fatalf("contract not carried into Config: indexes=%d facts=%d", len(jsonConfig.Indexes), len(jsonConfig.Facts))
	}
	jsonPlan, err := compiler.Compile(data)
	if err != nil {
		t.Fatalf("Compile JSON: %v", err)
	}

	radius := StepInt64(FactInt64("wait_millis"), Int64Step{At: 0, Value: 10}, Int64Step{At: 30000, Value: 50})
	typedPlan, err := Compile(Config{
		Indexes: indexes,
		Facts:   facts,
		Root: And(
			Lookup(StringQuery{Index: "mode", Values: UnionStrings(SeedStrings("mode"), FactStrings("mode_keys"))}),
			Lookup(Uint64Query{Index: "bucket", Values: FactUint64s("bucket_keys")}),
			Lookup(Int64RangeQuery{Index: "rating", Min: SubInt64(SeedInt64("rating"), radius), Max: AddInt64(SeedInt64("rating"), radius)}),
		),
		ContainsProbeThreshold: 17,
	})
	if err != nil {
		t.Fatalf("Compile typed: %v", err)
	}
	if jsonPlan.Fingerprint() != typedPlan.Fingerprint() {
		t.Fatalf("fingerprint mismatch: JSON=%s typed=%s", jsonPlan.Fingerprint(), typedPlan.Fingerprint())
	}

	store, err := New(jsonPlan)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	documents := []indexedTestTicket{
		indexedTicket(1, &common.Ticket{StringLists: map[string][]string{"mode": {"ranked"}}, Uint64Lists: map[string][]uint64{"bucket": {7}}, Int64Values: map[string]int64{"rating": 100}}),
		indexedTicket(2, &common.Ticket{StringLists: map[string][]string{"mode": {"ranked"}}, Uint64Lists: map[string][]uint64{"bucket": {7}}, Int64Values: map[string]int64{"rating": 140}}),
		indexedTicket(3, &common.Ticket{StringLists: map[string][]string{"mode": {"casual"}}, Uint64Lists: map[string][]uint64{"bucket": {9}}, Int64Values: map[string]int64{"rating": 105}}),
	}
	for _, document := range documents {
		if err := store.Add(document.docID, document.Ticket); err != nil {
			t.Fatalf("Add(%d): %v", document.docID, err)
		}
	}
	session, err := store.BeginTick(Facts{StringLists: map[string][]string{"mode_keys": {"ranked"}}})
	if err != nil {
		t.Fatalf("BeginTick: %v", err)
	}
	candidates, err := session.Candidates(documents[0].docID, documents[0].Ticket, Facts{
		Uint64Lists: map[string][]uint64{"bucket_keys": {7}},
		Int64Values: map[string]int64{"wait_millis": 30000},
	})
	if err != nil {
		t.Fatalf("Candidates: %v", err)
	}
	if got := candidates.IDs(); len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("Candidates=%v, want [1 2]", got)
	}
}

func TestJSONCompilerValidatesFixedIndexAndFactContract(t *testing.T) {
	compiler := mustJSONCompiler(t, JSONContract{
		Indexes: []IndexSpec{
			NewMultiValueIndex(MultiValueIndexConfig{Name: "strings", Field: "strings", KeyType: KeyTypeString}),
			NewMultiValueIndex(MultiValueIndexConfig{Name: "uints", Field: "uints", KeyType: KeyTypeUint64}),
			NewInt64RangeIndex(Int64RangeIndexConfig{Name: "rating", Field: "rating"}),
		},
		Facts: []FactSpec{
			{Name: "string_fact", Type: FactTypeStrings, MaxValues: 2},
			{Name: "uint_fact", Type: FactTypeUint64s, MaxValues: 2},
			{Name: "int_fact", Type: FactTypeInt64},
		},
	})

	tests := []struct {
		name string
		plan string
		code string
		path string
	}{
		{
			name: "index outside contract",
			plan: `{"type":"lookup","query":{"type":"multi_value","index":"missing","values":{"type":"literal_strings","values":["x"]}}}`,
			code: "UNAVAILABLE_INDEX", path: "$.plan.query.index",
		},
		{
			name: "wrong index kind",
			plan: `{"type":"lookup","query":{"type":"int64_range","index":"strings","min":{"type":"literal_int64","value":0},"max":{"type":"literal_int64","value":1}}}`,
			code: "QUERY_INDEX_MISMATCH", path: "$.plan.query.index",
		},
		{
			name: "value type disagrees with index key type",
			plan: `{"type":"lookup","query":{"type":"multi_value","index":"strings","values":{"type":"seed_uint64s","field":"keys"}}}`,
			code: "EXPRESSION_TYPE_MISMATCH", path: "$.plan.query.values.type",
		},
		{
			name: "fact outside contract",
			plan: `{"type":"lookup","query":{"type":"multi_value","index":"strings","values":{"type":"fact_strings","fact":"missing"}}}`,
			code: "UNAVAILABLE_FACT", path: "$.plan.query.values.fact",
		},
		{
			name: "fact type mismatch",
			plan: `{"type":"lookup","query":{"type":"multi_value","index":"strings","values":{"type":"fact_strings","fact":"int_fact"}}}`,
			code: "FACT_TYPE_MISMATCH", path: "$.plan.query.values.fact",
		},
		{
			name: "JSON cannot redeclare indexes",
			plan: `{"type":"none"}`,
			code: "UNKNOWN_FIELD", path: "$.indexes",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			prefix := `{"schemaVersion":"prefilter/v1",`
			if tc.name == "JSON cannot redeclare indexes" {
				prefix += `"indexes":[],`
			}
			_, err := compiler.Compile([]byte(prefix + `"plan":` + tc.plan + `}`))
			requirePrefilterError(t, err, "json", tc.code, tc.path)
		})
	}
}

func TestJSONCompilerSupportsClosedV1ExpressionSet(t *testing.T) {
	compiler := mustJSONCompiler(t, JSONContract{
		Indexes: []IndexSpec{
			NewMultiValueIndex(MultiValueIndexConfig{Name: "strings", Field: "strings", MaxQueryValues: 4}),
			NewMultiValueIndex(MultiValueIndexConfig{Name: "uints", Field: "uints", KeyType: KeyTypeUint64, MaxQueryValues: 8}),
			NewInt64RangeIndex(Int64RangeIndexConfig{Name: "rating", Field: "rating"}),
		},
		Facts: []FactSpec{
			{Name: "uint_fact", Type: FactTypeUint64s, MaxValues: 2},
			{Name: "int_fact", Type: FactTypeInt64},
		},
	})
	data := []byte(`{
  "schemaVersion":"prefilter/v1",
  "plan":{
    "type":"and",
    "children":[
      {"type":"lookup","query":{"type":"multi_value","index":"strings","values":{"type":"literal_strings","values":["ranked"]}}},
      {"type":"lookup","query":{"type":"multi_value","index":"uints","values":{"type":"union_uint64s","items":[
        {"type":"seed_uint64s","field":"uints"},
        {"type":"fact_uint64s","fact":"uint_fact"},
        {"type":"literal_uint64s","values":[7,9]}
      ]}}},
      {"type":"if","when":{"type":"gte_int64","left":{"type":"fact_int64","fact":"int_fact"},"right":{"type":"literal_int64","value":0}},
	   "then":{"type":"or","children":[
	     {"type":"lookup","query":{"type":"int64_range","index":"rating",
	       "min":{"type":"clamp_int64","value":{"type":"seed_int64","field":"rating"},"min":{"type":"literal_int64","value":0},"max":{"type":"literal_int64","value":1000}},
	       "max":{"type":"literal_int64","value":1000}}},
	     {"type":"none"}
	   ]},
       "else":{"type":"none"}},
      {"type":"exclude","child":{"type":"lookup","query":{"type":"multi_value","index":"strings","values":{"type":"seed_strings","field":"blocked"}}}}
    ]
  }
}`)
	plan, err := compiler.Compile(data)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	requirements := plan.Requirements()
	if len(requirements.Indexes) != 3 || len(requirements.Facts) != 2 {
		t.Fatalf("Requirements=%+v", requirements)
	}
}

func TestJSONCompilerStrictDecoding(t *testing.T) {
	compiler := mustJSONCompiler(t, JSONContract{})
	tests := []struct {
		name string
		data string
		code string
	}{
		{"unknown schema", `{"schemaVersion":"prefilter/v2","plan":{"type":"none"}}`, "UNKNOWN_SCHEMA_VERSION"},
		{"missing schema", `{"plan":{"type":"none"}}`, "MISSING_FIELD"},
		{"missing plan", `{"schemaVersion":"prefilter/v1"}`, "MISSING_FIELD"},
		{"duplicate key", `{"schemaVersion":"prefilter/v1","plan":{"type":"none","type":"none"}}`, "DUPLICATE_KEY"},
		{"unknown field", `{"schemaVersion":"prefilter/v1","plan":{"type":"none","extra":1}}`, "UNKNOWN_FIELD"},
		{"null", `{"schemaVersion":"prefilter/v1","plan":null}`, "NULL_NOT_ALLOWED"},
		{"trailing JSON", `{"schemaVersion":"prefilter/v1","plan":{"type":"none"}} {}`, "TRAILING_JSON"},
		{"non-object root", `[]`, "INVALID_ROOT"},
		{"unknown node", `{"schemaVersion":"prefilter/v1","plan":{"type":"scan"}}`, "UNKNOWN_TYPE"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := compiler.Compile([]byte(tc.data))
			requirePrefilterError(t, err, "json", tc.code, "")
		})
	}
}

func TestJSONCompilerLimitsAndNumericValidation(t *testing.T) {
	compiler := mustJSONCompiler(t, JSONContract{
		Indexes: []IndexSpec{NewInt64RangeIndex(Int64RangeIndexConfig{Name: "rating", Field: "rating"})},
		Limits:  JSONLimits{MaxBytes: 512, MaxSteps: 1},
	})

	tooLarge := []byte(strings.Repeat(" ", 513))
	_, err := compiler.Parse(tooLarge)
	requirePrefilterError(t, err, "json", "JSON_SIZE_LIMIT", "$")

	overflow := []byte(`{"schemaVersion":"prefilter/v1","plan":{"type":"lookup","query":{"type":"int64_range","index":"rating","min":{"type":"literal_int64","value":9223372036854775808},"max":{"type":"literal_int64","value":1}}}}`)
	_, err = compiler.Parse(overflow)
	requirePrefilterError(t, err, "json", "INVALID_STRUCTURE", "$.plan.query.min")

	tooManySteps := []byte(`{"schemaVersion":"prefilter/v1","plan":{"type":"lookup","query":{"type":"int64_range","index":"rating","min":{"type":"step_int64","input":{"type":"literal_int64","value":0},"steps":[{"at":0,"value":1},{"at":1,"value":2}]},"max":{"type":"literal_int64","value":3}}}}`)
	_, err = compiler.Parse(tooManySteps)
	requirePrefilterError(t, err, "json", "STEP_LIMIT", "$.plan.query.min.steps")

	missingStepField := []byte(`{"schemaVersion":"prefilter/v1","plan":{"type":"lookup","query":{"type":"int64_range","index":"rating","min":{"type":"step_int64","input":{"type":"literal_int64","value":0},"steps":[{"at":0}]},"max":{"type":"literal_int64","value":3}}}}`)
	_, err = compiler.Parse(missingStepField)
	requirePrefilterError(t, err, "json", "MISSING_FIELD", "$.plan.query.min.steps[0]")
}

func TestNewJSONCompilerValidatesContractAndSnapshotsSlices(t *testing.T) {
	_, err := NewJSONCompiler(JSONContract{Facts: []FactSpec{{Name: "bad", Type: FactTypeStrings}}})
	requirePrefilterError(t, err, "compile", "INVALID_FACT_LIMIT", "facts[0]")
	_, err = NewJSONCompiler(JSONContract{Limits: JSONLimits{MaxDepth: -1}})
	requirePrefilterError(t, err, "compile", "INVALID_JSON_LIMIT", "jsonContract.limits.maxDepth")

	indexes := []IndexSpec{NewInt64RangeIndex(Int64RangeIndexConfig{Name: "rating", Field: "rating"})}
	facts := []FactSpec{{Name: "wait", Type: FactTypeInt64}}
	compiler := mustJSONCompiler(t, JSONContract{Indexes: indexes, Facts: facts})
	indexes[0] = NewInt64RangeIndex(Int64RangeIndexConfig{Name: "replacement", Field: "replacement"})
	facts[0] = FactSpec{Name: "replacement", Type: FactTypeInt64}

	data := []byte(`{"schemaVersion":"prefilter/v1","plan":{"type":"lookup","query":{"type":"int64_range","index":"rating","min":{"type":"fact_int64","fact":"wait"},"max":{"type":"fact_int64","fact":"wait"}}}}`)
	if _, err := compiler.Compile(data); err != nil {
		t.Fatalf("compiler contract changed through caller slices: %v", err)
	}
}

func mustJSONCompiler(t *testing.T, contract JSONContract) *JSONCompiler {
	t.Helper()
	compiler, err := NewJSONCompiler(contract)
	if err != nil {
		t.Fatalf("NewJSONCompiler: %v", err)
	}
	return compiler
}

func requirePrefilterError(t *testing.T, err error, phase, code, path string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected %s/%s error", phase, code)
	}
	var target *Error
	if !errors.As(err, &target) {
		t.Fatalf("error %T is not *prefilter.Error: %v", err, err)
	}
	if target.Phase != phase || target.Code != code || path != "" && target.Path != path {
		t.Fatalf("error=(phase=%q code=%q path=%q), want (%q %q %q): %v", target.Phase, target.Code, target.Path, phase, code, path, err)
	}
}
