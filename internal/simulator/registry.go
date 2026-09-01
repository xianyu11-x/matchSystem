package simulator

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"

	"matchSystem/internal/common"
	"matchSystem/internal/identity"
	"matchSystem/internal/matchsystem"
)

var (
	ErrTicketAlreadyObserved = errors.New("ticket is already present in simulator observations")
	ErrTicketNotObserved     = errors.New("ticket is not present in simulator observations")
	ErrTicketAmbiguous       = errors.New("ticket id belongs to multiple owners")
	ErrInvalidCursor         = errors.New("invalid observation cursor")
)

// DefaultMatchHistoryLimit bounds the number of completed Match snapshots
// retained by a simulator runtime when the scenario does not specify an
// explicit limit. Match history is an in-memory observation facility, not a
// durable store; retaining a bounded tail prevents completed Matches from
// growing without limit during long simulations.
const DefaultMatchHistoryLimit = 1000

type observationKey struct {
	owner    identity.OwnerRef
	ticketID common.TicketID
}

type ticketObservation struct {
	key         observationKey
	sequence    uint64
	ticket      *common.Ticket
	decision    common.RouteDecision
	objectFacts matchsystem.Facts
	status      TicketStatus
}

// ObservationRegistry owns detached Ticket and Fact observations. The key is
// deliberately (OwnerRef, TicketID): TicketID is only unique inside one
// LogicalNode store.
type ObservationRegistry struct {
	mu          sync.RWMutex
	nextSeq     uint64
	tickets     map[observationKey]*ticketObservation
	matches     []MatchRecord
	matchesByID map[string]MatchRecord
	matchLimit  int
}

func NewObservationRegistry() *ObservationRegistry {
	return NewObservationRegistryWithLimit(DefaultMatchHistoryLimit)
}

// NewObservationRegistryWithLimit creates an observation registry with a
// bounded completed-Match history. A non-positive limit selects the default;
// callers that need to disable history should not use the registry as their
// observation store because Match records are part of the simulator API.
func NewObservationRegistryWithLimit(limit int) *ObservationRegistry {
	if limit <= 0 {
		limit = DefaultMatchHistoryLimit
	}
	return &ObservationRegistry{
		tickets:     make(map[observationKey]*ticketObservation),
		matchesByID: make(map[string]MatchRecord),
		matchLimit:  limit,
	}
}

// NewObservationRegistryWithMatchLimit is a descriptive alias retained for
// callers that prefer the configuration name used by Scenario.
func NewObservationRegistryWithMatchLimit(limit int) *ObservationRegistry {
	return NewObservationRegistryWithLimit(limit)
}

func (r *ObservationRegistry) RecordTicket(owner identity.OwnerRef, decision common.RouteDecision, ticket *common.Ticket, objectFacts FactSnapshot) (TicketView, error) {
	if r == nil {
		return TicketView{}, fmt.Errorf("observation registry is nil")
	}
	if err := owner.Validate(); err != nil {
		return TicketView{}, err
	}
	if decision.Owner != (identity.OwnerRef{}) && decision.Owner != owner {
		return TicketView{}, fmt.Errorf("RouteDecision owner does not match observation owner")
	}
	if ticket == nil || ticket.TicketID == 0 {
		return TicketView{}, fmt.Errorf("ticket is nil or TicketID is zero")
	}
	key := observationKey{owner: owner, ticketID: ticket.TicketID}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.tickets[key]; exists {
		return TicketView{}, ErrTicketAlreadyObserved
	}
	r.nextSeq++
	entry := &ticketObservation{
		key:         key,
		sequence:    r.nextSeq,
		ticket:      common.CloneTicket(ticket),
		decision:    cloneRouteDecision(decision),
		objectFacts: objectFacts.values(),
		status:      TicketWaiting,
	}
	r.tickets[key] = entry
	return entry.view(), nil
}

