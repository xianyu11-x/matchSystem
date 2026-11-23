package simulator

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/xianyu11-x/matchSystem/pkg/client"
	"github.com/xianyu11-x/matchSystem/pkg/identity"
	"github.com/xianyu11-x/matchSystem/pkg/matchsystem"
	"github.com/xianyu11-x/matchSystem/pkg/matchsystem/fact"
)

// ErrPhysicalNodeAlreadyExists 表示待新增的物理节点 ID 已存在。
var ErrPhysicalNodeAlreadyExists = errors.New("simulator PhysicalNode already exists")

// AddPhysicalNode 增量加载一个物理节点及其规则，不改变现有节点和运行状态。
// 规则列表可以为空；所有规则必须属于新增节点。
func (s *Simulator) AddPhysicalNode(ctx context.Context, physical PhysicalNodeSpec, rules []RuleSpec) error {
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
	return s.addPhysicalNodeLocked(ctx, physical, rules)
}

// addPhysicalNodeLocked 要求调用方持有 Simulator 写锁。
func (s *Simulator) addPhysicalNodeLocked(ctx context.Context, physical PhysicalNodeSpec, rules []RuleSpec) error {
	runtime, err := s.runtimeReadLocked()
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, exists := runtime.physical[physical.ID]; exists {
		return fmt.Errorf("%w: %d", ErrPhysicalNodeAlreadyExists, physical.ID)
	}

	ownedRules := (Scenario{Rules: rules}).Clone().Rules
	for index, rule := range ownedRules {
		if rule.PhysicalNodeID != physical.ID {
			return &ValidationError{Issues: []ValidationIssue{{
				Path:    fmt.Sprintf("$.rules[%d].physicalNodeId", index),
				Code:    "PHYSICAL_NODE_MISMATCH",
				Message: fmt.Sprintf("rule belongs to PhysicalNode %d, expected %d", rule.PhysicalNodeID, physical.ID),
			}}}
		}
	}
	physical.Selector = normalizeSelector(physical.Selector)
	candidate := runtime.scenario.Clone()
	candidate.PhysicalNodes = append(candidate.PhysicalNodes, physical)
	candidate.Rules = append(candidate.Rules, ownedRules...)
	report, _ := validateScenarioForNode(candidate, &physical.ID)
	if !report.Valid {
		return report.Err()
	}

	// 暂存运行时共享观测注册表，Provider 才能在提交后读取真实的 Ticket 数据。
	staged := &simulatorRuntime{
		nodes:                     make(map[identity.PhysicalNodeID]*physicalNodeAdapter, 1),
		rules:                     make(map[logicalIndexKey]RuleSpec, len(ownedRules)),
		validators:                make(map[ownerIndexKey]*fact.Validator, len(ownedRules)),
		providers:                 make(map[logicalIndexKey]*PythonWorker, len(ownedRules)),
		providerDescriptors:       make(map[logicalIndexKey]matchsystem.ProviderDescriptorSet, len(ownedRules)),
		providerDescriptorSources: make(map[logicalIndexKey]providerDescriptorSourceSet, len(ownedRules)),
		registry:                  runtime.registry,
	}
	committed := false
	defer func() {
		if !committed {
			closeRuntime(staged)
		}
	}()
	adapter, err := createRuntimePhysicalNode(physical, ownedRules)
	if err != nil {
		return err
	}
	staged.nodes[physical.ID] = adapter
	for _, rule := range ownedRules {
		if err := loadRuntimeRule(ctx, staged, rule); err != nil {
			return err
		}
	}
	if runtime.roundActive {
		if err := adapter.BeginMatchRound(ctx, runtime.roundNow); err != nil {
			return fmt.Errorf("begin round on PhysicalNode %d: %w", physical.ID, err)
		}
	}

	physicalRoute, routes := physicalNodeRoutes(physical, ownedRules)
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := runtime.router.UpdatePhysicalNode(physicalRoute, routes); err != nil {
		return err
	}
	// 路由提交后不再执行可能失败的操作；所有变更在同一把锁下对外可见。
	runtime.scenario = candidate
	runtime.physical[physical.ID] = physical
	runtime.nodes[physical.ID] = adapter
	for key, rule := range staged.rules {
		runtime.rules[key] = rule
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
	committed = true
	runtime.appendEvent("physical_node_added", map[string]any{"physicalNodeId": strconv.FormatUint(uint64(physical.ID), 10)})
	return nil
}

// physicalNodeRoutes 使用场景部署的统一身份构造该物理节点的路由。
func physicalNodeRoutes(physical PhysicalNodeSpec, rules []RuleSpec) (client.PhysicalRoute, []client.RuleRoute) {
	physicalRoute := client.PhysicalRoute{PhysicalNodeID: physical.ID, Endpoint: physical.Endpoint, Enabled: physical.Enabled}
	routes := make([]client.RuleRoute, 0, len(rules))
	for _, rule := range rules {
		routes = append(routes, client.RuleRoute{LogicalNode: rule.LogicalNode, PhysicalNodeID: rule.PhysicalNodeID, Weight: rule.Weight, Enabled: rule.Enabled})
	}
	return physicalRoute, routes
}

// ClearPhysicalNodeTickets 清空指定物理节点的等待 Ticket，保留部署、轮次和匹配历史。
func (s *Simulator) ClearPhysicalNodeTickets(ctx context.Context, id identity.PhysicalNodeID) (int, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if err := id.Validate(); err != nil {
		return 0, err
	}
	if s == nil {
		return 0, ErrSimulatorClosed
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	runtime, err := s.runtimeReadLocked()
	if err != nil {
		return 0, err
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	adapter := runtime.nodes[id]
	if adapter == nil {
		return 0, fmt.Errorf("%w: %d", ErrUnknownNode, id)
	}
	removed, err := adapter.ClearTickets(ctx)
	if err != nil {
		return 0, err
	}
	runtime.registry.removePhysicalTickets(id)
	runtime.appendEvent("physical_node_tickets_cleared", map[string]any{
		"physicalNodeId": strconv.FormatUint(uint64(id), 10),
		"removedTickets": removed,
	})
	return removed, nil
}

// RemovePhysicalNode 移除物理节点及其等待 Ticket，保留完成的匹配记录。
// 对不存在的非空节点重复删除是幂等操作。
func (s *Simulator) RemovePhysicalNode(ctx context.Context, id identity.PhysicalNodeID) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := id.Validate(); err != nil {
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
	if err := ctx.Err(); err != nil {
		return err
	}
	adapter, exists := runtime.nodes[id]
	if !exists {
		return nil
	}

	candidate := runtime.scenario.Clone()
	physicalNodes := make([]PhysicalNodeSpec, 0, len(candidate.PhysicalNodes))
	for _, node := range candidate.PhysicalNodes {
		if node.ID != id {
			physicalNodes = append(physicalNodes, node)
		}
	}
	candidate.PhysicalNodes = physicalNodes
	rules := make([]RuleSpec, 0, len(candidate.Rules))
	removedRules := make([]RuleSpec, 0)
	for _, rule := range candidate.Rules {
		if rule.PhysicalNodeID == id {
			removedRules = append(removedRules, rule)
		} else {
			rules = append(rules, rule)
		}
	}
	candidate.Rules = rules
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := runtime.router.UpdatePhysicalNode(client.PhysicalRoute{PhysicalNodeID: id}, nil); err != nil {
		return err
	}
	// 先等待可能仍在执行的命令退出，再清理其 Provider 和观测数据。
	adapter.close()
	for _, rule := range removedRules {
		if worker := runtime.providers[logicalIndex(rule.LogicalNode)]; worker != nil {
			worker.Close()
		}
		delete(runtime.providers, logicalIndex(rule.LogicalNode))
		delete(runtime.providerDescriptors, logicalIndex(rule.LogicalNode))
		delete(runtime.providerDescriptorSources, logicalIndex(rule.LogicalNode))
		delete(runtime.validators, ownerIndex(identity.OwnerRef{LogicalNode: rule.LogicalNode, PhysicalNodeID: id}))
		delete(runtime.rules, logicalIndex(rule.LogicalNode))
	}
	removedTickets := runtime.registry.removePhysicalTickets(id)
	delete(runtime.physical, id)
	delete(runtime.nodes, id)
	runtime.scenario = candidate
	runtime.appendEvent("physical_node_removed", map[string]any{"physicalNodeId": strconv.FormatUint(uint64(id), 10), "removedTickets": removedTickets})
	return nil
}

func (r *ObservationRegistry) removePhysicalTickets(id identity.PhysicalNodeID) int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	removed := 0
	for key := range r.tickets {
		if key.owner.physicalID == id {
			delete(r.tickets, key)
			removed++
		}
	}
	return removed
}
