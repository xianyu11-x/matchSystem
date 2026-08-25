package matchsystem

type GroupBuilderConfig struct{ CandidateLimitPerSeed int }
type groupBuilder struct {
	candidateLimit int
	maxPlayers     int
}

func newGroupBuilder(config GroupBuilderConfig, maxPlayers int) groupBuilder {
	if config.CandidateLimitPerSeed <= 0 {
		config.CandidateLimitPerSeed = 128
	}
	if maxPlayers <= 0 {
		maxPlayers = 8
	}
	return groupBuilder{candidateLimit: config.CandidateLimitPerSeed, maxPlayers: maxPlayers}
}
func (b groupBuilder) build(seed *storedTicket, rankedCandidates []*storedTicket, rules matchRules, now int64, facts FactView) []*Ticket {
	group := []*Ticket{seed.Ticket}
	for _, candidate := range rankedCandidates {
		if len(group) >= b.maxPlayers {
			break
		}
		if candidate != seed && rules.CanJoinGroupWithFacts(group, candidate.Ticket, now, facts) {
			group = append(group, candidate.Ticket)
		}
	}
	return group
}
