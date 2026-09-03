package matchsystem

import (
	"container/heap"
	"fmt"

	"matchSystem/internal/common"
	"matchSystem/internal/matchsystem/fact"
	"matchSystem/internal/matchsystem/prefilter"
)

// storedTicket keeps LogicalNode-local index metadata separate from the
// shared Ticket model. The Ticket pointer is owned by the pool until Remove
// or a committed Match transfers it to the caller.
type storedTicket struct {
	*Ticket
	docID uint32
	// Object Facts are optional. Rules without Object-scoped Facts keep this
	// pointer nil, avoiding a slot struct and its buffers for every Ticket.
	objectFacts *fact.ObjectSlot
}

// ticketStore owns the mutable Ticket lifetime for one LogicalNode. It keeps
// the TicketID/DocID mappings, Prefilter index, and oldest-waiting metric in
// one consistency boundary. Seed ordering indexes are owned by SeedOrderRuntime;
// the owning PhysicalNode serializes all access.
type ticketStore struct {
	prefilterStore *prefilter.IndexStore
	objectLayout   *fact.ObjectLayout
	nextDocID      uint32

	ticketsByDocID  map[uint32]*storedTicket
	ticketIDToDocID map[TicketID]uint32
	freeDocIDs      []uint32

	oldestTickets oldestTicketHeap
}

func newTicketStore(prefilterStore *prefilter.IndexStore, layouts ...*fact.ObjectLayout) *ticketStore {
	var objectLayout *fact.ObjectLayout
	if len(layouts) > 0 {
		objectLayout = layouts[0]
	}
	return &ticketStore{
		prefilterStore:  prefilterStore,
		objectLayout:    objectLayout,
		nextDocID:       1,
		ticketsByDocID:  make(map[uint32]*storedTicket),
		ticketIDToDocID: make(map[TicketID]uint32),
	}
}

// Add deep-copies ticket exactly once into the LogicalNode-owned pool. The
// Prefilter receives that owned snapshot and maintains its own lookup snapshot
// for expression evaluation.
func (s *ticketStore) Add(ticket *Ticket) (uint32, error) {
	if s == nil {
		return 0, fmt.Errorf("ticket store is nil")
	}
	if ticket == nil {
		return 0, fmt.Errorf("ticket is nil")
	}
	if ticket.TicketID == 0 {
		return 0, fmt.Errorf("TicketID is required")
	}
	if _, exists := s.ticketIDToDocID[ticket.TicketID]; exists {
		return 0, fmt.Errorf("TicketID %d already exists", ticket.TicketID)
	}
	docID, err := s.allocateDocID()
	if err != nil {
		return 0, err
	}
	owned := common.CloneTicket(ticket)
	stored := &storedTicket{Ticket: owned, docID: docID}
	if s.objectLayout.HasFields() {
		stored.objectFacts = new(fact.ObjectSlot)
		stored.objectFacts.Init(s.objectLayout)
	}
	if s.prefilterStore == nil {
		s.releaseFailedDocID(docID)
		return 0, fmt.Errorf("prefilter store is nil")
	}
	if err := s.prefilterStore.Add(docID, owned); err != nil {
		s.releaseFailedDocID(docID)
		return 0, err
	}
	s.ticketsByDocID[docID] = stored
	s.ticketIDToDocID[stored.TicketID] = docID
	heap.Push(&s.oldestTickets, stored)
	return docID, nil
}

func (s *ticketStore) Remove(ticketID TicketID) bool {
	if s == nil {
		return false
	}
	docID, ok := s.ticketIDToDocID[ticketID]
	if !ok {
		return false
	}
	s.removeDocID(docID)
	return true
}

// Get returns an owned deep copy. Mutating or retaining the returned Ticket
// cannot affect the store-owned snapshot.
func (s *ticketStore) Get(ticketID TicketID) (*Ticket, bool) {
	ticket, ok := s.lookupTicket(ticketID)
	if !ok {
		return nil, false
	}
	return common.CloneTicket(ticket), true
}

// lookupTicket returns a borrowed Ticket pointer for the matching owner. The
// pointer remains valid only until the next owner mutation that consumes it.
func (s *ticketStore) lookupTicket(ticketID TicketID) (*Ticket, bool) {
	if s == nil {
		return nil, false
	}
	docID, ok := s.ticketIDToDocID[ticketID]
	if !ok {
		return nil, false
	}
	stored := s.ticketsByDocID[docID]
	if stored == nil {
		return nil, false
	}
	return stored.Ticket, true
}

func (s *ticketStore) lookupDocID(docID uint32) (*storedTicket, bool) {
	if s == nil {
		return nil, false
	}
	stored, ok := s.ticketsByDocID[docID]
	return stored, ok && stored != nil
}

