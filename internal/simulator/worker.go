package simulator

import (
	"context"
	"fmt"
	"sync"

	"matchSystem/internal/common"
	"matchSystem/internal/identity"
	"matchSystem/internal/matchsystem"
)

type nodeCommand struct {
	ctx   context.Context
	apply func(*matchsystem.PhysicalNode) (any, error)
	ready chan nodeReply
}

type nodeReply struct {
	value any
	err   error
}

// physicalNodeAdapter is the owner-goroutine boundary around one core
// PhysicalNode. All calls into the non-goroutine-safe core are serialized by
// this worker, including Load, Describe, and lifecycle operations.
type physicalNodeAdapter struct {
	node     *matchsystem.PhysicalNode
	commands chan nodeCommand
	done     chan struct{}
	closeOne sync.Once
	sendMu   sync.RWMutex
}

func newPhysicalNodeAdapter(node *matchsystem.PhysicalNode) (*physicalNodeAdapter, error) {
	if node == nil {
		return nil, fmt.Errorf("PhysicalNode is nil")
	}
	adapter := &physicalNodeAdapter{
		node:     node,
		commands: make(chan nodeCommand),
		done:     make(chan struct{}),
	}
	go adapter.loop()
	return adapter, nil
}

func (a *physicalNodeAdapter) loop() {
	defer close(a.done)
	for command := range a.commands {
		if command.ctx == nil {
			command.ctx = context.Background()
		}
		if err := command.ctx.Err(); err != nil {
			command.ready <- nodeReply{err: err}
			continue
		}
		if command.apply == nil {
			command.ready <- nodeReply{err: fmt.Errorf("PhysicalNode command is nil")}
			continue
		}
		value, err := command.apply(a.node)
		command.ready <- nodeReply{value: value, err: err}
	}
}

