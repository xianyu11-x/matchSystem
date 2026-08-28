package prefilter

import "matchSystem/internal/matchsystem/fact"

// Facts contains one immutable Fact layer. Tick Facts are borrowed by a
// TickSession; Seed Facts are borrowed for one synchronous Candidates call.
// Lists and Values use the same cardinality convention as common.Ticket.
type Facts = fact.Values
