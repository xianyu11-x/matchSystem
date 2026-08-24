package matchsystem

import (
	"container/heap"
	"errors"
	"fmt"
	"math"
	"sort"

	"matchSystem/internal/identity"
	"matchSystem/internal/matchsystem/prefilter"
)

type SeedSchedulerConfig struct{ SeedLimitPerTick int }

type LogicalNodeConfig struct {
	SeedScheduler SeedSchedulerConfig
	GroupBuilder  GroupBuilderConfig
	MaxPlayers    int
	Prefilter     prefilter.Config
}

// LogicalNode owns one completely isolated matching partition, including its
// tickets, prefilter indexes, rules, scheduling state, and lifecycle.
// Add, Remove, Get, Tick, TickOne, and their WithFacts variants must all be
// called sequentially by the owning PhysicalNode goroutine. LogicalNode contains
// no synchronization and is not goroutine-safe.
type LogicalNode struct {
	key             identity.LogicalNodeKey
	state           LogicalNodeState
	facts           FactProvider
	seedFacts       SeedFactProvider
	config          LogicalNodeConfig
	rules           matchRules
	builder         groupBuilder
	prefilterStore  *prefilter.IndexStore
	nextDocID       uint32
	ticketsByDocID  map[uint32]*Ticket
	ticketIDToDocID map[string]uint32
	arrivalOrder    []uint32
}

func (p *LogicalNode) Add(ticket *Ticket) (uint32, error) {
	if ticket == nil {
		return 0, fmt.Errorf("ticket is nil")
	}
	if ticket.TicketID == "" {
		return 0, fmt.Errorf("TicketID is required")
	}
	if _, exists := p.ticketIDToDocID[ticket.TicketID]; exists {
		return 0, fmt.Errorf("TicketID %q already exists", ticket.TicketID)
	}
	stored := cloneTicket(ticket)
	if stored.DocID == 0 {
		if p.nextDocID == 0 {
			return 0, fmt.Errorf("DocID space is exhausted")
		}
		stored.DocID = p.nextDocID
		p.nextDocID++
	} else {
		if _, exists := p.ticketsByDocID[stored.DocID]; exists {
			return 0, fmt.Errorf("DocID %d already exists", stored.DocID)
		}
		if stored.DocID >= p.nextDocID {
			if stored.DocID == math.MaxUint32 {
				p.nextDocID = 0
			} else {
				p.nextDocID = stored.DocID + 1
			}
		}
	}
	if err := p.prefilterStore.Add(toPrefilterDocument(stored)); err != nil {
		return 0, err
	}
	p.ticketsByDocID[stored.DocID] = stored
	p.ticketIDToDocID[stored.TicketID] = stored.DocID
	p.arrivalOrder = append(p.arrivalOrder, stored.DocID)
	return stored.DocID, nil
}

func (p *LogicalNode) Remove(ticketID string) bool {
	docID, ok := p.ticketIDToDocID[ticketID]
	if !ok {
		return false
	}
	p.removeDocID(docID)
	p.compactArrivalOrder()
	return true
}

func (p *LogicalNode) Get(ticketID string) (*Ticket, bool) {
	docID, ok := p.ticketIDToDocID[ticketID]
	if !ok {
		return nil, false
	}
	ticket := p.ticketsByDocID[docID]
	if ticket == nil {
		return nil, false
	}
	return cloneTicket(ticket), true
}

func (p *LogicalNode) Len() int { return len(p.ticketsByDocID) }
func (p *LogicalNode) Tick(now int64) ([]Match, error) {
	return p.TickWithFacts(now, prefilter.Facts{})
}

// TickOne executes the same matching pipeline as Tick, but stops after the
// first successful group. It is the execution primitive used by PhysicalNode.
func (p *LogicalNode) TickOne(now int64) (*Match, error) {
	return p.TickOneWithFacts(now, prefilter.Facts{})
}

func (p *LogicalNode) TickOneWithFacts(now int64, facts prefilter.Facts) (*Match, error) {
	seeds := p.selectSeeds()
	session, err := p.prefilterStore.BeginTick(facts)
	if err != nil {
		return nil, fmt.Errorf("begin prefilter Tick: %w", err)
	}
	var tickErrors []error
	for _, seed := range seeds {
		if seed == nil {
			continue
		}
		seedFacts, err := p.createSeedFacts(seed, now, facts)
		if err != nil {
			tickErrors = append(tickErrors, fmt.Errorf("seed %q: create Facts: %w", seed.TicketID, err))
			continue
		}
		candidateSet, err := session.Candidates(toPrefilterDocument(seed), seedFacts)
		if err != nil {
			tickErrors = append(tickErrors, fmt.Errorf("seed %q: %w", seed.TicketID, err))
			continue
		}
		candidateSet.Remove(seed.DocID)
		group := p.builder.build(seed, p.topCandidates(seed, candidateSet, now), p.rules, now)
		if !p.rules.CanStartGroup(group, now) {
			if !p.rules.ShouldForceStart(seed, now) {
				continue
			}
			group = []*Ticket{seed}
		}

		for _, ticket := range group {
			p.removeDocID(ticket.DocID)
		}
		p.compactArrivalOrder()
		return &Match{Tickets: group}, nil
	}
	return nil, errors.Join(tickErrors...)
}

