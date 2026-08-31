// Package contract implements the sole logical-node-contract/v3 declaration.
package contract

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"

	"matchSystem/internal/matchsystem/expression"
	"matchSystem/internal/matchsystem/fact"
	"matchSystem/internal/matchsystem/jsonstrict"
)

const SchemaVersion = "logical-node-contract/v3"

const (
	ScopeTick   = fact.ScopeTick
	ScopeObject = fact.ScopeObject
	ScopeMatch  = fact.ScopeMatch
)

type FactType = fact.Type

const (
	FactTypeStrings = fact.TypeStrings
	FactTypeInt64   = fact.TypeInt64
	FactTypeUint64s = fact.TypeUint64s
)

type FactSpec = fact.Spec
type AttributeSpec = expression.AttributeSpec

type IndexType string
type KeyType string

const (
	IndexTypeMultiValue IndexType = "multi_value"
	IndexTypeInt64Range IndexType = "int64_range"
	KeyTypeString       KeyType   = "string"
	KeyTypeUint64       KeyType   = "uint64"
)

// IndexSpec is the JSON-neutral declaration of one Prefilter index. Name is
// simultaneously the declared Attribute name, physical index identifier and
// Prefilter query reference.
type IndexSpec struct {
	Type              IndexType
	Name              string
	KeyType           KeyType
	MaxDocumentValues int
	MaxQueryValues    int
}

type Limits struct {
	MaxBytes          int `json:"maxBytes"`
	MaxDepth          int `json:"maxDepth"`
	MaxChildren       int `json:"maxChildren"`
	MaxStringBytes    int `json:"maxStringBytes"`
	MaxIndexes        int `json:"maxIndexes"`
	MaxAttributes     int `json:"maxAttributes"`
	MaxFacts          int `json:"maxFacts"`
	MaxValues         int `json:"maxValues"`
	MaxDocumentValues int `json:"maxDocumentValues"`
	MaxQueryValues    int `json:"maxQueryValues"`
}

func DefaultLimits() Limits {
	return Limits{
		MaxBytes:          1 << 20,
		MaxDepth:          64,
		MaxChildren:       128,
		MaxStringBytes:    1024,
		MaxIndexes:        128,
		MaxAttributes:     256,
		MaxFacts:          256,
		MaxValues:         10000,
		MaxDocumentValues: 256,
		MaxQueryValues:    256,
	}
}

func normalizeLimits(limits Limits) Limits {
	defaults := DefaultLimits()
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
	if limits.MaxIndexes == 0 {
		limits.MaxIndexes = defaults.MaxIndexes
	}
	if limits.MaxAttributes == 0 {
		limits.MaxAttributes = defaults.MaxAttributes
	}
	if limits.MaxFacts == 0 {
		limits.MaxFacts = defaults.MaxFacts
	}
	if limits.MaxValues == 0 {
		limits.MaxValues = defaults.MaxValues
	}
	if limits.MaxDocumentValues == 0 {
		limits.MaxDocumentValues = defaults.MaxDocumentValues
	}
	if limits.MaxQueryValues == 0 {
		limits.MaxQueryValues = defaults.MaxQueryValues
	}
	return limits
}

func validateLimits(limits Limits) error {
	for name, value := range map[string]int{
		"maxBytes": limits.MaxBytes, "maxDepth": limits.MaxDepth,
		"maxChildren": limits.MaxChildren, "maxStringBytes": limits.MaxStringBytes,
		"maxIndexes": limits.MaxIndexes, "maxAttributes": limits.MaxAttributes,
		"maxFacts": limits.MaxFacts, "maxValues": limits.MaxValues,
		"maxDocumentValues": limits.MaxDocumentValues, "maxQueryValues": limits.MaxQueryValues,
	} {
		if value < 0 {
			return jsonError("$.limits."+name, "INVALID_LIMIT", "limit must not be negative")
		}
	}
	return nil
}

// Contract is immutable after Parse/Validate and can be used to construct a
// Prefilter config and expression CompileOptions without exposing mutable maps.
type Contract struct {
	Attributes []AttributeSpec
	Facts      []FactSpec
	Indexes    []IndexSpec
	Limits     Limits
}

