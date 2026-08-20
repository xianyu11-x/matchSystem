package matchsystem

type TicketSet map[uint32]struct{}

func NewTicketSet(ids ...uint32) TicketSet {
	s := make(TicketSet, len(ids))
	for _, id := range ids {
		s[id] = struct{}{}
	}
	return s
}

func (s TicketSet) Clone() TicketSet {
	out := make(TicketSet, len(s))
	for id := range s {
		out[id] = struct{}{}
	}
	return out
}

func (s TicketSet) Len() int {
	return len(s)
}

func (s TicketSet) Add(id uint32) {
	s[id] = struct{}{}
}

func (s TicketSet) Remove(id uint32) {
	delete(s, id)
}

func (s TicketSet) Has(id uint32) bool {
	_, ok := s[id]
	return ok
}

func (s TicketSet) And(other TicketSet) TicketSet {
	if len(s) > len(other) {
		return other.And(s)
	}
	out := make(TicketSet)
	for id := range s {
		if _, ok := other[id]; ok {
			out[id] = struct{}{}
		}
	}
	return out
}

func (s TicketSet) Or(other TicketSet) TicketSet {
	out := s.Clone()
	for id := range other {
		out[id] = struct{}{}
	}
	return out
}

func (s TicketSet) Difference(other TicketSet) TicketSet {
	out := make(TicketSet, len(s))
	for id := range s {
		if _, ok := other[id]; !ok {
			out[id] = struct{}{}
		}
	}
	return out
}

func (s TicketSet) IDs() []uint32 {
	ids := make([]uint32, 0, len(s))
	for id := range s {
		ids = append(ids, id)
	}
	return ids
}
