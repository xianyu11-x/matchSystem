// Package matchsystem contains both the matching algorithm and MatchService's
// in-process PhysicalNode/LogicalNode boundary. The PhysicalNode is the
// single owner of its LogicalNodes; outer scheduling, rate limiting, and
// server IO are out of scope.
package matchsystem

import (
	"context"

	"matchSystem/internal/common"
	"matchSystem/internal/identity"
)

type PhysicalTickResult struct {
	LogicalNode identity.LogicalNodeKey
	Match       *common.Match
}

// PhysicalNode is not goroutine-safe. Its owner goroutine must serialize Load,
// Add, Remove, Get, Tick, BeginDrain, Stop, and Describe.
type PhysicalNode struct {
	id     identity.PhysicalNodeID
	nodes  map[identity.RuleKey]*LogicalNode
	order  []identity.RuleKey
	cursor int
}

func NewPhysicalNode(id identity.PhysicalNodeID) (*PhysicalNode, error) {
	if err := id.Validate(); err != nil {
		return nil, err
	}
	return &PhysicalNode{id: id, nodes: make(map[identity.RuleKey]*LogicalNode)}, nil
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
	return node.addCommon(ctx, ticket)
}

func (p *PhysicalNode) Remove(ctx context.Context, owner identity.OwnerRef, ticketID string) (bool, error) {
	node, err := p.resolve(owner)
	if err != nil {
		return false, err
	}
	return node.removeCommon(ctx, ticketID)
}

func (p *PhysicalNode) Get(ctx context.Context, owner identity.OwnerRef, ticketID string) (*common.Ticket, bool, error) {
	node, err := p.resolve(owner)
	if err != nil {
		return nil, false, err
	}
	return node.getCommon(ctx, ticketID)
}

func (p *PhysicalNode) Tick(ctx context.Context, now int64) (PhysicalTickResult, error) {
	if err := ctx.Err(); err != nil {
		return PhysicalTickResult{}, err
	}
	node, key, err := p.selectLogicalNode()
	if err != nil {
		return PhysicalTickResult{}, err
	}
	match, err := node.tickCommon(ctx, now)
	return PhysicalTickResult{LogicalNode: key, Match: match}, err
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
		if len(p.order) == 0 {
			p.cursor = 0
		} else if p.cursor > i {
			p.cursor--
		} else if p.cursor >= len(p.order) {
			p.cursor = 0
		}
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
	for checked := 0; checked < len(p.order); checked++ {
		index := (p.cursor + checked) % len(p.order)
		node := p.nodes[p.order[index]]
		if node == nil || !node.runnable() {
			continue
		}
		p.cursor = (index + 1) % len(p.order)
		return node, node.key, nil
	}
	return nil, identity.LogicalNodeKey{}, ErrNoLogicalNodeAvailable
}
