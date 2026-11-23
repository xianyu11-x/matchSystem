package matchsystem

import (
	"context"
	"errors"
	"testing"

	"github.com/xianyu11-x/matchSystem/pkg/identity"
)

func TestPhysicalNodeUsesRuleIDForIdentity(t *testing.T) {
	ctx := context.Background()
	selector := FuncLogicalNodeSelector(func(selection LogicalNodeSelectContext) (identity.LogicalNodeKey, error) {
		for _, candidate := range selection.Candidates {
			if candidate.Key.Rule.Namespace != "" {
				t.Fatalf("选择器候选不应包含展示别名: %+v", candidate.Key)
			}
			if candidate.Eligible {
				key := candidate.Key
				key.Rule.Namespace = "伪造别名"
				return key, nil
			}
		}
		return identity.LogicalNodeKey{}, ErrNoLogicalNodeAvailable
	})
	physical, err := NewPhysicalNode(1, WithLogicalNodeSelector(selector))
	if err != nil {
		t.Fatal(err)
	}
	first := identity.LogicalNodeKey{Rule: identity.RuleKey{Namespace: "配置别名", RuleID: 1}, PlacementID: 11}
	second := identity.LogicalNodeKey{Rule: identity.RuleKey{Namespace: "配置别名", RuleID: 2}, PlacementID: 22}
	makeSpec := func(key identity.LogicalNodeKey, documentAlias string) LogicalNodeSpec {
		documentKey := key.Rule
		documentKey.Namespace = documentAlias
		return LogicalNodeSpec{Key: key, RuleJSON: testRuleJSON(t, documentKey,
			integrationEmptyContractJSON, integrationNonePrefilterJSON, integrationCompleteEvaluationJSON,
			logicalNodeConfig{MaxPlayers: 1})}
	}
	if err := physical.Load(ctx, makeSpec(first, "规则文档别名")); err != nil {
		t.Fatalf("相同 RuleID 的不同别名应能绑定: %v", err)
	}
	duplicate := first
	duplicate.Rule.Namespace = "另一个别名"
	duplicate.PlacementID = 33
	if err := physical.Load(ctx, makeSpec(duplicate, "第三个别名")); !errors.Is(err, ErrDuplicateRuleKey) {
		t.Fatalf("相同 RuleID 的不同别名应冲突: %v", err)
	}
	if err := physical.Load(ctx, makeSpec(second, "配置别名")); err != nil {
		t.Fatalf("不同 RuleID 可以使用同一别名: %v", err)
	}
	owner := identity.OwnerRef{PhysicalNodeID: 1, LogicalNode: first}
	owner.LogicalNode.Rule.Namespace = "旧别名"
	if err := physical.Add(ctx, owner, testTicket(1)); err != nil {
		t.Fatalf("不同别名应定位相同节点: %v", err)
	}
	wrongPlacement := owner.LogicalNode
	wrongPlacement.PlacementID++
	if _, err := physical.FactSpecs(ctx, wrongPlacement); !errors.Is(err, ErrOwnerMismatch) {
		t.Fatalf("不同 PlacementID 应被拒绝: %v", err)
	}
	if err := physical.BeginMatchRound(ctx, 10); err != nil {
		t.Fatal(err)
	}
	result, err := physical.ProduceMatch(ctx)
	if err != nil || result.Match == nil || result.LogicalNode != first {
		t.Fatalf("应返回配置中的真实别名: %+v, %v", result, err)
	}
}

func TestSmoothWeightsIgnoreRuleAlias(t *testing.T) {
	duplicate := map[identity.RuleKey]uint32{
		{Namespace: "first", RuleID: 1}:  2,
		{Namespace: "second", RuleID: 1}: 3,
	}
	if _, err := NewSmoothWeightedRoundRobinLogicalNodeSelector(duplicate); err == nil {
		t.Fatal("相同 RuleID 的权重配置应冲突")
	}
	selector, err := NewSmoothWeightedRoundRobinLogicalNodeSelector(map[identity.RuleKey]uint32{
		{Namespace: "shared", RuleID: 1}: 2,
		{Namespace: "shared", RuleID: 2}: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	selection := LogicalNodeSelectContext{Candidates: []LogicalNodeCandidate{
		{Key: identity.LogicalNodeKey{Rule: identity.RuleKey{Namespace: "first", RuleID: 1}, PlacementID: 1}, Eligible: true},
		{Key: identity.LogicalNodeKey{Rule: identity.RuleKey{Namespace: "first", RuleID: 2}, PlacementID: 2}, Eligible: true},
	}}
	counts := map[uint64]int{}
	for _, alias := range []string{"first", "second", "third"} {
		for i := range selection.Candidates {
			selection.Candidates[i].Key.Rule.Namespace = alias
		}
		key, err := selector.Select(selection)
		if err != nil {
			t.Fatal(err)
		}
		counts[key.Rule.RuleID]++
	}
	if counts[1] != 2 || counts[2] != 1 {
		t.Fatalf("别名变化不应重置权重状态: %v", counts)
	}
}
