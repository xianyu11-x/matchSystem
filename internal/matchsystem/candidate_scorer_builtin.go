package matchsystem

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"

	"matchSystem/internal/matchsystem/contract"
	"matchSystem/internal/matchsystem/jsonstrict"
)

// CandidateScorerType identifies a built-in candidate scoring function.
//
// The values are part of the rule configuration format. They are deliberately
// kept separate from CandidateScorer, which is the runtime callback used by
// the matching pipeline.
type CandidateScorerType string

const (
	CandidateScorerConstant   CandidateScorerType = "constant"
	CandidateScorerCreatedAt  CandidateScorerType = "created_at"
	CandidateScorerInt64Field CandidateScorerType = "int64_field"
)

// CandidateScorerConfig is the JSON-facing description of one built-in
// scorer. Params must contain one JSON object whose shape is determined by
// Type.
type CandidateScorerConfig struct {
	Type   CandidateScorerType `json:"type"`
	Params json.RawMessage     `json:"params"`
}

type candidateScorerConstantParams struct {
	Value *float64 `json:"value"`
}

type candidateScorerCreatedAtParams struct {
	Direction string   `json:"direction"`
	Weight    *float64 `json:"weight"`
}

type candidateScorerInt64FieldParams struct {
	Field        string   `json:"field"`
	Direction    string   `json:"direction"`
	Weight       *float64 `json:"weight"`
	MissingScore *float64 `json:"missingScore"`
}

const (
	candidateScorerDirectionAscending  = "ascending"
	candidateScorerDirectionDescending = "descending"
	candidateScorerMaxWeight           = math.MaxFloat64 / float64(9223372036854775807)
)

// NewCandidateScorer compiles a built-in scorer from its configuration. The
// returned callback only captures immutable scalar configuration and is safe
// to own exclusively inside one LogicalNode.
func NewCandidateScorer(config CandidateScorerConfig, schema contract.Contract) (CandidateScorer, error) {
	switch config.Type {
	case CandidateScorerConstant:
		params, err := decodeCandidateScorerParams(config.Params, &candidateScorerConstantParams{})
		if err != nil {
			return nil, fmt.Errorf("decode %q scorer params: %w", config.Type, err)
		}
		value := params.(*candidateScorerConstantParams).Value
		if value == nil {
			return nil, fmt.Errorf("%q scorer params.value is required", config.Type)
		}
		if !finiteCandidateScore(*value) {
			return nil, fmt.Errorf("%q scorer params.value must be finite", config.Type)
		}
		return func(CandidateScoreContext) (float64, error) {
			return *value, nil
		}, nil

	case CandidateScorerCreatedAt:
		params, err := decodeCandidateScorerParams(config.Params, &candidateScorerCreatedAtParams{})
		if err != nil {
			return nil, fmt.Errorf("decode %q scorer params: %w", config.Type, err)
		}
		value := params.(*candidateScorerCreatedAtParams)
		if err := validateCandidateScorerDirection(config.Type, value.Direction); err != nil {
			return nil, err
		}
		weight, err := candidateScorerWeight(config.Type, value.Weight)
		if err != nil {
			return nil, err
		}
		factor := candidateScorerDirectionFactor(value.Direction)
		return func(ctx CandidateScoreContext) (float64, error) {
			if ctx.Candidate == nil {
				return 0, fmt.Errorf("%q scorer received a nil candidate", config.Type)
			}
			return float64(ctx.Candidate.CreatedAt) * factor * weight, nil
		}, nil

	case CandidateScorerInt64Field:
		params, err := decodeCandidateScorerParams(config.Params, &candidateScorerInt64FieldParams{})
		if err != nil {
			return nil, fmt.Errorf("decode %q scorer params: %w", config.Type, err)
		}
		value := params.(*candidateScorerInt64FieldParams)
		if value.Field == "" {
			return nil, fmt.Errorf("%q scorer params.field is required", config.Type)
		}
		if err := validateCandidateScorerDirection(config.Type, value.Direction); err != nil {
			return nil, err
		}
		weight, err := candidateScorerWeight(config.Type, value.Weight)
		if err != nil {
			return nil, err
		}
		missingScore := -math.MaxFloat64
		if value.MissingScore != nil {
			if !finiteCandidateScore(*value.MissingScore) {
				return nil, fmt.Errorf("%q scorer params.missingScore must be finite", config.Type)
			}
			missingScore = *value.MissingScore
		}
		attribute, found := candidateScorerAttribute(schema, value.Field)
		if !found {
			return nil, fmt.Errorf("%q scorer field %q is not a declared attribute", config.Type, value.Field)
		}
		if attribute.Type != contract.FactTypeInt64 {
			return nil, fmt.Errorf("%q scorer field %q must be an int64 attribute", config.Type, value.Field)
		}
		factor := candidateScorerDirectionFactor(value.Direction)
		return func(ctx CandidateScoreContext) (float64, error) {
			if ctx.Candidate == nil {
				return 0, fmt.Errorf("%q scorer received a nil candidate", config.Type)
			}
			raw, found := ctx.Candidate.Int64Values[value.Field]
			if !found {
				// missingScore is already in final score space. Keeping it out of
				// the direction/weight transform guarantees that the default
				// value is always worse than a present field.
				return missingScore, nil
			}
			return float64(raw) * factor * weight, nil
		}, nil

	default:
		return nil, fmt.Errorf("unsupported candidate scorer type %q", config.Type)
	}
}

func decodeCandidateScorerParams(raw json.RawMessage, target any) (any, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("params is required")
	}
	if trimmed[0] != '{' {
		return nil, fmt.Errorf("params must be one JSON object")
	}
	// Keep the scorer parameter boundary consistent with the other JSON
	// compilers: reject duplicate keys, null values, malformed input, and
	// trailing JSON before decoding the type-specific object.
	if err := jsonstrict.ValidateWithOptions(trimmed, jsonstrict.Options{}); err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("params must contain one JSON object")
		}
		return nil, err
	}
	return target, nil
}

func validateCandidateScorerDirection(kind CandidateScorerType, direction string) error {
	if direction != candidateScorerDirectionAscending && direction != candidateScorerDirectionDescending {
		return fmt.Errorf("%q scorer params.direction must be %q or %q", kind, candidateScorerDirectionAscending, candidateScorerDirectionDescending)
	}
	return nil
}

func candidateScorerDirectionFactor(direction string) float64 {
	if direction == candidateScorerDirectionAscending {
		return -1
	}
	return 1
}

func candidateScorerWeight(kind CandidateScorerType, value *float64) (float64, error) {
	weight := 1.0
	if value != nil {
		weight = *value
	}
	if !finiteCandidateScore(weight) || weight <= 0 || weight > candidateScorerMaxWeight {
		return 0, fmt.Errorf("%q scorer params.weight must be finite, greater than zero, and at most %g", kind, candidateScorerMaxWeight)
	}
	return weight, nil
}

func finiteCandidateScore(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func candidateScorerAttribute(schema contract.Contract, name string) (contract.AttributeSpec, bool) {
	for _, attribute := range schema.Attributes {
		if attribute.Name == name {
			return attribute, true
		}
	}
	return contract.AttributeSpec{}, false
}
