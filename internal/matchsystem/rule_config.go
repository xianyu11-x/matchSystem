package matchsystem

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"

	"matchSystem/internal/identity"
	"matchSystem/internal/matchsystem/contract"
	"matchSystem/internal/matchsystem/evaluation"
	"matchSystem/internal/matchsystem/jsonstrict"
	"matchSystem/internal/matchsystem/prefilter"
)

// RuleJSONSchemaVersion identifies the complete, one-file configuration for
// one matching rule. The embedded Contract, Prefilter, and Evaluation
// documents intentionally retain their own v3 envelopes.
const RuleJSONSchemaVersion = "match-rule/v1"

// Candidate limits are intentionally kept at the aggregate RuleJSON boundary
// so every LogicalNode gets the same bounded ranking policy.  The scoring pool
// is larger than the retained Top-L by default: this keeps scoring work bounded
// without changing the existing heap-based ranking semantics for the retained
// candidates.
const (
	defaultCandidateScoringLimitPerSeed = 500
	defaultCandidateLimitPerSeed        = 50
)

var defaultRuleJSONLimits = jsonstrict.Options{
	MaxBytes:        4 << 20,
	MaxDepth:        96,
	MaxObjectFields: 256,
	MaxArrayItems:   10000,
	MaxValues:       50000,
	MaxStringBytes:  1024,
}

// RuleConfigError is the structured error boundary for the complete rule
// configuration. Path uses the same JSONPath-like convention as the
// component JSON compilers (for example $.prefilter.bitmap.expr).
type RuleConfigError struct {
	Phase string
	Path  string
	Code  string
	Err   error
}

func (e *RuleConfigError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Path == "" {
		return fmt.Sprintf("rule config %s [%s]: %v", e.Phase, e.Code, e.Err)
	}
	return fmt.Sprintf("rule config %s at %s [%s]: %v", e.Phase, e.Path, e.Code, e.Err)
}

