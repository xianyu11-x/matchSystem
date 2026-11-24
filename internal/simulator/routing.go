package simulator

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/xianyu11-x/matchSystem/pkg/client"
	"github.com/xianyu11-x/matchSystem/pkg/identity"
	"github.com/xianyu11-x/matchSystem/pkg/matchsystem"
)

type routeTimer struct {
	timer *time.Timer
}

func effectiveRouteUpdateIntervalMS(interval int64) int64 {
	if interval == 0 {
		return DefaultRouteUpdateIntervalMS
	}
	return interval
}

// newEmptyRouter 先建立空路由表，选择器在整个运行时内保持不变。
func newEmptyRouter(config RoutingConfig) (*client.Router, error) {
	if report := ValidateRoutingConfig(config); !report.Valid {
		return nil, report.Err()
	}
	table, err := client.NewRouteTable(client.RouteTableConfig{})
	if err != nil {
		return nil, err
	}
	switch config.Algorithm {
	case "", RoutingWeightedRendezvous:
		return client.NewRouter(table)
	case RoutingQueueWeightedRandom:
		return client.NewRouter(table, client.WithRouteSelector(client.NewQueueWeightedRandomSelector()))
	case RoutingWeightedPriority:
		selector, err := client.NewWeightedPrioritySelector(config.TicketLimit)
		if err != nil {
			return nil, err
		}
		return client.NewRouter(table, client.WithRouteSelector(selector))
	default:
		return nil, fmt.Errorf("unsupported routing algorithm %q", config.Algorithm)
	}
}

// physicalSnapshotRoutes 将核心状态与宿主配置合并；未出现在快照中的部署不发布。
func physicalSnapshotRoutes(physical PhysicalNodeSpec, rules []RuleSpec, snapshot matchsystem.PhysicalNodeRouteSnapshot) (client.PhysicalRoute, []client.RuleRoute, error) {
	if snapshot.PhysicalNodeID != physical.ID {
		return client.PhysicalRoute{}, nil, fmt.Errorf("route snapshot PhysicalNodeID %d does not match %d", snapshot.PhysicalNodeID, physical.ID)
	}
	configured := make(map[logicalIndexKey]RuleSpec, len(rules))
	for _, rule := range rules {
		if rule.PhysicalNodeID == physical.ID {
			configured[logicalIndex(rule.LogicalNode)] = rule
		}
	}
	physicalRoute := client.PhysicalRoute{PhysicalNodeID: physical.ID, Endpoint: physical.Endpoint, Enabled: physical.Enabled}
	routes := make([]client.RuleRoute, 0, len(snapshot.LogicalNodes))
	for _, descriptor := range snapshot.LogicalNodes {
		rule, ok := configured[logicalIndex(descriptor.Key)]
		if !ok {
			return client.PhysicalRoute{}, nil, fmt.Errorf("route snapshot LogicalNode %s has no deployment on PhysicalNode %d", descriptor.Key, physical.ID)
		}
		if descriptor.State == matchsystem.LogicalNodeStopped {
			continue
		}
		if descriptor.State != matchsystem.LogicalNodeReady && descriptor.State != matchsystem.LogicalNodeDraining {
			return client.PhysicalRoute{}, nil, fmt.Errorf("route snapshot LogicalNode %s has unsupported state %q", descriptor.Key, descriptor.State)
		}
		routes = append(routes, client.RuleRoute{
			LogicalNode: rule.LogicalNode, PhysicalNodeID: physical.ID,
			Weight: rule.Weight, Enabled: rule.Enabled && descriptor.State == matchsystem.LogicalNodeReady,
			TicketCount: descriptor.TicketCount,
		})
	}
	return physicalRoute, routes, nil
}

