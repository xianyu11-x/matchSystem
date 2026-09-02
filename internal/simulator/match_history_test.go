package simulator

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"testing"

	"matchSystem/internal/common"
	"matchSystem/internal/matchsystem"
	"matchSystem/internal/matchsystem/fact"
)

func TestMatchHistoryIsBoundedAndDetached(t *testing.T) {
	scenario, key := testScenario()
	scenario.MatchHistoryLimit = 2
	sim, err := NewSimulator(scenario)
	if err != nil {
		t.Fatalf("NewSimulator: %v", err)
	}
	defer sim.Close()

	ctx := context.Background()
	for ticketID := uint64(1); ticketID <= 3; ticketID++ {
		if _, err := sim.AddTicket(ctx, TicketInput{
			Rule:      key.Rule,
			TicketID:  ticketID,
			CreatedAt: int64(ticketID),
			ObjectFacts: FactSnapshot{StringLists: map[string][]string{
				"object_tag": {"member"},
			}},
		}); err != nil {
			t.Fatalf("AddTicket(%d): %v", ticketID, err)
		}
	}
	if err := sim.BeginRound(ctx, 100); err != nil {
		t.Fatalf("BeginRound: %v", err)
	}
	result, err := sim.ProduceAll(ctx, 0)
	if err != nil {
		t.Fatalf("ProduceAll: %v", err)
	}
	if len(result.Matches) != 3 {
		t.Fatalf("produced matches=%d, want 3", len(result.Matches))
	}
	if got := []int64{result.Matches[0].DurationMs, result.Matches[1].DurationMs, result.Matches[2].DurationMs}; !reflect.DeepEqual(got, []int64{99, 98, 97}) {
		t.Fatalf("match wait durations=%v, want [99 98 97]", got)
	}

	page, err := sim.ListMatches(ctx, MatchQuery{Limit: 10})
	if err != nil {
		t.Fatalf("ListMatches: %v", err)
	}
	if page.Total != 2 || len(page.Items) != 2 {
		t.Fatalf("bounded history page=%#v, want two retained records", page)
	}
	if page.Items[0].ID != result.Matches[2].ID || page.Items[1].ID != result.Matches[1].ID {
		t.Fatalf("retained match order=%q,%q", page.Items[0].ID, page.Items[1].ID)
	}
	firstPage, err := sim.ListMatches(ctx, MatchQuery{Limit: 1})
	if err != nil || len(firstPage.Items) != 1 || firstPage.Items[0].ID != result.Matches[2].ID || firstPage.NextCursor != "1" {
		t.Fatalf("newest-first first page=%#v err=%v", firstPage, err)
	}
	secondPage, err := sim.ListMatches(ctx, MatchQuery{Cursor: firstPage.NextCursor, Limit: 1})
	if err != nil || len(secondPage.Items) != 1 || secondPage.Items[0].ID != result.Matches[1].ID || secondPage.NextCursor != "" {
		t.Fatalf("newest-first cursor page=%#v err=%v", secondPage, err)
	}
	if _, ok, err := sim.GetMatch(ctx, result.Matches[0].ID); err != nil || ok {
		t.Fatalf("evicted match lookup: ok=%v err=%v", ok, err)
	}

	copyOfMatch, ok, err := sim.GetMatch(ctx, result.Matches[1].ID)
	if err != nil || !ok {
		t.Fatalf("GetMatch: ok=%v err=%v", ok, err)
	}
	copyOfMatch.Tickets[0].ObjectFacts.StringLists["object_tag"][0] = "mutated"
	copyOfMatch.Tickets = append(copyOfMatch.Tickets, copyOfMatch.Tickets[0])
	retained, ok, err := sim.GetMatch(ctx, result.Matches[1].ID)
	if err != nil || !ok {
		t.Fatalf("GetMatch after mutation: ok=%v err=%v", ok, err)
	}
	if len(retained.Tickets) != 1 || retained.Tickets[0].ObjectFacts.StringLists["object_tag"][0] != "member" {
		t.Fatalf("Match snapshot leaked mutable state: %#v", retained)
	}

	if err := sim.ReplaceScenario(ctx, scenario); err != nil {
		t.Fatalf("ReplaceScenario: %v", err)
	}
	reset, err := sim.ListMatches(ctx, MatchQuery{Limit: 10})
	if err != nil {
		t.Fatalf("ListMatches after replacement: %v", err)
	}
	if reset.Total != 0 || len(reset.Items) != 0 {
		t.Fatalf("scenario replacement retained old Match history: %#v", reset)
	}
	if _, err := sim.AddTicket(ctx, TicketInput{
		Rule: key.Rule, TicketID: 99, CreatedAt: 200,
		ObjectFacts: FactSnapshot{StringLists: map[string][]string{"object_tag": {"new-scenario"}}},
	}); err != nil {
		t.Fatalf("AddTicket after replacement: %v", err)
	}
	if err := sim.BeginRound(ctx, 200); err != nil {
		t.Fatalf("BeginRound after replacement: %v", err)
	}
	newResult, err := sim.ProduceMatch(ctx)
	if err != nil || newResult.Match == nil {
		t.Fatalf("ProduceMatch after replacement: match=%#v err=%v", newResult.Match, err)
	}
	for _, oldMatch := range result.Matches {
		if newResult.Match.ID == oldMatch.ID {
			t.Fatalf("Match ID was reused across Scenario replacement: old=%q new=%q", oldMatch.ID, newResult.Match.ID)
		}
		if _, ok, err := sim.GetMatch(ctx, oldMatch.ID); err != nil || ok {
			t.Fatalf("old Match ID resolved after replacement: id=%q ok=%v err=%v", oldMatch.ID, ok, err)
		}
	}
}