func (e *RuleConfigError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func ruleJSONError(path, code, format string, args ...any) error {
	return &RuleConfigError{Phase: "json", Path: path, Code: code, Err: fmt.Errorf(format, args...)}
}

func ruleCompileError(path, code, format string, args ...any) error {
	return &RuleConfigError{Phase: "compile", Path: path, Code: code, Err: fmt.Errorf(format, args...)}
}

// CompiledRuleConfig contains the immutable products of compiling one
// match-rule/v1 document. The fields remain package-private so LogicalNode
// can consume the products without exposing component implementation details
// to callers in other packages.
type CompiledRuleConfig struct {
	ruleKey     identity.RuleKey
	contract    contract.Contract
	plan        *prefilter.Plan
	evaluation  evaluation.Predicates
	scorer      CandidateScorer
	seedPolicy  SeedOrderRuntime
	config      logicalNodeConfig
	fingerprint string
}

// RuleKey returns this rule's complete namespace-qualified identity.
func (c *CompiledRuleConfig) RuleKey() identity.RuleKey {
	if c == nil {
		return identity.RuleKey{}
	}
	return c.ruleKey
}

// Contract returns an owned defensive copy of the Contract used by all
// component compilers. Mutating the returned value cannot change this
// compiled configuration.
func (c *CompiledRuleConfig) Contract() contract.Contract {
	if c == nil {
		return contract.Contract{}
	}
	return c.contract.Clone()
}

// Fingerprint returns the SHA-256 hex digest of the canonicalized complete
// input document. It includes every top-level and embedded configuration
// field, including scorer, seed selection, and runtime settings.
func (c *CompiledRuleConfig) Fingerprint() string {
	if c == nil {
		return ""
	}
	return c.fingerprint
}

type ruleJSONEnvelope struct {
	schemaVersion string
	ruleKey       identity.RuleKey
	contract      json.RawMessage
	prefilter     json.RawMessage
	evaluation    json.RawMessage
	scoring       CandidateScorerConfig
	seedSelection SeedOrderPolicyConfig
	runtime       ruleRuntimeConfig
}

type ruleRuntimeConfig struct {
	candidateScoringLimitPerSeed int
	candidateLimitPerSeed        int
	maxPlayers                   int
	attemptLimitPerProduceMatch  int
	attemptLimitPerMatchRound    int
}

// CompileRuleJSON validates and compiles one complete match-rule/v1
// configuration. Structural validation is performed at the aggregate
// boundary before any embedded compiler is invoked, so duplicate keys,
// trailing values, and null values cannot bypass a RawMessage section.
func CompileRuleJSON(data []byte) (*CompiledRuleConfig, error) {
	if err := validateRuleJSONStructure(data); err != nil {
		return nil, err
	}

	canonical, err := canonicalRuleJSON(data)
	if err != nil {
		return nil, err
	}
	fingerprintBytes := sha256.Sum256(canonical)

	object, err := decodeRuleJSONObject(data, "$")
	if err != nil {
		return nil, err
	}
	rawVersion, ok := object["schemaVersion"]
	if !ok {
		return nil, ruleJSONError("$.schemaVersion", "UNKNOWN_SCHEMA_VERSION", "schemaVersion is required")
	}
	version, err := decodeRuleString(rawVersion, "$.schemaVersion")
	if err != nil {
		return nil, err
	}
	// Gate the aggregate version before interpreting its current shape, just
	// like the embedded Contract/Prefilter/Evaluation compilers do.
	if version != RuleJSONSchemaVersion {
		return nil, ruleJSONError("$.schemaVersion", "UNKNOWN_SCHEMA_VERSION",
			"unsupported schemaVersion %q", version)
	}
	if err := checkRuleJSONFields(object, "$",
		"schemaVersion", "ruleKey", "contract", "prefilter", "evaluation",
		"scoring", "seedSelection", "runtime"); err != nil {
		return nil, err
	}

	envelope, err := parseRuleEnvelope(object)
	if err != nil {
		return nil, err
	}

	schema, err := contract.Parse(envelope.contract, contract.DefaultLimits())
	if err != nil {
		return nil, adaptRuleConfigError(err, "$.contract", "INVALID_CONTRACT")
	}
	if envelope.scoring.Type == CandidateScorerInt64Field {
		field, fieldErr := scoringInt64Field(envelope.scoring.Params)
		if fieldErr != nil {
			return nil, fieldErr
		}
		if fieldErr = requireInt64RuleAttribute(schema, field, "$.scoring.params.field", "scoring"); fieldErr != nil {
			return nil, fieldErr
		}
	}
	if envelope.seedSelection.Kind == SeedOrderInt64Priority {
		if err := requireInt64RuleAttribute(schema, envelope.seedSelection.PriorityField, "$.seedSelection.params.field", "seed priority"); err != nil {
			return nil, err
		}
	}

	plan, err := prefilter.CompileJSON(envelope.prefilter, schema)
	if err != nil {
		return nil, adaptRuleConfigError(err, "$.prefilter", "INVALID_PREFILTER")
	}

	predicates, err := evaluation.CompileJSON(envelope.evaluation, schema)
	if err != nil {
		return nil, adaptRuleConfigError(err, "$.evaluation", "INVALID_EVALUATION")
	}

	scorer, err := NewCandidateScorer(envelope.scoring, schema)
	if err != nil {
		return nil, adaptRuleConfigError(err, "$.scoring", "INVALID_SCORER")
	}

	seedPolicy, err := NewSeedOrderPolicy(envelope.seedSelection)
	if err != nil {
		return nil, adaptRuleConfigError(err, "$.seedSelection", "INVALID_SEED_SELECTION")
	}

	config := logicalNodeConfig{
		CandidateScoringLimitPerSeed: envelope.runtime.candidateScoringLimitPerSeed,
		CandidateLimitPerSeed:        envelope.runtime.candidateLimitPerSeed,
		MaxPlayers:                   envelope.runtime.maxPlayers,
		SeedScheduler: seedSchedulerConfig{
			AttemptLimitPerProduceMatch: envelope.runtime.attemptLimitPerProduceMatch,
			AttemptLimitPerMatchRound:   envelope.runtime.attemptLimitPerMatchRound,
		},
	}

	return &CompiledRuleConfig{
		ruleKey:     envelope.ruleKey,
		contract:    schema.Clone(),
		plan:        plan,
		evaluation:  predicates,
		scorer:      scorer,
		seedPolicy:  seedPolicy,
		config:      config,
		fingerprint: hex.EncodeToString(fingerprintBytes[:]),
	}, nil
}

func validateRuleJSONStructure(data []byte) error {
	if err := jsonstrict.ValidateWithOptions(data, defaultRuleJSONLimits); err != nil {
		var structural *jsonstrict.Error
		if errors.As(err, &structural) {
			return &RuleConfigError{Phase: "json", Path: structural.Path, Code: structural.Code, Err: structural.Err}
		}
		return &RuleConfigError{Phase: "json", Path: "$", Code: "INVALID_JSON", Err: err}
	}
	return nil
}

func canonicalRuleJSON(data []byte) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, ruleJSONError("$", "INVALID_JSON", "%v", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, ruleJSONError("$", "TRAILING_JSON", "a second JSON value is not allowed")
		}
		return nil, ruleJSONError("$", "INVALID_JSON", "%v", err)
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return nil, ruleJSONError("$", "INVALID_JSON", "cannot canonicalize JSON: %v", err)
	}
	return canonical, nil
}

