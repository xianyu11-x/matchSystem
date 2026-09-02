package fact

import (
	"time"

	"matchSystem/internal/common"
)

// ObjectLayout is the immutable, shared Object Fact schema used by every
// Ticket in one LogicalNode. Slots keep only per-Ticket state and reusable
// value buffers; the field metadata is shared.
type ObjectLayout struct {
	fields      map[string]objectField
	stringCount int
	uint64Count int
}

type objectField struct {
	typeValue Type
	maxValues int
	index     int
}

// NewObjectLayout validates and compiles the Object-scoped portion of a Fact
// contract. Non-object Specs are ignored so callers may pass the complete
// contract Fact list.
func NewObjectLayout(specs []Spec) (*ObjectLayout, error) {
	if _, err := NewValidator(specs); err != nil {
		return nil, err
	}
	layout := &ObjectLayout{fields: make(map[string]objectField)}
	for _, spec := range specs {
		if spec.Scope != ScopeObject {
			continue
		}
		field := objectField{typeValue: spec.Type, maxValues: spec.MaxValues}
		switch spec.Type {
		case TypeStrings:
			field.index = layout.stringCount
			layout.stringCount++
		case TypeUint64s:
			field.index = layout.uint64Count
			layout.uint64Count++
		case TypeInt64:
			// Int64 fields do not need a reusable slice buffer.
		default:
			return nil, newError("facts.object."+spec.Name, "INVALID_FACT", "unsupported Object Fact type %d", spec.Type)
		}
		if _, exists := layout.fields[spec.Name]; exists {
			return nil, newError("facts.object."+spec.Name, "DUPLICATE_FACT", "Object Fact %q is duplicated", spec.Name)
		}
		layout.fields[spec.Name] = field
	}
	return layout, nil
}

// HasFields reports whether this layout contains at least one Object-scoped
// Fact. LogicalNode stores no per-Ticket ObjectSlot at all when this is false.
func (layout *ObjectLayout) HasFields() bool {
	return layout != nil && len(layout.fields) != 0
}

// Writer is a typed, schema-bound Object Fact writer. Its methods copy input
// slices into the slot-owned reusable buffers. Values returns the current
// borrowed layer for diagnostics/validation after the provider returns.
type Writer struct {
	slot *ObjectSlot
}

// SetStrings publishes one complete string-list value. An empty or nil input
// still publishes the field, which is distinct from an omitted field.
func (w *Writer) SetStrings(name string, values []string) error {
	field, err := w.field(name, TypeStrings)
	if err != nil {
		return err
	}
	if field.maxValues > 0 && len(values) > field.maxValues {
		return newError("facts.object."+name, "FACT_VALUE_LIMIT", "fact %q contains %d values; maximum is %d", name, len(values), field.maxValues)
	}
	if err := w.ensureStringMap(); err != nil {
		return err
	}
	buffer := w.slot.stringBuffers[field.index][:0]
	if cap(buffer) < len(values) {
		buffer = growStrings(buffer, len(values))
		w.slot.capacityGrowths++
	}
	buffer = append(buffer, values...)
	w.slot.stringBuffers[field.index] = buffer
	w.slot.values.StringLists[name] = buffer
	return nil
}

// AppendString appends one value to a string-list field, creating the field
// if it was not written yet.
func (w *Writer) AppendString(name, value string) error {
	field, err := w.field(name, TypeStrings)
	if err != nil {
		return err
	}
	buffer := w.slot.stringBuffers[field.index]
	if field.maxValues > 0 && len(buffer) >= field.maxValues {
		return newError("facts.object."+name, "FACT_VALUE_LIMIT", "fact %q contains more than maximum %d values", name, field.maxValues)
	}
	if err := w.ensureStringMap(); err != nil {
		return err
	}
	if len(buffer) == cap(buffer) {
		buffer = growStrings(buffer, len(buffer)+1)
		w.slot.capacityGrowths++
	}
	buffer = append(buffer, value)
	w.slot.stringBuffers[field.index] = buffer
	w.slot.values.StringLists[name] = buffer
	return nil
}

// SetUint64s publishes one complete uint64-list value.
func (w *Writer) SetUint64s(name string, values []uint64) error {
	field, err := w.field(name, TypeUint64s)
	if err != nil {
		return err
	}
	if field.maxValues > 0 && len(values) > field.maxValues {
		return newError("facts.object."+name, "FACT_VALUE_LIMIT", "fact %q contains %d values; maximum is %d", name, len(values), field.maxValues)
	}
	if err := w.ensureUint64Map(); err != nil {
		return err
	}
	buffer := w.slot.uint64Buffers[field.index][:0]
	if cap(buffer) < len(values) {
		buffer = growUint64s(buffer, len(values))
		w.slot.capacityGrowths++
	}
	buffer = append(buffer, values...)
	w.slot.uint64Buffers[field.index] = buffer
	w.slot.values.Uint64Lists[name] = buffer
	return nil
}

