package prefilter

import "testing"

func TestParseLogicalNodeContractThenCompileSeparatePlan(t *testing.T) {
	contractData := []byte(`{
  "schemaVersion": "logical-node-contract/v1",
  "indexes": [
    {"type":"multi_value","name":"mode","field":"mode","keyType":"string","maxDocumentValues":2,"maxQueryValues":4},
    {"type":"multi_value","name":"bucket","field":"bucket","keyType":"uint64","maxDocumentValues":2,"maxQueryValues":4},
    {"type":"int64_range","name":"rating","field":"rating"}
  ],
  "facts": [
    {"name":"mode_keys","type":"strings","maxValues":2},
    {"name":"bucket_keys","type":"uint64s","maxValues":2},
    {"name":"wait_millis","type":"int64"}
  ]
}`)
	contract, err := ParseLogicalNodeContract(contractData, JSONLimits{})
	if err != nil {
		t.Fatalf("ParseLogicalNodeContract: %v", err)
	}
	if len(contract.Indexes) != 3 || len(contract.Facts) != 3 {
		t.Fatalf("contract indexes=%d facts=%d", len(contract.Indexes), len(contract.Facts))
	}

	compiler, err := NewJSONCompiler(contract)
	if err != nil {
		t.Fatalf("NewJSONCompiler: %v", err)
	}
	planData := []byte(`{
  "schemaVersion":"prefilter/v1",
  "plan":{"type":"and","children":[
    {"type":"lookup","query":{"type":"multi_value","index":"mode","values":{"type":"fact_strings","fact":"mode_keys"}}},
    {"type":"lookup","query":{"type":"multi_value","index":"bucket","values":{"type":"fact_uint64s","fact":"bucket_keys"}}},
    {"type":"lookup","query":{"type":"int64_range","index":"rating",
      "min":{"type":"sub_int64","left":{"type":"seed_int64","field":"rating"},"right":{"type":"fact_int64","fact":"wait_millis"}},
      "max":{"type":"add_int64","left":{"type":"seed_int64","field":"rating"},"right":{"type":"fact_int64","fact":"wait_millis"}}}}
  ]}
}`)
	plan, err := compiler.Compile(planData)
	if err != nil {
		t.Fatalf("Compile plan JSON: %v", err)
	}
	requirements := plan.Requirements()
	if len(requirements.Indexes) != 3 || len(requirements.Facts) != 3 {
		t.Fatalf("Requirements=%+v", requirements)
	}
}

func TestContractAndPlanJSONCannotBeMixed(t *testing.T) {
	_, err := ParseLogicalNodeContract([]byte(`{
  "schemaVersion":"logical-node-contract/v1",
  "indexes":[],
  "facts":[],
  "plan":{"type":"none"}
}`), JSONLimits{})
	requirePrefilterError(t, err, "json", "UNKNOWN_FIELD", "$.plan")

	compiler := mustJSONCompiler(t, JSONContract{})
	_, err = compiler.Compile([]byte(`{
  "schemaVersion":"prefilter/v1",
  "indexes":[],
  "facts":[],
  "plan":{"type":"none"}
}`))
	requirePrefilterError(t, err, "json", "UNKNOWN_FIELD", "$.indexes")
}

func TestParseLogicalNodeContractRejectsInvalidIndexAndFactDeclarations(t *testing.T) {
	tests := []struct {
		name string
		body string
		code string
		path string
	}{
		{
			name: "missing indexes",
			body: `"facts":[]`,
			code: "MISSING_FIELD", path: "$.indexes",
		},
		{
			name: "missing facts",
			body: `"indexes":[]`,
			code: "MISSING_FIELD", path: "$.facts",
		},
		{
			name: "unknown index type",
			body: `"indexes":[{"type":"geo","name":"x","field":"x"}],"facts":[]`,
			code: "UNKNOWN_TYPE", path: "$.indexes[0].type",
		},
		{
			name: "missing key type",
			body: `"indexes":[{"type":"multi_value","name":"x","field":"x","maxDocumentValues":1,"maxQueryValues":1}],"facts":[]`,
			code: "INVALID_KEY_TYPE", path: "$.indexes[0].keyType",
		},
		{
			name: "invalid index limit",
			body: `"indexes":[{"type":"multi_value","name":"x","field":"x","keyType":"string","maxDocumentValues":0,"maxQueryValues":1}],"facts":[]`,
			code: "INVALID_KEY_LIMIT", path: "$.indexes[0].maxDocumentValues",
		},
		{
			name: "duplicate index",
			body: `"indexes":[{"type":"int64_range","name":"x","field":"a"},{"type":"int64_range","name":"x","field":"b"}],"facts":[]`,
			code: "DUPLICATE_INDEX", path: "$.indexes[1].name",
		},
		{
			name: "range rejects multi value fields",
			body: `"indexes":[{"type":"int64_range","name":"x","field":"x","keyType":"string"}],"facts":[]`,
			code: "UNKNOWN_FIELD", path: "$.indexes[0].keyType",
		},
		{
			name: "unknown fact type",
			body: `"indexes":[],"facts":[{"name":"x","type":"float64"}]`,
			code: "UNKNOWN_TYPE", path: "$.facts[0].type",
		},
		{
			name: "list fact missing limit",
			body: `"indexes":[],"facts":[{"name":"x","type":"strings"}]`,
			code: "INVALID_FACT_LIMIT", path: "$.facts[0].maxValues",
		},
		{
			name: "scalar fact rejects limit",
			body: `"indexes":[],"facts":[{"name":"x","type":"int64","maxValues":1}]`,
			code: "INVALID_FACT_LIMIT", path: "$.facts[0].maxValues",
		},
		{
			name: "duplicate fact",
			body: `"indexes":[],"facts":[{"name":"x","type":"int64"},{"name":"x","type":"int64"}]`,
			code: "DUPLICATE_FACT", path: "$.facts[1].name",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			data := []byte(`{"schemaVersion":"logical-node-contract/v1",` + tc.body + `}`)
			_, err := ParseLogicalNodeContract(data, JSONLimits{})
			requirePrefilterError(t, err, "json", tc.code, tc.path)
		})
	}
}

func TestParseLogicalNodeContractAppliesStrictDecodingAndLimits(t *testing.T) {
	_, err := ParseLogicalNodeContract([]byte(`{"schemaVersion":"logical-node-contract/v2","indexes":[],"facts":[]}`), JSONLimits{})
	requirePrefilterError(t, err, "json", "UNKNOWN_SCHEMA_VERSION", "$.schemaVersion")

	_, err = ParseLogicalNodeContract([]byte(`{
  "schemaVersion":"logical-node-contract/v1",
  "indexes":[],
  "indexes":[],
  "facts":[]
}`), JSONLimits{})
	requirePrefilterError(t, err, "json", "DUPLICATE_KEY", "$.indexes")

	_, err = ParseLogicalNodeContract([]byte(`{
  "schemaVersion":"logical-node-contract/v1",
  "indexes":[
    {"type":"int64_range","name":"a","field":"a"},
    {"type":"int64_range","name":"b","field":"b"}
  ],
  "facts":[]
}`), JSONLimits{MaxIndexes: 1})
	requirePrefilterError(t, err, "json", "INDEX_LIMIT", "$.indexes")

	_, err = ParseLogicalNodeContract([]byte(`{
  "schemaVersion":"logical-node-contract/v1",
  "indexes":[],
  "facts":[{"name":"a","type":"int64"},{"name":"b","type":"int64"}]
}`), JSONLimits{MaxFacts: 1})
	requirePrefilterError(t, err, "json", "FACT_LIMIT", "$.facts")
}
