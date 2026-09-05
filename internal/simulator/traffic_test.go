package simulator

import (
	"context"
	"math/rand"
	"strings"
	"testing"
	"time"
)

func awaitTraffic(t *testing.T, s *Simulator, predicate func(TrafficStatus) bool) TrafficStatus {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		status := s.Traffic()
		if predicate(status) {
			return status
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("traffic timeout: %+v", s.Traffic())
	return TrafficStatus{}
}
func TestTrafficLifecycle(t *testing.T) {
	scenario, key := testScenario()
	s, err := New(scenario)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	config := TrafficConfig{Distribution: "constant", Rate: 50, MatchIntervalMS: 150, MaxMatches: 1, Seed: 4}
	spec := BatchGeneratorSpec{Rule: key.Rule, FirstTicketID: 100, Seed: 9}
	before := time.Now().UnixMilli()
	if _, err = s.StartTraffic(config, spec); err != nil {
		t.Fatal(err)
	}
	if _, err = s.StartTraffic(config, spec); err == nil {
		t.Fatal("duplicate start accepted")
	}
	awaitTraffic(t, s, func(st TrafficStatus) bool { return st.Rounds >= 2 })
	stopped := s.StopTraffic()
	time.Sleep(180 * time.Millisecond)
	if s.Traffic() != stopped {
		t.Fatal("injection after stop")
	}
	if stopped.Produced > stopped.Rounds || stopped.NextTicketID != 100+stopped.Injected {
		t.Fatalf("bad counters: %+v", stopped)
	}
	page, err := s.ListTickets(context.Background(), TicketQuery{})
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range page.Items {
		if v.CreatedAt < before || v.CreatedAt > time.Now().UnixMilli() {
			t.Fatal("invalid creation time")
		}
	}
	spec.FirstTicketID = stopped.NextTicketID
	if _, err = s.StartTraffic(config, spec); err != nil {
		t.Fatal(err)
	}
	bad := scenario
	bad.SchemaVersion = "bad"
	if err = s.ReplaceScenario(context.Background(), bad); err == nil {
		t.Fatal("bad scenario accepted")
	}
	if s.Traffic().State != "running" {
		t.Fatal("failed replacement stopped traffic")
	}
	if err = s.ReplaceScenario(context.Background(), scenario); err != nil {
		t.Fatal(err)
	}
	stopped = s.Traffic()
	time.Sleep(100 * time.Millisecond)
	if s.Traffic() != stopped || stopped.State != "stopped" {
		t.Fatal("replacement did not stop")
	}
	if _, err = s.StartTraffic(config, spec); err != nil {
		t.Fatal(err)
	}
	s.Close()
	stopped = s.Traffic()
	time.Sleep(100 * time.Millisecond)
	if s.Traffic() != stopped {
		t.Fatal("close did not stop")
	}
}
func TestTrafficNoForcedMatchAndFailure(t *testing.T) {
	scenario, key := testScenario()
	scenario.Rules[0].RuleJSON = []byte(strings.Replace(string(scenario.Rules[0].RuleJSON), `"canComplete":{"schemaVersion":"expression-scalar/v3","resultType":"bool","expr":{"op":"bool_literal","value":true}}`, `"canComplete":{"schemaVersion":"expression-scalar/v3","resultType":"bool","expr":{"op":"bool_literal","value":false}}`, 1))
	s, err := New(scenario)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	config := TrafficConfig{Distribution: "burst", Rate: 1, BurstSize: 3, BurstIntervalMS: 100, MatchIntervalMS: 100, MaxMatches: 2}
	if _, err = s.StartTraffic(config, BatchGeneratorSpec{Rule: key.Rule, FirstTicketID: 1}); err != nil {
		t.Fatal(err)
	}
	st := awaitTraffic(t, s, func(st TrafficStatus) bool { return st.Rounds >= 2 })
	if st.Produced != 0 {
		t.Fatal("forced match")
	}
	s.StopTraffic()
	if _, err = s.StartTraffic(config, BatchGeneratorSpec{Rule: key.Rule, FirstTicketID: 1}); err != nil {
		t.Fatal(err)
	}
	st = awaitTraffic(t, s, func(st TrafficStatus) bool { return st.State == "failed" })
	if st.Error == "" {
		t.Fatal("missing failure")
	}
}

func TestPoissonArrivalSequence(t *testing.T) {
	config := TrafficConfig{Distribution: "poisson", Rate: 100}
	a, b := rand.New(rand.NewSource(42)), rand.New(rand.NewSource(42))
	var total time.Duration
	distinct := false
	var prev time.Duration
	for i := 0; i < 10000; i++ {
		d := trafficDelay(config, a)
		if d != trafficDelay(config, b) || d <= 0 {
			t.Fatal("arrival sequence is not reproducible")
		}
		total += d
		if prev != 0 && prev != d {
			distinct = true
		}
		prev = d
	}
	mean := total.Seconds() / 10000
	if mean < 0.009 || mean > 0.011 || !distinct {
		t.Fatalf("unexpected Poisson mean %f", mean)
	}
}

func TestTrafficSafeIDExhaustion(t *testing.T) {
	scenario, key := testScenario()
	s, err := New(scenario)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	config := TrafficConfig{Distribution: "constant", Rate: 100, MatchIntervalMS: 100, MaxMatches: 1}
	if _, err = s.StartTraffic(config, BatchGeneratorSpec{Rule: key.Rule, FirstTicketID: 9007199254740991}); err != nil {
		t.Fatal(err)
	}
	status := awaitTraffic(t, s, func(st TrafficStatus) bool { return st.State == "failed" })
	if status.Injected != 1 || status.NextTicketID != 0 || status.Error == "" {
		t.Fatalf("unsafe ID status: %+v", status)
	}
}