// AppendUint64 appends one value to a uint64-list field.
func (w *Writer) AppendUint64(name string, value uint64) error {
	field, err := w.field(name, TypeUint64s)
	if err != nil {
		return err
	}
	buffer := w.slot.uint64Buffers[field.index]
	if field.maxValues > 0 && len(buffer) >= field.maxValues {
		return newError("facts.object."+name, "FACT_VALUE_LIMIT", "fact %q contains more than maximum %d values", name, field.maxValues)
	}
	if err := w.ensureUint64Map(); err != nil {
		return err
	}
	if len(buffer) == cap(buffer) {
		buffer = growUint64s(buffer, len(buffer)+1)
		w.slot.capacityGrowths++
	}
	buffer = append(buffer, value)
	w.slot.uint64Buffers[field.index] = buffer
	w.slot.values.Uint64Lists[name] = buffer
	return nil
}

// SetInt64 publishes one int64 value.
func (w *Writer) SetInt64(name string, value int64) error {
	if _, err := w.field(name, TypeInt64); err != nil {
		return err
	}
	if w.slot.values.Int64Values == nil {
		w.slot.values.Int64Values = make(map[string]int64, w.slot.layout.int64Count())
	}
	w.slot.values.Int64Values[name] = value
	return nil
}

// CopyFrom copies a Values layer through the same schema and capacity checks
// as the typed writer methods. It is useful for adapters such as the
// simulator, while providers in the hot path can write typed fields directly.
func (w *Writer) CopyFrom(values Values) error {
	for name, list := range values.StringLists {
		if err := w.SetStrings(name, list); err != nil {
			return err
		}
	}
	for name, list := range values.Uint64Lists {
		if err := w.SetUint64s(name, list); err != nil {
			return err
		}
	}
	for name, value := range values.Int64Values {
		if err := w.SetInt64(name, value); err != nil {
			return err
		}
	}
	return nil
}

// Values returns the slot's current borrowed Values layer. It must only be
// used synchronously while the slot is ready for the current generation.
func (w *Writer) Values() Values {
	if w == nil || w.slot == nil {
		return Values{}
	}
	return w.slot.values
}

func (w *Writer) field(name string, expected Type) (objectField, error) {
	if w == nil || w.slot == nil || w.slot.layout == nil {
		return objectField{}, newError("facts.object."+name, "MISSING_LAYOUT", "Object Fact layout is nil")
	}
	field, ok := w.slot.layout.fields[name]
	if !ok {
		return objectField{}, newError("facts.object."+name, "UNDECLARED_FACT", "Object Fact %q is not declared by the contract", name)
	}
	if field.typeValue != expected {
		return objectField{}, newError("facts.object."+name, "FACT_TYPE_MISMATCH", "Object Fact %q expects type %d, not %d", name, field.typeValue, expected)
	}
	return field, nil
}

func (w *Writer) ensureStringMap() error {
	if w == nil || w.slot == nil || w.slot.layout == nil {
		return newError("facts.object", "MISSING_LAYOUT", "Object Fact layout is nil")
	}
	if w.slot.values.StringLists == nil {
		w.slot.values.StringLists = make(map[string][]string, w.slot.layout.stringCount)
	}
	return nil
}

func (w *Writer) ensureUint64Map() error {
	if w == nil || w.slot == nil || w.slot.layout == nil {
		return newError("facts.object", "MISSING_LAYOUT", "Object Fact layout is nil")
	}
	if w.slot.values.Uint64Lists == nil {
		w.slot.values.Uint64Lists = make(map[string][]uint64, w.slot.layout.uint64Count)
	}
	return nil
}

func growStrings(buffer []string, need int) []string {
	capacity := cap(buffer) * 2
	if capacity < 1 {
		capacity = 1
	}
	if capacity < need {
		capacity = need
	}
	grown := make([]string, len(buffer), capacity)
	copy(grown, buffer)
	return grown
}

func growUint64s(buffer []uint64, need int) []uint64 {
	capacity := cap(buffer) * 2
	if capacity < 1 {
		capacity = 1
	}
	if capacity < need {
		capacity = need
	}
	grown := make([]uint64, len(buffer), capacity)
	copy(grown, buffer)
	return grown
}

// ObjectSlotState is exposed for diagnostics and tests without exposing the
// mutable slot internals.
type ObjectSlotState uint8

const (
	ObjectSlotUnseen ObjectSlotState = iota
	ObjectSlotRefreshing
	ObjectSlotReady
	ObjectSlotFailed
)

