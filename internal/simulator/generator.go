package simulator

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"sort"
	"strconv"

	"matchSystem/internal/common"
)

const maxGeneratedTickets = 1_000_000

// GenerateBatch creates detached TicketInput values using an explicit,
// replayable random seed. Map keys are traversed in sorted order so output is
// stable across Go map iteration order and process runs.
func GenerateBatch(spec BatchGeneratorSpec) ([]TicketInput, error) {
	if err := validateBatchSpec(spec); err != nil {
		return nil, err
	}
	if spec.Count == 0 {
		return []TicketInput{}, nil
	}
	if spec.FirstTicketID == 0 {
		spec.FirstTicketID = 1
	}
	if spec.CreatedAtStep == 0 {
		spec.CreatedAtStep = 1
	}
	rng := rand.New(rand.NewSource(spec.Seed))
	stringFields := sortedStringKeys(spec.StringChoices)
	uint64Fields := sortedUint64Keys(spec.Uint64Choices)
	int64Fields := sortedRangeKeys(spec.Int64Ranges)
	result := make([]TicketInput, spec.Count)
	for index := 0; index < spec.Count; index++ {
		ticketID, err := addTicketID(spec.FirstTicketID, index)
		if err != nil {
			return nil, err
		}
		createdAt, err := addInt64(spec.CreatedAtStart, spec.CreatedAtStep, index)
		if err != nil {
			return nil, err
		}
		input := TicketInput{
			Rule:        spec.Rule,
			TicketID:    ticketID,
			CreatedAt:   createdAt,
			StringLists: cloneStringLists(spec.StringLists),
			Uint64Lists: cloneUint64Lists(spec.Uint64Lists),
			Int64Values: cloneInt64Values(spec.Int64Values),
			ObjectFacts: spec.ObjectFacts.clone(),
		}
		if spec.AffinityPrefix != "" {
			input.AffinityKey = spec.AffinityPrefix + strconv.FormatUint(ticketID, 10)
		}
		if spec.RequestIDPrefix != "" {
			input.RequestID = spec.RequestIDPrefix + strconv.FormatUint(ticketID, 10)
		}
		for _, field := range stringFields {
			choices := spec.StringChoices[field]
			input.StringLists[field] = []string{choices[rng.Intn(len(choices))]}
		}
		for _, field := range uint64Fields {
			choices := spec.Uint64Choices[field]
			input.Uint64Lists[field] = []uint64{choices[rng.Intn(len(choices))]}
		}
		for _, field := range int64Fields {
			rangeSpec := spec.Int64Ranges[field]
			input.Int64Values[field] = randomInt64(rng, rangeSpec.Min, rangeSpec.Max)
		}
		result[index] = input
	}
	return result, nil
}

func validateBatchSpec(spec BatchGeneratorSpec) error {
	if err := spec.Rule.Validate(); err != nil {
		return fmt.Errorf("%w: invalid Rule: %v", ErrInvalidBatchSpec, err)
	}
	if spec.Count < 0 || spec.Count > maxGeneratedTickets {
		return fmt.Errorf("%w: count must be between 0 and %d", ErrInvalidBatchSpec, maxGeneratedTickets)
	}
	for field, values := range spec.StringChoices {
		if field == "" || len(values) == 0 {
			return fmt.Errorf("%w: StringChoices[%q] must contain at least one value", ErrInvalidBatchSpec, field)
		}
	}
	for field, values := range spec.Uint64Choices {
		if field == "" || len(values) == 0 {
			return fmt.Errorf("%w: Uint64Choices[%q] must contain at least one value", ErrInvalidBatchSpec, field)
		}
	}
	for field, rangeSpec := range spec.Int64Ranges {
		if field == "" || rangeSpec.Min > rangeSpec.Max {
			return fmt.Errorf("%w: Int64Ranges[%q] has invalid bounds", ErrInvalidBatchSpec, field)
		}
	}
	if spec.FirstTicketID != 0 {
		if _, err := addTicketID(spec.FirstTicketID, spec.Count-1); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidBatchSpec, err)
		}
	}
	if _, err := addInt64(spec.CreatedAtStart, spec.CreatedAtStep, spec.Count-1); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidBatchSpec, err)
	}
	return nil
}

