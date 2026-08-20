package matchsystem

// Ticket is domain-neutral. Business data belongs in Attributes or Numeric.
type Ticket struct {
	DocID      uint32
	TicketID   string
	CreatedAt  int64
	MaxPlayers int
	Attributes map[string]string
	Numeric    map[string]int64
}

type Match struct {
	Tickets []*Ticket
}
