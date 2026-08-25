package identity

import "testing"

func TestKeysAreComparableAndCanonical(t *testing.T) {
	left := RuleKey{Namespace: "a", RuleID: 23}
	right := RuleKey{Namespace: "a2", RuleID: 3}
	if left.String() == right.String() {
		t.Fatal("length-prefixed canonical strings must not collide")
	}

	nodes := map[LogicalNodeKey]string{
		{Rule: left, PlacementID: "p1"}:  "left",
		{Rule: right, PlacementID: "p1"}: "right",
	}
	if len(nodes) != 2 {
		t.Fatalf("expected distinct comparable keys, got %d", len(nodes))
	}
}

func TestOwnerRefValidation(t *testing.T) {
	valid := OwnerRef{
		LogicalNode:    LogicalNodeKey{Rule: RuleKey{RuleID: 1}, PlacementID: "p1"},
		PhysicalNodeID: "physical-1",
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid owner: %v", err)
	}
	if err := (OwnerRef{}).Validate(); err == nil {
		t.Fatal("empty owner must be rejected")
	}
}

func TestRuleIDMustBePositive(t *testing.T) {
	for _, ruleID := range []RuleID{0, -1} {
		if err := (RuleKey{RuleID: ruleID}).Validate(); err == nil {
			t.Fatalf("RuleID %d must be rejected", ruleID)
		}
	}
}
