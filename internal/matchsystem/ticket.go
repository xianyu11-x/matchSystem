package matchsystem

import "matchSystem/internal/common"

type TicketID = common.TicketID
type Ticket = common.Ticket
type Match = common.Match

// storedTicket keeps LogicalNode-local index metadata separate from the one
// shared Ticket model. The Ticket pointer is owned by the pool until Remove or
// a committed match transfers/discards that ownership.
type storedTicket struct {
	*Ticket
	docID        uint32
	arrivalIndex int
}