func (a *physicalNodeAdapter) call(ctx context.Context, apply func(*matchsystem.PhysicalNode) (any, error)) (any, error) {
	if a == nil || a.node == nil {
		return nil, fmt.Errorf("PhysicalNode adapter is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ready := make(chan nodeReply, 1)
	command := nodeCommand{ctx: ctx, apply: apply, ready: ready}
	a.sendMu.RLock()
	select {
	case a.commands <- command:
	case <-ctx.Done():
		a.sendMu.RUnlock()
		return nil, ctx.Err()
	case <-a.done:
		a.sendMu.RUnlock()
		return nil, fmt.Errorf("PhysicalNode worker is stopped")
	}
	a.sendMu.RUnlock()
	select {
	case reply := <-ready:
		return reply.value, reply.err
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-a.done:
		return nil, fmt.Errorf("PhysicalNode worker is stopped")
	}
}

func (a *physicalNodeAdapter) close() {
	if a == nil {
		return
	}
	a.closeOne.Do(func() {
		a.sendMu.Lock()
		close(a.commands)
		a.sendMu.Unlock()
		<-a.done
	})
}

func (a *physicalNodeAdapter) ID() identity.PhysicalNodeID {
	if a == nil || a.node == nil {
		return ""
	}
	return a.node.ID()
}

func (a *physicalNodeAdapter) Load(ctx context.Context, spec matchsystem.LogicalNodeSpec) error {
	_, err := a.call(ctx, func(node *matchsystem.PhysicalNode) (any, error) {
		return nil, node.Load(ctx, spec)
	})
	return err
}

func (a *physicalNodeAdapter) Add(ctx context.Context, owner identity.OwnerRef, ticket *common.Ticket) error {
	_, err := a.call(ctx, func(node *matchsystem.PhysicalNode) (any, error) {
		return nil, node.Add(ctx, owner, ticket)
	})
	return err
}

func (a *physicalNodeAdapter) Remove(ctx context.Context, owner identity.OwnerRef, ticketID common.TicketID) (bool, error) {
	value, err := a.call(ctx, func(node *matchsystem.PhysicalNode) (any, error) {
		return node.Remove(ctx, owner, ticketID)
	})
	if err != nil {
		return false, err
	}
	result, ok := value.(bool)
	if !ok {
		return false, fmt.Errorf("PhysicalNode Remove returned unexpected value %T", value)
	}
	return result, nil
}

func (a *physicalNodeAdapter) Get(ctx context.Context, owner identity.OwnerRef, ticketID common.TicketID) (*common.Ticket, bool, error) {
	value, err := a.call(ctx, func(node *matchsystem.PhysicalNode) (any, error) {
		ticket, ok, err := node.Get(ctx, owner, ticketID)
		return getResult{ticket: ticket, ok: ok}, err
	})
	if err != nil {
		return nil, false, err
	}
	result, ok := value.(getResult)
	if !ok {
		return nil, false, fmt.Errorf("PhysicalNode Get returned unexpected value %T", value)
	}
	return result.ticket, result.ok, nil
}

type getResult struct {
	ticket *common.Ticket
	ok     bool
}

func (a *physicalNodeAdapter) BeginMatchRound(ctx context.Context, now int64) error {
	_, err := a.call(ctx, func(node *matchsystem.PhysicalNode) (any, error) {
		return nil, node.BeginMatchRound(ctx, now)
	})
	return err
}

func (a *physicalNodeAdapter) ProduceMatch(ctx context.Context) (NodeProduceResult, error) {
	value, err := a.call(ctx, func(node *matchsystem.PhysicalNode) (any, error) {
		result, err := node.ProduceMatch(ctx)
		return NodeProduceResult{
			PhysicalNodeID: node.ID(),
			LogicalNode:    result.LogicalNode,
			Match:          result.Match,
		}, err
	})
	if err != nil {
		return NodeProduceResult{}, err
	}
	result, ok := value.(NodeProduceResult)
	if !ok {
		return NodeProduceResult{}, fmt.Errorf("PhysicalNode ProduceMatch returned unexpected value %T", value)
	}
	return result, nil
}

func (a *physicalNodeAdapter) BeginDrain(ctx context.Context, key identity.LogicalNodeKey) error {
	_, err := a.call(ctx, func(node *matchsystem.PhysicalNode) (any, error) {
		return nil, node.BeginDrain(ctx, key)
	})
	return err
}

func (a *physicalNodeAdapter) Stop(ctx context.Context, key identity.LogicalNodeKey) error {
	_, err := a.call(ctx, func(node *matchsystem.PhysicalNode) (any, error) {
		return nil, node.Stop(ctx, key)
	})
	return err
}

func (a *physicalNodeAdapter) Describe(ctx context.Context) ([]NodeDescriptor, error) {
	value, err := a.call(ctx, func(node *matchsystem.PhysicalNode) (any, error) {
		descriptors := node.Describe()
		result := make([]NodeDescriptor, len(descriptors))
		for index, descriptor := range descriptors {
			result[index] = NodeDescriptor{
				PhysicalNodeID: node.ID(),
				Key:            descriptor.Key,
				State:          string(descriptor.State),
				TicketCount:    descriptor.TicketCount,
			}
		}
		return result, nil
	})
	if err != nil {
		return nil, err
	}
	result, ok := value.([]NodeDescriptor)
	if !ok {
		return nil, fmt.Errorf("PhysicalNode Describe returned unexpected value %T", value)
	}
	return result, nil
}

func (a *physicalNodeAdapter) FactSpecs(ctx context.Context, key identity.LogicalNodeKey) ([]matchsystem.FactSpec, error) {
	value, err := a.call(ctx, func(node *matchsystem.PhysicalNode) (any, error) {
		return node.FactSpecs(ctx, key)
	})
	if err != nil {
		return nil, err
	}
	result, ok := value.([]matchsystem.FactSpec)
	if !ok {
		return nil, fmt.Errorf("PhysicalNode FactSpecs returned unexpected value %T", value)
	}
	return append([]matchsystem.FactSpec(nil), result...), nil
}

var _ NodePort = (*physicalNodeAdapter)(nil)
