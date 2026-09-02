package fact

import (
	"errors"
	"testing"

	"matchSystem/internal/common"
)

func testObjectLayout(t *testing.T) *ObjectLayout {
	t.Helper()
	layout, err := NewObjectLayout([]Spec{
		{Name: "object-label", Type: TypeStrings, MaxValues: 2, Scope: ScopeObject},
		{Name: "object-id", Type: TypeUint64s, MaxValues: 2, Scope: ScopeObject},
		{Name: "object-score", Type: TypeInt64, Scope: ScopeObject},
	})
	if err != nil {
		t.Fatalf("compile Object layout: %v", err)
	}
	return layout
}

func TestFrameLazyWriterCachesAndRefreshesSlot(t *testing.T) {
	layout := testObjectLayout(t)
	var slot ObjectSlot
	slot.Init(layout)
	tick := Values{StringLists: map[string][]string{"tick-label": {"before"}}}
	frame := NewFrame(tick, 1, false)
	ticket := &common.Ticket{TicketID: 7}
	source := []string{"trusted"}
	calls := 0
	provider := ObjectProvider(func(object *common.Ticket, _ int64, suppliedTick Values, out Writer) error {
		calls++
		if object != ticket || suppliedTick.StringLists["tick-label"][0] != "before" {
			t.Fatalf("provider did not receive borrowed inputs: object=%p tick=%#v", object, suppliedTick)
		}
		if err := out.SetStrings("object-label", source); err != nil {
			return err
		}
		if err := out.SetUint64s("object-id", []uint64{object.TicketID}); err != nil {
			return err
		}
		return out.SetInt64("object-score", 9)
	})

	got, access, err := frame.Object(&slot, ticket, 123, provider)
	if err != nil {
		t.Fatalf("first Object refresh: %v", err)
	}
	if !access.Refreshed || !access.ProviderCalled || access.CacheHit || calls != 1 {
		t.Fatalf("first access metadata: %#v calls=%d", access, calls)
	}
	source[0] = "caller-mutated"
	if got.StringLists["object-label"][0] != "trusted" {
		t.Fatalf("writer did not copy source slice: %#v", got)
	}

	cached, access, err := frame.Object(&slot, ticket, 123, func(*common.Ticket, int64, Values, Writer) error {
		calls++
		return errors.New("must not run")
	})
	if err != nil || calls != 1 || !access.CacheHit || access.Refreshed || access.ProviderCalled {
		t.Fatalf("same generation was not cached: values=%#v access=%#v calls=%d err=%v", cached, access, calls, err)
	}

	next := NewFrame(tick, 2, false)
	if _, access, err := next.Object(&slot, ticket, 124, provider); err != nil || !access.Refreshed || calls != 2 {
		t.Fatalf("next generation did not refresh: access=%#v calls=%d err=%v", access, calls, err)
	}
	if got, ok := slot.ValuesFor(2); !ok || got.StringLists["object-label"][0] != "caller-mutated" {
		t.Fatalf("next generation did not publish current source value: %#v ok=%v", got, ok)
	}
}

func TestFrameWithoutObjectSlotIsNoop(t *testing.T) {
	frame := NewFrame(Values{}, 1, false)
	calls := 0
	values, access, err := frame.Object(nil, &common.Ticket{TicketID: 1}, 0, func(*common.Ticket, int64, Values, Writer) error {
		calls++
		return errors.New("must not run for an empty Object layout")
	})
	if err != nil || calls != 0 || access != (ObjectAccess{}) {
		t.Fatalf("empty Object slot was not a no-op: values=%#v access=%#v calls=%d err=%v", values, access, calls, err)
	}
}

