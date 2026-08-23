package matchsystem

// Ticket is domain-neutral. StringLists and Uint64Lists are multi-value
// fields, while Int64Values contains one scalar value per field.
type Ticket struct {
	DocID       uint32
	TicketID    string
	CreatedAt   int64
	StringLists map[string][]string
	Uint64Lists map[string][]uint64
	Int64Values map[string]int64
}

type Match struct{ Tickets []*Ticket }

func cloneTicket(ticket *Ticket) *Ticket {
	clone := &Ticket{DocID: ticket.DocID, TicketID: ticket.TicketID, CreatedAt: ticket.CreatedAt, StringLists: make(map[string][]string, len(ticket.StringLists)), Uint64Lists: make(map[string][]uint64, len(ticket.Uint64Lists)), Int64Values: make(map[string]int64, len(ticket.Int64Values))}
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
