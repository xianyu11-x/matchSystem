package client

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/xianyu11-x/matchSystem/pkg/common"
)

var (
	ErrNoRoute               = errors.New("no route available")
	ErrOwnerNotFound         = errors.New("owner is not present in route table")
	ErrInvalidRouteSelection = errors.New("selector returned an ineligible owner")
)

// RouteRequest 描述新 Ticket 的路由请求；AffinityKey 为零时使用 TicketID。
type RouteRequest = common.RouteRequest

// RouteDecision 保存确定的路由目标及其端点。DecisionID 是可能碰撞的确定性摘要。
type RouteDecision = common.RouteDecision

// ResolvedOwner 是从路由表解析出的目标及其当前端点。
type ResolvedOwner = common.ResolvedOwner

// RouterOption 配置 Router。
type RouterOption func(*Router) error

// WithRouteSelector 设置自定义选择器。
func WithRouteSelector(selector RouteSelector) RouterOption {
	return func(router *Router) error {
		if selector == nil {
			return fmt.Errorf("RouteSelector is nil")
		}
		router.selector = selector
		return nil
	}
}

// Router 由单一协程持有；Replace、UpdatePhysicalNode、RouteNew 和 ResolveOwner 必须串行调用。
// 零值采用默认选择器，可通过 Replace 或 UpdatePhysicalNode 设置路由表。
type Router struct {
	table    *RouteTable
	selector RouteSelector
}

func NewRouter(table *RouteTable, options ...RouterOption) (*Router, error) {
	if table == nil {
		return nil, fmt.Errorf("RouteTable is required")
	}
	router := &Router{table: table, selector: WeightedRendezvousSelector{}}
	for _, option := range options {
		if option == nil {
			return nil, fmt.Errorf("Router option is nil")
		}
		if err := option(router); err != nil {
			return nil, err
		}
	}
	return router, nil
}

func (r *Router) Replace(table *RouteTable) error {
	if table == nil {
		return fmt.Errorf("RouteTable is required")
	}
	r.table = table
	return nil
}

// UpdatePhysicalNode 原子新增或替换物理节点及其全部规则路由；空规则列表表示节点下线，
// 只需提供 PhysicalNodeID，并移除节点及其全部规则路由。不存在的节点下线是幂等操作。
// 已有规则在候选列表中保持原位置，新增节点或规则追加到对应规则的候选列表末尾。
func (r *Router) UpdatePhysicalNode(physical PhysicalRoute, routes []RuleRoute) error {
	if physical.PhysicalNodeID == 0 {
		return fmt.Errorf("PhysicalNodeID must be positive")
	}
	original := r.table
	if _, ok := original.physical(physical.PhysicalNodeID); !ok && len(routes) == 0 {
		return nil
	}
	for _, route := range routes {
		if route.PhysicalNodeID != physical.PhysicalNodeID {
			return fmt.Errorf("route for %+v references PhysicalNodeID %d, expected %d", route.LogicalNode, route.PhysicalNodeID, physical.PhysicalNodeID)
		}
	}

	var config RouteTableConfig
	if original != nil {
		config.PhysicalNodes = make([]PhysicalRoute, 0, len(original.physicalNodes)+1)
		config.Rules = make([]RuleRoute, 0, len(original.byLogical)+len(routes))
		for id, existing := range original.physicalNodes {
			if id != physical.PhysicalNodeID {
				config.PhysicalNodes = append(config.PhysicalNodes, existing)
			}
		}
		for _, ruleRoutes := range original.byRule {
			for _, route := range ruleRoutes {
				if route.PhysicalNodeID != physical.PhysicalNodeID {
					config.Rules = append(config.Rules, route)
				}
			}
		}
	}
	if len(routes) > 0 {
		config.PhysicalNodes = append(config.PhysicalNodes, physical)
	}
	config.Rules = append(config.Rules, routes...)

	updated, err := NewRouteTable(config)
	if err != nil {
		return err
	}
	// 构造器将新路由追加到末尾；对于已有规则，移回该物理节点原有的位置。
	for _, route := range routes {
		for i, old := range original.ruleRoutes(route.LogicalNode.Rule) {
			if old.PhysicalNodeID == physical.PhysicalNodeID {
				ruleRoutes := updated.byRule[route.LogicalNode.Rule.RuleID]
				copy(ruleRoutes[i+1:], ruleRoutes[i:len(ruleRoutes)-1])
				ruleRoutes[i] = route
				break
			}
		}
	}
	r.table = updated
	return nil
}

