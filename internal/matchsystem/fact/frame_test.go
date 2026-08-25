package fact

import (
	"errors"
	"testing"

	"matchSystem/internal/common"
)

func TestFrameOwnsTickAndCachesObjectValues(t *testing.T) {
	tick := Values{Int64Values: map[string]int64{"capacity": 3}}
	frame, err := NewFrame(tick, []Spec{
		{Name: "capacity", Type: TypeInt64},
		{Name: "priority", Type: TypeInt64},
	})
	if err != nil {
		t.Fatal(err)
	}
	tick.Int64Values["capacity"] = 99
	if got := frame.Tick().Int64Values["capacity"]; got != 3 {
		t.Fatalf("Frame did not own Tick Facts: capacity=%d", got)
	}

	object := &common.Ticket{TicketID: 7}
	reused := Values{Int64Values: map[string]int64{"priority": 10}}
	calls := 0
	provider := func(*common.Ticket, int64, Values) (Values, error) {
		calls++
		return reused, nil
	}
	if _, err := frame.Object(object, 1, provider); err != nil {
		t.Fatal(err)
	}
	reused.Int64Values["priority"] = 100
	if _, err := frame.Object(object, 1, provider); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("Object provider calls=%d want=1", calls)
	}
	values, ok := frame.View().For(object)
	if !ok || values.Int64Values["priority"] != 10 {
		t.Fatalf("Frame did not own Object Facts: values=%#v ok=%v", values, ok)
	}
}

func TestFactValidationReturnsStructuredErrors(t *testing.T) {
	_, err := ValidateTypes("facts.tick", Values{
		StringLists: map[string][]string{"shared": {"x"}},
		Int64Values: map[string]int64{"shared": 1},
	})
	requireErrorCode(t, err, "FACT_TYPE_COLLISION")

	left := NameSet{"shared": {}}
	right := NameSet{"shared": {}}
	requireErrorCode(t, ValidateScopes("facts.seed", "Tick", "Seed", left, right), "FACT_SCOPE_COLLISION")
}

func requireErrorCode(t *testing.T, err error, code string) {
	t.Helper()
	var target *Error
	if !errors.As(err, &target) || target.Code != code {
		t.Fatalf("error=%v, want fact.Error code %s", err, code)
	}
}
