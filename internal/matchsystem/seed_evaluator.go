package matchsystem

import (
	"context"
	"errors"
	"fmt"

	"matchSystem/internal/common"
	"matchSystem/internal/identity"
	"matchSystem/internal/matchsystem/evaluation"
	"matchSystem/internal/matchsystem/fact"
	"matchSystem/internal/matchsystem/prefilter"
)

// seedEvaluator owns the complete decision pipeline for one LogicalNode. It
// may read the ticketStore while evaluating, but never mutates it; consuming a
// successful Match is the caller's explicit ticketStore.Commit operation.
type seedEvaluator struct {
	key            identity.LogicalNodeKey
	tickFacts      FactProvider
	objectFacts    ObjectFactProvider
	evaluation     evaluation.Predicates
	scorer         CandidateScorer
	matchFacts     MatchFactProvider
	candidateLimit int
	maxPlayers     int
	store          seedStoreReader
}

// seedStoreReader is the evaluator's deliberately narrow store view. Both
// operations are read-only with respect to Ticket membership and lifecycle;
// beginPrefilterTick may prepare Prefilter-derived query caches for the
// current Tick, but it cannot add/remove/consume Tickets.
type seedStoreReader interface {
	beginPrefilterTick(Facts) (*prefilter.TickSession, error)
	lookupDocID(uint32) (*storedTicket, bool)
}

type seedEvaluatorConfig struct {
	key            identity.LogicalNodeKey
	tickFacts      FactProvider
	objectFacts    ObjectFactProvider
	evaluation     evaluation.Predicates
	scorer         CandidateScorer
	matchFacts     MatchFactProvider
	candidateLimit int
	maxPlayers     int
	store          seedStoreReader
}

func newSeedEvaluator(config seedEvaluatorConfig) *seedEvaluator {
	return &seedEvaluator{
		key:            config.key,
		tickFacts:      config.tickFacts,
		objectFacts:    config.objectFacts,
		evaluation:     config.evaluation,
		scorer:         config.scorer,
		matchFacts:     config.matchFacts,
		candidateLimit: config.candidateLimit,
		maxPlayers:     config.maxPlayers,
		store:          config.store,
	}
}

// seedSession is the immutable-per-ProduceMatch evaluation context. One Tick
// Fact layer and one Prefilter TickSession are shared by all seeds attempted in
// the call; Object Facts are cached by the Fact Frame.
type seedSession struct {
	evaluator *seedEvaluator
	now       int64
	frame     *fact.Frame
	prefilter *prefilter.TickSession
	trace     *produceMatchTrace
}

