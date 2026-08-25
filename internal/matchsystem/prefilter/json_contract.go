package prefilter

import (
	"encoding/json"
	"fmt"
)

const LogicalNodeContractSchemaVersion = "logical-node-contract/v1"

// JSONContractSchemaVersion is kept as a compatibility alias.
const JSONContractSchemaVersion = LogicalNodeContractSchemaVersion

// ParseLogicalNodeContract strictly parses the node-wide Index/Fact catalog
// used before any Prefilter plan is designed. Contract JSON and plan JSON are
// separate documents with different schema versions and cannot be mixed.
func ParseLogicalNodeContract(data []byte, limits JSONLimits) (LogicalNodeContract, error) {
	if err := validateJSONLimits(limits); err != nil {
		return LogicalNodeContract{}, err
	}
	limits = normalizeJSONLimits(limits)
	if err := validateJSONInput(data, limits); err != nil {
		return LogicalNodeContract{}, err
	}

	var envelope struct {
		SchemaVersion string            `json:"schemaVersion"`
		Indexes       []json.RawMessage `json:"indexes"`
		Facts         []json.RawMessage `json:"facts"`
	}
	if err := decodeStrict(data, &envelope); err != nil {
		return LogicalNodeContract{}, structureError("$", err)
	}
	if envelope.SchemaVersion == "" {
		return LogicalNodeContract{}, jsonError("$.schemaVersion", "MISSING_FIELD", "schemaVersion is required")
	}
	if envelope.SchemaVersion != JSONContractSchemaVersion {
		return LogicalNodeContract{}, jsonError("$.schemaVersion", "UNKNOWN_SCHEMA_VERSION", "unsupported schemaVersion %q", envelope.SchemaVersion)
	}
	if envelope.Indexes == nil {
		return LogicalNodeContract{}, jsonError("$.indexes", "MISSING_FIELD", "indexes is required; use an empty array when no indexes are available")
	}
	if envelope.Facts == nil {
		return LogicalNodeContract{}, jsonError("$.facts", "MISSING_FIELD", "facts is required; use an empty array when no Facts are available")
	}
	if len(envelope.Indexes) > limits.MaxIndexes {
		return LogicalNodeContract{}, jsonError("$.indexes", "INDEX_LIMIT", "contract contains %d indexes; maximum is %d", len(envelope.Indexes), limits.MaxIndexes)
	}
	if len(envelope.Facts) > limits.MaxFacts {
		return LogicalNodeContract{}, jsonError("$.facts", "FACT_LIMIT", "contract contains %d Facts; maximum is %d", len(envelope.Facts), limits.MaxFacts)
	}

	contract := LogicalNodeContract{
		Indexes: make([]IndexSpec, len(envelope.Indexes)),
		Facts:   make([]FactSpec, len(envelope.Facts)),
		Limits:  limits,
	}
	indexNames := make(map[string]struct{}, len(envelope.Indexes))
	for i, raw := range envelope.Indexes {
		path := fmt.Sprintf("$.indexes[%d]", i)
		index, name, err := parseJSONIndexSpec(raw, path, limits)
		if err != nil {
			return LogicalNodeContract{}, err
		}
		if _, exists := indexNames[name]; exists {
			return LogicalNodeContract{}, jsonError(path+".name", "DUPLICATE_INDEX", "index %q is duplicated", name)
		}
		indexNames[name] = struct{}{}
		contract.Indexes[i] = index
	}
	factNames := make(map[string]struct{}, len(envelope.Facts))
	for i, raw := range envelope.Facts {
		path := fmt.Sprintf("$.facts[%d]", i)
		fact, err := parseJSONFactSpec(raw, path, limits)
		if err != nil {
			return LogicalNodeContract{}, err
		}
		if _, exists := factNames[fact.Name]; exists {
			return LogicalNodeContract{}, jsonError(path+".name", "DUPLICATE_FACT", "Fact %q is duplicated", fact.Name)
		}
		factNames[fact.Name] = struct{}{}
		contract.Facts[i] = fact
	}

	// Keep contract JSON and Go construction on the same validation path.
	if _, err := NewJSONCompiler(contract); err != nil {
		return LogicalNodeContract{}, err
	}
	return contract, nil
}

// ParseJSONContract is kept for compatibility. New code should use
// ParseLogicalNodeContract to make the node-wide scope explicit.
func ParseJSONContract(data []byte, limits JSONLimits) (LogicalNodeContract, error) {
	return ParseLogicalNodeContract(data, limits)
}

