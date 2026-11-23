package simulatorapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/xianyu11-x/matchSystem/internal/simulator"
	"github.com/xianyu11-x/matchSystem/pkg/identity"
	"github.com/xianyu11-x/matchSystem/pkg/matchsystem"
)

type apiDescribedTickProvider struct{}

func (apiDescribedTickProvider) Provide(context.Context, matchsystem.TickFactInput) (matchsystem.Facts, error) {
	return matchsystem.Facts{}, nil
}

func (apiDescribedTickProvider) Describe() (matchsystem.ProviderDescriptor, error) {
	return matchsystem.ProviderDescriptor{
		ID:      "test.tick-facts",
		Version: "v1",
		Facts: []matchsystem.FactSpec{{
			Name: "party-size", Type: matchsystem.FactTypeInt64, Scope: matchsystem.FactScopeTick,
		}},
	}, nil
}

func TestLogicalNodeFactsEndpointReturnsDetachedMetadata(t *testing.T) {
	key := identity.LogicalNodeKey{Rule: identity.RuleKey{Namespace: "facts", RuleID: 1}, PlacementID: testPlacement("p1")}
	ruleJSON := bytes.Replace(
		apiRuleJSON("facts", 1),
		[]byte(`"facts":[]`),
		[]byte(`"facts":[{"name":"party-size","type":"int64","scope":"tick","description":"number of players in the current match"}]`),
		1,
	)
	rule := simulator.NewRuleSpec(key, testPhysical("p1"), ruleJSON)
	rule.FactProvider = apiDescribedTickProvider{}
	rule.TickFacts = simulator.FactSnapshot{Int64Values: map[string]int64{"party-size": 3}}
	runtime, err := simulator.NewSimulator(simulator.Scenario{
		SchemaVersion: simulator.ScenarioSchemaVersion,
		PhysicalNodes: []simulator.PhysicalNodeSpec{simulator.NewPhysicalNodeSpec(testPhysical("p1"), "inproc://p1")},
		Rules:         []simulator.RuleSpec{rule},
	})
	if err != nil {
		t.Fatalf("NewSimulator: %v", err)
	}
	defer runtime.Close()

	adapter := NewSimulatorAdapter(runtime)
	result, err := adapter.GetLogicalNodeFacts(context.Background(), LogicalNodeFactsQuery{
		RuleID: 1, PlacementID: testPlacement("p1"),
	})
	if err != nil {
		t.Fatalf("GetLogicalNodeFacts: %v", err)
	}
	if len(result.Facts) != 1 || len(result.ContractFacts) != 1 || result.Facts[0].Type != "int64" || result.Facts[0].Scope != "tick" || result.Facts[0].Description == "" {
		t.Fatalf("unexpected Fact metadata: %#v", result)
	}
	if result.ProviderDescriptors.Tick == nil || result.ProviderDescriptors.Tick.ID != "test.tick-facts" || len(result.ProviderDescriptors.Tick.Facts) != 1 {
		t.Fatalf("unexpected Provider Descriptor metadata: %#v", result.ProviderDescriptors)
	}
	result.Facts[0].Description = "mutated"
	again, err := adapter.GetLogicalNodeFacts(context.Background(), LogicalNodeFactsQuery{
		RuleID: 1, PlacementID: testPlacement("p1"),
	})
	if err != nil {
		t.Fatalf("second GetLogicalNodeFacts: %v", err)
	}
	if again.Facts[0].Description == "mutated" {
		t.Fatal("Fact metadata query exposed mutable internal state")
	}

	server := httptest.NewServer(NewHandler(adapter))
	defer server.Close()
	response, err := server.Client().Get(server.URL + "/api/v1/logical-nodes/facts?ruleNamespace=474191651361140340&ruleId=1&placementId=17745680053102999587")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("Fact metadata endpoint status=%d", response.StatusCode)
	}
	var wire LogicalNodeFactsResponse
	if err := json.NewDecoder(response.Body).Decode(&wire); err != nil {
		t.Fatal(err)
	}
	if wire.LogicalNode.Rule.Namespace != "facts" || len(wire.Facts) != 1 || len(wire.ContractFacts) != 1 || wire.Facts[0].Description == "" || wire.ProviderDescriptors.Tick == nil || wire.RuntimeFacts.Tick.Int64Values == nil {
		t.Fatalf("unexpected wire Fact metadata: %#v", wire)
	}
}

