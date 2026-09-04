package matchsystem

import (
	"fmt"
	"reflect"
	"sort"

	"matchSystem/internal/matchsystem/fact"
)

// Provider handshake error codes. The code is stable for callers that need to
// classify a LogicalNode startup failure without parsing its message.
const (
	ProviderHandshakeMissingProvider       = "MISSING_PROVIDER"
	ProviderHandshakeMissingDescriptor     = "MISSING_DESCRIPTOR"
	ProviderHandshakeInvalidDescriptor     = "INVALID_DESCRIPTOR"
	ProviderHandshakeMissingFact           = "MISSING_FACT"
	ProviderHandshakeExtraFact             = "EXTRA_FACT"
	ProviderHandshakeFactTypeMismatch      = "FACT_TYPE_MISMATCH"
	ProviderHandshakeFactScopeMismatch     = "FACT_SCOPE_MISMATCH"
	ProviderHandshakeFactMaxValuesMismatch = "FACT_MAX_VALUES_MISMATCH"
	ProviderHandshakeDuplicateFact         = "DUPLICATE_FACT"
)

// ProviderHandshakeError describes a mismatch between a rule's Fact contract
// and one provider's startup descriptor. Expected and Actual are populated
// whenever the error concerns a named Fact; they are nil for provider or
// descriptor presence errors.
type ProviderHandshakeError struct {
	Provider        string
	Scope           FactScope
	Code            string
	Name            string
	ProviderID      string
	ProviderVersion string
	Expected        *FactSpec
	Actual          *FactSpec
	Err             error
}

func (e *ProviderHandshakeError) Error() string {
	if e == nil {
		return "<nil>"
	}
	prefix := fmt.Sprintf("provider handshake for %s provider", e.Provider)
	if e.Scope != "" {
		prefix += fmt.Sprintf(" (%s scope)", e.Scope)
	}
	if e.ProviderID != "" {
		prefix += fmt.Sprintf(" %q", e.ProviderID)
		if e.ProviderVersion != "" {
			prefix += fmt.Sprintf(" version %q", e.ProviderVersion)
		}
	}
	if e.Code != "" {
		prefix += fmt.Sprintf(" [%s]", e.Code)
	}
	if e.Name != "" {
		prefix += fmt.Sprintf(" Fact %q", e.Name)
	}
	if e.Err == nil {
		return prefix
	}
	return prefix + ": " + e.Err.Error()
}

