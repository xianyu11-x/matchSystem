// Package matchsystem contains both the matching algorithm and MatchService's
// in-process PhysicalNode/LogicalNode boundary. The PhysicalNode is the
// single owner of its LogicalNodes; outer scheduling, rate limiting, and
// server IO are out of scope.
package matchsystem

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/xianyu11-x/matchSystem/pkg/common"
	"github.com/xianyu11-x/matchSystem/pkg/identity"
)

type PhysicalMatchResult struct {
	LogicalNode identity.LogicalNodeKey
	Match       *common.Match
}

// PhysicalNodeRouteSnapshot 是单个物理节点及其全部逻辑节点的独立路由快照。
// LogicalNodes 按当前部署顺序排列，并包含排空中的逻辑节点。
type PhysicalNodeRouteSnapshot struct {
	PhysicalNodeID identity.PhysicalNodeID
	LogicalNodes   []LogicalNodeDescriptor
}

type PhysicalNodeOption func(*PhysicalNode) error

// WithLogger 为物理节点及其未单独配置 Logger 的逻辑节点注入统一日志出口。
func WithLogger(logger *slog.Logger) PhysicalNodeOption {
	return func(node *PhysicalNode) error {
		if logger == nil {
			return fmt.Errorf("Logger is nil")
		}
		node.logger = logger
		return nil
	}
}

// WithTimeline 为物理节点加载或接管的逻辑节点启用可选时间线录制。
func WithTimeline(config TimelineConfig) PhysicalNodeOption {
	return func(node *PhysicalNode) error {
		node.timeline = config
		return nil
	}
}

func WithLogicalNodeSelector(selector LogicalNodeSelector) PhysicalNodeOption {
	return func(node *PhysicalNode) error {
		if selector == nil {
			return fmt.Errorf("LogicalNodeSelector is nil")
		}
		node.selector = selector
		return nil
	}
}

// PhysicalNode 不支持并发访问。宿主须通过唯一的 owner goroutine 串行调用 Load、
// Reconfigure、Add、Remove、ClearTickets、Get、BeginMatchRound、ProduceMatch、BeginDrain、
// Stop、Describe、RouteSnapshot 及其他访问节点状态的方法。
type PhysicalNode struct {
	id          identity.PhysicalNodeID
	nodes       map[uint64]*LogicalNode
	order       []uint64
	selector    LogicalNodeSelector
	logger      *slog.Logger
	timeline    TimelineConfig
	roundActive bool
	roundNow    int64
}

func NewPhysicalNode(id identity.PhysicalNodeID, options ...PhysicalNodeOption) (*PhysicalNode, error) {
	if err := id.Validate(); err != nil {
		return nil, err
	}
	node := &PhysicalNode{
		id:       id,
		nodes:    make(map[uint64]*LogicalNode),
		selector: NewRoundRobinLogicalNodeSelector(),
	}
	for _, option := range options {
		if option == nil {
			return nil, fmt.Errorf("PhysicalNode option is nil")
		}
		if err := option(node); err != nil {
			return nil, err
		}
	}
	if node.timeline.Enabled && node.logger == nil {
		return nil, fmt.Errorf("timeline recording requires a Logger")
	}
	return node, nil
}

func (p *PhysicalNode) ID() identity.PhysicalNodeID { return p.id }

// EndTimelines 标记此物理节点持有的所有录制实例结束，不改变匹配状态。
// 仅应在物理节点不再承担业务、即将销毁时由 owner 协程调用；结束不可撤销。
// 重复调用安全，结束后继续使用节点不会产生时间线事件。
func (p *PhysicalNode) EndTimelines() {
	if p == nil {
		return
	}
	for _, rule := range p.order {
		if node := p.nodes[rule]; node != nil {
			node.endTimeline()
		}
	}
}

func (p *PhysicalNode) Load(ctx context.Context, spec LogicalNodeSpec) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := spec.Key.Validate(); err != nil {
		return err
	}
	if _, exists := p.nodes[spec.Key.Rule.RuleID]; exists {
		return ErrDuplicateRuleKey
	}
	if spec.Logger == nil {
		spec.Logger = p.logger
	}
	if !spec.Timeline.Enabled && p.timeline.Enabled {
		spec.Timeline = p.timeline
	}
	node, err := newLogicalNode(spec, p.id)
	if err != nil {
		return err
	}
	p.nodes[spec.Key.Rule.RuleID] = node
	p.order = append(p.order, spec.Key.Rule.RuleID)
	if p.logger != nil {
		p.logger.Info("logical node loaded", "physical_node_id", uint64(p.id), "logical_node", spec.Key.String())
	}
	return nil
}

