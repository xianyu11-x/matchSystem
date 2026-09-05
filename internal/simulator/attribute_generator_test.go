package simulator

import (
	"matchSystem/internal/identity"
	"math"
	"math/rand"
	"reflect"
	"testing"
)

func generatorTestSpec() BatchGeneratorSpec {
	return BatchGeneratorSpec{Rule: identity.RuleKey{Namespace: "test", RuleID: 1}, Count: 10, Seed: 42}
}
func TestAttributeSamplingPrecisionAndReplay(t *testing.T) {
	s := generatorTestSpec()
	n := 3
	s.AttributeGenerators = map[string]AttributeGenerator{"u": {Type: "uint64s", Set: "18446744073709551613-18446744073709551615,18446744073709551615", Count: &n}, "i": {Type: "int64", Min: "-9223372036854775808", Max: "9223372036854775807"}}
	a, e := GenerateBatch(s)
	if e != nil {
		t.Fatal(e)
	}
	b, e := GenerateBatch(s)
	if e != nil || !reflect.DeepEqual(a, b) {
		t.Fatal("not replayable", e)
	}
	for _, v := range a {
		seen := map[uint64]bool{}
		for _, u := range v.Uint64Lists["u"] {
			if u < math.MaxUint64-2 || seen[u] {
				t.Fatal(v)
			}
			seen[u] = true
		}
	}
}
func TestAttributeSamplingCounts(t *testing.T) {
	for _, distribution := range []string{"uniform", "low", "high", "triangular"} {
		for _, n := range []int{0, 2, 3} {
			s := generatorTestSpec()
			s.AttributeGenerators = map[string]AttributeGenerator{"s": {Type: "strings", Values: []string{"a", "a", "b"}, Count: &n, Distribution: distribution}}
			a, e := GenerateBatch(s)
			if n == 3 {
				if e == nil {
					t.Fatal("accepted impossible sample")
				}
				continue
			}
			if e != nil || len(a[0].StringLists["s"]) != n {
				t.Fatal(n, e)
			}
		}
	}
	n := 8
	s := generatorTestSpec()
	s.AttributeGenerators = map[string]AttributeGenerator{"u": {Type: "uint64s", Set: "0-18446744073709551615", Count: &n}}
	if _, e := GenerateBatch(s); e != nil {
		t.Fatal(e)
	}
	s.AttributeGenerators["u"] = AttributeGenerator{Type: "uint64s", Set: "7", Count: &n, Replacement: true}
	a, e := GenerateBatch(s)
	if e != nil || len(a[0].Uint64Lists["u"]) != 8 {
		t.Fatal(e)
	}
}
func TestAttributeDistributionShape(t *testing.T) {
	means := map[string]float64{}
	for _, d := range []string{"uniform", "low", "high", "triangular"} {
		s := generatorTestSpec()
		s.Count = 10000
		s.AttributeGenerators = map[string]AttributeGenerator{"i": {Type: "int64", Min: "0", Max: "100", Distribution: d}}
		a, e := GenerateBatch(s)
		if e != nil {
			t.Fatal(e)
		}
		for _, v := range a {
			means[d] += float64(v.Int64Values["i"]) / 10000
		}
	}
	if means["low"] >= 40 || means["high"] <= 60 || means["uniform"] < 48 || means["uniform"] > 52 || means["triangular"] < 48 || means["triangular"] > 52 {
		t.Fatal(means)
	}
}
func TestAttributeInvalidConfig(t *testing.T) {
	for _, g := range []AttributeGenerator{{Type: "uint64s", Set: "2-1"}, {Type: "uint64s", Set: "18446744073709551616"}, {Type: "int64", Min: "0", Max: "9223372036854775808"}, {Type: "strings", Values: []string{"a"}, Distribution: "typo"}} {
		s := generatorTestSpec()
		s.AttributeGenerators = map[string]AttributeGenerator{"x": g}
		if _, e := GenerateBatch(s); e == nil {
			t.Fatal(g)
		}
	}
}
func TestWithoutReplacementUsesRemainingOrderedRanks(t *testing.T) {
	n := 4
	s := generatorTestSpec()
	s.AttributeGenerators = map[string]AttributeGenerator{"x": {Type: "uint64s", Set: "1-4", Count: &n, Distribution: "low"}}
	p, e := compileAttributeGenerators(s)
	if e != nil {
		t.Fatal(e)
	}
	input := TicketInput{Uint64Lists: map[string][]uint64{}}
	if e = p.apply(&input, rand.New(rand.NewSource(9))); e != nil {
		t.Fatal(e)
	}
	seen := map[uint64]bool{}
	for _, v := range input.Uint64Lists["x"] {
		seen[v] = true
	}
	if len(seen) != 4 {
		t.Fatal(input)
	}
}

func TestAttributeDerivedSources(t *testing.T) {
	s := generatorTestSpec()
	s.FirstTicketID = math.MaxUint64 - 9
	s.AttributeGenerators = map[string]AttributeGenerator{"z": {Type: "uint64s", Source: "ticketId"}, "a": {Type: "uint64s", Source: "shared", Ref: "b"}, "b": {Type: "uint64s", Source: "shared", Ref: "z"}, "text": {Type: "strings", Source: "ticketId"}, "level": {Type: "int64", Min: "1", Max: "100"}, "copy": {Type: "int64", Source: "shared", Ref: "level"}}
	a, e := GenerateBatch(s)
	if e != nil {
		t.Fatal(e)
	}
	b, e := GenerateBatch(s)
	if e != nil || !reflect.DeepEqual(a, b) {
		t.Fatal("derived replay differs", e)
	}
	for _, v := range a {
		if !reflect.DeepEqual(v.Uint64Lists["a"], v.Uint64Lists["z"]) || v.Int64Values["copy"] != v.Int64Values["level"] {
			t.Fatal(v)
		}
	}
	a[0].Uint64Lists["a"][0] = 0
	if a[0].Uint64Lists["z"][0] == 0 || a[0].Uint64Lists["b"][0] == 0 {
		t.Fatal("shared lists alias")
	}
	if a[9].StringLists["text"][0] != "18446744073709551615" {
		t.Fatal(a[9])
	}
}
func TestAttributeDerivedRejectsInvalidReferences(t *testing.T) {
	n := 1
	for _, g := range []map[string]AttributeGenerator{
		{"a": {Type: "strings", Source: "shared", Ref: "a"}},
		{"a": {Type: "strings", Source: "shared", Ref: "b"}, "b": {Type: "strings", Source: "shared", Ref: "a"}},
		{"a": {Type: "strings", Source: "shared", Ref: "missing"}},
		{"a": {Type: "strings", Source: "shared", Ref: "b"}, "b": {Type: "uint64s", Set: "1"}},
		{"a": {Type: "int64", Source: "ticketId", Count: &n}},
		{"a": {Type: "int64", Source: "ticketId", Distribution: "uniform"}},
	} {
		s := generatorTestSpec()
		s.AttributeGenerators = g
		if _, e := GenerateBatch(s); e == nil {
			t.Fatal(g)
		}
	}
	s := generatorTestSpec()
	s.FirstTicketID = math.MaxInt64
	s.AttributeGenerators = map[string]AttributeGenerator{"a": {Type: "int64", Source: "ticketId"}}
	if _, e := GenerateBatch(s); e == nil {
		t.Fatal("int64 ticketId overflow accepted")
	}
}
