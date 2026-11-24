package simulator

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"

	"github.com/xianyu11-x/matchSystem/pkg/identity"
	"github.com/xianyu11-x/matchSystem/pkg/matchsystem"
	"github.com/xianyu11-x/matchSystem/pkg/matchsystem/fact"
)

// RuleDeploymentSpec 描述从规则库选择的单个 LogicalNode 部署。
// 已部署的相同身份默认保留原规则快照；ApplyLatest 显式采用规则库当前版本。
type RuleDeploymentSpec struct {
	LogicalNode    identity.LogicalNodeKey `json:"logicalNode"`
	Weight         uint32                  `json:"weight"`
	Enabled        bool                    `json:"enabled"`
	TickFacts      FactSnapshot            `json:"tickFacts,omitempty"`
	KeepTickFacts  bool                    `json:"keepTickFacts,omitempty"`
	PythonProvider *PythonProviderConfig   `json:"pythonProvider,omitempty"`
	ApplyLatest    bool                    `json:"applyLatest,omitempty"`
}

type ruleDeploymentJSON struct {
	LogicalNode    json.RawMessage       `json:"logicalNode"`
	Weight         uint32                `json:"weight"`
	Enabled        *bool                 `json:"enabled"`
	TickFacts      FactSnapshot          `json:"tickFacts,omitempty"`
	KeepTickFacts  bool                  `json:"keepTickFacts,omitempty"`
	PythonProvider *PythonProviderConfig `json:"pythonProvider,omitempty"`
	ApplyLatest    bool                  `json:"applyLatest,omitempty"`
}

func (deployment *RuleDeploymentSpec) UnmarshalJSON(data []byte) error {
	if deployment == nil {
		return fmt.Errorf("RuleDeploymentSpec receiver is nil")
	}
	var wire ruleDeploymentJSON
	if err := decodeStrictJSON(data, &wire); err != nil {
		return fmt.Errorf("decode RuleDeploymentSpec: %w", err)
	}
	if wire.Enabled == nil {
		return fmt.Errorf("decode RuleDeploymentSpec: enabled is required")
	}
	var key identity.LogicalNodeKey
	if len(wire.LogicalNode) > 0 {
		var err error
		key, err = DecodeLogicalNodeKeyJSON(wire.LogicalNode)
		if err != nil {
			return fmt.Errorf("decode RuleDeploymentSpec logicalNode: %w", err)
		}
	}
	*deployment = RuleDeploymentSpec{
		LogicalNode: key, Weight: wire.Weight,
		Enabled: *wire.Enabled, TickFacts: wire.TickFacts.clone(), KeepTickFacts: wire.KeepTickFacts,
		PythonProvider: clonePythonProviderConfig(wire.PythonProvider), ApplyLatest: wire.ApplyLatest,
	}
	return nil
}

// AddPhysicalNodeFromLibrary 在规则库快照中解析规则并增量新增物理节点。
func (s *Simulator) AddPhysicalNodeFromLibrary(ctx context.Context, physical PhysicalNodeSpec, deployments []RuleDeploymentSpec) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil {
		return ErrSimulatorClosed
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.runtimeReadLocked(); err != nil {
		return err
	}
	rules, err := s.libraryDeploymentsLocked(physical.ID, deployments, nil)
	if err != nil {
		return err
	}
	return s.addPhysicalNodeLocked(ctx, physical, rules)
}

