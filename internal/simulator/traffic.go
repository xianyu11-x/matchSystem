package simulator

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	stdRuntime "runtime"
	"time"
)

// TrafficMode selects the clock used by a traffic run. Realtime preserves the
// original wall-clock loop; Fixed advances a virtual clock by a deterministic
// number of steps and never sleeps between steps.
type TrafficMode string

const (
	TrafficModeRealtime TrafficMode = "realtime"
	TrafficModeFixed    TrafficMode = "fixed"
)

// TrafficConfig controls arrivals independently from rule evaluation.
type TrafficConfig struct {
	Mode            TrafficMode `json:"mode,omitempty"`
	Distribution    string      `json:"distribution"`
	Rate            float64     `json:"rate"`
	BurstSize       int         `json:"burstSize"`
	BurstIntervalMS int64       `json:"burstIntervalMs"`
	MatchIntervalMS int64       `json:"matchIntervalMs"`
	MaxMatches      int         `json:"maxMatches"`
	Seed            int64       `json:"seed"`
	// Fixed mode fields. StartTimeMS is the virtual Unix-millisecond origin;
	// StepMS advances it once per deterministic step. Steps is required for
	// fixed runs so callers can wait for a finite completed state.
	Steps       int   `json:"steps,omitempty"`
	StepMS      int64 `json:"stepMs,omitempty"`
	StartTimeMS int64 `json:"startTimeMs,omitempty"`
}
type TrafficStatus struct {
	State         string        `json:"state"`
	Config        TrafficConfig `json:"config"`
	Injected      uint64        `json:"injected"`
	Produced      uint64        `json:"produced"`
	Rounds        uint64        `json:"rounds"`
	NextTicketID  uint64        `json:"nextTicketId"`
	StartedAt     int64         `json:"startedAt"`
	LagMS         int64         `json:"lagMs"`
	Step          int           `json:"step,omitempty"`
	TotalSteps    int           `json:"totalSteps,omitempty"`
	VirtualNow    int64         `json:"virtualNow,omitempty"`
	WallElapsedMS int64         `json:"wallElapsedMs,omitempty"`
	Error         string        `json:"error,omitempty"`
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
	config.Mode = normalizeTrafficMode(config.Mode)
	if config.Mode != TrafficModeRealtime && config.Mode != TrafficModeFixed {
		return invalid("unsupported traffic mode")
	}
	if config.Distribution != "constant" && config.Distribution != "poisson" && config.Distribution != "burst" {
		return invalid("unsupported traffic distribution")
	}
	if math.IsNaN(config.Rate) || math.IsInf(config.Rate, 0) || config.Rate < 0.001 || config.Rate > 10000 {
		return invalid("rate must be in [0.001,10000]")
	}
	if config.Mode == TrafficModeFixed {
		if config.StepMS == 0 {
			config.StepMS = config.MatchIntervalMS
		}
		if config.MatchIntervalMS == 0 {
			config.MatchIntervalMS = config.StepMS
		}
		if config.Steps < 1 || config.Steps > 10000000 {
			return invalid("fixed traffic steps must be in [1,10000000]")
		}
		if config.StepMS < 1 || config.StepMS > 86400000 {
			return invalid("fixed traffic stepMs must be in [1,86400000]")
		}
		stepCount := int64(config.Steps - 1)
		if stepCount > math.MaxInt64/config.StepMS {
			return invalid("fixed traffic virtual timeline overflows int64")
		}
		span := stepCount * config.StepMS
		if config.StartTimeMS > math.MaxInt64-span {
			return invalid("fixed traffic virtual timeline overflows int64")
		}
	}
	minMatchInterval := int64(100)
	if config.Mode == TrafficModeFixed {
		// Fixed runs are virtual and need to support fine-grained clocks for
		// deterministic tests and scripted scenarios. Realtime retains its
		// original lower bound to avoid a busy wall-clock loop.
		minMatchInterval = 1
	}
	if config.MatchIntervalMS < minMatchInterval || config.MatchIntervalMS > 86400000 || config.MaxMatches < 1 || config.MaxMatches > 10000 {
		return invalid(fmt.Sprintf("match interval must be %d..86400000 ms and maxMatches 1..10000", minMatchInterval))
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
	if config.Mode == TrafficModeFixed && config.StartTimeMS == 0 {
		config.StartTimeMS = time.Now().UnixMilli()
	}
	run := &trafficRun{stop: make(chan struct{}), runtime: runtime}
	s.traffic = run
	s.trafficStatus = TrafficStatus{State: "running", Config: config, NextTicketID: spec.FirstTicketID, StartedAt: time.Now().UnixMilli(), TotalSteps: config.Steps}
	go s.runTraffic(run, config, spec)
	return s.trafficStatus, nil
}
func (s *Simulator) runTraffic(run *trafficRun, config TrafficConfig, spec BatchGeneratorSpec) {
	if config.Mode == TrafficModeFixed {
		s.runFixedTraffic(run, config, spec)
		return
	}
	s.runRealtimeTraffic(run, config, spec)
}

func (s *Simulator) runRealtimeTraffic(run *trafficRun, config TrafficConfig, spec BatchGeneratorSpec) {
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
		// Yield after each wall-clock operation so a stop or status request can
		// be scheduled even when the timer fires repeatedly without delay.
		stdRuntime.Gosched()
	}
}

