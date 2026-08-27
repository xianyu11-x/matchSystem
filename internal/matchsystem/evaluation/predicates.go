package evaluation

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"matchSystem/internal/common"
	"matchSystem/internal/matchsystem/contract"
	"matchSystem/internal/matchsystem/expression"
	"matchSystem/internal/matchsystem/fact"
	"matchSystem/internal/matchsystem/jsonstrict"
)

// SchemaVersion identifies the predicate-only Evaluation envelope.
const SchemaVersion = "evaluation/v3"

// CanJoinInput is the complete read-only input visible to the CanJoin
// predicate. Existing Match members are intentionally absent. MatchFactsBefore
// is the complete aggregate snapshot immediately before the candidate is
// considered.
type CanJoinInput struct {
	Now              int64
	SeedAttributes   *common.Ticket
	SeedFacts        fact.Values
	TickFacts        fact.Values
	Candidate        *common.Ticket
	CandidateFacts   fact.Values
	MatchFactsBefore fact.Values
}

// CanCompleteInput is the complete read-only input visible to CanComplete.
// It can observe only the current Tick Facts and the complete Match Fact
// snapshot; Match members, Seed, and Candidate are not exposed.
type CanCompleteInput struct {
	TickFacts  fact.Values
	MatchFacts fact.Values
}

// Predicates is the compiled predicate set. Its expression implementation and
// validators remain private so callers cannot construct a typed expression
// graph or inject a phase/domain registry.
type Predicates struct {
	canJoin     *expression.ScalarProgram
	canComplete *expression.ScalarProgram
	attributes  *contract.AttributeValidator
	facts       *fact.Validator
}

// CompileJSON parses exactly the evaluation/v3 envelope and compiles its two
// required Bool scalar roots against a defensive Contract snapshot.
//
// The accepted top-level shape is exactly:
//
//	{"schemaVersion":"evaluation/v3","canJoin":<BoolRoot>,"canComplete":<BoolRoot>}
//
// Candidate scoring and Match Fact updates are owned by LogicalNode and its
// direct callbacks. They are deliberately not part of this document or
// compiler.
func CompileJSON(data []byte, schema contract.Contract, supplied ...expression.JSONLimits) (Predicates, error) {
	schema = schema.Clone()
	if err := schema.Validate(); err != nil {
		return Predicates{}, adaptCompileError(err)
	}
	if len(supplied) > 1 {
		return Predicates{}, compileError("jsonLimits", "INVALID_LIMIT", "at most one JSON limits value is allowed")
	}
	if len(supplied) == 1 {
		if err := validateEvaluationJSONLimits(supplied[0]); err != nil {
			return Predicates{}, err
		}
	}

	limits := evaluationLimits(schema, supplied...)
	if err := validateJSON(data, limits); err != nil {
		return Predicates{}, err
	}
	envelope, err := decodeObject(data, "$")
	if err != nil {
		return Predicates{}, err
	}
	version, err := schemaVersion(envelope)
	if err != nil {
		return Predicates{}, err
	}
	if version != SchemaVersion {
		return Predicates{}, jsonError("$.schemaVersion", "UNKNOWN_SCHEMA_VERSION", "unsupported schemaVersion %q", version)
	}
	// Check the current shape only after the version gate. An unsupported
	// document must fail as UNKNOWN_SCHEMA_VERSION before any legacy field is
	// interpreted.
	if err := checkFields(envelope, "$", "schemaVersion", "canJoin", "canComplete"); err != nil {
		return Predicates{}, err
	}
	canJoinRaw, err := requiredField(envelope, "canJoin", "$.canJoin")
	if err != nil {
		return Predicates{}, err
	}
	canCompleteRaw, err := requiredField(envelope, "canComplete", "$.canComplete")
	if err != nil {
		return Predicates{}, err
	}

	attributes, err := schema.CompileAttributeValidator()
	if err != nil {
		return Predicates{}, adaptCompileError(err)
	}
	facts, err := fact.NewValidator(schema.FactSpecs())
	if err != nil {
		return Predicates{}, adaptCompileError(err)
	}

	join, err := expression.CompileScalarJSON(canJoinRaw, expression.ScalarCompileOptions{
		Profile: evaluationProfile(schema, canJoinCapabilities, limits),
	})
	if err != nil {
		return Predicates{}, adaptExpressionError(err, "$.canJoin")
	}
	complete, err := expression.CompileScalarJSON(canCompleteRaw, expression.ScalarCompileOptions{
		Profile: evaluationProfile(schema, canCompleteCapabilities, limits),
	})
	if err != nil {
		return Predicates{}, adaptExpressionError(err, "$.canComplete")
	}
	if join.ResultType() != expression.ResultBool {
		return Predicates{}, compileError("$.canJoin.resultType", "ROOT_NOT_ALLOWED", "CanJoin must return bool")
	}
	if complete.ResultType() != expression.ResultBool {
		return Predicates{}, compileError("$.canComplete.resultType", "ROOT_NOT_ALLOWED", "CanComplete must return bool")
	}
	if err := checkAggregateCost(join, complete, limits); err != nil {
		return Predicates{}, err
	}
	return Predicates{canJoin: join, canComplete: complete, attributes: attributes, facts: facts}, nil
}

