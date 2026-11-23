// Package matchsystem contains both the matching algorithm and MatchService's
// in-process PhysicalNode/LogicalNode boundary. The PhysicalNode is the
// single owner of its LogicalNodes; outer scheduling, rate limiting, and
// server IO are out of scope.
package matchsystem

import (
	"context"
	"fmt"

	"github.com/xianyu11-x/matchSystem/pkg/common"
	"github.com/xianyu11-x/matchSystem/pkg/identity"
)

type PhysicalMatchResult struct {
	LogicalNode identity.LogicalNodeKey
	Match       *common.Match
}

type PhysicalNodeOption func(*PhysicalNode) error

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
// Reconfigure、Add、Remove、Get、BeginMatchRound、ProduceMatch、BeginDrain、
// Stop、Describe 及其他访问节点状态的方法。
type PhysicalNode struct {
	id          identity.PhysicalNodeID
	nodes       map[uint64]*LogicalNode
	order       []uint64
	selector    LogicalNodeSelector
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
	return node, nil
}

func (p *PhysicalNode) ID() identity.PhysicalNodeID { return p.id }

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
	node, err := NewLogicalNode(spec)
	if err != nil {
		return err
	}
	p.nodes[spec.Key.Rule.RuleID] = node
	p.order = append(p.order, spec.Key.Rule.RuleID)
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
	return nil
}

func (p *PhysicalNode) Add(ctx context.Context, owner identity.OwnerRef, ticket *common.Ticket) error {
	node, err := p.resolve(owner)
	if err != nil {
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
	delete(p.nodes, key.Rule.RuleID)
	for i, rule := range p.order {
		if rule != key.Rule.RuleID {
			continue
		}
		p.order = append(p.order[:i], p.order[i+1:]...)
		break
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
