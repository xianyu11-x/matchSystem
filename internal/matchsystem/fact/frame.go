package fact

import (
	"fmt"
	"time"

	"matchSystem/internal/common"
)

// ObjectProvider writes the Object Fact layer synchronously into out. The
// Ticket and Tick arguments are borrowed read-only views and are valid only
// for the duration of the callback. Values written through out are copied
// into the Ticket's reusable ObjectSlot.
//
// A provider must not retain object, tick, or out, and must not mutate object
// or tick. The writer is the only supported way to publish Object Facts.
type ObjectProvider func(object *common.Ticket, now int64, tick Values, out Writer) error

// ObjectAccess describes one Frame.Object operation. It is returned as a
// value so the matching owner can aggregate diagnostics without logging per
// Ticket. ProviderDuration is populated only when the Frame was created with
// observation enabled.
type ObjectAccess struct {
	ProviderCalled   bool
	Refreshed        bool
	CacheHit         bool
	CapacityGrowths  uint64
	RefreshDuration  time.Duration
	ProviderDuration time.Duration
	Error            bool
}

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

// Frame owns the Tick Fact layer for one matching attempt. Object Fact data
// lives in per-Ticket ObjectSlots; Frame only carries the shared Tick layer,
// generation, and whether timing diagnostics are enabled.
type Frame struct {
	tick       Values
	generation uint64
	observe    bool
}

// NewFrame creates a Fact frame. generation must be non-zero and is shared by
// every ObjectSlot accessed during this ProduceMatch operation.
func NewFrame(tick Values, generation uint64, observe bool) *Frame {
	return &Frame{
		tick:       Clone(tick),
		generation: generation,
		observe:    observe,
	}
}

// Tick returns the Frame-owned Tick layer for read-only downstream borrowing.
func (f *Frame) Tick() Values {
	if f == nil {
		return Values{}
	}
	return f.tick
}

// Generation identifies the ProduceMatch generation represented by this
// Frame. Slots refresh at most once for this value.
func (f *Frame) Generation() uint64 {
	if f == nil {
		return 0
	}
	return f.generation
}

// Object lazily refreshes the supplied per-Ticket slot and returns a borrowed
// read-only Values layer. A ready slot is returned without invoking the
// provider again; a failed slot returns the same error until the next
// generation.
func (f *Frame) Object(slot *ObjectSlot, ticket *common.Ticket, now int64, provider ObjectProvider) (Values, ObjectAccess, error) {
	if f == nil {
		return Values{}, ObjectAccess{Error: true}, newError("facts.frame", "NIL_FRAME", "Fact frame is nil")
	}
	if ticket == nil {
		return Values{}, ObjectAccess{Error: true}, newError("facts.object", "NIL_OBJECT", "object ticket is nil")
	}
	if f.generation == 0 {
		return Values{}, ObjectAccess{Error: true}, newError("facts.object", "INVALID_GENERATION", "Fact frame generation must be non-zero")
	}
	if slot == nil {
		// Rules without Object-scoped Facts do not allocate a slot per Ticket.
		// The evaluator still goes through this seam so its materialization
		// accounting remains structurally uniform, but there is no provider or
		// cache event to report for an empty Object layer.
		return Values{}, ObjectAccess{}, nil
	}
	return slot.ensure(f.generation, ticket, now, f.tick, provider, f.observe)
}
