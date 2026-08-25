package matchsystem

import (
	"container/heap"
	"errors"
	"fmt"
	"sort"

	"matchSystem/internal/common"
	"matchSystem/internal/identity"
	"matchSystem/internal/matchsystem/fact"
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
	ticketsByDocID  map[uint32]*storedTicket
	ticketIDToDocID map[TicketID]uint32
	// freeDocIDs is consumed only by Add. The owner contract forbids Add while
	// a match round is being consumed, so a recycled ID cannot make a stale
	// seed entry resolve to a newly added Ticket.
	freeDocIDs           []uint32
	arrivalOrder         []uint32
	seedOrderPolicy      SeedOrderPolicy
	seedRound            seedRound
	seedCandidates       []*Ticket
	storedSeedCandidates []*storedTicket
	seedOrderSpare       []uint32
	oldestTickets        oldestTicketHeap
}

// Add deep-copies ticket exactly once and makes that copy immutable pool state.
func (p *LogicalNode) Add(ticket *Ticket) (uint32, error) {
	if ticket == nil {
		return 0, fmt.Errorf("ticket is nil")
	}
	if ticket.TicketID == 0 {
		return 0, fmt.Errorf("TicketID is required")
	}
	if _, exists := p.ticketIDToDocID[ticket.TicketID]; exists {
		return 0, fmt.Errorf("TicketID %d already exists", ticket.TicketID)
	}
	owned := common.CloneTicket(ticket)
	docID, err := p.allocateDocID()
	if err != nil {
		return 0, err
	}
	stored := &storedTicket{Ticket: owned, docID: docID}
	if err := p.prefilterStore.Add(stored.docID, stored.Ticket); err != nil {
		p.recycleDocID(stored.docID)
		return 0, err
	}
	p.ticketsByDocID[stored.docID] = stored
	p.ticketIDToDocID[stored.TicketID] = stored.docID
	stored.arrivalIndex = len(p.arrivalOrder)
	p.arrivalOrder = append(p.arrivalOrder, stored.docID)
	heap.Push(&p.oldestTickets, stored)
	return stored.docID, nil
}

func (p *LogicalNode) Remove(ticketID TicketID) bool {
	docID, ok := p.ticketIDToDocID[ticketID]
	if !ok {
		return false
	}
	p.removeDocID(docID)
	p.compactArrivalOrder()
	return true
}

// Get returns a borrowed pointer for immediate synchronous inspection. It must
// not be mutated or retained across another command on the owning node.
func (p *LogicalNode) Get(ticketID TicketID) (*Ticket, bool) {
	return p.lookupTicket(ticketID)
}

// lookupTicket borrows the LogicalNode-owned Ticket. Callers must not mutate it
// or retain it across another owner command. A committed match transfers
// ownership of the same pointer.
func (p *LogicalNode) lookupTicket(ticketID TicketID) (*Ticket, bool) {
	docID, ok := p.ticketIDToDocID[ticketID]
	if !ok {
		return nil, false
	}
	stored := p.ticketsByDocID[docID]
	if stored == nil {
		return nil, false
	}
	return stored.Ticket, true
}

func (p *LogicalNode) Len() int { return len(p.ticketsByDocID) }

