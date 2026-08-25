package prefilter

import (
	"fmt"
	"testing"

	"matchSystem/internal/common"
)

func BenchmarkIndexStoreRoaring(b *testing.B) {
	for _, size := range []int{100_000, 500_000, 1_000_000} {
		b.Run(fmt.Sprintf("tickets=%d", size), func(b *testing.B) {
			config := Config{
				Indexes: []IndexSpec{NewMultiValueIndex(MultiValueIndexConfig{Name: "dimension", Field: "dimension", MaxDocumentValues: 4, MaxQueryValues: 4})},
				Root:    Lookup(StringQuery{Index: "dimension", Values: SeedStrings("dimension")}),
			}
			plan, err := Compile(config)
			if err != nil {
				b.Fatal(err)
			}
			store, err := New(plan)
			if err != nil {
				b.Fatal(err)
			}
			for id := 1; id <= size; id++ {
				ticket := &common.Ticket{TicketID: uint64(id), StringLists: map[string][]string{"dimension": {fmt.Sprintf("key-%d", id%1000)}}}
				if err := store.Add(uint32(id), ticket); err != nil {
					b.Fatal(err)
				}
			}
			seed := &common.Ticket{TicketID: 1, StringLists: map[string][]string{"dimension": {"key-1"}}}
			session, err := store.BeginTick(Facts{})
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := session.Candidates(1, seed, Facts{}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
