package prefilter

import (
	"reflect"
	"strings"
	"testing"

	"github.com/xianyu11-x/matchSystem/pkg/common"
	"github.com/xianyu11-x/matchSystem/pkg/matchsystem/contract"
	"github.com/xianyu11-x/matchSystem/pkg/matchsystem/fact"
)

func TestBuiltinCreateTimeNearestAndRequirements(t *testing.T) {
	schema := contract.Contract{Indexes: []contract.IndexSpec{{Type: contract.IndexTypeInt64Range, Name: common.CreateTimeAttributeName}}}
	planJSON := []byte(`{"schemaVersion":"prefilter/v3","bitmap":{"resultType":"bitmap","expr":{
		"op":"lookup_nearest","index":"createTime","direction":"right","limit":2,
		"target":{"schemaVersion":"expression-scalar/v3","resultType":"int64",
			"expr":{"op":"int64_ref","source":"seed_attributes","name":"createTime"}}
	}}}`)
	plan, err := CompileJSON(planJSON, schema)
	if err != nil {
		t.Fatal(err)
	}
	requirements := plan.Requirements()
	if len(requirements.Attributes) != 0 || len(requirements.BuiltinAttributes) != 1 || requirements.BuiltinAttributes[0].Name != common.CreateTimeAttributeName || len(requirements.Indexes) != 1 {
		t.Fatalf("内置属性依赖分类错误: %#v", requirements)
	}
	requirements.BuiltinAttributes[0].Source = "changed"
	requirements.BuiltinAttributes[0].IndexTypes[0] = contract.IndexTypeMultiValue
	again := plan.Requirements()
	if again.BuiltinAttributes[0].Source != "ticket.createdAt" || again.BuiltinAttributes[0].IndexTypes[0] != contract.IndexTypeInt64Range {
		t.Fatal("修改 Requirements 返回值影响了 Plan")
	}
	store, err := New(plan)
	if err != nil {
		t.Fatal(err)
	}
	tickets := []*common.Ticket{{TicketID: 1, CreatedAt: 0}, {TicketID: 2, CreatedAt: -10}, {TicketID: 3, CreatedAt: 0}, {TicketID: 4, CreatedAt: 4}}
	for i, ticket := range tickets {
		if err := store.Add(uint32(i+1), ticket); err != nil {
			t.Fatalf("添加 Ticket: %v", err)
		}
	}
	session, err := store.BeginTick(Facts{})
	if err != nil {
		t.Fatal(err)
	}
	set, err := session.Candidates(1, tickets[0], Facts{})
	if err != nil || !reflect.DeepEqual(set.IDs(), []uint32{3, 4}) {
		t.Fatalf("内置时间最近候选=%v, err=%v", set, err)
	}
	if tickets[0].Int64Values != nil {
		t.Fatal("创建索引修改了调用方属性 map")
	}
	rangePlan, err := CompileJSON([]byte(`{"schemaVersion":"prefilter/v3","bitmap":{"resultType":"bitmap","expr":{
		"op":"lookup_range","index":"createTime",
		"min":{"schemaVersion":"expression-scalar/v3","resultType":"int64","expr":{"op":"int64_literal","value":-10}},
		"max":{"schemaVersion":"expression-scalar/v3","resultType":"int64","expr":{"op":"int64_literal","value":0}}
	}}}`), schema)
	if err != nil {
		t.Fatal(err)
	}
	rangeStore, err := New(rangePlan)
	if err != nil {
		t.Fatal(err)
	}
	for i, ticket := range tickets {
		if err := rangeStore.Add(uint32(i+1), ticket); err != nil {
			t.Fatal(err)
		}
	}
	rangeSession, err := rangeStore.BeginTick(Facts{})
	if err != nil {
		t.Fatal(err)
	}
	set, err = rangeSession.Candidates(1, tickets[0], Facts{})
	if err != nil || !reflect.DeepEqual(set.IDs(), []uint32{1, 2, 3}) {
		t.Fatalf("内置时间范围候选=%v, err=%v", set, err)
	}
}