func parseRuleEnvelope(object map[string]json.RawMessage) (ruleJSONEnvelope, error) {
	var envelope ruleJSONEnvelope
	version, err := requiredRuleString(object, "schemaVersion", "$.schemaVersion")
	if err != nil {
		return envelope, err
	}
	envelope.schemaVersion = version

	rawRuleKey, err := requiredRuleField(object, "ruleKey", "$.ruleKey")
	if err != nil {
		return envelope, err
	}
	ruleKeyObject, err := decodeRuleJSONObject(rawRuleKey, "$.ruleKey")
	if err != nil {
		return envelope, err
	}
	if err := checkRuleJSONFields(ruleKeyObject, "$.ruleKey", "namespace", "ruleId"); err != nil {
		return envelope, err
	}
	namespace, err := optionalRuleString(ruleKeyObject, "namespace", "$.ruleKey.namespace")
	if err != nil {
		return envelope, err
	}
	ruleID, err := requiredRuleInt32(ruleKeyObject, "ruleId", "$.ruleKey.ruleId")
	if err != nil {
		return envelope, err
	}
	envelope.ruleKey = identity.RuleKey{Namespace: namespace, RuleID: ruleID}
	if err := envelope.ruleKey.Validate(); err != nil {
		return envelope, ruleJSONError("$.ruleKey.ruleId", "INVALID_RULE_ID", "%v", err)
	}

	for _, field := range []string{"contract", "prefilter", "evaluation", "scoring", "seedSelection", "runtime"} {
		raw, fieldErr := requiredRuleField(object, field, "$."+field)
		if fieldErr != nil {
			return envelope, fieldErr
		}
		switch field {
		case "contract":
			if _, fieldErr = decodeRuleJSONObject(raw, "$.contract"); fieldErr != nil {
				return envelope, fieldErr
			}
			envelope.contract = cloneRawMessage(raw)
		case "prefilter":
			if _, fieldErr = decodeRuleJSONObject(raw, "$.prefilter"); fieldErr != nil {
				return envelope, fieldErr
			}
			envelope.prefilter = cloneRawMessage(raw)
		case "evaluation":
			if _, fieldErr = decodeRuleJSONObject(raw, "$.evaluation"); fieldErr != nil {
				return envelope, fieldErr
			}
			envelope.evaluation = cloneRawMessage(raw)
		case "scoring":
			envelope.scoring, fieldErr = parseScoringConfig(raw)
			if fieldErr != nil {
				return envelope, fieldErr
			}
		case "seedSelection":
			envelope.seedSelection, fieldErr = parseSeedSelectionConfig(raw, nil)
			if fieldErr != nil {
				return envelope, fieldErr
			}
		case "runtime":
			envelope.runtime, fieldErr = parseRuleRuntimeConfig(raw)
			if fieldErr != nil {
				return envelope, fieldErr
			}
		}
	}
	return envelope, nil
}

