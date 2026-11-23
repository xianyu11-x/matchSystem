package matchsystem

import (
	"context"
	"errors"
	"testing"

	"github.com/xianyu11-x/matchSystem/pkg/identity"
)

func newReconfigureLogicalNode(t *testing.T, key identity.LogicalNodeKey) *LogicalNode {
	t.Helper()
	rule := integrationRuleJSON(t, key.Rule, integrationEmptyContractJSON, integrationNonePrefilterJSON,
		integrationCompleteEvaluationJSON, `{"type":"constant","params":{"value":1}}`,
		`{"type":"arrival","params":{}}`, logicalNodeConfig{SeedScheduler: seedSchedulerConfig{
			AttemptLimitPerProduceMatch: 1,
			AttemptLimitPerMatchRound:   10,
		}})
	node, err := NewLogicalNode(LogicalNodeSpec{Key: key, RuleJSON: rule})
	if err != nil {
		t.Fatal(err)
	}
	return node
}

func TestPhysicalNodeReconfigurePreservesRuntimeAndInitializesReplacements(t *testing.T) {
	ctx := context.Background()
	physical, err := NewPhysicalNode(1)
	if err != nil {
		t.Fatal(err)
	}
	first := identity.LogicalNodeKey{Rule: identity.RuleKey{Namespace: "原别名", RuleID: 1}, PlacementID: 11}
	second := identity.LogicalNodeKey{Rule: identity.RuleKey{Namespace: "旧节点", RuleID: 2}, PlacementID: 22}
	third := identity.LogicalNodeKey{Rule: identity.RuleKey{Namespace: "新增节点", RuleID: 3}, PlacementID: 33}
	original := newReconfigureLogicalNode(t, first)
	removed := newReconfigureLogicalNode(t, second)
	if err := physical.Reconfigure([]identity.LogicalNodeKey{first, second}, []*LogicalNode{original, removed}, nil); err != nil {
		t.Fatal(err)
	}
	if physical.roundActive || original.seedRound.initialized || removed.seedRound.initialized {
		t.Fatal("没有活动轮次时不应初始化准备节点的轮次")
	}
	for _, id := range []TicketID{1, 2} {
		if err := physical.Add(ctx, identity.OwnerRef{PhysicalNodeID: 1, LogicalNode: first}, testTicket(id)); err != nil {
			t.Fatal(err)
		}
	}
	if err := physical.BeginMatchRound(ctx, 100); err != nil {
		t.Fatal(err)
	}
	if original.nextSeed() == nil {
		t.Fatal("未能消费原节点的一个种子")
	}
	originalRound := original.seedRound
	oldSelector := physical.selector.(*roundRobinLogicalNodeSelector)
	oldSelector.last = first
	oldSelector.hasLast = true

	replacementKey := second
	replacementKey.Rule.Namespace = "新节点"
	replacement := newReconfigureLogicalNode(t, replacementKey)
	added := newReconfigureLogicalNode(t, third)
	if err := replacement.Add(testTicket(5)); err != nil {
		t.Fatal(err)
	}
	if err := replacement.BeginMatchRound(200); err != nil {
		t.Fatal(err)
	}
	if replacement.nextSeed() == nil {
		t.Fatal("未能消费准备节点的一个种子")
	}
	firstAlias, secondAlias := first, second
	firstAlias.Rule.Namespace = "请求别名"
	secondAlias.Rule.Namespace = "另一请求别名"
	if err := physical.Reconfigure([]identity.LogicalNodeKey{third, firstAlias, secondAlias}, []*LogicalNode{replacement, added}, nil); err != nil {
		t.Fatal(err)
	}
	if physical.nodes[1] != original || physical.nodes[2] != replacement || physical.nodes[3] != added {
		t.Fatal("原节点、新节点或替换节点的实例不正确")
	}
	if original.Len() != 2 || original.seedRound != originalRound {
		t.Fatalf("未变更节点的队列或轮次进度被重置: %+v", original.seedRound)
	}
	if replacement.Len() != 1 || !replacement.seedRound.initialized || replacement.seedRound.now != 100 || replacement.seedRound.attemptedSeeds != 0 || !replacement.seedOrderRuntime.HasNext() {
		t.Fatalf("替换节点未正确进入当前轮次: %+v", replacement.seedRound)
	}
	if !added.seedRound.initialized || added.seedRound.now != 100 || added.seedRound.attemptedSeeds != 0 {
		t.Fatalf("新增节点未正确进入当前轮次: %+v", added.seedRound)
	}
	if physical.selector != oldSelector || !oldSelector.hasLast || oldSelector.last != first {
		t.Fatal("nil 选择器未保留原实例及游标")
	}
	descriptors := physical.Describe()
	if len(descriptors) != 3 || descriptors[0].Key != third || descriptors[1].Key != first || descriptors[2].Key != replacementKey {
		t.Fatalf("节点顺序或实际别名不正确: %+v", descriptors)
	}
	newSelector := NewRoundRobinLogicalNodeSelector()
	if err := physical.Reconfigure([]identity.LogicalNodeKey{third, first, second}, nil, newSelector); err != nil {
		t.Fatal(err)
	}
	if physical.selector != newSelector || original.seedRound != originalRound || physical.nodes[2] != replacement {
		t.Fatal("替换选择器影响了节点实例或其轮次进度")
	}
}

