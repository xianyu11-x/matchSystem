// Package matchsystem contains both the matching algorithm and MatchService's
// in-process PhysicalNode/LogicalNode boundary. The PhysicalNode is the
// single owner of its LogicalNodes; outer scheduling, rate limiting, and
// server IO are out of scope.
package matchsystem

import (
	"context"
	"fmt"

	"matchSystem/internal/common"
	"matchSystem/internal/identity"
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

// PhysicalNode is not goroutine-safe. Its owner goroutine must serialize Load,
// Add, Remove, Get, BeginMatchRound, ProduceMatch, BeginDrain, Stop, and
// Describe.
type PhysicalNode struct {
	id          identity.PhysicalNodeID
	nodes       map[identity.RuleKey]*LogicalNode
	order       []identity.RuleKey
	selector    LogicalNodeSelector
	roundActive bool
}

func NewPhysicalNode(id identity.PhysicalNodeID, options ...PhysicalNodeOption) (*PhysicalNode, error) {
	if err := id.Validate(); err != nil {
		return nil, err
	}
	node := &PhysicalNode{
		id:       id,
		nodes:    make(map[identity.RuleKey]*LogicalNode),
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
	if _, exists := p.nodes[spec.Key.Rule]; exists {
		return ErrDuplicateRuleKey
	}
	node, err := NewLogicalNode(spec)
	if err != nil {
		return err
	}
	p.nodes[spec.Key.Rule] = node
	p.order = append(p.order, spec.Key.Rule)
	return nil
}

func (p *PhysicalNode) Add(ctx context.Context, owner identity.OwnerRef, ticket *common.Ticket) (uint32, error) {
	node, err := p.resolve(owner)
	if err != nil {
		return 0, err
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

// BeginMatchRound atomically captures a new seed order for every LogicalNode.
// Logical-node selection continues from its existing cursor across rounds.
func (p *PhysicalNode) BeginMatchRound(ctx context.Context, now int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	rounds := make(map[identity.RuleKey]seedRound, len(p.nodes))
	for _, rule := range p.order {
		node := p.nodes[rule]
		if node == nil {
			continue
		}
		round, err := node.buildSeedRound(now)
		if err != nil {
			return err
		}
		rounds[rule] = round
	}
	for rule, round := range rounds {
		p.nodes[rule].installSeedRound(round)
	}
	p.roundActive = true
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
	delete(p.nodes, key.Rule)
	for i, rule := range p.order {
		if rule != key.Rule {
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
	node := p.nodes[key.Rule]
	if node == nil {
		return nil, ErrLogicalNodeNotFound
	}
	if node.key != key {
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
		candidates = append(candidates, LogicalNodeCandidate{
			Key:             node.key,
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
