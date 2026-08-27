package matchsystem

import (
	"container/heap"
	"context"
	"errors"
	"fmt"
	"math"
	"sort"

	"matchSystem/internal/common"
	"matchSystem/internal/identity"
	"matchSystem/internal/matchsystem/contract"
	"matchSystem/internal/matchsystem/evaluation"
	"matchSystem/internal/matchsystem/fact"
	"matchSystem/internal/matchsystem/prefilter"
)

type LogicalNodeConfig struct {
	SeedScheduler SeedSchedulerConfig
	GroupBuilder  GroupBuilderConfig
	MaxPlayers    int
}

// LogicalNode owns one completely isolated matching partition, including its
// tickets, prefilter indexes, evaluation plan, scheduling state, and lifecycle.
// All methods must be called sequentially by the owning PhysicalNode goroutine.
// LogicalNode contains no synchronization and is not goroutine-safe.
type LogicalNode struct {
	key             identity.LogicalNodeKey
	contract        contract.Contract
	state           LogicalNodeState
	tickFacts       FactProvider
	objectFacts     ObjectFactProvider
	config          LogicalNodeConfig
	evaluation      evaluation.Predicates
	scorer          CandidateScorer
	matchFacts      MatchFactProvider
	factValidator   *fact.Validator
	builder         groupBuilder
	prefilterStore  *prefilter.IndexStore
	nextDocID       uint32
	ticketsByDocID  map[uint32]*storedTicket
	ticketIDToDocID map[TicketID]uint32
	// freeDocIDs is consumed only by Add. The owner contract forbids Add while
	// a match round is being consumed, so a recycled ID cannot make a stale
	// seed entry resolve to a newly added Ticket.
	freeDocIDs      []uint32
	arrivalOrder    []uint32
	seedOrderPolicy SeedOrderPolicy
	seedRound       seedRound
	seedCandidates  []*Ticket
	seedOrderSpare  []uint32
	oldestTickets   oldestTicketHeap
}

// Add deep-copies ticket exactly once and makes that copy immutable pool state.
func (p *LogicalNode) Add(ticket *Ticket) (uint32, error) {
	if ticket == nil {
		return 0, fmt.Errorf("ticket is nil")
	}
	if ticket.TicketID == 0 {
		return 0, fmt.Errorf("TicketID is required")
	}
	if _, exists := p.ticketIDToDocID[ticket.TicketID]; exists {
		return 0, fmt.Errorf("TicketID %d already exists", ticket.TicketID)
	}
	owned := common.CloneTicket(ticket)
	docID, err := p.allocateDocID()
	if err != nil {
		return 0, err
	}
	stored := &storedTicket{Ticket: owned, docID: docID}
	if err := p.prefilterStore.Add(stored.docID, stored.Ticket); err != nil {
		p.recycleDocID(stored.docID)
		return 0, err
	}
	p.ticketsByDocID[stored.docID] = stored
	p.ticketIDToDocID[stored.TicketID] = stored.docID
	stored.arrivalIndex = len(p.arrivalOrder)
	p.arrivalOrder = append(p.arrivalOrder, stored.docID)
	heap.Push(&p.oldestTickets, stored)
	return stored.docID, nil
}

func (p *LogicalNode) Remove(ticketID TicketID) bool {
	docID, ok := p.ticketIDToDocID[ticketID]
	if !ok {
		return false
	}
	p.removeDocID(docID)
	p.compactArrivalOrder()
	return true
}

// Get returns an owned deep copy of the requested Ticket. Mutating or retaining
// the returned value cannot affect the LogicalNode-owned Ticket.
func (p *LogicalNode) Get(ticketID TicketID) (*Ticket, bool) {
	ticket, ok := p.lookupTicket(ticketID)
	if !ok {
		return nil, false
	}
	return common.CloneTicket(ticket), true
}

// lookupTicket is an internal borrowed lookup for matching and lifecycle code.
// Callers must not mutate it or retain it across another owner command. A
// committed match transfers ownership of the same pointer.
func (p *LogicalNode) lookupTicket(ticketID TicketID) (*Ticket, bool) {
	docID, ok := p.ticketIDToDocID[ticketID]
	if !ok {
		return nil, false
	}
	stored := p.ticketsByDocID[docID]
	if stored == nil {
		return nil, false
	}
	return stored.Ticket, true
}

