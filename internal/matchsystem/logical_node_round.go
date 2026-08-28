package matchsystem

import (
	"fmt"

	"matchSystem/internal/matchsystem/prefilter"
)

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

// nextSeed reserves one valid seed in the current matching round. The cursor
// advances before evaluation, so failures never make a seed selectable again
// in that round. Deleted DocIDs remain harmless stale entries in the snapshot
// and do not consume the round attempt budget.
func (p *LogicalNode) nextSeed() *storedTicket {
	p.advancePastStaleSeeds()
	if p.seedRound.cursor == len(p.seedRound.order) ||
		p.seedRound.attemptedSeeds >= p.config.SeedScheduler.AttemptLimitPerMatchRound {
		return nil
	}
	docID := p.seedRound.order[p.seedRound.cursor]
	p.seedRound.cursor++
	p.seedRound.attemptedSeeds++
	stored, _ := p.store.lookupDocID(docID)
	return stored
}

func (p *LogicalNode) hasUntriedSeed() bool {
	if !p.seedRound.initialized {
		return false
	}
	p.advancePastStaleSeeds()
	return p.seedRound.cursor < len(p.seedRound.order) &&
		p.seedRound.attemptedSeeds < p.config.SeedScheduler.AttemptLimitPerMatchRound
}

// advancePastStaleSeeds permanently consumes deleted entries for this round.
// The owner contract forbids Add while a round is being consumed, so a
// recycled DocID cannot become active again before that round ends.
func (p *LogicalNode) advancePastStaleSeeds() {
	for p.seedRound.cursor < len(p.seedRound.order) {
		if _, ok := p.store.lookupDocID(p.seedRound.order[p.seedRound.cursor]); ok {
			return
		}
		p.seedRound.cursor++
	}
}

func (p *LogicalNode) oldestCreatedAt() (int64, bool) {
	return p.store.oldestCreatedAt()
}

func (p *LogicalNode) buildSeedRound(now int64) (seedRound, error) {
	p.seedCandidates = p.seedCandidates[:0]
	// Policies receive the complete active candidate list so they can choose a
	// globally best subset. Their output is bounded separately by
	// SeedOrderContext.MaxSeeds and resolveSeedOrder.
	p.store.forEachArrival(func(ticket *storedTicket) bool {
		p.seedCandidates = append(p.seedCandidates, ticket.Ticket)
		return true
	})
	ticketOrder, err := p.seedOrderPolicy.BuildOrder(SeedOrderContext{
		Now:        now,
		Candidates: p.seedCandidates,
		MaxSeeds:   p.config.SeedScheduler.AttemptLimitPerMatchRound,
	})
	if err != nil {
		return seedRound{}, fmt.Errorf("build seed order for LogicalNode %s: %w", p.key, err)
	}
	order, err := p.resolveSeedOrder(ticketOrder)
	if err != nil {
		return seedRound{}, fmt.Errorf("validate seed order for LogicalNode %s: %w", p.key, err)
	}
	return seedRound{now: now, order: order, initialized: true}, nil
}

func (p *LogicalNode) resolveSeedOrder(ticketOrder []TicketID) ([]uint32, error) {
	order := make([]uint32, len(ticketOrder))
	if len(order) > p.config.SeedScheduler.AttemptLimitPerMatchRound {
		return nil, fmt.Errorf("policy returned %d TicketIDs, maximum is %d", len(order), p.config.SeedScheduler.AttemptLimitPerMatchRound)
	}
	seen := prefilter.NewDocSet()
	for index, ticketID := range ticketOrder {
		docID, exists := p.store.docIDForTicketID(ticketID)
		if !exists || seen.Contains(docID) {
			return nil, fmt.Errorf("policy returned duplicate or unknown TicketID %d", ticketID)
		}
		seen.Add(docID)
		order[index] = docID
	}
	return order, nil
}

func (p *LogicalNode) installSeedRound(round seedRound) {
	p.store.beginRound()
	p.seedRound = round
}

type seedRound struct {
	now            int64
	order          []uint32
	cursor         int
	attemptedSeeds int
	initialized    bool
}