func (e *ProviderHandshakeError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func newProviderHandshakeError(provider string, scope fact.Scope, code string, name string, descriptor *ProviderDescriptor, expected, actual *fact.Spec, format string, args ...any) error {
	err := fmt.Errorf(format, args...)
	result := &ProviderHandshakeError{
		Provider: provider, Scope: scope, Code: code, Name: name,
		Expected: expected, Actual: actual, Err: err,
	}
	if descriptor != nil {
		result.ProviderID = descriptor.ID
		result.ProviderVersion = descriptor.Version
	}
	return result
}

// validateProviderHandshake compares one provider descriptor with the Fact
// declarations belonging to its scope. The providerPresent flag is kept
// separate from the descriptor so a provider callback cannot accidentally be
// treated as self-describing.
//
// A rule with no Facts for a scope does not require a provider or descriptor.
// This preserves existing callers that use a callback for other purposes. A
// provider descriptor may still advertise Facts that are not used by the rule;
// only the rule's Contract Facts need to be covered by the descriptor.
func validateProviderHandshake(provider string, scope fact.Scope, expected []fact.Spec, providerPresent bool, descriptor *ProviderDescriptor) error {
	// A scope with Contract Facts still requires both a live provider callback
	// and its descriptor. A scope without Contract Facts requires neither, but
	// an explicitly supplied non-empty descriptor remains a provider
	// declaration whose Fact metadata must be valid.
	if descriptor == nil {
		if len(expected) == 0 {
			return nil
		}
		if !providerPresent {
			return newProviderHandshakeError(provider, scope, ProviderHandshakeMissingProvider, "", descriptor, nil, nil,
				"provider is required because the rule declares %d %s-scoped Fact(s)", len(expected), scope)
		}
		return newProviderHandshakeError(provider, scope, ProviderHandshakeMissingDescriptor, "", nil, nil, nil,
			"provider descriptor is required because the rule declares %d %s-scoped Fact(s)", len(expected), scope)
	}
	if len(expected) > 0 && !providerPresent {
		return newProviderHandshakeError(provider, scope, ProviderHandshakeMissingProvider, "", descriptor, nil, nil,
			"provider is required because the rule declares %d %s-scoped Fact(s)", len(expected), scope)
	}
	if len(expected) > 0 || len(descriptor.Facts) > 0 {
		if descriptor.ID == "" {
			return newProviderHandshakeError(provider, scope, ProviderHandshakeInvalidDescriptor, "", descriptor, nil, nil,
				"provider descriptor ID is required")
		}
		if descriptor.Version == "" {
			return newProviderHandshakeError(provider, scope, ProviderHandshakeInvalidDescriptor, "", descriptor, nil, nil,
				"provider descriptor version is required")
		}
	}

	expectedByName := make(map[string]fact.Spec, len(expected))
	for _, spec := range expected {
		expectedByName[spec.Name] = spec
	}
	actualByName := make(map[string]fact.Spec, len(descriptor.Facts))
	for _, spec := range descriptor.Facts {
		if spec.Name == "" {
			return newProviderHandshakeError(provider, scope, ProviderHandshakeInvalidDescriptor, "", descriptor, nil, factSpecPointer(spec),
				"provider descriptor contains a Fact with an empty name")
		}
		if _, exists := actualByName[spec.Name]; exists {
			return newProviderHandshakeError(provider, scope, ProviderHandshakeDuplicateFact, spec.Name, descriptor, nil, factSpecPointer(spec),
				"provider descriptor declares Fact %q more than once", spec.Name)
		}
		actualByName[spec.Name] = spec
	}
	if len(expected) == 0 {
		extraNames := make([]string, 0, len(actualByName))
		for name := range actualByName {
			extraNames = append(extraNames, name)
		}
		sort.Strings(extraNames)
		for _, name := range extraNames {
			if err := validateExtraProviderFact(provider, scope, descriptor, actualByName[name]); err != nil {
				return err
			}
		}
		return nil
	}

	names := make([]string, 0, len(expectedByName))
	for name := range expectedByName {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		want := expectedByName[name]
		got, exists := actualByName[name]
		if !exists {
			return newProviderHandshakeError(provider, scope, ProviderHandshakeMissingFact, name, descriptor, factSpecPointer(want), nil,
				"provider descriptor is missing declared Fact %q", name)
		}
		if got.Scope != want.Scope {
			return newProviderHandshakeError(provider, scope, ProviderHandshakeFactScopeMismatch, name, descriptor, factSpecPointer(want), factSpecPointer(got),
				"expected scope %q, got %q", want.Scope, got.Scope)
		}
		if got.Type != want.Type {
			return newProviderHandshakeError(provider, scope, ProviderHandshakeFactTypeMismatch, name, descriptor, factSpecPointer(want), factSpecPointer(got),
				"expected type %s, got %s", factTypeName(want.Type), factTypeName(got.Type))
		}
		if got.MaxValues != want.MaxValues {
			return newProviderHandshakeError(provider, scope, ProviderHandshakeFactMaxValuesMismatch, name, descriptor, factSpecPointer(want), factSpecPointer(got),
				"expected maxValues %d, got %d", want.MaxValues, got.MaxValues)
		}
	}

	// Provider descriptors are allowed to advertise additional Facts. Validate
	// those declarations as provider-owned metadata, but do not require a
	// corresponding Contract Fact. This lets a provider share one implementation
	// across rules and compute a superset of the values consumed by a Contract.
	extraNames := make([]string, 0, len(actualByName))
	for name := range actualByName {
		if _, exists := expectedByName[name]; !exists {
			extraNames = append(extraNames, name)
		}
	}
	sort.Strings(extraNames)
	for _, name := range extraNames {
		if err := validateExtraProviderFact(provider, scope, descriptor, actualByName[name]); err != nil {
			return err
		}
	}
	return nil
}

func validateExtraProviderFact(provider string, scope fact.Scope, descriptor *ProviderDescriptor, spec fact.Spec) error {
	actual := factSpecPointer(spec)
	if spec.Scope != scope {
		return newProviderHandshakeError(provider, scope, ProviderHandshakeFactScopeMismatch, spec.Name, descriptor, nil, actual,
			"provider descriptor Fact %q has scope %q, expected %q", spec.Name, spec.Scope, scope)
	}
	switch spec.Type {
	case fact.TypeStrings, fact.TypeUint64s:
		if spec.MaxValues <= 0 {
			return newProviderHandshakeError(provider, scope, ProviderHandshakeInvalidDescriptor, spec.Name, descriptor, nil, actual,
				"provider descriptor multi-value Fact %q must declare a positive maxValues", spec.Name)
		}
	case fact.TypeInt64:
		if spec.MaxValues != 0 {
			return newProviderHandshakeError(provider, scope, ProviderHandshakeInvalidDescriptor, spec.Name, descriptor, nil, actual,
				"provider descriptor int64 Fact %q must not declare maxValues", spec.Name)
		}
	default:
		return newProviderHandshakeError(provider, scope, ProviderHandshakeInvalidDescriptor, spec.Name, descriptor, nil, actual,
			"provider descriptor Fact %q has invalid type %d", spec.Name, spec.Type)
	}
	return nil
}

func factSpecsForScope(specs []fact.Spec, scope fact.Scope) []fact.Spec {
	result := make([]fact.Spec, 0)
	for _, spec := range specs {
		if spec.Scope == scope {
			result = append(result, spec)
		}
	}
	return result
}

func providerIsPresent(provider any) bool {
	if provider == nil {
		return false
	}
	value := reflect.ValueOf(provider)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return !value.IsNil()
	default:
		return true
	}
}

func factSpecPointer(spec fact.Spec) *fact.Spec {
	copy := spec
	return &copy
}

func factTypeName(value fact.Type) string {
	switch value {
	case fact.TypeStrings:
		return "strings"
	case fact.TypeInt64:
		return "int64"
	case fact.TypeUint64s:
		return "uint64s"
	default:
		return fmt.Sprintf("type(%d)", value)
	}
}
