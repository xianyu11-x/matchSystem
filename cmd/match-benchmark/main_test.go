package main

import (
	"strings"
	"testing"

	"matchSystem/internal/identity"
)

func TestBuildTicketsUsesTicketIDAttributesAndLists(t *testing.T) {
	tickets := buildTickets(whiteListSize + blackListSize)
	if len(tickets) != whiteListSize+blackListSize {
		t.Fatalf("ticket count=%d, want %d", len(tickets), whiteListSize+blackListSize)
	}

	seed := tickets[0]
	if got := seed.Uint64Lists["whitelist"]; len(got) != whiteListSize || got[0] != 1 || got[len(got)-1] != whiteListSize {
		t.Fatalf("seed whitelist=%v", got)
	}
	if got := seed.Uint64Lists["blacklist"]; len(got) != blackListSize || got[0] != whiteListSize+1 || got[len(got)-1] != whiteListSize+blackListSize {
		t.Fatalf("seed blacklist=%v", got)
	}
	for index, ticket := range tickets {
		want := uint64(index + 1)
		if got := ticket.Uint64Lists["ticketId"]; len(got) != 1 || got[0] != want {
			t.Fatalf("ticket %d ticketId attribute=%v, want [%d]", ticket.TicketID, got, want)
		}
	}
}

func TestBenchmarkRuleUsesTicketIDIndexForLists(t *testing.T) {
	rule := string(benchmarkRuleJSON(identity.RuleKey{Namespace: benchmarkRuleNamespace, RuleID: 1}, 1000, defaultMatchSize, 1))
	for _, fragment := range []string{
		`"name":"ticketId","type":"uint64s"`,
		`"name":"ticketId","keyType":"uint64"`,
		`"op":"lookup_uint64","index":"ticketId"`,
		`"op":"uint64s_ref","source":"seed_attributes","name":"whitelist"`,
		`"op":"uint64s_ref","source":"seed_attributes","name":"blacklist"`,
	} {
		if !strings.Contains(rule, fragment) {
			t.Fatalf("rule is missing %q", fragment)
		}
	}
	if strings.Contains(rule, `"yes"`) {
		t.Fatal("rule still contains shared yes/no list marker")
	}
}

func TestBenchmarkRuleUsesRequestedRoundAttemptLimit(t *testing.T) {
	rule := string(benchmarkRuleJSON(identity.RuleKey{Namespace: benchmarkRuleNamespace, RuleID: 1}, 1000, defaultMatchSize, 100000))
	if !strings.Contains(rule, `"attemptLimitPerProduceMatch":1`) {
		t.Fatal("benchmark must keep one seed attempt per ProduceMatch call")
	}
	if !strings.Contains(rule, `"attemptLimitPerMatchRound":100000`) {
		t.Fatal("benchmark round limit does not match the requested independent limit")
	}
}

func TestRunScaleSupportsTenProducesInOneRound(t *testing.T) {
	result, err := runScale(benchmarkConfig{
		samples:                   1,
		producesPerRound:          10,
		attemptLimitPerMatchRound: 10,
		matchSize:                 defaultMatchSize,
	}, 1000)
	if err != nil {
		t.Fatalf("run ten produces in one round: %v", err)
	}
	if len(result.samples) != 1 {
		t.Fatalf("samples=%d, want 1", len(result.samples))
	}
	sample := result.samples[0]
	if got := len(sample.produceCalls); got != 10 {
		t.Fatalf("ProduceMatch calls=%d, want 10", got)
	}
	if got := sample.successfulMatches + sample.failedCalls + sample.exhaustedCalls; got != 10 {
		t.Fatalf("outcome calls=%d, want 10 (sample=%+v)", got, sample)
	}
	if sample.successfulMatches+sample.failedCalls != 10 || sample.exhaustedCalls != 0 {
		t.Fatalf("unexpected outcomes with a 10-seed round stream: success=%d failed=%d exhausted=%d", sample.successfulMatches, sample.failedCalls, sample.exhaustedCalls)
	}
	if sample.consumedSeeds != 10 {
		t.Fatalf("consumed seeds=%d, want 10", sample.consumedSeeds)
	}
	if sample.remaining != result.remaining || sample.remaining != 1000-30*int(sample.successfulMatches) {
		t.Fatalf("remaining=%d, result remaining=%d, successes=%d", sample.remaining, result.remaining, sample.successfulMatches)
	}
}

func TestRunScaleSupportsTwentyProducesWithEightTicketMatches(t *testing.T) {
	const (
		poolSize     = 1000
		produces     = 20
		attemptLimit = 500
		matchSize    = 8
	)
	result, err := runScale(benchmarkConfig{
		samples:                   1,
		producesPerRound:          produces,
		attemptLimitPerMatchRound: attemptLimit,
		matchSize:                 matchSize,
	}, poolSize)
	if err != nil {
		t.Fatalf("run twenty %d-ticket produces in one round: %v", matchSize, err)
	}
	if len(result.samples) != 1 {
		t.Fatalf("samples=%d, want 1", len(result.samples))
	}
	sample := result.samples[0]
	if got := len(sample.produceCalls); got != produces {
		t.Fatalf("ProduceMatch calls=%d, want %d", got, produces)
	}
	if sample.successfulMatches != produces || sample.failedCalls != 0 || sample.exhaustedCalls != 0 {
		t.Fatalf("unexpected outcomes: success=%d failed=%d exhausted=%d", sample.successfulMatches, sample.failedCalls, sample.exhaustedCalls)
	}
	if sample.consumedSeeds != produces {
		t.Fatalf("consumed seeds=%d, want %d", sample.consumedSeeds, produces)
	}
	wantRemaining := poolSize - matchSize*produces
	if sample.remaining != result.remaining || sample.remaining != wantRemaining {
		t.Fatalf("remaining=%d, result remaining=%d, want %d", sample.remaining, result.remaining, wantRemaining)
	}
}

func TestBenchmarkRuleUsesRequestedMatchSize(t *testing.T) {
	rule := string(benchmarkRuleJSON(identity.RuleKey{Namespace: benchmarkRuleNamespace, RuleID: 1}, 1000, 8, 500))
	if !strings.Contains(rule, `"maxPlayers":8`) {
		t.Fatal("benchmark runtime maxPlayers does not match requested match size")
	}
	if !strings.Contains(rule, `"value":8}}`) {
		t.Fatal("benchmark canComplete threshold does not match requested match size")
	}
}
