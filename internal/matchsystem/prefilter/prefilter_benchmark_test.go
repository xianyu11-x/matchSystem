package prefilter

import (
	"math/rand"
	"runtime"
	"testing"

	"matchSystem/internal/common"
	"matchSystem/internal/matchsystem/contract"
)

const (
	prefilterBenchmarkTicketCount    = 100000
	prefilterBenchmarkWhitelist      = 10
	prefilterBenchmarkBlacklist      = 30
	prefilterBenchmarkRandSeed       = int64(prefilterBenchmarkTicketCount)*7919 + 17
	prefilterBenchmarkCandidateCount = 5464
)

var prefilterBenchmarkStats = Stats{
	LookupCalls:   4,
	ContainsCalls: 0,
	AndCalls:      1,
	OrCalls:       2,
	SubtractCalls: 1,
}

type prefilterBenchmarkTicketData struct {
	id    uint64
	level int64
	score int64
}

type prefilterBenchmarkWorkload struct {
	session   *TickSession
	seed      *common.Ticket
	seedDocID uint32
}

func BenchmarkPrefilterCandidates100k(b *testing.B) {
	b.Run("current-ticket-id-uint64-list", func(b *testing.B) {
		workload := mustPreparePrefilterBenchmark(b, benchmarkPrefilterCurrent)
		benchmarkPrefilterCandidates(b, workload)
	})
	b.Run("legacy-yes-string-marker", func(b *testing.B) {
		workload := mustPreparePrefilterBenchmark(b, benchmarkPrefilterLegacy)
		benchmarkPrefilterCandidates(b, workload)
	})
}

func benchmarkPrefilterCandidates(b *testing.B, workload prefilterBenchmarkWorkload) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		candidates, _, err := workload.session.CandidatesWithStats(workload.seedDocID, workload.seed, Facts{})
		if err != nil {
			b.Fatal(err)
		}
		candidates.Remove(workload.seedDocID)
		runtime.KeepAlive(candidates)
	}
	b.StopTimer()
}

func TestPrefilterBenchmarkWorkloads(t *testing.T) {
	for _, test := range []struct {
		name string
		kind benchmarkPrefilterKind
	}{
		{name: "current-ticket-id-uint64-list", kind: benchmarkPrefilterCurrent},
		{name: "legacy-yes-string-marker", kind: benchmarkPrefilterLegacy},
	} {
		t.Run(test.name, func(t *testing.T) {
			workload := mustPreparePrefilterBenchmark(t, test.kind)
			candidates, stats, err := workload.session.CandidatesWithStats(workload.seedDocID, workload.seed, Facts{})
			if err != nil {
				t.Fatalf("evaluate prefilter: %v", err)
			}
			candidates.Remove(workload.seedDocID)
			if got := candidates.Count(); got != prefilterBenchmarkCandidateCount {
				t.Fatalf("non-seed candidate count=%d, want %d", got, prefilterBenchmarkCandidateCount)
			}
			if stats != prefilterBenchmarkStats {
				t.Fatalf("prefilter stats=%+v, want %+v", stats, prefilterBenchmarkStats)
			}
			t.Logf("candidate count=%d stats=%+v", candidates.Count(), stats)
		})
	}
}

type benchmarkPrefilterKind uint8

const (
	benchmarkPrefilterCurrent benchmarkPrefilterKind = iota
	benchmarkPrefilterLegacy
)

func mustPreparePrefilterBenchmark(tb testing.TB, kind benchmarkPrefilterKind) prefilterBenchmarkWorkload {
	tb.Helper()
	planJSON, schema := benchmarkPrefilterPlan(kind)
	plan, err := CompileJSON(planJSON, schema)
	if err != nil {
		tb.Fatalf("compile prefilter plan: %v", err)
	}
	store, err := New(plan)
	if err != nil {
		tb.Fatalf("create prefilter store: %v", err)
	}
	data := buildPrefilterBenchmarkData()
	var seed *common.Ticket
	for index, value := range data {
		ticket := buildPrefilterBenchmarkTicket(value, index, kind)
		if index == 0 {
			seed = ticket
		}
		if err := store.Add(uint32(index+1), ticket); err != nil {
			tb.Fatalf("add ticket %d: %v", value.id, err)
		}
	}
	session, err := store.BeginTick(Facts{})
	if err != nil {
		tb.Fatalf("begin prefilter tick: %v", err)
	}
	workload := prefilterBenchmarkWorkload{session: session, seed: seed, seedDocID: 1}
	validatePrefilterBenchmarkStats(tb, workload)
	return workload
}

