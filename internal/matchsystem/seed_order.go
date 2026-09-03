package matchsystem

import (
	"container/heap"
	"container/list"
	"fmt"
	"math/rand"
)

// SeedOrderPolicyKind identifies a built-in policy used to stream bounded,
// non-repeating seeds during a matching round.
type SeedOrderPolicyKind string

const (
	SeedOrderArrival       SeedOrderPolicyKind = "arrival"
	SeedOrderOldest        SeedOrderPolicyKind = "oldest"
	SeedOrderInt64Priority SeedOrderPolicyKind = "int64_priority"
	SeedOrderRandom        SeedOrderPolicyKind = "random"
)

type SeedPriorityDirection string

const (
	SeedPriorityDescending SeedPriorityDirection = "descending"
	SeedPriorityAscending  SeedPriorityDirection = "ascending"
)

// SeedOrderPolicyConfig is the normalized internal configuration produced from
// match-rule/v1 seedSelection. The zero value selects arrival order for direct
// factory tests; LogicalNode production loading always supplies an explicit type.
type SeedOrderPolicyConfig struct {
	Kind              SeedOrderPolicyKind
	PriorityField     string
	PriorityDirection SeedPriorityDirection
	RandomSeed        int64
}

// seedSchedulerConfig controls how one LogicalNode consumes seeds after the
// values have been validated by match-rule/v1 compilation.
type seedSchedulerConfig struct {
	// AttemptLimitPerProduceMatch limits the number of valid seeds consumed by
	// one ProduceMatch call. match-rule/v1 requires a positive value.
	AttemptLimitPerProduceMatch int
	// AttemptLimitPerMatchRound limits the total number of valid seeds consumed
	// by this LogicalNode during one matching round. The count accumulates over
	// multiple ProduceMatch calls and resets at BeginMatchRound. Stale/deleted
	// entries skipped by the runtime do not consume this budget.
	AttemptLimitPerMatchRound int
}

// SeedOrderRuntime is the lifecycle-owned interface between LogicalNode and a
// seed policy. Policies keep their own indexes and expose only a stream of
// TicketIDs; the ticketStore remains the sole owner of DocIDs and bitmap
// identities.
//
// Add receives the store-owned Ticket synchronously. Implementations must copy
// every field they need and must not retain the pointer or any of its maps.
// Remove is idempotent so Commit can forward every removed TicketID safely.
// BeginRound starts a new bounded, non-repeating stream. The limit is the
// maximum number of IDs the stream may yield in that round. Entries moved out
// of the active index are held until the next BeginRound, where still-active
// entries become eligible again. Callers must not Add between BeginRound and
// the completion of that round; no pending Add buffer is provided.
type SeedOrderRuntime interface {
	Add(*Ticket)
	Remove(TicketID)
	BeginRound(limit int)
	HasNext() bool
	Next() (TicketID, bool)
}

// SeedOrderPolicy is retained as the public name for the policy product while
// its runtime contract is lifecycle-oriented. It intentionally aliases the
// minimal seed runtime interface rather than exposing DocIDs or full-pool
// round candidates.
type SeedOrderPolicy = SeedOrderRuntime

// NewSeedOrderPolicy compiles built-in seed ordering configuration into a
// runtime policy. Runtime policies are owned by one LogicalNode and need not
// be goroutine-safe.
func NewSeedOrderPolicy(config SeedOrderPolicyConfig) (SeedOrderRuntime, error) {
	kind := config.Kind
	if kind == "" {
		kind = SeedOrderArrival
	}
	switch kind {
	case SeedOrderArrival:
		return &arrivalSeedOrderPolicy{
			entries: list.New(),
			active:  make(map[TicketID]*list.Element),
		}, nil
	case SeedOrderOldest:
		return &oldestSeedOrderPolicy{
			active: make(map[TicketID]*oldestSeedEntry),
		}, nil
	case SeedOrderInt64Priority:
		if config.PriorityField == "" {
			return nil, fmt.Errorf("PriorityField is required for %q seed order", kind)
		}
		direction := config.PriorityDirection
		if direction == "" {
			direction = SeedPriorityDescending
		}
		if direction != SeedPriorityDescending && direction != SeedPriorityAscending {
			return nil, fmt.Errorf("unsupported seed priority direction %q", direction)
		}
		return &int64PrioritySeedOrderPolicy{
			field:     config.PriorityField,
			direction: direction,
			active:    make(map[TicketID]*prioritySeedEntry),
			entries:   prioritySeedHeap{ascending: direction == SeedPriorityAscending},
		}, nil
	case SeedOrderRandom:
		return &randomSeedOrderPolicy{
			random:    rand.New(rand.NewSource(config.RandomSeed)),
			positions: make(map[TicketID]int),
		}, nil
	default:
		return nil, fmt.Errorf("unsupported seed order policy %q", kind)
	}
}

