package matchsystem

import (
	"context"
	"time"

	"matchSystem/internal/matchsystem/prefilter"
)

// ProduceMatchMetrics is the aggregated timing and counter snapshot for one
// ProduceMatchWithMetrics call. Durations are measured inside the matching
// owner and are intentionally returned as one value; the production
// ProduceMatch path does not allocate a metrics object or read the clock.
//
// CandidateRanking contains the complete bounded top-L selection stage. Its
// materialization, scoring, and final sort durations are sub-measurements of
// CandidateRanking and are therefore not expected to add up to Duration.
type ProduceMatchMetrics struct {
	Duration                 time.Duration
	SeedPreparation          time.Duration
	SessionPreparation       time.Duration
	AttemptPreparation       time.Duration
	Prefilter                time.Duration
	CandidateRanking         time.Duration
	CandidateMaterialization time.Duration
	CandidateScoring         time.Duration
	CandidateSort            time.Duration
	CanJoin                  time.Duration
	MatchFactUpdate          time.Duration
	CanComplete              time.Duration
	MatchBuild               time.Duration
	Commit                   time.Duration

	SeedAttempts                  uint64
	PrefilterCalls                uint64
	PrefilterCandidates           uint64
	CandidateVisited              uint64
	CandidateMaterializationCalls uint64
	CandidateScoringCalls         uint64
	RankedCandidates              uint64
	CandidateSortCalls            uint64
	CanJoinCalls                  uint64
	JoinedCandidates              uint64
	MatchFactUpdateCalls          uint64
	CanCompleteCalls              uint64
	CommitCalls                   uint64
	MatchSize                     int

	// Prefilter operation counters make an index-plan result explainable
	// without emitting one log line per candidate.
	PrefilterLookupCalls   uint64
	PrefilterContainsCalls uint64
	PrefilterAndCalls      uint64
	PrefilterOrCalls       uint64
	PrefilterSubtractCalls uint64
}

// ProduceMatchWithMetrics runs the same matching operation as ProduceMatch
// and returns one aggregated timing/counter snapshot. It is intended for
// benchmarks and diagnostics; callers on the normal production path should
// continue using ProduceMatch.
func (p *LogicalNode) ProduceMatchWithMetrics(ctx context.Context) (*Match, ProduceMatchMetrics, error) {
	trace := newProduceMatchTrace()
	match, err := p.produceMatch(ctx, trace)
	if match != nil {
		trace.metrics.MatchSize = len(match.Tickets)
	}
	return match, trace.finish(), err
}

type produceMatchTrace struct {
	started time.Time
	metrics ProduceMatchMetrics
}

type produceMatchStage uint8

const (
	produceStageSeedPreparation produceMatchStage = iota
	produceStageSessionPreparation
	produceStageAttemptPreparation
	produceStagePrefilter
	produceStageCandidateRanking
	produceStageCandidateMaterialization
	produceStageCandidateScoring
	produceStageCandidateSort
	produceStageCanJoin
	produceStageMatchFactUpdate
	produceStageCanComplete
	produceStageMatchBuild
	produceStageCommit
)

func newProduceMatchTrace() *produceMatchTrace {
	return &produceMatchTrace{started: time.Now()}
}

// start intentionally performs no clock read when tracing is disabled. The
// nil receiver calls used by the hot path make instrumentation branches local
// to the existing stage boundaries without duplicating the matching logic.
func (t *produceMatchTrace) start() time.Time {
	if t == nil {
		return time.Time{}
	}
	return time.Now()
}

func (t *produceMatchTrace) addDuration(stage produceMatchStage, started time.Time) {
	if t == nil {
		return
	}
	duration := time.Since(started)
	switch stage {
	case produceStageSeedPreparation:
		t.metrics.SeedPreparation += duration
	case produceStageSessionPreparation:
		t.metrics.SessionPreparation += duration
	case produceStageAttemptPreparation:
		t.metrics.AttemptPreparation += duration
	case produceStagePrefilter:
		t.metrics.Prefilter += duration
	case produceStageCandidateRanking:
		t.metrics.CandidateRanking += duration
	case produceStageCandidateMaterialization:
		t.metrics.CandidateMaterialization += duration
	case produceStageCandidateScoring:
		t.metrics.CandidateScoring += duration
	case produceStageCandidateSort:
		t.metrics.CandidateSort += duration
	case produceStageCanJoin:
		t.metrics.CanJoin += duration
	case produceStageMatchFactUpdate:
		t.metrics.MatchFactUpdate += duration
	case produceStageCanComplete:
		t.metrics.CanComplete += duration
	case produceStageMatchBuild:
		t.metrics.MatchBuild += duration
	case produceStageCommit:
		t.metrics.Commit += duration
	}
}

func (t *produceMatchTrace) finish() ProduceMatchMetrics {
	if t == nil {
		return ProduceMatchMetrics{}
	}
	t.metrics.Duration = time.Since(t.started)
	return t.metrics
}

func (t *produceMatchTrace) recordSeedAttempt() {
	if t != nil {
		t.metrics.SeedAttempts++
	}
}

func (t *produceMatchTrace) recordPrefilter(stats prefilter.Stats, candidates uint64) {
	if t == nil {
		return
	}
	t.metrics.PrefilterCalls++
	t.metrics.PrefilterCandidates += candidates
	t.metrics.PrefilterLookupCalls += stats.LookupCalls
	t.metrics.PrefilterContainsCalls += stats.ContainsCalls
	t.metrics.PrefilterAndCalls += stats.AndCalls
	t.metrics.PrefilterOrCalls += stats.OrCalls
	t.metrics.PrefilterSubtractCalls += stats.SubtractCalls
}

func (t *produceMatchTrace) recordCandidateMaterialization() {
	if t != nil {
		t.metrics.CandidateMaterializationCalls++
	}
}

func (t *produceMatchTrace) recordCandidateScore() {
	if t != nil {
		t.metrics.CandidateScoringCalls++
	}
}

func (t *produceMatchTrace) recordCandidateVisited() {
	if t != nil {
		t.metrics.CandidateVisited++
	}
}

func (t *produceMatchTrace) recordRankedCandidate() {
	if t != nil {
		t.metrics.RankedCandidates++
	}
}

func (t *produceMatchTrace) recordCandidateSort() {
	if t != nil {
		t.metrics.CandidateSortCalls++
	}
}

func (t *produceMatchTrace) recordCanJoin() {
	if t != nil {
		t.metrics.CanJoinCalls++
	}
}

func (t *produceMatchTrace) recordJoinedCandidate() {
	if t != nil {
		t.metrics.JoinedCandidates++
	}
}

func (t *produceMatchTrace) recordMatchFactUpdate() {
	if t != nil {
		t.metrics.MatchFactUpdateCalls++
	}
}

func (t *produceMatchTrace) recordCanComplete() {
	if t != nil {
		t.metrics.CanCompleteCalls++
	}
}

func (t *produceMatchTrace) recordCommit() {
	if t != nil {
		t.metrics.CommitCalls++
	}
}