func (c Contract) Validate() error {
	limits := normalizeLimits(c.Limits)
	if err := validateLimits(c.Limits); err != nil {
		return err
	}
	if len(c.Attributes) > limits.MaxAttributes {
		return compileError("attributes", "ATTRIBUTE_LIMIT", "too many attributes")
	}
	if len(c.Facts) > limits.MaxFacts {
		return compileError("facts", "FACT_LIMIT", "too many Facts")
	}
	if len(c.Indexes) > limits.MaxIndexes {
		return compileError("indexes", "INDEX_LIMIT", "too many indexes")
	}
	attributes := make(map[string]AttributeSpec, len(c.Attributes))
	facts := make(map[string]struct{}, len(c.Facts))
	indexes := make(map[string]struct{}, len(c.Indexes))
	for index, attribute := range c.Attributes {
		path := fmt.Sprintf("attributes[%d]", index)
		if err := validateName(path+".name", attribute.Name, limits); err != nil {
			return err
		}
		if err := validateValueType(path+".type", attribute.Type); err != nil {
			return err
		}
		if attribute.Type == fact.TypeInt64 {
			if attribute.MaxValues != 0 {
				return compileError(path+".maxValues", "INVALID_ATTRIBUTE_LIMIT", "int64 attribute must not declare maxValues")
			}
		} else if attribute.MaxValues <= 0 || attribute.MaxValues > limits.MaxValues {
			return compileError(path+".maxValues", "INVALID_ATTRIBUTE_LIMIT", "multi-value attribute maxValues is outside limits")
		}
		if _, exists := attributes[attribute.Name]; exists {
			return compileError(path+".name", "DUPLICATE_ATTRIBUTE", "attribute %q is duplicated", attribute.Name)
		}
		if _, exists := facts[attribute.Name]; exists {
			return compileError(path+".name", "DUPLICATE_NAME", "attribute %q collides with a Fact", attribute.Name)
		}
		attributes[attribute.Name] = attribute
	}
	for index, spec := range c.Facts {
		path := fmt.Sprintf("facts[%d]", index)
		if err := validateName(path+".name", spec.Name, limits); err != nil {
			return err
		}
		if err := validateDescription(path+".description", spec.Description, limits); err != nil {
			return err
		}
		if err := validateValueType(path+".type", spec.Type); err != nil {
			return err
		}
		if spec.Scope != fact.ScopeTick && spec.Scope != fact.ScopeObject && spec.Scope != fact.ScopeMatch {
			return compileError(path+".scope", "INVALID_FACT_SCOPE", "Fact scope must be tick, object, or match")
		}
		if spec.Type == fact.TypeInt64 {
			if spec.MaxValues != 0 {
				return compileError(path+".maxValues", "INVALID_FACT_LIMIT", "int64 Fact must not declare maxValues")
			}
		} else if spec.MaxValues <= 0 || spec.MaxValues > limits.MaxValues {
			return compileError(path+".maxValues", "INVALID_FACT_LIMIT", "multi-value Fact maxValues is outside limits")
		}
		if _, exists := facts[spec.Name]; exists {
			return compileError(path+".name", "DUPLICATE_FACT", "Fact %q is duplicated", spec.Name)
		}
		if _, exists := attributes[spec.Name]; exists {
			return compileError(path+".name", "DUPLICATE_NAME", "Fact %q collides with an attribute", spec.Name)
		}
		facts[spec.Name] = struct{}{}
	}
	for index, spec := range c.Indexes {
		path := fmt.Sprintf("indexes[%d]", index)
		if err := validateName(path+".name", spec.Name, limits); err != nil {
			return err
		}
		if _, exists := indexes[spec.Name]; exists {
			return compileError(path+".name", "DUPLICATE_INDEX", "index %q is duplicated", spec.Name)
		}
		indexes[spec.Name] = struct{}{}
		attribute, exists := attributes[spec.Name]
		if !exists {
			return compileError(path+".name", "MISSING_ATTRIBUTE", "name %q is not a declared Attribute", spec.Name)
		}
		switch spec.Type {
		case IndexTypeMultiValue:
			if spec.KeyType != KeyTypeString && spec.KeyType != KeyTypeUint64 {
				return compileError(path+".keyType", "INVALID_KEY_TYPE", "multi_value index keyType is invalid")
			}
			if spec.MaxDocumentValues <= 0 || spec.MaxDocumentValues > limits.MaxDocumentValues {
				return compileError(path+".maxDocumentValues", "INVALID_KEY_LIMIT", "maxDocumentValues is outside limits")
			}
			if spec.MaxQueryValues <= 0 || spec.MaxQueryValues > limits.MaxQueryValues {
				return compileError(path+".maxQueryValues", "INVALID_KEY_LIMIT", "maxQueryValues is outside limits")
			}
			want := fact.TypeStrings
			if spec.KeyType == KeyTypeUint64 {
				want = fact.TypeUint64s
			}
			if attribute.Type != want {
				return compileError(path+".name", "ATTRIBUTE_TYPE_MISMATCH", "index key type does not match attribute %q", spec.Name)
			}
		case IndexTypeInt64Range:
			if spec.KeyType != "" || spec.MaxDocumentValues != 0 || spec.MaxQueryValues != 0 {
				return compileError(path, "INVALID_INDEX_FIELD", "int64_range does not accept keyType or multi_value limits")
			}
			if attribute.Type != fact.TypeInt64 {
				return compileError(path+".name", "ATTRIBUTE_TYPE_MISMATCH", "int64_range index requires an int64 attribute")
			}
		default:
			return compileError(path+".type", "UNKNOWN_INDEX_TYPE", "unknown index type %q", spec.Type)
		}
	}
	return nil
}

