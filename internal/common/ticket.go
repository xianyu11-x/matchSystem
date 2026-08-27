// Package common contains transport-neutral value objects shared by the
// client-side router and MatchService's in-process boundary.
package common

type Endpoint string
type TicketID = uint64

// Ticket intentionally has no DocID. DocID is allocated independently inside
// one LogicalNode and is never a cross-node identity. StringLists and
// Uint64Lists hold multiple values per field; Int64Values holds one scalar per
// field.
type Ticket struct {
	TicketID    TicketID
	CreatedAt   int64
	StringLists map[string][]string
	Uint64Lists map[string][]uint64
	Int64Values map[string]int64
}

// MatchFacts is the owned, typed Fact state committed alongside a Match.
// It intentionally mirrors fact.Values without importing the matchsystem/fact
// package, keeping common usable as the transport boundary.
type MatchFacts struct {
	StringLists map[string][]string
	Uint64Lists map[string][]uint64
	Int64Values map[string]int64
}

// CloneMatchFacts returns an independent deep copy of Match Facts.
func CloneMatchFacts(in MatchFacts) MatchFacts {
	out := MatchFacts{
		StringLists: make(map[string][]string, len(in.StringLists)),
		Uint64Lists: make(map[string][]uint64, len(in.Uint64Lists)),
		Int64Values: make(map[string]int64, len(in.Int64Values)),
	}
	for name, values := range in.StringLists {
		out.StringLists[name] = append([]string(nil), values...)
	}
	for name, values := range in.Uint64Lists {
		out.Uint64Lists[name] = append([]uint64(nil), values...)
	}
	for name, value := range in.Int64Values {
		out.Int64Values[name] = value
	}
	return out
}

type Match struct {
	// Tickets owns the pointers transferred out of the matching pool. Callers
	// may mutate or retain them after ProduceMatch returns.
	Tickets []*Ticket
	// Facts is an independent snapshot of the Match Facts committed by the
	// evaluation layer.
	Facts MatchFacts
}

// CloneTicket creates the one owned copy used when a Ticket enters a pool.
func CloneTicket(ticket *Ticket) *Ticket {
	if ticket == nil {
		return nil
	}
	clone := &Ticket{
		TicketID:    ticket.TicketID,
		CreatedAt:   ticket.CreatedAt,
		StringLists: make(map[string][]string, len(ticket.StringLists)),
		Uint64Lists: make(map[string][]uint64, len(ticket.Uint64Lists)),
		Int64Values: make(map[string]int64, len(ticket.Int64Values)),
	}
	for field, values := range ticket.StringLists {
		clone.StringLists[field] = append([]string(nil), values...)
	}
	for field, values := range ticket.Uint64Lists {
		clone.Uint64Lists[field] = append([]uint64(nil), values...)
	}
	for field, value := range ticket.Int64Values {
		clone.Int64Values[field] = value
	}
	return clone
}
