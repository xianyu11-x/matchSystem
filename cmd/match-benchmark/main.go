// Command match-benchmark measures one complete LogicalNode matching attempt
// for a range of waiting-pool sizes. It intentionally keeps pool population
// out of the match timing so the result describes the hot path after tickets
// have entered the pool.
package main

import (
	"context"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"matchSystem/internal/identity"
	"matchSystem/internal/matchsystem"
)

const (
	defaultSizes   = "1000,5000,10000,25000,50000,75000,100000"
	defaultSamples = 10
	defaultWarmups = 2

	whiteListSize      = 10
	blackListSize      = 30
	maxListQueryValues = blackListSize
	levelMin           = 1
	levelMax           = 40
	scoreMin           = 1
	scoreMax           = 500
)

const benchmarkRuleNamespace = "performance"

type benchmarkConfig struct {
	sizes   []int
	samples int
	warmups int
}

type sampleResult struct {
	setup   time.Duration
	round   time.Duration
	produce time.Duration
	heap    uint64
	metrics matchsystem.ProduceMatchMetrics
}

type durationSummary struct {
	min  time.Duration
	mean time.Duration
	p50  time.Duration
	p95  time.Duration
	max  time.Duration
}

type scaleResult struct {
	size           int
	samples        []sampleResult
	candidateCount int
	matchSize      int
	remaining      int
}

func main() {
	var sizesText string
	var samples, warmups int
	flag.StringVar(&sizesText, "sizes", defaultSizes, "comma-separated waiting-pool sizes")
	flag.IntVar(&samples, "samples", defaultSamples, "measured samples per size")
	flag.IntVar(&warmups, "warmups", defaultWarmups, "discarded warmup samples per size")
	flag.Parse()

	sizes, err := parseSizes(sizesText)
	if err != nil {
		fatalf("parse -sizes: %v", err)
	}
	if samples <= 0 {
		fatalf("-samples must be greater than zero")
	}
	if warmups < 0 {
		fatalf("-warmups must not be negative")
	}

	config := benchmarkConfig{sizes: sizes, samples: samples, warmups: warmups}
	fmt.Printf("match-benchmark\n")
	fmt.Printf("go=%s os=%s arch=%s cpus=%d gomaxprocs=%d\n", runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.NumCPU(), runtime.GOMAXPROCS(0))
	fmt.Printf("samples=%d warmups=%d sizes=%v\n", config.samples, config.warmups, config.sizes)
	fmt.Printf("rule=whitelist(ticketId 1-%d) blacklist(ticketId %d-%d) [blacklist-priority, lists-disjoint] level[%d,%d] score[%d,%d] matchSize=30 canJoin=always score=1\n", whiteListSize, whiteListSize+1, whiteListSize+blackListSize, levelMin, levelMax, scoreMin, scoreMax)
	fmt.Println()
	fmt.Println("size  candidates  match  remaining  setup p50/p95(ms)  round p50/p95(ms)  produce p50/p95(ms)  total p50/p95(ms)  heap(MiB)")
	fmt.Println("----  ----------  -----  ---------  -----------------  -----------------  -------------------  -----------------  ---------")

	results := make([]scaleResult, 0, len(config.sizes))
	for _, size := range config.sizes {
		result, err := runScale(config, size)
		if err != nil {
			fatalf("pool size %d: %v", size, err)
		}
		results = append(results, result)
		printScale(result)
	}
	fmt.Println()
	printStageBreakdown(results)
}

func parseSizes(value string) ([]int, error) {
	parts := strings.Split(value, ",")
	if len(parts) == 0 {
		return nil, fmt.Errorf("no sizes supplied")
	}
	seen := make(map[int]struct{}, len(parts))
	sizes := make([]int, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, fmt.Errorf("empty size")
		}
		size, err := strconv.Atoi(part)
		if err != nil || size <= 0 {
			return nil, fmt.Errorf("invalid size %q", part)
		}
		if _, exists := seen[size]; exists {
			continue
		}
		seen[size] = struct{}{}
		sizes = append(sizes, size)
	}
	sort.Ints(sizes)
	return sizes, nil
}