// Reconfigure 根据 order 原子替换节点集合，保留未替换节点的实例及其运行状态。
// replacements 中的节点须由 NewLogicalNode 构造、尚未挂载，并由调用方独占；
// 成功后其所有权转交给 PhysicalNode，调用方不得再访问。失败时调用方保留所有权，
// 所有节点和选择器均不变。selector 为 nil 时保留原选择器及其调度游标。
// 物理节点已有活动轮次时，仅为新节点初始化同一轮次，不重置原节点的消费进度。
func (p *PhysicalNode) Reconfigure(order []identity.LogicalNodeKey, replacements []*LogicalNode, selector LogicalNodeSelector) error {
	if p == nil {
		return fmt.Errorf("PhysicalNode is nil")
	}
	prepared := make(map[uint64]*LogicalNode, len(replacements))
	for _, node := range replacements {
		if node == nil {
			return fmt.Errorf("prepared LogicalNode is nil")
		}
		if err := node.key.Validate(); err != nil {
			return err
		}
		if _, exists := prepared[node.key.Rule.RuleID]; exists {
			return ErrDuplicateRuleKey
		}
		if node.state != LogicalNodeReady || node.store == nil || node.evaluator == nil {
			return fmt.Errorf("prepared LogicalNode %s is not initialized", node.key)
		}
		if err := node.validateMatchRound(); err != nil {
			return err
		}
		prepared[node.key.Rule.RuleID] = node
	}
	next := make(map[uint64]*LogicalNode, len(order))
	nextOrder := make([]uint64, 0, len(order))
	for _, key := range order {
		if err := key.Validate(); err != nil {
			return err
		}
		if _, exists := next[key.Rule.RuleID]; exists {
			return ErrDuplicateRuleKey
		}
		node := prepared[key.Rule.RuleID]
		if node == nil {
			node = p.nodes[key.Rule.RuleID]
		}
		if node == nil || node.key.PlacementID != key.PlacementID {
			return fmt.Errorf("%w: %s", ErrLogicalNodeNotFound, key)
		}
		next[key.Rule.RuleID] = node
		nextOrder = append(nextOrder, key.Rule.RuleID)
	}
	for rule, node := range prepared {
		if next[rule] != node {
			return fmt.Errorf("prepared LogicalNode %s is absent from the requested order", node.key)
		}
	}
	// 先准备所有录制状态，失败时不改变已有节点或预构建节点。
	newTimelines := make(map[*LogicalNode]*timelineState, len(replacements))
	for _, node := range replacements {
		if node.timeline != nil || !p.timeline.Enabled {
			continue
		}
		logger := node.logger
		if logger == nil {
			logger = p.logger
		}
		state, err := newTimelineState(logger, p.timeline, p.id)
		if err != nil {
			return err
		}
		newTimelines[node] = state
	}
	for rule, old := range p.nodes {
		if next[rule] != old {
			old.endTimeline()
		}
	}
	for _, node := range replacements {
		if node.logger == nil {
			node.logger = p.logger
		}
		if state := newTimelines[node]; state != nil {
			node.timeline = state
			node.startTimeline()
		} else if node.timeline != nil {
			// 已独立录制的实例保留标识和序号；附着后事件注明物理节点。
			node.timeline.physicalNodeID = p.id
		}
	}
	if p.roundActive {
		for _, node := range replacements {
			node.beginMatchRound(p.roundNow)
		}
	}
	p.nodes = next
	p.order = nextOrder
	if selector != nil {
		p.selector = selector
	}
	if p.logger != nil {
		p.logger.Info("logical nodes reconfigured", "physical_node_id", uint64(p.id), "logical_node_count", len(next))
	}
	return nil
}

func (p *PhysicalNode) Add(ctx context.Context, owner identity.OwnerRef, ticket *common.Ticket) error {
	node, err := p.resolve(owner)
	if err != nil {
		if p.logger != nil {
			p.logger.Warn("ticket add rejected", "physical_node_id", uint64(p.id), "logical_node", owner.LogicalNode.String(), "error", err)
		}
		return err
	}
	return node.addTicket(ctx, ticket)
}

func (p *PhysicalNode) Remove(ctx context.Context, owner identity.OwnerRef, ticketID common.TicketID) (bool, error) {
	node, err := p.resolve(owner)
	if err != nil {
		return false, err
	}
	return node.removeTicket(ctx, ticketID)
}

