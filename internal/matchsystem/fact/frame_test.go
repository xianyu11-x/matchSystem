package fact

import (
	"testing"

	"matchSystem/internal/common"
)

func TestFrameOwnsTrustedProviderFactsWithoutRuntimeValidation(t *testing.T) {
	tick := Values{
		StringLists: map[string][]string{"tick-label": {"before"}},
	}
	frame := NewFrame(tick)
	tick.StringLists["tick-label"][0] = "caller-mutated"
	if got := frame.Tick().StringLists["tick-label"][0]; got != "before" {
		t.Fatalf("frame Tick aliases provider result: got %q", got)
	}

	ticket := &common.Ticket{TicketID: 7}
	providerValues := Values{
		// These deliberately contain an undeclared name and a type collision.
		// The production Frame owns trusted provider output; the Validator is
		// exercised explicitly by provider contract tests below instead.
		StringLists: map[string][]string{"object-label": {"trusted"}},
		Int64Values: map[string]int64{"object-label": 7},
	}
	got, err := frame.Object(ticket, 123, func(object *common.Ticket, _ int64, suppliedTick Values) (Values, error) {
		object.TicketID = 99
		suppliedTick.StringLists["tick-label"][0] = "provider-mutated"
		return providerValues, nil
	})
	if err != nil {
		t.Fatalf("trusted Object provider was rejected: %v", err)
	}
	if got.StringLists["object-label"][0] != "trusted" || got.Int64Values["object-label"] != 7 {
		t.Fatalf("unexpected owned Object facts: %#v", got)
	}
	providerValues.StringLists["object-label"][0] = "provider-mutated"
	if got.StringLists["object-label"][0] != "trusted" {
		t.Fatal("frame Object facts alias provider result")
	}
	if frame.Tick().StringLists["tick-label"][0] != "before" {
		t.Fatal("Object provider mutated frame Tick through its input")
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

	objectProvider := ObjectProvider(func(*common.Ticket, int64, Values) (Values, error) {
		return Values{Uint64Lists: map[string][]uint64{"object-id": {7}}}, nil
	})
	objectFacts, err := objectProvider(&common.Ticket{TicketID: 7}, 123, tickFacts)
	if err != nil {
		t.Fatalf("object provider: %v", err)
	}
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