func parseScoringConfig(raw json.RawMessage) (CandidateScorerConfig, error) {
	object, err := decodeRuleJSONObject(raw, "$.scoring")
	if err != nil {
		return CandidateScorerConfig{}, err
	}
	if err := checkRuleJSONFields(object, "$.scoring", "type", "params"); err != nil {
		return CandidateScorerConfig{}, err
	}
	typeValue, err := requiredRuleString(object, "type", "$.scoring.type")
	if err != nil {
		return CandidateScorerConfig{}, err
	}
	paramsRaw, err := requiredRuleField(object, "params", "$.scoring.params")
	if err != nil {
		return CandidateScorerConfig{}, err
	}
	if _, err := decodeRuleJSONObject(paramsRaw, "$.scoring.params"); err != nil {
		return CandidateScorerConfig{}, err
	}
	if err := validateScoringParams(typeValue, paramsRaw); err != nil {
		return CandidateScorerConfig{}, err
	}
	return CandidateScorerConfig{Type: CandidateScorerType(typeValue), Params: cloneRawMessage(paramsRaw)}, nil
}

func validateScoringParams(typeValue string, raw json.RawMessage) error {
	object, err := decodeRuleJSONObject(raw, "$.scoring.params")
	if err != nil {
		return err
	}
	var fields []string
	var required []string
	switch CandidateScorerType(typeValue) {
	case CandidateScorerConstant:
		fields, required = []string{"value"}, []string{"value"}
	case CandidateScorerCreatedAt:
		fields, required = []string{"direction", "weight"}, []string{"direction"}
	case CandidateScorerInt64Field:
		fields, required = []string{"field", "direction", "weight", "missingScore"}, []string{"field", "direction"}
	default:
		return ruleJSONError("$.scoring.type", "UNKNOWN_SCORER_TYPE", "unsupported scorer type %q", typeValue)
	}
	if err := checkRuleJSONFields(object, "$.scoring.params", fields...); err != nil {
		return err
	}
	for _, field := range required {
		if _, err := requiredRuleField(object, field, "$.scoring.params."+field); err != nil {
			return err
		}
	}
	if rawDirection, ok := object["direction"]; ok {
		direction, err := decodeRuleString(rawDirection, "$.scoring.params.direction")
		if err != nil {
			return err
		}
		if direction != "ascending" && direction != "descending" {
			return ruleJSONError("$.scoring.params.direction", "INVALID_VALUE", "direction must be %q or %q", "ascending", "descending")
		}
	}
	if rawField, ok := object["field"]; ok {
		field, err := decodeRuleString(rawField, "$.scoring.params.field")
		if err != nil {
			return err
		}
		if field == "" {
			return ruleJSONError("$.scoring.params.field", "INVALID_VALUE", "field must not be empty")
		}
	}
	for _, field := range []string{"value", "weight", "missingScore"} {
		if rawNumber, ok := object[field]; ok {
			value, err := decodeRuleFloat(rawNumber, "$.scoring.params."+field)
			if err != nil {
				return err
			}
			if math.IsNaN(value) || math.IsInf(value, 0) {
				return ruleJSONError("$.scoring.params."+field, "INVALID_VALUE", "number must be finite")
			}
			if field == "weight" && (value <= 0 || value > candidateScorerMaxWeight) {
				return ruleJSONError("$.scoring.params.weight", "INVALID_VALUE", "weight must be greater than zero and at most %g", candidateScorerMaxWeight)
			}
		}
	}
	return nil
}