func TestPhysicalNodeReconfigureFailureLeavesStateUntouched(t *testing.T) {
	first := identity.LogicalNodeKey{Rule: identity.RuleKey{RuleID: 1}, PlacementID: 11}
	second := identity.LogicalNodeKey{Rule: identity.RuleKey{RuleID: 2}, PlacementID: 22}
	third := identity.LogicalNodeKey{Rule: identity.RuleKey{RuleID: 3}, PlacementID: 33}
	physical, err := NewPhysicalNode(1)
	if err != nil {
		t.Fatal(err)
	}
	original := newReconfigureLogicalNode(t, first)
	prepared := newReconfigureLogicalNode(t, second)
	if err := original.Add(testTicket(1)); err != nil {
		t.Fatal(err)
	}
	if err := prepared.Add(testTicket(2)); err != nil {
		t.Fatal(err)
	}
	if err := physical.Reconfigure([]identity.LogicalNodeKey{first}, []*LogicalNode{original}, nil); err != nil {
		t.Fatal(err)
	}
	if err := physical.BeginMatchRound(context.Background(), 100); err != nil {
		t.Fatal(err)
	}
	if err := prepared.BeginMatchRound(200); err != nil {
		t.Fatal(err)
	}
	if original.nextSeed() == nil || prepared.nextSeed() == nil {
		t.Fatal("未能消费原节点或准备节点的种子")
	}
	originalRound, preparedRound := original.seedRound, prepared.seedRound
	oldSelector := physical.selector

	firstAlias, wrongPlacement := first, second
	firstAlias.Rule.Namespace = "不同别名"
	wrongPlacement.PlacementID++
	duplicate := *prepared
	duplicate.key.Rule.Namespace = "不同别名"
	invalidRuntime := newReconfigureLogicalNode(t, third)
	invalidRuntime.seedOrderRuntime = nil
	tests := []struct {
		name         string
		order        []identity.LogicalNodeKey
		replacements []*LogicalNode
		want         error
	}{
		{"无效顺序身份", []identity.LogicalNodeKey{first, {}}, []*LogicalNode{prepared}, nil},
		{"重复顺序规则", []identity.LogicalNodeKey{first, firstAlias, second}, []*LogicalNode{prepared}, ErrDuplicateRuleKey},
		{"准备节点规则重复", []identity.LogicalNodeKey{first, second}, []*LogicalNode{prepared, &duplicate}, ErrDuplicateRuleKey},
		{"缺失节点", []identity.LogicalNodeKey{first, second, third}, []*LogicalNode{prepared}, ErrLogicalNodeNotFound},
		{"部署身份不匹配", []identity.LogicalNodeKey{first, wrongPlacement}, []*LogicalNode{prepared}, ErrLogicalNodeNotFound},
		{"准备节点未列入顺序", []identity.LogicalNodeKey{first}, []*LogicalNode{prepared}, nil},
		{"空准备节点", []identity.LogicalNodeKey{first, second}, []*LogicalNode{prepared, nil}, nil},
		{"后续准备节点缺少轮次运行时", []identity.LogicalNodeKey{first, second, third}, []*LogicalNode{prepared, invalidRuntime}, nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := physical.Reconfigure(test.order, test.replacements, NewRoundRobinLogicalNodeSelector())
			if err == nil || (test.want != nil && !errors.Is(err, test.want)) {
				t.Fatalf("未按预期拒绝重配置: %v", err)
			}
			if len(physical.nodes) != 1 || physical.nodes[1] != original || len(physical.order) != 1 || physical.order[0] != 1 || physical.selector != oldSelector {
				t.Fatal("失败的重配置改变了物理节点或选择器")
			}
			if original.Len() != 1 || original.seedRound != originalRound || prepared.Len() != 1 || prepared.seedRound != preparedRound {
				t.Fatal("失败的重配置改变了原节点或准备节点")
			}
		})
	}
}
