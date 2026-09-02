package matchsystem

import (
	"container/heap"
	"container/list"
	"fmt"
	"math/rand"
)

// SeedOrderPolicyKind identifies a built-in policy used to build the bounded
// seed order at the beginning of a matching round.
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
	// entries in the round snapshot do not consume this budget.
	AttemptLimitPerMatchRound int
}

// SeedOrderRuntime is the lifecycle-owned interface between LogicalNode and a
// seed policy. Policies keep their own indexes and only expose TicketIDs; the
// ticketStore remains the sole owner of DocIDs and bitmap identities.
//
// Add receives the store-owned Ticket synchronously. Implementations must copy
// every field they need and must not retain the pointer or any of its maps.
// Remove is idempotent so Commit can forward every removed TicketID safely.
type SeedOrderRuntime interface {
	Add(*Ticket)
	Remove(TicketID)
	BuildRound(limit int) ([]TicketID, error)
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
// exact entry in O(1); BuildRound walks only live entries and stops at limit.
type arrivalSeedOrderPolicy struct {
	entries *list.List
	active  map[TicketID]*list.Element
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
	if p.entries != nil {
		p.entries.Remove(element)
	}
	delete(p.active, ticketID)
}

func (p *arrivalSeedOrderPolicy) BuildRound(limit int) ([]TicketID, error) {
	if p == nil || limit <= 0 || len(p.active) == 0 || p.entries == nil {
		return nil, nil
	}
	order := make([]TicketID, 0, minSeedLimit(limit, len(p.active)))
	for element := p.entries.Front(); element != nil && len(order) < limit; element = element.Next() {
		entry, ok := element.Value.(arrivalSeedEntry)
		if !ok {
			continue
		}
		order = append(order, entry.ticketID)
	}
	return order, nil
}

// oldestSeedOrderPolicy maintains a heap of copied ordering fields. Every entry
// carries its heap index so Remove can delete it immediately; the active map
// distinguishes an old entry from a re-added TicketID with the same public ID.
type oldestSeedOrderPolicy struct {
	entries  oldestSeedHeap
	active   map[TicketID]*oldestSeedEntry
	sequence uint64
	reinsert []*oldestSeedEntry
}

type oldestSeedEntry struct {
	ticketID  TicketID
	created   int64
	sequence  uint64
	active    bool
	heapIndex int
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
}

func (p *oldestSeedOrderPolicy) BuildRound(limit int) ([]TicketID, error) {
	if p == nil || limit <= 0 || len(p.active) == 0 {
		return nil, nil
	}
	order := make([]TicketID, 0, minSeedLimit(limit, len(p.active)))
	if cap(p.reinsert) < cap(order) {
		p.reinsert = make([]*oldestSeedEntry, 0, cap(order))
	} else {
		p.reinsert = p.reinsert[:0]
	}
	for len(order) < limit && p.entries.Len() > 0 {
		entry := heap.Pop(&p.entries).(*oldestSeedEntry)
		if !entry.active || p.active[entry.ticketID] != entry {
			continue
		}
		order = append(order, entry.ticketID)
		p.reinsert = append(p.reinsert, entry)
	}
	for _, entry := range p.reinsert {
		heap.Push(&p.entries, entry)
	}
	for index := range p.reinsert {
		p.reinsert[index] = nil
	}
	p.reinsert = p.reinsert[:0]
	return order, nil
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
// semantics that present values sort before missing values.
type int64PrioritySeedOrderPolicy struct {
	field     string
	direction SeedPriorityDirection
	entries   prioritySeedHeap
	active    map[TicketID]*prioritySeedEntry
	sequence  uint64
	reinsert  []*prioritySeedEntry
}

type prioritySeedEntry struct {
	ticketID  TicketID
	value     int64
	present   bool
	sequence  uint64
	active    bool
	heapIndex int
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
}

func (p *int64PrioritySeedOrderPolicy) BuildRound(limit int) ([]TicketID, error) {
	if p == nil || limit <= 0 || len(p.active) == 0 {
		return nil, nil
	}
	order := make([]TicketID, 0, minSeedLimit(limit, len(p.active)))
	if cap(p.reinsert) < cap(order) {
		p.reinsert = make([]*prioritySeedEntry, 0, cap(order))
	} else {
		p.reinsert = p.reinsert[:0]
	}
	for len(order) < limit && p.entries.Len() > 0 {
		entry := heap.Pop(&p.entries).(*prioritySeedEntry)
		if !entry.active || p.active[entry.ticketID] != entry {
			continue
		}
		order = append(order, entry.ticketID)
		p.reinsert = append(p.reinsert, entry)
	}
	for _, entry := range p.reinsert {
		heap.Push(&p.entries, entry)
	}
	for index := range p.reinsert {
		p.reinsert[index] = nil
	}
	p.reinsert = p.reinsert[:0]
	return order, nil
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

// randomSeedOrderPolicy keeps a dense TicketID array. Remove uses swap-remove,
// and BuildRound performs a partial Fisher-Yates shuffle in place, copying only
// the requested prefix and restoring the swaps before returning. Thus random
// selection is O(limit) and never materializes a full shuffled pool.
type randomSeedOrderPolicy struct {
	random    *rand.Rand
	ticketIDs []TicketID
	positions map[TicketID]int
	swaps     []int
}

func (p *randomSeedOrderPolicy) Add(ticket *Ticket) {
	if p == nil || ticket == nil {
		return
	}
	if p.positions == nil {
		p.positions = make(map[TicketID]int)
	}
	if _, exists := p.positions[ticket.TicketID]; exists {
		p.Remove(ticket.TicketID)
	}
	p.positions[ticket.TicketID] = len(p.ticketIDs)
	p.ticketIDs = append(p.ticketIDs, ticket.TicketID)
}

func (p *randomSeedOrderPolicy) Remove(ticketID TicketID) {
	if p == nil || p.positions == nil {
		return
	}
	index, exists := p.positions[ticketID]
	if !exists {
		return
	}
	last := len(p.ticketIDs) - 1
	if index != last {
		lastID := p.ticketIDs[last]
		p.ticketIDs[index] = lastID
		p.positions[lastID] = index
	}
	p.ticketIDs[last] = 0
	p.ticketIDs = p.ticketIDs[:last]
	delete(p.positions, ticketID)
}

func (p *randomSeedOrderPolicy) BuildRound(limit int) ([]TicketID, error) {
	if p == nil || limit <= 0 || len(p.ticketIDs) == 0 {
		return nil, nil
	}
	if limit > len(p.ticketIDs) {
		limit = len(p.ticketIDs)
	}
	if p.random == nil {
		p.random = rand.New(rand.NewSource(0))
	}
	order := make([]TicketID, limit)
	if cap(p.swaps) < limit {
		p.swaps = make([]int, limit)
	} else {
		p.swaps = p.swaps[:limit]
	}
	for index := 0; index < limit; index++ {
		swapIndex := index + p.random.Intn(len(p.ticketIDs)-index)
		p.swaps[index] = swapIndex
		p.swap(index, swapIndex)
		order[index] = p.ticketIDs[index]
	}
	for index := limit - 1; index >= 0; index-- {
		p.swap(index, p.swaps[index])
	}
	return order, nil
}

func (p *randomSeedOrderPolicy) swap(left, right int) {
	if left == right {
		return
	}
	leftID, rightID := p.ticketIDs[left], p.ticketIDs[right]
	p.ticketIDs[left], p.ticketIDs[right] = rightID, leftID
	p.positions[leftID] = right
	p.positions[rightID] = left
}

func minSeedLimit(limit, available int) int {
	if limit < available {
		return limit
	}
	return available
}
