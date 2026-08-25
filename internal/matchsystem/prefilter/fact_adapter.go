package prefilter

import "matchSystem/internal/matchsystem/fact"

type FactType = fact.Type

const (
	FactTypeStrings = fact.TypeStrings
	FactTypeInt64   = fact.TypeInt64
	FactTypeUint64s = fact.TypeUint64s
)

type FactSpec = fact.Spec

// Facts contains one immutable Fact layer. Tick Facts are borrowed by a
// TickSession; Seed Facts are borrowed for one synchronous Candidates call.
// Lists and Values use the same cardinality convention as common.Ticket.
type Facts = fact.Values

func validateFactTypes(path string, facts Facts) (fact.NameSet, error) {
	names, err := fact.ValidateTypes(path, facts)
	return names, adaptFactError(err)
}

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