// arrivalSeedOrderPolicy keeps active entries in an intrusive ordered list and
// maps each TicketID to its list element. Remove and replacement Add unlink the
// exact entry in O(1). The round cursor is private to this policy; a failed
// seed is already past the cursor and therefore cannot repeat until the next
// BeginRound.
type arrivalSeedOrderPolicy struct {
	entries *list.List
	active  map[TicketID]*list.Element

	roundCursor  *list.Element
	roundLimit   int
	roundYielded int
	roundActive  bool
}

type arrivalSeedEntry struct {
	ticketID TicketID
}

func (p *arrivalSeedOrderPolicy) Add(ticket *Ticket) {
	if p == nil || ticket == nil {
		return
	}
	if p.entries == nil {
		p.entries = list.New()
	}
	if p.active == nil {
		p.active = make(map[TicketID]*list.Element)
	}
	if previous, exists := p.active[ticket.TicketID]; exists {
		if p.roundCursor == previous {
			p.roundCursor = previous.Next()
		}
		p.entries.Remove(previous)
	}
	p.active[ticket.TicketID] = p.entries.PushBack(arrivalSeedEntry{ticketID: ticket.TicketID})
}

func (p *arrivalSeedOrderPolicy) Remove(ticketID TicketID) {
	if p == nil || p.active == nil {
		return
	}
	element, exists := p.active[ticketID]
	if !exists {
		return
	}
	if p.roundCursor == element {
		p.roundCursor = element.Next()
	}
	if p.entries != nil {
		p.entries.Remove(element)
	}
	delete(p.active, ticketID)
}

func (p *arrivalSeedOrderPolicy) BeginRound(limit int) {
	if p == nil {
		return
	}
	if limit < 0 {
		limit = 0
	}
	p.roundLimit = limit
	p.roundYielded = 0
	p.roundActive = true
	if p.entries == nil {
		p.roundCursor = nil
		return
	}
	p.roundCursor = p.entries.Front()
}

func (p *arrivalSeedOrderPolicy) HasNext() bool {
	return p != nil && p.roundActive && p.roundYielded < p.roundLimit && p.roundCursor != nil
}

func (p *arrivalSeedOrderPolicy) Next() (TicketID, bool) {
	if p == nil || !p.roundActive || p.roundYielded >= p.roundLimit {
		return 0, false
	}
	for p.roundCursor != nil && p.roundYielded < p.roundLimit {
		element := p.roundCursor
		p.roundCursor = element.Next()
		entry, ok := element.Value.(arrivalSeedEntry)
		if !ok || p.active[entry.ticketID] != element {
			continue
		}
		p.roundYielded++
		return entry.ticketID, true
	}
	return 0, false
}

// oldestSeedOrderPolicy maintains a heap of copied ordering fields. Every entry
// carries its heap index so Remove can delete it immediately. Entries yielded
// in the current round move to held; the next BeginRound restores still-active
// entries. The active map distinguishes an old entry from a re-added TicketID
// with the same public ID.
type oldestSeedOrderPolicy struct {
	entries      oldestSeedHeap
	active       map[TicketID]*oldestSeedEntry
	sequence     uint64
	held         []*oldestSeedEntry
	roundLimit   int
	roundYielded int
	roundActive  bool
}

type oldestSeedEntry struct {
	ticketID  TicketID
	created   int64
	sequence  uint64
	active    bool
	heapIndex int
	heldIndex int
}