// schemaVersion is the evaluation-envelope variant of the shared loader
// rule: missing, legacy, and unknown envelope versions all use the same
// UNKNOWN_SCHEMA_VERSION boundary.  Scalar expressions intentionally retain
// their separate MISSING_FIELD contract.
func schemaVersion(object map[string]json.RawMessage) (string, error) {
	raw, ok := object["schemaVersion"]
	if !ok {
		return "", jsonError("$.schemaVersion", "UNKNOWN_SCHEMA_VERSION", "schemaVersion is required")
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return "", jsonError("$.schemaVersion", "NULL_NOT_ALLOWED", "schemaVersion must not be null")
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", jsonError("$.schemaVersion", "TYPE_MISMATCH", "string is required")
	}
	return value, nil
}

const (
	canJoinCapabilities = expression.CapabilitySeedAttributes |
		expression.CapabilitySeedFacts |
		expression.CapabilityTickFacts |
		expression.CapabilityCandidateAttributes |
		expression.CapabilityCandidateFacts |
		expression.CapabilityMatchFacts
	canCompleteCapabilities = expression.CapabilityTickFacts | expression.CapabilityMatchFacts
)

func factScopeAllows(source expression.Source, scope fact.Scope) bool {
	switch scope {
	case fact.ScopeTick:
		return source == expression.SourceTickFacts
	case fact.ScopeObject:
		return source == expression.SourceSeedFacts || source == expression.SourceCandidateFacts
	case fact.ScopeMatch:
		return source == expression.SourceMatchFacts
	default:
		return false
	}
}

func evaluationProfile(schema contract.Contract, capabilities expression.Capabilities, limits expression.JSONLimits) expression.CompileProfile {
	profile := expression.ProfileForRoots(expression.ResultBool)
	profile.AllowedSources = capabilities
	profile.Attributes = schema.AttributeSpecs()
	profile.Facts = schema.FactSpecs()
	profile.FactAllowed = func(source expression.Source, name string) bool {
		for _, spec := range profile.Facts {
			if spec.Name == name {
				return factScopeAllows(source, spec.Scope)
			}
		}
		return false
	}
	profile.JSONLimits = limits
	profile.Limits = expression.Limits{
		MaxDepth:         limits.MaxDepth,
		MaxChildren:      limits.MaxChildren,
		MaxLiteralValues: limits.MaxLiteralValues,
		MaxSteps:         limits.MaxSteps,
		MaxNodes:         limits.MaxNodes,
		MaxInstructions:  limits.MaxInstructions,
	}
	return profile
}