// SetObjectFacts records a detached object Fact snapshot for an observed
// Ticket. Contract validation is performed by Simulator before this method.
func (r *ObservationRegistry) SetObjectFacts(owner identity.OwnerRef, ticketID common.TicketID, values FactSnapshot) error {
	if r == nil {
		return fmt.Errorf("observation registry is nil")
	}
	key := observationKey{owner: owner, ticketID: ticketID}
	r.mu.Lock()
	defer r.mu.Unlock()
	entry := r.tickets[key]
	if entry == nil {
		return ErrTicketNotObserved
	}
	entry.objectFacts = values.values()
	return nil
}

func (r *ObservationRegistry) ObjectFacts(owner identity.OwnerRef, ticketID common.TicketID) (matchsystem.Facts, bool) {
	if r == nil {
		return matchsystem.Facts{}, false
	}
	key := observationKey{owner: owner, ticketID: ticketID}
	r.mu.RLock()
	entry := r.tickets[key]
	if entry == nil {
		r.mu.RUnlock()
		return matchsystem.Facts{}, false
	}
	values := cloneFacts(entry.objectFacts)
	r.mu.RUnlock()
	return values, true
}

// GetObjectFacts is the DTO-friendly counterpart of ObjectFacts. The
// returned snapshot is detached and can be safely retained by an HTTP layer.
func (r *ObservationRegistry) GetObjectFacts(owner identity.OwnerRef, ticketID common.TicketID) (FactSnapshot, bool) {
	values, ok := r.ObjectFacts(owner, ticketID)
	if !ok {
		return FactSnapshot{}, false
	}
	return factSnapshot(values), true
}

func (r *ObservationRegistry) GetTicket(owner identity.OwnerRef, ticketID common.TicketID) (TicketView, bool) {
	if r == nil {
		return TicketView{}, false
	}
	key := observationKey{owner: owner, ticketID: ticketID}
	r.mu.RLock()
	entry := r.tickets[key]
	if entry == nil {
		r.mu.RUnlock()
		return TicketView{}, false
	}
	view := entry.view()
	r.mu.RUnlock()
	return view, true
}

func (r *ObservationRegistry) RemoveTicket(owner identity.OwnerRef, ticketID common.TicketID) bool {
	if r == nil {
		return false
	}
	key := observationKey{owner: owner, ticketID: ticketID}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.tickets[key]; !exists {
		return false
	}
	delete(r.tickets, key)
	return true
}

func (r *ObservationRegistry) OwnersForTicket(ticketID common.TicketID) []identity.OwnerRef {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	owners := make([]identity.OwnerRef, 0)
	for key := range r.tickets {
		if key.ticketID == ticketID {
			owners = append(owners, key.owner)
		}
	}
	r.mu.RUnlock()
	sort.Slice(owners, func(i, j int) bool { return owners[i].String() < owners[j].String() })
	return owners
}

