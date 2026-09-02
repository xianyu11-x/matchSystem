package matchsystem

import (
	"fmt"
	"testing"
)

func BenchmarkSeedOrderRuntimeBuildRound100kLimit1(b *testing.B) {
	tests := []struct {
		name   string
		config SeedOrderPolicyConfig
	}{
		{name: "arrival", config: SeedOrderPolicyConfig{Kind: SeedOrderArrival}},
		{name: "oldest", config: SeedOrderPolicyConfig{Kind: SeedOrderOldest}},
		{name: "priority", config: SeedOrderPolicyConfig{
			Kind:              SeedOrderInt64Priority,
			PriorityField:     "priority",
			PriorityDirection: SeedPriorityDescending,
		}},
		{name: "random", config: SeedOrderPolicyConfig{Kind: SeedOrderRandom, RandomSeed: 1}},
	}
	for _, test := range tests {
		b.Run(test.name, func(b *testing.B) {
			runtime, err := NewSeedOrderPolicy(test.config)
			if err != nil {
				b.Fatal(err)
			}
			for id := TicketID(1); id <= 100000; id++ {
				runtime.Add(&Ticket{
					TicketID:  id,
					CreatedAt: int64(id),
					Int64Values: map[string]int64{
						"priority": int64(id),
					},
				})
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				order, err := runtime.BuildRound(1)
				if err != nil || len(order) != 1 {
					b.Fatalf("BuildRound(1) returned %v, err=%v", order, err)
				}
			}
		})
	}
}

func BenchmarkSeedOrderRuntimeBuildRoundLimit(b *testing.B) {
	for _, limit := range []int{1, 32, 256} {
		b.Run(fmt.Sprintf("limit-%d", limit), func(b *testing.B) {
			runtime, err := NewSeedOrderPolicy(SeedOrderPolicyConfig{Kind: SeedOrderArrival})
			if err != nil {
				b.Fatal(err)
			}
			for id := TicketID(1); id <= 100000; id++ {
				runtime.Add(&Ticket{TicketID: id})
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				order, err := runtime.BuildRound(limit)
				if err != nil || len(order) != limit {
					b.Fatalf("BuildRound(%d) returned %v, err=%v", limit, order, err)
				}
			}
		})
	}
}
