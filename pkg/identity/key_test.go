package identity

import (
	"encoding/json"
	"math"
	"testing"
)

func TestIdentityJSONRoundTrip(t *testing.T) {
	owner := OwnerRef{
		LogicalNode: LogicalNodeKey{
			Rule:        RuleKey{Namespace: "展示别名", RuleID: math.MaxUint64},
			PlacementID: math.MaxUint64,
		},
		PhysicalNodeID: math.MaxUint64,
	}
	encoded, err := json.Marshal(owner)
	const want = `{"logicalNode":{"rule":{"namespace":"展示别名","ruleId":"18446744073709551615"},"placementId":"18446744073709551615"},"physicalNodeId":"18446744073709551615"}`
	if err != nil || string(encoded) != want {
		t.Fatalf("身份编码 = %s, %v，期望 %s", encoded, err, want)
	}
	var decoded OwnerRef
	if err := json.Unmarshal(encoded, &decoded); err != nil || decoded != owner {
		t.Fatalf("身份回读 = %+v, %v，期望 %+v", decoded, err, owner)
	}
	for _, raw := range []string{
		`{"ruleId":1}`,
		`{"ruleId":"18446744073709551616"}`,
		`{"namespace":1,"ruleId":"1"}`,
	} {
		var key RuleKey
		if err := json.Unmarshal([]byte(raw), &key); err == nil {
			t.Errorf("标准字段标签应拒绝无效身份: %s", raw)
		}
	}
}

func TestScalarNodeIdentityUsesStandardNumberJSON(t *testing.T) {
	for _, test := range []struct {
		value any
		want  string
	}{
		{PhysicalNodeID(math.MaxUint64), `18446744073709551615`},
		{PlacementID(math.MaxUint64), `18446744073709551615`},
		{[]PhysicalNodeID{0, math.MaxUint64}, `[0,18446744073709551615]`},
		{map[PlacementID]string{math.MaxUint64: "node"}, `{"18446744073709551615":"node"}`},
	} {
		encoded, err := json.Marshal(test.value)
		if err != nil || string(encoded) != test.want {
			t.Errorf("编码 %v = %s, %v，期望 %s", test.value, encoded, err, test.want)
		}
	}
}

func TestIdentityValidationAndString(t *testing.T) {
	if (RuleKey{}).Validate() == nil || (PhysicalNodeID(0)).Validate() == nil || (PlacementID(0)).Validate() == nil {
		t.Fatal("规则和节点身份零值应被拒绝")
	}
	if got, want := (RuleKey{Namespace: "1", RuleID: 23}).String(), "2:23"; got != want {
		t.Fatalf("规则身份字符串 = %q，期望 %q", got, want)
	}
	if (RuleKey{Namespace: "1", RuleID: 23}).String() == (RuleKey{Namespace: "12", RuleID: 3}).String() {
		t.Fatal("不同的规则身份不应得到相同字符串")
	}
	if (RuleKey{Namespace: "first", RuleID: 23}).String() != (RuleKey{Namespace: "second", RuleID: 23}).String() {
		t.Fatal("展示别名不应影响规则身份字符串")
	}
	first := LogicalNodeKey{Rule: RuleKey{Namespace: "first", RuleID: 23}, PlacementID: 4}
	second := LogicalNodeKey{Rule: RuleKey{Namespace: "second", RuleID: 23}, PlacementID: 4}
	if first.String() != second.String() ||
		(OwnerRef{LogicalNode: first, PhysicalNodeID: 5}).String() != (OwnerRef{LogicalNode: second, PhysicalNodeID: 5}).String() {
		t.Fatal("展示别名不应影响节点或归属身份字符串")
	}
}