// CommitMatch removes all waiting members and appends a detached immutable
// MatchRecord. All members are checked before any deletion.
func (r *ObservationRegistry) CommitMatch(owner identity.OwnerRef, match *common.Match, matchID string, round uint64, now int64, physicalID identity.PhysicalNodeID, logicalID identity.LogicalNodeKey) (MatchRecord, error) {
	if r == nil {
		return MatchRecord{}, fmt.Errorf("observation registry is nil")
	}
	if match == nil {
		return MatchRecord{}, fmt.Errorf("match is nil")
	}
	if matchID == "" {
		return MatchRecord{}, fmt.Errorf("match id is required")
	}
	if err := owner.Validate(); err != nil {
		return MatchRecord{}, err
	}
	if owner.PhysicalNodeID != physicalID || owner.LogicalNode != logicalID {
		return MatchRecord{}, fmt.Errorf("match owner does not agree with Match result")
	}
	seen := make(map[common.TicketID]struct{}, len(match.Tickets))
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.matchesByID == nil {
		r.matchesByID = make(map[string]MatchRecord, len(r.matches))
		for _, existing := range r.matches {
			r.matchesByID[existing.ID] = cloneMatchRecord(existing)
		}
	}
	if _, exists := r.matchesByID[matchID]; exists {
		return MatchRecord{}, fmt.Errorf("match id %q is already recorded", matchID)
	}
	views := make([]TicketView, len(match.Tickets))
	keys := make([]observationKey, len(match.Tickets))
	for index, ticket := range match.Tickets {
		if ticket == nil || ticket.TicketID == 0 {
			return MatchRecord{}, fmt.Errorf("match ticket %d is nil or TicketID is zero", index)
		}
		if _, exists := seen[ticket.TicketID]; exists {
			return MatchRecord{}, fmt.Errorf("match contains duplicate TicketID %d", ticket.TicketID)
		}
		seen[ticket.TicketID] = struct{}{}
		key := observationKey{owner: owner, ticketID: ticket.TicketID}
		entry := r.tickets[key]
		if entry == nil {
			return MatchRecord{}, fmt.Errorf("match TicketID %d is not observed for %s", ticket.TicketID, owner)
		}
		view := entry.view()
		if match.ObjectFacts != nil {
			// Prefer the Object Fact snapshot materialized by this matching
			// frame. The input observation can differ when a custom provider
			// derives or normalizes Object Facts for evaluation.
			objectFacts, exists := match.ObjectFacts[ticket.TicketID]
			if exists {
				view.ObjectFacts = factSnapshotFromMatchFacts(objectFacts)
			} else {
				view.ObjectFacts = FactSnapshot{}
			}
		}
		view.Status = TicketMatched
		views[index] = view
		keys[index] = key
	}
	for _, key := range keys {
		delete(r.tickets, key)
	}
	record := MatchRecord{
		ID:             matchID,
		Round:          round,
		Now:            now,
		DurationMs:     matchWaitDuration(now, match.Tickets),
		PhysicalNodeID: physicalID,
		LogicalNode:    logicalID,
		Tickets:        views,
		Facts:          factSnapshotFromMatch(match),
	}
	stored := cloneMatchRecord(record)
	r.matches = append(r.matches, stored)
	r.matchesByID[matchID] = cloneMatchRecord(stored)
	limit := r.matchLimit
	if limit <= 0 {
		limit = DefaultMatchHistoryLimit
		r.matchLimit = limit
	}
	if len(r.matches) > limit {
		evicted := r.matches[0]
		delete(r.matchesByID, evicted.ID)
		copy(r.matches, r.matches[1:])
		last := len(r.matches) - 1
		// Clear the unused backing-array slot so an evicted record's nested
		// maps and slices are eligible for collection immediately.
		r.matches[last] = MatchRecord{}
		r.matches = r.matches[:last]
	}
	return cloneMatchRecord(record), nil
}

// matchWaitDuration reports the amount of time the oldest member waited
// before this round committed the Match. CreatedAt and now use the same
// caller-defined unit (the HTTP adapter uses Unix milliseconds). The value is
// clamped at zero for future-dated tickets and at MaxInt64 on subtraction
// overflow. This deliberately does not claim to measure matching CPU time,
// which is not part of the simulator's current observation model.
func matchWaitDuration(now int64, tickets []*common.Ticket) int64 {
	var oldest int64
	found := false
	for _, ticket := range tickets {
		if ticket == nil {
			continue
		}
		if !found || ticket.CreatedAt < oldest {
			oldest = ticket.CreatedAt
			found = true
		}
	}
	if !found || now <= oldest {
		return 0
	}
	// now-oldest can overflow when now is positive and oldest is negative.
	if oldest < 0 && now > math.MaxInt64+oldest {
		return math.MaxInt64
	}
	return now - oldest
}

func (r *ObservationRegistry) ListTickets(query TicketQuery) (TicketPage, error) {
	if r == nil {
		return TicketPage{}, fmt.Errorf("observation registry is nil")
	}
	start, limit, err := pageBounds(query.Cursor, query.Limit)
	if err != nil {
		return TicketPage{}, err
	}
	r.mu.RLock()
	entries := make([]*ticketObservation, 0, len(r.tickets))
	for _, entry := range r.tickets {
		if !ticketMatches(entry, query) {
			continue
		}
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].sequence < entries[j].sequence })
	total := len(entries)
	items := make([]TicketView, 0, pageItemCapacity(start, total, limit))
	if start < total {
		end := total
		if limit < total-start {
			end = start + limit
		}
		for _, entry := range entries[start:end] {
			items = append(items, entry.view())
		}
	}
	r.mu.RUnlock()
	return TicketPage{Items: items, NextCursor: nextCursor(start, limit, total), Total: total}, nil
}