// snapshotRouter 从所有实际节点重建完整路由，并保留场景规则的候选顺序。
func snapshotRouter(ctx context.Context, runtime *simulatorRuntime, config RoutingConfig) (*client.Router, error) {
	router, err := newEmptyRouter(config)
	if err != nil {
		return nil, err
	}
	tableConfig := client.RouteTableConfig{
		PhysicalNodes: make([]client.PhysicalRoute, 0, len(runtime.scenario.PhysicalNodes)),
		Rules:         make([]client.RuleRoute, 0, len(runtime.scenario.Rules)),
	}
	routesByLogical := make(map[logicalIndexKey]client.RuleRoute, len(runtime.scenario.Rules))
	for _, physical := range runtime.scenario.PhysicalNodes {
		adapter := runtime.nodes[physical.ID]
		if adapter == nil {
			return nil, fmt.Errorf("%w: %d", ErrUnknownNode, physical.ID)
		}
		snapshot, err := adapter.RouteSnapshot(ctx)
		if err != nil {
			return nil, fmt.Errorf("read route snapshot for PhysicalNode %d: %w", physical.ID, err)
		}
		physicalRoute, routes, err := physicalSnapshotRoutes(physical, runtime.scenario.Rules, snapshot)
		if err != nil {
			return nil, err
		}
		tableConfig.PhysicalNodes = append(tableConfig.PhysicalNodes, physicalRoute)
		for _, route := range routes {
			key := logicalIndex(route.LogicalNode)
			if _, exists := routesByLogical[key]; exists {
				return nil, fmt.Errorf("duplicate LogicalNode %s in route snapshots", route.LogicalNode)
			}
			routesByLogical[key] = route
		}
	}
	for _, rule := range runtime.scenario.Rules {
		if route, exists := routesByLogical[logicalIndex(rule.LogicalNode)]; exists {
			tableConfig.Rules = append(tableConfig.Rules, route)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	table, err := client.NewRouteTable(tableConfig)
	if err != nil {
		return nil, err
	}
	if err := router.Replace(table); err != nil {
		return nil, err
	}
	return router, nil
}

// UpdateRouting 切换选路算法并发布各节点当前快照，不重建节点或停止流量。
func (s *Simulator) UpdateRouting(ctx context.Context, config RoutingConfig) (RoutingConfig, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return RoutingConfig{}, err
	}
	if s == nil {
		return RoutingConfig{}, ErrSimulatorClosed
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	runtime, err := s.runtimeReadLocked()
	if err != nil {
		return RoutingConfig{}, err
	}
	next, err := snapshotRouter(ctx, runtime, config)
	if err != nil {
		return RoutingConfig{}, err
	}
	if err := ctx.Err(); err != nil {
		return RoutingConfig{}, err
	}
	runtime.router = next
	runtime.scenario.Routing = config
	return config, nil
}

// startRouteTimersLocked 仅在运行时发布后调用，暂存运行时不创建定时器。
func (s *Simulator) startRouteTimersLocked(runtime *simulatorRuntime) {
	if runtime == nil {
		return
	}
	for _, physical := range runtime.scenario.PhysicalNodes {
		s.scheduleRouteTimerLocked(runtime, physical.ID)
	}
}

func (s *Simulator) scheduleRouteTimerLocked(runtime *simulatorRuntime, id identity.PhysicalNodeID) {
	if runtime.routeTimers == nil {
		runtime.routeTimers = make(map[identity.PhysicalNodeID]*routeTimer)
	}
	stopRouteTimerLocked(runtime, id)
	physical, exists := runtime.physical[id]
	if !exists {
		return
	}
	entry := &routeTimer{}
	interval := time.Duration(effectiveRouteUpdateIntervalMS(physical.RouteUpdateIntervalMS)) * time.Millisecond
	entry.timer = time.AfterFunc(interval, func() { s.refreshRouteTimer(runtime, id, entry) })
	runtime.routeTimers[id] = entry
}

func stopRouteTimerLocked(runtime *simulatorRuntime, id identity.PhysicalNodeID) {
	if runtime == nil {
		return
	}
	if entry := runtime.routeTimers[id]; entry != nil {
		entry.timer.Stop()
		delete(runtime.routeTimers, id)
	}
}

func stopRouteTimersLocked(runtime *simulatorRuntime) {
	if runtime == nil {
		return
	}
	for id := range runtime.routeTimers {
		stopRouteTimerLocked(runtime, id)
	}
}

// refreshRouteTimer 检查运行时和定时器代次，旧回调不能覆盖新快照。
func (s *Simulator) refreshRouteTimer(runtime *simulatorRuntime, id identity.PhysicalNodeID, entry *routeTimer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.runtime != runtime || runtime.routeTimers[id] != entry {
		return
	}
	if err := refreshPhysicalRoute(context.Background(), runtime, id); err != nil {
		log.Printf("刷新 PhysicalNode %d 路由快照失败，将在下一周期重试: %v", id, err)
	}
	s.scheduleRouteTimerLocked(runtime, id)
}

func refreshPhysicalRoute(ctx context.Context, runtime *simulatorRuntime, id identity.PhysicalNodeID) error {
	physical, exists := runtime.physical[id]
	adapter := runtime.nodes[id]
	if !exists || adapter == nil {
		return fmt.Errorf("%w: %d", ErrUnknownNode, id)
	}
	snapshot, err := adapter.RouteSnapshot(ctx)
	if err != nil {
		return err
	}
	physicalRoute, routes, err := physicalSnapshotRoutes(physical, runtime.scenario.Rules, snapshot)
	if err != nil {
		return err
	}
	return runtime.router.UpdatePhysicalNode(physicalRoute, routes)
}