func TestLogicalNodeFactsEndpointReturnsNotImplementedForLegacyService(t *testing.T) {
	server := httptest.NewServer(NewHandler(&fakeService{}))
	defer server.Close()
	client := server.Client()

	response, err := client.Get(server.URL + "/api/v1/logical-nodes/facts?ruleId=1&placementId=17745680053102999587")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var unsupported ErrorResponse
	if err := json.NewDecoder(response.Body).Decode(&unsupported); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusNotImplemented || unsupported.Error.Code != "NOT_IMPLEMENTED" {
		t.Fatalf("unsupported service: status=%d body=%#v", response.StatusCode, unsupported)
	}
}

func TestLogicalNodeFactsEndpointReturnsNotFound(t *testing.T) {
	runtime := newLogicalNodeFactsTestRuntime(t,
		simulator.NewRuleSpec(identity.LogicalNodeKey{Rule: identity.RuleKey{Namespace: "one", RuleID: 1}, PlacementID: testPlacement("p1")}, testPhysical("p1"), apiRuleJSON("one", 1)),
	)
	defer runtime.Close()
	server := httptest.NewServer(NewHandler(NewSimulatorAdapter(runtime)))
	defer server.Close()

	response, err := server.Client().Get(server.URL + "/api/v1/logical-nodes/facts?ruleNamespace=8544106591765445504&ruleId=1&placementId=18421470166314807403")
	if err != nil {
		t.Fatal(err)
	}
	var missing ErrorResponse
	if err := json.NewDecoder(response.Body).Decode(&missing); err != nil {
		response.Body.Close()
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNotFound || missing.Error.Code != "LOGICAL_NODE_NOT_FOUND" {
		t.Fatalf("missing LogicalNode: status=%d body=%#v", response.StatusCode, missing)
	}
}

func TestLogicalNodeFactsEndpointIgnoresNamespaceQuery(t *testing.T) {
	runtime := newLogicalNodeFactsTestRuntime(t,
		simulator.NewRuleSpec(identity.LogicalNodeKey{Rule: identity.RuleKey{Namespace: "one", RuleID: 1}, PlacementID: testPlacement("p1")}, testPhysical("p1"), apiRuleJSON("one", 1)),
	)
	defer runtime.Close()
	server := httptest.NewServer(NewHandler(NewSimulatorAdapter(runtime)))
	defer server.Close()

	for _, suffix := range []string{"", "&ruleNamespace=other%2Falias%3A01"} {
		response, err := server.Client().Get(server.URL + "/api/v1/logical-nodes/facts?ruleId=1&placementId=17745680053102999587" + suffix)
		if err != nil {
			t.Fatal(err)
		}
		var result LogicalNodeFactsResponse
		if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
			response.Body.Close()
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusOK || result.LogicalNode.Rule.Namespace != "one" {
			t.Fatalf("规则别名影响查询: status=%d result=%#v", response.StatusCode, result)
		}
	}
}

func newLogicalNodeFactsTestRuntime(t *testing.T, rules ...simulator.RuleSpec) *simulator.Simulator {
	t.Helper()
	runtime, err := simulator.NewSimulator(simulator.Scenario{
		SchemaVersion: simulator.ScenarioSchemaVersion,
		PhysicalNodes: []simulator.PhysicalNodeSpec{
			simulator.NewPhysicalNodeSpec(testPhysical("p1"), "inproc://p1"),
			simulator.NewPhysicalNodeSpec(testPhysical("p2"), "inproc://p2"),
		},
		Rules: rules,
	})
	if err != nil {
		t.Fatalf("NewSimulator: %v", err)
	}
	return runtime
}