// BeginSession creates the Tick Fact frame and Prefilter session. It is called
// after LogicalNode reserves its first seed, preserving the round rule that a
// provider/configuration failure never makes that seed selectable again.
func (e *seedEvaluator) BeginSession(ctx context.Context, input TickFactInput, traces ...*produceMatchTrace) (*seedSession, error) {
	if e == nil {
		return nil, fmt.Errorf("seed evaluator is nil")
	}
	var trace *produceMatchTrace
	if len(traces) > 0 {
		trace = traces[0]
	}
	facts := Facts{}
	if e.tickFacts != nil {
		values, err := invokeProvider(ctx, "tickFacts", func() (Facts, error) {
			return e.tickFacts(ctx, input)
		})
		if err != nil {
			return nil, fmt.Errorf("create Tick Facts for %s: %w", e.key, err)
		}
		// NewFrame immediately clones the callback result before the provider
		// can reuse or mutate its maps.
		facts = values
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	frame := fact.NewFrame(facts)
	if e.store == nil {
		return nil, fmt.Errorf("ticket store is not initialized")
	}
	prefilterSession, err := e.store.beginPrefilterTick(frame.Tick())
	if err != nil {
		return nil, fmt.Errorf("begin prefilter Tick: %w", err)
	}
	return &seedSession{evaluator: e, now: input.Now, frame: frame, prefilter: prefilterSession, trace: trace}, nil
}

// Evaluate evaluates one already-reserved seed and returns a Match only after
// every predicate, Fact provider, and candidate-ranking decision required for
// that Match has succeeded. It does not mutate the Ticket store.
func (s *seedSession) Evaluate(ctx context.Context, seed *storedTicket) (*Match, error) {
	if s == nil || s.evaluator == nil || s.frame == nil || s.prefilter == nil {
		return nil, fmt.Errorf("seed evaluation session is not initialized")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if seed == nil || seed.Ticket == nil {
		return nil, fmt.Errorf("seed ticket is nil")
	}
	e := s.evaluator
	attemptPreparationStart := s.trace.start()
	seedFacts, err := s.frame.Object(seed.Ticket, s.now, e.objectFacts)
	if err != nil {
		s.trace.addDuration(produceStageAttemptPreparation, attemptPreparationStart)
		return nil, fmt.Errorf("seed %d: create Facts: %w", seed.TicketID, err)
	}
	tickFacts := s.frame.Tick()

	// MatchFactProvider is the only Match Fact writer. Its output is owned
	// before any predicate can observe it; the provider's Fact contract is
	// verified by provider tests rather than revalidated on every attempt.
	matchFacts, err := e.initializeMatchFacts(ctx, s.now, seed.Ticket, seedFacts, tickFacts)
	if err != nil {
		s.trace.addDuration(produceStageAttemptPreparation, attemptPreparationStart)
		return nil, err
	}
	s.trace.addDuration(produceStageAttemptPreparation, attemptPreparationStart)
	complete, err := s.canComplete(evaluation.CanCompleteInput{
		TickFacts:  tickFacts,
		MatchFacts: matchFacts,
	})
	if err != nil {
		return nil, err
	}
	group := []*Ticket{seed.Ticket}
	if complete {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return s.buildMatch(group, matchFacts), nil
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	prefilterStart := s.trace.start()
	candidateSet, prefilterStats, err := s.prefilter.CandidatesWithStats(seed.docID, seed.Ticket, seedFacts)
	if candidateSet != nil {
		candidateSet.Remove(seed.docID)
	}
	s.trace.addDuration(produceStagePrefilter, prefilterStart)
	s.trace.recordPrefilter(prefilterStats, candidateSet.Count())
	if err != nil {
		return nil, fmt.Errorf("seed %d: %w", seed.TicketID, err)
	}
	rankedCandidates, candidateErrors := s.topCandidates(ctx, candidateSet, seed, seedFacts, tickFacts)
	for _, candidate := range rankedCandidates {
		if len(group) >= e.maxPlayers {
			break
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		candidateFactStart := s.trace.start()
		s.trace.recordCandidateMaterialization()
		candidateFacts, candidateFactErr := s.frame.Object(candidate.Ticket, s.now, e.objectFacts)
		s.trace.addDuration(produceStageCandidateMaterialization, candidateFactStart)
		if candidateFactErr != nil {
			candidateErrors = errors.Join(candidateErrors, fmt.Errorf("candidate %d: create Facts: %w", candidate.TicketID, candidateFactErr))
			if isContextTermination(candidateFactErr) {
				return nil, candidateErrors
			}
			continue
		}
		join, err := s.canJoin(evaluation.CanJoinInput{
			Now:              s.now,
			SeedAttributes:   seed.Ticket,
			SeedFacts:        seedFacts,
			TickFacts:        tickFacts,
			Candidate:        candidate.Ticket,
			CandidateFacts:   candidateFacts,
			MatchFactsBefore: matchFacts,
		})
		if err != nil {
			return nil, err
		}
		if !join {
			continue
		}

		// OnJoin receives a clone of the old snapshot and returns the complete
		// next snapshot. Neither group nor Match Fact state changes until the
		// callback succeeds.
		var nextMatchFacts Facts
		if e.matchFacts != nil {
			matchFactUpdateStart := s.trace.start()
			nextMatchFacts, err = e.onJoinMatchFacts(ctx, s.now, seed.Ticket, seedFacts, tickFacts, candidate.Ticket, candidateFacts, matchFacts)
			s.trace.addDuration(produceStageMatchFactUpdate, matchFactUpdateStart)
			if err != nil {
				return nil, err
			}
			s.trace.recordMatchFactUpdate()
		}
		nextGroup := append(append([]*Ticket(nil), group...), candidate.Ticket)
		group, matchFacts = nextGroup, nextMatchFacts
		s.trace.recordJoinedCandidate()

		complete, err = s.canComplete(evaluation.CanCompleteInput{
			TickFacts:  tickFacts,
			MatchFacts: matchFacts,
		})
		if err != nil {
			return nil, err
		}
		if complete {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			return s.buildMatch(group, matchFacts), nil
		}
	}
	return nil, candidateErrors
}

func (s *seedSession) canJoin(input evaluation.CanJoinInput) (bool, error) {
	s.trace.recordCanJoin()
	started := s.trace.start()
	result, err := s.evaluator.evaluation.CanJoin(input)
	s.trace.addDuration(produceStageCanJoin, started)
	return result, err
}

func (s *seedSession) canComplete(input evaluation.CanCompleteInput) (bool, error) {
	s.trace.recordCanComplete()
	started := s.trace.start()
	result, err := s.evaluator.evaluation.CanComplete(input)
	s.trace.addDuration(produceStageCanComplete, started)
	return result, err
}

func (e *seedEvaluator) initializeMatchFacts(ctx context.Context, now int64, seed *Ticket, seedFacts, tickFacts Facts) (Facts, error) {
	if err := ctx.Err(); err != nil {
		return Facts{}, providerCanceledError("matchFacts.initialize", err)
	}
	if e.matchFacts == nil {
		return Facts{}, nil
	}
	input := InitializeInput{
		Now:            now,
		SeedAttributes: common.CloneTicket(seed),
		SeedFacts:      fact.Clone(seedFacts),
		TickFacts:      fact.Clone(tickFacts),
	}
	values, err := invokeProvider(ctx, "matchFacts.initialize", func() (Facts, error) {
		return e.matchFacts.Initialize(ctx, input)
	})
	if err != nil {
		return Facts{}, err
	}
	return fact.Clone(values), nil
}

func (e *seedEvaluator) onJoinMatchFacts(ctx context.Context, now int64, seed *Ticket, seedFacts, tickFacts Facts, candidate *Ticket, candidateFacts, before Facts) (Facts, error) {
	if e.matchFacts == nil {
		return Facts{}, providerError("matchFacts.onJoin", errors.New("MatchFactProvider is nil"))
	}
	if err := ctx.Err(); err != nil {
		return Facts{}, providerCanceledError("matchFacts.onJoin", err)
	}
	input := JoinInput{
		Now:              now,
		SeedAttributes:   common.CloneTicket(seed),
		SeedFacts:        fact.Clone(seedFacts),
		TickFacts:        fact.Clone(tickFacts),
		Candidate:        common.CloneTicket(candidate),
		CandidateFacts:   fact.Clone(candidateFacts),
		MatchFactsBefore: fact.Clone(before),
	}
	values, err := invokeProvider(ctx, "matchFacts.onJoin", func() (Facts, error) {
		return e.matchFacts.OnJoin(ctx, input)
	})
	if err != nil {
		return Facts{}, err
	}
	return fact.Clone(values), nil
}

func (s *seedSession) buildMatch(group []*Ticket, values Facts) *Match {
	matchBuildStart := s.trace.start()
	objectFacts := make(map[common.TicketID]common.MatchFacts, len(group))
	if s != nil && s.frame != nil {
		view := s.frame.View()
		for _, ticket := range group {
			if ticket == nil {
				continue
			}
			facts, ok := view.For(ticket)
			if !ok {
				// Every member has already passed through Frame.Object before
				// this point. Keep an explicit empty entry if a future evaluator
				// path constructs a member without materializing it, so callers
				// can distinguish an empty result from an absent map.
				objectFacts[ticket.TicketID] = common.MatchFacts{}
				continue
			}
			owned := fact.Clone(facts)
			objectFacts[ticket.TicketID] = common.MatchFacts{
				StringLists: owned.StringLists,
				Uint64Lists: owned.Uint64Lists,
				Int64Values: owned.Int64Values,
			}
		}
	}
	facts := common.MatchFacts{
		StringLists: values.StringLists,
		Uint64Lists: values.Uint64Lists,
		Int64Values: values.Int64Values,
	}
	match := &Match{
		Tickets:     group,
		Facts:       common.CloneMatchFacts(facts),
		ObjectFacts: common.CloneMatchObjectFacts(objectFacts),
	}
	s.trace.addDuration(produceStageMatchBuild, matchBuildStart)
	return match
}

func evalError(path, code, format string, args ...any) error {
	return &evaluation.Error{Phase: "evaluate", Path: path, Code: code, Err: fmt.Errorf(format, args...)}
}