func (p *oldestSeedOrderPolicy) Add(ticket *Ticket) {
	if p == nil || ticket == nil {
		return
	}
	if p.active == nil {
		p.active = make(map[TicketID]*oldestSeedEntry)
	}
	if previous, exists := p.active[ticket.TicketID]; exists {
		p.removeEntry(previous)
	}
	p.sequence++
	entry := &oldestSeedEntry{
		ticketID:  ticket.TicketID,
		created:   ticket.CreatedAt,
		sequence:  p.sequence,
		active:    true,
		heapIndex: -1,
		heldIndex: -1,
	}
	p.active[ticket.TicketID] = entry
	heap.Push(&p.entries, entry)
}

func (p *oldestSeedOrderPolicy) Remove(ticketID TicketID) {
	if p == nil || p.active == nil {
		return
	}
	entry, exists := p.active[ticketID]
	if !exists {
		return
	}
	delete(p.active, ticketID)
	p.removeEntry(entry)
}

func (p *oldestSeedOrderPolicy) removeEntry(entry *oldestSeedEntry) {
	if entry == nil {
		return
	}
	entry.active = false
	if entry.heapIndex >= 0 && entry.heapIndex < p.entries.Len() && p.entries[entry.heapIndex] == entry {
		heap.Remove(&p.entries, entry.heapIndex)
	}
	if entry.heldIndex >= 0 && entry.heldIndex < len(p.held) && p.held[entry.heldIndex] == entry {
		last := len(p.held) - 1
		if entry.heldIndex != last {
			moved := p.held[last]
			p.held[entry.heldIndex] = moved
			moved.heldIndex = entry.heldIndex
		}
		p.held[last] = nil
		p.held = p.held[:last]
	}
	entry.heldIndex = -1
}

func (p *oldestSeedOrderPolicy) BeginRound(limit int) {
	if p == nil {
		return
	}
	// Entries yielded by the previous round are not removed from the active
	// map. Restore only those that are still live; Commit/Remove marks deleted
	// entries inactive and removes them from held immediately.
	for _, entry := range p.held {
		if entry == nil {
			continue
		}
		entry.heldIndex = -1
		if entry.active && p.active[entry.ticketID] == entry {
			heap.Push(&p.entries, entry)
		} else {
			entry.active = false
		}
	}
	p.held = p.held[:0]
	if limit < 0 {
		limit = 0
	}
	p.roundLimit = limit
	p.roundYielded = 0
	p.roundActive = true
}

func (p *oldestSeedOrderPolicy) HasNext() bool {
	return p != nil && p.roundActive && p.roundYielded < p.roundLimit && p.entries.Len() > 0
}

func (p *oldestSeedOrderPolicy) Next() (TicketID, bool) {
	if p == nil || !p.roundActive || p.roundYielded >= p.roundLimit {
		return 0, false
	}
	for p.entries.Len() > 0 {
		entry := heap.Pop(&p.entries).(*oldestSeedEntry)
		if !entry.active || p.active[entry.ticketID] != entry {
			continue
		}
		entry.heldIndex = len(p.held)
		p.held = append(p.held, entry)
		p.roundYielded++
		return entry.ticketID, true
	}
	return 0, false
}

type oldestSeedHeap []*oldestSeedEntry

func (h oldestSeedHeap) Len() int { return len(h) }

func (h oldestSeedHeap) Less(i, j int) bool {
	if h[i].created != h[j].created {
		return h[i].created < h[j].created
	}
	return h[i].sequence < h[j].sequence
}

func (h oldestSeedHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].heapIndex = i
	h[j].heapIndex = j
}

func (h *oldestSeedHeap) Push(value any) {
	entry := value.(*oldestSeedEntry)
	entry.heapIndex = len(*h)
	*h = append(*h, entry)
}

func (h *oldestSeedHeap) Pop() any {
	old := *h
	n := len(old)
	value := old[n-1]
	old[n-1] = nil
	*h = old[:n-1]
	value.heapIndex = -1
	return value
}

// int64PrioritySeedOrderPolicy keeps only the configured scalar and its
// presence bit. This avoids retaining Ticket maps while preserving the old
// semantics that present values sort before missing values. As with oldest,
// yielded entries are held until the next round and then restored if active.
type int64PrioritySeedOrderPolicy struct {
	field        string
	direction    SeedPriorityDirection
	entries      prioritySeedHeap
	active       map[TicketID]*prioritySeedEntry
	sequence     uint64
	held         []*prioritySeedEntry
	roundLimit   int
	roundYielded int
	roundActive  bool
}