func (p *LogicalNode) Len() int { return len(p.ticketsByDocID) }

// BeginMatchRound captures one immutable seed order and resets its cursor.
// Tickets added after this call become eligible in the next round.
func (p *LogicalNode) BeginMatchRound(now int64) error {
	round, err := p.buildSeedRound(now)
	if err != nil {
		return err
	}
	p.installSeedRound(round)
	return nil
}

func (p *LogicalNode) produceMatchFromSeed(ctx context.Context, now int64, facts Facts, firstSeed *storedTicket) (*Match, error) {
	if firstSeed == nil {
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	attemptLimit := p.config.SeedScheduler.AttemptLimitPerProduceMatch
	// firstSeed has already been reserved by nextSeed, so add it back when
	// calculating the remaining per-round capacity for this call.
	if remaining := p.config.SeedScheduler.AttemptLimitPerMatchRound - p.seedRound.attemptedSeeds + 1; attemptLimit > remaining {
		attemptLimit = remaining
	}
	if attemptLimit <= 0 {
		return nil, nil
	}
	frame, err := fact.NewFrame(facts, p.contract.Facts)
	if err != nil {
		return nil, fmt.Errorf("begin Fact frame: %w", err)
	}
	session, err := p.prefilterStore.BeginTick(frame.Tick())
	if err != nil {
		return nil, fmt.Errorf("begin prefilter Tick: %w", err)
	}
	var tickErrors []error
	for attempted := 0; attempted < attemptLimit; attempted++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		seed := firstSeed
		if attempted > 0 {
			seed = p.nextSeed()
		}
		if seed == nil {
			break
		}
		match, err := p.evaluateSeed(ctx, now, frame, session, seed)
		if err != nil {
			if isContextTermination(err) {
				return nil, err
			}
			var evaluationErr *evaluation.Error
			if errors.As(err, &evaluationErr) {
				return nil, err
			}
			tickErrors = append(tickErrors, fmt.Errorf("seed %d: evaluation: %w", seed.TicketID, err))
			continue
		}
		if match != nil {
			return match, nil
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nil, errors.Join(tickErrors...)
}

func (p *LogicalNode) evaluateSeed(ctx context.Context, now int64, frame *fact.Frame, session *prefilter.TickSession, seed *storedTicket) (*Match, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	seedFacts, err := frame.Object(seed.Ticket, now, p.objectFacts)
	if err != nil {
		return nil, fmt.Errorf("seed %d: create Facts: %w", seed.TicketID, err)
	}
	tickFacts := frame.Tick()

	// MatchFactProvider is the only Match Fact writer.  Its output is fully
	// validated and owned before any predicate can observe it.
	matchFacts, err := p.initializeMatchFacts(ctx, now, seed.Ticket, seedFacts, tickFacts)
	if err != nil {
		return nil, err
	}
	complete, err := p.evaluation.CanComplete(evaluation.CanCompleteInput{
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
		return p.commitMatch(group, matchFacts), nil
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	candidateSet, err := session.Candidates(seed.docID, seed.Ticket, seedFacts)
	if err != nil {
		return nil, fmt.Errorf("seed %d: %w", seed.TicketID, err)
	}
	candidateSet.Remove(seed.docID)
	rankedCandidates, candidateErrors := p.topCandidates(ctx, candidateSet, now, frame, seed, seedFacts, tickFacts)
	for _, candidate := range rankedCandidates {
		if len(group) >= p.builder.maxPlayers {
			break
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		candidateFacts, candidateFactErr := frame.Object(candidate.Ticket, now, p.objectFacts)
		if candidateFactErr != nil {
			candidateErrors = errors.Join(candidateErrors, fmt.Errorf("candidate %d: create Facts: %w", candidate.TicketID, candidateFactErr))
			if isContextTermination(candidateFactErr) {
				return nil, candidateErrors
			}
			continue
		}
		join, err := p.evaluation.CanJoin(evaluation.CanJoinInput{
			Now:              now,
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
		// next snapshot.  No group or Match Fact state is changed until both
		// the callback and full Contract validation succeed.
		var nextMatchFacts Facts
		if p.matchFacts != nil {
			nextMatchFacts, err = p.onJoinMatchFacts(ctx, now, seed.Ticket, seedFacts, tickFacts, candidate.Ticket, candidateFacts, matchFacts)
			if err != nil {
				return nil, err
			}
		}
		nextGroup := append(append([]*Ticket(nil), group...), candidate.Ticket)
		group, matchFacts = nextGroup, nextMatchFacts

		complete, err = p.evaluation.CanComplete(evaluation.CanCompleteInput{
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
			return p.commitMatch(group, matchFacts), nil
		}
	}
	return nil, candidateErrors
}

func (p *LogicalNode) initializeMatchFacts(ctx context.Context, now int64, seed *Ticket, seedFacts, tickFacts Facts) (Facts, error) {
	if err := ctx.Err(); err != nil {
		return Facts{}, providerCanceledError("matchFacts.initialize", err)
	}
	if p.matchFacts == nil {
		return Facts{}, nil
	}
	input := InitializeInput{
		Now:            now,
		SeedAttributes: common.CloneTicket(seed),
		SeedFacts:      fact.Clone(seedFacts),
		TickFacts:      fact.Clone(tickFacts),
	}
	values, err := invokeProvider(ctx, "matchFacts.initialize", func() (Facts, error) {
		return p.matchFacts.Initialize(ctx, input)
	})
	if err != nil {
		return Facts{}, err
	}
	return p.cloneValidatedMatchFacts("matchFacts.initialize", values)
}

func (p *LogicalNode) onJoinMatchFacts(ctx context.Context, now int64, seed *Ticket, seedFacts, tickFacts Facts, candidate *Ticket, candidateFacts, before Facts) (Facts, error) {
	if p.matchFacts == nil {
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
		return p.matchFacts.OnJoin(ctx, input)
	})
	if err != nil {
		return Facts{}, err
	}
	return p.cloneValidatedMatchFacts("matchFacts.onJoin", values)
}

func (p *LogicalNode) cloneValidatedMatchFacts(path string, values Facts) (Facts, error) {
	if p.factValidator == nil {
		return Facts{}, evalError(path, "INVALID_VALIDATOR", "Match Fact validator is not initialized")
	}
	owned, err := p.factValidator.CloneValidatedMatch(path, values)
	if err != nil {
		return Facts{}, evaluationErrorFromFact(err)
	}
	return owned, nil
}

func invokeProvider(ctx context.Context, path string, invoke func() (Facts, error)) (values Facts, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = &evaluation.Error{Phase: "evaluate", Path: path, Code: "PROVIDER_PANIC", Err: panicError("provider panic", recovered)}
			values = Facts{}
			return
		}
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
				err = providerCanceledError(path, err)
			} else {
				err = providerError(path, err)
			}
			values = Facts{}
			return
		}
		if canceled := ctx.Err(); canceled != nil {
			err = providerCanceledError(path, canceled)
			values = Facts{}
		}
	}()
	if canceled := ctx.Err(); canceled != nil {
		return Facts{}, providerCanceledError(path, canceled)
	}
	return invoke()
}

func providerError(path string, err error) error {
	return &evaluation.Error{Phase: "evaluate", Path: path, Code: "PROVIDER_ERROR", Err: err}
}

func providerCanceledError(path string, err error) error {
	return &evaluation.Error{Phase: "evaluate", Path: path, Code: "PROVIDER_CANCELED", Err: err}
}

func isContextTermination(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func panicError(prefix string, recovered any) error {
	if panicErr, ok := recovered.(error); ok {
		return fmt.Errorf("%s: %w", prefix, panicErr)
	}
	return fmt.Errorf("%s: %v", prefix, recovered)
}

func (p *LogicalNode) commitMatch(group []*Ticket, values Facts) *Match {
	for _, ticket := range group {
		if docID, ok := p.ticketIDToDocID[ticket.TicketID]; ok {
			p.removeDocID(docID)
		}
	}
	p.compactArrivalOrder()
	facts := common.MatchFacts{StringLists: values.StringLists, Uint64Lists: values.Uint64Lists, Int64Values: values.Int64Values}
	return &Match{Tickets: group, Facts: common.CloneMatchFacts(facts)}
}

func evalError(path, code, format string, args ...any) error {
	return &evaluation.Error{Phase: "evaluate", Path: path, Code: code, Err: fmt.Errorf(format, args...)}
}

func evaluationErrorFromFact(err error) error {
	if factErr, ok := err.(*fact.Error); ok {
		return evalError(factErr.Path, factErr.Code, "%v", factErr.Err)
	}
	return evalError("matchFacts", "FACTS", "%v", err)
}

// nextSeed reserves one valid seed in the current matching round. The cursor
// advances before evaluation, so failures never make a seed selectable again
// in that round. Deleted DocIDs remain harmless stale entries in the round
// snapshot and do not consume the round attempt budget.
func (p *LogicalNode) nextSeed() *storedTicket {
	p.advancePastStaleSeeds()
	if p.seedRound.cursor == len(p.seedRound.order) ||
		p.seedRound.attemptedSeeds >= p.config.SeedScheduler.AttemptLimitPerMatchRound {
		return nil
	}
	docID := p.seedRound.order[p.seedRound.cursor]
	p.seedRound.cursor++
	p.seedRound.attemptedSeeds++
	return p.ticketsByDocID[docID]
}

func (p *LogicalNode) hasUntriedSeed() bool {
	if !p.seedRound.initialized {
		return false
	}
	p.advancePastStaleSeeds()
	return p.seedRound.cursor < len(p.seedRound.order) &&
		p.seedRound.attemptedSeeds < p.config.SeedScheduler.AttemptLimitPerMatchRound
}

// advancePastStaleSeeds permanently consumes deleted entries for this round.
// The owner contract forbids Add while a round is being consumed, so a recycled
// DocID cannot become active again before that round ends.
func (p *LogicalNode) advancePastStaleSeeds() {
	for p.seedRound.cursor < len(p.seedRound.order) {
		if p.ticketsByDocID[p.seedRound.order[p.seedRound.cursor]] != nil {
			return
		}
		p.seedRound.cursor++
	}
}

func (p *LogicalNode) oldestCreatedAt() (int64, bool) {
	for len(p.oldestTickets) > 0 {
		oldest := p.oldestTickets[0]
		if p.ticketsByDocID[oldest.docID] == oldest {
			return oldest.CreatedAt, true
		}
		heap.Pop(&p.oldestTickets)
	}
	return 0, false
}

func (p *LogicalNode) buildSeedRound(now int64) (seedRound, error) {
	if policy, ok := p.seedOrderPolicy.(optimizedSeedOrderPolicy); ok {
		order, ownsOrder := policy.buildOrder(p, p.seedOrderSpare)
		return seedRound{now: now, order: order, ownsOrder: ownsOrder, initialized: true}, nil
	}
	p.seedCandidates = p.seedCandidates[:0]
	// Custom policies receive the complete active candidate list so they can
	// choose a globally best subset. Their output is bounded separately by
	// SeedOrderContext.MaxSeeds and resolveSeedOrder.
	for _, docID := range p.arrivalOrder {
		if ticket := p.ticketsByDocID[docID]; ticket != nil {
			p.seedCandidates = append(p.seedCandidates, ticket.Ticket)
		}
	}
	ticketOrder, err := p.seedOrderPolicy.BuildOrder(SeedOrderContext{
		Now:        now,
		Candidates: p.seedCandidates,
		MaxSeeds:   p.config.SeedScheduler.AttemptLimitPerMatchRound,
	})
	if err != nil {
		return seedRound{}, fmt.Errorf("build seed order for LogicalNode %s: %w", p.key, err)
	}
	order, err := p.resolveSeedOrder(ticketOrder)
	if err != nil {
		return seedRound{}, fmt.Errorf("validate seed order for LogicalNode %s: %w", p.key, err)
	}
	return seedRound{now: now, order: order, ownsOrder: true, initialized: true}, nil
}

func (p *LogicalNode) resolveSeedOrder(ticketOrder []TicketID) ([]uint32, error) {
	order := make([]uint32, len(ticketOrder))
	if len(order) > p.config.SeedScheduler.AttemptLimitPerMatchRound {
		return nil, fmt.Errorf("policy returned %d TicketIDs, maximum is %d", len(order), p.config.SeedScheduler.AttemptLimitPerMatchRound)
	}
	seen := prefilter.NewDocSet()
	for index, ticketID := range ticketOrder {
		docID, exists := p.ticketIDToDocID[ticketID]
		if !exists || seen.Contains(docID) {
			return nil, fmt.Errorf("policy returned duplicate or unknown TicketID %d", ticketID)
		}
		seen.Add(docID)
		order[index] = docID
	}
	return order, nil
}

func (p *LogicalNode) installSeedRound(round seedRound) {
	previous := p.seedRound
	p.seedRound = round
	if previous.ownsOrder {
		p.seedOrderSpare = previous.order[:0]
	} else if round.ownsOrder {
		// Optimized built-ins may have promoted seedOrderSpare to the active
		// round. It must not be reused until that round is replaced.
		p.seedOrderSpare = nil
	}
}

func (p *LogicalNode) topCandidates(ctx context.Context, candidates *prefilter.DocSet, now int64, frame *fact.Frame, seed *storedTicket, seedFacts, tickFacts Facts) ([]*storedTicket, error) {
	limit := p.builder.candidateLimit
	best := make(candidateHeap, 0, limit)
	var candidateErrors []error
	scoringFailed := false
	contextFailed := false
	candidates.ForEach(func(docID uint32) bool {
		if contextErr := ctx.Err(); contextErr != nil {
			candidateErrors = append(candidateErrors, contextErr)
			contextFailed = true
			return false
		}
		ticket := p.ticketsByDocID[docID]
		if ticket == nil {
			return true
		}
		candidateFacts, err := frame.Object(ticket.Ticket, now, p.objectFacts)
		if err != nil {
			candidateErrors = append(candidateErrors, fmt.Errorf("candidate %d: create Facts: %w", ticket.TicketID, err))
			if isContextTermination(err) {
				contextFailed = true
				return false
			}
			return true
		}
		score, err := p.scoreCandidate(ctx, now, seed.Ticket, seedFacts, tickFacts, ticket.Ticket, candidateFacts)
		if err != nil {
			candidateErrors = append(candidateErrors, fmt.Errorf("candidate %d: score: %w", ticket.TicketID, err))
			scoringFailed = true
			return false
		}
		entry := candidateEntry{ticket: ticket, score: score}
		if len(best) < limit {
			heap.Push(&best, entry)
		} else if betterCandidate(entry, best[0]) {
			best[0] = entry
			heap.Fix(&best, 0)
		}
		return true
	})
	if scoringFailed || contextFailed {
		// A scorer is part of the candidate selection contract. Once it fails,
		// or the context reaches a terminal state, the current ProduceMatch must
		// fail closed instead of allowing a later candidate to create a Match
		// from a partial ranking.
		return nil, errors.Join(candidateErrors...)
	}
	sort.Slice(best, func(i, j int) bool { return betterCandidate(best[i], best[j]) })
	out := make([]*storedTicket, len(best))
	for i := range best {
		out[i] = best[i].ticket
	}
	return out, errors.Join(candidateErrors...)
}

func (p *LogicalNode) scoreCandidate(ctx context.Context, now int64, seed *Ticket, seedFacts, tickFacts Facts, candidate *Ticket, candidateFacts Facts) (score float64, err error) {
	if p.scorer == nil {
		return 0, evalError("candidateScorer", "MISSING_SCORER", "CandidateScorer is nil")
	}
	if canceled := ctx.Err(); canceled != nil {
		return 0, canceled
	}
	input := CandidateScoreContext{
		Seed:           common.CloneTicket(seed),
		Candidate:      common.CloneTicket(candidate),
		Now:            now,
		TickFacts:      fact.Clone(tickFacts),
		SeedFacts:      fact.Clone(seedFacts),
		CandidateFacts: fact.Clone(candidateFacts),
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			score = 0
			err = &evaluation.Error{Phase: "evaluate", Path: "candidateScorer", Code: "SCORER_PANIC", Err: panicError("candidate scorer panic", recovered)}
			return
		}
		if err != nil {
			err = &evaluation.Error{Phase: "evaluate", Path: "candidateScorer", Code: "SCORER_ERROR", Err: err}
			return
		}
		if math.IsNaN(score) || math.IsInf(score, 0) {
			score = 0
			err = evalError("candidateScorer", "NONFINITE_SCORE", "candidate scorer returned non-finite score")
		}
	}()
	score, err = p.scorer(input)
	return score, err
}

func (p *LogicalNode) removeDocID(docID uint32) {
	ticket := p.ticketsByDocID[docID]
	if ticket == nil {
		return
	}
	p.prefilterStore.Remove(docID)
	if index := ticket.arrivalIndex; index >= 0 && index < len(p.arrivalOrder) && p.arrivalOrder[index] == docID {
		p.arrivalOrder[index] = 0
	}
	delete(p.ticketsByDocID, docID)
	delete(p.ticketIDToDocID, ticket.TicketID)
	p.recycleDocID(docID)
}

func (p *LogicalNode) allocateDocID() (uint32, error) {
	if last := len(p.freeDocIDs) - 1; last >= 0 {
		docID := p.freeDocIDs[last]
		p.freeDocIDs = p.freeDocIDs[:last]
		return docID, nil
	}
	if p.nextDocID == 0 {
		return 0, fmt.Errorf("DocID space is exhausted")
	}
	docID := p.nextDocID
	p.nextDocID++
	return docID, nil
}

func (p *LogicalNode) recycleDocID(docID uint32) {
	if docID != 0 {
		p.freeDocIDs = append(p.freeDocIDs, docID)
	}
}

func (p *LogicalNode) compactArrivalOrder() {
	if len(p.arrivalOrder) <= len(p.ticketsByDocID)*2+1024 {
		return
	}
	compacted := make([]uint32, 0, len(p.ticketsByDocID))
	for _, docID := range p.arrivalOrder {
		if ticket := p.ticketsByDocID[docID]; ticket != nil {
			ticket.arrivalIndex = len(compacted)
			compacted = append(compacted, docID)
		}
	}
	p.arrivalOrder = compacted
}

type candidateEntry struct {
	ticket *storedTicket
	score  float64
}

type oldestTicketHeap []*storedTicket

func (h oldestTicketHeap) Len() int { return len(h) }
func (h oldestTicketHeap) Less(i, j int) bool {
	if h[i].CreatedAt != h[j].CreatedAt {
		return h[i].CreatedAt < h[j].CreatedAt
	}
	return h[i].docID < h[j].docID
}
func (h oldestTicketHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *oldestTicketHeap) Push(value any) {
	*h = append(*h, value.(*storedTicket))
}
func (h *oldestTicketHeap) Pop() any {
	old := *h
	value := old[len(old)-1]
	*h = old[:len(old)-1]
	return value
}

type candidateHeap []candidateEntry

func (h candidateHeap) Len() int { return len(h) }
func (h candidateHeap) Less(i, j int) bool {
	if h[i].score != h[j].score {
		return h[i].score < h[j].score
	}
	return h[i].ticket.docID > h[j].ticket.docID
}
func (h candidateHeap) Swap(i, j int)   { h[i], h[j] = h[j], h[i] }
func (h *candidateHeap) Push(value any) { *h = append(*h, value.(candidateEntry)) }
func (h *candidateHeap) Pop() any {
	old := *h
	value := old[len(old)-1]
	*h = old[:len(old)-1]
	return value
}
func betterCandidate(left, right candidateEntry) bool {
	if left.score != right.score {
		return left.score > right.score
	}
	return left.ticket.docID < right.ticket.docID
}