// evaluationLimits creates one immutable limit snapshot for the complete
// envelope. Contract limits are the shared outer boundary; zero fields use
// the contract defaults and scalar expression defaults.
func evaluationLimits(schema contract.Contract, supplied ...expression.JSONLimits) expression.JSONLimits {
	limits := expression.DefaultJSONLimits()
	contractLimits := schema.Limits
	defaults := contract.DefaultLimits()
	if contractLimits.MaxBytes == 0 {
		contractLimits.MaxBytes = defaults.MaxBytes
	}
	if contractLimits.MaxDepth == 0 {
		contractLimits.MaxDepth = defaults.MaxDepth
	}
	if contractLimits.MaxChildren == 0 {
		contractLimits.MaxChildren = defaults.MaxChildren
	}
	if contractLimits.MaxStringBytes == 0 {
		contractLimits.MaxStringBytes = defaults.MaxStringBytes
	}
	if contractLimits.MaxValues == 0 {
		contractLimits.MaxValues = defaults.MaxValues
	}
	limits.MaxBytes = contractLimits.MaxBytes
	limits.MaxDepth = contractLimits.MaxDepth
	// MaxObjectFields and MaxArrayItems belong to the scalar JSON contract.
	// They intentionally retain expression.DefaultJSONLimits and are not
	// aliases of the outer Contract.MaxChildren AST limit.
	limits.MaxChildren = contractLimits.MaxChildren
	limits.MaxStringBytes = contractLimits.MaxStringBytes
	limits.MaxValues = contractLimits.MaxValues
	if len(supplied) == 1 {
		limits = tightenEvaluationJSONLimits(limits, supplied[0])
	}
	return limits
}

func validateEvaluationJSONLimits(limits expression.JSONLimits) error {
	for _, item := range []struct {
		name  string
		value int
	}{
		{"maxBytes", limits.MaxBytes}, {"maxDepth", limits.MaxDepth},
		{"maxObjectFields", limits.MaxObjectFields}, {"maxArrayItems", limits.MaxArrayItems},
		{"maxValues", limits.MaxValues}, {"maxStringBytes", limits.MaxStringBytes},
		{"maxChildren", limits.MaxChildren}, {"maxLiteralValues", limits.MaxLiteralValues},
		{"maxSteps", limits.MaxSteps}, {"maxNodes", limits.MaxNodes}, {"maxInstructions", limits.MaxInstructions},
	} {
		if item.value < 0 {
			return compileError("jsonLimits."+item.name, "INVALID_LIMIT", "JSON limit must not be negative")
		}
	}
	return nil
}

// tightenEvaluationJSONLimits overlays a caller snapshot without allowing it
// to loosen any Contract-bound field.  Scalar-only fields are still useful to
// the caller, but zero means "use the default" and never removes a bound.
func tightenEvaluationJSONLimits(base, supplied expression.JSONLimits) expression.JSONLimits {
	minPositive := func(current, requested int) int {
		if requested <= 0 {
			return current
		}
		if current <= 0 || requested < current {
			return requested
		}
		return current
	}
	base.MaxBytes = minPositive(base.MaxBytes, supplied.MaxBytes)
	base.MaxDepth = minPositive(base.MaxDepth, supplied.MaxDepth)
	base.MaxObjectFields = minPositive(base.MaxObjectFields, supplied.MaxObjectFields)
	base.MaxArrayItems = minPositive(base.MaxArrayItems, supplied.MaxArrayItems)
	base.MaxValues = minPositive(base.MaxValues, supplied.MaxValues)
	base.MaxStringBytes = minPositive(base.MaxStringBytes, supplied.MaxStringBytes)
	base.MaxChildren = minPositive(base.MaxChildren, supplied.MaxChildren)
	base.MaxLiteralValues = minPositive(base.MaxLiteralValues, supplied.MaxLiteralValues)
	base.MaxSteps = minPositive(base.MaxSteps, supplied.MaxSteps)
	base.MaxNodes = minPositive(base.MaxNodes, supplied.MaxNodes)
	base.MaxInstructions = minPositive(base.MaxInstructions, supplied.MaxInstructions)
	return base
}