type prioritySeedEntry struct {
	ticketID  TicketID
	value     int64
	present   bool
	sequence  uint64
	active    bool
	heapIndex int
	heldIndex int
}

func (p *int64PrioritySeedOrderPolicy) Add(ticket *Ticket) {
	if p == nil || ticket == nil {
		return
	}
	if p.active == nil {
		p.active = make(map[TicketID]*prioritySeedEntry)
	}
	if previous, exists := p.active[ticket.TicketID]; exists {
		p.removeEntry(previous)
	}
	value, present := ticket.Int64Values[p.field]
	p.sequence++
	entry := &prioritySeedEntry{
		ticketID:  ticket.TicketID,
		value:     value,
		present:   present,
		sequence:  p.sequence,
		active:    true,
		heapIndex: -1,
		heldIndex: -1,
	}
	p.active[ticket.TicketID] = entry
	heap.Push(&p.entries, entry)
}

func (p *int64PrioritySeedOrderPolicy) Remove(ticketID TicketID) {
	if p == nil || p.active == nil {
		return
	}
	entry, exists := p.active[ticketID]
	if !exists {
		return
	}
	delete(p.active, ticketID)
	p.removeEntry(entry)
}

func (p *int64PrioritySeedOrderPolicy) removeEntry(entry *prioritySeedEntry) {
	if entry == nil {
		return
	}
	entry.active = false
	if entry.heapIndex >= 0 && entry.heapIndex < p.entries.Len() && p.entries.entries[entry.heapIndex] == entry {
		heap.Remove(&p.entries, entry.heapIndex)
	}
	if entry.heldIndex >= 0 && entry.heldIndex < len(p.held) && p.held[entry.heldIndex] == entry {
		last := len(p.held) - 1
		if entry.heldIndex != last {
			moved := p.held[last]
			p.held[entry.heldIndex] = moved
			moved.heldIndex = entry.heldIndex
		}
		p.held[last] = nil
		p.held = p.held[:last]
	}
	entry.heldIndex = -1
}

func (p *int64PrioritySeedOrderPolicy) BeginRound(limit int) {
	if p == nil {
		return
	}
	for _, entry := range p.held {
		if entry == nil {
			continue
		}
		entry.heldIndex = -1
		if entry.active && p.active[entry.ticketID] == entry {
			heap.Push(&p.entries, entry)
		} else {
			entry.active = false
		}
	}
	p.held = p.held[:0]
	if limit < 0 {
		limit = 0
	}
	p.roundLimit = limit
	p.roundYielded = 0
	p.roundActive = true
}

func (p *int64PrioritySeedOrderPolicy) HasNext() bool {
	return p != nil && p.roundActive && p.roundYielded < p.roundLimit && p.entries.Len() > 0
}

func (p *int64PrioritySeedOrderPolicy) Next() (TicketID, bool) {
	if p == nil || !p.roundActive || p.roundYielded >= p.roundLimit {
		return 0, false
	}
	for p.entries.Len() > 0 {
		entry := heap.Pop(&p.entries).(*prioritySeedEntry)
		if !entry.active || p.active[entry.ticketID] != entry {
			continue
		}
		entry.heldIndex = len(p.held)
		p.held = append(p.held, entry)
		p.roundYielded++
		return entry.ticketID, true
	}
	return 0, false
}

type prioritySeedHeap struct {
	entries   []*prioritySeedEntry
	ascending bool
}

func (h prioritySeedHeap) Len() int { return len(h.entries) }

func (h prioritySeedHeap) Less(i, j int) bool {
	left, right := h.entries[i], h.entries[j]
	if left.present != right.present {
		return left.present
	}
	if left.value != right.value {
		if h.ascending {
			return left.value < right.value
		}
		return left.value > right.value
	}
	return left.sequence < right.sequence
}

func (h prioritySeedHeap) Swap(i, j int) {
	h.entries[i], h.entries[j] = h.entries[j], h.entries[i]
	h.entries[i].heapIndex = i
	h.entries[j].heapIndex = j
}