func parseJSONIndexSpec(raw json.RawMessage, path string, limits JSONLimits) (IndexSpec, string, error) {
	typeName, err := decodeType(raw, path)
	if err != nil {
		return nil, "", err
	}
	switch typeName {
	case "multi_value":
		var dto struct {
			Type              string  `json:"type"`
			Name              string  `json:"name"`
			Field             string  `json:"field"`
			KeyType           KeyType `json:"keyType"`
			MaxDocumentValues *int    `json:"maxDocumentValues"`
			MaxQueryValues    *int    `json:"maxQueryValues"`
		}
		if err := decodeAt(raw, path, &dto); err != nil {
			return nil, "", err
		}
		if err := requireJSONName(limits, path+".name", dto.Name); err != nil {
			return nil, "", err
		}
		if err := requireJSONName(limits, path+".field", dto.Field); err != nil {
			return nil, "", err
		}
		if dto.KeyType != KeyTypeString && dto.KeyType != KeyTypeUint64 {
			return nil, "", jsonError(path+".keyType", "INVALID_KEY_TYPE", "keyType must be %q or %q", KeyTypeString, KeyTypeUint64)
		}
		if dto.MaxDocumentValues == nil || *dto.MaxDocumentValues <= 0 {
			return nil, "", jsonError(path+".maxDocumentValues", "INVALID_KEY_LIMIT", "maxDocumentValues must be a positive integer")
		}
		if dto.MaxQueryValues == nil || *dto.MaxQueryValues <= 0 {
			return nil, "", jsonError(path+".maxQueryValues", "INVALID_KEY_LIMIT", "maxQueryValues must be a positive integer")
		}
		return NewMultiValueIndex(MultiValueIndexConfig{
			Name:              dto.Name,
			Field:             dto.Field,
			KeyType:           dto.KeyType,
			MaxDocumentValues: *dto.MaxDocumentValues,
			MaxQueryValues:    *dto.MaxQueryValues,
		}), dto.Name, nil
	case "int64_range":
		var dto struct {
			Type  string `json:"type"`
			Name  string `json:"name"`
			Field string `json:"field"`
		}
		if err := decodeAt(raw, path, &dto); err != nil {
			return nil, "", err
		}
		if err := requireJSONName(limits, path+".name", dto.Name); err != nil {
			return nil, "", err
		}
		if err := requireJSONName(limits, path+".field", dto.Field); err != nil {
			return nil, "", err
		}
		return NewInt64RangeIndex(Int64RangeIndexConfig{Name: dto.Name, Field: dto.Field}), dto.Name, nil
	default:
		return nil, "", jsonError(path+".type", "UNKNOWN_TYPE", "unknown index type %q", typeName)
	}
}

func parseJSONFactSpec(raw json.RawMessage, path string, limits JSONLimits) (FactSpec, error) {
	var dto struct {
		Name      string `json:"name"`
		Type      string `json:"type"`
		MaxValues *int   `json:"maxValues,omitempty"`
	}
	if err := decodeAt(raw, path, &dto); err != nil {
		return FactSpec{}, err
	}
	if err := requireJSONName(limits, path+".name", dto.Name); err != nil {
		return FactSpec{}, err
	}
	switch dto.Type {
	case "strings":
		if dto.MaxValues == nil || *dto.MaxValues <= 0 {
			return FactSpec{}, jsonError(path+".maxValues", "INVALID_FACT_LIMIT", "strings Fact requires a positive maxValues")
		}
		return FactSpec{Name: dto.Name, Type: FactTypeStrings, MaxValues: *dto.MaxValues}, nil
	case "uint64s":
		if dto.MaxValues == nil || *dto.MaxValues <= 0 {
			return FactSpec{}, jsonError(path+".maxValues", "INVALID_FACT_LIMIT", "uint64s Fact requires a positive maxValues")
		}
		return FactSpec{Name: dto.Name, Type: FactTypeUint64s, MaxValues: *dto.MaxValues}, nil
	case "int64":
		if dto.MaxValues != nil {
			return FactSpec{}, jsonError(path+".maxValues", "INVALID_FACT_LIMIT", "int64 Fact must not declare maxValues")
		}
		return FactSpec{Name: dto.Name, Type: FactTypeInt64}, nil
	case "":
		return FactSpec{}, jsonError(path+".type", "MISSING_FIELD", "Fact type is required")
	default:
		return FactSpec{}, jsonError(path+".type", "UNKNOWN_TYPE", "unknown Fact type %q", dto.Type)
	}
}