func validatePrefilterBenchmarkStats(tb testing.TB, workload prefilterBenchmarkWorkload) {
	tb.Helper()
	candidates, stats, err := workload.session.CandidatesWithStats(workload.seedDocID, workload.seed, Facts{})
	if err != nil {
		tb.Fatalf("validate prefilter plan: %v", err)
	}
	candidates.Remove(workload.seedDocID)
	if got := candidates.Count(); got != prefilterBenchmarkCandidateCount {
		tb.Fatalf("validated non-seed candidate count=%d, want %d", got, prefilterBenchmarkCandidateCount)
	}
	if stats != prefilterBenchmarkStats {
		tb.Fatalf("validated prefilter stats=%+v, want %+v", stats, prefilterBenchmarkStats)
	}
	tb.Logf("validated candidate count=%d stats=%+v", candidates.Count(), stats)
}

func buildPrefilterBenchmarkData() []prefilterBenchmarkTicketData {
	rng := rand.New(rand.NewSource(prefilterBenchmarkRandSeed))
	data := make([]prefilterBenchmarkTicketData, prefilterBenchmarkTicketCount)
	for index := range data {
		data[index] = prefilterBenchmarkTicketData{
			id:    uint64(index + 1),
			level: int64(rng.Intn(40) + 1),
			score: int64(rng.Intn(500) + 1),
		}
	}
	return data
}

func buildPrefilterBenchmarkTicket(value prefilterBenchmarkTicketData, index int, kind benchmarkPrefilterKind) *common.Ticket {
	ticket := &common.Ticket{
		TicketID:    value.id,
		CreatedAt:   int64(value.id),
		Int64Values: map[string]int64{"level": value.level, "score": value.score},
	}
	if kind == benchmarkPrefilterCurrent {
		ticket.Uint64Lists = map[string][]uint64{"ticketId": {value.id}}
		if index == 0 {
			ticket.Uint64Lists["whitelist"] = benchmarkPrefilterIDs(1, prefilterBenchmarkWhitelist)
			ticket.Uint64Lists["blacklist"] = benchmarkPrefilterIDs(prefilterBenchmarkWhitelist+1, prefilterBenchmarkBlacklist)
		}
		return ticket
	}
	ticket.StringLists = make(map[string][]string, 1)
	switch {
	case index < prefilterBenchmarkWhitelist:
		ticket.StringLists["whitelist"] = []string{"yes"}
	case index < prefilterBenchmarkWhitelist+prefilterBenchmarkBlacklist:
		ticket.StringLists["blacklist"] = []string{"yes"}
	}
	return ticket
}

func benchmarkPrefilterIDs(first, count int) []uint64 {
	values := make([]uint64, count)
	for index := range values {
		values[index] = uint64(first + index)
	}
	return values
}

func benchmarkPrefilterPlan(kind benchmarkPrefilterKind) ([]byte, contract.Contract) {
	if kind == benchmarkPrefilterLegacy {
		return benchmarkPrefilterLegacyJSON(), benchmarkPrefilterLegacySchema()
	}
	return benchmarkPrefilterCurrentJSON(), benchmarkPrefilterCurrentSchema()
}

func benchmarkPrefilterCurrentSchema() contract.Contract {
	return contract.Contract{
		Attributes: []contract.AttributeSpec{
			{Name: "ticketId", Type: contract.FactTypeUint64s, MaxValues: 1},
			{Name: "whitelist", Type: contract.FactTypeUint64s, MaxValues: prefilterBenchmarkWhitelist},
			{Name: "blacklist", Type: contract.FactTypeUint64s, MaxValues: prefilterBenchmarkBlacklist},
			{Name: "level", Type: contract.FactTypeInt64},
			{Name: "score", Type: contract.FactTypeInt64},
		},
		Indexes: []contract.IndexSpec{
			{Type: contract.IndexTypeMultiValue, Name: "ticketId", KeyType: contract.KeyTypeUint64, MaxDocumentValues: 1, MaxQueryValues: prefilterBenchmarkBlacklist},
			{Type: contract.IndexTypeInt64Range, Name: "level"},
			{Type: contract.IndexTypeInt64Range, Name: "score"},
		},
	}
}

