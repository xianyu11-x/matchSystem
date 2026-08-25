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
	tickNames NameSet
	objects   map[common.TicketID]Values
	objectErr map[common.TicketID]error
	contract  map[string]Spec
}

func NewFrame(tick Values, specs []Spec) (*Frame, error) {
	contract := make(map[string]Spec, len(specs))
	for _, spec := range specs {
		contract[spec.Name] = spec
	}
	tick = Clone(tick)
	tickNames, err := validateLayer("facts.tick", tick, contract)
	if err != nil {
		return nil, err
	}
	return &Frame{
		tick:      tick,
		tickNames: tickNames,
		objects:   make(map[common.TicketID]Values),
		objectErr: make(map[common.TicketID]error),
		contract:  contract,
	}, nil
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
		values, err = provider(ticket, now, f.tick)
		if err != nil {
			f.objectErr[ticket.TicketID] = err
			return Values{}, err
		}
	}
	values = Clone(values)
	path := fmt.Sprintf("facts.object[%d]", ticket.TicketID)
	names, err := validateLayer(path, values, f.contract)
	if err != nil {
		f.objectErr[ticket.TicketID] = err
		return Values{}, err
	}
	if err := ValidateScopes(path, "Tick", "object", f.tickNames, names); err != nil {
		f.objectErr[ticket.TicketID] = err
		return Values{}, err
	}
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

func validateLayer(path string, values Values, contract map[string]Spec) (NameSet, error) {
	fields, names, err := Inspect(path, values)
	if err != nil {
		return nil, err
	}
	for _, field := range fields {
		fieldPath := path + "." + field.Name
		spec, exists := contract[field.Name]
		if !exists {
			return nil, newError(fieldPath, "UNDECLARED_FACT", "fact %q is not declared by the contract", field.Name)
		}
		if spec.Type != field.Type {
			return nil, newError(fieldPath, "FACT_TYPE_MISMATCH", "fact %q has a value in the wrong type map", field.Name)
		}
		if spec.MaxValues > 0 && field.ValueCount > spec.MaxValues {
			return nil, newError(fieldPath, "FACT_VALUE_LIMIT", "fact %q contains %d values; maximum is %d", field.Name, field.ValueCount, spec.MaxValues)
		}
	}
	return names, nil
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