func validateName(path, name string, limits Limits) error {
	if name == "" {
		return compileError(path, "EMPTY_NAME", "name is required")
	}
	if !utf8.ValidString(name) {
		return compileError(path, "INVALID_STRING", "name is not valid UTF-8")
	}
	if len(name) > limits.MaxStringBytes {
		return compileError(path, "STRING_LIMIT", "string exceeds %d bytes", limits.MaxStringBytes)
	}
	return nil
}

func validateDescription(path, description string, limits Limits) error {
	if !utf8.ValidString(description) {
		return compileError(path, "INVALID_STRING", "description is not valid UTF-8")
	}
	if len(description) > limits.MaxStringBytes {
		return compileError(path, "STRING_LIMIT", "description exceeds %d bytes", limits.MaxStringBytes)
	}
	return nil
}

func validateValueType(path string, value fact.Type) error {
	if value != fact.TypeStrings && value != fact.TypeInt64 && value != fact.TypeUint64s {
		return compileError(path, "UNKNOWN_TYPE", "unknown value type %d", value)
	}
	return nil
}

func (c Contract) FactSpecs() []fact.Spec { return append([]fact.Spec(nil), c.Facts...) }
func (c Contract) AttributeSpecs() []expression.AttributeSpec {
	return append([]expression.AttributeSpec(nil), c.Attributes...)
}
func (c Contract) Clone() Contract {
	return Contract{
		Attributes: append([]AttributeSpec(nil), c.Attributes...),
		Facts:      append([]FactSpec(nil), c.Facts...),
		Indexes:    append([]IndexSpec(nil), c.Indexes...),
		Limits:     c.Limits,
	}
}