func runScale(config benchmarkConfig, size int) (scaleResult, error) {
	tickets := buildTickets(size)
	candidateCount := expectedCandidateCount(tickets)
	result := scaleResult{size: size, candidateCount: candidateCount}

	for iteration := 0; iteration < config.warmups+config.samples; iteration++ {
		// Each sample gets a fresh node because ProduceMatch commits its 30
		// members. Population is setup work and is not included in the match
		// timing below.
		runtime.GC()
		node, setup, heapBytes, err := prepareNode(tickets, size)
		if err != nil {
			return scaleResult{}, err
		}

		roundStart := time.Now()
		if err := node.BeginMatchRound(1); err != nil {
			return scaleResult{}, fmt.Errorf("begin match round: %w", err)
		}
		roundDuration := time.Since(roundStart)

		produceStart := time.Now()
		match, metrics, err := node.ProduceMatchWithMetrics(context.Background())
		produceDuration := time.Since(produceStart)
		if err != nil {
			return scaleResult{}, fmt.Errorf("produce match: %w", err)
		}
		if err := validateMatch(match); err != nil {
			return scaleResult{}, err
		}
		if got := node.Len(); got != size-30 {
			return scaleResult{}, fmt.Errorf("commit removed %d tickets; want 30 (remaining=%d, want=%d)", size-got, got, size-30)
		}

		result.matchSize = len(match.Tickets)
		result.remaining = node.Len()
		if iteration >= config.warmups {
			result.samples = append(result.samples, sampleResult{
				setup:   setup,
				round:   roundDuration,
				produce: produceDuration,
				heap:    heapBytes,
				metrics: metrics,
			})
		}
	}
	return result, nil
}

func prepareNode(tickets []*matchsystem.Ticket, size int) (*matchsystem.LogicalNode, time.Duration, uint64, error) {
	key := identity.LogicalNodeKey{
		Rule:        identity.RuleKey{Namespace: benchmarkRuleNamespace, RuleID: 1},
		PlacementID: identity.PlacementID(fmt.Sprintf("pool-%d", size)),
	}
	node, err := matchsystem.NewLogicalNode(matchsystem.LogicalNodeSpec{
		Key:               key,
		RuleJSON:          benchmarkRuleJSON(key.Rule, size),
		MatchFactProvider: benchmarkMatchFactProvider{},
		MatchFactProviderDescriptor: &matchsystem.ProviderDescriptor{
			ID:      "match-benchmark.party-size",
			Version: "v1",
			Facts: []matchsystem.FactSpec{{
				Name:  "party-size",
				Type:  matchsystem.FactTypeInt64,
				Scope: matchsystem.FactScopeMatch,
			}},
		},
	})
	if err != nil {
		return nil, 0, 0, fmt.Errorf("create LogicalNode: %w", err)
	}
	setupStart := time.Now()
	for _, ticket := range tickets {
		if _, err := node.Add(ticket); err != nil {
			return nil, 0, 0, fmt.Errorf("add ticket %d: %w", ticket.TicketID, err)
		}
	}
	setupDuration := time.Since(setupStart)
	// Force the setup heap snapshot to exclude garbage left by a previous
	// sample. This is diagnostic only and does not affect the hot-path timers.
	runtime.GC()
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return node, setupDuration, stats.HeapAlloc, nil
}