func parseSeedSelectionConfig(raw json.RawMessage, schema *contract.Contract) (SeedOrderPolicyConfig, error) {
	object, err := decodeRuleJSONObject(raw, "$.seedSelection")
	if err != nil {
		return SeedOrderPolicyConfig{}, err
	}
	if err := checkRuleJSONFields(object, "$.seedSelection", "type", "params"); err != nil {
		return SeedOrderPolicyConfig{}, err
	}
	typeValue, err := requiredRuleString(object, "type", "$.seedSelection.type")
	if err != nil {
		return SeedOrderPolicyConfig{}, err
	}
	paramsRaw, err := requiredRuleField(object, "params", "$.seedSelection.params")
	if err != nil {
		return SeedOrderPolicyConfig{}, err
	}
	params, err := decodeRuleJSONObject(paramsRaw, "$.seedSelection.params")
	if err != nil {
		return SeedOrderPolicyConfig{}, err
	}

	config := SeedOrderPolicyConfig{Kind: SeedOrderPolicyKind(typeValue)}
	switch config.Kind {
	case SeedOrderArrival, SeedOrderOldest:
		if err := checkRuleJSONFields(params, "$.seedSelection.params"); err != nil {
			return SeedOrderPolicyConfig{}, err
		}
	case SeedOrderInt64Priority:
		if err := checkRuleJSONFields(params, "$.seedSelection.params", "field", "direction"); err != nil {
			return SeedOrderPolicyConfig{}, err
		}
		field, fieldErr := requiredRuleString(params, "field", "$.seedSelection.params.field")
		if fieldErr != nil {
			return SeedOrderPolicyConfig{}, fieldErr
		}
		if field == "" {
			return SeedOrderPolicyConfig{}, ruleJSONError("$.seedSelection.params.field", "INVALID_VALUE", "field must not be empty")
		}
		direction, directionErr := requiredRuleString(params, "direction", "$.seedSelection.params.direction")
		if directionErr != nil {
			return SeedOrderPolicyConfig{}, directionErr
		}
		if direction != string(SeedPriorityAscending) && direction != string(SeedPriorityDescending) {
			return SeedOrderPolicyConfig{}, ruleJSONError("$.seedSelection.params.direction", "INVALID_VALUE", "direction must be %q or %q", SeedPriorityAscending, SeedPriorityDescending)
		}
		config.PriorityField = field
		config.PriorityDirection = SeedPriorityDirection(direction)
		if schema != nil {
			if err := requireInt64RuleAttribute(*schema, field, "$.seedSelection.params.field", "seed priority"); err != nil {
				return SeedOrderPolicyConfig{}, err
			}
		}
	case SeedOrderRandom:
		if err := checkRuleJSONFields(params, "$.seedSelection.params", "randomSeed"); err != nil {
			return SeedOrderPolicyConfig{}, err
		}
		seed, seedErr := requiredRuleInt64(params, "randomSeed", "$.seedSelection.params.randomSeed")
		if seedErr != nil {
			return SeedOrderPolicyConfig{}, seedErr
		}
		config.RandomSeed = seed
	default:
		return SeedOrderPolicyConfig{}, ruleJSONError("$.seedSelection.type", "UNKNOWN_SEED_SELECTION_TYPE", "unsupported seed selection type %q", typeValue)
	}
	return config, nil
}

func requireInt64RuleAttribute(schema contract.Contract, name, path, purpose string) error {
	for _, attribute := range schema.Attributes {
		if attribute.Name != name {
			continue
		}
		if attribute.Type != contract.FactTypeInt64 {
			return ruleCompileError(path, "ATTRIBUTE_TYPE_MISMATCH", "%s field %q must be an int64 attribute", purpose, name)
		}
		return nil
	}
	return ruleCompileError(path, "ATTRIBUTE_NOT_FOUND", "%s field %q is not a declared attribute", purpose, name)
}

func scoringInt64Field(raw json.RawMessage) (string, error) {
	object, err := decodeRuleJSONObject(raw, "$.scoring.params")
	if err != nil {
		return "", err
	}
	return requiredRuleString(object, "field", "$.scoring.params.field")
}