func Parse(data []byte, limits Limits) (Contract, error) {
	if err := validateLimits(limits); err != nil {
		return Contract{}, err
	}
	limits = normalizeLimits(limits)
	if err := jsonstrict.ValidateWithOptions(data, jsonstrict.Options{
		MaxBytes: limits.MaxBytes, MaxDepth: limits.MaxDepth, MaxStringBytes: limits.MaxStringBytes,
	}); err != nil {
		return Contract{}, strictJSONError(err)
	}
	if err := rejectNullFields(data, "$", "schemaVersion", "attributes", "facts", "indexes", "limits"); err != nil {
		return Contract{}, err
	}
	// Gate the envelope version before decoding the complete DTO.  Besides
	// making legacy documents fail closed before their old fields are
	// interpreted, this gives an unversioned contract the same
	// UNKNOWN_SCHEMA_VERSION boundary as any other unsupported version.
	var rawEnvelope map[string]json.RawMessage
	if err := json.Unmarshal(data, &rawEnvelope); err != nil || rawEnvelope == nil {
		return Contract{}, jsonError("$", "INVALID_OBJECT", "contract envelope must be an object")
	}
	rawVersion, ok := rawEnvelope["schemaVersion"]
	if !ok {
		return Contract{}, jsonError("$.schemaVersion", "UNKNOWN_SCHEMA_VERSION", "schemaVersion is required")
	}
	var version string
	if err := json.Unmarshal(rawVersion, &version); err != nil {
		return Contract{}, jsonError("$.schemaVersion", "TYPE_MISMATCH", "string is required")
	}
	if version != SchemaVersion {
		return Contract{}, jsonError("$.schemaVersion", "UNKNOWN_SCHEMA_VERSION", "unsupported schemaVersion %q", version)
	}
	if err := validateContractEnvelopeTypes(rawEnvelope); err != nil {
		return Contract{}, err
	}
	var envelope struct {
		SchemaVersion string            `json:"schemaVersion"`
		Attributes    []json.RawMessage `json:"attributes"`
		Facts         []json.RawMessage `json:"facts"`
		Indexes       []json.RawMessage `json:"indexes"`
		Limits        *Limits           `json:"limits"`
	}
	if err := decodeStrict(data, &envelope); err != nil {
		return Contract{}, structureJSONError("$", err)
	}
	if envelope.Attributes == nil {
		return Contract{}, jsonError("$.attributes", "MISSING_FIELD", "attributes is required")
	}
	if envelope.Facts == nil {
		return Contract{}, jsonError("$.facts", "MISSING_FIELD", "facts is required")
	}
	if envelope.Indexes == nil {
		return Contract{}, jsonError("$.indexes", "MISSING_FIELD", "indexes is required")
	}
	if envelope.Limits != nil {
		limits = *envelope.Limits
		if err := validateLimits(limits); err != nil {
			return Contract{}, err
		}
		limits = normalizeLimits(limits)
	}
	if err := jsonstrict.ValidateWithOptions(data, jsonstrict.Options{
		MaxBytes: limits.MaxBytes, MaxDepth: limits.MaxDepth, MaxStringBytes: limits.MaxStringBytes,
	}); err != nil {
		return Contract{}, strictJSONError(err)
	}
	if len(envelope.Attributes) > limits.MaxAttributes {
		return Contract{}, jsonError("$.attributes", "ATTRIBUTE_LIMIT", "too many attributes")
	}
	if len(envelope.Facts) > limits.MaxFacts {
		return Contract{}, jsonError("$.facts", "FACT_LIMIT", "too many Facts")
	}
	if len(envelope.Indexes) > limits.MaxIndexes {
		return Contract{}, jsonError("$.indexes", "INDEX_LIMIT", "too many indexes")
	}
	contract := Contract{Limits: limits, Attributes: make([]AttributeSpec, len(envelope.Attributes)), Facts: make([]FactSpec, len(envelope.Facts)), Indexes: make([]IndexSpec, len(envelope.Indexes))}
	for i, raw := range envelope.Attributes {
		attribute, err := parseAttribute(raw, fmt.Sprintf("$.attributes[%d]", i), limits)
		if err != nil {
			return Contract{}, err
		}
		contract.Attributes[i] = attribute
	}
	for i, raw := range envelope.Facts {
		factSpec, err := parseFact(raw, fmt.Sprintf("$.facts[%d]", i), limits)
		if err != nil {
			return Contract{}, err
		}
		contract.Facts[i] = factSpec
	}
	for i, raw := range envelope.Indexes {
		indexSpec, err := parseIndex(raw, fmt.Sprintf("$.indexes[%d]", i), limits)
		if err != nil {
			return Contract{}, err
		}
		contract.Indexes[i] = indexSpec
	}
	if err := contract.Validate(); err != nil {
		return Contract{}, err
	}
	return contract, nil
}

func validateContractEnvelopeTypes(object map[string]json.RawMessage) error {
	for _, name := range []string{"attributes", "facts", "indexes"} {
		raw, ok := object[name]
		if !ok {
			continue
		}
		var values []json.RawMessage
		if err := json.Unmarshal(raw, &values); err != nil || values == nil {
			return jsonError("$."+name, "TYPE_MISMATCH", "array is required")
		}
	}
	if raw, ok := object["limits"]; ok {
		var values map[string]json.RawMessage
		if err := json.Unmarshal(raw, &values); err != nil || values == nil {
			return jsonError("$.limits", "TYPE_MISMATCH", "object is required")
		}
	}
	return nil
}

