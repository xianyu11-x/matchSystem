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