// libraryDeploymentsLocked 按需复制规则 JSON；调用方必须持有写锁。
func (s *Simulator) libraryDeploymentsLocked(physicalID identity.PhysicalNodeID, deployments []RuleDeploymentSpec, previous map[logicalIndexKey]RuleSpec) ([]RuleSpec, error) {
	library := s.ruleLibraryIndex
	rules := make([]RuleSpec, 0, len(deployments))
	for index, deployment := range deployments {
		old, exists := previous[logicalIndex(deployment.LogicalNode)]
		if deployment.KeepTickFacts && !exists {
			return nil, &ValidationError{Issues: []ValidationIssue{{
				Path: fmt.Sprintf("$.deployments[%d].keepTickFacts", index), Code: "KEEP_TICK_FACTS_REQUIRES_EXISTING",
				Message: "keepTickFacts requires an existing LogicalNode deployment",
			}}}
		}
		rule := old
		if !exists || deployment.ApplyLatest {
			raw, found := library[deployment.LogicalNode.Rule.RuleID]
			if !found {
				return nil, &ValidationError{Issues: []ValidationIssue{{
					Path: fmt.Sprintf("$.deployments[%d].logicalNode.rule", index), Code: "RULE_NOT_IN_LIBRARY",
					Message: fmt.Sprintf("RuleKey %s is not present in the rule library", deployment.LogicalNode.Rule),
				}}}
			}
			rule.RuleJSON = append(json.RawMessage(nil), raw...)
		}
		rule.LogicalNode = deployment.LogicalNode
		rule.PhysicalNodeID = physicalID
		rule.Weight = deployment.Weight
		rule.Enabled = deployment.Enabled
		if deployment.KeepTickFacts {
			rule.TickFacts = old.TickFacts.clone()
		} else {
			rule.TickFacts = deployment.TickFacts.clone()
		}
		rule.PythonProvider = clonePythonProviderConfig(deployment.PythonProvider)
		rules = append(rules, rule)
	}
	return rules, nil
}

// sameDeployedRule 忽略仅影响路由或调度的权重、启用状态和端点。
func sameDeployedRule(previous, next RuleSpec) bool {
	if logicalIndex(previous.LogicalNode) != logicalIndex(next.LogicalNode) || previous.PhysicalNodeID != next.PhysicalNodeID ||
		!reflect.DeepEqual(previous.TickFacts.clone(), next.TickFacts.clone()) ||
		!reflect.DeepEqual(previous.PythonProvider, next.PythonProvider) {
		return false
	}
	if bytes.Equal(previous.RuleJSON, next.RuleJSON) {
		return true
	}
	oldRule, oldErr := matchsystem.CompileRuleJSON(previous.RuleJSON)
	newRule, newErr := matchsystem.CompileRuleJSON(next.RuleJSON)
	return oldErr == nil && newErr == nil && oldRule.Fingerprint() == newRule.Fingerprint()
}

// selectorChanged 仅在算法或加权表变动时替换调度器，保留其他情况下的调度游标。
func selectorChanged(oldPhysical, newPhysical PhysicalNodeSpec, oldRules, newRules []RuleSpec) bool {
	oldKind, newKind := normalizeSelector(oldPhysical.Selector), normalizeSelector(newPhysical.Selector)
	if oldKind != newKind {
		return true
	}
	if newKind != SelectorWeighted {
		return false
	}
	oldWeights, newWeights := make(map[identity.RuleID]uint32), make(map[identity.RuleID]uint32)
	for _, rule := range oldRules {
		oldWeights[rule.LogicalNode.Rule.RuleID] = rule.Weight
	}
	for _, rule := range newRules {
		newWeights[rule.LogicalNode.Rule.RuleID] = rule.Weight
	}
	return !reflect.DeepEqual(oldWeights, newWeights)
}

