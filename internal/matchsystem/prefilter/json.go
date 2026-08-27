package prefilter

import (
	"errors"

	"matchSystem/internal/matchsystem/contract"
	"matchSystem/internal/matchsystem/expression"
	"matchSystem/internal/matchsystem/jsonstrict"
)

// JSONSchemaVersion is the only Prefilter plan envelope accepted by the
// production API. The envelope owns a Prefilter bitmap expression; scalar operands
// inside it use expression-scalar/v3.
const JSONSchemaVersion = prefilterSchemaVersion

// DefaultJSONLimits returns the shared expression JSON bounds. Prefilter does
// not define a second domain-specific limit model.
func DefaultJSONLimits() expression.JSONLimits { return expression.DefaultJSONLimits() }

// JSONCompiler snapshots one Contract and compiles exactly one prefilter/v3
// document. Parsing, semantic compilation, and Plan construction share one
// private Prefilter compiler boundary.
type JSONCompiler struct {
	compiler *bitmapCompiler
}

// NewJSONCompiler creates a compiler for one immutable Contract. An optional
// JSON limit set can only tighten the Contract's effective limits.
func NewJSONCompiler(schema contract.Contract, limitValues ...expression.JSONLimits) (*JSONCompiler, error) {
	compiler, err := newBitmapCompiler(schema, limitValues...)
	if err != nil {
		return nil, err
	}
	return &JSONCompiler{compiler: compiler}, nil
}

// Compile validates and compiles a prefilter/v3 document into an immutable
// runtime Plan. Legacy envelopes and typed roots are rejected by the compiler
// before a Plan is created.
func (c *JSONCompiler) Compile(data []byte) (*Plan, error) {
	if c == nil || c.compiler == nil {
		return nil, jsonError("$", "NIL_JSON_COMPILER", "JSON compiler is nil")
	}
	return c.compiler.compile(data)
}

// CompileJSON is the single-call production entry point for callers that do
// not need to retain a compiler instance.
func CompileJSON(data []byte, schema contract.Contract, limitValues ...expression.JSONLimits) (*Plan, error) {
	compiler, err := NewJSONCompiler(schema, limitValues...)
	if err != nil {
		return nil, err
	}
	return compiler.Compile(data)
}

func normalizePrefilterJSONLimits(limits expression.JSONLimits) expression.JSONLimits {
	defaults := expression.DefaultJSONLimits()
	if limits.MaxBytes == 0 {
		limits.MaxBytes = defaults.MaxBytes
	}
	if limits.MaxDepth == 0 {
		limits.MaxDepth = defaults.MaxDepth
	}
	if limits.MaxObjectFields == 0 {
		limits.MaxObjectFields = defaults.MaxObjectFields
	}
	if limits.MaxArrayItems == 0 {
		limits.MaxArrayItems = defaults.MaxArrayItems
	}
	if limits.MaxValues == 0 {
		limits.MaxValues = defaults.MaxValues
	}
	if limits.MaxStringBytes == 0 {
		limits.MaxStringBytes = defaults.MaxStringBytes
	}
	if limits.MaxChildren == 0 {
		limits.MaxChildren = defaults.MaxChildren
	}
	if limits.MaxLiteralValues == 0 {
		limits.MaxLiteralValues = defaults.MaxLiteralValues
	}
	if limits.MaxSteps == 0 {
		limits.MaxSteps = defaults.MaxSteps
	}
	if limits.MaxNodes == 0 {
		limits.MaxNodes = defaults.MaxNodes
	}
	if limits.MaxInstructions == 0 {
		limits.MaxInstructions = defaults.MaxInstructions
	}
	return limits
}

func snapshotContractLimits(limits contract.Limits) contract.Limits {
	defaults := contract.DefaultLimits()
	if limits.MaxBytes == 0 {
		limits.MaxBytes = defaults.MaxBytes
	}
	if limits.MaxDepth == 0 {
		limits.MaxDepth = defaults.MaxDepth
	}
	if limits.MaxChildren == 0 {
		limits.MaxChildren = defaults.MaxChildren
	}
	if limits.MaxStringBytes == 0 {
		limits.MaxStringBytes = defaults.MaxStringBytes
	}
	if limits.MaxValues == 0 {
		limits.MaxValues = defaults.MaxValues
	}
	return limits
}

func adaptJSONStrictError(err error) error {
	if err == nil {
		return nil
	}
	var strictErr *jsonstrict.Error
	if errors.As(err, &strictErr) {
		return jsonError(strictErr.Path, strictErr.Code, "%v", strictErr.Err)
	}
	return err
}

func validateJSONLimits(limits expression.JSONLimits) error {
	values := []struct {
		name  string
		value int
	}{
		{"maxBytes", limits.MaxBytes}, {"maxDepth", limits.MaxDepth},
		{"maxObjectFields", limits.MaxObjectFields}, {"maxArrayItems", limits.MaxArrayItems},
		{"maxValues", limits.MaxValues}, {"maxStringBytes", limits.MaxStringBytes},
		{"maxChildren", limits.MaxChildren}, {"maxLiteralValues", limits.MaxLiteralValues},
		{"maxSteps", limits.MaxSteps}, {"maxNodes", limits.MaxNodes}, {"maxInstructions", limits.MaxInstructions},
	}
	for _, item := range values {
		if item.value < 0 {
			return compileError("jsonLimits."+item.name, "INVALID_LIMIT", "JSON limit must not be negative")
		}
	}
	return nil
}
