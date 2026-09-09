package simulator

import (
	"context"
	"math"
	"testing"
	"time"
)

func TestFixedTrafficUsesVirtualClockAndCompletes(t *testing.T) {
	run := func() (TrafficStatus, []MatchRecord) {
		scenario, key := testScenario()
		sim, err := NewService(scenario)
		if err != nil {
			t.Fatal(err)
		}
		defer sim.Close()
		config := TrafficConfig{
			Mode:            TrafficModeFixed,
			Distribution:    "burst",
			Rate:            1,
			BurstSize:       2,
			BurstIntervalMS: 100,
			MatchIntervalMS: 100,
			MaxMatches:      100,
			Seed:            17,
			Steps:           4,
			StepMS:          100,
			StartTimeMS:     1_700_000_000_000,
		}
		if _, err := sim.StartTraffic(config, BatchGeneratorSpec{Rule: key.Rule, FirstTicketID: 10, Seed: 21}); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(2 * time.Second)
		for {
			status := sim.Traffic()
			if status.State == "completed" {
				matches, err := sim.ListMatches(context.Background(), MatchQuery{Limit: 100})
				if err != nil {
					t.Fatal(err)
				}
				return status, matches.Items
			}
			if status.State == "failed" || time.Now().After(deadline) {
				t.Fatalf("fixed traffic did not complete: %+v", status)
			}
			time.Sleep(time.Millisecond)
		}
	}
	firstStatus, firstMatches := run()
	secondStatus, secondMatches := run()
	if firstStatus.State != "completed" || firstStatus.Step != 4 || firstStatus.VirtualNow != 1_700_000_000_300 {
		t.Fatalf("unexpected fixed status: %+v", firstStatus)
	}
	if firstStatus.Injected != secondStatus.Injected || firstStatus.Produced != secondStatus.Produced || len(firstMatches) != len(secondMatches) {
		t.Fatalf("fixed run counters are not deterministic: first=%+v second=%+v", firstStatus, secondStatus)
	}
	if firstStatus.Injected != 6 || firstStatus.Produced != 6 {
		t.Fatalf("burst arrivals were not expanded deterministically: %+v", firstStatus)
	}
	if len(firstMatches) == 0 {
		t.Fatal("fixed traffic produced no matches")
	}
	for i := range firstMatches {
		if firstMatches[i].Now != secondMatches[i].Now || firstMatches[i].DurationMs != secondMatches[i].DurationMs {
			t.Fatalf("fixed match timing differs at %d: %#v %#v", i, firstMatches[i], secondMatches[i])
		}
		if firstMatches[i].Now < 1_700_000_000_000 || firstMatches[i].Now > 1_700_000_000_300 {
			t.Fatalf("match escaped virtual clock: %#v", firstMatches[i])
		}
	}
}

func TestFixedTrafficRejectsVirtualTimelineOverflow(t *testing.T) {
	scenario, key := testScenario()
	sim, err := NewService(scenario)
	if err != nil {
		t.Fatal(err)
	}
	defer sim.Close()
	_, err = sim.StartTraffic(TrafficConfig{
		Mode:            TrafficModeFixed,
		Distribution:    "constant",
		Rate:            1,
		MatchIntervalMS: 1,
		MaxMatches:      1,
		Steps:           2,
		StepMS:          1,
		StartTimeMS:     math.MaxInt64,
	}, BatchGeneratorSpec{Rule: key.Rule, FirstTicketID: 1})
	if err == nil {
		t.Fatal("accepted a fixed virtual timeline that overflows int64")
	}
}

func TestFixedTrafficProcessesAllDueArrivalsBeforeMatching(t *testing.T) {
	scenario, key := testScenario()
	sim, err := NewService(scenario)
	if err != nil {
		t.Fatal(err)
	}
	defer sim.Close()
	const start = int64(1_800_000_000_000)
	if _, err := sim.StartTraffic(TrafficConfig{
		Mode:            TrafficModeFixed,
		Distribution:    "constant",
		Rate:            100,
		MatchIntervalMS: 300,
		MaxMatches:      100,
		Seed:            3,
		Steps:           2,
		StepMS:          300,
		StartTimeMS:     start,
	}, BatchGeneratorSpec{Rule: key.Rule, FirstTicketID: 100, Seed: 4}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		status := sim.Traffic()
		if status.State == "completed" {
			if status.Injected != 30 || status.Produced != 30 || status.VirtualNow != start+300 {
				t.Fatalf("unexpected due-arrival status: %+v", status)
			}
			matches, listErr := sim.ListMatches(context.Background(), MatchQuery{Limit: 100})
			if listErr != nil {
				t.Fatal(listErr)
			}
			if len(matches.Items) != 30 {
				t.Fatalf("got %d matches, want 30", len(matches.Items))
			}
			for index, match := range matches.Items {
				if match.Now != start+300 || len(match.Tickets) != 1 {
					t.Fatalf("match %d was not evaluated after due arrivals: %#v", index, match)
				}
				if match.Tickets[0].CreatedAt < start+10 || match.Tickets[0].CreatedAt > start+300 {
					t.Fatalf("match %d has invalid virtual CreatedAt: %#v", index, match.Tickets[0])
				}
			}
			return
		}
		if status.State == "failed" || time.Now().After(deadline) {
			t.Fatalf("fixed due-arrival run did not complete: %+v", status)
		}
		time.Sleep(time.Millisecond)
	}
}
