package simulatorapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/xianyu11-x/matchSystem/internal/simulator"
	"github.com/xianyu11-x/matchSystem/pkg/identity"
)

type physicalNodeFakeService struct {
	fakeService
	created PhysicalNodeCreateRequest
	removed string
}

func (f *physicalNodeFakeService) AddPhysicalNode(_ context.Context, request PhysicalNodeCreateRequest) error {
	f.created = request
	return f.err
}

func (f *physicalNodeFakeService) RemovePhysicalNode(_ context.Context, id string) error {
	f.removed = id
	return f.err
}

type physicalNodeDeploymentFakeService struct {
	physicalNodeFakeService
	added   PhysicalNodeCreateRequest
	updated PhysicalNodeCreateRequest
}

func (f *physicalNodeDeploymentFakeService) AddPhysicalNodeFromLibrary(_ context.Context, request PhysicalNodeCreateRequest) error {
	f.added = request
	return f.err
}

func (f *physicalNodeDeploymentFakeService) UpdatePhysicalNodeFromLibrary(_ context.Context, request PhysicalNodeCreateRequest) error {
	f.updated = request
	return f.err
}

func TestPhysicalNodeDeploymentHTTPWire(t *testing.T) {
	service := &physicalNodeDeploymentFakeService{}
	handler := NewHandler(service)
	const body = `{"physicalNode":{"id":"7452203632095078013","endpoint":"inproc://one","enabled":true},"deployments":[{"logicalNode":{"rule":{"namespace":"demo","ruleId":1},"placementId":"17745680053102999587"},"weight":2,"enabled":true,"tickFacts":{"int64s":{"f":3}},"pythonProvider":{"script":"provider.py"},"applyLatest":true}]}`
	for _, method := range []string{http.MethodPost, http.MethodPut} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(method, "/api/v1/physical-nodes", strings.NewReader(body)))
		if recorder.Code != http.StatusNoContent || recorder.Body.Len() != 0 {
			t.Fatalf("%s status=%d body=%q", method, recorder.Code, recorder.Body.String())
		}
	}
	for _, request := range []PhysicalNodeCreateRequest{service.added, service.updated} {
		if request.PhysicalNode == nil || request.PhysicalNode.ID != testPhysical("n1") || len(request.Deployments) != 1 || request.Rules != nil {
			t.Fatalf("deployment request=%#v", request)
		}
		encoded, err := json.Marshal(request.Deployments[0])
		if err != nil || !bytes.Contains(encoded, []byte(`"applyLatest":true`)) || !bytes.Contains(encoded, []byte(`"provider.py"`)) || !bytes.Contains(encoded, []byte(fmt.Sprintf(`"placementId":"%d"`, testPlacement("p1")))) {
			t.Fatalf("deployment=%s err=%v", encoded, err)
		}
	}
	keepBody := strings.Replace(body, `"applyLatest":true`, `"applyLatest":true,"keepTickFacts":true`, 1)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPut, "/api/v1/physical-nodes", strings.NewReader(keepBody)))
	if recorder.Code != http.StatusNoContent || len(service.updated.Deployments) != 1 || !service.updated.Deployments[0].KeepTickFacts {
		t.Fatalf("keepTickFacts status=%d body=%s request=%#v", recorder.Code, recorder.Body.String(), service.updated)
	}
	for _, entry := range []struct{ method, body string }{
		{http.MethodPost, `{"physicalNode":{"id":"17745680053102999587","endpoint":"e","enabled":true},"rules":[],"deployments":[]}`},
		{http.MethodPut, `{"physicalNode":{"id":"17745680053102999587","endpoint":"e","enabled":true}}`},
		{http.MethodPut, `{"physicalNode":{"id":"17745680053102999587","endpoint":"e","enabled":true},"rules":[]}`},
		{http.MethodPut, `{"physicalNode":{"id":"17745680053102999587","endpoint":"e","enabled":true},"deployments":null}`},
		{http.MethodPut, `{"physicalNode":{"id":"17745680053102999587","endpoint":"e","enabled":true},"deployments":[{"unknown":true}]}`},
	} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(entry.method, "/api/v1/physical-nodes", strings.NewReader(entry.body)))
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("%s body=%s status=%d response=%s", entry.method, entry.body, recorder.Code, recorder.Body.String())
		}
	}
	recorder = httptest.NewRecorder()
	NewHandler(&physicalNodeFakeService{}).ServeHTTP(recorder, httptest.NewRequest(http.MethodPut, "/api/v1/physical-nodes", strings.NewReader(`{"physicalNode":{"id":"17745680053102999587","endpoint":"e","enabled":true},"deployments":[]}`)))
	if recorder.Code != http.StatusNotImplemented {
		t.Fatalf("legacy service update status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestPhysicalNodeHTTPKeepsExactTickFacts(t *testing.T) {
	scenario := apiScenario()
	scenario.PhysicalNodes = scenario.PhysicalNodes[:1]
	scenario.Rules = scenario.Rules[:1]
	scenario.Rules[0].RuleJSON = bytes.Replace(scenario.Rules[0].RuleJSON, []byte(`"facts":[]`), []byte(`"facts":[{"name":"waitingCount","type":"int64","scope":"tick"}]`), 1)
	scenario.Rules[0].TickFacts.Int64Values = map[string]int64{"waitingCount": 9223372036854775807}
	runtime, err := simulator.NewSimulator(scenario)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	server := httptest.NewServer(NewHandler(NewSimulatorAdapter(runtime)))
	defer server.Close()
	client := server.Client()

	response := doJSON(t, client, http.MethodPost, server.URL+"/api/v1/tickets", `{"ticket":{"ticketId":44},"rule":{"namespace":"api","ruleId":1},"placementId":"17745680053102999587"}`)
	response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create ticket status=%d", response.StatusCode)
	}
	body := `{"physicalNode":{"id":"17745680053102999587","endpoint":"inproc://changed","enabled":true},"deployments":[{"logicalNode":{"rule":{"namespace":"api","ruleId":1},"placementId":"17745680053102999587"},"weight":2,"enabled":true,"tickFacts":{"int64s":{"waitingCount":1}},"keepTickFacts":true}]}`
	response = doJSON(t, client, http.MethodPut, server.URL+"/api/v1/physical-nodes", body)
	data, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("update status=%d body=%s", response.StatusCode, data)
	}
	current, err := runtime.GetScenario(context.Background())
	if err != nil || current.Rules[0].TickFacts.Int64Values["waitingCount"] != 9223372036854775807 {
		t.Fatalf("tick facts changed: scenario=%#v err=%v", current, err)
	}
	response, err = client.Get(server.URL + "/api/v1/tickets?limit=100")
	if err != nil {
		t.Fatal(err)
	}
	var tickets TicketPage
	decodeResponse(t, response, &tickets)
	if len(tickets.Items) != 1 || tickets.Items[0].Ticket.TicketID != 44 {
		t.Fatalf("ticket not preserved: %#v", tickets)
	}
	newBody := `{"physicalNode":{"id":"4127208643663288636","endpoint":"inproc://p2","enabled":true},"deployments":[{"logicalNode":{"rule":{"namespace":"api","ruleId":1},"placementId":"4127208643663288636"},"weight":1,"enabled":true,"keepTickFacts":true}]}`
	response = doJSON(t, client, http.MethodPost, server.URL+"/api/v1/physical-nodes", newBody)
	data, _ = io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusUnprocessableEntity || !bytes.Contains(data, []byte(`"KEEP_TICK_FACTS_REQUIRES_EXISTING"`)) {
		t.Fatalf("new deployment status=%d body=%s", response.StatusCode, data)
	}
}

