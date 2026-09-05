package simulatorapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"matchSystem/internal/matchsystem"
	"matchSystem/internal/matchsystem/fact"
	"matchSystem/internal/simulator"
)

func TestComposerBatchTimeAndObjectFacts(t *testing.T) {
	scenario := apiScenario()
	for i := range scenario.Rules {
		rule := &scenario.Rules[i]
		rule.RuleJSON = []byte(strings.Replace(string(rule.RuleJSON), `"facts":[]`, `"facts":[{"name":"latency","type":"int64","scope":"object"}]`, 1))
		rule.ObjectFactProviderDescriptor = &matchsystem.ProviderDescriptor{ID: "test.object", Version: "v1", Facts: []matchsystem.FactSpec{{Name: "latency", Type: fact.TypeInt64, Scope: fact.ScopeObject}}}
	}
	runtime, err := simulator.NewSimulator(scenario)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	server := httptest.NewServer(NewHandler(NewSimulatorAdapter(runtime)))
	defer server.Close()
	response := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/v1/tickets/batch", `{"count":2,"seed":7,"startTicketId":700,"rule":{"namespace":"api","ruleId":1},"placementId":"p1","createdAtStart":1788639000000,"createdAtStep":250,"affinityPrefix":"route-","requestIdPrefix":"request-","objectFacts":{"int64Values":{"latency":42}},"atomic":true}`)
	defer response.Body.Close()
	var result TicketBatchResponse
	decodeResponse(t, response, &result)
	if result.Accepted != 2 {
		t.Fatalf("accepted: %+v", result)
	}
	for index, id := range []string{"700", "701"} {
		response := doJSON(t, server.Client(), http.MethodGet, server.URL+"/api/v1/tickets?search="+id, "")
		var page TicketPage
		decodeResponse(t, response, &page)
		if len(page.Items) != 1 {
			t.Fatalf("tickets: %+v", page)
		}
		view := page.Items[0]
		response.Body.Close()
		if view.Ticket.CreatedAt != 1788639000000+int64(index)*250 || view.Facts.Int64Values["latency"] != 42 {
			encoded, _ := json.Marshal(view)
			t.Fatalf("lost composer values: %s", encoded)
		}
	}
	// Prefixes are runtime routing/request inputs rather than observation DTO fields.
	inputs, err := simulator.GenerateBatch(generatorSpec(CustomTicketsRequest{Rule: RuleKey{Namespace: "api", RuleID: 1}, Count: 1, StartTicketID: 9, AffinityPrefix: "a-", RequestIDPrefix: "r-"}))
	if err != nil || inputs[0].AffinityKey != "a-9" || inputs[0].RequestID != "r-9" {
		t.Fatalf("prefixes: %+v %v", inputs, err)
	}
}
