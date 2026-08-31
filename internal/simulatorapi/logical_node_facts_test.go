package simulatorapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"matchSystem/internal/identity"
	"matchSystem/internal/simulator"
)

func TestLogicalNodeFactsEndpointReturnsDetachedMetadata(t *testing.T) {
	key := identity.LogicalNodeKey{Rule: identity.RuleKey{Namespace: "facts", RuleID: 1}, PlacementID: "p1"}
	ruleJSON := bytes.Replace(
		apiRuleJSON("facts", 1),
		[]byte(`"facts":[]`),
		[]byte(`"facts":[{"name":"party-size","type":"int64","scope":"tick","description":"number of players in the current match"}]`),
		1,
	)
	runtime, err := simulator.NewSimulator(simulator.Scenario{
		SchemaVersion: simulator.ScenarioSchemaVersion,
		PhysicalNodes: []simulator.PhysicalNodeSpec{simulator.NewPhysicalNodeSpec("p1", "inproc://p1")},
		Rules:         []simulator.RuleSpec{simulator.NewRuleSpec(key, "p1", ruleJSON)},
	})
	if err != nil {
		t.Fatalf("NewSimulator: %v", err)
	}
	defer runtime.Close()

	adapter := NewSimulatorAdapter(runtime)
	result, err := adapter.GetLogicalNodeFacts(context.Background(), LogicalNodeFactsQuery{
		RuleNamespace: "facts", RuleID: 1, PlacementID: "p1",
	})
	if err != nil {
		t.Fatalf("GetLogicalNodeFacts: %v", err)
	}
	if len(result.Facts) != 1 || result.Facts[0].Type != "int64" || result.Facts[0].Scope != "tick" || result.Facts[0].Description == "" {
		t.Fatalf("unexpected Fact metadata: %#v", result)
	}
	result.Facts[0].Description = "mutated"
	again, err := adapter.GetLogicalNodeFacts(context.Background(), LogicalNodeFactsQuery{
		RuleNamespace: "facts", RuleID: 1, PlacementID: "p1",
	})
	if err != nil {
		t.Fatalf("second GetLogicalNodeFacts: %v", err)
	}
	if again.Facts[0].Description == "mutated" {
		t.Fatal("Fact metadata query exposed mutable internal state")
	}

	server := httptest.NewServer(NewHandler(adapter))
	defer server.Close()
	response, err := server.Client().Get(server.URL + "/api/v1/logical-nodes/facts?ruleNamespace=facts&ruleId=1&placementId=p1")
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
	if len(wire.Facts) != 1 || wire.Facts[0].Description == "" {
		t.Fatalf("unexpected wire Fact metadata: %#v", wire)
	}
}

func TestLogicalNodeFactsEndpointReturnsNotImplementedForLegacyService(t *testing.T) {
	server := httptest.NewServer(NewHandler(&fakeService{}))
	defer server.Close()
	client := server.Client()

	response, err := client.Get(server.URL + "/api/v1/logical-nodes/facts?ruleId=1&placementId=p1")
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
		simulator.NewRuleSpec(identity.LogicalNodeKey{Rule: identity.RuleKey{Namespace: "one", RuleID: 1}, PlacementID: "p1"}, "p1", apiRuleJSON("one", 1)),
	)
	defer runtime.Close()
	server := httptest.NewServer(NewHandler(NewSimulatorAdapter(runtime)))
	defer server.Close()

	response, err := server.Client().Get(server.URL + "/api/v1/logical-nodes/facts?ruleNamespace=one&ruleId=1&placementId=missing")
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

func TestLogicalNodeFactsEndpointReturnsAmbiguousRule(t *testing.T) {
	runtime := newLogicalNodeFactsTestRuntime(t,
		simulator.NewRuleSpec(identity.LogicalNodeKey{Rule: identity.RuleKey{Namespace: "one", RuleID: 1}, PlacementID: "p1"}, "p1", apiRuleJSON("one", 1)),
		simulator.NewRuleSpec(identity.LogicalNodeKey{Rule: identity.RuleKey{Namespace: "two", RuleID: 1}, PlacementID: "p1"}, "p1", apiRuleJSON("two", 1)),
	)
	defer runtime.Close()
	server := httptest.NewServer(NewHandler(NewSimulatorAdapter(runtime)))
	defer server.Close()

	response, err := server.Client().Get(server.URL + "/api/v1/logical-nodes/facts?ruleId=1&placementId=p1")
	if err != nil {
		t.Fatal(err)
	}
	var ambiguous ErrorResponse
	if err := json.NewDecoder(response.Body).Decode(&ambiguous); err != nil {
		response.Body.Close()
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusConflict || ambiguous.Error.Code != "LOGICAL_NODE_AMBIGUOUS" {
		t.Fatalf("ambiguous query: status=%d body=%#v", response.StatusCode, ambiguous)
	}
}

func newLogicalNodeFactsTestRuntime(t *testing.T, rules ...simulator.RuleSpec) *simulator.Simulator {
	t.Helper()
	runtime, err := simulator.NewSimulator(simulator.Scenario{
		SchemaVersion: simulator.ScenarioSchemaVersion,
		PhysicalNodes: []simulator.PhysicalNodeSpec{
			simulator.NewPhysicalNodeSpec("p1", "inproc://p1"),
			simulator.NewPhysicalNodeSpec("p2", "inproc://p2"),
		},
		Rules: rules,
	})
	if err != nil {
		t.Fatalf("NewSimulator: %v", err)
	}
	return runtime
}