func parseAttribute(raw json.RawMessage, path string, limits Limits) (AttributeSpec, error) {
	if err := rejectNullFields(raw, path, "name", "type", "maxValues"); err != nil {
		return AttributeSpec{}, err
	}
	var dto struct {
		Name      string `json:"name"`
		Type      string `json:"type"`
		MaxValues *int   `json:"maxValues"`
	}
	if err := decodeStrict(raw, &dto); err != nil {
		return AttributeSpec{}, structureJSONError(path, err)
	}
	if err := validateName(path+".name", dto.Name, limits); err != nil {
		return AttributeSpec{}, err
	}
	typeValue, err := parseType(path+".type", dto.Type)
	if err != nil {
		return AttributeSpec{}, err
	}
	max := 0
	if dto.MaxValues != nil {
		max = *dto.MaxValues
	}
	if typeValue == fact.TypeInt64 && dto.MaxValues != nil {
		return AttributeSpec{}, jsonError(path+".maxValues", "INVALID_ATTRIBUTE_LIMIT", "int64 attribute must not declare maxValues")
	}
	if typeValue != fact.TypeInt64 && (dto.MaxValues == nil || max <= 0 || max > limits.MaxValues) {
		return AttributeSpec{}, jsonError(path+".maxValues", "INVALID_ATTRIBUTE_LIMIT", "multi-value attribute maxValues is required and bounded")
	}
	return AttributeSpec{Name: dto.Name, Type: typeValue, MaxValues: max}, nil
}

func parseFact(raw json.RawMessage, path string, limits Limits) (FactSpec, error) {
	if err := rejectNullFields(raw, path, "name", "type", "scope", "maxValues", "description"); err != nil {
		return FactSpec{}, err
	}
	var dto struct {
		Name        string `json:"name"`
		Type        string `json:"type"`
		Scope       string `json:"scope"`
		MaxValues   *int   `json:"maxValues"`
		Description string `json:"description"`
	}
	if err := decodeStrict(raw, &dto); err != nil {
		return FactSpec{}, structureJSONError(path, err)
	}
	if err := validateName(path+".name", dto.Name, limits); err != nil {
		return FactSpec{}, err
	}
	typeValue, err := parseType(path+".type", dto.Type)
	if err != nil {
		return FactSpec{}, err
	}
	var scope fact.Scope
	switch dto.Scope {
	case string(fact.ScopeTick):
		scope = fact.ScopeTick
	case string(fact.ScopeObject):
		scope = fact.ScopeObject
	case string(fact.ScopeMatch):
		scope = fact.ScopeMatch
	default:
		return FactSpec{}, jsonError(path+".scope", "INVALID_FACT_SCOPE", "scope must be tick, object, or match")
	}
	max := 0
	if dto.MaxValues != nil {
		max = *dto.MaxValues
	}
	if typeValue == fact.TypeInt64 && dto.MaxValues != nil {
		return FactSpec{}, jsonError(path+".maxValues", "INVALID_FACT_LIMIT", "int64 Fact must not declare maxValues")
	}
	if typeValue != fact.TypeInt64 && (dto.MaxValues == nil || max <= 0 || max > limits.MaxValues) {
		return FactSpec{}, jsonError(path+".maxValues", "INVALID_FACT_LIMIT", "multi-value Fact maxValues is required and bounded")
	}
	return FactSpec{Name: dto.Name, Type: typeValue, MaxValues: max, Scope: scope, Description: dto.Description}, nil
}

func parseType(path, value string) (fact.Type, error) {
	switch value {
	case "strings":
		return fact.TypeStrings, nil
	case "int64":
		return fact.TypeInt64, nil
	case "uint64s":
		return fact.TypeUint64s, nil
	case "":
		return 0, jsonError(path, "MISSING_FIELD", "type is required")
	default:
		return 0, jsonError(path, "UNKNOWN_TYPE", "unknown type %q", value)
	}
}

