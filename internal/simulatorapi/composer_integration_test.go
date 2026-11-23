package simulatorapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/xianyu11-x/matchSystem/internal/simulator"
)

func TestComposerBatchTimeAndObjectFacts(t *testing.T) {
	scenario := apiScenario()
	for i := range scenario.Rules {
		rule := &scenario.Rules[i]
		rule.RuleJSON = []byte(strings.Replace(string(rule.RuleJSON), `"facts":[]`, `"facts":[{"name":"latencyMs","type":"int64","scope":"object"}]`, 1))
	}
	runtime, err := simulator.NewSimulator(scenario)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	server := httptest.NewServer(NewHandler(NewSimulatorAdapter(runtime)))
	defer server.Close()
	response := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/v1/tickets/batch", `{"count":2,"seed":7,"startTicketId":700,"rule":{"namespace":"api","ruleId":1},"placementId":"17745680053102999587","createdAtStart":1788639000000,"createdAtStep":250,"affinityPrefix":"5","requestIdPrefix":"6","objectFacts":{"int64Values":{"latencyMs":42}},"atomic":true}`)
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
		if view.Ticket.CreatedAt != 1788639000000+int64(index)*250 || view.Facts.Int64Values["latencyMs"] != 42 {
			encoded, _ := json.Marshal(view)
			t.Fatalf("lost composer values: %s", encoded)
		}
	}
	// 前缀参与路由，不作为观测 DTO 字段。
	inputs, err := simulator.GenerateBatch(generatorSpec(CustomTicketsRequest{Rule: RuleKey{Namespace: "api", RuleID: 1}, Count: 1, StartTicketID: 9, AffinityPrefix: "5", RequestIDPrefix: "6"}))
	if err != nil || inputs[0].AffinityKey != "59" || inputs[0].RequestID != "69" {
		t.Fatalf("prefixes: %+v %v", inputs, err)
	}
}
