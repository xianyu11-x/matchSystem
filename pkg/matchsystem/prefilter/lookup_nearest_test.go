package prefilter

import (
	"fmt"
	"math"
	"math/big"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/RoaringBitmap/roaring/v2"
	"github.com/xianyu11-x/matchSystem/pkg/common"
	"github.com/xianyu11-x/matchSystem/pkg/matchsystem/contract"
)

func TestInt64RangeNearestDirectionsAndSeed(t *testing.T) {
	index := newInt64RangeTestIndex(2)
	for doc, value := range []int64{10, 8, 10, 12, 12, 14} {
		index.add(uint32(doc+1), int64RangeTestTicket(value))
	}
	index.prepare()
	for _, test := range []struct {
		name      string
		target    int64
		direction string
		limit     uint64
		scope     *roaring.Bitmap
		want      []uint32
	}{
		{name: "left includes target", target: 10, direction: "left", limit: 1, want: []uint32{3}},
		{name: "right includes target", target: 10, direction: "right", limit: 1, want: []uint32{3}},
		{name: "both ties choose smaller value", target: 10, direction: "both", limit: 2, want: []uint32{2, 3}},
		{name: "same value keeps all documents", target: 11, direction: "right", limit: 1, want: []uint32{4, 5}},
		{name: "seed only value does not consume limit", target: 10, direction: "right", limit: 1, scope: roaring.BitmapOf(1, 4, 5), want: []uint32{4, 5}},
		{name: "scope excludes closer values", target: 10, direction: "both", limit: 1, scope: roaring.BitmapOf(1, 2, 4, 5), want: []uint32{2}},
		{name: "limit exceeds distinct values", target: 10, direction: "right", limit: 100, want: []uint32{3, 4, 5, 6}},
		{name: "no candidate on side", target: 7, direction: "left", limit: 2},
		{name: "empty scope", target: 10, direction: "both", limit: 2, scope: roaring.New()},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := index.nearest(boundIndexQuery{kind: boundQueryNearest, target: test.target, direction: test.direction, limit: test.limit}, test.scope, 1)
			if err != nil {
				t.Fatal(err)
			}
			assertInt64RangeBitmapEquals(t, test.want, got, test.name)
		})
	}
}

