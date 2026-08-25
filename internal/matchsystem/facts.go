package matchsystem

import (
	"fmt"
	"sort"

	"matchSystem/internal/matchsystem/fact"
)

type FactType = fact.Type

const (
	FactTypeStrings = fact.TypeStrings
	FactTypeInt64   = fact.TypeInt64
	FactTypeUint64s = fact.TypeUint64s
)

type FactSpec = fact.Spec
type Facts = fact.Values

// FactError identifies a node-wide Fact contract or scope failure.
type FactError struct {
	Path string
	Code string
	Err  error
}

func (e *FactError) Error() string {
	if e == nil {
		return "<nil>"
	}
	return fmt.Sprintf("match facts at %s [%s]: %v", e.Path, e.Code, e.Err)
}

func (e *FactError) Unwrap() error { return e.Err }

func newFactError(path, code, format string, args ...any) error {
	return &FactError{Path: path, Code: code, Err: fmt.Errorf(format, args...)}
}

// FactView is a read-only view over one Tick layer and the object layers that
// have been materialized so far. Values returned by its methods must not be
// mutated or retained beyond the synchronous callback receiving the view.
type FactView struct {
	tick    Facts
	objects map[uint32]Facts
}

func (v FactView) Tick() Facts { return v.tick }

func (v FactView) For(ticket *Ticket) (Facts, bool) {
	if ticket == nil {
		return Facts{}, false
	}
	return v.ForDocID(ticket.DocID)
}

func (v FactView) ForDocID(docID uint32) (Facts, bool) {
	values, ok := v.objects[docID]
	return values, ok
}

type factFrame struct {
	tick      Facts
	tickNames map[string]struct{}
	objects   map[uint32]Facts
	objectErr map[uint32]error
	contract  map[string]FactSpec
}

func newFactFrame(tick Facts, specs []FactSpec) (*factFrame, error) {
	contract := make(map[string]FactSpec, len(specs))
	for _, spec := range specs {
		contract[spec.Name] = spec
	}
	tick = fact.Clone(tick)
	tickNames, err := validateFactLayer("facts.tick", tick, contract)
	if err != nil {
		return nil, err
	}
	return &factFrame{
		tick:      tick,
		tickNames: tickNames,
		objects:   make(map[uint32]Facts),
		objectErr: make(map[uint32]error),
		contract:  contract,
	}, nil
}

func (f *factFrame) view() FactView {
	if f == nil {
		return FactView{}
	}
	return FactView{tick: f.tick, objects: f.objects}
}

func (f *factFrame) object(ticket *Ticket, now int64, provider ObjectFactProvider) (Facts, error) {
	if ticket == nil {
		return Facts{}, newFactError("facts.object", "NIL_OBJECT", "object ticket is nil")
	}
	if values, ok := f.objects[ticket.DocID]; ok {
		return values, nil
	}
	if cachedErr, ok := f.objectErr[ticket.DocID]; ok {
		return Facts{}, cachedErr
	}
	values := Facts{}
	var err error
	if provider != nil {
		values, err = provider(ticket, now, f.tick)
		if err != nil {
			f.objectErr[ticket.DocID] = err
			return Facts{}, err
		}
	}
	values = fact.Clone(values)
	path := fmt.Sprintf("facts.object[%d]", ticket.DocID)
	names, err := validateFactLayer(path, values, f.contract)
	if err != nil {
		f.objectErr[ticket.DocID] = err
		return Facts{}, err
	}
	for _, name := range sortedFactNames(names) {
		if _, exists := f.tickNames[name]; exists {
			err := newFactError(path+"."+name, "FACT_SCOPE_COLLISION", "fact %q is present in both Tick and object Facts", name)
			f.objectErr[ticket.DocID] = err
			return Facts{}, err
		}
	}
	f.objects[ticket.DocID] = values
	return values, nil
}

func validateFactLayer(path string, values Facts, contract map[string]FactSpec) (map[string]struct{}, error) {
	names := make(map[string]struct{}, len(values.StringLists)+len(values.Uint64Lists)+len(values.Int64Values))
	for _, group := range []struct {
		typeValue FactType
		keys      []string
		length    func(string) int
	}{
		{typeValue: FactTypeStrings, keys: sortedMapKeys(values.StringLists), length: func(name string) int { return len(values.StringLists[name]) }},
		{typeValue: FactTypeUint64s, keys: sortedMapKeys(values.Uint64Lists), length: func(name string) int { return len(values.Uint64Lists[name]) }},
		{typeValue: FactTypeInt64, keys: sortedMapKeys(values.Int64Values), length: func(string) int { return 1 }},
	} {
		for _, name := range group.keys {
			fieldPath := path + "." + name
			if _, exists := names[name]; exists {
				return nil, newFactError(fieldPath, "FACT_TYPE_COLLISION", "fact %q is present in multiple type maps", name)
			}
			names[name] = struct{}{}
			spec, exists := contract[name]
			if !exists {
				return nil, newFactError(fieldPath, "UNDECLARED_FACT", "fact %q is not declared by the LogicalNode", name)
			}
			if spec.Type != group.typeValue {
				return nil, newFactError(fieldPath, "FACT_TYPE_MISMATCH", "fact %q has a value in the wrong type map", name)
			}
			if spec.MaxValues > 0 && group.length(name) > spec.MaxValues {
				return nil, newFactError(fieldPath, "FACT_VALUE_LIMIT", "fact %q contains %d values; maximum is %d", name, group.length(name), spec.MaxValues)
			}
		}
	}
	return names, nil
}

func sortedMapKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedFactNames(values map[string]struct{}) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sameFactSpecs(left, right []FactSpec) bool {
	if len(left) != len(right) {
		return false
	}
	byName := make(map[string]FactSpec, len(left))
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
