package matchsystem

import (
	nodecontract "matchSystem/internal/matchsystem/contract"
	nodeevaluation "matchSystem/internal/matchsystem/evaluation"
	"matchSystem/internal/matchsystem/expression"
)

// EvaluationPredicates is the compiled predicate-only Evaluation contract.
// LogicalNode owns candidate scoring and Match Fact providers directly.
type EvaluationPredicates = nodeevaluation.Predicates
type EvaluationCanJoinInput = nodeevaluation.CanJoinInput
type EvaluationCanCompleteInput = nodeevaluation.CanCompleteInput
type EvaluationError = nodeevaluation.Error

const EvaluationSchemaVersion = nodeevaluation.SchemaVersion

// CompileEvaluationJSON parses and compiles the strict Evaluation predicate
// envelope.
func CompileEvaluationJSON(data []byte, schema nodecontract.Contract, limits ...expression.JSONLimits) (EvaluationPredicates, error) {
	return nodeevaluation.CompileJSON(data, schema, limits...)
}
