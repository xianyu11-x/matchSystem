// Package common contains transport-neutral value objects shared by the
// client-side router and MatchService's in-process boundary.
package common

type Endpoint string

// Ticket intentionally has no DocID. DocID is allocated independently inside
// one LogicalNode and is never a cross-node identity. StringLists and
// Uint64Lists hold multiple values per field; Int64Values holds one scalar per
// field.
type Ticket struct {
	TicketID    string
	CreatedAt   int64
	StringLists map[string][]string
	Uint64Lists map[string][]uint64
	Int64Values map[string]int64
}

type Match struct {
	Tickets []Ticket
}

func CloneTicket(ticket Ticket) Ticket {
	clone := Ticket{
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