func TestPhysicalNodeHandlerDecodesScenarioRulesAndNumericID(t *testing.T) {
	service := &physicalNodeFakeService{}
	handler := NewHandler(service)
	const body = `{"physicalNode":{"id":"11626264156447716647","endpoint":"inproc://one","enabled":false,"selector":"largest_queue"},"rules":[{"logicalNode":{"rule":{"namespace":"demo","ruleId":1},"placementId":"17745680053102999587"},"physicalNodeId":"11626264156447716647","weight":3,"enabled":true,"rule":{"schemaVersion":"match-rule/v1"},"tickFacts":{"int64s":{"f":3}},"pythonProvider":{"script":"provider.py","timeoutMs":123}}]}`
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/physical-nodes", strings.NewReader(body)))
	if recorder.Code != http.StatusNoContent || recorder.Body.Len() != 0 {
		t.Fatalf("add status=%d body=%q", recorder.Code, recorder.Body.String())
	}
	request := service.created
	if request.PhysicalNode == nil || request.PhysicalNode.ID != testPhysical("node/一") || request.PhysicalNode.Enabled == nil || *request.PhysicalNode.Enabled || request.PhysicalNode.Selector != "largest_queue" {
		t.Fatalf("physical declaration=%#v", request.PhysicalNode)
	}
	if len(request.Rules) != 1 || request.Rules[0].LogicalNode.Rule.Namespace != "demo" || request.Rules[0].LogicalNode.PlacementID != testPlacement("p1") || request.Rules[0].PhysicalNodeID != testPhysical("node/一") || request.Rules[0].Weight != 3 || request.Rules[0].TickFacts.Int64Values["f"] != 3 || request.Rules[0].PythonProvider == nil || request.Rules[0].PythonProvider.Script != "provider.py" || request.Rules[0].PythonProvider.TimeoutMS != 123 || string(request.Rules[0].RuleJSON) != `{"schemaVersion":"match-rule/v1"}` {
		t.Fatalf("rule declaration=%#v", request.Rules)
	}

	const numericID = "11626264156447716647"
	query := url.Values{"physicalNodeId": {numericID}}
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodDelete, "/api/v1/physical-nodes?"+query.Encode(), nil))
	if recorder.Code != http.StatusNoContent || recorder.Body.Len() != 0 || service.removed != numericID {
		t.Fatalf("remove status=%d body=%q id=%q", recorder.Code, recorder.Body.String(), service.removed)
	}
}