// BeginMatchRound captures one immutable seed order and resets its cursor.
// Tickets added after this call become eligible in the next round.
func (p *LogicalNode) BeginMatchRound(now int64) error {
	round, err := p.buildSeedRound(now)
	if err != nil {
		return err
	}
	p.installSeedRound(round)
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

func (p *LogicalNode) produceMatchFromSeed(now int64, facts Facts, firstSeed *storedTicket) (*Match, error) {
	if firstSeed == nil {
		return nil, nil
	}
	frame, err := fact.NewFrame(facts, p.config.Facts)
	if err != nil {
		return nil, fmt.Errorf("begin Fact frame: %w", err)
	}
	session, err := p.prefilterStore.BeginTick(frame.Tick())
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
		seedFacts, err := frame.Object(seed.Ticket, now, p.objectFacts)
		if err != nil {
			tickErrors = append(tickErrors, fmt.Errorf("seed %d: create Facts: %w", seed.TicketID, err))
			continue
		}
		candidateSet, err := session.Candidates(seed.docID, seed.Ticket, seedFacts)
		if err != nil {
			tickErrors = append(tickErrors, fmt.Errorf("seed %d: %w", seed.TicketID, err))
			continue
		}
		candidateSet.Remove(seed.docID)
		rankedCandidates, candidateErr := p.topCandidates(seed, candidateSet, now, frame)
		if candidateErr != nil {
			tickErrors = append(tickErrors, fmt.Errorf("seed %d: candidate Facts: %w", seed.TicketID, candidateErr))
		}
		group := p.builder.build(seed, rankedCandidates, p.rules, now, frame.View())
		if !p.rules.CanStartGroupWithFacts(group, now, frame.View()) {
			if !p.rules.ShouldForceStartWithFacts(seed.Ticket, now, frame.View()) {
				continue
			}
			group = []*Ticket{seed.Ticket}
		}

		for _, ticket := range group {
			p.removeDocID(p.ticketIDToDocID[ticket.TicketID])
		}
		p.compactArrivalOrder()
		return &Match{Tickets: group}, nil
	}
	return nil, errors.Join(tickErrors...)
}

// nextSeed reserves one seed in the current matching round. The cursor advances
// before evaluation, so failures never make a seed selectable again in that
// round. Deleted DocIDs remain harmless stale entries in the round snapshot.
func (p *LogicalNode) nextSeed() *storedTicket {
	p.advancePastStaleSeeds()
	if p.seedRound.cursor == len(p.seedRound.order) {
		return nil
	}
	docID := p.seedRound.order[p.seedRound.cursor]
	p.seedRound.cursor++
	return p.ticketsByDocID[docID]
}

func (p *LogicalNode) hasUntriedSeed() bool {
	if !p.seedRound.initialized {
		return false
	}
	p.advancePastStaleSeeds()
	return p.seedRound.cursor < len(p.seedRound.order)
}

// advancePastStaleSeeds permanently consumes deleted entries for this round.
// The owner contract forbids Add while a round is being consumed, so a recycled
// DocID cannot become active again before that round ends.
func (p *LogicalNode) advancePastStaleSeeds() {
	for p.seedRound.cursor < len(p.seedRound.order) {
		if p.ticketsByDocID[p.seedRound.order[p.seedRound.cursor]] != nil {
			return
		}
		p.seedRound.cursor++
	}
}

func (p *LogicalNode) oldestCreatedAt() (int64, bool) {
	for len(p.oldestTickets) > 0 {
		oldest := p.oldestTickets[0]
		if p.ticketsByDocID[oldest.docID] == oldest {
			return oldest.CreatedAt, true
		}
		heap.Pop(&p.oldestTickets)
	}
	return 0, false
}

func (p *LogicalNode) buildSeedRound(now int64) (seedRound, error) {
	if policy, ok := p.seedOrderPolicy.(optimizedSeedOrderPolicy); ok {
		order, ownsOrder := policy.buildOrder(p, p.seedOrderSpare)
		return seedRound{now: now, order: order, ownsOrder: ownsOrder, initialized: true}, nil
	}
	p.seedCandidates = p.seedCandidates[:0]
	for _, docID := range p.arrivalOrder {
		if ticket := p.ticketsByDocID[docID]; ticket != nil {
			p.seedCandidates = append(p.seedCandidates, ticket.Ticket)
		}
	}
	ticketOrder, err := p.seedOrderPolicy.BuildOrder(SeedOrderContext{Now: now, Candidates: p.seedCandidates})
	if err != nil {
		return seedRound{}, fmt.Errorf("build seed order for LogicalNode %s: %w", p.key, err)
	}
	order, err := p.resolveSeedOrder(ticketOrder)
	if err != nil {
		return seedRound{}, fmt.Errorf("validate seed order for LogicalNode %s: %w", p.key, err)
	}
	return seedRound{now: now, order: order, ownsOrder: true, initialized: true}, nil
}

