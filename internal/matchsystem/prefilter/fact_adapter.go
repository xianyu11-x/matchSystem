package prefilter

import "matchSystem/internal/matchsystem/fact"

// Facts contains one immutable Fact layer. Tick Facts are borrowed by a
// TickSession; Seed Facts are borrowed for one synchronous Candidates call.
// Lists and Values use the same cardinality convention as common.Ticket.
type Facts = fact.Values

func validateFactScopes(tickNames, seedNames fact.NameSet) error {
	return adaptFactError(fact.ValidateScopes("facts.seed", "Tick", "Seed", tickNames, seedNames))
}

func adaptFactError(err error) error {
	if err == nil {
		return nil
	}
	if factErr, ok := err.(*fact.Error); ok {
		return evaluationError(factErr.Path, factErr.Code, "%v", factErr.Err)
	}
	return err
}