// UpdatePhysicalNodeFromLibrary 原子更新单个物理节点。未变化的 LogicalNode 和队列
// 保持原实例；移除或替换的节点仅丢弃自身等待队列，不改变 Match 历史。
func (s *Simulator) UpdatePhysicalNodeFromLibrary(ctx context.Context, physical PhysicalNodeSpec, deployments []RuleDeploymentSpec) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil {
		return ErrSimulatorClosed
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	runtime, err := s.runtimeReadLocked()
	if err != nil {
		return err
	}
	oldPhysical, exists := runtime.physical[physical.ID]
	if !exists {
		return fmt.Errorf("%w: %d", ErrUnknownNode, physical.ID)
	}
	previous := make(map[logicalIndexKey]RuleSpec)
	oldRules := make([]RuleSpec, 0)
	for _, rule := range runtime.scenario.Rules {
		if rule.PhysicalNodeID == physical.ID {
			previous[logicalIndex(rule.LogicalNode)] = rule
			oldRules = append(oldRules, rule)
		}
	}
	rules, err := s.libraryDeploymentsLocked(physical.ID, deployments, previous)
	if err != nil {
		return err
	}
	physical.Selector = normalizeSelector(physical.Selector)
	candidate := runtime.scenario.Clone()
	for i := range candidate.PhysicalNodes {
		if candidate.PhysicalNodes[i].ID == physical.ID {
			candidate.PhysicalNodes[i] = physical
		}
	}
	// 保留现有路由顺序，避免无关节点因一次编辑被重新排序。
	remaining := make(map[logicalIndexKey]RuleSpec, len(rules))
	for _, rule := range rules {
		remaining[logicalIndex(rule.LogicalNode)] = rule
	}
	nextRules := make([]RuleSpec, 0, len(candidate.Rules)+len(rules))
	for _, old := range candidate.Rules {
		if old.PhysicalNodeID != physical.ID {
			nextRules = append(nextRules, old)
		} else if next, ok := remaining[logicalIndex(old.LogicalNode)]; ok {
			nextRules = append(nextRules, next)
			delete(remaining, logicalIndex(old.LogicalNode))
		}
	}
	for _, rule := range rules {
		if _, ok := remaining[logicalIndex(rule.LogicalNode)]; ok {
			nextRules = append(nextRules, rule)
			delete(remaining, logicalIndex(rule.LogicalNode))
		}
	}
	// 不使用 map 去重：重复部署必须交给场景校验显式拒绝。
	if len(rules) != len(deployments) || len(remaining) != 0 || len(rules) != countRulesForPhysical(nextRules, physical.ID) {
		return &ValidationError{Issues: []ValidationIssue{{Path: "$.deployments", Code: "DUPLICATE_LOGICAL_NODE", Message: "duplicate LogicalNode deployment"}}}
	}
	candidate.Rules = nextRules
	// 原节点的 Provider 已完成握手。只为新增或更改的节点进行实际握手。
	var skipProviders identity.PhysicalNodeID
	report := validateScenarioForNode(candidate, &skipProviders)
	if !report.Valid {
		return report.Err()
	}
	var selector matchsystem.LogicalNodeSelector
	if selectorChanged(oldPhysical, physical, oldRules, rules) {
		selector, err = selectorForPhysical(physical.ID, physical.Selector, rules)
		if err != nil {
			return err
		}
	}
	staged := &simulatorRuntime{
		validators:                make(map[ownerIndexKey]*fact.Validator),
		providers:                 make(map[logicalIndexKey]*PythonWorker),
		providerDescriptors:       make(map[logicalIndexKey]matchsystem.ProviderDescriptorSet),
		providerDescriptorSources: make(map[logicalIndexKey]providerDescriptorSourceSet), registry: runtime.registry,
		replayLog: runtime.replayLog,
	}
	committed := false
	defer func() {
		if !committed {
			closeRuntime(staged)
		}
	}()
	order := make([]identity.LogicalNodeKey, 0, len(rules))
	replacements := make([]*matchsystem.LogicalNode, 0, len(rules))
	unchanged := make(map[logicalIndexKey]bool, len(rules))
	for _, rule := range rules {
		order = append(order, rule.LogicalNode)
		if old, ok := previous[logicalIndex(rule.LogicalNode)]; ok && sameDeployedRule(old, rule) {
			unchanged[logicalIndex(rule.LogicalNode)] = true
			continue
		}
		if err := loadRuntimeRuleWith(ctx, staged, rule, func(spec matchsystem.LogicalNodeSpec) (matchsystem.ProviderDescriptorSet, error) {
			if err := ctx.Err(); err != nil {
				return matchsystem.ProviderDescriptorSet{}, err
			}
			node, err := matchsystem.NewLogicalNode(spec)
			if err != nil {
				return matchsystem.ProviderDescriptorSet{}, runtimeRuleLoadError(staged, rule, err)
			}
			replacements = append(replacements, node)
			return node.ProviderDescriptors(), nil
		}); err != nil {
			return err
		}
	}
	// 未替换节点保留当前状态；新节点在提交时从 Ready、空队列开始。
	adapter := runtime.nodes[physical.ID]
	currentSnapshot, err := adapter.RouteSnapshot(ctx)
	if err != nil {
		return err
	}
	currentDescriptors := make(map[logicalIndexKey]matchsystem.LogicalNodeDescriptor, len(currentSnapshot.LogicalNodes))
	for _, descriptor := range currentSnapshot.LogicalNodes {
		currentDescriptors[logicalIndex(descriptor.Key)] = descriptor
	}
	predicted := matchsystem.PhysicalNodeRouteSnapshot{PhysicalNodeID: physical.ID, LogicalNodes: make([]matchsystem.LogicalNodeDescriptor, 0, len(rules))}
	for _, rule := range rules {
		index := logicalIndex(rule.LogicalNode)
		if unchanged[index] {
			descriptor, exists := currentDescriptors[index]
			if !exists {
				return fmt.Errorf("%w: %s", matchsystem.ErrLogicalNodeNotFound, rule.LogicalNode)
			}
			predicted.LogicalNodes = append(predicted.LogicalNodes, descriptor)
		} else {
			predicted.LogicalNodes = append(predicted.LogicalNodes, matchsystem.LogicalNodeDescriptor{Key: rule.LogicalNode, State: matchsystem.LogicalNodeReady})
		}
	}
	physicalRoute, routes, err := physicalSnapshotRoutes(physical, rules, predicted)
	if err != nil {
		return err
	}
	// 先在临时路由器校验快照，节点提交失败时保留原路由。
	nextRouter := *runtime.router
	if err := nextRouter.UpdatePhysicalNode(physicalRoute, routes); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// 提交命令入队后仍检查调用上下文；一旦开始发布就等待确定结果，
	// 防止上下文取消导致节点已修改而调用方误以为失败。
	_, err = adapter.call(context.Background(), func(node *matchsystem.PhysicalNode) (any, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return nil, node.Reconfigure(order, replacements, selector)
	})
	if err != nil {
		return err
	}
	runtime.router = &nextRouter
	removedTickets := 0
	for _, old := range oldRules {
		if unchanged[logicalIndex(old.LogicalNode)] {
			continue
		}
		owner := identity.OwnerRef{LogicalNode: old.LogicalNode, PhysicalNodeID: physical.ID}
		removedTickets += runtime.registry.removeOwnerTickets(owner)
		if worker := runtime.providers[logicalIndex(old.LogicalNode)]; worker != nil {
			worker.Close()
		}
		delete(runtime.providers, logicalIndex(old.LogicalNode))
		delete(runtime.providerDescriptors, logicalIndex(old.LogicalNode))
		delete(runtime.providerDescriptorSources, logicalIndex(old.LogicalNode))
		delete(runtime.validators, ownerIndex(owner))
		delete(runtime.rules, logicalIndex(old.LogicalNode))
	}
	for _, rule := range rules {
		runtime.rules[logicalIndex(rule.LogicalNode)] = rule
	}
	for owner, validator := range staged.validators {
		runtime.validators[owner] = validator
	}
	for key, worker := range staged.providers {
		runtime.providers[key] = worker
	}
	for key, descriptors := range staged.providerDescriptors {
		runtime.providerDescriptors[key] = descriptors
	}
	for key, sources := range staged.providerDescriptorSources {
		runtime.providerDescriptorSources[key] = sources
	}
	runtime.scenario = candidate
	runtime.physical[physical.ID] = physical
	committed = true
	s.scheduleRouteTimerLocked(runtime, physical.ID)
	runtime.appendEvent("physical_node_updated", map[string]any{"physicalNodeId": strconv.FormatUint(uint64(physical.ID), 10), "removedTickets": removedTickets})
	return nil
}

func countRulesForPhysical(rules []RuleSpec, id identity.PhysicalNodeID) int {
	count := 0
	for _, rule := range rules {
		if rule.PhysicalNodeID == id {
			count++
		}
	}
	return count
}

func (r *ObservationRegistry) removeOwnerTickets(owner identity.OwnerRef) int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	removed := 0
	for key := range r.tickets {
		if key.owner == ownerIndex(owner) {
			delete(r.tickets, key)
			removed++
		}
	}
	return removed
}