func (r *ObservationRegistry) ListMatches(query MatchQuery) (MatchPage, error) {
	if r == nil {
		return MatchPage{}, fmt.Errorf("observation registry is nil")
	}
	start, limit, err := pageBounds(query.Cursor, query.Limit)
	if err != nil {
		return MatchPage{}, err
	}
	r.mu.RLock()
	// The retained slice is oldest-first for cheap tail eviction; the public
	// page is intentionally newest-first for recent-Match inspection.
	total := len(r.matches)
	end := total
	if start < total && limit < total-start {
		end = start + limit
	}
	items := make([]MatchRecord, 0, pageItemCapacity(start, total, limit))
	if start < total {
		// Match history is exposed newest-first so the first page is the
		// recent tail users typically want to inspect.
		for offset := start; offset < end; offset++ {
			record := r.matches[total-1-offset]
			items = append(items, cloneMatchRecord(record))
		}
	}
	r.mu.RUnlock()
	return MatchPage{Items: items, NextCursor: nextCursor(start, limit, total), Total: total}, nil
}

// GetMatch returns one detached completed-Match snapshot. The boolean is
// false when the ID is not retained (including after history eviction or a
// scenario replacement). The registry lock protects both the ID index and
// the nested map/slice snapshots.
func (r *ObservationRegistry) GetMatch(matchID string) (MatchRecord, bool) {
	if r == nil || strings.TrimSpace(matchID) == "" {
		return MatchRecord{}, false
	}
	r.mu.RLock()
	record, ok := r.matchesByID[matchID]
	if !ok {
		// A zero-value ObservationRegistry is still safe to use. The fallback
		// also keeps registries created as struct literals queryable until the
		// first CommitMatch initializes the index.
		for _, candidate := range r.matches {
			if candidate.ID == matchID {
				record, ok = candidate, true
				break
			}
		}
	}
	if !ok {
		r.mu.RUnlock()
		return MatchRecord{}, false
	}
	result := cloneMatchRecord(record)
	r.mu.RUnlock()
	return result, true
}

// MatchHistoryLimit reports the effective in-memory retention bound.
func (r *ObservationRegistry) MatchHistoryLimit() int {
	if r == nil {
		return 0
	}
	r.mu.RLock()
	limit := r.matchLimit
	r.mu.RUnlock()
	if limit <= 0 {
		return DefaultMatchHistoryLimit
	}
	return limit
}

func (r *ObservationRegistry) Len() int {
	if r == nil {
		return 0
	}
	r.mu.RLock()
	value := len(r.tickets)
	r.mu.RUnlock()
	return value
}

func (entry *ticketObservation) view() TicketView {
	if entry == nil || entry.ticket == nil {
		return TicketView{}
	}
	return TicketView{
		Rule:        entry.key.owner.LogicalNode.Rule,
		TicketID:    entry.ticket.TicketID,
		CreatedAt:   entry.ticket.CreatedAt,
		StringLists: cloneStringLists(entry.ticket.StringLists),
		Uint64Lists: cloneUint64Lists(entry.ticket.Uint64Lists),
		Int64Values: cloneInt64Values(entry.ticket.Int64Values),
		ObjectFacts: factSnapshot(entry.objectFacts),
		Owner:       entry.key.owner,
		Decision:    cloneRouteDecision(entry.decision),
		Status:      entry.status,
	}
}

func ticketMatches(entry *ticketObservation, query TicketQuery) bool {
	if entry == nil {
		return false
	}
	if query.PhysicalNodeID != "" && entry.key.owner.PhysicalNodeID != query.PhysicalNodeID {
		return false
	}
	if query.Rule != nil && entry.key.owner.LogicalNode.Rule != *query.Rule {
		return false
	}
	if query.Status != "" && entry.status != query.Status {
		return false
	}
	if search := strings.ToLower(strings.TrimSpace(query.Search)); search != "" && !ticketContains(entry, search) {
		return false
	}
	return true
}

