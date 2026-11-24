// Package client 实现到 PhysicalNode 的不可变客户端路由表和路由决策。
package client

import (
	"fmt"

	"github.com/xianyu11-x/matchSystem/pkg/common"
	"github.com/xianyu11-x/matchSystem/pkg/identity"
)

// RuleKey、LogicalNodeKey 和 OwnerRef 与匹配核心共享完整身份。
type RuleKey = identity.RuleKey
type LogicalNodeKey = identity.LogicalNodeKey
type OwnerRef = identity.OwnerRef

type PhysicalRoute struct {
	PhysicalNodeID identity.PhysicalNodeID
	Endpoint       common.Endpoint
	Enabled        bool
}

type RuleRoute struct {
	LogicalNode    LogicalNodeKey
	PhysicalNodeID identity.PhysicalNodeID
	Weight         uint32
	Enabled        bool
	// TicketCount 是宿主上报的逻辑部署等待队列快照，不会随选路自动递增。
	TicketCount int
}

type RouteTableConfig struct {
	PhysicalNodes []PhysicalRoute
	Rules         []RuleRoute
}

// RouteTable 创建后不可变；访问 Router 仍须遵循单一所有者协程约定。
type RouteTable struct {
	physicalNodes map[identity.PhysicalNodeID]PhysicalRoute
	byRule        map[uint64][]RuleRoute
	byLogical     map[logicalRouteKey]RuleRoute
}

func NewRouteTable(config RouteTableConfig) (*RouteTable, error) {
	table := &RouteTable{
		physicalNodes: make(map[identity.PhysicalNodeID]PhysicalRoute, len(config.PhysicalNodes)),
		byRule:        make(map[uint64][]RuleRoute),
		byLogical:     make(map[logicalRouteKey]RuleRoute, len(config.Rules)),
	}
	for _, physical := range config.PhysicalNodes {
		if physical.PhysicalNodeID == 0 {
			return nil, fmt.Errorf("PhysicalNodeID must be positive")
		}
		if physical.Endpoint == "" {
			return nil, fmt.Errorf("Endpoint is required for PhysicalNodeID %d", physical.PhysicalNodeID)
		}
		if _, exists := table.physicalNodes[physical.PhysicalNodeID]; exists {
			return nil, fmt.Errorf("duplicate PhysicalNodeID %d", physical.PhysicalNodeID)
		}
		table.physicalNodes[physical.PhysicalNodeID] = physical
	}

	seenRuleOnPhysical := make(map[rulePhysicalKey]struct{}, len(config.Rules))
	for _, route := range config.Rules {
		if err := route.LogicalNode.Validate(); err != nil {
			return nil, fmt.Errorf("invalid LogicalNode: %w", err)
		}
		if route.PhysicalNodeID == 0 {
			return nil, fmt.Errorf("invalid route for %+v: PhysicalNodeID must be positive", route.LogicalNode)
		}
		if _, exists := table.physicalNodes[route.PhysicalNodeID]; !exists {
			return nil, fmt.Errorf("PhysicalNodeID %d is not declared", route.PhysicalNodeID)
		}
		if route.Weight == 0 {
			return nil, fmt.Errorf("Weight must be positive for %+v on %d", route.LogicalNode, route.PhysicalNodeID)
		}
		if route.TicketCount < 0 {
			return nil, fmt.Errorf("TicketCount must be non-negative for %+v on %d", route.LogicalNode, route.PhysicalNodeID)
		}
		logical := logicalRouteKey{ruleID: route.LogicalNode.Rule.RuleID, placement: route.LogicalNode.PlacementID}
		if existing, exists := table.byLogical[logical]; exists {
			return nil, fmt.Errorf("duplicate LogicalNodeKey %+v on PhysicalNodeID %d and %d", route.LogicalNode, existing.PhysicalNodeID, route.PhysicalNodeID)
		}
		key := rulePhysicalKey{ruleID: route.LogicalNode.Rule.RuleID, physical: route.PhysicalNodeID}
		if _, exists := seenRuleOnPhysical[key]; exists {
			return nil, fmt.Errorf("duplicate RuleKey %+v on PhysicalNodeID %d", route.LogicalNode.Rule, route.PhysicalNodeID)
		}
		seenRuleOnPhysical[key] = struct{}{}
		table.byLogical[logical] = route
		table.byRule[logical.ruleID] = append(table.byRule[logical.ruleID], route)
	}
	return table, nil
}

func (t *RouteTable) physical(id identity.PhysicalNodeID) (PhysicalRoute, bool) {
	if t == nil {
		return PhysicalRoute{}, false
	}
	entry, ok := t.physicalNodes[id]
	return entry, ok
}

func (t *RouteTable) ruleRoutes(rule identity.RuleKey) []RuleRoute {
	if t == nil {
		return nil
	}
	return t.byRule[rule.RuleID]
}

func (t *RouteTable) logicalRoute(key LogicalNodeKey) (RuleRoute, bool) {
	if t == nil {
		return RuleRoute{}, false
	}
	entry, ok := t.byLogical[logicalRouteKey{ruleID: key.Rule.RuleID, placement: key.PlacementID}]
	return entry, ok
}

type rulePhysicalKey struct {
	ruleID   uint64
	physical identity.PhysicalNodeID
}

type logicalRouteKey struct {
	ruleID    uint64
	placement identity.PlacementID
}
