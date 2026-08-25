package prefilter

import "matchSystem/internal/common"

type indexedTestTicket struct {
	docID uint32
	*common.Ticket
}

func indexedTicket(docID uint32, ticket *common.Ticket) indexedTestTicket {
	if ticket.TicketID == 0 {
		ticket.TicketID = uint64(docID)
	}
	return indexedTestTicket{docID: docID, Ticket: ticket}
}
