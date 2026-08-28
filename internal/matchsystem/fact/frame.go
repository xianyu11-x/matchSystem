package fact

import (
	"context"
	"fmt"
	"sort"

	"matchSystem/internal/common"
)

// Provider creates the Tick Fact layer for one matching attempt.
type Provider func(ctx context.Context, now int64) (Values, error)

// ObjectProvider creates the Object Fact layer for one Ticket. The supplied
// Tick layer is immutable for the synchronous callback.
type ObjectProvider func(object *common.Ticket, now int64, tick Values) (Values, error)

// Error identifies a Fact contract or scope failure.
type Error struct {
	Path string
	Code string
	Err  error
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	return fmt.Sprintf("facts at %s [%s]: %v", e.Path, e.Code, e.Err)
}

func (e *Error) Unwrap() error { return e.Err }

func newError(path, code, format string, args ...any) error {
	return &Error{Path: path, Code: code, Err: fmt.Errorf(format, args...)}
}

// View is a read-only view over one Tick layer and the Object layers that have
// been materialized so far. Returned Values must not be mutated or retained
// beyond the synchronous callback receiving the View.
type View struct {
	tick    Values
	objects map[common.TicketID]Values
}

func (v View) Tick() Values { return v.tick }

func (v View) For(ticket *common.Ticket) (Values, bool) {
	if ticket == nil {
		return Values{}, false
	}
	values, ok := v.objects[ticket.TicketID]
	return values, ok
}

// Frame owns the immutable Fact layers for one matching attempt. It clones the
// Tick layer once and materializes each Object layer at most once per TicketID.
type Frame struct {
	tick      Values
	objects   map[common.TicketID]Values
	objectErr map[common.TicketID]error
}

// NewFrame creates an owned Fact frame for one matching attempt. Fact
// providers are trusted in the production pipeline, so the frame only takes
// ownership of the callback result; schema/type/scope validation belongs in
// provider contract tests and is deliberately not repeated here.
func NewFrame(tick Values) *Frame {
	return &Frame{
		tick:      Clone(tick),
		objects:   make(map[common.TicketID]Values),
		objectErr: make(map[common.TicketID]error),
	}
}

// Tick returns the Frame-owned Tick layer for read-only downstream borrowing.
func (f *Frame) Tick() Values {
	if f == nil {
		return Values{}
	}
	return f.tick
}

func (f *Frame) View() View {
	if f == nil {
		return View{}
	}
	return View{tick: f.tick, objects: f.objects}
}

func (f *Frame) Object(ticket *common.Ticket, now int64, provider ObjectProvider) (Values, error) {
	if ticket == nil {
		return Values{}, newError("facts.object", "NIL_OBJECT", "object ticket is nil")
	}
	if values, ok := f.objects[ticket.TicketID]; ok {
		return values, nil
	}
	if cachedErr, ok := f.objectErr[ticket.TicketID]; ok {
		return Values{}, cachedErr
	}
	values := Values{}
	var err error
	if provider != nil {
		// Give the provider owned snapshots so mutating either argument cannot
		// alter the Frame's immutable Ticket/Tick layers used by later
		// predicates or candidates.
		values, err = provider(common.CloneTicket(ticket), now, Clone(f.tick))
		if err != nil {
			f.objectErr[ticket.TicketID] = err
			return Values{}, err
		}
	}
	values = Clone(values)
	f.objects[ticket.TicketID] = values
	return values, nil
}

// NameSet is the set of names present in one Fact layer.
type NameSet map[string]struct{}

// Field describes one value entry in a Fact layer.
type Field struct {
	Name       string
	Type       Type
	ValueCount int
}