func addTicketID(first common.TicketID, index int) (common.TicketID, error) {
	if index < 0 {
		return first, nil
	}
	if uint64(index) > math.MaxUint64-first {
		return 0, fmt.Errorf("TicketID overflows at index %d", index)
	}
	return first + common.TicketID(index), nil
}

func addInt64(start, step int64, index int) (int64, error) {
	if index < 0 {
		return start, nil
	}
	if step > 0 && int64(index) > 0 && step > math.MaxInt64/int64(index) {
		return 0, fmt.Errorf("CreatedAt overflows at index %d", index)
	}
	if step < 0 && int64(index) > 0 && step < math.MinInt64/int64(index) {
		return 0, fmt.Errorf("CreatedAt underflows at index %d", index)
	}
	delta := step * int64(index)
	if delta > 0 && start > math.MaxInt64-delta {
		return 0, fmt.Errorf("CreatedAt overflows at index %d", index)
	}
	if delta < 0 && start < math.MinInt64-delta {
		return 0, fmt.Errorf("CreatedAt underflows at index %d", index)
	}
	return start + delta, nil
}

func randomInt64(rng *rand.Rand, minimum, maximum int64) int64 {
	if minimum == maximum {
		return minimum
	}
	// Unsigned subtraction gives the correct span for ranges crossing zero.
	// A span of zero means the full int64 domain and needs no modulo.
	span := uint64(maximum) - uint64(minimum) + 1
	if span == 0 {
		return int64(rng.Uint64())
	}
	return int64(uint64(minimum) + rng.Uint64()%span)
}

func sortedStringKeys(values map[string][]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedUint64Keys(values map[string][]uint64) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedRangeKeys(values map[string]Int64Range) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// GenerateTickets is a descriptive alias for GenerateBatch.
func GenerateTickets(spec BatchGeneratorSpec) ([]TicketInput, error) {
	return GenerateBatch(spec)
}

// GenerateBatch executes the deterministic generator without inserting the
// resulting Tickets. AddBatch combines generation, routing, and insertion.
func (s *Simulator) GenerateBatch(ctx context.Context, spec BatchGeneratorSpec) ([]TicketInput, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return GenerateBatch(spec)
}

// AddBatch generates and inserts Tickets in deterministic order. A returned
// error includes the successfully added prefix in BatchResult, making partial
// application explicit to callers.
func (s *Simulator) AddBatch(ctx context.Context, spec BatchGeneratorSpec) (BatchResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	inputs, err := GenerateBatch(spec)
	result := BatchResult{Seed: spec.Seed, Requested: spec.Count}
	if err != nil {
		return result, err
	}
	if s == nil {
		return result, ErrSimulatorClosed
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	runtime, err := s.runtimeReadLocked()
	if err != nil {
		return result, err
	}
	for _, input := range inputs {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		decision, routeErr := runtime.router.RouteNew(ctx, input.routeRequest())
		if routeErr != nil {
			return result, routeErr
		}
		added, addErr := runtime.addAtOwner(ctx, decision, input)
		if addErr != nil {
			return result, addErr
		}
		result.Added++
		result.TicketIDs = append(result.TicketIDs, added.Ticket.TicketID)
		result.Decisions = append(result.Decisions, added.Decision)
	}
	return result, nil
}

// AddTicketsBatch is an alias kept for callers that prefer a verb matching the
// REST endpoint name.
func (s *Simulator) AddTicketsBatch(ctx context.Context, spec BatchGeneratorSpec) (BatchResult, error) {
	return s.AddBatch(ctx, spec)
}
