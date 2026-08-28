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
}

// BeginSession creates the Tick Fact frame and Prefilter session. It is called
// after LogicalNode reserves its first seed, preserving the round rule that a
// provider/configuration failure never makes that seed selectable again.
func (e *seedEvaluator) BeginSession(ctx context.Context, now int64) (*seedSession, error) {
	if e == nil {
		return nil, fmt.Errorf("seed evaluator is nil")
	}
	facts := Facts{}
	if e.tickFacts != nil {
		values, err := invokeProvider(ctx, "tickFacts", func() (Facts, error) {
			return e.tickFacts(ctx, now)
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
	return &seedSession{evaluator: e, now: now, frame: frame, prefilter: prefilterSession}, nil
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
	seedFacts, err := s.frame.Object(seed.Ticket, s.now, e.objectFacts)
	if err != nil {
		return nil, fmt.Errorf("seed %d: create Facts: %w", seed.TicketID, err)
	}
	tickFacts := s.frame.Tick()

	// MatchFactProvider is the only Match Fact writer. Its output is owned
	// before any predicate can observe it; the provider's Fact contract is
	// verified by provider tests rather than revalidated on every attempt.
	matchFacts, err := e.initializeMatchFacts(ctx, s.now, seed.Ticket, seedFacts, tickFacts)
	if err != nil {
		return nil, err
	}
	complete, err := e.evaluation.CanComplete(evaluation.CanCompleteInput{
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
		return buildMatch(group, matchFacts), nil
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	candidateSet, err := s.prefilter.Candidates(seed.docID, seed.Ticket, seedFacts)
	if err != nil {
		return nil, fmt.Errorf("seed %d: %w", seed.TicketID, err)
	}
	candidateSet.Remove(seed.docID)
	rankedCandidates, candidateErrors := s.topCandidates(ctx, candidateSet, seed, seedFacts, tickFacts)
	for _, candidate := range rankedCandidates {
		if len(group) >= e.maxPlayers {
			break
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		candidateFacts, candidateFactErr := s.frame.Object(candidate.Ticket, s.now, e.objectFacts)
		if candidateFactErr != nil {
			candidateErrors = errors.Join(candidateErrors, fmt.Errorf("candidate %d: create Facts: %w", candidate.TicketID, candidateFactErr))
			if isContextTermination(candidateFactErr) {
				return nil, candidateErrors
			}
			continue
		}
		join, err := e.evaluation.CanJoin(evaluation.CanJoinInput{
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
			nextMatchFacts, err = e.onJoinMatchFacts(ctx, s.now, seed.Ticket, seedFacts, tickFacts, candidate.Ticket, candidateFacts, matchFacts)
			if err != nil {
				return nil, err
			}
		}
		nextGroup := append(append([]*Ticket(nil), group...), candidate.Ticket)
		group, matchFacts = nextGroup, nextMatchFacts

		complete, err = e.evaluation.CanComplete(evaluation.CanCompleteInput{
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
			return buildMatch(group, matchFacts), nil
		}
	}
	return nil, candidateErrors
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

func buildMatch(group []*Ticket, values Facts) *Match {
	facts := common.MatchFacts{
		StringLists: values.StringLists,
		Uint64Lists: values.Uint64Lists,
		Int64Values: values.Int64Values,
	}
	return &Match{Tickets: group, Facts: common.CloneMatchFacts(facts)}
}

func evalError(path, code, format string, args ...any) error {
	return &evaluation.Error{Phase: "evaluate", Path: path, Code: code, Err: fmt.Errorf(format, args...)}
}