func buildTickets(size int) []*matchsystem.Ticket {
	rng := rand.New(rand.NewSource(int64(size)*7919 + 17))
	tickets := make([]*matchsystem.Ticket, size)
	whitelist := ticketIDs(1, whiteListSize)
	blacklist := ticketIDs(whiteListSize+1, whiteListSize+blackListSize)
	for index := 0; index < size; index++ {
		id := matchsystem.TicketID(index + 1)
		uint64Lists := map[string][]uint64{
			// TicketID is metadata on common.Ticket, so expose the same identity
			// as a declared attribute for the rule's indexed lookup.
			"ticketId": {uint64(id)},
		}
		if index == 0 {
			// The first arrival is the seed for this benchmark. Its lists contain
			// the actual candidate TicketIDs, rather than a shared yes/no marker.
			uint64Lists["whitelist"] = append([]uint64(nil), whitelist...)
			uint64Lists["blacklist"] = append([]uint64(nil), blacklist...)
		}
		tickets[index] = &matchsystem.Ticket{
			TicketID:    id,
			CreatedAt:   int64(id),
			Uint64Lists: uint64Lists,
			Int64Values: map[string]int64{
				"level": int64(rng.Intn(levelMax-levelMin+1) + levelMin),
				"score": int64(rng.Intn(scoreMax-scoreMin+1) + scoreMin),
			},
		}
	}
	return tickets
}

func ticketIDs(first, last int) []uint64 {
	if last < first {
		return nil
	}
	ids := make([]uint64, last-first+1)
	for index := range ids {
		ids[index] = uint64(first + index)
	}
	return ids
}

func expectedCandidateCount(tickets []*matchsystem.Ticket) int {
	if len(tickets) == 0 {
		return 0
	}
	seed := tickets[0]
	seedLevel := seed.Int64Values["level"]
	seedScore := seed.Int64Values["score"]
	count := 0
	for _, ticket := range tickets {
		if ticket == nil || isBlacklisted(ticket.TicketID) {
			continue
		}
		if isWhitelisted(ticket.TicketID) || (within(seedLevel, ticket.Int64Values["level"], 5) && within(seedScore, ticket.Int64Values["score"], 50)) {
			count++
		}
	}
	return count
}

func within(left, right, delta int64) bool {
	return right >= left-delta && right <= left+delta
}

func isWhitelisted(ticketID matchsystem.TicketID) bool {
	return ticketID >= 1 && ticketID <= whiteListSize
}

func isBlacklisted(ticketID matchsystem.TicketID) bool {
	return ticketID > whiteListSize && ticketID <= whiteListSize+blackListSize
}

func validateMatch(match *matchsystem.Match) error {
	if match == nil {
		return fmt.Errorf("ProduceMatch returned nil")
	}
	if len(match.Tickets) != 30 {
		return fmt.Errorf("match size=%d, want 30", len(match.Tickets))
	}
	seen := make(map[matchsystem.TicketID]struct{}, len(match.Tickets))
	for _, ticket := range match.Tickets {
		if ticket == nil {
			return fmt.Errorf("match contains nil ticket")
		}
		if _, exists := seen[ticket.TicketID]; exists {
			return fmt.Errorf("match contains duplicate ticket %d", ticket.TicketID)
		}
		seen[ticket.TicketID] = struct{}{}
		if isBlacklisted(ticket.TicketID) {
			return fmt.Errorf("match contains blacklisted ticket %d", ticket.TicketID)
		}
		if values := ticket.Uint64Lists["ticketId"]; len(values) != 1 || values[0] != uint64(ticket.TicketID) {
			return fmt.Errorf("ticket %d has mismatched ticketId attribute", ticket.TicketID)
		}
	}
	for id := 1; id <= whiteListSize; id++ {
		if !isWhitelisted(matchsystem.TicketID(id)) {
			continue
		}
		if _, exists := seen[matchsystem.TicketID(id)]; !exists {
			return fmt.Errorf("match omitted whitelisted ticket %d", id)
		}
	}
	return nil
}

func printScale(result scaleResult) {
	setup := summarize(result.samples, func(sample sampleResult) time.Duration { return sample.setup })
	round := summarize(result.samples, func(sample sampleResult) time.Duration { return sample.round })
	produce := summarize(result.samples, func(sample sampleResult) time.Duration { return sample.produce })
	total := make([]sampleResult, len(result.samples))
	copy(total, result.samples)
	for index := range total {
		total[index].produce += total[index].round
	}
	totalSummary := summarize(total, func(sample sampleResult) time.Duration { return sample.produce })
	heap := medianUint64(result.samples, func(sample sampleResult) uint64 { return sample.heap })
	fmt.Printf("%-5d %-11d %-6d %-10d %-15s %-16s %-17s %-16s %.1f\n",
		result.size,
		result.candidateCount,
		result.matchSize,
		result.remaining,
		formatSummary(setup),
		formatSummary(round),
		formatSummary(produce),
		formatSummary(totalSummary),
		float64(heap)/(1024*1024))
}