// ObjectSlot stores one Ticket's reusable Object Fact buffers and generation
// state. It is owned by the matching goroutine and is not goroutine-safe.
type ObjectSlot struct {
	layout          *ObjectLayout
	generation      uint64
	state           ObjectSlotState
	err             error
	values          Values
	stringBuffers   [][]string
	uint64Buffers   [][]uint64
	capacityGrowths uint64
}

// Init attaches the shared layout and resets a slot when a Ticket enters the
// store. It is intentionally separate from construction so the matching
// owner can allocate slots only for rules that declare Object Facts.
func (s *ObjectSlot) Init(layout *ObjectLayout) {
	if s == nil {
		return
	}
	var stringCount, uint64Count int
	if layout != nil {
		stringCount = layout.stringCount
		uint64Count = layout.uint64Count
	}
	*s = ObjectSlot{
		layout:        layout,
		stringBuffers: make([][]string, stringCount),
		uint64Buffers: make([][]uint64, uint64Count),
	}
}

// State reports the current lifecycle state.
func (s *ObjectSlot) State() ObjectSlotState {
	if s == nil {
		return ObjectSlotFailed
	}
	return s.state
}

// ValuesFor returns the ready layer for generation. The returned maps/slices
// are borrowed and must not be mutated or retained.
func (s *ObjectSlot) ValuesFor(generation uint64) (Values, bool) {
	if s == nil || s.state != ObjectSlotReady || s.generation != generation {
		return Values{}, false
	}
	return s.values, true
}

// Invalidate releases logical presence for a Ticket that is removed or
// committed. Re-adding the same TicketID receives a fresh slot, so no old
// generation can become visible to the new Ticket.
func (s *ObjectSlot) Invalidate() {
	if s == nil {
		return
	}
	s.resetValues()
	s.layout = nil
	s.generation = 0
	s.state = ObjectSlotUnseen
	s.err = nil
}

func (s *ObjectSlot) ensure(generation uint64, object *common.Ticket, now int64, tick Values, provider ObjectProvider, observe bool) (Values, ObjectAccess, error) {
	access := ObjectAccess{}
	if s == nil {
		return Values{}, ObjectAccess{Error: true}, newError("facts.object", "MISSING_SLOT", "object Fact slot is nil")
	}
	if generation == 0 {
		return Values{}, ObjectAccess{Error: true}, newError("facts.object", "INVALID_GENERATION", "Object Fact generation must be non-zero")
	}
	if s.generation != generation {
		s.resetForGeneration(generation)
	}
	switch s.state {
	case ObjectSlotReady:
		access.CacheHit = true
		return s.values, access, nil
	case ObjectSlotFailed:
		access.CacheHit = true
		access.Error = true
		return Values{}, access, s.err
	case ObjectSlotRefreshing:
		s.err = newError("facts.object", "REENTRANT_PROVIDER", "Object Fact provider re-entered the same Ticket slot")
		s.state = ObjectSlotFailed
		access.Error = true
		return Values{}, access, s.err
	case ObjectSlotUnseen:
		// Continue below.
	default:
		return Values{}, ObjectAccess{Error: true}, newError("facts.object", "INVALID_SLOT_STATE", "unknown Object Fact slot state %d", s.state)
	}

	s.state = ObjectSlotRefreshing
	access.Refreshed = true
	var refreshStarted, providerStarted time.Time
	if observe {
		refreshStarted = time.Now()
		if provider != nil {
			providerStarted = refreshStarted
		}
	}
	var err error
	if provider != nil {
		access.ProviderCalled = true
		writer := Writer{slot: s}
		err = provider(object, now, tick, writer)
		if observe {
			access.ProviderDuration = time.Since(providerStarted)
		}
	}
	if observe {
		access.RefreshDuration = time.Since(refreshStarted)
	}
	access.CapacityGrowths = s.capacityGrowths
	if err != nil {
		s.resetValues()
		s.err = err
		s.state = ObjectSlotFailed
		access.Error = true
		return Values{}, access, err
	}
	s.err = nil
	s.state = ObjectSlotReady
	return s.values, access, nil
}

func (s *ObjectSlot) resetForGeneration(generation uint64) {
	s.resetValues()
	s.generation = generation
	s.state = ObjectSlotUnseen
	s.err = nil
	s.capacityGrowths = 0
}

func (s *ObjectSlot) resetValues() {
	clear(s.values.StringLists)
	clear(s.values.Uint64Lists)
	clear(s.values.Int64Values)
	for index := range s.stringBuffers {
		s.stringBuffers[index] = s.stringBuffers[index][:0]
	}
	for index := range s.uint64Buffers {
		s.uint64Buffers[index] = s.uint64Buffers[index][:0]
	}
}

func (layout *ObjectLayout) int64Count() int {
	if layout == nil {
		return 0
	}
	return len(layout.fields) - layout.stringCount - layout.uint64Count
}
