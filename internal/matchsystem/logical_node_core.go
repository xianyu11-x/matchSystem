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

type LogicalNodeConfig struct {
	SeedScheduler SeedSchedulerConfig
	GroupBuilder  GroupBuilderConfig
	MaxPlayers    int
	// Facts is the node-wide contract shared by Prefilter, scoring and group
	// evaluation. Prefilter.Facts is accepted only as a compatibility source.
	Facts     []FactSpec
	Prefilter prefilter.Config
}

// LogicalNode owns one completely isolated matching partition, including its
// tickets, prefilter indexes, rules, scheduling state, and lifecycle.
// All methods must be called sequentially by the owning PhysicalNode goroutine.
// LogicalNode contains no synchronization and is not goroutine-safe.
type LogicalNode struct {
	key             identity.LogicalNodeKey
	state           LogicalNodeState
	tickFacts       FactProvider
	objectFacts     ObjectFactProvider
	config          LogicalNodeConfig
	rules           matchRules
	builder         groupBuilder
	prefilterStore  *prefilter.IndexStore
	nextDocID       uint32
	ticketsByDocID  map[uint32]*Ticket
	ticketIDToDocID map[string]uint32
	arrivalOrder    []uint32
	seedOrderPolicy SeedOrderPolicy
	seedRound       seedRound
	oldestTickets   oldestTicketHeap
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
	heap.Push(&p.oldestTickets, stored)
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

// BeginMatchRound captures one immutable seed order and resets its cursor.
// Tickets added after this call become eligible in the next round.
func (p *LogicalNode) BeginMatchRound(now int64) error {
	round, err := p.buildSeedRound(now)
	if err != nil {
		return err
	}
	p.seedRound = round
	return nil
}

// ProduceMatch consumes seeds from the current round and returns at most one
// group. BeginMatchRound must be called before the first ProduceMatch call.
func (p *LogicalNode) ProduceMatch(facts Facts) (*Match, error) {
	if !p.seedRound.initialized {
		return nil, ErrMatchRoundNotStarted
	}
	seed := p.nextSeed()
	return p.produceMatchFromSeed(p.seedRound.now, facts, seed)
}

func (p *LogicalNode) produceMatchFromSeed(now int64, facts Facts, firstSeed *Ticket) (*Match, error) {
	if firstSeed == nil {
		return nil, nil
	}
	frame, err := newFactFrame(facts, p.config.Facts)
	if err != nil {
		return nil, fmt.Errorf("begin Fact frame: %w", err)
	}
	session, err := p.prefilterStore.BeginTick(frame.tick)
	if err != nil {
		return nil, fmt.Errorf("begin prefilter Tick: %w", err)
	}
	var tickErrors []error
	for attempted := 0; attempted < p.config.SeedScheduler.AttemptLimitPerProduceMatch; attempted++ {
		seed := firstSeed
		if attempted > 0 {
			seed = p.nextSeed()
		}
		if seed == nil {
			break
		}
		seedFacts, err := frame.object(seed, now, p.objectFacts)
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
		rankedCandidates, candidateErr := p.topCandidates(seed, candidateSet, now, frame)
		if candidateErr != nil {
			tickErrors = append(tickErrors, fmt.Errorf("seed %q: candidate Facts: %w", seed.TicketID, candidateErr))
		}
		group := p.builder.build(seed, rankedCandidates, p.rules, now, frame.view())
		if !p.rules.CanStartGroupWithFacts(group, now, frame.view()) {
			if !p.rules.ShouldForceStartWithFacts(seed, now, frame.view()) {
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

// nextSeed reserves one seed in the current matching round. The cursor advances
// before evaluation, so failures never make a seed selectable again in that
// round. Deleted DocIDs remain harmless stale entries in the round snapshot.
func (p *LogicalNode) nextSeed() *Ticket {
	for p.seedRound.cursor < len(p.seedRound.order) {
		docID := p.seedRound.order[p.seedRound.cursor]
		p.seedRound.cursor++
		if ticket := p.ticketsByDocID[docID]; ticket != nil {
			return ticket
		}
	}
	return nil
}
func (p *LogicalNode) hasUntriedSeed() bool {
	if !p.seedRound.initialized {
		return false
	}
	for index := p.seedRound.cursor; index < len(p.seedRound.order); index++ {
		if p.ticketsByDocID[p.seedRound.order[index]] != nil {
			return true
		}
	}
	return false
}

func (p *LogicalNode) oldestCreatedAt() (int64, bool) {
	for len(p.oldestTickets) > 0 {
		oldest := p.oldestTickets[0]
		if p.ticketsByDocID[oldest.DocID] == oldest {
			return oldest.CreatedAt, true
		}
		heap.Pop(&p.oldestTickets)
	}
	return 0, false
}

func (p *LogicalNode) buildSeedRound(now int64) (seedRound, error) {
	candidates := make([]*Ticket, 0, len(p.ticketsByDocID))
	for _, docID := range p.arrivalOrder {
		if ticket := p.ticketsByDocID[docID]; ticket != nil {
			candidates = append(candidates, ticket)
		}
	}
	order, err := p.seedOrderPolicy.BuildOrder(SeedOrderContext{Now: now, Candidates: candidates})
	if err != nil {
		return seedRound{}, fmt.Errorf("build seed order for LogicalNode %s: %w", p.key, err)
	}
	if err := validateSeedOrder(candidates, order); err != nil {
		return seedRound{}, fmt.Errorf("validate seed order for LogicalNode %s: %w", p.key, err)
	}
	return seedRound{now: now, order: append([]uint32(nil), order...), initialized: true}, nil
}

func validateSeedOrder(candidates []*Ticket, order []uint32) error {
	if len(order) != len(candidates) {
		return fmt.Errorf("policy returned %d DocIDs for %d candidates", len(order), len(candidates))
	}
	remaining := make(map[uint32]struct{}, len(candidates))
	for _, ticket := range candidates {
		remaining[ticket.DocID] = struct{}{}
	}
	for _, docID := range order {
		if _, exists := remaining[docID]; !exists {
			return fmt.Errorf("policy returned duplicate or unknown DocID %d", docID)
		}
		delete(remaining, docID)
	}
	return nil
}

func (p *LogicalNode) topCandidates(seed *Ticket, candidates *prefilter.DocSet, now int64, frame *factFrame) ([]*Ticket, error) {
	limit := p.builder.candidateLimit
	best := make(candidateHeap, 0, limit)
	var candidateErrors []error
	candidates.ForEach(func(docID uint32) bool {
		ticket := p.ticketsByDocID[docID]
		if ticket == nil {
			return true
		}
		if _, err := frame.object(ticket, now, p.objectFacts); err != nil {
			candidateErrors = append(candidateErrors, fmt.Errorf("candidate %q: create Facts: %w", ticket.TicketID, err))
			return true
		}
		entry := candidateEntry{ticket: ticket, score: p.rules.ScoreCandidateWithContext(CandidateScoreContext{
			Seed: seed, Candidate: ticket, Now: now, Facts: frame.view(),
		})}
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
	return out, errors.Join(candidateErrors...)
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

type candidateEntry struct {
	ticket *Ticket
	score  float64
}

type oldestTicketHeap []*Ticket

func (h oldestTicketHeap) Len() int { return len(h) }
func (h oldestTicketHeap) Less(i, j int) bool {
	if h[i].CreatedAt != h[j].CreatedAt {
		return h[i].CreatedAt < h[j].CreatedAt
	}
	return h[i].DocID < h[j].DocID
}
func (h oldestTicketHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *oldestTicketHeap) Push(value any) {
	*h = append(*h, value.(*Ticket))
}
func (h *oldestTicketHeap) Pop() any {
	old := *h
	value := old[len(old)-1]
	*h = old[:len(old)-1]
	return value
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