func validateJSON(data []byte, limits expression.JSONLimits) error {
	if limits.MaxBytes < 0 || limits.MaxDepth < 0 || limits.MaxObjectFields < 0 || limits.MaxArrayItems < 0 || limits.MaxChildren < 0 || limits.MaxStringBytes < 0 || limits.MaxValues < 0 {
		return jsonError("$", "INVALID_LIMIT", "evaluation limits must not be negative")
	}
	if err := jsonstrict.ValidateWithOptions(data, jsonstrict.Options{
		MaxBytes:        limits.MaxBytes,
		MaxDepth:        limits.MaxDepth,
		MaxObjectFields: limits.MaxObjectFields,
		MaxArrayItems:   limits.MaxArrayItems,
		MaxValues:       limits.MaxValues,
		MaxStringBytes:  limits.MaxStringBytes,
	}); err != nil {
		return adaptJSONStructureError(err, "$")
	}
	return nil
}

func requiredField(object map[string]json.RawMessage, name, path string) (json.RawMessage, error) {
	value, ok := object[name]
	if !ok {
		return nil, jsonError(path, "MISSING_FIELD", "%s is required", name)
	}
	if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
		return nil, jsonError(path, "NULL_NOT_ALLOWED", "%s must not be null", name)
	}
	return value, nil
}

func decodeObject(data []byte, path string) (map[string]json.RawMessage, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil || object == nil {
		return nil, jsonError(path, "INVALID_OBJECT", "object is required")
	}
	return object, nil
}

func checkFields(object map[string]json.RawMessage, path string, allowed ...string) error {
	set := make(map[string]struct{}, len(allowed))
	for _, field := range allowed {
		set[field] = struct{}{}
	}
	unknown := ""
	for field := range object {
		if _, ok := set[field]; !ok && (unknown == "" || field < unknown) {
			unknown = field
		}
	}
	if unknown == "" {
		return nil
	}
	return jsonError(path+"."+unknown, "UNKNOWN_FIELD", "unknown field %q", unknown)
}

func checkAggregateCost(join, complete *expression.ScalarProgram, limits expression.JSONLimits) error {
	if join == nil || complete == nil {
		return compileError("$", "INVALID_PROGRAM", "evaluation predicates are not compiled")
	}
	left, right := join.Cost(), complete.Cost()
	if limits.MaxNodes > 0 && left.Nodes+right.Nodes > limits.MaxNodes {
		return jsonError("$", "NODE_LIMIT", "evaluation contains %d nodes; maximum is %d", left.Nodes+right.Nodes, limits.MaxNodes)
	}
	if limits.MaxInstructions > 0 && left.Instructions+right.Instructions > limits.MaxInstructions {
		return jsonError("$", "INSTRUCTION_LIMIT", "evaluation contains %d instructions; maximum is %d", left.Instructions+right.Instructions, limits.MaxInstructions)
	}
	if limits.MaxLiteralValues > 0 && left.LiteralValues+right.LiteralValues > limits.MaxLiteralValues {
		return jsonError("$", "VALUE_LIMIT", "evaluation contains %d literal values; maximum is %d", left.LiteralValues+right.LiteralValues, limits.MaxLiteralValues)
	}
	if limits.MaxSteps > 0 && left.Steps+right.Steps > limits.MaxSteps {
		return jsonError("$", "STEP_LIMIT", "evaluation contains %d steps; maximum is %d", left.Steps+right.Steps, limits.MaxSteps)
	}
	return nil
}

// CanJoin evaluates only the compiled Bool predicate. Inputs and Fact layers
// are copied before validation/evaluation, so expression code cannot mutate
// caller-owned state.
func (p Predicates) CanJoin(input CanJoinInput) (bool, error) {
	if p.canJoin == nil {
		return false, evaluateError("canJoin", "MISSING_EXPRESSION", "canJoin is not compiled")
	}
	if p.attributes == nil || p.facts == nil {
		return false, evaluateError("canJoin", "MISSING_VALIDATOR", "evaluation predicates are not initialized")
	}
	lookup := newJoinLookup(input)
	if err := p.validateJoinInput(lookup); err != nil {
		return false, err
	}
	result, err := p.canJoin.EvaluateBool(lookup)
	if err != nil || lookup.denied != nil || lookup.missing != nil {
		return false, adaptLookupError(err, "canJoin", lookup)
	}
	return result, nil
}