type metricStage struct {
	name  string
	value func(matchsystem.ProduceMatchMetrics) time.Duration
}

var produceMetricStages = []metricStage{
	{name: "seed preparation", value: func(metrics matchsystem.ProduceMatchMetrics) time.Duration { return metrics.SeedPreparation }},
	{name: "session preparation", value: func(metrics matchsystem.ProduceMatchMetrics) time.Duration { return metrics.SessionPreparation }},
	{name: "attempt preparation", value: func(metrics matchsystem.ProduceMatchMetrics) time.Duration { return metrics.AttemptPreparation }},
	{name: "prefilter", value: func(metrics matchsystem.ProduceMatchMetrics) time.Duration { return metrics.Prefilter }},
	{name: "candidate ranking", value: func(metrics matchsystem.ProduceMatchMetrics) time.Duration { return metrics.CandidateRanking }},
	{name: "candidate materialization", value: func(metrics matchsystem.ProduceMatchMetrics) time.Duration { return metrics.CandidateMaterialization }},
	{name: "Object Fact refresh", value: func(metrics matchsystem.ProduceMatchMetrics) time.Duration { return metrics.ObjectFactRefresh }},
	{name: "Object Fact provider", value: func(metrics matchsystem.ProduceMatchMetrics) time.Duration { return metrics.ObjectFactProvider }},
	{name: "candidate scoring", value: func(metrics matchsystem.ProduceMatchMetrics) time.Duration { return metrics.CandidateScoring }},
	{name: "candidate sort", value: func(metrics matchsystem.ProduceMatchMetrics) time.Duration { return metrics.CandidateSort }},
	{name: "canJoin", value: func(metrics matchsystem.ProduceMatchMetrics) time.Duration { return metrics.CanJoin }},
	{name: "Match Fact update", value: func(metrics matchsystem.ProduceMatchMetrics) time.Duration { return metrics.MatchFactUpdate }},
	{name: "canComplete", value: func(metrics matchsystem.ProduceMatchMetrics) time.Duration { return metrics.CanComplete }},
	{name: "match build", value: func(metrics matchsystem.ProduceMatchMetrics) time.Duration { return metrics.MatchBuild }},
	{name: "commit", value: func(metrics matchsystem.ProduceMatchMetrics) time.Duration { return metrics.Commit }},
}

