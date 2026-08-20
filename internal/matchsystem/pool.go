package matchsystem

import (
	"math/rand"
	"sort"
)

type SeedSchedulerConfig struct {
	SeedLimitPerTick int

	OldestRatio       float64
	MatchabilityRatio float64
	RecentRatio       float64
	RandomRatio       float64
}

type PoolConfig struct {
	SeedScheduler SeedSchedulerConfig
	GroupBuilder  GroupBuilderConfig
	MaxPlayers    int
}

type MatchPool struct {
	config  PoolConfig
	rules   matchRules
	builder GroupBuilder
	rand    *rand.Rand

	nextDocID uint32

	ticketsByDocID  map[uint32]*Ticket
	ticketIDToDocID map[string]uint32
	arrivalOrder    []uint32

	numericIndex   map[string]map[uint32]int64
	numericBuckets map[string]map[int64]TicketSet

	attributeIndex map[string]map[string]TicketSet
}

func NewMatchPool(config PoolConfig, rules *RuleSet) *MatchPool {
	if config.SeedScheduler.SeedLimitPerTick <= 0 {
		config.SeedScheduler.SeedLimitPerTick = 500
	}
	if config.MaxPlayers <= 0 {
		config.MaxPlayers = 8
	}
	if rules == nil {
		rules = NewRuleSet()
	}
	return &MatchPool{
		config:          config,
		rules:           rules,
		builder:         NewGroupBuilder(config.GroupBuilder),
		rand:            rand.New(rand.NewSource(1)),
		nextDocID:       1,
		ticketsByDocID:  make(map[uint32]*Ticket),
		ticketIDToDocID: make(map[string]uint32),
		numericIndex:    make(map[string]map[uint32]int64),
		numericBuckets:  make(map[string]map[int64]TicketSet),
		attributeIndex:  make(map[string]map[string]TicketSet),
	}
}

func (p *MatchPool) Add(ticket *Ticket) uint32 {
	if ticket.DocID == 0 {
		ticket.DocID = p.nextDocID
		p.nextDocID++
	}
	ticket.MaxPlayers = p.config.MaxPlayers
	if ticket.Attributes == nil {
		ticket.Attributes = map[string]string{}
	}
	if ticket.Numeric == nil {
		ticket.Numeric = map[string]int64{}
	}

	p.ticketsByDocID[ticket.DocID] = ticket
	p.ticketIDToDocID[ticket.TicketID] = ticket.DocID
	p.arrivalOrder = append(p.arrivalOrder, ticket.DocID)

	for field, value := range ticket.Numeric {
		if _, ok := p.numericIndex[field]; !ok {
			p.numericIndex[field] = make(map[uint32]int64)
		}
		p.numericIndex[field][ticket.DocID] = value
		buckets, ok := p.numericBuckets[field]
		if !ok {
			buckets = make(map[int64]TicketSet)
			p.numericBuckets[field] = buckets
		}
		set, ok := buckets[value]
		if !ok {
			set = NewTicketSet()
			buckets[value] = set
		}
		set.Add(ticket.DocID)
	}
	for field, value := range ticket.Attributes {
		values, ok := p.attributeIndex[field]
		if !ok {
			values = make(map[string]TicketSet)
			p.attributeIndex[field] = values
		}
		set, ok := values[value]
		if !ok {
			set = NewTicketSet()
			values[value] = set
		}
		set.Add(ticket.DocID)
	}
	return ticket.DocID
}

func (p *MatchPool) Remove(ticketID string) bool {
	docID, ok := p.ticketIDToDocID[ticketID]
	if !ok {
		return false
	}
	p.removeDocID(docID)
	return true
}

func (p *MatchPool) Len() int {
	return len(p.ticketsByDocID)
}

func (p *MatchPool) Tick(now int64) []Match {
	seeds := p.selectSeeds(now)
	used := NewTicketSet()
	matches := make([]Match, 0)

	for _, seed := range seeds {
		if seed == nil || used.Has(seed.DocID) {
			continue
		}

		candidateSet := p.rules.CandidateSet(p, seed, now)
		candidateSet.Remove(seed.DocID)
		for id := range used {
			candidateSet.Remove(id)
		}

		candidates := p.materialize(candidateSet)
		group := p.builder.Build(seed, candidates, p.rules, now)
		if p.rules.CanStartGroup(group, now) {
			matches = append(matches, Match{Tickets: group})
			markGroupUsed(used, group)
			continue
		}

		if p.rules.ShouldForceStart(seed, now) {
			group = []*Ticket{seed}
			matches = append(matches, Match{Tickets: group})
			markGroupUsed(used, group)
		}
	}

	for docID := range used {
		p.removeDocID(docID)
	}
	p.compactArrivalOrder()
	return matches
}

func (p *MatchPool) allSet() TicketSet {
	out := NewTicketSet()
	for id := range p.ticketsByDocID {
		out.Add(id)
	}
	return out
}

func (p *MatchPool) numericRange(field string, min, max int64) TicketSet {
	out := NewTicketSet()
	buckets := p.numericBuckets[field]
	values := p.numericIndex[field]
	if len(buckets) == 0 || len(values) == 0 || min > max {
		return out
	}
	for value := min; value <= max; value++ {
		for docID := range buckets[value] {
			if current, ok := values[docID]; ok && current == value {
				out.Add(docID)
			}
		}
	}
	return out
}

func (p *MatchPool) estimateNumericRange(field string, min, max int64) int {
	buckets := p.numericBuckets[field]
	if len(buckets) == 0 || min > max {
		return 0
	}
	count := 0
	for value := min; value <= max; value++ {
		count += len(buckets[value])
	}
	return count
}