func parseRuleRuntimeConfig(raw json.RawMessage) (ruleRuntimeConfig, error) {
	object, err := decodeRuleJSONObject(raw, "$.runtime")
	if err != nil {
		return ruleRuntimeConfig{}, err
	}
	if err := checkRuleJSONFields(object, "$.runtime", "candidateScoringLimitPerSeed", "candidateLimitPerSeed", "maxPlayers", "attemptLimitPerProduceMatch", "attemptLimitPerMatchRound"); err != nil {
		return ruleRuntimeConfig{}, err
	}
	var config ruleRuntimeConfig
	// Candidate limits are optional so existing match-rule/v1 documents remain
	// loadable.  An omitted field uses the current bounded-ranking baseline;
	// an explicitly supplied zero (or negative value) is still rejected as an
	// invalid configuration rather than silently turning the limit off.
	candidateScoringLimit, present, fieldErr := optionalRuleInt(object, "candidateScoringLimitPerSeed", "$.runtime.candidateScoringLimitPerSeed")
	if fieldErr != nil {
		return ruleRuntimeConfig{}, fieldErr
	}
	if !present {
		candidateScoringLimit = defaultCandidateScoringLimitPerSeed
	}
	if candidateScoringLimit <= 0 {
		return ruleRuntimeConfig{}, ruleJSONError("$.runtime.candidateScoringLimitPerSeed", "INVALID_VALUE", "value must be greater than zero")
	}
	config.candidateScoringLimitPerSeed = candidateScoringLimit

	candidateLimit, present, fieldErr := optionalRuleInt(object, "candidateLimitPerSeed", "$.runtime.candidateLimitPerSeed")
	if fieldErr != nil {
		return ruleRuntimeConfig{}, fieldErr
	}
	if !present {
		candidateLimit = defaultCandidateLimitPerSeed
	}
	if candidateLimit <= 0 {
		return ruleRuntimeConfig{}, ruleJSONError("$.runtime.candidateLimitPerSeed", "INVALID_VALUE", "value must be greater than zero")
	}
	config.candidateLimitPerSeed = candidateLimit
	fields := []struct {
		name   string
		target *int
	}{
		{"maxPlayers", &config.maxPlayers},
		{"attemptLimitPerProduceMatch", &config.attemptLimitPerProduceMatch},
		{"attemptLimitPerMatchRound", &config.attemptLimitPerMatchRound},
	}
	for _, field := range fields {
		value, fieldErr := requiredRuleInt(object, field.name, "$.runtime."+field.name)
		if fieldErr != nil {
			return ruleRuntimeConfig{}, fieldErr
		}
		if value <= 0 {
			return ruleRuntimeConfig{}, ruleJSONError("$.runtime."+field.name, "INVALID_VALUE", "value must be greater than zero")
		}
		*field.target = value
	}
	if config.attemptLimitPerProduceMatch > config.attemptLimitPerMatchRound {
		return ruleRuntimeConfig{}, ruleJSONError("$.runtime.attemptLimitPerProduceMatch", "INVALID_VALUE", "attemptLimitPerProduceMatch must not exceed attemptLimitPerMatchRound")
	}
	return config, nil
}

func decodeRuleJSONObject(raw []byte, path string) (map[string]json.RawMessage, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, ruleJSONError(path, "MISSING_FIELD", "JSON object is required")
	}
	var object map[string]json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.UseNumber()
	if err := decoder.Decode(&object); err != nil {
		return nil, ruleJSONError(path, "TYPE_MISMATCH", "object is required")
	}
	if object == nil {
		return nil, ruleJSONError(path, "TYPE_MISMATCH", "object is required")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, ruleJSONError(path, "TRAILING_JSON", "a second JSON value is not allowed")
		}
		return nil, ruleJSONError(path, "INVALID_JSON", "%v", err)
	}
	return object, nil
}