func printStageBreakdown(results []scaleResult) {
	fmt.Println("ProduceMatch stage breakdown (p50/p95 duration; sub-ms stages shown in µs; share is per-sample stage/ProduceMatch p50/p95)")
	fmt.Println("size  stage                         duration p50/p95  share p50/p95")
	fmt.Println("----  ----------------------------  -----------------  --------------")
	for _, result := range results {
		for _, stage := range produceMetricStages {
			duration := summarize(result.samples, func(sample sampleResult) time.Duration {
				return stage.value(sample.metrics)
			})
			ratio := summarizeRatio(result.samples, stage.value)
			fmt.Printf("%-5d %-28s  %-17s  %5.1f%%/%5.1f%%\n",
				result.size, stage.name, formatStageSummary(duration), ratio.p50, ratio.p95)
		}
	}
	fmt.Println()
	fmt.Println("ProduceMatch aggregate counters (p50/p95 per sample)")
	fmt.Println("size  seeds  prefilter-candidates  candidate-visited  materialized  scored  obj-refresh  obj-provider  obj-cache  obj-growth  obj-errors  canJoin  joined  fact-updates  canComplete  commits")
	fmt.Println("----  -----  --------------------  -----------------  ------------  ------  -----------  ------------  ---------  ----------  ----------  -------  ------  ------------  -----------  -------")
	for _, result := range results {
		fmt.Printf("%-5d %-6s %-22s %-18s %-13s %-7s %-12s %-13s %-9s %-10s %-10s %-8s %-8s %-13s %-12s %-8s\n",
			result.size,
			formatCountSummary(result.samples, func(sample sampleResult) uint64 { return sample.metrics.SeedAttempts }),
			formatCountSummary(result.samples, func(sample sampleResult) uint64 { return sample.metrics.PrefilterCandidates }),
			formatCountSummary(result.samples, func(sample sampleResult) uint64 { return sample.metrics.CandidateVisited }),
			formatCountSummary(result.samples, func(sample sampleResult) uint64 { return sample.metrics.CandidateMaterializationCalls }),
			formatCountSummary(result.samples, func(sample sampleResult) uint64 { return sample.metrics.CandidateScoringCalls }),
			formatCountSummary(result.samples, func(sample sampleResult) uint64 { return sample.metrics.ObjectFactRefreshes }),
			formatCountSummary(result.samples, func(sample sampleResult) uint64 { return sample.metrics.ObjectFactProviderCalls }),
			formatCountSummary(result.samples, func(sample sampleResult) uint64 { return sample.metrics.ObjectFactCacheHits }),
			formatCountSummary(result.samples, func(sample sampleResult) uint64 { return sample.metrics.ObjectFactCapacityGrowths }),
			formatCountSummary(result.samples, func(sample sampleResult) uint64 { return sample.metrics.ObjectFactErrors }),
			formatCountSummary(result.samples, func(sample sampleResult) uint64 { return sample.metrics.CanJoinCalls }),
			formatCountSummary(result.samples, func(sample sampleResult) uint64 { return sample.metrics.JoinedCandidates }),
			formatCountSummary(result.samples, func(sample sampleResult) uint64 { return sample.metrics.MatchFactUpdateCalls }),
			formatCountSummary(result.samples, func(sample sampleResult) uint64 { return sample.metrics.CanCompleteCalls }),
			formatCountSummary(result.samples, func(sample sampleResult) uint64 { return sample.metrics.CommitCalls }))
	}
}

func formatStageSummary(summary durationSummary) string {
	if summary.max > 0 && summary.max < time.Millisecond {
		return fmt.Sprintf("%.1fµs/%.1fµs",
			float64(summary.p50)/float64(time.Microsecond),
			float64(summary.p95)/float64(time.Microsecond))
	}
	return formatSummary(summary)
}

type ratioSummary struct {
	p50 float64
	p95 float64
}

func summarizeRatio(samples []sampleResult, value func(matchsystem.ProduceMatchMetrics) time.Duration) ratioSummary {
	ratios := make([]float64, 0, len(samples))
	for _, sample := range samples {
		if sample.produce <= 0 {
			ratios = append(ratios, 0)
			continue
		}
		ratios = append(ratios, float64(value(sample.metrics))/float64(sample.produce)*100)
	}
	sort.Float64s(ratios)
	if len(ratios) == 0 {
		return ratioSummary{}
	}
	p95Index := int(0.95*float64(len(ratios)-1) + 0.5)
	return ratioSummary{p50: ratios[len(ratios)/2], p95: ratios[p95Index]}
}

type countSummary struct {
	p50 uint64
	p95 uint64
}