// Inspect returns all fields in deterministic type/name order and rejects a
// name appearing in more than one type map.
func Inspect(path string, values Values) ([]Field, NameSet, error) {
	fields := make([]Field, 0, len(values.StringLists)+len(values.Uint64Lists)+len(values.Int64Values))
	names := make(NameSet, cap(fields))
	for _, group := range []struct {
		typeValue Type
		keys      []string
		length    func(string) int
	}{
		{typeValue: TypeStrings, keys: sortedMapKeys(values.StringLists), length: func(name string) int { return len(values.StringLists[name]) }},
		{typeValue: TypeUint64s, keys: sortedMapKeys(values.Uint64Lists), length: func(name string) int { return len(values.Uint64Lists[name]) }},
		{typeValue: TypeInt64, keys: sortedMapKeys(values.Int64Values), length: func(string) int { return 1 }},
	} {
		for _, name := range group.keys {
			fieldPath := path + "." + name
			if _, exists := names[name]; exists {
				return nil, nil, newError(fieldPath, "FACT_TYPE_COLLISION", "fact %q is present in multiple type maps", name)
			}
			names[name] = struct{}{}
			fields = append(fields, Field{Name: name, Type: group.typeValue, ValueCount: group.length(name)})
		}
	}
	return fields, names, nil
}

// ValidateTypes validates the type namespace and returns the names in a layer.
func ValidateTypes(path string, values Values) (NameSet, error) {
	_, names, err := Inspect(path, values)
	if err != nil {
		return nil, err
	}
	return names, nil
}

// ValidateScopes rejects names present in both supplied layers.
func ValidateScopes(path, leftScope, rightScope string, left, right NameSet) error {
	names := make([]string, 0, len(right))
	for name := range right {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if _, exists := left[name]; exists {
			return newError(path+"."+name, "FACT_SCOPE_COLLISION", "fact %q is present in both %s and %s Facts", name, leftScope, rightScope)
		}
	}
	return nil
}

// ValidateLayer checks one scoped Values snapshot against the shared contract.
func ValidateLayer(path string, values Values, specs []Spec, expectedScope Scope) (NameSet, error) {
	validator, err := NewValidator(specs)
	if err != nil {
		return nil, err
	}
	return validator.ValidateLayer(path, values, expectedScope)
}

// Validator is an immutable compiled Fact contract index for provider
// contract tests and debugging. The production matching pipeline trusts
// in-repository providers and deliberately does not invoke this validator on
// every Fact snapshot.
type Validator struct{ byName map[string]Spec }

func NewValidator(specs []Spec) (*Validator, error) {
	byName := make(map[string]Spec, len(specs))
	for index, spec := range specs {
		if spec.Name == "" {
			return nil, newError(fmt.Sprintf("facts[%d].name", index), "INVALID_FACT", "fact name is required")
		}
		if spec.Type != TypeStrings && spec.Type != TypeUint64s && spec.Type != TypeInt64 {
			return nil, newError(fmt.Sprintf("facts[%d].type", index), "INVALID_FACT", "fact %q has invalid type %d", spec.Name, spec.Type)
		}
		if spec.Scope != ScopeTick && spec.Scope != ScopeObject && spec.Scope != ScopeMatch {
			return nil, newError(fmt.Sprintf("facts[%d].scope", index), "INVALID_FACT_SCOPE", "fact %q has invalid scope %q", spec.Name, spec.Scope)
		}
		if spec.Type == TypeInt64 && spec.MaxValues != 0 {
			return nil, newError(fmt.Sprintf("facts[%d].maxValues", index), "INVALID_FACT_LIMIT", "int64 fact %q must not declare MaxValues", spec.Name)
		}
		if (spec.Type == TypeStrings || spec.Type == TypeUint64s) && spec.MaxValues <= 0 {
			return nil, newError(fmt.Sprintf("facts[%d].maxValues", index), "INVALID_FACT_LIMIT", "multi-value fact %q requires a positive MaxValues", spec.Name)
		}
		if _, exists := byName[spec.Name]; exists {
			return nil, newError(fmt.Sprintf("facts[%d].name", index), "DUPLICATE_FACT", "fact %q is duplicated", spec.Name)
		}
		byName[spec.Name] = spec
	}
	return &Validator{byName: byName}, nil
}

