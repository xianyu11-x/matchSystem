package client

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strconv"

	"matchSystem/internal/common"
	"matchSystem/internal/identity"
)

var (
	ErrNoRoute       = errors.New("no route available")
	ErrOwnerNotFound = errors.New("owner is not present in route table")
)

type RouteRequest = common.RouteRequest
type RouteDecision = common.RouteDecision
type ResolvedOwner = common.ResolvedOwner

// Router is single-owner mutable routing state. Replace, RouteNew, and
// ResolveOwner must be called sequentially by the same goroutine.
type Router struct {
	table *RouteTable
}

func NewRouter(table *RouteTable) (*Router, error) {
	if table == nil {
		return nil, fmt.Errorf("RouteTable is required")
	}
	return &Router{table: table}, nil
}

func (r *Router) Replace(table *RouteTable) error {
	if table == nil {
		return fmt.Errorf("RouteTable is required")
	}
	r.table = table
	return nil
}

func (r *Router) RouteNew(ctx context.Context, request RouteRequest) (RouteDecision, error) {
	if err := ctx.Err(); err != nil {
		return RouteDecision{}, err
	}
	if err := request.Rule.Validate(); err != nil {
		return RouteDecision{}, err
	}
	if request.TicketID == "" {
		return RouteDecision{}, fmt.Errorf("TicketID is required")
	}
	affinity := request.AffinityKey
	if affinity == "" {
		affinity = request.TicketID
	}

	table := r.table
	if table == nil {
		return RouteDecision{}, ErrNoRoute
	}
	var selected RuleRoute
	var selectedPhysical PhysicalRoute
	bestScore := -1.0
	for _, route := range table.ruleRoutes(request.Rule) {
		physical, ok := table.physical(route.PhysicalNodeID)
		if !ok || !route.Enabled || !physical.Enabled {
			continue
		}
		score := weightedRendezvousScore(request.Rule, affinity, route.PhysicalNodeID, route.Weight)
		if score > bestScore || score == bestScore && route.PhysicalNodeID < selected.PhysicalNodeID {
			selected = route
			selectedPhysical = physical
			bestScore = score
		}
	}
	if bestScore < 0 {
		return RouteDecision{}, ErrNoRoute
	}

	owner := identity.OwnerRef{LogicalNode: selected.LogicalNode, PhysicalNodeID: selected.PhysicalNodeID}
	return RouteDecision{
		DecisionID: decisionID(request, affinity, owner),
		Owner:      owner,
		Endpoint:   selectedPhysical.Endpoint,
	}, nil
}

func (r *Router) ResolveOwner(owner identity.OwnerRef) (ResolvedOwner, error) {
	if err := owner.Validate(); err != nil {
		return ResolvedOwner{}, err
	}
	table := r.table
	if table == nil {
		return ResolvedOwner{}, ErrOwnerNotFound
	}
	physical, ok := table.physical(owner.PhysicalNodeID)
	if !ok {
		return ResolvedOwner{}, ErrOwnerNotFound
	}
	for _, route := range table.ruleRoutes(owner.LogicalNode.Rule) {
		if route.PhysicalNodeID == owner.PhysicalNodeID && route.LogicalNode == owner.LogicalNode {
			return ResolvedOwner{Owner: owner, Endpoint: physical.Endpoint}, nil
		}
	}
	return ResolvedOwner{}, ErrOwnerNotFound
}

func weightedRendezvousScore(rule identity.RuleKey, affinity string, physical identity.PhysicalNodeID, weight uint32) float64 {
	digest := sha256.Sum256([]byte(stableParts(rule.String(), affinity, string(physical))))
	hash := binary.BigEndian.Uint64(digest[:8])
	u := (float64(hash>>12) + 0.5) / float64(uint64(1)<<52)
	return float64(weight) / -math.Log(u)
}

func decisionID(request RouteRequest, affinity string, owner identity.OwnerRef) string {
	digest := sha256.Sum256([]byte(stableParts(
		request.Rule.String(),
		request.TicketID,
		affinity,
		request.RequestID,
		owner.String(),
	)))
	return hex.EncodeToString(digest[:])
}

func stableParts(parts ...string) string {
	result := ""
	for _, part := range parts {
		result += strconv.Itoa(len(part)) + ":" + part
	}
	return result
}
