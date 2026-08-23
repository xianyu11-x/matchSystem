package prefilter

import (
	"strings"
	"testing"
)

func TestCompileRejectsInvalidPlans(t *testing.T) {
	index := NewMultiValueIndex(MultiValueIndexConfig{Name: "a", Field: "a", MaxDocumentValues: 4, MaxQueryValues: 4})
	uint64Index := NewMultiValueIndex(MultiValueIndexConfig{Name: "u", Field: "u", KeyType: KeyTypeUint64, MaxDocumentValues: 4, MaxQueryValues: 4})
	cases := []struct {
		name   string
		config Config
		code   string
	}{
		{"missing root", Config{}, "MISSING_ROOT"},
		{"empty and", Config{Root: And()}, "EMPTY_AND"},
		{"empty or", Config{Root: Or()}, "EMPTY_OR"},
		{"exclude without scope", Config{Indexes: []IndexSpec{index}, Root: Exclude(Lookup(StringQuery{Index: "a", Values: LiteralStrings("x")}))}, "EXCLUDE_REQUIRES_SCOPE"},
		{"exclude inside root or", Config{Indexes: []IndexSpec{index}, Root: Or(Lookup(StringQuery{Index: "a", Values: LiteralStrings("x")}), Exclude(Lookup(StringQuery{Index: "a", Values: LiteralStrings("y")})))}, "EXCLUDE_REQUIRES_SCOPE"},
		{"missing index", Config{Root: Lookup(StringQuery{Index: "missing", Values: LiteralStrings("x")})}, "MISSING_INDEX"},
		{"query mismatch", Config{Indexes: []IndexSpec{index}, Root: Lookup(Int64RangeQuery{Index: "a", Min: LiteralInt64(0), Max: LiteralInt64(1)})}, "QUERY_INDEX_MISMATCH"},
		{"empty union", Config{Indexes: []IndexSpec{index}, Root: Lookup(StringQuery{Index: "a", Values: UnionStrings()})}, "EMPTY_UNION"},
		{"fact exceeds query contract", Config{Indexes: []IndexSpec{index}, Facts: []FactSpec{{Name: "values", Type: FactTypeStrings, MaxValues: 8}}, Root: Lookup(StringQuery{Index: "a", Values: FactStrings("values")})}, "QUERY_KEY_CONTRACT"},
		{"string query on uint64 index", Config{Indexes: []IndexSpec{uint64Index}, Root: Lookup(StringQuery{Index: "u", Values: LiteralStrings("1")})}, "QUERY_KEY_TYPE_MISMATCH"},
		{"uint64 query on string index", Config{Indexes: []IndexSpec{index}, Root: Lookup(Uint64Query{Index: "a", Values: LiteralUint64s(1)})}, "QUERY_KEY_TYPE_MISMATCH"},
		{"uint64 fact type mismatch", Config{Indexes: []IndexSpec{uint64Index}, Facts: []FactSpec{{Name: "values", Type: FactTypeStrings, MaxValues: 4}}, Root: Lookup(Uint64Query{Index: "u", Values: FactUint64s("values")})}, "FACT_TYPE_MISMATCH"},
		{"uint64 fact exceeds query contract", Config{Indexes: []IndexSpec{uint64Index}, Facts: []FactSpec{{Name: "values", Type: FactTypeUint64s, MaxValues: 8}}, Root: Lookup(Uint64Query{Index: "u", Values: FactUint64s("values")})}, "QUERY_KEY_CONTRACT"},
		{"invalid key type", Config{Indexes: []IndexSpec{NewMultiValueIndex(MultiValueIndexConfig{Name: "bad", Field: "bad", KeyType: KeyType("bytes"), MaxDocumentValues: 1, MaxQueryValues: 1})}, Root: None()}, "INVALID_KEY_TYPE"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Compile(tc.config)
			if err == nil || !strings.Contains(err.Error(), tc.code) {
				t.Fatalf("expected %s, got %v", tc.code, err)
			}
		})
	}
}

func TestCompileDetectsCycle(t *testing.T) {
	cycle := &andExpr{}
	cycle.children = []Expr{cycle}
	_, err := Compile(Config{Root: cycle})
	if err == nil || !strings.Contains(err.Error(), "CYCLE") {
		t.Fatalf("expected cycle error, got %v", err)
	}
}

func TestFingerprintNormalizesCommutativeChildren(t *testing.T) {
	indexes := []IndexSpec{
		NewMultiValueIndex(MultiValueIndexConfig{Name: "a", Field: "a", MaxDocumentValues: 4, MaxQueryValues: 4}),
		NewMultiValueIndex(MultiValueIndexConfig{Name: "b", Field: "b", MaxDocumentValues: 4, MaxQueryValues: 4}),
	}
	a := Lookup(StringQuery{Index: "a", Values: LiteralStrings("x")})
	b := Lookup(StringQuery{Index: "b", Values: LiteralStrings("y")})
	left, err := Compile(Config{Indexes: indexes, Root: And(a, b)})
	if err != nil {
		t.Fatal(err)
	}
	right, err := Compile(Config{Indexes: indexes, Root: And(b, a)})
	if err != nil {
		t.Fatal(err)
	}
	if left.Fingerprint() != right.Fingerprint() {
		t.Fatalf("fingerprints differ: %s != %s", left.Fingerprint(), right.Fingerprint())
	}
	requirements := left.Requirements()
	if len(requirements.Indexes) != 2 || requirements.Indexes[0].Name != "a" || requirements.Indexes[1].Name != "b" {
		t.Fatalf("unexpected requirements: %#v", requirements)
	}
}

func TestRequirementsIncludesKeyType(t *testing.T) {
	plan, err := Compile(Config{
		Indexes: []IndexSpec{NewMultiValueIndex(MultiValueIndexConfig{Name: "u", Field: "u", KeyType: KeyTypeUint64, MaxDocumentValues: 4, MaxQueryValues: 4})},
		Root:    Lookup(Uint64Query{Index: "u", Values: LiteralUint64s(1)}),
	})
	if err != nil {
		t.Fatal(err)
	}
	requirements := plan.Requirements()
	if len(requirements.Indexes) != 1 || requirements.Indexes[0].KeyType != KeyTypeUint64 {
		t.Fatalf("uint64 key type missing from requirements: %#v", requirements)
	}
}

func TestDefaultAndExplicitStringKeyTypeHaveSameFingerprint(t *testing.T) {
	compile := func(keyType KeyType) *Plan {
		t.Helper()
		plan, err := Compile(Config{
			Indexes: []IndexSpec{NewMultiValueIndex(MultiValueIndexConfig{Name: "s", Field: "s", KeyType: keyType, MaxDocumentValues: 4, MaxQueryValues: 4})},
			Root:    Lookup(StringQuery{Index: "s", Values: LiteralStrings("value")}),
		})
		if err != nil {
			t.Fatal(err)
		}
		return plan
	}

	implicit := compile("")
	explicit := compile(KeyTypeString)
	if implicit.Fingerprint() != explicit.Fingerprint() {
		t.Fatalf("default and explicit string fingerprints differ: %s != %s", implicit.Fingerprint(), explicit.Fingerprint())
	}
	if got := implicit.Requirements().Indexes[0].KeyType; got != KeyTypeString {
		t.Fatalf("default key type was not normalized: %q", got)
	}
}
