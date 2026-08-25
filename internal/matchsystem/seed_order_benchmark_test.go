package matchsystem

import (
	"testing"

	"matchSystem/internal/identity"
	"matchSystem/internal/matchsystem/prefilter"
)

func BenchmarkBeginMatchRound100K(b *testing.B) {
	benchmarks := []struct {
		name   string
		config SeedOrderPolicyConfig
	}{
		{name: "arrival", config: SeedOrderPolicyConfig{Kind: SeedOrderArrival}},
		{name: "oldest", config: SeedOrderPolicyConfig{Kind: SeedOrderOldest}},
		{name: "random", config: SeedOrderPolicyConfig{Kind: SeedOrderRandom, RandomSeed: 1}},
	}
	for _, benchmark := range benchmarks {
		b.Run(benchmark.name, func(b *testing.B) {
			node, err := NewLogicalNode(LogicalNodeSpec{
				Key: identity.LogicalNodeKey{
					Rule:        identity.RuleKey{RuleID: 1},
					PlacementID: "benchmark",
				},
				Config: LogicalNodeConfig{
					SeedScheduler: SeedSchedulerConfig{
						AttemptLimitPerMatchRound: 500,
						Order:                     benchmark.config,
					},
					Prefilter: prefilter.Config{Root: prefilter.None()},
				},
			})
			if err != nil {
				b.Fatal(err)
			}
			for index := 0; index < 100_000; index++ {
				if _, err := node.Add(&Ticket{
					TicketID:  uint64(index + 1),
					CreatedAt: int64(100_000 - index),
				}); err != nil {
					b.Fatal(err)
				}
			}
			// Warm both owned order buffers before measuring steady-state rounds.
			if err := node.BeginMatchRound(1); err != nil {
				b.Fatal(err)
			}
			if err := node.BeginMatchRound(2); err != nil {
				b.Fatal(err)
			}
			b.ResetTimer()
			for round := 0; round < b.N; round++ {
				if err := node.BeginMatchRound(int64(round + 3)); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