// CanComplete evaluates only the compiled Bool predicate. Its input does not
// contain any Match member collection, Seed, or Candidate.
func (p Predicates) CanComplete(input CanCompleteInput) (bool, error) {
	if p.canComplete == nil {
		return false, evaluateError("canComplete", "MISSING_EXPRESSION", "canComplete is not compiled")
	}
	if p.facts == nil {
		return false, evaluateError("canComplete", "MISSING_VALIDATOR", "evaluation predicates are not initialized")
	}
	lookup := newCompleteLookup(input)
	if err := validateFacts(p.facts, "canComplete.tickFacts", lookup.tickFacts, fact.ScopeTick); err != nil {
		return false, err
	}
	if err := validateCompleteMatchFacts(p.facts, "canComplete.matchFacts", lookup.matchFacts); err != nil {
		return false, err
	}
	result, err := p.canComplete.EvaluateBool(lookup)
	if err != nil || lookup.denied != nil || lookup.missing != nil {
		return false, adaptLookupError(err, "canComplete", lookup)
	}
	return result, nil
}

type scalarLookup struct {
	seed           *common.Ticket
	candidate      *common.Ticket
	seedFacts      fact.Values
	candidateFacts fact.Values
	tickFacts      fact.Values
	matchFacts     fact.Values
	allowed        expression.Capabilities
	missing        *lookupMissing
	denied         *lookupDenied
}

type lookupMissing struct {
	source expression.Source
	name   string
	kind   string
}

func (e *lookupMissing) Error() string {
	return fmt.Sprintf("%s value %q is missing from source %d", e.kind, e.name, e.source)
}

type lookupDenied struct {
	source expression.Source
	name   string
	kind   string
}

func (e *lookupDenied) Error() string {
	return fmt.Sprintf("%s value %q is not allowed from source %d", e.kind, e.name, e.source)
}

func newJoinLookup(input CanJoinInput) *scalarLookup {
	return &scalarLookup{
		seed:           common.CloneTicket(input.SeedAttributes),
		candidate:      common.CloneTicket(input.Candidate),
		seedFacts:      fact.Clone(input.SeedFacts),
		candidateFacts: fact.Clone(input.CandidateFacts),
		tickFacts:      fact.Clone(input.TickFacts),
		matchFacts:     fact.Clone(input.MatchFactsBefore),
		allowed:        canJoinCapabilities,
	}
}

func newCompleteLookup(input CanCompleteInput) *scalarLookup {
	return &scalarLookup{
		tickFacts:  fact.Clone(input.TickFacts),
		matchFacts: fact.Clone(input.MatchFacts),
		allowed:    canCompleteCapabilities,
	}
}

func (l *scalarLookup) allow(source expression.Source, name, kind string) bool {
	if l == nil {
		return false
	}
	if l.allowed.Allows(source) {
		return true
	}
	if l.denied == nil {
		l.denied = &lookupDenied{source: source, name: name, kind: kind}
	}
	return false
}

func (l *scalarLookup) missingValue(source expression.Source, name, kind string) {
	if l != nil && l.denied == nil && l.missing == nil {
		l.missing = &lookupMissing{source: source, name: name, kind: kind}
	}
}