func TestInt64RangeNearestExtremeDistances(t *testing.T) {
	index := newInt64RangeTestIndex(2)
	index.add(1, int64RangeTestTicket(math.MinInt64))
	index.add(2, int64RangeTestTicket(math.MaxInt64))
	index.prepare()
	for _, test := range []struct {
		target    int64
		direction string
		want      []uint32
	}{
		{target: -1, direction: "both", want: []uint32{1}},
		{target: 0, direction: "both", want: []uint32{2}},
		{target: math.MinInt64, direction: "left", want: []uint32{1}},
		{target: math.MaxInt64, direction: "right", want: []uint32{2}},
	} {
		got, err := index.nearest(boundIndexQuery{kind: boundQueryNearest, target: test.target, direction: test.direction, limit: 1}, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		assertInt64RangeBitmapEquals(t, test.want, got, "extreme nearest")
	}
}

func TestInt64RangeNearestMatchesReference(t *testing.T) {
	rng := rand.New(rand.NewSource(20261124))
	index := newInt64RangeTestIndex(4)
	values := make(map[uint32]int64)
	for id := uint32(1); id <= 80; id++ {
		value := int64(rng.Intn(31) - 15)
		if id == 1 {
			value = math.MinInt64
		} else if id == 2 {
			value = math.MaxInt64
		}
		values[id] = value
		index.add(id, int64RangeTestTicket(value))
	}
	index.prepare()
	for iteration := 0; iteration < 200; iteration++ {
		target := int64(rng.Uint64())
		if iteration%3 == 0 {
			target = int64(rng.Intn(41) - 20)
		}
		seedID := uint32(rng.Intn(80) + 1)
		var scope *roaring.Bitmap
		if iteration%2 == 0 {
			scope = roaring.New()
			for id := uint32(1); id <= 80; id++ {
				if rng.Intn(3) == 0 {
					scope.Add(id)
				}
			}
		}
		direction := []string{"left", "right", "both"}[iteration%3]
		limit := uint64(rng.Intn(8) + 1)
		query := boundIndexQuery{kind: boundQueryNearest, target: target, direction: direction, limit: limit}
		got, err := index.nearest(query, scope, seedID)
		if err != nil {
			t.Fatal(err)
		}
		want := nearestReference(values, query, scope, seedID)
		assertInt64RangeBitmapEquals(t, want, got, fmt.Sprintf("reference iteration %d", iteration))
	}
}

func nearestReference(values map[uint32]int64, query boundIndexQuery, scope *roaring.Bitmap, seedID uint32) []uint32 {
	byValue := make(map[int64][]uint32)
	for id, value := range values {
		if id == seedID || scope != nil && !scope.Contains(id) || query.direction == "left" && value > query.target || query.direction == "right" && value < query.target {
			continue
		}
		byValue[value] = append(byValue[value], id)
	}
	keys := make([]int64, 0, len(byValue))
	for value := range byValue {
		keys = append(keys, value)
	}
	sort.Slice(keys, func(i, j int) bool {
		if query.direction == "left" {
			return keys[i] > keys[j]
		}
		if query.direction == "right" {
			return keys[i] < keys[j]
		}
		left := new(big.Int).Abs(new(big.Int).Sub(big.NewInt(keys[i]), big.NewInt(query.target)))
		right := new(big.Int).Abs(new(big.Int).Sub(big.NewInt(keys[j]), big.NewInt(query.target)))
		if comparison := left.Cmp(right); comparison != 0 {
			return comparison < 0
		}
		return keys[i] < keys[j]
	})
	var out []uint32
	for position, key := range keys {
		if uint64(position) >= query.limit {
			break
		}
		out = append(out, byValue[key]...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func TestLookupNearestBitmapScopeAndBranches(t *testing.T) {
	nearest := nearestTestExpr("value", `{"op":"int64_literal","value":0}`, "right", 1)
	nearestTwo := nearestTestExpr("value", `{"op":"int64_literal","value":0}`, "right", 2)
	nearestOther := nearestTestExpr("other", `{"op":"int64_literal","value":0}`, "right", 1)
	group := nearestTestRange("group", 1)
	tier := nearestTestRange("tier", 1)
	onlyB := nearestTestRange("other", 2)
	none := `{"op":"none"}`
	for _, test := range []struct {
		name string
		expr string
		want []uint32
	}{
		{name: "standalone", expr: nearest, want: []uint32{2}},
		{name: "single nearest in and", expr: nearestTestAnd(nearest), want: []uint32{2}},
		{name: "ordinary conditions run before nearest", expr: nearestTestAnd(nearest, group, tier), want: []uint32{3}},
		{name: "isolated child then outer filter", expr: nearestTestAnd(nearestTestAnd(group, nearest), tier)},
		{name: "outer filter before isolated child", expr: nearestTestAnd(tier, nearestTestAnd(group, nearest))},
		{name: "outer narrow anchor cannot influence child", expr: nearestTestAnd(onlyB, nearestTestAnd(group, nearest))},
		{name: "outer narrow anchor cannot influence child in reverse order", expr: nearestTestAnd(nearestTestAnd(group, nearest), onlyB)},
		{name: "isolated nearest only child", expr: nearestTestAnd(tier, nearestTestAnd(nearest))},
		{name: "isolated nearest only child under narrow anchor", expr: nearestTestAnd(onlyB, nearestTestAnd(nearest))},
		{name: "scope from exclude without ordinary anchor", expr: nearestTestAnd(`{"op":"exclude","value":`+group+`}`, nearest), want: []uint32{4}},
		{name: "nearest nodes keep order", expr: nearestTestAnd(nearestTwo, nearestOther), want: []uint32{3}},
		{name: "reverse nearest nodes keep order", expr: nearestTestAnd(nearestOther, nearestTwo), want: []uint32{4}},
		{name: "or branch is independent", expr: nearestTestAnd(tier, `{"op":"or","children":[`+nearest+`,`+none+`]}`)},
		{name: "or branch is independent under narrow anchor", expr: nearestTestAnd(onlyB, `{"op":"or","children":[`+nearest+`,`+none+`]}`)},
		{name: "if branch is independent", expr: nearestTestAnd(tier, `{"op":"if","when":{"schemaVersion":"expression-scalar/v3","resultType":"bool","expr":{"op":"bool_literal","value":true}},"then":`+nearest+`,"else":`+none+`}`)},
		{name: "exclude branch is independent", expr: nearestTestAnd(tier, `{"op":"exclude","value":`+nearest+`}`), want: []uint32{3, 4, 5, 6}},
		{name: "empty ordinary scope", expr: nearestTestAnd(none, nearest)},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := nearestTestCandidates(t, test.expr, Facts{}, nearestTestTickets())
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(test.want) || !reflect.DeepEqual(append([]uint32(nil), got...), test.want) {
				t.Fatalf("候选=%v, 期望 %v", got, test.want)
			}
		})
	}
}

func TestLookupNearestDynamicTargetAndMissingValue(t *testing.T) {
	expr := nearestTestExpr("value", `{"op":"int64_ref","source":"seed_attributes","name":"value"}`, "left", 1)
	got, err := nearestTestCandidates(t, expr, Facts{}, nearestTestTickets())
	if err != nil || !reflect.DeepEqual(got, []uint32{6}) {
		t.Fatalf("动态 seed 目标候选=%v, err=%v", got, err)
	}
	tickets := nearestTestTickets()
	delete(tickets[0].Int64Values, "value")
	if _, err := nearestTestCandidates(t, expr, Facts{}, tickets); err == nil || !strings.Contains(err.Error(), "MISSING_VALUE") {
		t.Fatalf("缺失 seed 目标应报错，得到 %v", err)
	}
	tickExpr := nearestTestExpr("value", `{"op":"int64_ref","source":"tick_facts","name":"target"}`, "both", 1)
	got, err = nearestTestCandidates(t, tickExpr, Facts{Int64Values: map[string]int64{"target": 2}}, nearestTestTickets())
	if err != nil || !reflect.DeepEqual(got, []uint32{3}) {
		t.Fatalf("动态 Tick 目标候选=%v, err=%v", got, err)
	}
	seedFactExpr := nearestTestExpr("value", `{"op":"int64_ref","source":"seed_facts","name":"seedTarget"}`, "both", 1)
	got, err = nearestTestCandidatesWithSeedFacts(t, seedFactExpr, Facts{}, Facts{Int64Values: map[string]int64{"seedTarget": 3}}, nearestTestTickets())
	if err != nil || !reflect.DeepEqual(got, []uint32{4}) {
		t.Fatalf("动态 seed Fact 目标候选=%v, err=%v", got, err)
	}
}

func TestLookupNearestValidationAndFingerprint(t *testing.T) {
	base := nearestTestExpr("value", `{"op":"int64_literal","value":0}`, "right", 1)
	for _, test := range []struct{ name, expr string }{
		{"missing index", strings.Replace(base, `"index":"value",`, "", 1)},
		{"unknown index", strings.Replace(base, `"index":"value"`, `"index":"missing"`, 1)},
		{"wrong index type", strings.Replace(base, `"index":"value"`, `"index":"pool"`, 1)},
		{"missing target", strings.Replace(base, `"target":{"schemaVersion":"expression-scalar/v3","resultType":"int64","expr":{"op":"int64_literal","value":0}},`, "", 1)},
		{"invalid direction", strings.Replace(base, `"direction":"right"`, `"direction":"up"`, 1)},
		{"missing direction", strings.Replace(base, `"direction":"right",`, "", 1)},
		{"zero limit", strings.Replace(base, `"limit":1`, `"limit":0`, 1)},
		{"negative limit", strings.Replace(base, `"limit":1`, `"limit":-1`, 1)},
		{"fractional limit", strings.Replace(base, `"limit":1`, `"limit":1.5`, 1)},
		{"oversized limit", strings.Replace(base, `"limit":1`, `"limit":18446744073709551616`, 1)},
		{"missing limit", strings.Replace(base, `,"limit":1`, "", 1)},
		{"unknown field", strings.Replace(base, `,"limit":1`, `,"limit":1,"extra":1`, 1)},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := nearestTestCompile(test.expr); err == nil {
				t.Fatal("非法配置未被拒绝")
			}
		})
	}
	first, err := nearestTestCompile(base)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := nearestTestCompile(nearestTestExpr("value", `{"op":"int64_literal","value":0}`, "right", math.MaxUint64)); err != nil {
		t.Fatalf("最大 uint64 上限应被接受: %v", err)
	}
	for _, other := range []string{
		nearestTestExpr("value", `{"op":"int64_literal","value":1}`, "right", 1),
		nearestTestExpr("value", `{"op":"int64_literal","value":0}`, "left", 1),
		nearestTestExpr("value", `{"op":"int64_literal","value":0}`, "right", 2),
		nearestTestExpr("other", `{"op":"int64_literal","value":0}`, "right", 1),
	} {
		plan, err := nearestTestCompile(other)
		if err != nil {
			t.Fatal(err)
		}
		if plan.Fingerprint() == first.Fingerprint() {
			t.Fatalf("不同查询使用相同指纹: %s", other)
		}
	}
	second := nearestTestExpr("other", `{"op":"int64_literal","value":0}`, "right", 1)
	ab, err := nearestTestCompile(nearestTestAnd(base, second))
	if err != nil {
		t.Fatal(err)
	}
	ba, err := nearestTestCompile(nearestTestAnd(second, base))
	if err != nil {
		t.Fatal(err)
	}
	if ab.Fingerprint() == ba.Fingerprint() {
		t.Fatal("不同 nearest 执行顺序使用相同指纹")
	}
}