func TestMatchWaitDurationClampsFutureAndOverflow(t *testing.T) {
	if got := matchWaitDuration(100, []*common.Ticket{{CreatedAt: 120}, {CreatedAt: 110}}); got != 0 {
		t.Fatalf("future ticket duration=%d, want 0", got)
	}
	if got := matchWaitDuration(100, nil); got != 0 {
		t.Fatalf("empty match duration=%d, want 0", got)
	}
	if got := matchWaitDuration(math.MaxInt64, []*common.Ticket{{CreatedAt: -1}}); got != math.MaxInt64 {
		t.Fatalf("overflow duration=%d, want MaxInt64", got)
	}
}

func TestMatchHistoryLimitRejectsNegativeScenarioValue(t *testing.T) {
	scenario, _ := testScenario()
	scenario.MatchHistoryLimit = -1
	report := ValidateScenario(scenario)
	if report.Valid {
		t.Fatal("ValidateScenario accepted negative matchHistoryLimit")
	}
	for _, issue := range report.Issues {
		if issue.Code == "INVALID_MATCH_HISTORY_LIMIT" {
			return
		}
	}
	t.Fatalf("missing match history limit issue: %#v", report.Issues)
}

func TestZeroValueSimulatorReplacementInitializesMatchID(t *testing.T) {
	scenario, key := testScenario()
	var sim Simulator
	ctx := context.Background()
	if err := sim.ReplaceScenario(ctx, scenario); err != nil {
		t.Fatalf("ReplaceScenario on zero-value Simulator: %v", err)
	}
	defer sim.Close()
	if _, err := sim.AddTicket(ctx, TicketInput{Rule: key.Rule, TicketID: 1, CreatedAt: 10}); err != nil {
		t.Fatalf("AddTicket: %v", err)
	}
	if err := sim.BeginRound(ctx, 100); err != nil {
		t.Fatalf("BeginRound: %v", err)
	}
	result, err := sim.ProduceMatch(ctx)
	if err != nil || result.Match == nil {
		t.Fatalf("ProduceMatch: match=%#v err=%v", result.Match, err)
	}
	if result.Match.ID != "match-1" {
		t.Fatalf("zero-value Simulator Match ID=%q, want match-1", result.Match.ID)
	}
}

func TestInitializedZeroMatchIDCounterReportsOverflow(t *testing.T) {
	sim, err := NewSimulator(Scenario{})
	if err != nil {
		t.Fatalf("NewSimulator: %v", err)
	}
	defer sim.Close()
	for _, counter := range []uint64{0, math.MaxUint64} {
		sim.mu.Lock()
		sim.nextMatchID = counter
		sim.matchIDInitialized = true
		_, err = sim.allocateMatchID()
		sim.mu.Unlock()
		if err == nil {
			t.Fatalf("initialized Match ID counter %d was treated as uninitialized", counter)
		}
	}
}

func TestReplaceScenarioAndGetReturnsExactConcurrentSnapshots(t *testing.T) {
	base, _ := testScenario()
	sim, err := NewSimulator(base)
	if err != nil {
		t.Fatalf("NewSimulator: %v", err)
	}
	defer sim.Close()

	scenarios := []Scenario{base.Clone(), base.Clone()}
	scenarios[0].MatchHistoryLimit = 11
	scenarios[1].MatchHistoryLimit = 22
	const replacements = 12
	type replacementResult struct {
		expected int
		actual   Scenario
		err      error
	}
	start := make(chan struct{})
	results := make(chan replacementResult, replacements)
	for index := 0; index < replacements; index++ {
		index := index
		go func() {
			<-start
			actual, replaceErr := sim.ReplaceScenarioAndGet(context.Background(), scenarios[index%len(scenarios)])
			results <- replacementResult{expected: scenarios[index%len(scenarios)].MatchHistoryLimit, actual: actual, err: replaceErr}
		}()
	}
	close(start)
	for index := 0; index < replacements; index++ {
		result := <-results
		if result.err != nil {
			t.Fatalf("concurrent ReplaceScenarioAndGet: %v", result.err)
		}
		if result.actual.MatchHistoryLimit != result.expected {
			t.Fatalf("replacement returned a later scenario: got limit=%d, want %d", result.actual.MatchHistoryLimit, result.expected)
		}
	}
}