func (p *LogicalNode) TickWithFacts(now int64, facts prefilter.Facts) ([]Match, error) {
	seeds := p.selectSeeds()
	used := prefilter.NewDocSet()
	matches := make([]Match, 0)
	session, err := p.prefilterStore.BeginTick(facts)
	if err != nil {
		return nil, fmt.Errorf("begin prefilter Tick: %w", err)
	}
	var tickErrors []error
	for _, seed := range seeds {
		if seed == nil || used.Contains(seed.DocID) {
			continue
		}
		seedFacts, err := p.createSeedFacts(seed, now, facts)
		if err != nil {
			tickErrors = append(tickErrors, fmt.Errorf("seed %q: create Facts: %w", seed.TicketID, err))
			continue
		}
		candidateSet, err := session.Candidates(toPrefilterDocument(seed), seedFacts)
		if err != nil {
			tickErrors = append(tickErrors, fmt.Errorf("seed %q: %w", seed.TicketID, err))
			continue
		}
		candidateSet.Remove(seed.DocID)
		candidateSet.Subtract(used)
		group := p.builder.build(seed, p.topCandidates(seed, candidateSet, now), p.rules, now)
		if p.rules.CanStartGroup(group, now) {
			matches = append(matches, Match{Tickets: group})
			markGroupUsed(used, group)
			continue
		}
		if p.rules.ShouldForceStart(seed, now) {
			group = []*Ticket{seed}
			matches = append(matches, Match{Tickets: group})
			markGroupUsed(used, group)
		}
	}
	used.ForEach(func(docID uint32) bool { p.removeDocID(docID); return true })
	p.compactArrivalOrder()
	return matches, errors.Join(tickErrors...)
}

func (p *LogicalNode) createSeedFacts(seed *Ticket, now int64, tickFacts prefilter.Facts) (prefilter.Facts, error) {
	if p.seedFacts == nil {
		return prefilter.Facts{}, nil
	}
	return p.seedFacts(seed, now, tickFacts)
}

func (p *LogicalNode) selectSeeds() []*Ticket {
	limit := p.config.SeedScheduler.SeedLimitPerTick
	if limit > len(p.ticketsByDocID) {
		limit = len(p.ticketsByDocID)
	}
	selected := make([]*Ticket, 0, limit)
	for _, docID := range p.arrivalOrder {
		if len(selected) == limit {
			break
		}
		if ticket := p.ticketsByDocID[docID]; ticket != nil {
			selected = append(selected, ticket)
		}
	}
	return selected
}

func (p *LogicalNode) topCandidates(seed *Ticket, candidates *prefilter.DocSet, now int64) []*Ticket {
	limit := p.builder.candidateLimit
	best := make(candidateHeap, 0, limit)
	candidates.ForEach(func(docID uint32) bool {
		ticket := p.ticketsByDocID[docID]
		if ticket == nil {
			return true
		}
		entry := candidateEntry{ticket: ticket, score: p.rules.ScoreCandidate(seed, ticket, now)}
		if len(best) < limit {
			heap.Push(&best, entry)
		} else if betterCandidate(entry, best[0]) {
			best[0] = entry
			heap.Fix(&best, 0)
		}
		return true
	})
	sort.Slice(best, func(i, j int) bool { return betterCandidate(best[i], best[j]) })
	out := make([]*Ticket, len(best))
	for i := range best {
		out[i] = best[i].ticket
	}
	return out
}

func (p *LogicalNode) removeDocID(docID uint32) {
	ticket := p.ticketsByDocID[docID]
	if ticket == nil {
		return
	}
	p.prefilterStore.Remove(docID)
	delete(p.ticketsByDocID, docID)
	delete(p.ticketIDToDocID, ticket.TicketID)
}

func (p *LogicalNode) compactArrivalOrder() {
	if len(p.arrivalOrder) <= len(p.ticketsByDocID)*2+1024 {
		return
	}
	compacted := make([]uint32, 0, len(p.ticketsByDocID))
	for _, docID := range p.arrivalOrder {
		if p.ticketsByDocID[docID] != nil {
			compacted = append(compacted, docID)
		}
	}
	p.arrivalOrder = compacted
}

func toPrefilterDocument(ticket *Ticket) prefilter.Document {
	return prefilter.Document{DocID: ticket.DocID, CreatedAt: ticket.CreatedAt, StringLists: ticket.StringLists, Uint64Lists: ticket.Uint64Lists, Int64Values: ticket.Int64Values}
}
func markGroupUsed(used *prefilter.DocSet, group []*Ticket) {
	for _, ticket := range group {
		used.Add(ticket.DocID)
	}
}

type candidateEntry struct {
	ticket *Ticket
	score  float64
}
type candidateHeap []candidateEntry

func (h candidateHeap) Len() int { return len(h) }
func (h candidateHeap) Less(i, j int) bool {
	if h[i].score != h[j].score {
		return h[i].score < h[j].score
	}
	return h[i].ticket.DocID > h[j].ticket.DocID
}
func (h candidateHeap) Swap(i, j int)   { h[i], h[j] = h[j], h[i] }
func (h *candidateHeap) Push(value any) { *h = append(*h, value.(candidateEntry)) }
func (h *candidateHeap) Pop() any {
	old := *h
	value := old[len(old)-1]
	*h = old[:len(old)-1]
	return value
}
func betterCandidate(left, right candidateEntry) bool {
	if left.score != right.score {
		return left.score > right.score
	}
	return left.ticket.DocID < right.ticket.DocID
}
