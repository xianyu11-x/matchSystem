package matchsystem

import "fmt"

// BeginMatchRound resets the generic round state and asks the seed runtime to
// start a fresh bounded stream. The runtime owns its ordering cursor and
// temporarily held entries; LogicalNode owns only the fixed round time and
// effective attempt budget.
func (p *LogicalNode) BeginMatchRound(now int64) error {
	if err := p.validateMatchRound(); err != nil {
		return err
	}
	p.beginMatchRound(now)
	return nil
}

func (p *LogicalNode) validateMatchRound() error {
	if p == nil || p.seedOrderRuntime == nil {
		return fmt.Errorf("seed order runtime is not initialized for LogicalNode %s", p.key)
	}
	return nil
}

func (p *LogicalNode) beginMatchRound(now int64) {
	limit := p.config.SeedScheduler.AttemptLimitPerMatchRound
	p.seedOrderRuntime.BeginRound(limit)
	p.seedRound = seedRound{
		now:         now,
		initialized: true,
	}
}

// nextSeed reserves one live seed in the current matching round. The runtime
// stream moves every returned ID out of its active ordering index, so a seed
// cannot repeat after a failed evaluation. The store lookup is retained as a
// defensive ownership/liveness check; only a live stored Ticket consumes the
// LogicalNode's effective attempt budget.
func (p *LogicalNode) nextSeed() *storedTicket {
	if p == nil || !p.seedRound.initialized || p.seedOrderRuntime == nil {
		return nil
	}
	if p.seedRound.attemptedSeeds >= p.config.SeedScheduler.AttemptLimitPerMatchRound {
		return nil
	}
	for p.seedOrderRuntime.HasNext() &&
		p.seedRound.attemptedSeeds < p.config.SeedScheduler.AttemptLimitPerMatchRound {
		ticketID, ok := p.seedOrderRuntime.Next()
		if !ok {
			return nil
		}
		stored, live := p.store.lookupTicketID(ticketID)
		if !live || stored == nil || stored.Ticket == nil {
			// Add/Remove/Commit are owner-serialized, so this path should only
			// be reachable if a custom/injected runtime is inconsistent. Remove
			// keeps the invalid ID from being retried in a later round.
			p.seedOrderRuntime.Remove(ticketID)
			continue
		}
		p.seedRound.attemptedSeeds++
		return stored
	}
	return nil
}

func (p *LogicalNode) hasUntriedSeed() bool {
	if p == nil || !p.seedRound.initialized || p.seedOrderRuntime == nil {
		return false
	}
	return p.seedRound.attemptedSeeds < p.config.SeedScheduler.AttemptLimitPerMatchRound &&
		p.seedOrderRuntime.HasNext()
}

// oldestCreatedAt is only the PhysicalNode selector's waiting-time metric;
// SeedOrderRuntime owns the independent "oldest" seed ordering index.
func (p *LogicalNode) oldestCreatedAt() (int64, bool) {
	return p.store.oldestCreatedAt()
}

type seedRound struct {
	now            int64
	attemptedSeeds int
	initialized    bool
}