func (h *prioritySeedHeap) Push(value any) {
	entry := value.(*prioritySeedEntry)
	entry.heapIndex = len(h.entries)
	h.entries = append(h.entries, entry)
}

func (h *prioritySeedHeap) Pop() any {
	old := h.entries
	n := len(old)
	value := old[n-1]
	old[n-1] = nil
	h.entries = old[:n-1]
	value.heapIndex = -1
	return value
}

// randomSeedOrderPolicy keeps a dense active TicketID array. Next chooses one
// active ID uniformly and moves it to held, so a round never repeats a seed.
// BeginRound appends still-active held IDs back into the dense array; Remove
// handles both locations with O(1) swap-remove.
type randomSeedOrderPolicy struct {
	random        *rand.Rand
	ticketIDs     []TicketID
	positions     map[TicketID]int
	held          []TicketID
	heldPositions map[TicketID]int
	roundLimit    int
	roundYielded  int
	roundActive   bool
}

func (p *randomSeedOrderPolicy) Add(ticket *Ticket) {
	if p == nil || ticket == nil {
		return
	}
	if p.positions == nil {
		p.positions = make(map[TicketID]int)
	}
	if p.heldPositions == nil {
		p.heldPositions = make(map[TicketID]int)
	}
	if _, exists := p.positions[ticket.TicketID]; exists {
		p.Remove(ticket.TicketID)
	}
	if _, exists := p.heldPositions[ticket.TicketID]; exists {
		p.Remove(ticket.TicketID)
	}
	p.positions[ticket.TicketID] = len(p.ticketIDs)
	p.ticketIDs = append(p.ticketIDs, ticket.TicketID)
}

func (p *randomSeedOrderPolicy) Remove(ticketID TicketID) {
	if p == nil || p.positions == nil {
		return
	}
	if index, exists := p.positions[ticketID]; exists {
		last := len(p.ticketIDs) - 1
		if index != last {
			lastID := p.ticketIDs[last]
			p.ticketIDs[index] = lastID
			p.positions[lastID] = index
		}
		p.ticketIDs[last] = 0
		p.ticketIDs = p.ticketIDs[:last]
		delete(p.positions, ticketID)
		return
	}
	if index, exists := p.heldPositions[ticketID]; exists {
		last := len(p.held) - 1
		if index != last {
			lastID := p.held[last]
			p.held[index] = lastID
			p.heldPositions[lastID] = index
		}
		p.held[last] = 0
		p.held = p.held[:last]
		delete(p.heldPositions, ticketID)
	}
}

func (p *randomSeedOrderPolicy) BeginRound(limit int) {
	if p == nil {
		return
	}
	if p.heldPositions == nil {
		p.heldPositions = make(map[TicketID]int)
	}
	for _, ticketID := range p.held {
		p.positions[ticketID] = len(p.ticketIDs)
		p.ticketIDs = append(p.ticketIDs, ticketID)
	}
	clear(p.heldPositions)
	p.held = p.held[:0]
	if limit < 0 {
		limit = 0
	}
	p.roundLimit = limit
	p.roundYielded = 0
	p.roundActive = true
}

func (p *randomSeedOrderPolicy) HasNext() bool {
	return p != nil && p.roundActive && p.roundYielded < p.roundLimit && len(p.ticketIDs) > 0
}

func (p *randomSeedOrderPolicy) Next() (TicketID, bool) {
	if p == nil || !p.roundActive || p.roundYielded >= p.roundLimit || len(p.ticketIDs) == 0 {
		return 0, false
	}
	if p.random == nil {
		p.random = rand.New(rand.NewSource(0))
	}
	index := p.random.Intn(len(p.ticketIDs))
	ticketID := p.ticketIDs[index]
	last := len(p.ticketIDs) - 1
	if index != last {
		lastID := p.ticketIDs[last]
		p.ticketIDs[index] = lastID
		p.positions[lastID] = index
	}
	p.ticketIDs[last] = 0
	p.ticketIDs = p.ticketIDs[:last]
	delete(p.positions, ticketID)
	p.heldPositions[ticketID] = len(p.held)
	p.held = append(p.held, ticketID)
	p.roundYielded++
	return ticketID, true
}