func ticketContains(entry *ticketObservation, search string) bool {
	if entry.ticket == nil {
		return false
	}
	if strings.Contains(strconv.FormatUint(uint64(entry.ticket.TicketID), 10), search) ||
		strings.Contains(strings.ToLower(entry.key.owner.String()), search) {
		return true
	}
	containsStrings := func(values map[string][]string) bool {
		for name, items := range values {
			if strings.Contains(strings.ToLower(name), search) {
				return true
			}
			for _, item := range items {
				if strings.Contains(strings.ToLower(item), search) {
					return true
				}
			}
		}
		return false
	}
	containsUint64s := func(values map[string][]uint64) bool {
		for name, items := range values {
			if strings.Contains(strings.ToLower(name), search) {
				return true
			}
			for _, item := range items {
				if strings.Contains(strconv.FormatUint(item, 10), search) {
					return true
				}
			}
		}
		return false
	}
	containsInt64s := func(values map[string]int64) bool {
		for name, item := range values {
			if strings.Contains(strings.ToLower(name), search) ||
				strings.Contains(strconv.FormatInt(item, 10), search) {
				return true
			}
		}
		return false
	}
	return containsStrings(entry.ticket.StringLists) ||
		containsUint64s(entry.ticket.Uint64Lists) ||
		containsInt64s(entry.ticket.Int64Values) ||
		containsStrings(entry.objectFacts.StringLists) ||
		containsUint64s(entry.objectFacts.Uint64Lists) ||
		containsInt64s(entry.objectFacts.Int64Values)
}

func pageBounds(cursor string, requested int) (int, int, error) {
	start := 0
	if cursor != "" {
		value, err := strconv.Atoi(cursor)
		if err != nil || value < 0 {
			return 0, 0, ErrInvalidCursor
		}
		start = value
	}
	limit := requested
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	return start, limit, nil
}

func nextCursor(start, limit, total int) string {
	if start >= total || limit <= 0 || limit >= total-start {
		return ""
	}
	return strconv.Itoa(start + limit)
}

func pageItemCapacity(start, total, limit int) int {
	if start >= total || limit <= 0 {
		return 0
	}
	remaining := total - start
	if limit < remaining {
		return limit
	}
	return remaining
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}

func cloneFacts(values matchsystem.Facts) matchsystem.Facts {
	return matchsystem.Facts{
		StringLists: cloneStringLists(values.StringLists),
		Uint64Lists: cloneUint64Lists(values.Uint64Lists),
		Int64Values: cloneInt64Values(values.Int64Values),
	}
}

func cloneRouteDecision(decision common.RouteDecision) common.RouteDecision {
	return decision
}

func factSnapshotFromMatch(match *common.Match) FactSnapshot {
	if match == nil {
		return FactSnapshot{}
	}
	return FactSnapshot{
		StringLists: cloneStringLists(match.Facts.StringLists),
		Uint64Lists: cloneUint64Lists(match.Facts.Uint64Lists),
		Int64Values: cloneInt64Values(match.Facts.Int64Values),
	}
}

func factSnapshotFromMatchFacts(values common.MatchFacts) FactSnapshot {
	return FactSnapshot{
		StringLists: cloneStringLists(values.StringLists),
		Uint64Lists: cloneUint64Lists(values.Uint64Lists),
		Int64Values: cloneInt64Values(values.Int64Values),
	}
}

func cloneMatchRecord(record MatchRecord) MatchRecord {
	out := record
	out.Tickets = make([]TicketView, len(record.Tickets))
	for index, ticket := range record.Tickets {
		out.Tickets[index] = ticket
		out.Tickets[index].StringLists = cloneStringLists(ticket.StringLists)
		out.Tickets[index].Uint64Lists = cloneUint64Lists(ticket.Uint64Lists)
		out.Tickets[index].Int64Values = cloneInt64Values(ticket.Int64Values)
		out.Tickets[index].ObjectFacts = ticket.ObjectFacts.clone()
	}
	out.Facts = record.Facts.clone()
	return out
}
