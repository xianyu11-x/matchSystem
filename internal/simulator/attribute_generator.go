package simulator

import (
	"fmt"
	"math"
	"math/big"
	"math/rand"
	"sort"
	"strconv"
	"strings"
)

// AttributeGenerator uses decimal strings for all 64-bit bounds and set members.
type AttributeGenerator struct {
	Type         string   `json:"type"`
	Source       string   `json:"source,omitempty"`
	Ref          string   `json:"ref,omitempty"`
	Values       []string `json:"values,omitempty"`
	Set          string   `json:"set,omitempty"`
	Min          string   `json:"min,omitempty"`
	Max          string   `json:"max,omitempty"`
	Count        *int     `json:"count,omitempty"`
	Replacement  bool     `json:"replacement,omitempty"`
	Distribution string   `json:"distribution,omitempty"`
	Mean         *float64 `json:"mean,omitempty"`
	StdDev       *float64 `json:"stdDev,omitempty"`
}
type integerInterval struct{ lo, hi *big.Int }
type compiledAttribute struct {
	name      string
	spec      AttributeGenerator
	intervals []integerInterval
	size      *big.Int
	count     int
}
type attributePlan []compiledAttribute

var one = big.NewInt(1)

func compileAttributeGenerators(spec BatchGeneratorSpec) (attributePlan, error) {
	keys := make([]string, 0, len(spec.AttributeGenerators))
	for k := range spec.AttributeGenerators {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	plan := attributePlan{}
	state := map[string]int{}
	var visit func(string) error
	visit = func(name string) error {
		fail := func(msg string) error {
			return fmt.Errorf("%w: attributeGenerators[%q]: %s", ErrInvalidBatchSpec, name, msg)
		}
		if state[name] == 2 {
			return nil
		}
		if state[name] == 1 {
			return fail("shared source cycle")
		}
		state[name] = 1
		g, ok := spec.AttributeGenerators[name]
		if !ok || name == "" {
			return fail("missing attribute")
		}
		if g.Type != "strings" && g.Type != "uint64s" && g.Type != "int64" {
			return fail("invalid type")
		}
		if _, ok := spec.StringChoices[name]; ok {
			return fail("conflicts with stringChoices")
		}
		if _, ok := spec.Uint64Choices[name]; ok {
			return fail("conflicts with uint64Choices")
		}
		if _, ok := spec.Int64Ranges[name]; ok {
			return fail("conflicts with int64Ranges")
		}
		c := compiledAttribute{name: name, spec: g, count: 1, size: new(big.Int)}
		if g.Count != nil {
			c.count = *g.Count
		}
		if c.count < 0 || c.count > 4096 {
			return fail("count must be 0..4096")
		}
		if g.Distribution != "" && g.Distribution != "uniform" && g.Distribution != "low" && g.Distribution != "high" && g.Distribution != "triangular" && g.Distribution != "normal" {
			return fail("invalid distribution")
		}
		if g.Distribution == "normal" {
			lo, e1 := strconv.ParseInt(g.Min, 10, 64)
			hi, e2 := strconv.ParseInt(g.Max, 10, 64)
			if g.Type != "int64" || (g.Source != "" && g.Source != "sample") || e1 != nil || e2 != nil || lo > hi || lo < -9007199254740991 || hi > 9007199254740991 {
				return fail("normal requires sampled int64 bounds within the safe integer range")
			}
			if g.Mean == nil || g.StdDev == nil || math.IsNaN(*g.Mean) || math.IsInf(*g.Mean, 0) || math.IsNaN(*g.StdDev) || math.IsInf(*g.StdDev, 0) || *g.Mean < float64(lo) || *g.Mean > float64(hi) || *g.StdDev <= 0 {
				return fail("normal requires finite mean within min/max and finite stdDev > 0")
			}
		} else if g.Mean != nil || g.StdDev != nil {
			return fail("mean/stdDev are only supported for normal distribution")
		}
		if g.Source == "shared" || g.Source == "ticketId" {
			if g.Count != nil || g.Replacement || g.Distribution != "" || len(g.Values) > 0 || g.Set != "" || g.Min != "" || g.Max != "" {
				return fail("derived sources cannot have sampling options")
			}
			if g.Source == "shared" {
				if err := visit(g.Ref); err != nil {
					return err
				}
				if spec.AttributeGenerators[g.Ref].Type != g.Type {
					return fail("shared source type mismatch")
				}
			} else {
				if g.Ref != "" {
					return fail("ticketId cannot have ref")
				}
				first := spec.FirstTicketID
				if first == 0 {
					first = 1
				}
				last, err := addTicketID(first, spec.Count-1)
				if err != nil {
					return fail(err.Error())
				}
				if g.Type == "int64" && last > math.MaxInt64 {
					return fail("ticketId exceeds int64")
				}
			}
		} else {
			if g.Source != "" && g.Source != "sample" {
				return fail("invalid source")
			}
			if g.Ref != "" {
				return fail("sample cannot have ref")
			}
			switch g.Type {
			case "strings":
				if g.Set != "" || g.Min != "" || g.Max != "" {
					return fail("strings requires values")
				}
				seen := map[string]bool{}
				values := []string{}
				for _, v := range g.Values {
					if !seen[v] {
						seen[v] = true
						values = append(values, v)
					}
				}
				c.spec.Values = values
				c.size.SetInt64(int64(len(values)))
			case "int64":
				if len(g.Values) > 0 || g.Set != "" || g.Count != nil || g.Replacement {
					return fail("int64 requires min/max only")
				}
				lo, e := strconv.ParseInt(g.Min, 10, 64)
				if e != nil {
					return fail("invalid min")
				}
				hi, e := strconv.ParseInt(g.Max, 10, 64)
				if e != nil || hi < lo {
					return fail("invalid max")
				}
				c.intervals = []integerInterval{{big.NewInt(lo), big.NewInt(hi)}}
			case "uint64s":
				if len(g.Values) > 0 || g.Min != "" || g.Max != "" {
					return fail("uint64s requires set")
				}
				for _, part := range strings.Split(g.Set, ",") {
					ends := strings.Split(strings.TrimSpace(part), "-")
					if len(ends) > 2 {
						return fail("invalid set interval")
					}
					lo, e := strconv.ParseUint(strings.TrimSpace(ends[0]), 10, 64)
					if e != nil {
						return fail("invalid set value")
					}
					hi := lo
					if len(ends) == 2 {
						hi, e = strconv.ParseUint(strings.TrimSpace(ends[1]), 10, 64)
					}
					if e != nil || hi < lo {
						return fail("invalid set interval")
					}
					c.intervals = append(c.intervals, integerInterval{new(big.Int).SetUint64(lo), new(big.Int).SetUint64(hi)})
				}
				sort.Slice(c.intervals, func(i, j int) bool { return c.intervals[i].lo.Cmp(c.intervals[j].lo) < 0 })
				merged := []integerInterval{}
				for _, v := range c.intervals {
					if len(merged) > 0 && v.lo.Cmp(new(big.Int).Add(merged[len(merged)-1].hi, one)) <= 0 {
						if v.hi.Cmp(merged[len(merged)-1].hi) > 0 {
							merged[len(merged)-1].hi = v.hi
						}
					} else {
						merged = append(merged, v)
					}
				}
				c.intervals = merged
			}
			for _, v := range c.intervals {
				c.size.Add(c.size, new(big.Int).Add(new(big.Int).Sub(v.hi, v.lo), one))
			}
			if c.size.Sign() == 0 {
				return fail("empty set")
			}
			if !g.Replacement && c.size.Cmp(big.NewInt(int64(c.count))) < 0 {
				return fail("count exceeds distinct set size")
			}
		}
		state[name] = 2
		plan = append(plan, c)
		return nil
	}
	for _, k := range keys {
		if err := visit(k); err != nil {
			return nil, err
		}
	}
	return plan, nil
}

func sampleRank(rng *rand.Rand, size *big.Int, distribution string) *big.Int {
	a := new(big.Int).Rand(rng, size)
	if distribution == "" || distribution == "uniform" {
		return a
	}
	b := new(big.Int).Rand(rng, size)
	switch distribution {
	case "low":
		if b.Cmp(a) < 0 {
			return b
		}
	case "high":
		if b.Cmp(a) > 0 {
			return b
		}
	case "triangular":
		return a.Add(a, b).Rsh(a, 1)
	}
	return a
}
func (p attributePlan) apply(input *TicketInput, rng *rand.Rand) error {
	for _, c := range p {
		g := c.spec
		if g.Distribution == "normal" {
			lo, hi := float64(c.intervals[0].lo.Int64()), float64(c.intervals[0].hi.Int64())
			value := math.Round(*g.Mean + *g.StdDev*rng.NormFloat64())
			input.Int64Values[c.name] = int64(math.Max(lo, math.Min(hi, value)))
			continue
		}
		if g.Source == "shared" {
			switch g.Type {
			case "strings":
				input.StringLists[c.name] = append([]string{}, input.StringLists[g.Ref]...)
			case "uint64s":
				input.Uint64Lists[c.name] = append([]uint64{}, input.Uint64Lists[g.Ref]...)
			case "int64":
				input.Int64Values[c.name] = input.Int64Values[g.Ref]
			}
			continue
		}
		if g.Source == "ticketId" {
			switch g.Type {
			case "strings":
				input.StringLists[c.name] = []string{strconv.FormatUint(input.TicketID, 10)}
			case "uint64s":
				input.Uint64Lists[c.name] = []uint64{input.TicketID}
			case "int64":
				input.Int64Values[c.name] = int64(input.TicketID)
			}
			continue
		}
		sv := []string{}
		uv := []uint64{}
		removed := []*big.Int{}
		for i := 0; i < c.count; i++ {
			size := new(big.Int).Set(c.size)
			if !g.Replacement {
				size.Sub(size, big.NewInt(int64(i)))
			}
			rank := sampleRank(rng, size, g.Distribution)
			if !g.Replacement {
				for _, r := range removed {
					if rank.Cmp(r) >= 0 {
						rank.Add(rank, one)
					} else {
						break
					}
				}
				removed = append(removed, new(big.Int).Set(rank))
				sort.Slice(removed, func(i, j int) bool { return removed[i].Cmp(removed[j]) < 0 })
			}
			if g.Type == "strings" {
				sv = append(sv, g.Values[rank.Int64()])
				continue
			}
			for _, v := range c.intervals {
				span := new(big.Int).Add(new(big.Int).Sub(v.hi, v.lo), one)
				if rank.Cmp(span) < 0 {
					value := new(big.Int).Add(v.lo, rank)
					if g.Type == "int64" {
						input.Int64Values[c.name] = value.Int64()
					} else {
						uv = append(uv, value.Uint64())
					}
					break
				}
				rank.Sub(rank, span)
			}
		}
		if g.Type == "strings" {
			input.StringLists[c.name] = sv
		}
		if g.Type == "uint64s" {
			input.Uint64Lists[c.name] = uv
		}
	}
	return nil
}
