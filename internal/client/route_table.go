// Package client implements immutable client-side routing to PhysicalNode.
package client

import (
	"fmt"

	"matchSystem/internal/common"
	"matchSystem/internal/identity"
)

type PhysicalRoute struct {
	PhysicalNodeID identity.PhysicalNodeID
	Endpoint       common.Endpoint
	Enabled        bool
}

type RuleRoute struct {
	LogicalNode    identity.LogicalNodeKey
	PhysicalNodeID identity.PhysicalNodeID
	Weight         uint32
	Enabled        bool
}

type RouteTableConfig struct {
	PhysicalNodes []PhysicalRoute
	Rules         []RuleRoute
}

// RouteTable is immutable after construction. Router access still follows the
// repository-wide single-owner goroutine contract.
type RouteTable struct {
	physicalNodes map[identity.PhysicalNodeID]PhysicalRoute
	byRule        map[identity.RuleKey][]RuleRoute
}

func NewRouteTable(config RouteTableConfig) (*RouteTable, error) {
	table := &RouteTable{
		physicalNodes: make(map[identity.PhysicalNodeID]PhysicalRoute, len(config.PhysicalNodes)),
		byRule:        make(map[identity.RuleKey][]RuleRoute),
	}
	for _, physical := range config.PhysicalNodes {
		if err := physical.PhysicalNodeID.Validate(); err != nil {
			return nil, err
		}
		if physical.Endpoint == "" {
			return nil, fmt.Errorf("Endpoint is required for PhysicalNodeID %q", physical.PhysicalNodeID)
		}
		if _, exists := table.physicalNodes[physical.PhysicalNodeID]; exists {
			return nil, fmt.Errorf("duplicate PhysicalNodeID %q", physical.PhysicalNodeID)
		}
		table.physicalNodes[physical.PhysicalNodeID] = physical
	}

	seenRuleOnPhysical := make(map[rulePhysicalKey]struct{}, len(config.Rules))
	seenLogicalNode := make(map[identity.LogicalNodeKey]identity.PhysicalNodeID, len(config.Rules))
	for _, route := range config.Rules {
		if err := route.LogicalNode.Validate(); err != nil {
			return nil, fmt.Errorf("invalid LogicalNode: %w", err)
		}
		if err := route.PhysicalNodeID.Validate(); err != nil {
			return nil, fmt.Errorf("invalid route for %s: %w", route.LogicalNode, err)
		}
		if _, exists := table.physicalNodes[route.PhysicalNodeID]; !exists {
			return nil, fmt.Errorf("PhysicalNodeID %q is not declared", route.PhysicalNodeID)
		}
		if route.Weight == 0 {
			return nil, fmt.Errorf("Weight must be positive for %s on %q", route.LogicalNode, route.PhysicalNodeID)
		}
		if existing, exists := seenLogicalNode[route.LogicalNode]; exists {
			return nil, fmt.Errorf("duplicate LogicalNodeKey %s on PhysicalNodeID %q and %q", route.LogicalNode, existing, route.PhysicalNodeID)
		}
		seenLogicalNode[route.LogicalNode] = route.PhysicalNodeID
		key := rulePhysicalKey{rule: route.LogicalNode.Rule, physical: route.PhysicalNodeID}
		if _, exists := seenRuleOnPhysical[key]; exists {
			return nil, fmt.Errorf("duplicate RuleKey %s on PhysicalNodeID %q", route.LogicalNode.Rule, route.PhysicalNodeID)
		}
		seenRuleOnPhysical[key] = struct{}{}
		table.byRule[route.LogicalNode.Rule] = append(table.byRule[route.LogicalNode.Rule], route)
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
	return t.byRule[rule]
}

type rulePhysicalKey struct {
	rule     identity.RuleKey
	physical identity.PhysicalNodeID
}
