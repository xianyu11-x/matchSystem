package matchsystem

import "sort"

type GroupBuilderConfig struct {
	CandidateLimitPerSeed int
}

type GroupBuilder struct{ config GroupBuilderConfig }

func NewGroupBuilder(config GroupBuilderConfig) GroupBuilder {
	if config.CandidateLimitPerSeed <= 0 {
		config.CandidateLimitPerSeed = 128
	}
	return GroupBuilder{config: config}
}

// Build uses the single retained algorithm: score-ordered greedy grouping.
func (b GroupBuilder) Build(seed *Ticket, candidates []*Ticket, rules matchRules, now int64) []*Ticket {
	sort.SliceStable(candidates, func(i, j int) bool {
		left := rules.ScoreCandidate(seed, candidates[i], now)
		right := rules.ScoreCandidate(seed, candidates[j], now)
		if left != right {
			return left > right
		}
		return candidates[i].DocID < candidates[j].DocID
	})
	if len(candidates) > b.config.CandidateLimitPerSeed {
		candidates = candidates[:b.config.CandidateLimitPerSeed]
	}

	group := []*Ticket{seed}
	maxPlayers := seed.MaxPlayers
	if maxPlayers <= 0 {
		maxPlayers = 8
	}
	for _, candidate := range candidates {
		if candidate.DocID == seed.DocID {
			continue
		}
		if len(group) >= maxPlayers {
			break
		}
		if rules.CanJoinGroup(group, candidate, now) {
			group = append(group, candidate)
		}
	}
	return group
}