func TestMatchHistoryUsesObjectFactProviderSnapshot(t *testing.T) {
	scenario, key := testScenario()
	scenario.Rules[0].ObjectFactProvider = func(object *common.Ticket, _ int64, _ matchsystem.Facts, out matchsystem.ObjectFactWriter) error {
		return out.SetStrings("object_tag", []string{fmt.Sprintf("provider-%d", object.TicketID)})
	}
	scenario.Rules[0].ObjectFactProviderDescriptor = &matchsystem.ProviderDescriptor{
		ID:      "test.object-facts",
		Version: "v1",
		Facts: []fact.Spec{{
			Name: "object_tag", Type: fact.TypeStrings, Scope: fact.ScopeObject, MaxValues: 2,
		}},
	}
	sim, err := NewSimulator(scenario)
	if err != nil {
		t.Fatalf("NewSimulator: %v", err)
	}
	defer sim.Close()

	ctx := context.Background()
	if _, err := sim.AddTicket(ctx, TicketInput{
		Rule: key.Rule, TicketID: 7, CreatedAt: 10,
		ObjectFacts: FactSnapshot{StringLists: map[string][]string{"object_tag": {"input-value"}}},
	}); err != nil {
		t.Fatalf("AddTicket: %v", err)
	}
	if err := sim.BeginRound(ctx, 100); err != nil {
		t.Fatalf("BeginRound: %v", err)
	}
	result, err := sim.ProduceMatch(ctx)
	if err != nil {
		t.Fatalf("ProduceMatch: %v", err)
	}
	if result.Match == nil || len(result.Match.Tickets) != 1 {
		t.Fatalf("unexpected Match: %#v", result)
	}
	if got := result.Match.Tickets[0].ObjectFacts.StringLists["object_tag"][0]; got != "provider-7" {
		t.Fatalf("Match stored input Object Fact instead of provider result: got %q", got)
	}
	if got := result.Match.Tickets[0].ObjectFacts.StringLists["object_tag"]; len(got) != 1 || got[0] != "provider-7" {
		t.Fatalf("unexpected provider Object Fact snapshot: %#v", got)
	}
}

func TestMatchHistoryDoesNotRecordFailedOrEmptyProduce(t *testing.T) {
	scenario, key := testScenario()
	scenario.Rules[0].ObjectFactProvider = func(*common.Ticket, int64, matchsystem.Facts, matchsystem.ObjectFactWriter) error {
		return errors.New("object provider failed")
	}
	scenario.Rules[0].ObjectFactProviderDescriptor = &matchsystem.ProviderDescriptor{
		ID:      "test.failing-object-facts",
		Version: "v1",
		Facts: []fact.Spec{{
			Name: "object_tag", Type: fact.TypeStrings, Scope: fact.ScopeObject, MaxValues: 2,
		}},
	}
	sim, err := NewSimulator(scenario)
	if err != nil {
		t.Fatalf("NewSimulator: %v", err)
	}
	defer sim.Close()

	ctx := context.Background()
	if _, err := sim.ProduceMatch(ctx); !errors.Is(err, ErrRoundNotStarted) {
		t.Fatalf("ProduceMatch before round: %v", err)
	}
	page, err := sim.ListMatches(ctx, MatchQuery{Limit: 10})
	if err != nil || page.Total != 0 {
		t.Fatalf("history after missing round: total=%d err=%v", page.Total, err)
	}
	if err := sim.BeginRound(ctx, 100); err != nil {
		t.Fatalf("BeginRound without Tickets: %v", err)
	}
	if _, err := sim.ProduceMatch(ctx); !errors.Is(err, matchsystem.ErrNoLogicalNodeAvailable) {
		t.Fatalf("ProduceMatch without Tickets: %v", err)
	}
	page, err = sim.ListMatches(ctx, MatchQuery{Limit: 10})
	if err != nil || page.Total != 0 {
		t.Fatalf("history after empty produce: total=%d err=%v", page.Total, err)
	}
	if _, err := sim.AddTicket(ctx, TicketInput{
		Rule: key.Rule, TicketID: 7, CreatedAt: 10,
		ObjectFacts: FactSnapshot{StringLists: map[string][]string{"object_tag": {"input-value"}}},
	}); err != nil {
		t.Fatalf("AddTicket: %v", err)
	}
	if err := sim.BeginRound(ctx, 200); err != nil {
		t.Fatalf("BeginRound with Ticket: %v", err)
	}
	if _, err := sim.ProduceMatch(ctx); err == nil {
		t.Fatal("ProduceMatch accepted failing ObjectFactProvider")
	}
	page, err = sim.ListMatches(ctx, MatchQuery{Limit: 10})
	if err != nil || page.Total != 0 {
		t.Fatalf("history after failed produce: total=%d err=%v", page.Total, err)
	}
}

func TestMatchHistoryGetRejectsEmptyID(t *testing.T) {
	sim, err := NewSimulator(Scenario{})
	if err != nil {
		t.Fatalf("NewSimulator: %v", err)
	}
	defer sim.Close()
	if _, ok, err := sim.GetMatch(context.Background(), " "); err != ErrInvalidMatchID || ok {
		t.Fatalf("empty Match ID: ok=%v err=%v", ok, err)
	}
}