func nearestTestExpr(index, target, direction string, limit uint64) string {
	return fmt.Sprintf(`{"op":"lookup_nearest","index":%q,"target":{"schemaVersion":"expression-scalar/v3","resultType":"int64","expr":%s},"direction":%q,"limit":%d}`, index, target, direction, limit)
}

func nearestTestRange(index string, value int64) string {
	return fmt.Sprintf(`{"op":"lookup_range","index":%q,"min":{"schemaVersion":"expression-scalar/v3","resultType":"int64","expr":{"op":"int64_literal","value":%d}},"max":{"schemaVersion":"expression-scalar/v3","resultType":"int64","expr":{"op":"int64_literal","value":%d}}}`, index, value, value)
}

func nearestTestAnd(children ...string) string {
	return `{"op":"and","children":[` + strings.Join(children, ",") + `]}`
}

func nearestTestCompile(expr string) (*Plan, error) {
	schema := contract.Contract{
		Attributes: []contract.AttributeSpec{{Name: "pool", Type: contract.FactTypeStrings, MaxValues: 1}},
		Facts: []contract.FactSpec{
			{Name: "target", Type: contract.FactTypeInt64, Scope: contract.ScopeTick},
			{Name: "seedTarget", Type: contract.FactTypeInt64, Scope: contract.ScopeObject},
		},
		Indexes: []contract.IndexSpec{{Type: contract.IndexTypeMultiValue, Name: "pool", KeyType: contract.KeyTypeString,
			MaxDocumentValues: 1, MaxQueryValues: 1}},
	}
	for _, name := range []string{"value", "other", "group", "tier"} {
		schema.Attributes = append(schema.Attributes, contract.AttributeSpec{Name: name, Type: contract.FactTypeInt64})
		schema.Indexes = append(schema.Indexes, contract.IndexSpec{Type: contract.IndexTypeInt64Range, Name: name})
	}
	return CompileJSON([]byte(`{"schemaVersion":"prefilter/v3","bitmap":{"resultType":"bitmap","expr":`+expr+`}}`), schema)
}