func (l *scalarLookup) Strings(source expression.Source, name string) ([]string, bool) {
	if !l.allow(source, name, "strings") {
		return nil, false
	}
	var values []string
	var ok bool
	switch source {
	case expression.SourceSeedAttributes:
		if l.seed != nil {
			values, ok = l.seed.StringLists[name]
		}
	case expression.SourceCandidateAttributes:
		if l.candidate != nil {
			values, ok = l.candidate.StringLists[name]
		}
	case expression.SourceSeedFacts:
		values, ok = l.seedFacts.StringLists[name]
	case expression.SourceCandidateFacts:
		values, ok = l.candidateFacts.StringLists[name]
	case expression.SourceTickFacts:
		values, ok = l.tickFacts.StringLists[name]
	case expression.SourceMatchFacts:
		values, ok = l.matchFacts.StringLists[name]
	}
	if !ok {
		l.missingValue(source, name, "strings")
	}
	return values, ok
}

func (l *scalarLookup) Uint64s(source expression.Source, name string) ([]uint64, bool) {
	if !l.allow(source, name, "uint64s") {
		return nil, false
	}
	var values []uint64
	var ok bool
	switch source {
	case expression.SourceSeedAttributes:
		if l.seed != nil {
			values, ok = l.seed.Uint64Lists[name]
		}
	case expression.SourceCandidateAttributes:
		if l.candidate != nil {
			values, ok = l.candidate.Uint64Lists[name]
		}
	case expression.SourceSeedFacts:
		values, ok = l.seedFacts.Uint64Lists[name]
	case expression.SourceCandidateFacts:
		values, ok = l.candidateFacts.Uint64Lists[name]
	case expression.SourceTickFacts:
		values, ok = l.tickFacts.Uint64Lists[name]
	case expression.SourceMatchFacts:
		values, ok = l.matchFacts.Uint64Lists[name]
	}
	if !ok {
		l.missingValue(source, name, "uint64s")
	}
	return values, ok
}

func (l *scalarLookup) Int64(source expression.Source, name string) (int64, bool) {
	if !l.allow(source, name, "int64") {
		return 0, false
	}
	var value int64
	var ok bool
	switch source {
	case expression.SourceSeedAttributes:
		if l.seed != nil {
			value, ok = l.seed.Int64Values[name]
		}
	case expression.SourceCandidateAttributes:
		if l.candidate != nil {
			value, ok = l.candidate.Int64Values[name]
		}
	case expression.SourceSeedFacts:
		value, ok = l.seedFacts.Int64Values[name]
	case expression.SourceCandidateFacts:
		value, ok = l.candidateFacts.Int64Values[name]
	case expression.SourceTickFacts:
		value, ok = l.tickFacts.Int64Values[name]
	case expression.SourceMatchFacts:
		value, ok = l.matchFacts.Int64Values[name]
	}
	if !ok {
		l.missingValue(source, name, "int64")
	}
	return value, ok
}

var _ expression.Lookup = (*scalarLookup)(nil)

func (p Predicates) validateJoinInput(lookup *scalarLookup) error {
	if lookup.seed != nil {
		if err := p.attributes.ValidateTicket("canJoin.seed.attributes", lookup.seed); err != nil {
			return adaptContextError(err, "canJoin.seed.attributes")
		}
	}
	if lookup.candidate != nil {
		if err := p.attributes.ValidateTicket("canJoin.candidate.attributes", lookup.candidate); err != nil {
			return adaptContextError(err, "canJoin.candidate.attributes")
		}
	}
	if err := validateFacts(p.facts, "canJoin.tickFacts", lookup.tickFacts, fact.ScopeTick); err != nil {
		return err
	}
	if err := validateFacts(p.facts, "canJoin.seedFacts", lookup.seedFacts, fact.ScopeObject); err != nil {
		return err
	}
	if err := validateFacts(p.facts, "canJoin.candidateFacts", lookup.candidateFacts, fact.ScopeObject); err != nil {
		return err
	}
	return validateCompleteMatchFacts(p.facts, "canJoin.matchFacts", lookup.matchFacts)
}

func validateFacts(validator *fact.Validator, path string, values fact.Values, scope fact.Scope) error {
	if validator == nil {
		return evaluateError(path, "MISSING_VALIDATOR", "Fact validator is not initialized")
	}
	if _, err := validator.ValidateLayer(path, values, scope); err != nil {
		return adaptContextError(err, path)
	}
	return nil
}