func summarizeCounts(samples []sampleResult, value func(sampleResult) uint64) countSummary {
	values := make([]uint64, 0, len(samples))
	for _, sample := range samples {
		values = append(values, value(sample))
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	if len(values) == 0 {
		return countSummary{}
	}
	p95Index := int(0.95*float64(len(values)-1) + 0.5)
	return countSummary{p50: values[len(values)/2], p95: values[p95Index]}
}

func formatCountSummary(samples []sampleResult, value func(sampleResult) uint64) string {
	summary := summarizeCounts(samples, value)
	return fmt.Sprintf("%d/%d", summary.p50, summary.p95)
}

func summarize(samples []sampleResult, value func(sampleResult) time.Duration) durationSummary {
	values := make([]time.Duration, 0, len(samples))
	for _, sample := range samples {
		values = append(values, value(sample))
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	if len(values) == 0 {
		return durationSummary{}
	}
	var total time.Duration
	for _, current := range values {
		total += current
	}
	p95Index := int(0.95*float64(len(values)-1) + 0.5)
	return durationSummary{
		min:  values[0],
		mean: total / time.Duration(len(values)),
		p50:  values[len(values)/2],
		p95:  values[p95Index],
		max:  values[len(values)-1],
	}
}

func medianUint64(samples []sampleResult, value func(sampleResult) uint64) uint64 {
	values := make([]uint64, 0, len(samples))
	for _, sample := range samples {
		values = append(values, value(sample))
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	if len(values) == 0 {
		return 0
	}
	return values[len(values)/2]
}

func formatSummary(summary durationSummary) string {
	return fmt.Sprintf("%s/%s", formatMillis(summary.p50), formatMillis(summary.p95))
}

func formatMillis(value time.Duration) string {
	return fmt.Sprintf("%.3f", float64(value)/float64(time.Millisecond))
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", args...)
	os.Exit(1)
}

func benchmarkRuleJSON(key identity.RuleKey, size int) []byte {
	return []byte(fmt.Sprintf(`{
  "schemaVersion":"match-rule/v1",
  "ruleKey":{"namespace":%q,"ruleId":%d},
	"contract":{
	    "schemaVersion":"logical-node-contract/v3",
	    "attributes":[
	      {"name":"ticketId","type":"uint64s","maxValues":1},
	      {"name":"whitelist","type":"uint64s","maxValues":%d},
	      {"name":"blacklist","type":"uint64s","maxValues":%d},
	      {"name":"level","type":"int64"},
	      {"name":"score","type":"int64"}
	    ],
	    "facts":[{"name":"party-size","type":"int64","scope":"match"}],
	    "indexes":[
	      {"type":"multi_value","name":"ticketId","keyType":"uint64","maxDocumentValues":1,"maxQueryValues":%d},
	      {"type":"int64_range","name":"level"},
	      {"type":"int64_range","name":"score"}
    ]
  },
  "prefilter":{"schemaVersion":"prefilter/v3","bitmap":{"resultType":"bitmap","expr":{
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
  }}},
  "evaluation":{
    "schemaVersion":"evaluation/v3",
    "canJoin":{"schemaVersion":"expression-scalar/v3","resultType":"bool","expr":{"op":"bool_literal","value":true}},
    "canComplete":{"schemaVersion":"expression-scalar/v3","resultType":"bool","expr":{"op":"int64_gte","left":{"op":"int64_ref","source":"match_facts","name":"party-size"},"right":{"op":"int64_literal","value":30}}}
  },
  "scoring":{"type":"constant","params":{"value":1}},
  "seedSelection":{"type":"arrival","params":{}},
  "runtime":{"candidateScoringLimitPerSeed":500,"candidateLimitPerSeed":%d,"maxPlayers":30,"attemptLimitPerProduceMatch":1,"attemptLimitPerMatchRound":1}
}`, key.Namespace, key.RuleID, whiteListSize, blackListSize, maxListQueryValues, size))
}

type benchmarkMatchFactProvider struct{}

func (benchmarkMatchFactProvider) Initialize(context.Context, matchsystem.InitializeInput) (matchsystem.Facts, error) {
	return matchsystem.Facts{Int64Values: map[string]int64{"party-size": 1}}, nil
}

func (benchmarkMatchFactProvider) OnJoin(_ context.Context, input matchsystem.JoinInput) (matchsystem.Facts, error) {
	return matchsystem.Facts{Int64Values: map[string]int64{"party-size": input.MatchFactsBefore.Int64Values["party-size"] + 1}}, nil
}
