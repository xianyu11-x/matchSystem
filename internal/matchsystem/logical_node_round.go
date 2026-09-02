package matchsystem

import "fmt"

// BeginMatchRound captures one immutable seed order and resets its cursor.
// Tickets added after this call are maintained by the seed runtime for the
// next snapshot; the current snapshot only contains the TicketIDs returned by
// BuildRound.
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
// in that round. Removed/committed TicketIDs remain harmless stale entries in
// the snapshot and do not consume the round attempt budget.
func (p *LogicalNode) nextSeed() *storedTicket {
	p.advancePastStaleSeeds()
	if p.seedRound.cursor == len(p.seedRound.order) ||
		p.seedRound.attemptedSeeds >= p.config.SeedScheduler.AttemptLimitPerMatchRound {
		return nil
	}
	ticketID := p.seedRound.order[p.seedRound.cursor]
	p.seedRound.cursor++
	p.seedRound.attemptedSeeds++
	stored, _ := p.store.lookupTicketID(ticketID)
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
// The matching owner does not mutate the ticket pool between BeginMatchRound
// and its ProduceMatch calls; a TicketID lookup is therefore sufficient to
// detect a committed/deleted snapshot entry.
func (p *LogicalNode) advancePastStaleSeeds() {
	for p.seedRound.cursor < len(p.seedRound.order) {
		ticketID := p.seedRound.order[p.seedRound.cursor]
		if _, ok := p.store.lookupTicketID(ticketID); ok {
			return
		}
		p.seedRound.cursor++
	}
}

// oldestCreatedAt is only the PhysicalNode selector's waiting-time metric;
// SeedOrderRuntime owns the independent "oldest" seed ordering index.
func (p *LogicalNode) oldestCreatedAt() (int64, bool) {
	return p.store.oldestCreatedAt()
}

func (p *LogicalNode) buildSeedRound(now int64) (seedRound, error) {
	if p.seedOrderRuntime == nil {
		return seedRound{}, fmt.Errorf("seed order runtime is not initialized for LogicalNode %s", p.key)
	}
	ticketOrder, err := p.seedOrderRuntime.BuildRound(p.config.SeedScheduler.AttemptLimitPerMatchRound)
	if err != nil {
		return seedRound{}, fmt.Errorf("build seed order for LogicalNode %s: %w", p.key, err)
	}
	if len(ticketOrder) > p.config.SeedScheduler.AttemptLimitPerMatchRound {
		return seedRound{}, fmt.Errorf("policy returned %d TicketIDs, maximum is %d", len(ticketOrder), p.config.SeedScheduler.AttemptLimitPerMatchRound)
	}
	// All production runtimes are built-in and receive the same owner-serialized
	// Add/Remove lifecycle events, so they guarantee active, unique TicketIDs
	// and return an independent snapshot slice. Take that slice directly; the
	// lookup in nextSeed remains the stale/Commit guard at consumption time.
	return seedRound{now: now, order: ticketOrder, initialized: true}, nil
}

func (p *LogicalNode) installSeedRound(round seedRound) {
	p.seedRound = round
}

type seedRound struct {
	now            int64
	order          []TicketID
	cursor         int
	attemptedSeeds int
	initialized    bool
}
