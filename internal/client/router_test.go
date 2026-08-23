package client

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"matchSystem/internal/common"
	"matchSystem/internal/identity"
)

func TestRouterSelectsPhysicalNodeDeterministically(t *testing.T) {
	rule := identity.RuleKey{Namespace: "prod", RuleID: "ranked"}
	table := mustRouteTable(t, RouteTableConfig{
		PhysicalNodes: []PhysicalRoute{
			{PhysicalNodeID: "physical-a", Endpoint: common.Endpoint("a:9000"), Enabled: true},
			{PhysicalNodeID: "physical-b", Endpoint: common.Endpoint("b:9000"), Enabled: true},
		},
		Rules: []RuleRoute{
			{LogicalNode: identity.LogicalNodeKey{Rule: rule, PlacementID: "p1"}, PhysicalNodeID: "physical-a", Weight: 1, Enabled: true},
			{LogicalNode: identity.LogicalNodeKey{Rule: rule, PlacementID: "p2"}, PhysicalNodeID: "physical-b", Weight: 1, Enabled: true},
		},
	})
	router, err := NewRouter(table)
	if err != nil {
		t.Fatal(err)
	}
	request := RouteRequest{Rule: rule, TicketID: "ticket-1", AffinityKey: "party-1", RequestID: "request-1"}
	first, err := router.RouteNew(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := router.RouteNew(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("route is not deterministic: %#v != %#v", first, second)
	}
	resolved, err := router.ResolveOwner(first.Owner)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Endpoint != first.Endpoint {
		t.Fatalf("resolved endpoint %q != decision endpoint %q", resolved.Endpoint, first.Endpoint)
	}
}

func TestRouterReplaceRunsOnOwnerCommandBoundary(t *testing.T) {
	rule := identity.RuleKey{RuleID: "ranked"}
	logical := identity.LogicalNodeKey{Rule: rule, PlacementID: "p1"}
	oldTable := mustRouteTable(t, RouteTableConfig{
		PhysicalNodes: []PhysicalRoute{{PhysicalNodeID: "physical-a", Endpoint: "a:9000", Enabled: true}},
		Rules:         []RuleRoute{{LogicalNode: logical, PhysicalNodeID: "physical-a", Weight: 1, Enabled: true}},
	})
	newTable := mustRouteTable(t, RouteTableConfig{
		PhysicalNodes: []PhysicalRoute{{PhysicalNodeID: "physical-b", Endpoint: "b:9000", Enabled: true}},
		Rules:         []RuleRoute{{LogicalNode: logical, PhysicalNodeID: "physical-b", Weight: 1, Enabled: true}},
	})
	router, err := NewRouter(oldTable)
	if err != nil {
		t.Fatal(err)
	}
	request := RouteRequest{Rule: rule, TicketID: "ticket-1"}
	before, err := router.RouteNew(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if err := router.Replace(newTable); err != nil {
		t.Fatal(err)
	}
	after, err := router.RouteNew(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if before.Owner.PhysicalNodeID != "physical-a" || after.Owner.PhysicalNodeID != "physical-b" {
		t.Fatalf("unexpected sequential replacement: before=%#v after=%#v", before, after)
	}
}

func TestDecisionIDUsesEffectiveAffinityKey(t *testing.T) {
	rule := identity.RuleKey{RuleID: "ranked"}
	table := mustRouteTable(t, RouteTableConfig{
		PhysicalNodes: []PhysicalRoute{{PhysicalNodeID: "physical-a", Endpoint: "a:9000", Enabled: true}},
		Rules:         []RuleRoute{{LogicalNode: identity.LogicalNodeKey{Rule: rule, PlacementID: "p1"}, PhysicalNodeID: "physical-a", Weight: 1, Enabled: true}},
	})
	router, _ := NewRouter(table)
	implicit, err := router.RouteNew(context.Background(), RouteRequest{Rule: rule, TicketID: "ticket-1", RequestID: "request-1"})
	if err != nil {
		t.Fatal(err)
	}
	explicit, err := router.RouteNew(context.Background(), RouteRequest{Rule: rule, TicketID: "ticket-1", AffinityKey: "ticket-1", RequestID: "request-1"})
	if err != nil {
		t.Fatal(err)
	}
	if implicit.DecisionID != explicit.DecisionID || implicit.Owner != explicit.Owner {
		t.Fatalf("equivalent effective affinity produced different decisions: %#v != %#v", implicit, explicit)
	}
}

func TestRouterSkipsDisabledPhysicalNode(t *testing.T) {
	rule := identity.RuleKey{RuleID: "ranked"}
	table := mustRouteTable(t, RouteTableConfig{
		PhysicalNodes: []PhysicalRoute{{PhysicalNodeID: "physical-a", Endpoint: "a:9000", Enabled: false}},
		Rules:         []RuleRoute{{LogicalNode: identity.LogicalNodeKey{Rule: rule, PlacementID: "p1"}, PhysicalNodeID: "physical-a", Weight: 1, Enabled: true}},
	})
	router, _ := NewRouter(table)
	_, err := router.RouteNew(context.Background(), RouteRequest{Rule: rule, TicketID: "ticket-1"})
	if !errors.Is(err, ErrNoRoute) {
		t.Fatalf("expected ErrNoRoute, got %v", err)
	}
}

func TestRouterCanDistributeSameRuleAcrossPhysicalNodes(t *testing.T) {
	rule := identity.RuleKey{RuleID: "ranked"}
	table := mustRouteTable(t, RouteTableConfig{
		PhysicalNodes: []PhysicalRoute{
			{PhysicalNodeID: "physical-a", Endpoint: "a:9000", Enabled: true},
			{PhysicalNodeID: "physical-b", Endpoint: "b:9000", Enabled: true},
		},
		Rules: []RuleRoute{
			{LogicalNode: identity.LogicalNodeKey{Rule: rule, PlacementID: "p1"}, PhysicalNodeID: "physical-a", Weight: 1, Enabled: true},
			{LogicalNode: identity.LogicalNodeKey{Rule: rule, PlacementID: "p2"}, PhysicalNodeID: "physical-b", Weight: 1, Enabled: true},
		},
	})
	router, _ := NewRouter(table)
	selected := map[identity.PhysicalNodeID]bool{}
	for i := 0; i < 128; i++ {
		suffix := strconv.Itoa(i)
		decision, err := router.RouteNew(context.Background(), RouteRequest{
			Rule: rule, TicketID: "ticket-" + suffix, AffinityKey: "affinity-" + suffix,
		})
		if err != nil {
			t.Fatal(err)
		}
		selected[decision.Owner.PhysicalNodeID] = true
	}
	if !selected["physical-a"] || !selected["physical-b"] {
		t.Fatalf("expected both PhysicalNodes to receive routes, got %v", selected)
	}
}

func TestRouteTableRejectsDuplicateRuleOnPhysicalNode(t *testing.T) {
	rule := identity.RuleKey{RuleID: "ranked"}
	_, err := NewRouteTable(RouteTableConfig{
		PhysicalNodes: []PhysicalRoute{{PhysicalNodeID: "physical-a", Endpoint: "a:9000", Enabled: true}},
		Rules: []RuleRoute{
			{LogicalNode: identity.LogicalNodeKey{Rule: rule, PlacementID: "p1"}, PhysicalNodeID: "physical-a", Weight: 1, Enabled: true},
			{LogicalNode: identity.LogicalNodeKey{Rule: rule, PlacementID: "p2"}, PhysicalNodeID: "physical-a", Weight: 1, Enabled: true},
		},
	})
	if err == nil {
		t.Fatal("expected duplicate local RuleKey to be rejected")
	}
}

func TestRouteTableRejectsLogicalNodeOnMultiplePhysicalNodes(t *testing.T) {
	rule := identity.RuleKey{RuleID: "ranked"}
	logical := identity.LogicalNodeKey{Rule: rule, PlacementID: "p1"}
	_, err := NewRouteTable(RouteTableConfig{
		PhysicalNodes: []PhysicalRoute{
			{PhysicalNodeID: "physical-a", Endpoint: "a:9000", Enabled: true},
			{PhysicalNodeID: "physical-b", Endpoint: "b:9000", Enabled: true},
		},
		Rules: []RuleRoute{
			{LogicalNode: logical, PhysicalNodeID: "physical-a", Weight: 1, Enabled: true},
			{LogicalNode: logical, PhysicalNodeID: "physical-b", Weight: 1, Enabled: true},
		},
	})
	if err == nil {
		t.Fatal("expected duplicate LogicalNodeKey to be rejected")
	}
}

func mustRouteTable(t *testing.T, config RouteTableConfig) *RouteTable {
	t.Helper()
	table, err := NewRouteTable(config)
	if err != nil {
		t.Fatal(err)
	}
	return table
}
