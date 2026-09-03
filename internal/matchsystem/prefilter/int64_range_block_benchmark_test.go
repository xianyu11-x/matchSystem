package prefilter

import (
	"fmt"
	"runtime"
	"testing"

	"matchSystem/internal/common"
	"matchSystem/internal/matchsystem/contract"
)

func BenchmarkInt64RangeBlockSizes100k(b *testing.B) {
	for _, blockSize := range []int{8, 16, 32} {
		b.Run(fmt.Sprintf("block-%d", blockSize), func(b *testing.B) {
			index := prepareInt64RangeBlockBenchmarkIndex(blockSize)
			queries := []struct {
				name  string
				query boundIndexQuery
			}{
				{name: "level-narrow", query: boundIndexQuery{kind: boundQueryRange, min: 17, max: 23}},
				{name: "score-medium", query: boundIndexQuery{kind: boundQueryRange, min: 200, max: 300}},
				{name: "score-wide", query: boundIndexQuery{kind: boundQueryRange, min: 100, max: 400}},
			}
			for _, test := range queries {
				b.Run(test.name+"-lookup", func(b *testing.B) {
					b.ReportAllocs()
					b.ResetTimer()
					for iteration := 0; iteration < b.N; iteration++ {
						result, err := index.lookup(test.query)
						if err != nil {
							b.Fatal(err)
						}
						runtime.KeepAlive(result)
					}
				})
				b.Run(test.name+"-estimate", func(b *testing.B) {
					b.ReportAllocs()
					b.ResetTimer()
					for iteration := 0; iteration < b.N; iteration++ {
						result, err := index.estimate(test.query)
						if err != nil {
							b.Fatal(err)
						}
						runtime.KeepAlive(result)
					}
				})
			}
		})
	}
}

func prepareInt64RangeBlockBenchmarkIndex(blockSize int) *int64RangeIndex {
	index := newInt64RangeIndex(indexSpec{
		name: "score",
		kind: contract.IndexTypeInt64Range,
	})
	index.blockSize = blockSize
	for docID := uint32(1); docID <= 100000; docID++ {
		value := int64((docID-1)%500 + 1)
		index.add(docID, &common.Ticket{Int64Values: map[string]int64{"score": value}})
	}
	index.prepare()
	return index
}
