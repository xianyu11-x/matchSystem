package simulator

import (
	"context"
	"fmt"
	"math"

	"matchSystem/internal/common"
	"matchSystem/internal/identity"
	"matchSystem/internal/matchsystem"
	"matchSystem/internal/matchsystem/fact"
)

// Built-in simulator Fact names.  They are ordinary Contract Facts (the
// simulator does not add them to a rule automatically), but the simulator's
// default providers know how to calculate them when they are declared.
const (
	// WaitingCountFactName is the number of active Tickets in the owning
	// LogicalNode when a matching attempt starts.
	WaitingCountFactName = "waitingCount"
	// WaitingTimeFactName is the elapsed queue time of one Ticket at the
	// round's Now timestamp, in the caller-defined timestamp unit.
	WaitingTimeFactName = "waitingTime"

	// FactNameWaitingCount and FactNameWaitingTime are descriptive aliases for
	// hosts that group constants by the Fact prefix.
	FactNameWaitingCount = WaitingCountFactName
	FactNameWaitingTime  = WaitingTimeFactName
)

// simulatorTickFactProvider preserves configured static Tick values and
// refreshes the simulator-owned queue metrics for every ProduceMatch attempt.
// The refresh is intentionally selected from the Contract rather than from a
// provider descriptor: a descriptor may advertise a superset of the values a
// rule consumes, while the runtime writer is bound to the Contract.
// simulatorTickFactProvider is a named function value so existing in-process
// callers can still invoke it directly. Its Describe method is the sole
// capability declaration for the built-in Tick provider.
type simulatorTickProviderFunc func(context.Context, matchsystem.TickFactInput) (matchsystem.Facts, error)

func (p simulatorTickProviderFunc) Provide(ctx context.Context, input matchsystem.TickFactInput) (matchsystem.Facts, error) {
	return p(ctx, input)
}

func (simulatorTickProviderFunc) Describe() (matchsystem.ProviderDescriptor, error) {
	return matchsystem.ProviderDescriptor{
		ID:      "simulator.tick-facts",
		Version: "v1",
		Facts: []matchsystem.FactSpec{{
			Name: WaitingCountFactName, Type: matchsystem.FactTypeInt64, Scope: matchsystem.FactScopeTick,
		}},
	}, nil
}

func simulatorTickFactProvider(static FactSnapshot, specs []fact.Spec) simulatorTickProviderFunc {
	static = static.clone()
	declaresWaitingCount := false
	for _, spec := range specs {
		if spec.Name == WaitingCountFactName && spec.Type == fact.TypeInt64 && spec.Scope == fact.ScopeTick {
			declaresWaitingCount = true
			break
		}
	}
	return func(_ context.Context, input matchsystem.TickFactInput) (matchsystem.Facts, error) {
		values := static.values()
		if declaresWaitingCount && values.Int64Values == nil {
			values.Int64Values = make(map[string]int64)
		}
		if declaresWaitingCount {
			values.Int64Values[WaitingCountFactName] = waitingCountValue(input.Node.WaitingCount)
		}
		return values, nil
	}
}

// simulatorObjectFactProvider copies caller-provided Object values and then
// overlays the simulator-owned queue wait metric.  The value is calculated in
// the same caller-defined time unit as Ticket.CreatedAt and Tick.Now; the HTTP
// simulator uses Unix milliseconds, so the usual API value is milliseconds.
type simulatorObjectProvider struct {
	registry *ObservationRegistry
	owner    identity.OwnerRef
}

func (p simulatorObjectProvider) Describe() (matchsystem.ProviderDescriptor, error) {
	return matchsystem.ProviderDescriptor{
		ID:      "simulator.object-facts",
		Version: "v1",
		Facts: []matchsystem.FactSpec{
			{Name: "preferredRoles", Type: matchsystem.FactTypeStrings, Scope: matchsystem.FactScopeObject, MaxValues: 3},
			{Name: "latencyMs", Type: matchsystem.FactTypeInt64, Scope: matchsystem.FactScopeObject},
			{Name: WaitingTimeFactName, Type: matchsystem.FactTypeInt64, Scope: matchsystem.FactScopeObject},
		},
	}, nil
}

func (p simulatorObjectProvider) Provide(object *common.Ticket, now int64, _ matchsystem.Facts, out matchsystem.ObjectFactWriter) error {
	if object == nil {
		return fmt.Errorf("object Ticket is nil")
	}
	if p.registry != nil {
		if values, ok := p.registry.ObjectFacts(p.owner, object.TicketID); ok {
			if err := copyBuiltinObjectFacts(values, out); err != nil {
				return err
			}
		}
	}
	if err := setBuiltinInt64(out, WaitingTimeFactName, elapsedFactTime(now, object.CreatedAt)); err != nil {
		return err
	}
	return nil
}

func simulatorObjectFactProvider(registry *ObservationRegistry, owner identity.OwnerRef, _ []fact.Spec) simulatorObjectProvider {
	return simulatorObjectProvider{registry: registry, owner: owner}
}

func copyBuiltinObjectFacts(values matchsystem.Facts, out matchsystem.ObjectFactWriter) error {
	if values.StringLists != nil {
		if raw, ok := values.StringLists["preferredRoles"]; ok {
			if err := setBuiltinStrings(out, "preferredRoles", raw); err != nil {
				return err
			}
		}
	}
	if values.Int64Values != nil {
		if raw, ok := values.Int64Values["latencyMs"]; ok {
			if err := setBuiltinInt64(out, "latencyMs", raw); err != nil {
				return err
			}
		}
	}
	return nil
}

func setBuiltinStrings(out matchsystem.ObjectFactWriter, name string, values []string) error {
	if err := out.SetStrings(name, values); err != nil && !isUndeclaredObjectFact(err) {
		return err
	}
	return nil
}

func setBuiltinInt64(out matchsystem.ObjectFactWriter, name string, value int64) error {
	if err := out.SetInt64(name, value); err != nil && !isUndeclaredObjectFact(err) {
		return err
	}
	return nil
}

func isUndeclaredObjectFact(err error) bool {
	factErr, ok := err.(*fact.Error)
	return ok && factErr.Code == "UNDECLARED_FACT"
}

func waitingCountValue(count int) int64 {
	if count <= 0 {
		return 0
	}
	return int64(count)
}

// elapsedFactTime returns max(0, now-createdAt), saturating at MaxInt64 if a
// future-dated or extreme Ticket would otherwise overflow the subtraction.
func elapsedFactTime(now, createdAt int64) int64 {
	if now <= createdAt {
		return 0
	}
	if createdAt < 0 && now > math.MaxInt64+createdAt {
		return math.MaxInt64
	}
	return now - createdAt
}