func checkRuleJSONFields(object map[string]json.RawMessage, path string, allowed ...string) error {
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, field := range allowed {
		allowedSet[field] = struct{}{}
	}
	unknown := make([]string, 0)
	for field := range object {
		if _, ok := allowedSet[field]; !ok {
			unknown = append(unknown, field)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	sort.Strings(unknown)
	return ruleJSONError(path+"."+unknown[0], "UNKNOWN_FIELD", "unknown field %q", unknown[0])
}

func requiredRuleField(object map[string]json.RawMessage, name, path string) (json.RawMessage, error) {
	raw, ok := object[name]
	if !ok {
		return nil, ruleJSONError(path, "MISSING_FIELD", "%s is required", name)
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, ruleJSONError(path, "NULL_NOT_ALLOWED", "%s must not be null", name)
	}
	return raw, nil
}

func requiredRuleString(object map[string]json.RawMessage, name, path string) (string, error) {
	raw, err := requiredRuleField(object, name, path)
	if err != nil {
		return "", err
	}
	return decodeRuleString(raw, path)
}

func optionalRuleString(object map[string]json.RawMessage, name, path string) (string, error) {
	raw, ok := object[name]
	if !ok {
		return "", nil
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return "", ruleJSONError(path, "NULL_NOT_ALLOWED", "%s must not be null", name)
	}
	return decodeRuleString(raw, path)
}

func decodeRuleString(raw []byte, path string) (string, error) {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", ruleJSONError(path, "TYPE_MISMATCH", "string is required")
	}
	return value, nil
}

func requiredRuleInt(object map[string]json.RawMessage, name, path string) (int, error) {
	raw, err := requiredRuleField(object, name, path)
	if err != nil {
		return 0, err
	}
	var value int
	if err := json.Unmarshal(raw, &value); err != nil {
		return 0, ruleJSONError(path, "TYPE_MISMATCH", "integer is required")
	}
	return value, nil
}

func optionalRuleInt(object map[string]json.RawMessage, name, path string) (int, bool, error) {
	raw, ok := object[name]
	if !ok {
		return 0, false, nil
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return 0, true, ruleJSONError(path, "NULL_NOT_ALLOWED", "%s must not be null", name)
	}
	var value int
	if err := json.Unmarshal(raw, &value); err != nil {
		return 0, true, ruleJSONError(path, "TYPE_MISMATCH", "integer is required")
	}
	return value, true, nil
}

func requiredRuleInt32(object map[string]json.RawMessage, name, path string) (int32, error) {
	raw, err := requiredRuleField(object, name, path)
	if err != nil {
		return 0, err
	}
	var value int32
	if err := json.Unmarshal(raw, &value); err != nil {
		return 0, ruleJSONError(path, "TYPE_MISMATCH", "integer is required")
	}
	return value, nil
}

func requiredRuleInt64(object map[string]json.RawMessage, name, path string) (int64, error) {
	raw, err := requiredRuleField(object, name, path)
	if err != nil {
		return 0, err
	}
	var value int64
	if err := json.Unmarshal(raw, &value); err != nil {
		return 0, ruleJSONError(path, "TYPE_MISMATCH", "integer is required")
	}
	return value, nil
}

func decodeRuleFloat(raw []byte, path string) (float64, error) {
	var value float64
	if err := json.Unmarshal(raw, &value); err != nil {
		return 0, ruleJSONError(path, "TYPE_MISMATCH", "number is required")
	}
	return value, nil
}

func cloneRawMessage(raw json.RawMessage) json.RawMessage {
	return append(json.RawMessage(nil), raw...)
}

func adaptRuleConfigError(err error, prefix, fallbackCode string) error {
	if err == nil {
		return nil
	}
	var ruleErr *RuleConfigError
	if errors.As(err, &ruleErr) {
		copy := *ruleErr
		copy.Path = prefixRuleConfigPath(prefix, copy.Path)
		return &copy
	}
	var contractErr *contract.Error
	if errors.As(err, &contractErr) {
		return &RuleConfigError{Phase: contractErr.Phase, Path: prefixRuleConfigPath(prefix, contractErr.Path), Code: contractErr.Code, Err: contractErr.Err}
	}
	var prefilterErr *prefilter.Error
	if errors.As(err, &prefilterErr) {
		return &RuleConfigError{Phase: prefilterErr.Phase, Path: prefixRuleConfigPath(prefix, prefilterErr.Path), Code: prefilterErr.Code, Err: prefilterErr.Err}
	}
	var evaluationErr *evaluation.Error
	if errors.As(err, &evaluationErr) {
		return &RuleConfigError{Phase: evaluationErr.Phase, Path: prefixRuleConfigPath(prefix, evaluationErr.Path), Code: evaluationErr.Code, Err: evaluationErr.Err}
	}
	return &RuleConfigError{Phase: "compile", Path: prefix, Code: fallbackCode, Err: err}
}

func prefixRuleConfigPath(prefix, child string) string {
	if prefix == "" {
		return child
	}
	if child == "" || child == "$" {
		return prefix
	}
	if strings.HasPrefix(child, "$") {
		return prefix + child[1:]
	}
	if strings.HasPrefix(child, ".") {
		return prefix + child
	}
	return prefix + "." + child
}