func (p *MatchPool) compactArrivalOrder() {
	if len(p.arrivalOrder) <= len(p.ticketsByDocID)*2+1024 {
		return
	}
	compacted := make([]uint32, 0, len(p.ticketsByDocID))
	for _, docID := range p.arrivalOrder {
		if _, ok := p.ticketsByDocID[docID]; ok {
			compacted = append(compacted, docID)
		}
	}
	p.arrivalOrder = compacted
}

func (p *MatchPool) attributeEquals(field, value string) TicketSet {
	if p.attributeIndex[field] == nil || p.attributeIndex[field][value] == nil {
		return NewTicketSet()
	}
	return p.attributeIndex[field][value].Clone()
}

func (p *MatchPool) estimateAttributeEquals(field, value string) int {
	if p.attributeIndex[field] == nil || p.attributeIndex[field][value] == nil {
		return 0
	}
	return p.attributeIndex[field][value].Len()
}

func (p *MatchPool) materialize(set TicketSet) []*Ticket {
	tickets := make([]*Ticket, 0, len(set))
	for docID := range set {
		if ticket, ok := p.ticketsByDocID[docID]; ok {
			tickets = append(tickets, ticket)
		}
	}
	return tickets
}

func (p *MatchPool) selectSeeds(now int64) []*Ticket {
	if len(p.ticketsByDocID) == 0 {
		return nil
	}

	limit := p.config.SeedScheduler.SeedLimitPerTick
	if limit > len(p.ticketsByDocID) {
		limit = len(p.ticketsByDocID)
	}

	cfg := p.config.SeedScheduler
	if cfg.OldestRatio == 0 && cfg.MatchabilityRatio == 0 && cfg.RecentRatio == 0 && cfg.RandomRatio == 0 {
		cfg.OldestRatio = 0.8
		cfg.RandomRatio = 0.2
	}

	selected := make([]*Ticket, 0, limit)
	seen := NewTicketSet()
	addTicket := func(ticket *Ticket) {
		if len(selected) >= limit || ticket == nil || seen.Has(ticket.DocID) {
			return
		}
		seen.Add(ticket.DocID)
		selected = append(selected, ticket)
	}

	oldestCount := ratioCount(limit, cfg.OldestRatio)
	recentCount := ratioCount(limit, cfg.RecentRatio)
	matchabilityCount := ratioCount(limit, cfg.MatchabilityRatio)

	for _, docID := range p.arrivalOrder {
		if len(selected) >= oldestCount {
			break
		}
		addTicket(p.ticketsByDocID[docID])
	}

	if matchabilityCount > 0 {
		byMatchability := p.allTickets()
		sort.Slice(byMatchability, func(i, j int) bool {
			left := p.matchability(byMatchability[i], now)
			right := p.matchability(byMatchability[j], now)
			if left != right {
				return left > right
			}
			return byMatchability[i].DocID < byMatchability[j].DocID
		})
		target := len(selected) + matchabilityCount
		for i := 0; i < len(byMatchability) && len(selected) < target; i++ {
			addTicket(byMatchability[i])
		}
	}

	recentTarget := len(selected) + recentCount
	for i := len(p.arrivalOrder) - 1; i >= 0 && len(selected) < recentTarget; i-- {
		addTicket(p.ticketsByDocID[p.arrivalOrder[i]])
	}

	if cfg.RandomRatio > 0 || len(selected) < limit {
		randomOrder := p.allTickets()
		p.rand.Shuffle(len(randomOrder), func(i, j int) {
			randomOrder[i], randomOrder[j] = randomOrder[j], randomOrder[i]
		})
		for _, ticket := range randomOrder {
			addTicket(ticket)
			if len(selected) >= limit {
				break
			}
		}
	}

	return selected
}

func (p *MatchPool) allTickets() []*Ticket {
	all := make([]*Ticket, 0, len(p.ticketsByDocID))
	for _, ticket := range p.ticketsByDocID {
		all = append(all, ticket)
	}
	return all
}

func (p *MatchPool) matchability(seed *Ticket, now int64) int {
	return p.rules.CandidateSet(p, seed, now).Len()
}

func (p *MatchPool) removeDocID(docID uint32) {
	ticket, ok := p.ticketsByDocID[docID]
	if !ok {
		return
	}
	delete(p.ticketsByDocID, docID)
	delete(p.ticketIDToDocID, ticket.TicketID)
	for field := range ticket.Numeric {
		value := p.numericIndex[field][docID]
		delete(p.numericIndex[field], docID)
		if p.numericBuckets[field] != nil && p.numericBuckets[field][value] != nil {
			p.numericBuckets[field][value].Remove(docID)
			if p.numericBuckets[field][value].Len() == 0 {
				delete(p.numericBuckets[field], value)
			}
		}
	}
	for field, value := range ticket.Attributes {
		if p.attributeIndex[field] != nil && p.attributeIndex[field][value] != nil {
			p.attributeIndex[field][value].Remove(docID)
			if p.attributeIndex[field][value].Len() == 0 {
				delete(p.attributeIndex[field], value)
			}
		}
	}
}

func markGroupUsed(used TicketSet, group []*Ticket) {
	for _, ticket := range group {
		used.Add(ticket.DocID)
	}
}

func ratioCount(limit int, ratio float64) int {
	if ratio <= 0 {
		return 0
	}
	count := int(float64(limit) * ratio)
	if count == 0 {
		count = 1
	}
	if count > limit {
		return limit
	}
	return count
}