func (v *Validator) ValidateLayer(path string, values Values, expectedScope Scope) (NameSet, error) {
	fields, names, err := Inspect(path, values)
	if err != nil {
		return nil, err
	}
	for _, field := range fields {
		fieldPath := path + "." + field.Name
		spec, exists := v.byName[field.Name]
		if !exists {
			return nil, newError(fieldPath, "UNDECLARED_FACT", "fact %q is not declared by the contract", field.Name)
		}
		if spec.Type != field.Type {
			return nil, newError(fieldPath, "FACT_TYPE_MISMATCH", "fact %q has a value in the wrong type map", field.Name)
		}
		if spec.Scope != expectedScope {
			return nil, newError(fieldPath, "FACT_SCOPE_MISMATCH", "fact %q belongs to %s scope, not %s", field.Name, spec.Scope, expectedScope)
		}
		if spec.MaxValues > 0 && field.ValueCount > spec.MaxValues {
			return nil, newError(fieldPath, "FACT_VALUE_LIMIT", "fact %q contains %d values; maximum is %d", field.Name, field.ValueCount, spec.MaxValues)
		}
	}
	return names, nil
}

// ValidateCompleteMatch validates a complete Match-scoped Fact layer.
//
// Unlike ValidateLayer, this method also requires every match-scoped Fact
// declared by the contract to be present. A map key with an empty slice is a
// valid representation of an empty multi-value Fact; omitting the key is not.
// Values belonging to another scope, undeclared values, duplicate type-map
// entries, type mismatches, and values over MaxValues are rejected by the same
// contract checks used for other Fact layers.
func (v *Validator) ValidateCompleteMatch(path string, values Values) error {
	if v == nil {
		return newError(path, "INVALID_VALIDATOR", "Fact validator is nil")
	}
	if _, err := v.ValidateLayer(path, values, ScopeMatch); err != nil {
		return err
	}
	for _, name := range sortedValidatorNames(v.byName, ScopeMatch) {
		if !valueContains(values, v.byName[name].Type, name) {
			return newError(path+"."+name, "MATCH_FACT_INCOMPLETE", "match Fact %q is missing from the complete layer", name)
		}
	}
	return nil
}

// CloneValidatedMatch validates a complete Match-scoped Fact layer and then
// returns an owned deep copy. The returned Values never aliases the provider's
// maps or slices, so it can be atomically retained by the matching owner.
func (v *Validator) CloneValidatedMatch(path string, values Values) (Values, error) {
	if err := v.ValidateCompleteMatch(path, values); err != nil {
		return Values{}, err
	}
	return Clone(values), nil
}

// ValidateCompleteMatch validates a complete Match-scoped Fact layer against
// the supplied shared Fact contract.
func ValidateCompleteMatch(path string, values Values, specs []Spec) error {
	validator, err := NewValidator(specs)
	if err != nil {
		return err
	}
	return validator.ValidateCompleteMatch(path, values)
}

// CloneValidatedMatch validates and owns a complete Match-scoped Fact layer
// against the supplied shared Fact contract.
func CloneValidatedMatch(path string, values Values, specs []Spec) (Values, error) {
	validator, err := NewValidator(specs)
	if err != nil {
		return Values{}, err
	}
	return validator.CloneValidatedMatch(path, values)
}

func sortedValidatorNames(specs map[string]Spec, scope Scope) []string {
	names := make([]string, 0, len(specs))
	for name, spec := range specs {
		if spec.Scope == scope {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func valueContains(values Values, typeValue Type, name string) bool {
	switch typeValue {
	case TypeStrings:
		_, ok := values.StringLists[name]
		return ok
	case TypeUint64s:
		_, ok := values.Uint64Lists[name]
		return ok
	case TypeInt64:
		_, ok := values.Int64Values[name]
		return ok
	default:
		return false
	}
}

func SameSpecs(left, right []Spec) bool {
	if len(left) != len(right) {
		return false
	}
	byName := make(map[string]Spec, len(left))
	for _, spec := range left {
		byName[spec.Name] = spec
	}
	for _, spec := range right {
		if existing, ok := byName[spec.Name]; !ok || existing != spec {
			return false
		}
	}
	return true
}

func sortedMapKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
