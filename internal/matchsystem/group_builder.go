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
func (b groupBuilder) build(seed *Ticket, rankedCandidates []*Ticket, rules matchRules, now int64) []*Ticket {
	group := []*Ticket{seed}
	for _, candidate := range rankedCandidates {
		if len(group) >= b.maxPlayers {
			break
		}
		if candidate.DocID != seed.DocID && rules.CanJoinGroup(group, candidate, now) {
			group = append(group, candidate)
		}
	}
	return group
}
