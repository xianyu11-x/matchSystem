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
func simulatorTickFactProvider(static FactSnapshot, specs []fact.Spec) matchsystem.FactProvider {
	static = static.clone()
	return func(_ context.Context, input matchsystem.TickFactInput) (matchsystem.Facts, error) {
		values := static.values()
		if values.Int64Values == nil {
			values.Int64Values = make(map[string]int64)
		}
		for _, spec := range specs {
			if spec.Type != fact.TypeInt64 || !isWaitingCountFact(spec.Name) {
				continue
			}
			values.Int64Values[spec.Name] = waitingCountValue(input.Node.WaitingCount)
		}
		return values, nil
	}
}

// simulatorObjectFactProvider copies caller-provided Object values and then
// overlays the simulator-owned queue wait metric.  The value is calculated in
// the same caller-defined time unit as Ticket.CreatedAt and Tick.Now; the HTTP
// simulator uses Unix milliseconds, so the usual API value is milliseconds.
func simulatorObjectFactProvider(registry *ObservationRegistry, owner identity.OwnerRef, specs []fact.Spec) matchsystem.ObjectFactProvider {
	return func(object *common.Ticket, now int64, _ matchsystem.Facts, out matchsystem.ObjectFactWriter) error {
		if object == nil {
			return fmt.Errorf("object Ticket is nil")
		}
		if registry != nil {
			if values, ok := registry.ObjectFacts(owner, object.TicketID); ok {
				if err := out.CopyFrom(values); err != nil {
					return err
				}
			}
		}
		for _, spec := range specs {
			if spec.Type != fact.TypeInt64 || !isWaitingTimeFact(spec.Name) {
				continue
			}
			if err := out.SetInt64(spec.Name, elapsedFactTime(now, object.CreatedAt)); err != nil {
				return err
			}
		}
		return nil
	}
}

func isWaitingCountFact(name string) bool {
	switch name {
	case WaitingCountFactName, "queueDepth", "waiting-count":
		return true
	default:
		return false
	}
}

func isWaitingTimeFact(name string) bool {
	switch name {
	case WaitingTimeFactName, "waitTime", "waiting-time", "wait-time":
		return true
	default:
		return false
	}
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