// runFixedTraffic advances a virtual clock and executes the same insertion
// path as realtime traffic. The host wall clock is used only for status
// diagnostics; CreatedAt, round Now, Tick.Now and provider inputs all use the
// virtual timestamp.
func (s *Simulator) runFixedTraffic(run *trafficRun, config TrafficConfig, spec BatchGeneratorSpec) {
	started := time.Now()
	arrival := rand.New(rand.NewSource(config.Seed))
	attributes := rand.New(rand.NewSource(spec.Seed))
	virtualNow := config.StartTimeMS
	nextArrival := virtualNow
	initialDelay := trafficDelayMS(config, arrival)
	if nextArrival > math.MaxInt64-initialDelay {
		nextArrival = math.MaxInt64
	} else {
		nextArrival += initialDelay
	}
	// A fixed run has one deterministic matching opportunity at its first
	// virtual instant; subsequent rounds follow MatchIntervalMS.
	nextMatch := virtualNow
	for step := 1; step <= config.Steps; step++ {
		select {
		case <-run.stop:
			return
		default:
		}
		virtualNow = config.StartTimeMS + int64(step-1)*config.StepMS
		arrivalTimes := make([]int64, 0, 1)
		for virtualNow >= nextArrival {
			arrivalCount := 1
			if config.Distribution == "burst" {
				arrivalCount = config.BurstSize
			}
			for index := 0; index < arrivalCount; index++ {
				arrivalTimes = append(arrivalTimes, nextArrival)
			}
			// MaxInt64 is a valid final virtual millisecond, but no later
			// representable arrival can be scheduled after it.
			if nextArrival == math.MaxInt64 {
				break
			}
			delay := trafficDelayMS(config, arrival)
			if nextArrival > math.MaxInt64-delay {
				nextArrival = math.MaxInt64
				break
			}
			nextArrival += delay
			if len(arrivalTimes) > 100000 {
				s.mu.Lock()
				if s.traffic == run && s.runtime == run.runtime && !s.closed {
					s.trafficStatus.Error = "fixed traffic produced more than 100000 arrivals in one step"
					s.stopTrafficLocked("failed")
				}
				s.mu.Unlock()
				return
			}
		}
		match := virtualNow >= nextMatch
		if match {
			if nextMatch > math.MaxInt64-config.MatchIntervalMS {
				nextMatch = math.MaxInt64
			} else {
				nextMatch += config.MatchIntervalMS
			}
		}

		s.mu.Lock()
		if s.traffic != run || s.runtime != run.runtime || s.closed {
			s.mu.Unlock()
			return
		}
		s.trafficStatus.Step = step
		s.trafficStatus.VirtualNow = virtualNow
		s.trafficStatus.LagMS = 0
		err := s.trafficStepAtTimes(run.runtime, &spec, attributes, arrivalTimes, virtualNow, match, config.MaxMatches)
		s.trafficStatus.WallElapsedMS = time.Since(started).Milliseconds()
		if err != nil {
			s.trafficStatus.Error = err.Error()
			s.stopTrafficLocked("failed")
		}
		s.mu.Unlock()
		if err != nil {
			return
		}
		// A fixed run deliberately does not sleep, but yielding between virtual
		// steps gives StopTraffic and status readers a scheduling opportunity even
		// when the configured step count is large.
		stdRuntime.Gosched()
	}
	s.mu.Lock()
	if s.traffic == run && !s.closed {
		s.stopTrafficLocked("completed")
		s.trafficStatus.Step = config.Steps
		s.trafficStatus.VirtualNow = config.StartTimeMS + int64(max(0, config.Steps-1))*config.StepMS
		s.trafficStatus.WallElapsedMS = time.Since(started).Milliseconds()
	}
	s.mu.Unlock()
}

func (s *Simulator) trafficStep(runtime *simulatorRuntime, spec *BatchGeneratorSpec, rng *rand.Rand, count int, now time.Time, match bool, maxMatches int) error {
	arrivalTimes := make([]int64, count)
	for index := range arrivalTimes {
		arrivalTimes[index] = now.UnixMilli()
	}
	return s.trafficStepAtTimes(runtime, spec, rng, arrivalTimes, now.UnixMilli(), match, maxMatches)
}

func (s *Simulator) trafficStepAt(runtime *simulatorRuntime, spec *BatchGeneratorSpec, rng *rand.Rand, count int, now int64, match bool, maxMatches int) error {
	arrivalTimes := make([]int64, count)
	for index := range arrivalTimes {
		arrivalTimes[index] = now
	}
	return s.trafficStepAtTimes(runtime, spec, rng, arrivalTimes, now, match, maxMatches)
}

func (s *Simulator) trafficStepAtTimes(runtime *simulatorRuntime, spec *BatchGeneratorSpec, rng *rand.Rand, arrivalTimes []int64, now int64, match bool, maxMatches int) error {
	ctx := context.Background()
	for _, createdAt := range arrivalTimes {
		if s.trafficStatus.NextTicketID == 0 || s.trafficStatus.NextTicketID > 9007199254740991 {
			return fmt.Errorf("TicketID exhausted")
		}
		spec.FirstTicketID = s.trafficStatus.NextTicketID
		spec.CreatedAtStart = createdAt
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
		if err := runtime.beginRound(ctx, now); err != nil {
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

func normalizeTrafficMode(mode TrafficMode) TrafficMode {
	switch mode {
	case "", TrafficModeRealtime:
		return TrafficModeRealtime
	case TrafficModeFixed, "fixed_step", "fixed-step":
		return TrafficModeFixed
	default:
		return mode
	}
}

func trafficDelayMS(config TrafficConfig, rng *rand.Rand) int64 {
	delay := trafficDelay(config, rng)
	ms := delay / time.Millisecond
	if delay%time.Millisecond != 0 {
		ms++
	}
	if ms < 1 {
		return 1
	}
	return int64(ms)
}