// lookupTicketID returns the live store entry, including its Object Fact slot,
// for the owner goroutine. The returned pointer is borrowed.
func (s *ticketStore) lookupTicketID(ticketID TicketID) (*storedTicket, bool) {
	if s == nil {
		return nil, false
	}
	docID, ok := s.ticketIDToDocID[ticketID]
	if !ok {
		return nil, false
	}
	return s.lookupDocID(docID)
}

func (s *ticketStore) Len() int {
	if s == nil {
		return 0
	}
	return len(s.ticketsByDocID)
}

func (s *ticketStore) beginPrefilterTick(tickFacts Facts) (*prefilter.TickSession, error) {
	if s == nil || s.prefilterStore == nil {
		return nil, fmt.Errorf("prefilter store is not initialized")
	}
	return s.prefilterStore.BeginTick(tickFacts)
}

// Commit atomically consumes every Ticket referenced by match. All references
// are validated before any mutation, including pointer ownership, so a stale
// or partially-constructed Match cannot remove only part of a group.
func (s *ticketStore) Commit(match *Match) error {
	if s == nil {
		return fmt.Errorf("ticket store is nil")
	}
	if match == nil {
		return fmt.Errorf("match is nil")
	}
	seen := make(map[TicketID]struct{}, len(match.Tickets))
	docIDs := make([]uint32, len(match.Tickets))
	for index, ticket := range match.Tickets {
		if ticket == nil {
			return fmt.Errorf("match ticket %d is nil", index)
		}
		if _, duplicate := seen[ticket.TicketID]; duplicate {
			return fmt.Errorf("match contains duplicate TicketID %d", ticket.TicketID)
		}
		seen[ticket.TicketID] = struct{}{}
		docID, ok := s.ticketIDToDocID[ticket.TicketID]
		if !ok {
			return fmt.Errorf("match TicketID %d is not active", ticket.TicketID)
		}
		stored := s.ticketsByDocID[docID]
		if stored == nil {
			return fmt.Errorf("match TicketID %d has no active DocID %d", ticket.TicketID, docID)
		}
		if stored.Ticket != ticket {
			return fmt.Errorf("match TicketID %d does not own the active Ticket", ticket.TicketID)
		}
		docIDs[index] = docID
	}
	for _, docID := range docIDs {
		s.removeDocID(docID)
	}
	return nil
}

func (s *ticketStore) removeDocID(docID uint32) {
	if s == nil {
		return
	}
	ticket := s.ticketsByDocID[docID]
	if ticket == nil {
		return
	}
	if s.prefilterStore == nil {
		panic("ticket store invariant violated: Prefilter store is nil")
	}
	if !s.prefilterStore.Remove(docID) {
		panic(fmt.Sprintf("ticket store invariant violated: Prefilter DocID %d is not active", docID))
	}
	if ticket.objectFacts != nil {
		ticket.objectFacts.Invalidate()
	}
	delete(s.ticketsByDocID, docID)
	delete(s.ticketIDToDocID, ticket.TicketID)
	s.recycleDocID(docID)
}

func (s *ticketStore) allocateDocID() (uint32, error) {
	if s == nil {
		return 0, fmt.Errorf("ticket store is nil")
	}
	if last := len(s.freeDocIDs) - 1; last >= 0 {
		docID := s.freeDocIDs[last]
		s.freeDocIDs = s.freeDocIDs[:last]
		return docID, nil
	}
	if s.nextDocID == 0 {
		return 0, fmt.Errorf("DocID space is exhausted")
	}
	docID := s.nextDocID
	s.nextDocID++
	return docID, nil
}

func (s *ticketStore) recycleDocID(docID uint32) {
	if s == nil || docID == 0 {
		return
	}
	s.freeDocIDs = append(s.freeDocIDs, docID)
}

// releaseFailedDocID returns an ID allocated by an Add that never reached the
// active membership maps. Such an ID cannot be present in the seed runtime and
// is therefore safe to reuse immediately, even during a round.
func (s *ticketStore) releaseFailedDocID(docID uint32) {
	if s != nil && docID != 0 {
		s.freeDocIDs = append(s.freeDocIDs, docID)
	}
}

// oldestCreatedAt reports the oldest live Ticket for PhysicalNode selectors.
// This heap is a generic waiting-time metric; seed order policies maintain
// their own independent ordering indexes.
func (s *ticketStore) oldestCreatedAt() (int64, bool) {
	if s == nil {
		return 0, false
	}
	for len(s.oldestTickets) > 0 {
		oldest := s.oldestTickets[0]
		if s.ticketsByDocID[oldest.docID] == oldest {
			return oldest.CreatedAt, true
		}
		heap.Pop(&s.oldestTickets)
	}
	return 0, false
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