func TestObjectWriterSchemaAndFailureLifecycle(t *testing.T) {
	layout := testObjectLayout(t)
	var slot ObjectSlot
	slot.Init(layout)
	frame := NewFrame(Values{}, 1, false)
	ticket := &common.Ticket{TicketID: 1}

	if _, _, err := frame.Object(&slot, ticket, 0, func(_ *common.Ticket, _ int64, _ Values, out Writer) error {
		return out.SetStrings("object-score", []string{"wrong-type"})
	}); err == nil {
		t.Fatal("writer accepted wrong type")
	}
	if slot.State() != ObjectSlotFailed {
		t.Fatalf("wrong-type writer state=%v, want failed", slot.State())
	}
	if values, ok := slot.ValuesFor(1); ok || len(values.StringLists) != 0 {
		t.Fatalf("failed writer published partial values: %#v ok=%v", values, ok)
	}

	// A failed generation is cached, but the next generation retries and can
	// explicitly publish an empty list.
	frame = NewFrame(Values{}, 2, false)
	values, access, err := frame.Object(&slot, ticket, 0, func(_ *common.Ticket, _ int64, _ Values, out Writer) error {
		if err := out.SetStrings("object-label", nil); err != nil {
			return err
		}
		return out.SetUint64s("object-id", []uint64{})
	})
	if err != nil || !access.Refreshed {
		t.Fatalf("retry failed: values=%#v access=%#v err=%v", values, access, err)
	}
	if list, present := values.StringLists["object-label"]; !present || len(list) != 0 {
		t.Fatalf("empty string list was not represented as present: %#v present=%v", list, present)
	}
	if list, present := values.Uint64Lists["object-id"]; !present || len(list) != 0 {
		t.Fatalf("empty uint64 list was not represented as present: %#v present=%v", list, present)
	}

	frame = NewFrame(Values{}, 3, false)
	if _, _, err := frame.Object(&slot, ticket, 0, func(_ *common.Ticket, _ int64, _ Values, out Writer) error {
		return out.SetStrings("object-label", []string{"a", "b", "c"})
	}); err == nil {
		t.Fatal("writer accepted values over MaxValues")
	}
	var factErr *Error
	if _, _, err := frame.Object(&slot, ticket, 0, nil); !errors.As(err, &factErr) || factErr.Code != "FACT_VALUE_LIMIT" {
		t.Fatalf("overflow error was not cached with schema code: %T %v", err, err)
	}
}

func TestValidatorIsExplicitProviderContractCheck(t *testing.T) {
	validator, err := NewValidator([]Spec{
		{Name: "tick-label", Type: TypeStrings, MaxValues: 2, Scope: ScopeTick},
		{Name: "object-id", Type: TypeUint64s, MaxValues: 2, Scope: ScopeObject},
		{Name: "match-count", Type: TypeInt64, Scope: ScopeMatch},
	})
	if err != nil {
		t.Fatalf("compile provider contract validator: %v", err)
	}

	tickFacts := Values{StringLists: map[string][]string{"tick-label": {"ready"}}}
	if _, err := validator.ValidateLayer("facts.tick", tickFacts, ScopeTick); err != nil {
		t.Fatalf("tick provider does not satisfy contract: %v", err)
	}

	layout, err := NewObjectLayout([]Spec{{Name: "object-id", Type: TypeUint64s, MaxValues: 2, Scope: ScopeObject}})
	if err != nil {
		t.Fatalf("compile object layout: %v", err)
	}
	var slot ObjectSlot
	slot.Init(layout)
	objectProvider := ObjectProvider(func(_ *common.Ticket, _ int64, _ Values, out Writer) error {
		return out.SetUint64s("object-id", []uint64{7})
	})
	if err := objectProvider(&common.Ticket{TicketID: 7}, 123, tickFacts, Writer{slot: &slot}); err != nil {
		t.Fatalf("object provider: %v", err)
	}
	objectFacts := slot.values
	if _, err := validator.ValidateLayer("facts.object", objectFacts, ScopeObject); err != nil {
		t.Fatalf("object provider does not satisfy contract: %v", err)
	}

	matchFacts := Values{Int64Values: map[string]int64{"match-count": 1}}
	if err := validator.ValidateCompleteMatch("facts.match", matchFacts); err != nil {
		t.Fatalf("match provider output does not satisfy contract: %v", err)
	}

	invalidTick := Values{Int64Values: map[string]int64{"tick-label": 1}}
	if _, err := validator.ValidateLayer("facts.tick", invalidTick, ScopeTick); err == nil {
		t.Fatal("explicit provider contract check accepted a wrong Fact type")
	}
}