func TestBuiltinCreateTimeScalarDependencyAndFingerprint(t *testing.T) {
	oldPlan, err := CompileJSON([]byte(`{"schemaVersion":"prefilter/v3","bitmap":{"resultType":"bitmap","expr":{"op":"none"}}}`), contract.Contract{})
	if err != nil {
		t.Fatal(err)
	}
	const oldFingerprint = "bfd769a37d77b40a60bcbb38d688529f4911833a10174c9c8802a1b37d8d43db"
	if oldPlan.Fingerprint() != oldFingerprint || len(oldPlan.Requirements().BuiltinAttributes) != 0 {
		t.Fatalf("未引用内置属性的旧计划发生变化: %s, %#v", oldPlan.Fingerprint(), oldPlan.Requirements())
	}
	unusedPlan, err := CompileJSON([]byte(`{"schemaVersion":"prefilter/v3","bitmap":{"resultType":"bitmap","expr":{"op":"none"}}}`), contract.Contract{Indexes: []contract.IndexSpec{{Type: contract.IndexTypeInt64Range, Name: common.CreateTimeAttributeName}}})
	if err != nil || len(unusedPlan.Requirements().BuiltinAttributes) != 0 {
		t.Fatalf("未使用的内置索引不应计为表达式依赖: %#v, %v", unusedPlan, err)
	}
	schema := contract.Contract{
		Attributes: []contract.AttributeSpec{{Name: "value", Type: fact.TypeInt64}},
		Indexes:    []contract.IndexSpec{{Type: contract.IndexTypeInt64Range, Name: "value"}},
	}
	plan, err := CompileJSON([]byte(`{"schemaVersion":"prefilter/v3","bitmap":{"resultType":"bitmap","expr":{
		"op":"lookup_range","index":"value",
		"min":{"schemaVersion":"expression-scalar/v3","resultType":"int64","expr":{"op":"int64_ref","source":"seed_attributes","name":"createTime"}},
		"max":{"schemaVersion":"expression-scalar/v3","resultType":"int64","expr":{"op":"int64_ref","source":"seed_attributes","name":"createTime"}}
	}}}`), schema)
	if err != nil {
		t.Fatal(err)
	}
	got := plan.Requirements()
	if len(got.Attributes) != 1 || got.Attributes[0].Name != "value" || len(got.BuiltinAttributes) != 1 || got.BuiltinAttributes[0].Name != common.CreateTimeAttributeName {
		t.Fatalf("表达式依赖分类错误: %#v", got)
	}
	changed := cloneRequirements(got)
	changed.BuiltinAttributes[0].Source = "different"
	if canonicalRequirements(got) == canonicalRequirements(changed) {
		t.Fatal("内置属性来源没有参与规范化指纹输入")
	}
	store, err := New(plan)
	if err != nil {
		t.Fatal(err)
	}
	seed := &common.Ticket{TicketID: 1, CreatedAt: -5, Int64Values: map[string]int64{"value": 20}}
	candidate := &common.Ticket{TicketID: 2, CreatedAt: 77, Int64Values: map[string]int64{"value": -5}}
	if err := store.Add(1, seed); err != nil {
		t.Fatal(err)
	}
	if err := store.Add(2, candidate); err != nil {
		t.Fatal(err)
	}
	session, err := store.BeginTick(Facts{})
	if err != nil {
		t.Fatal(err)
	}
	set, err := session.Candidates(1, seed, Facts{})
	if err != nil || !reflect.DeepEqual(set.IDs(), []uint32{2}) {
		t.Fatalf("创建时间表达式候选=%v, err=%v", set, err)
	}
}

func TestBuiltinCreateTimeStandaloneStoreRejectsOverrides(t *testing.T) {
	plan, err := CompileJSON([]byte(`{"schemaVersion":"prefilter/v3","bitmap":{"resultType":"bitmap","expr":{"op":"none"}}}`), contract.Contract{})
	if err != nil {
		t.Fatal(err)
	}
	store, err := New(plan)
	if err != nil {
		t.Fatal(err)
	}
	err = store.Add(1, &common.Ticket{TicketID: 1, CreatedAt: 10, Int64Values: map[string]int64{common.CreateTimeAttributeName: 10}})
	if err == nil || !strings.Contains(err.Error(), "READ_ONLY_ATTRIBUTE") {
		t.Fatalf("独立 Prefilter 未拒绝只读属性写入: %v", err)
	}
}
