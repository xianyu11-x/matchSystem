package simulator

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"time"
)

// TrafficConfig controls wall-clock arrivals independently from rule evaluation.
type TrafficConfig struct {
	Distribution    string  `json:"distribution"`
	Rate            float64 `json:"rate"`
	BurstSize       int     `json:"burstSize"`
	BurstIntervalMS int64   `json:"burstIntervalMs"`
	MatchIntervalMS int64   `json:"matchIntervalMs"`
	MaxMatches      int     `json:"maxMatches"`
	Seed            int64   `json:"seed"`
}
type TrafficStatus struct {
	State        string        `json:"state"`
	Config       TrafficConfig `json:"config"`
	Injected     uint64        `json:"injected"`
	Produced     uint64        `json:"produced"`
	Rounds       uint64        `json:"rounds"`
	NextTicketID uint64        `json:"nextTicketId"`
	StartedAt    int64         `json:"startedAt"`
	LagMS        int64         `json:"lagMs"`
	Error        string        `json:"error,omitempty"`
}
type trafficRun struct {
	stop    chan struct{}
	runtime *simulatorRuntime
}

func (s *Simulator) stopTrafficLocked(state string) {
	if s.traffic != nil {
		close(s.traffic.stop)
		s.traffic = nil
		s.trafficStatus.State = state
	}
}
func (s *Simulator) Traffic() TrafficStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	status := s.trafficStatus
	if status.State == "" {
		status.State = "idle"
	}
	return status
}
func (s *Simulator) StopTraffic() TrafficStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopTrafficLocked("stopped")
	if s.trafficStatus.State == "" {
		s.trafficStatus.State = "idle"
	}
	return s.trafficStatus
}
func (s *Simulator) StartTraffic(config TrafficConfig, spec BatchGeneratorSpec) (TrafficStatus, error) {
	invalid := func(message string) (TrafficStatus, error) {
		return TrafficStatus{}, fmt.Errorf("%w: %s", ErrInvalidBatchSpec, message)
	}
	if config.Distribution != "constant" && config.Distribution != "poisson" && config.Distribution != "burst" {
		return invalid("unsupported traffic distribution")
	}
	if math.IsNaN(config.Rate) || math.IsInf(config.Rate, 0) || config.Rate < 0.001 || config.Rate > 10000 {
		return invalid("rate must be in [0.001,10000]")
	}
	if config.MatchIntervalMS < 100 || config.MatchIntervalMS > 86400000 || config.MaxMatches < 1 || config.MaxMatches > 10000 {
		return invalid("match interval must be 100..86400000 ms and maxMatches 1..10000")
	}
	if config.Distribution == "burst" && (config.BurstSize < 1 || config.BurstSize > 10000 || config.BurstIntervalMS < 100 || config.BurstIntervalMS > 86400000) {
		return invalid("burst size must be 1..10000 and interval 100..86400000 ms")
	}
	// Own all generator maps, including future generator extensions.
	data, err := json.Marshal(spec)
	if err != nil {
		return TrafficStatus{}, err
	}
	var owned BatchGeneratorSpec
	if err = json.Unmarshal(data, &owned); err != nil {
		return TrafficStatus{}, err
	}
	spec = owned
	spec.Count = 1
	spec.CreatedAtStart = 0
	spec.CreatedAtStep = 0
	if _, err := GenerateBatch(spec); err != nil {
		return TrafficStatus{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	runtime, err := s.runtimeReadLocked()
	if err != nil {
		return TrafficStatus{}, err
	}
	if s.traffic != nil {
		return invalid("traffic is already running; stop before starting again")
	}
	found := false
	for key := range runtime.rules {
		if key.Rule == spec.Rule {
			found = true
		}
	}
	if !found {
		return invalid("unknown traffic rule")
	}
	if spec.FirstTicketID == 0 {
		spec.FirstTicketID = 1
	}
	run := &trafficRun{stop: make(chan struct{}), runtime: runtime}
	s.traffic = run
	s.trafficStatus = TrafficStatus{State: "running", Config: config, NextTicketID: spec.FirstTicketID, StartedAt: time.Now().UnixMilli()}
	go s.runTraffic(run, config, spec)
	return s.trafficStatus, nil
}
func (s *Simulator) runTraffic(run *trafficRun, config TrafficConfig, spec BatchGeneratorSpec) {
	arrival := rand.New(rand.NewSource(config.Seed))
	attributes := rand.New(rand.NewSource(spec.Seed))
	delay := func() time.Duration { return trafficDelay(config, arrival) }
	nextArrival := time.Now().Add(delay())
	nextMatch := time.Now().Add(time.Duration(config.MatchIntervalMS) * time.Millisecond)
	timer := time.NewTimer(time.Until(nextArrival))
	defer timer.Stop()
	for {
		deadline := nextArrival
		if nextMatch.Before(deadline) {
			deadline = nextMatch
		}
		timer.Reset(max(time.Duration(0), time.Until(deadline)))
		select {
		case <-run.stop:
			return
		case <-timer.C:
		}
		s.mu.Lock()
		if s.traffic != run || s.runtime != run.runtime || s.closed {
			s.mu.Unlock()
			return
		}
		now := time.Now()
		s.trafficStatus.LagMS = max(int64(0), now.Sub(deadline).Milliseconds())
		count := 0
		if !now.Before(nextArrival) {
			count = 1
			if config.Distribution == "burst" {
				count = config.BurstSize
			}
			nextArrival = nextArrival.Add(delay())
		}
		match := !now.Before(nextMatch)
		if match {
			nextMatch = nextMatch.Add(time.Duration(config.MatchIntervalMS) * time.Millisecond)
		}
		err := s.trafficStep(run.runtime, &spec, attributes, count, now, match, config.MaxMatches)
		if err != nil {
			s.trafficStatus.Error = err.Error()
			s.stopTrafficLocked("failed")
		}
		s.mu.Unlock()
		if err != nil {
			return
		}
	}
}

func (s *Simulator) trafficStep(runtime *simulatorRuntime, spec *BatchGeneratorSpec, rng *rand.Rand, count int, now time.Time, match bool, maxMatches int) error {
	ctx := context.Background()
	for i := 0; i < count; i++ {
		if s.trafficStatus.NextTicketID == 0 || s.trafficStatus.NextTicketID > 9007199254740991 {
			return fmt.Errorf("TicketID exhausted")
		}
		spec.FirstTicketID = s.trafficStatus.NextTicketID
		spec.CreatedAtStart = now.UnixMilli()
		spec.Seed = rng.Int63()
		inputs, err := GenerateBatch(*spec)
		if err != nil {
			return err
		}
		decision, err := runtime.router.RouteNew(ctx, inputs[0].routeRequest())
		if err != nil {
			return err
		}
		if _, err = runtime.addAtOwner(ctx, decision, inputs[0]); err != nil {
			return err
		}
		s.trafficStatus.Injected++
		s.trafficStatus.NextTicketID++
		if s.trafficStatus.NextTicketID > 9007199254740991 {
			s.trafficStatus.NextTicketID = 0
			return fmt.Errorf("TicketID exhausted safe integer range")
		}
	}
	if match {
		if err := runtime.beginRound(ctx, now.UnixMilli()); err != nil {
			return err
		}
		result, err := runtime.produceAll(ctx, maxMatches, s.allocateMatchID)
		s.trafficStatus.Rounds++
		s.trafficStatus.Produced += uint64(len(result.Matches))
		return err
	}
	return nil
}

func trafficDelay(config TrafficConfig, rng *rand.Rand) time.Duration {
	if config.Distribution == "burst" {
		return time.Duration(config.BurstIntervalMS) * time.Millisecond
	}
	seconds := 1 / config.Rate
	if config.Distribution == "poisson" {
		seconds *= rng.ExpFloat64()
	}
	return max(time.Nanosecond, time.Duration(seconds*float64(time.Second)))
}