func TestPhysicalNodeHandlerValidationAndOptionalService(t *testing.T) {
	service := &physicalNodeFakeService{}
	handler := NewHandler(service)
	tests := []struct {
		name   string
		method string
		path   string
		body   string
		status int
	}{
		{"missing node", http.MethodPost, "/api/v1/physical-nodes", `{}`, http.StatusBadRequest},
		{"null node", http.MethodPost, "/api/v1/physical-nodes", `{"physicalNode":null}`, http.StatusBadRequest},
		{"missing enabled", http.MethodPost, "/api/v1/physical-nodes", `{"physicalNode":{"id":"17745680053102999587","endpoint":"inproc://p1"}}`, http.StatusBadRequest},
		{"null enabled", http.MethodPost, "/api/v1/physical-nodes", `{"physicalNode":{"id":"17745680053102999587","endpoint":"inproc://p1","enabled":null}}`, http.StatusBadRequest},
		{"null rules", http.MethodPost, "/api/v1/physical-nodes", `{"physicalNode":{"id":"17745680053102999587","endpoint":"inproc://p1","enabled":true},"rules":null}`, http.StatusBadRequest},
		{"unknown node field", http.MethodPost, "/api/v1/physical-nodes", `{"physicalNode":{"id":"17745680053102999587","endpoint":"inproc://p1","enabled":true,"extra":1}}`, http.StatusBadRequest},
		{"unknown rule field", http.MethodPost, "/api/v1/physical-nodes", `{"physicalNode":{"id":"17745680053102999587","endpoint":"inproc://p1","enabled":true},"rules":[{"extra":1}]}`, http.StatusBadRequest},
		{"missing query", http.MethodDelete, "/api/v1/physical-nodes", "", http.StatusBadRequest},
		{"empty query", http.MethodDelete, "/api/v1/physical-nodes?physicalNodeId=", "", http.StatusBadRequest},
		{"duplicate query", http.MethodDelete, "/api/v1/physical-nodes?physicalNodeId=14598278634844962250&physicalNodeId=4477677635727087946", "", http.StatusBadRequest},
		{"wrong method", http.MethodGet, "/api/v1/physical-nodes", "", http.StatusMethodNotAllowed},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(test.method, test.path, strings.NewReader(test.body)))
			if recorder.Code != test.status {
				t.Fatalf("status=%d body=%s, want %d", recorder.Code, recorder.Body.String(), test.status)
			}
			var response ErrorResponse
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil || response.Error.Code == "" {
				t.Fatalf("error response=%#v err=%v", response, err)
			}
		})
	}

	recorder := httptest.NewRecorder()
	NewHandler(&fakeService{}).ServeHTTP(recorder, httptest.NewRequest(http.MethodDelete, "/api/v1/physical-nodes?physicalNodeId=17745680053102999587", nil))
	if recorder.Code != http.StatusNotImplemented {
		t.Fatalf("legacy service status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	recorder = httptest.NewRecorder()
	NewHandler(nil).ServeHTTP(recorder, httptest.NewRequest(http.MethodDelete, "/api/v1/physical-nodes?physicalNodeId=17745680053102999587", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("nil service status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestPhysicalNodeRuntimeHTTP(t *testing.T) {
	runtime, err := simulator.NewSimulator(apiScenario())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	server := httptest.NewServer(NewHandler(NewSimulatorAdapter(runtime)))
	defer server.Close()
	client := server.Client()

	call := func(method, path, body string, want int) {
		t.Helper()
		response := doJSON(t, client, method, server.URL+path, body)
		defer response.Body.Close()
		data, _ := io.ReadAll(response.Body)
		if response.StatusCode != want {
			t.Fatalf("%s %s: status=%d body=%s, want %d", method, path, response.StatusCode, data, want)
		}
		if want == http.StatusNoContent && len(data) != 0 {
			t.Fatalf("204 response has body %q", data)
		}
	}
	createTicket := func(id int, placement string) {
		t.Helper()
		body := fmt.Sprintf(`{"ticket":{"ticketId":%d,"createdAt":100},"rule":{"namespace":"api","ruleId":1},"placementId":"%d"}`, id, testPlacement(placement))
		call(http.MethodPost, "/api/v1/tickets", body, http.StatusCreated)
	}

	createTicket(1, "p1")
	call(http.MethodPost, "/api/v1/rounds", `{"now":1000,"maxMatches":1}`, http.StatusOK)
	createTicket(2, "p1")
	const newID = "999999999999"
	key := identity.LogicalNodeKey{Rule: identity.RuleKey{Namespace: "api", RuleID: 1}, PlacementID: testPlacement("p3")}
	newRule := simulator.NewRuleSpec(key, testPhysical(newID), apiRuleJSON("api", 1))
	request, err := json.Marshal(map[string]any{
		"physicalNode": simulator.NewPhysicalNodeSpec(testPhysical(newID), "inproc://new"),
		"rules":        []simulator.RuleSpec{newRule},
	})
	if err != nil {
		t.Fatal(err)
	}
	call(http.MethodPost, "/api/v1/physical-nodes", string(request), http.StatusNoContent)
	response := doJSON(t, client, http.MethodPost, server.URL+"/api/v1/physical-nodes", string(request))
	data, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusConflict || !bytes.Contains(data, []byte(`"code":"DUPLICATE_PHYSICAL_NODE"`)) {
		t.Fatalf("duplicate node status=%d body=%s", response.StatusCode, data)
	}
	createTicket(3, "p3")

	response, err = client.Get(server.URL + "/api/v1/topology")
	if err != nil {
		t.Fatal(err)
	}
	var topology TopologyResponse
	decodeResponse(t, response, &topology)
	if len(topology.PhysicalNodes) != 3 {
		t.Fatalf("topology after add=%#v", topology)
	}
	query := url.Values{"physicalNodeId": {newID}}
	call(http.MethodDelete, "/api/v1/physical-nodes?"+query.Encode(), "", http.StatusNoContent)
	call(http.MethodDelete, "/api/v1/physical-nodes?"+query.Encode(), "", http.StatusNoContent)

	response, err = client.Get(server.URL + "/api/v1/tickets?limit=100")
	if err != nil {
		t.Fatal(err)
	}
	var tickets TicketPage
	decodeResponse(t, response, &tickets)
	if len(tickets.Items) != 1 || tickets.Items[0].Ticket.TicketID != 2 || tickets.Items[0].Owner.PhysicalNodeID != testPhysical("p1") {
		t.Fatalf("tickets after remove=%#v", tickets)
	}
	response, err = client.Get(server.URL + "/api/v1/matches")
	if err != nil {
		t.Fatal(err)
	}
	var matches MatchPage
	decodeResponse(t, response, &matches)
	if len(matches.Items) != 1 {
		t.Fatalf("completed history after remove=%#v", matches)
	}

	foreign := newRule
	foreign.PhysicalNodeID = testPhysical("p1")
	foreign.LogicalNode.PlacementID = testPlacement("p4")
	invalid, err := json.Marshal(map[string]any{
		"physicalNode": simulator.NewPhysicalNodeSpec(testPhysical("p4"), "inproc://p4"),
		"rules":        []simulator.RuleSpec{foreign},
	})
	if err != nil {
		t.Fatal(err)
	}
	response = doJSON(t, client, http.MethodPost, server.URL+"/api/v1/physical-nodes", string(invalid))
	defer response.Body.Close()
	data, _ = io.ReadAll(response.Body)
	if response.StatusCode != http.StatusUnprocessableEntity || !bytes.Contains(data, []byte(`"code":"VALIDATION_FAILED"`)) {
		t.Fatalf("foreign placement status=%d body=%s", response.StatusCode, data)
	}
	current, err := runtime.GetScenario(context.Background())
	if err != nil || len(current.PhysicalNodes) != 2 || len(current.Rules) != 2 {
		t.Fatalf("scenario after rejected add=%#v err=%v", current, err)
	}

	call(http.MethodPost, "/api/v1/physical-nodes", `{"physicalNode":{"id":"3322806064301589100","endpoint":"inproc://empty","enabled":true}}`, http.StatusNoContent)
	current, err = runtime.GetScenario(context.Background())
	if err != nil || len(current.PhysicalNodes) != 3 || len(current.Rules) != 2 {
		t.Fatalf("scenario after empty add=%#v err=%v", current, err)
	}
	call(http.MethodDelete, "/api/v1/physical-nodes?physicalNodeId=3322806064301589100", "", http.StatusNoContent)
}