func (p *LogicalNode) resolveSeedOrder(ticketOrder []TicketID) ([]uint32, error) {
	order := make([]uint32, len(ticketOrder))
	if len(order) != len(p.ticketsByDocID) {
		return nil, fmt.Errorf("policy returned %d TicketIDs for %d candidates", len(order), len(p.ticketsByDocID))
	}
	seen := prefilter.NewDocSet()
	for index, ticketID := range ticketOrder {
		docID, exists := p.ticketIDToDocID[ticketID]
		if !exists || seen.Contains(docID) {
			return nil, fmt.Errorf("policy returned duplicate or unknown TicketID %d", ticketID)
		}
		seen.Add(docID)
		order[index] = docID
	}
	return order, nil
}

func (p *LogicalNode) installSeedRound(round seedRound) {
	previous := p.seedRound
	p.seedRound = round
	if previous.ownsOrder {
		p.seedOrderSpare = previous.order[:0]
	} else if round.ownsOrder {
		// Optimized built-ins may have promoted seedOrderSpare to the active
		// round. It must not be reused until that round is replaced.
		p.seedOrderSpare = nil
	}
}

func (p *LogicalNode) topCandidates(seed *storedTicket, candidates *prefilter.DocSet, now int64, frame *fact.Frame) ([]*storedTicket, error) {
	limit := p.builder.candidateLimit
	best := make(candidateHeap, 0, limit)
	var candidateErrors []error
	candidates.ForEach(func(docID uint32) bool {
		ticket := p.ticketsByDocID[docID]
		if ticket == nil {
			return true
		}
		if _, err := frame.Object(ticket.Ticket, now, p.objectFacts); err != nil {
			candidateErrors = append(candidateErrors, fmt.Errorf("candidate %d: create Facts: %w", ticket.TicketID, err))
			return true
		}
		entry := candidateEntry{ticket: ticket, score: p.rules.ScoreCandidateWithContext(CandidateScoreContext{
			Seed: seed.Ticket, Candidate: ticket.Ticket, Now: now, Facts: frame.View(),
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
	out := make([]*storedTicket, len(best))
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
	if index := ticket.arrivalIndex; index >= 0 && index < len(p.arrivalOrder) && p.arrivalOrder[index] == docID {
		p.arrivalOrder[index] = 0
	}
	delete(p.ticketsByDocID, docID)
	delete(p.ticketIDToDocID, ticket.TicketID)
	p.recycleDocID(docID)
}

func (p *LogicalNode) allocateDocID() (uint32, error) {
	if last := len(p.freeDocIDs) - 1; last >= 0 {
		docID := p.freeDocIDs[last]
		p.freeDocIDs = p.freeDocIDs[:last]
		return docID, nil
	}
	if p.nextDocID == 0 {
		return 0, fmt.Errorf("DocID space is exhausted")
	}
	docID := p.nextDocID
	p.nextDocID++
	return docID, nil
}

func (p *LogicalNode) recycleDocID(docID uint32) {
	if docID != 0 {
		p.freeDocIDs = append(p.freeDocIDs, docID)
	}
}

func (p *LogicalNode) compactArrivalOrder() {
	if len(p.arrivalOrder) <= len(p.ticketsByDocID)*2+1024 {
		return
	}
	compacted := make([]uint32, 0, len(p.ticketsByDocID))
	for _, docID := range p.arrivalOrder {
		if ticket := p.ticketsByDocID[docID]; ticket != nil {
			ticket.arrivalIndex = len(compacted)
			compacted = append(compacted, docID)
		}
	}
	p.arrivalOrder = compacted
}

type candidateEntry struct {
	ticket *storedTicket
	score  float64
}

type oldestTicketHeap []*storedTicket

func (h oldestTicketHeap) Len() int { return len(h) }
func (h oldestTicketHeap) Less(i, j int) bool {
	if h[i].CreatedAt != h[j].CreatedAt {
		return h[i].CreatedAt < h[j].CreatedAt
	}
	return h[i].docID < h[j].docID
}
func (h oldestTicketHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *oldestTicketHeap) Push(value any) {
	*h = append(*h, value.(*storedTicket))
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
	return h[i].ticket.docID > h[j].ticket.docID
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
	return left.ticket.docID < right.ticket.docID
}
