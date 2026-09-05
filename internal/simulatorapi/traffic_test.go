package simulatorapi

import (
	"encoding/json"
	"matchSystem/internal/identity"
	"matchSystem/internal/simulator"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestTrafficHTTP(t *testing.T) {
	key := identity.LogicalNodeKey{Rule: identity.RuleKey{Namespace: "e2e", RuleID: 1}, PlacementID: "p1"}
	runtime, err := simulator.NewService(simulator.Scenario{PhysicalNodes: []simulator.PhysicalNodeSpec{simulator.NewPhysicalNodeSpec("p1", "inproc://p1")}, Rules: []simulator.RuleSpec{simulator.NewRuleSpec(key, "p1", apiRuleJSON("e2e", 1))}})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	server := httptest.NewServer(NewHandler(NewSimulatorAdapter(runtime)))
	defer server.Close()
	body := `{"config":{"distribution":"poisson","rate":100,"matchIntervalMs":150,"maxMatches":1,"seed":42},"generator":{"rule":{"namespace":"e2e","ruleId":1},"startTicketId":100,"seed":5}}`
	response := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/v1/traffic", body)
	if response.StatusCode != 200 {
		t.Fatalf("start: %d", response.StatusCode)
	}
	response.Body.Close()
	time.Sleep(350 * time.Millisecond)
	response = doJSON(t, server.Client(), http.MethodGet, server.URL+"/api/v1/traffic", "")
	var status simulator.TrafficStatus
	if err = json.NewDecoder(response.Body).Decode(&status); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if status.Injected == 0 || status.Rounds == 0 || status.Produced > status.Rounds {
		t.Fatalf("status: %+v", status)
	}
	response = doJSON(t, server.Client(), http.MethodDelete, server.URL+"/api/v1/traffic", "")
	if response.StatusCode != 200 {
		t.Fatal(response.StatusCode)
	}
	response.Body.Close()
	stopped := runtime.Traffic()
	time.Sleep(50 * time.Millisecond)
	if runtime.Traffic() != stopped {
		t.Fatal("HTTP stop leaked")
	}
	response = doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/v1/traffic", `{"config":{"distribution":"bad"},"generator":{}}`)
	if response.StatusCode < 400 {
		t.Fatal("invalid config accepted")
	}
	response.Body.Close()
}