// Withdraw 只将宿主明确发起的成功取消记录为主动退出。
func (p *PhysicalNode) Withdraw(ctx context.Context, owner identity.OwnerRef, ticketID common.TicketID) (bool, error) {
	node, err := p.resolve(owner)
	if err != nil {
		return false, err
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return node.Withdraw(ticketID), nil
}

// ClearTickets 清空当前物理节点上所有逻辑节点的等待队列，保留部署及轮次状态。
// 调用方须在 owner goroutine 中执行；开始清理后会完成全部删除。
func (p *PhysicalNode) ClearTickets(ctx context.Context) (int, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if p == nil {
		return 0, fmt.Errorf("PhysicalNode is nil")
	}
	removed := 0
	for _, node := range p.nodes {
		for ticketID := range node.store.ticketIDToDocID {
			if node.Remove(ticketID) {
				removed++
			}
		}
		// 最早到达时间堆采用惰性删除；队列清空后可立即释放残留引用。
		node.store.oldestTickets = nil
	}
	return removed, nil
}

// Get returns an owned deep copy of the requested Ticket. Mutating or retaining
// the returned value cannot affect the PhysicalNode-owned Ticket.
func (p *PhysicalNode) Get(ctx context.Context, owner identity.OwnerRef, ticketID common.TicketID) (*common.Ticket, bool, error) {
	node, err := p.resolve(owner)
	if err != nil {
		return nil, false, err
	}
	return node.getTicket(ctx, ticketID)
}

// BeginMatchRound starts a new bounded seed stream for every LogicalNode.
// Logical-node selection continues from its existing cursor across rounds.
func (p *PhysicalNode) BeginMatchRound(ctx context.Context, now int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// Runtime BeginRound has no failure result: all production policies are
	// built-in owner-local state machines. Keep a validation pass so a malformed
	// manually-constructed node cannot leave only a prefix of the physical node
	// in a new round.
	for _, rule := range p.order {
		node := p.nodes[rule]
		if node == nil {
			continue
		}
		if err := node.validateMatchRound(); err != nil {
			return err
		}
	}
	for _, rule := range p.order {
		node := p.nodes[rule]
		if node != nil {
			node.beginMatchRound(now)
		}
	}
	p.roundActive = true
	p.roundNow = now
	return nil
}

// ProduceMatch performs one logical-node matching attempt in the current
// MatchService round and returns at most one group. BeginMatchRound must be
// called before it; the round's fixed time is captured there.
func (p *PhysicalNode) ProduceMatch(ctx context.Context) (PhysicalMatchResult, error) {
	if err := ctx.Err(); err != nil {
		return PhysicalMatchResult{}, err
	}
	if !p.roundActive {
		return PhysicalMatchResult{}, ErrMatchRoundNotStarted
	}
	node, key, err := p.selectLogicalNode()
	if err != nil {
		return PhysicalMatchResult{}, err
	}
	match, err := node.ProduceMatch(ctx)
	return PhysicalMatchResult{LogicalNode: key, Match: match}, err
}

func (p *PhysicalNode) BeginDrain(ctx context.Context, key identity.LogicalNodeKey) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	node, err := p.resolveKey(key)
	if err != nil {
		return err
	}
	node.beginDrain()
	return nil
}

func (p *PhysicalNode) Stop(ctx context.Context, key identity.LogicalNodeKey) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	node, err := p.resolveKey(key)
	if err != nil {
		return err
	}
	if err := node.stop(); err != nil {
		return err
	}
	node.endTimeline()
	delete(p.nodes, key.Rule.RuleID)
	for i, rule := range p.order {
		if rule != key.Rule.RuleID {
			continue
		}
		p.order = append(p.order[:i], p.order[i+1:]...)
		break
	}
	if p.logger != nil {
		p.logger.Info("logical node stopped", "physical_node_id", uint64(p.id), "logical_node", key.String())
	}
	return nil
}

func (p *PhysicalNode) Describe() []LogicalNodeDescriptor {
	result := make([]LogicalNodeDescriptor, 0, len(p.order))
	for _, rule := range p.order {
		if node := p.nodes[rule]; node != nil {
			result = append(result, node.descriptor())
		}
	}
	return result
}