func benchmarkPrefilterLegacySchema() contract.Contract {
	return contract.Contract{
		Attributes: []contract.AttributeSpec{
			{Name: "whitelist", Type: contract.FactTypeStrings, MaxValues: 1},
			{Name: "blacklist", Type: contract.FactTypeStrings, MaxValues: 1},
			{Name: "level", Type: contract.FactTypeInt64},
			{Name: "score", Type: contract.FactTypeInt64},
		},
		Indexes: []contract.IndexSpec{
			{Type: contract.IndexTypeMultiValue, Name: "whitelist", KeyType: contract.KeyTypeString, MaxDocumentValues: 1, MaxQueryValues: 1},
			{Type: contract.IndexTypeMultiValue, Name: "blacklist", KeyType: contract.KeyTypeString, MaxDocumentValues: 1, MaxQueryValues: 1},
			{Type: contract.IndexTypeInt64Range, Name: "level"},
			{Type: contract.IndexTypeInt64Range, Name: "score"},
		},
	}
}

func benchmarkPrefilterCurrentJSON() []byte {
	return []byte(`{
  "schemaVersion":"prefilter/v3",
  "bitmap":{"resultType":"bitmap","expr":{
    "op":"and","children":[
      {"op":"or","children":[
        {"op":"lookup_uint64","index":"ticketId","values":{"schemaVersion":"expression-scalar/v3","resultType":"uint64s","expr":{"op":"uint64s_ref","source":"seed_attributes","name":"whitelist"}}},
        {"op":"and","children":[
          {"op":"lookup_range","index":"level","min":{"schemaVersion":"expression-scalar/v3","resultType":"int64","expr":{"op":"int64_sub","left":{"op":"int64_ref","source":"seed_attributes","name":"level"},"right":{"op":"int64_literal","value":5}}},"max":{"schemaVersion":"expression-scalar/v3","resultType":"int64","expr":{"op":"int64_add","left":{"op":"int64_ref","source":"seed_attributes","name":"level"},"right":{"op":"int64_literal","value":5}}}},
          {"op":"lookup_range","index":"score","min":{"schemaVersion":"expression-scalar/v3","resultType":"int64","expr":{"op":"int64_sub","left":{"op":"int64_ref","source":"seed_attributes","name":"score"},"right":{"op":"int64_literal","value":50}}},"max":{"schemaVersion":"expression-scalar/v3","resultType":"int64","expr":{"op":"int64_add","left":{"op":"int64_ref","source":"seed_attributes","name":"score"},"right":{"op":"int64_literal","value":50}}}}
        ]}
      ]},
      {"op":"exclude","value":{"op":"lookup_uint64","index":"ticketId","values":{"schemaVersion":"expression-scalar/v3","resultType":"uint64s","expr":{"op":"uint64s_ref","source":"seed_attributes","name":"blacklist"}}}}
    ]
  }}
}`)
}

func benchmarkPrefilterLegacyJSON() []byte {
	return []byte(`{
  "schemaVersion":"prefilter/v3",
  "bitmap":{"resultType":"bitmap","expr":{
    "op":"and","children":[
      {"op":"or","children":[
        {"op":"lookup_string","index":"whitelist","values":{"schemaVersion":"expression-scalar/v3","resultType":"strings","expr":{"op":"strings_literal","values":["yes"]}}},
        {"op":"and","children":[
          {"op":"lookup_range","index":"level","min":{"schemaVersion":"expression-scalar/v3","resultType":"int64","expr":{"op":"int64_sub","left":{"op":"int64_ref","source":"seed_attributes","name":"level"},"right":{"op":"int64_literal","value":5}}},"max":{"schemaVersion":"expression-scalar/v3","resultType":"int64","expr":{"op":"int64_add","left":{"op":"int64_ref","source":"seed_attributes","name":"level"},"right":{"op":"int64_literal","value":5}}}},
          {"op":"lookup_range","index":"score","min":{"schemaVersion":"expression-scalar/v3","resultType":"int64","expr":{"op":"int64_sub","left":{"op":"int64_ref","source":"seed_attributes","name":"score"},"right":{"op":"int64_literal","value":50}}},"max":{"schemaVersion":"expression-scalar/v3","resultType":"int64","expr":{"op":"int64_add","left":{"op":"int64_ref","source":"seed_attributes","name":"score"},"right":{"op":"int64_literal","value":50}}}}
        ]}
      ]},
      {"op":"exclude","value":{"op":"lookup_string","index":"blacklist","values":{"schemaVersion":"expression-scalar/v3","resultType":"strings","expr":{"op":"strings_literal","values":["yes"]}}}}
    ]
  }}
}`)
}
