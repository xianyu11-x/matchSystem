package contract

import (
	"strings"
	"testing"

	"github.com/xianyu11-x/matchSystem/pkg/common"
	"github.com/xianyu11-x/matchSystem/pkg/matchsystem/fact"
)

func TestBuiltinAttributesAndEffectiveContract(t *testing.T) {
	builtins := BuiltinAttributes()
	if len(builtins) != 1 || builtins[0].Name != common.CreateTimeAttributeName || builtins[0].Type != fact.TypeInt64 || !builtins[0].ReadOnly || builtins[0].Source != "ticket.createdAt" || len(builtins[0].IndexTypes) != 1 || builtins[0].IndexTypes[0] != IndexTypeInt64Range {
		t.Fatalf("内置属性目录不正确: %#v", builtins)
	}
	builtins[0].Name = "changed"
	builtins[0].IndexTypes[0] = IndexTypeMultiValue
	again := BuiltinAttributes()
	if again[0].Name != common.CreateTimeAttributeName || again[0].IndexTypes[0] != IndexTypeInt64Range {
		t.Fatalf("修改返回值影响了内置目录: %#v", again)
	}
	schema := Contract{
		Limits:     Limits{MaxAttributes: 1},
		Attributes: []AttributeSpec{{Name: "rank", Type: fact.TypeInt64}},
		Indexes:    []IndexSpec{{Type: IndexTypeInt64Range, Name: common.CreateTimeAttributeName}},
	}
	if err := schema.Validate(); err != nil {
		t.Fatalf("内置属性不应占业务属性配额: %v", err)
	}
	if got := schema.AttributeSpecs(); len(got) != 1 || got[0].Name != "rank" {
		t.Fatalf("显式属性目录错误: %#v", got)
	}
	if got := schema.EffectiveAttributeSpecs(); len(got) != 2 || got[1].Name != common.CreateTimeAttributeName || got[1].Type != fact.TypeInt64 {
		t.Fatalf("有效属性目录错误: %#v", got)
	} else {
		got[0].Name = "changed"
		if schema.Attributes[0].Name != "rank" {
			t.Fatal("修改有效目录影响了原 Contract")
		}
	}
	parsed, err := Parse([]byte(`{"schemaVersion":"logical-node-contract/v3","attributes":[],"facts":[],"indexes":[{"type":"int64_range","name":"createTime"}]}`), DefaultLimits())
	if err != nil || len(parsed.Attributes) != 0 || len(parsed.Indexes) != 1 {
		t.Fatalf("无需显式属性声明即可创建索引: %#v, %v", parsed, err)
	}
}

func TestBuiltinAttributeRejectsReservedNamesAndWrites(t *testing.T) {
	attributeErr := (Contract{Attributes: []AttributeSpec{{Name: common.CreateTimeAttributeName, Type: fact.TypeInt64}}}).Validate()
	if attributeErr == nil || !strings.Contains(attributeErr.Error(), "RESERVED_ATTRIBUTE") || !strings.Contains(attributeErr.Error(), "remove its declaration") {
		t.Fatalf("属性冲突缺少迁移提示: %v", attributeErr)
	}
	factErr := (Contract{Facts: []FactSpec{{Name: common.CreateTimeAttributeName, Type: fact.TypeInt64, Scope: fact.ScopeTick}}}).Validate()
	if factErr == nil || !strings.Contains(factErr.Error(), "RESERVED_ATTRIBUTE") || !strings.Contains(factErr.Error(), "rename the Fact") {
		t.Fatalf("Fact 冲突缺少迁移提示: %v", factErr)
	}
	for _, schema := range []Contract{
		{Attributes: []AttributeSpec{{Name: common.CreateTimeAttributeName, Type: fact.TypeInt64}}},
		{Attributes: []AttributeSpec{{Name: common.CreateTimeAttributeName, Type: fact.TypeStrings, MaxValues: 1}}},
		{Facts: []FactSpec{{Name: common.CreateTimeAttributeName, Type: fact.TypeInt64, Scope: fact.ScopeTick}}},
		{Indexes: []IndexSpec{{Type: IndexTypeMultiValue, Name: common.CreateTimeAttributeName, KeyType: KeyTypeString, MaxDocumentValues: 1, MaxQueryValues: 1}}},
		{Indexes: []IndexSpec{{Type: IndexTypeInt64Range, Name: "missing"}}},
		{Indexes: []IndexSpec{{Type: IndexTypeInt64Range, Name: common.CreateTimeAttributeName}, {Type: IndexTypeInt64Range, Name: common.CreateTimeAttributeName}}},
	} {
		if err := schema.Validate(); err == nil {
			t.Fatalf("非法声明被接受: %#v", schema)
		}
	}
	validator, err := (Contract{Attributes: []AttributeSpec{{Name: "rank", Type: fact.TypeInt64}}}).CompileAttributeValidator()
	if err != nil {
		t.Fatal(err)
	}
	for _, ticket := range []*common.Ticket{
		{Int64Values: map[string]int64{common.CreateTimeAttributeName: 0}},
		{StringLists: map[string][]string{common.CreateTimeAttributeName: {"x"}}},
		{Uint64Lists: map[string][]uint64{common.CreateTimeAttributeName: {1}}},
		{Int64Values: map[string]int64{common.CreateTimeAttributeName: 0}, StringLists: map[string][]string{common.CreateTimeAttributeName: {"x"}}},
	} {
		if err := validator.ValidateTicket("ticket", ticket); err == nil || !strings.Contains(err.Error(), "READ_ONLY_ATTRIBUTE") {
			t.Fatalf("内置属性写入应报告只读错误，得到 %v", err)
		}
	}
	if err := validator.ValidateTicket("ticket", &common.Ticket{CreatedAt: -1, Int64Values: map[string]int64{"rank": 7}}); err != nil {
		t.Fatalf("合法 Ticket 被拒绝: %v", err)
	}
	if err := validator.ValidateTicket("ticket", &common.Ticket{Int64Values: map[string]int64{"unknown": 7}}); err == nil || !strings.Contains(err.Error(), "UNDECLARED_ATTRIBUTE") {
		t.Fatalf("未知业务属性应继续报错，得到 %v", err)
	}
}