func (r *Router) RouteNew(ctx context.Context, request RouteRequest) (RouteDecision, error) {
	if err := ctx.Err(); err != nil {
		return RouteDecision{}, err
	}
	if err := request.Rule.Validate(); err != nil {
		return RouteDecision{}, err
	}
	if request.TicketID == 0 {
		return RouteDecision{}, fmt.Errorf("TicketID is required")
	}
	if request.AffinityKey == 0 {
		request.AffinityKey = request.TicketID
	}

	table := r.table
	var candidates []RouteCandidate
	for _, route := range table.ruleRoutes(request.Rule) {
		physical, ok := table.physical(route.PhysicalNodeID)
		if !ok || !route.Enabled || !physical.Enabled {
			continue
		}
		key := route.LogicalNode
		key.Rule.Namespace = ""
		candidates = append(candidates, RouteCandidate{
			Owner:  OwnerRef{LogicalNode: key, PhysicalNodeID: route.PhysicalNodeID},
			Weight: route.Weight,
		})
	}
	if len(candidates) == 0 {
		return RouteDecision{}, ErrNoRoute
	}

	selector := r.selector
	if selector == nil {
		selector = WeightedRendezvousSelector{}
	}
	selectionRequest := request
	selectionRequest.Rule.Namespace = ""
	owner, err := selector.Select(RouteSelection{Request: selectionRequest, Candidates: candidates})
	if err != nil {
		return RouteDecision{}, err
	}
	// 候选快照可由外部选择器修改，必须依据路由表重新校验目标。
	route, ok := table.logicalRoute(owner.LogicalNode)
	physical, physicalOK := table.physical(owner.PhysicalNodeID)
	if owner.LogicalNode.Rule.RuleID != request.Rule.RuleID || !ok || !route.Enabled ||
		route.PhysicalNodeID != owner.PhysicalNodeID || !physicalOK || !physical.Enabled {
		return RouteDecision{}, fmt.Errorf("%w: %+v", ErrInvalidRouteSelection, owner)
	}
	owner = OwnerRef{LogicalNode: route.LogicalNode, PhysicalNodeID: route.PhysicalNodeID}
	return RouteDecision{
		DecisionID: decisionID(request, owner),
		Owner:      owner,
		Endpoint:   physical.Endpoint,
	}, nil
}

// ResolveOwner 支持解析禁用的节点，以便继续访问已保存的历史目标。
func (r *Router) ResolveOwner(owner OwnerRef) (ResolvedOwner, error) {
	if err := owner.Validate(); err != nil {
		return ResolvedOwner{}, err
	}
	table := r.table
	physical, ok := table.physical(owner.PhysicalNodeID)
	if !ok {
		return ResolvedOwner{}, ErrOwnerNotFound
	}
	route, ok := table.logicalRoute(owner.LogicalNode)
	if !ok || route.PhysicalNodeID != owner.PhysicalNodeID {
		return ResolvedOwner{}, ErrOwnerNotFound
	}
	return ResolvedOwner{Owner: OwnerRef{LogicalNode: route.LogicalNode, PhysicalNodeID: route.PhysicalNodeID}, Endpoint: physical.Endpoint}, nil
}

// decisionID 对六个 uint64 按大端顺序编码并取 SHA-256 前 64 位；
// 摘要为零时映射为 1，因而与原本摘要为 1 的结果一样可能发生碰撞。
func decisionID(request RouteRequest, owner OwnerRef) uint64 {
	var input [48]byte
	values := [...]uint64{request.Rule.RuleID, request.TicketID,
		request.AffinityKey, request.RequestID, uint64(owner.LogicalNode.PlacementID), uint64(owner.PhysicalNodeID)}
	for i, value := range values {
		binary.BigEndian.PutUint64(input[i*8:(i+1)*8], value)
	}
	digest := sha256.Sum256(input[:])
	id := binary.BigEndian.Uint64(digest[:8])
	if id == 0 {
		return 1
	}
	return id
}