func parseIndex(raw json.RawMessage, path string, limits Limits) (IndexSpec, error) {
	if err := rejectNullFields(raw, path, "type", "name", "keyType", "maxDocumentValues", "maxQueryValues"); err != nil {
		return IndexSpec{}, err
	}
	var dto struct {
		Type              string `json:"type"`
		Name              string `json:"name"`
		KeyType           string `json:"keyType"`
		MaxDocumentValues *int   `json:"maxDocumentValues"`
		MaxQueryValues    *int   `json:"maxQueryValues"`
	}
	if err := decodeStrict(raw, &dto); err != nil {
		return IndexSpec{}, structureJSONError(path, err)
	}
	if err := validateName(path+".name", dto.Name, limits); err != nil {
		return IndexSpec{}, err
	}
	switch dto.Type {
	case string(IndexTypeMultiValue):
		if dto.KeyType != string(KeyTypeString) && dto.KeyType != string(KeyTypeUint64) {
			return IndexSpec{}, jsonError(path+".keyType", "INVALID_KEY_TYPE", "keyType must be string or uint64")
		}
		if dto.MaxDocumentValues == nil || dto.MaxQueryValues == nil || *dto.MaxDocumentValues <= 0 || *dto.MaxQueryValues <= 0 {
			return IndexSpec{}, jsonError(path, "INVALID_KEY_LIMIT", "multi_value limits are required")
		}
		if *dto.MaxDocumentValues > limits.MaxDocumentValues || *dto.MaxQueryValues > limits.MaxQueryValues {
			return IndexSpec{}, jsonError(path, "INVALID_KEY_LIMIT", "multi_value limits exceed contract limits")
		}
		return IndexSpec{Type: IndexTypeMultiValue, Name: dto.Name, KeyType: KeyType(dto.KeyType), MaxDocumentValues: *dto.MaxDocumentValues, MaxQueryValues: *dto.MaxQueryValues}, nil
	case string(IndexTypeInt64Range):
		if dto.KeyType != "" || dto.MaxDocumentValues != nil || dto.MaxQueryValues != nil {
			return IndexSpec{}, jsonError(path, "INVALID_INDEX_FIELD", "int64_range does not accept keyType or multi_value limits")
		}
		return IndexSpec{Type: IndexTypeInt64Range, Name: dto.Name}, nil
	case "":
		return IndexSpec{}, jsonError(path+".type", "MISSING_FIELD", "index type is required")
	default:
		return IndexSpec{}, jsonError(path+".type", "UNKNOWN_INDEX_TYPE", "unknown index type %q", dto.Type)
	}
}

func decodeStrict(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("trailing JSON value")
		}
		return err
	}
	return nil
}

func rejectNullFields(data []byte, path string, names ...string) error {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return jsonError(path, "INVALID_OBJECT", "%v", err)
	}
	for _, name := range names {
		value, exists := object[name]
		if exists && bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return jsonError(path+"."+name, "NULL_FIELD", "field must not be null")
		}
	}
	return nil
}

func strictJSONError(err error) error {
	var structural *jsonstrict.Error
	if errors.As(err, &structural) {
		return jsonError(structural.Path, structural.Code, "%v", structural.Err)
	}
	return jsonError("$", "INVALID_JSON", "%v", err)
}

func structureJSONError(path string, err error) error {
	message := err.Error()
	const prefix = "json: unknown field "
	if strings.HasPrefix(message, prefix) {
		field, unquoteErr := strconv.Unquote(strings.TrimPrefix(message, prefix))
		if unquoteErr == nil {
			return jsonError(path+"."+field, "UNKNOWN_FIELD", "%v", err)
		}
	}
	if strings.Contains(message, "trailing JSON value") || strings.Contains(message, "multiple JSON values") {
		return jsonError(path, "TRAILING_JSON", "%v", err)
	}
	return jsonError(path, "INVALID_OBJECT", "%v", err)
}

type Error struct {
	Phase, Path, Code string
	Err               error
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	return fmt.Sprintf("contract %s at %s [%s]: %v", e.Phase, e.Path, e.Code, e.Err)
}
func (e *Error) Unwrap() error { return e.Err }
func jsonError(path, code, format string, args ...any) error {
	return &Error{Phase: "json", Path: path, Code: code, Err: fmt.Errorf(format, args...)}
}
func compileError(path, code, format string, args ...any) error {
	return &Error{Phase: "compile", Path: path, Code: code, Err: fmt.Errorf(format, args...)}
}
