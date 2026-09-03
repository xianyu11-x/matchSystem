package matchsystem

import (
	"context"
	"testing"

	"matchSystem/internal/identity"
)

func TestPhysicalNodeBeginMatchRoundInitializesEveryLogicalRuntime(t *testing.T) {
	ctx := context.Background()
	physical, err := NewPhysicalNode(identity.PhysicalNodeID("physical-round-test"))
	if err != nil {
		t.Fatalf("create PhysicalNode: %v", err)
	}

	for ruleID := int32(1); ruleID <= 2; ruleID++ {
		key := identity.LogicalNodeKey{
			Rule:        identity.RuleKey{Namespace: "physical-round-test", RuleID: ruleID},
			PlacementID: identity.PlacementID("placement"),
		}
		spec := LogicalNodeSpec{
			Key: key,
			RuleJSON: integrationRuleJSON(t, key.Rule,
				integrationEmptyContractJSON,
				integrationNonePrefilterJSON,
				integrationCompleteEvaluationJSON,
				`{"type":"constant","params":{"value":1}}`,
				`{"type":"arrival","params":{}}`,
				logicalNodeConfig{SeedScheduler: seedSchedulerConfig{
					AttemptLimitPerProduceMatch: 1,
					AttemptLimitPerMatchRound:   10,
				}}),
		}
		if err := physical.Load(ctx, spec); err != nil {
			t.Fatalf("load LogicalNode %s: %v", key, err)
		}
		owner := identity.OwnerRef{PhysicalNodeID: physical.ID(), LogicalNode: key}
		if err := physical.Add(ctx, owner, testTicket(TicketID(ruleID))); err != nil {
			t.Fatalf("add Ticket to %s: %v", key, err)
		}
	}

	if err := physical.BeginMatchRound(ctx, 123); err != nil {
		t.Fatalf("begin PhysicalNode round: %v", err)
	}
	if !physical.roundActive {
		t.Fatal("PhysicalNode round was not marked active")
	}
	for rule, node := range physical.nodes {
		if node == nil {
			t.Fatalf("LogicalNode for %s is nil", rule)
		}
		if !node.seedRound.initialized || node.seedRound.now != 123 || node.seedRound.attemptedSeeds != 0 {
			t.Fatalf("LogicalNode %s has unexpected round state: %+v", rule, node.seedRound)
		}
		if !node.seedOrderRuntime.HasNext() {
			t.Fatalf("LogicalNode %s runtime was not initialized with its Ticket", rule)
		}
	}
}