func validateCompleteMatchFacts(validator *fact.Validator, path string, values fact.Values) error {
	if validator == nil {
		return evaluateError(path, "MISSING_VALIDATOR", "Fact validator is not initialized")
	}
	if err := validator.ValidateCompleteMatch(path, values); err != nil {
		return adaptContextError(err, path)
	}
	return nil
}

func adaptLookupError(err error, path string, lookup *scalarLookup) error {
	if lookup != nil && lookup.denied != nil {
		return &Error{Phase: "evaluate", Path: path, Code: "SOURCE_NOT_ALLOWED", Err: lookup.denied}
	}
	if lookup != nil && lookup.missing != nil {
		return &Error{Phase: "evaluate", Path: path, Code: "MISSING_VALUE", Err: lookup.missing}
	}
	return adaptEvaluateError(err, path)
}

func adaptEvaluateError(err error, path string) error {
	if err == nil {
		return nil
	}
	var expressionErr *expression.Error
	if errors.As(err, &expressionErr) {
		return &Error{Phase: "evaluate", Path: path, Code: expressionErr.Code, Err: expressionErr.Err}
	}
	return &Error{Phase: "evaluate", Path: path, Code: "EXPRESSION", Err: err}
}

func adaptCompileError(err error) error {
	if err == nil {
		return nil
	}
	var evaluationErr *Error
	if errors.As(err, &evaluationErr) {
		return evaluationErr
	}
	var contractErr *contract.Error
	if errors.As(err, &contractErr) {
		return &Error{Phase: "compile", Path: contractErr.Path, Code: contractErr.Code, Err: contractErr.Err}
	}
	return &Error{Phase: "compile", Code: "CONTRACT", Err: err}
}

func adaptExpressionError(err error, prefix string) error {
	if err == nil {
		return nil
	}
	var evaluationErr *Error
	if errors.As(err, &evaluationErr) {
		return &Error{Phase: evaluationErr.Phase, Path: prefixPath(prefix, evaluationErr.Path), Code: evaluationErr.Code, Err: evaluationErr.Err}
	}
	var expressionErr *expression.Error
	if errors.As(err, &expressionErr) {
		return &Error{Phase: expressionErr.Phase, Path: prefixPath(prefix, string(expressionErr.Path)), Code: expressionErr.Code, Err: expressionErr.Err}
	}
	return &Error{Phase: "compile", Path: prefix, Code: "EXPRESSION", Err: err}
}

func adaptContextError(err error, prefix string) error {
	if err == nil {
		return nil
	}
	var factErr *fact.Error
	if errors.As(err, &factErr) {
		return &Error{Phase: "evaluate", Path: prefixPath(prefix, factErr.Path), Code: factErr.Code, Err: factErr.Err}
	}
	var contractErr *contract.Error
	if errors.As(err, &contractErr) {
		return &Error{Phase: "evaluate", Path: prefixPath(prefix, contractErr.Path), Code: contractErr.Code, Err: contractErr.Err}
	}
	return &Error{Phase: "evaluate", Path: prefix, Code: "CONTEXT", Err: err}
}

func prefixPath(prefix, path string) string {
	if prefix == "" {
		return path
	}
	if path == "" || path == "$" || path == "root" {
		return prefix
	}
	if strings.HasPrefix(path, "$") {
		return prefix + strings.TrimPrefix(path, "$")
	}
	if strings.HasPrefix(path, "root") {
		return prefix + strings.TrimPrefix(path, "root")
	}
	if strings.HasPrefix(path, prefix) {
		return path
	}
	return prefix + "." + path
}

func adaptJSONStructureError(err error, fallback string) error {
	var structural *jsonstrict.Error
	if errors.As(err, &structural) {
		return jsonError(structural.Path, structural.Code, "%v", structural.Err)
	}
	return jsonError(fallback, "INVALID_JSON", "%v", err)
}