func nearestTestTickets() []*common.Ticket {
	values := [][4]int64{{100, 0, 1, 9}, {1, 4, 1, 0}, {2, 2, 1, 1}, {3, 0, 2, 1}, {4, 1, 1, 1}, {5, 3, 2, 1}}
	tickets := make([]*common.Ticket, len(values))
	for i, value := range values {
		tickets[i] = &common.Ticket{TicketID: uint64(i + 1), Int64Values: map[string]int64{"value": value[0], "other": value[1], "group": value[2], "tier": value[3]}}
	}
	return tickets
}

func nearestTestCandidates(t *testing.T, expr string, tick Facts, tickets []*common.Ticket) ([]uint32, error) {
	return nearestTestCandidatesWithSeedFacts(t, expr, tick, Facts{}, tickets)
}

func nearestTestCandidatesWithSeedFacts(t *testing.T, expr string, tick, seedFacts Facts, tickets []*common.Ticket) ([]uint32, error) {
	t.Helper()
	plan, err := nearestTestCompile(expr)
	if err != nil {
		return nil, err
	}
	store, err := New(plan)
	if err != nil {
		return nil, err
	}
	for i, ticket := range tickets {
		if err := store.Add(uint32(i+1), ticket); err != nil {
			return nil, err
		}
	}
	session, err := store.BeginTick(tick)
	if err != nil {
		return nil, err
	}
	candidates, err := session.Candidates(1, tickets[0], seedFacts)
	if err != nil {
		return nil, err
	}
	return candidates.IDs(), nil
}
