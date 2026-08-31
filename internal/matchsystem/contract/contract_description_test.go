package contract

import (
	"strings"
	"testing"

	"matchSystem/internal/matchsystem/fact"
)

func TestParseFactDescription(t *testing.T) {
	parsed, err := Parse([]byte(`{
		"schemaVersion":"logical-node-contract/v3",
		"attributes":[],
		"facts":[
			{"name":"party-size","type":"int64","scope":"match","description":"number of players in the current match"},
			{"name":"regions","type":"strings","scope":"tick","maxValues":2,"description":"regions allowed in the current round"}
		],
		"indexes":[]
	}`), DefaultLimits())
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := parsed.Facts[0].Description; got != "number of players in the current match" {
		t.Fatalf("int64 Fact description=%q", got)
	}
	if got := parsed.Facts[1].Description; got != "regions allowed in the current round" {
		t.Fatalf("multi-value Fact description=%q", got)
	}
}

func TestParseFactDescriptionRejectsNullAndOversizedValues(t *testing.T) {
	tests := []struct {
		name string
		json string
	}{
		{
			name: "null",
			json: `{"name":"count","type":"int64","scope":"match","description":null}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			limits := DefaultLimits()
			data := []byte(`{"schemaVersion":"logical-node-contract/v3","attributes":[],"facts":[` + test.json + `],"indexes":[]}`)
			if _, err := Parse(data, limits); err == nil {
				t.Fatal("Parse accepted invalid Fact description")
			}
		})
	}

	limits := DefaultLimits()
	limits.MaxStringBytes = 1024
	data := []byte(`{"schemaVersion":"logical-node-contract/v3","attributes":[],"facts":[{"name":"count","type":"int64","scope":"match","description":"` + strings.Repeat("x", limits.MaxStringBytes+1) + `"}],"indexes":[]}`)
	if _, err := Parse(data, limits); err == nil {
		t.Fatal("Parse accepted an oversized Fact description")
	}
}

func TestValidateFactDescriptionRejectsInvalidValuesFromGoCallers(t *testing.T) {
	invalidUTF8 := string([]byte{0xff})
	for _, description := range []string{invalidUTF8, strings.Repeat("x", DefaultLimits().MaxStringBytes+1)} {
		contract := Contract{Facts: []fact.Spec{{Name: "count", Type: fact.TypeInt64, Scope: fact.ScopeMatch, Description: description}}}
		if err := contract.Validate(); err == nil {
			t.Fatalf("Contract.Validate accepted invalid description %q", description)
		}
	}
}