// RouteSnapshot 返回可由调用方自由修改或保留的独立路由快照。
// 调用方须在 owner goroutine 中串行访问，以获取同一时刻的物理节点和逻辑节点状态。
func (p *PhysicalNode) RouteSnapshot() PhysicalNodeRouteSnapshot {
	return PhysicalNodeRouteSnapshot{
		PhysicalNodeID: p.id,
		LogicalNodes:   p.Describe(),
	}
}

// FactSpecs returns an owned snapshot of the Fact metadata declared by the
// specified LogicalNode. The lookup is performed through the same identity
// validation as the other LogicalNode operations, so a missing node is
// reported as ErrLogicalNodeNotFound instead of returning an empty contract.
func (p *PhysicalNode) FactSpecs(ctx context.Context, key identity.LogicalNodeKey) ([]FactSpec, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if p == nil {
		return nil, ErrLogicalNodeNotFound
	}
	node, err := p.resolveKey(key)
	if err != nil {
		return nil, err
	}
	return node.FactSpecs(), nil
}

// TimelineInstanceID 返回指定逻辑节点的录制流标识；调用方须在 owner 协程访问。
func (p *PhysicalNode) TimelineInstanceID(ctx context.Context, key identity.LogicalNodeKey) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if p == nil {
		return "", ErrLogicalNodeNotFound
	}
	node, err := p.resolveKey(key)
	if err != nil {
		return "", err
	}
	return node.TimelineInstanceID(), nil
}

// DescribeFacts is a descriptive alias for FactSpecs at the PhysicalNode
// boundary. It is useful to callers that already use Describe for snapshots.
func (p *PhysicalNode) DescribeFacts(ctx context.Context, key identity.LogicalNodeKey) ([]FactSpec, error) {
	return p.FactSpecs(ctx, key)
}

// ProviderDescriptors returns the capability snapshot captured during the
// LogicalNode startup handshake. It is intentionally exposed through the
// owner boundary so hosts can publish metadata without invoking Provider
// Describe a second time.
func (p *PhysicalNode) ProviderDescriptors(ctx context.Context, key identity.LogicalNodeKey) (ProviderDescriptorSet, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return ProviderDescriptorSet{}, err
	}
	if p == nil {
		return ProviderDescriptorSet{}, ErrLogicalNodeNotFound
	}
	node, err := p.resolveKey(key)
	if err != nil {
		return ProviderDescriptorSet{}, err
	}
	return node.ProviderDescriptors(), nil
}

func (p *PhysicalNode) resolve(owner identity.OwnerRef) (*LogicalNode, error) {
	if err := owner.Validate(); err != nil {
		return nil, err
	}
	if owner.PhysicalNodeID != p.id {
		return nil, ErrWrongPhysicalNode
	}
	return p.resolveKey(owner.LogicalNode)
}

func (p *PhysicalNode) resolveKey(key identity.LogicalNodeKey) (*LogicalNode, error) {
	if err := key.Validate(); err != nil {
		return nil, err
	}
	node := p.nodes[key.Rule.RuleID]
	if node == nil {
		return nil, ErrLogicalNodeNotFound
	}
	if node.key.PlacementID != key.PlacementID {
		return nil, ErrOwnerMismatch
	}
	return node, nil
}

func (p *PhysicalNode) selectLogicalNode() (*LogicalNode, identity.LogicalNodeKey, error) {
	if len(p.order) == 0 {
		return nil, identity.LogicalNodeKey{}, ErrNoLogicalNodeAvailable
	}
	candidates := make([]LogicalNodeCandidate, 0, len(p.order))
	for _, rule := range p.order {
		node := p.nodes[rule]
		if node == nil {
			continue
		}
		oldest, _ := node.oldestCreatedAt()
		key := node.key
		key.Rule.Namespace = ""
		candidates = append(candidates, LogicalNodeCandidate{
			Key:             key,
			Eligible:        node.runnable() && node.hasUntriedSeed(),
			TicketCount:     node.Len(),
			OldestCreatedAt: oldest,
		})
	}
	key, err := p.selector.Select(LogicalNodeSelectContext{Candidates: candidates})
	if err != nil {
		return nil, identity.LogicalNodeKey{}, err
	}
	node, err := p.resolveKey(key)
	if err != nil {
		return nil, identity.LogicalNodeKey{}, fmt.Errorf("LogicalNodeSelector returned invalid node %s: %w", key, err)
	}
	if !node.runnable() || !node.hasUntriedSeed() {
		return nil, identity.LogicalNodeKey{}, fmt.Errorf("LogicalNodeSelector returned ineligible node %s", key)
	}
	return node, node.key, nil
}
