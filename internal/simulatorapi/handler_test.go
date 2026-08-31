package simulatorapi

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"matchSystem/internal/identity"
	"matchSystem/internal/simulator"
)

type fakeService struct {
	health       HealthResponse
	capabilities CapabilitiesResponse
	scenario     ScenarioResponse
	topology     TopologyResponse
	tickets      TicketPage
	matches      MatchPage
	validation   ValidationResponse
	round        RoundResponse
	created      TicketView
	batch        TicketBatchResponse
	events       []Event
	err          error
	lastScenario ScenarioRequest
	lastValidate ValidateRuleRequest
	lastTicket   TicketCreateRequest
	lastCustom   CustomTicketsRequest
	lastBatch    TicketBatchRequest
	lastRound    RoundRequest
	lastDeleted  string
}

func (f *fakeService) Health(context.Context) (HealthResponse, error) {
	return f.health, f.err
}
func (f *fakeService) Capabilities(context.Context) (CapabilitiesResponse, error) {
	return f.capabilities, f.err
}
func (f *fakeService) GetScenario(context.Context) (ScenarioResponse, error) {
	return f.scenario, f.err
}
func (f *fakeService) ReplaceScenario(_ context.Context, request ScenarioRequest) (ScenarioResponse, error) {
	f.lastScenario = request
	return f.scenario, f.err
}
func (f *fakeService) ValidateRule(_ context.Context, request ValidateRuleRequest) (ValidationResponse, error) {
	f.lastValidate = request
	return f.validation, f.err
}
func (f *fakeService) Topology(context.Context) (TopologyResponse, error) {
	return f.topology, f.err
}
func (f *fakeService) ListTickets(context.Context, TicketListQuery) (TicketPage, error) {
	return f.tickets, f.err
}
func (f *fakeService) CreateTicket(_ context.Context, request TicketCreateRequest) (TicketView, error) {
	f.lastTicket = request
	return f.created, f.err
}
func (f *fakeService) CreateCustomTickets(_ context.Context, request CustomTicketsRequest) (TicketBatchResponse, error) {
	f.lastCustom = request
	return f.batch, f.err
}
func (f *fakeService) CreateTicketsBatch(_ context.Context, request TicketBatchRequest) (TicketBatchResponse, error) {
	f.lastBatch = request
	return f.batch, f.err
}
func (f *fakeService) DeleteTicket(_ context.Context, ticketID string) (DeleteTicketResponse, error) {
	f.lastDeleted = ticketID
	return DeleteTicketResponse{TicketID: 7, Deleted: true}, f.err
}
func (f *fakeService) RunRound(_ context.Context, request RoundRequest) (RoundResponse, error) {
	f.lastRound = request
	return f.round, f.err
}
func (f *fakeService) ListMatches(context.Context, MatchListQuery) (MatchPage, error) {
	return f.matches, f.err
}
func (f *fakeService) SubscribeEvents(context.Context, EventQuery) (<-chan Event, error) {
	if f.err != nil {
		return nil, f.err
	}
	stream := make(chan Event, len(f.events))
	for _, event := range f.events {
		stream <- event
	}
	close(stream)
	return stream, nil
}

func TestHandlerRoutesAndJSONWire(t *testing.T) {
	service := &fakeService{
		health:       HealthResponse{Status: "ok", Service: "test"},
		capabilities: CapabilitiesResponse{SchemaVersions: []string{"logical-node-contract/v3"}},
		scenario:     ScenarioResponse{Revision: "r1", Scenario: json.RawMessage(`{"rules":[]}`)},
		validation:   ValidationResponse{Valid: true},
		topology:     TopologyResponse{PhysicalNodes: []PhysicalNodeStatus{}},
		created:      TicketView{Ticket: Ticket{TicketID: 9, CreatedAt: 10}, State: "waiting"},
		batch:        TicketBatchResponse{Accepted: 2, Rejected: 0, GeneratorID: "g-1", Seed: 4},
		round:        RoundResponse{RoundID: "round-1", Produced: 1},
	}
	server := httptest.NewServer(NewHandler(service, WithAllowedOrigins("https://ui.example"), WithAllowCredentials(true)))
	defer server.Close()

	client := server.Client()
	get := func(path string) *http.Response {
		request, err := http.NewRequest(http.MethodGet, server.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Origin", "https://ui.example")
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
	response := get("/api/v1/health")
	if response.StatusCode != http.StatusOK || response.Header.Get("Access-Control-Allow-Origin") != "https://ui.example" || response.Header.Get("Access-Control-Allow-Credentials") != "true" {
		t.Fatalf("health/CORS: status=%d headers=%v", response.StatusCode, response.Header)
	}
	response.Body.Close()

	response = get("/api/v1/capabilities")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("capabilities status=%d", response.StatusCode)
	}
	response.Body.Close()
	response = get("/api/v1/topology")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("topology status=%d", response.StatusCode)
	}
	response.Body.Close()

	response = get("/api/v1/scenario")
	var scenario ScenarioResponse
	decodeResponse(t, response, &scenario)
	if scenario.Revision != "r1" || string(scenario.Scenario) != `{"rules":[]}` {
		t.Fatalf("scenario response: %#v", scenario)
	}

	put := doJSON(t, client, http.MethodPut, server.URL+"/api/v1/scenario", `{"scenario":{"rules":[1]}}`)
	if put.StatusCode != http.StatusOK || string(service.lastScenario.Scenario) != `{"rules":[1]}` {
		t.Fatalf("scenario replace: status=%d request=%s", put.StatusCode, service.lastScenario.Scenario)
	}
	put.Body.Close()

	validate := doJSON(t, client, http.MethodPost, server.URL+"/api/v1/rules/validate", `{"rule":{}}`)
	if validate.StatusCode != http.StatusOK || len(service.lastValidate.Rule) == 0 {
		t.Fatalf("validate: status=%d request=%#v", validate.StatusCode, service.lastValidate)
	}
	validate.Body.Close()

	ticketBody := `{"ticket":{"ticketId":9,"createdAt":10,"stringLists":{"region":["cn"]}},"rule":{"namespace":"demo","ruleId":1},"placementId":"p1","facts":{"stringLists":{"latency":["low"]}}}`
	created := doJSON(t, client, http.MethodPost, server.URL+"/api/v1/tickets", ticketBody)
	if created.StatusCode != http.StatusCreated || service.lastTicket.Rule.RuleID != 1 {
		t.Fatalf("create ticket: status=%d request=%#v", created.StatusCode, service.lastTicket)
	}
	created.Body.Close()

	custom := doJSON(t, client, http.MethodPost, server.URL+"/api/v1/tickets/custom", `{"count":2,"seed":4,"rule":{"ruleId":1}}`)
	if custom.StatusCode != http.StatusCreated || service.lastCustom.Count != 2 {
		t.Fatalf("custom: status=%d request=%#v", custom.StatusCode, service.lastCustom)
	}
	custom.Body.Close()

	batch := doJSON(t, client, http.MethodPost, server.URL+"/api/v1/tickets/batch", `{"tickets":[{"ticket":{"ticketId":9},"rule":{"ruleId":1}}]}`)
	if batch.StatusCode != http.StatusCreated || len(service.lastBatch.Tickets) != 1 {
		t.Fatalf("batch: status=%d request=%#v", batch.StatusCode, service.lastBatch)
	}
	batch.Body.Close()

	deleted := doJSON(t, client, http.MethodDelete, server.URL+"/api/v1/tickets/7", "")
	if deleted.StatusCode != http.StatusOK || service.lastDeleted != "7" {
		t.Fatalf("delete: status=%d id=%q", deleted.StatusCode, service.lastDeleted)
	}
	deleted.Body.Close()

	round := doJSON(t, client, http.MethodPost, server.URL+"/api/v1/rounds", `{"now":42,"maxMatches":3}`)
	if round.StatusCode != http.StatusOK || service.lastRound.Now == nil || *service.lastRound.Now != 42 {
		t.Fatalf("round: status=%d request=%#v", round.StatusCode, service.lastRound)
	}
	round.Body.Close()

	list := get("/api/v1/tickets?limit=10&state=waiting&search=demo")
	if list.StatusCode != http.StatusOK {
		t.Fatalf("list tickets status=%d", list.StatusCode)
	}
	list.Body.Close()

	matches := get("/api/v1/matches?limit=10")
	if matches.StatusCode != http.StatusOK {
		t.Fatalf("list matches status=%d", matches.StatusCode)
	}
	matches.Body.Close()
}

func TestHandlerStructuredErrorsAndMethods(t *testing.T) {
	service := &fakeService{err: NewServiceError(http.StatusServiceUnavailable, "RUNTIME_DOWN", "runtime unavailable")}
	handler := NewHandler(service)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("service error status=%d", recorder.Code)
	}
	var body ErrorResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Code != "RUNTIME_DOWN" || body.Error.Path != "/api/v1/health" {
		t.Fatalf("structured error=%#v", body)
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/unknown", nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("unknown endpoint status=%d", recorder.Code)
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/scenario", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("scenario runtime error status=%d", recorder.Code)
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/health?limit=0", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("health query should not be parsed status=%d", recorder.Code)
	}

	limitedHandler := NewHandler(&fakeService{}, WithMaxBodyBytes(8))
	recorder = httptest.NewRecorder()
	limitedRequest := httptest.NewRequest(http.MethodPost, "/api/v1/rounds", strings.NewReader(`{"now":123456789}`))
	limitedHandler.ServeHTTP(recorder, limitedRequest)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("oversized body status=%d", recorder.Code)
	}
}

func TestHandlerSSEUsesDefaultMessageJSON(t *testing.T) {
	service := &fakeService{events: []Event{{ID: "1", Type: "ticket_added", At: "2026-01-01T00:00:00Z", Payload: map[string]any{"ticketId": float64(1)}}}}
	recorder := httptest.NewRecorder()
	handler := NewHandler(service)
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/events", nil))
	if recorder.Code != http.StatusOK || !strings.HasPrefix(recorder.Header().Get("Content-Type"), "text/event-stream") {
		t.Fatalf("SSE response status=%d headers=%v", recorder.Code, recorder.Header())
	}
	text := recorder.Body.String()
	if !strings.Contains(text, "id: 1\n") || !strings.Contains(text, `data: {"id":"1","type":"ticket_added"`) || strings.Contains(text, "event: ticket_added") {
		t.Fatalf("unexpected SSE frame: %q", text)
	}
}

func TestSimulatorAdapterRuntimeHTTP(t *testing.T) {
	key := identity.LogicalNodeKey{Rule: identity.RuleKey{Namespace: "e2e", RuleID: 1}, PlacementID: "p1"}
	scenario := simulator.Scenario{
		SchemaVersion: simulator.ScenarioSchemaVersion,
		PhysicalNodes: []simulator.PhysicalNodeSpec{simulator.NewPhysicalNodeSpec("p1", "inproc://p1")},
		Rules:         []simulator.RuleSpec{simulator.NewRuleSpec(key, "p1", apiRuleJSON("e2e", 1))},
	}
	runtime, err := simulator.NewService(scenario)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	defer runtime.Close()
	server := httptest.NewServer(NewHandler(NewSimulatorAdapter(runtime)))
	defer server.Close()

	response, err := server.Client().Get(server.URL + "/api/v1/health")
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("health status=%d", response.StatusCode)
	}
	response.Body.Close()

	response, err = server.Client().Get(server.URL + "/api/v1/capabilities")
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("capabilities status=%d", response.StatusCode)
	}
	response.Body.Close()
	response, err = server.Client().Get(server.URL + "/api/v1/scenario")
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("scenario status=%d", response.StatusCode)
	}
	response.Body.Close()

	validateBody := `{"rule":` + string(apiRuleJSON("e2e", 1)) + `}`
	response = doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/v1/rules/validate", validateBody)
	if response.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(response.Body)
		t.Fatalf("validate status=%d body=%s", response.StatusCode, data)
	}
	response.Body.Close()

	scenarioPayload, err := json.Marshal(scenario)
	if err != nil {
		t.Fatal(err)
	}
	response = doJSON(t, server.Client(), http.MethodPut, server.URL+"/api/v1/scenario", `{"scenario":`+string(scenarioPayload)+`}`)
	if response.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(response.Body)
		t.Fatalf("replace scenario status=%d body=%s", response.StatusCode, data)
	}
	response.Body.Close()

	body := `{"ticket":{"ticketId":1,"createdAt":100,"stringLists":{},"uint64Lists":{},"int64Values":{}},"rule":{"namespace":"e2e","ruleId":1},"facts":{"stringLists":{},"uint64Lists":{},"int64Values":{}}}`
	response = doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/v1/tickets", body)
	if response.StatusCode != http.StatusCreated {
		data, _ := io.ReadAll(response.Body)
		t.Fatalf("create status=%d body=%s", response.StatusCode, data)
	}
	response.Body.Close()

	response, err = server.Client().Get(server.URL + "/api/v1/tickets?limit=10&state=waiting")
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("tickets status=%d", response.StatusCode)
	}
	response.Body.Close()

	response = doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/v1/tickets/custom", `{"count":1,"seed":2,"rule":{"namespace":"e2e","ruleId":1},"startTicketId":10,"createdAtStart":200}`)
	if response.StatusCode != http.StatusCreated {
		data, _ := io.ReadAll(response.Body)
		t.Fatalf("custom batch status=%d body=%s", response.StatusCode, data)
	}
	response.Body.Close()

	response = doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/v1/tickets/batch", `{"tickets":[{"ticket":{"ticketId":20,"createdAt":300},"rule":{"namespace":"e2e","ruleId":1}}]}`)
	if response.StatusCode != http.StatusCreated {
		data, _ := io.ReadAll(response.Body)
		t.Fatalf("ticket batch status=%d body=%s", response.StatusCode, data)
	}
	response.Body.Close()

	response = doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/v1/rounds", `{"now":1000,"maxMatches":1}`)
	if response.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(response.Body)
		t.Fatalf("round status=%d body=%s", response.StatusCode, data)
	}
	response.Body.Close()

	response = doJSON(t, server.Client(), http.MethodDelete, server.URL+"/api/v1/tickets/10", "")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("delete status=%d", response.StatusCode)
	}
	response.Body.Close()

	response, err = server.Client().Get(server.URL + "/api/v1/matches")
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("matches status=%d", response.StatusCode)
	}
	var matchPage MatchPage
	if err := json.NewDecoder(response.Body).Decode(&matchPage); err != nil {
		response.Body.Close()
		t.Fatalf("decode matches: %v", err)
	}
	response.Body.Close()
	if len(matchPage.Items) != 1 || matchPage.Items[0].MatchID == "" {
		t.Fatalf("unexpected match page: %#v", matchPage)
	}
	matchID := matchPage.Items[0].MatchID
	response, err = server.Client().Get(server.URL + "/api/v1/matches/" + matchID)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(response.Body)
		response.Body.Close()
		t.Fatalf("match detail status=%d body=%s", response.StatusCode, data)
	}
	var detail MatchView
	if err := json.NewDecoder(response.Body).Decode(&detail); err != nil {
		response.Body.Close()
		t.Fatalf("decode match detail: %v", err)
	}
	response.Body.Close()
	if detail.MatchID != matchID || detail.Round == 0 || detail.PhysicalNodeID != "p1" ||
		detail.LogicalNode.Rule.Namespace != "e2e" || detail.LogicalNode.PlacementID != "p1" ||
		len(detail.Tickets) != 2 || len(detail.Members) != 2 || detail.Members[0].State != "matched" {
		t.Fatalf("unexpected match detail: %#v", detail)
	}
	response, err = server.Client().Get(server.URL + "/api/v1/matches/match-does-not-exist")
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusNotFound {
		data, _ := io.ReadAll(response.Body)
		response.Body.Close()
		t.Fatalf("missing match detail status=%d body=%s", response.StatusCode, data)
	}
	response.Body.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/v1/events", nil)
	response, err = server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("events status=%d", response.StatusCode)
	}
	scanner := bufio.NewScanner(response.Body)
	foundData := false
	for scanner.Scan() {
		if strings.HasPrefix(scanner.Text(), "data: ") {
			foundData = true
			break
		}
	}
	if !foundData {
		t.Fatalf("runtime event stream did not emit data: %v", scanner.Err())
	}
}

func doJSON(t *testing.T, client *http.Client, method, url, body string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func decodeResponse(t *testing.T, response *http.Response, destination any) {
	t.Helper()
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		data, _ := io.ReadAll(response.Body)
		t.Fatalf("response status=%d body=%s", response.StatusCode, data)
	}
	if err := json.NewDecoder(response.Body).Decode(destination); err != nil {
		t.Fatal(err)
	}
}

func apiScenario() simulator.Scenario {
	rule := identity.RuleKey{Namespace: "api", RuleID: 1}
	ruleJSON := apiRuleJSON(rule.Namespace, rule.RuleID)
	return simulator.Scenario{
		SchemaVersion: simulator.ScenarioSchemaVersion,
		PhysicalNodes: []simulator.PhysicalNodeSpec{
			simulator.NewPhysicalNodeSpec("p1", "inproc://p1"),
			simulator.NewPhysicalNodeSpec("p2", "inproc://p2"),
		},
		Rules: []simulator.RuleSpec{
			simulator.NewRuleSpec(identity.LogicalNodeKey{Rule: rule, PlacementID: "p1"}, "p1", ruleJSON),
			simulator.NewRuleSpec(identity.LogicalNodeKey{Rule: rule, PlacementID: "p2"}, "p2", ruleJSON),
		},
	}
}

func apiRuleJSON(namespace string, ruleID int32) []byte {
	return []byte(fmt.Sprintf(`{"schemaVersion":"match-rule/v1","ruleKey":{"namespace":%q,"ruleId":%d},"contract":{"schemaVersion":"logical-node-contract/v3","attributes":[],"facts":[],"indexes":[]},"prefilter":{"schemaVersion":"prefilter/v3","bitmap":{"resultType":"bitmap","expr":{"op":"none"}}},"evaluation":{"schemaVersion":"evaluation/v3","canJoin":{"schemaVersion":"expression-scalar/v3","resultType":"bool","expr":{"op":"bool_literal","value":true}},"canComplete":{"schemaVersion":"expression-scalar/v3","resultType":"bool","expr":{"op":"bool_literal","value":true}}},"scoring":{"type":"constant","params":{"value":0}},"seedSelection":{"type":"arrival","params":{}},"runtime":{"candidateLimitPerSeed":128,"maxPlayers":8,"attemptLimitPerProduceMatch":500,"attemptLimitPerMatchRound":500}}`, namespace, ruleID))
}

func TestSimulatorAdapterPlacementIsExact(t *testing.T) {
	runtime, err := simulator.NewSimulator(apiScenario())
	if err != nil {
		t.Fatalf("NewSimulator: %v", err)
	}
	defer runtime.Close()
	adapter := NewSimulatorAdapter(runtime)
	request := TicketCreateRequest{
		Ticket:      Ticket{TicketID: 1},
		Rule:        RuleKey{Namespace: "api", RuleID: 1},
		PlacementID: "p2",
	}
	view, err := adapter.CreateTicket(context.Background(), request)
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	if view.Owner.PhysicalNodeID != "p2" || view.Owner.LogicalNode.PlacementID != "p2" {
		t.Fatalf("placement was not exact: %#v", view.Owner)
	}
	_, err = adapter.CreateTicket(context.Background(), TicketCreateRequest{
		Ticket:      Ticket{TicketID: 2},
		Rule:        request.Rule,
		PlacementID: "missing",
	})
	serviceErr, ok := err.(*ServiceError)
	if !ok || serviceErr.Status != 404 || serviceErr.Code != "PLACEMENT_NOT_FOUND" {
		t.Fatalf("missing placement error = %#v, want 404 PLACEMENT_NOT_FOUND", err)
	}
}

// cancelAfterSwapContext models a request whose cancellation is observed
// immediately after ReplaceScenario has committed the new runtime. The
// adapter must still return the committed scenario response rather than
// reporting an ambiguous post-commit failure.
type cancelAfterSwapContext struct {
	checks int
}

func (c *cancelAfterSwapContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (c *cancelAfterSwapContext) Done() <-chan struct{}       { return nil }
func (c *cancelAfterSwapContext) Err() error {
	c.checks++
	if c.checks >= 3 {
		return context.Canceled
	}
	return nil
}
func (c *cancelAfterSwapContext) Value(any) any { return nil }

func TestSimulatorAdapterReplaceScenarioReturnsCommittedResponseAfterCancellation(t *testing.T) {
	runtime, err := simulator.NewSimulator(apiScenario())
	if err != nil {
		t.Fatalf("NewSimulator: %v", err)
	}
	defer runtime.Close()
	adapter := NewSimulatorAdapter(runtime)
	payload, err := json.Marshal(apiScenario())
	if err != nil {
		t.Fatalf("Marshal scenario: %v", err)
	}

	requestContext := &cancelAfterSwapContext{}
	response, err := adapter.ReplaceScenario(requestContext, ScenarioRequest{Scenario: payload})
	if err != nil {
		t.Fatalf("ReplaceScenario after committed swap: %v", err)
	}
	if len(response.Scenario) == 0 {
		t.Fatal("ReplaceScenario returned an empty committed scenario")
	}
	if requestContext.checks != 2 {
		t.Fatalf("ReplaceScenario consulted request context after the swap: checks=%d", requestContext.checks)
	}

	current, err := runtime.GetScenario(context.Background())
	if err != nil {
		t.Fatalf("GetScenario after replacement: %v", err)
	}
	if len(current.Rules) != len(apiScenario().Rules) {
		t.Fatalf("replacement was not committed: rules=%d", len(current.Rules))
	}
}

func TestSimulatorAdapterConcurrentReplaceScenarioResponsesRemainRequestSpecific(t *testing.T) {
	runtime, err := simulator.NewSimulator(apiScenario())
	if err != nil {
		t.Fatalf("NewSimulator: %v", err)
	}
	defer runtime.Close()
	adapter := NewSimulatorAdapter(runtime)
	scenarios := []simulator.Scenario{apiScenario(), apiScenario()}
	scenarios[0].MatchHistoryLimit = 11
	scenarios[1].MatchHistoryLimit = 22
	payloads := make([][]byte, len(scenarios))
	for index, scenario := range scenarios {
		payloads[index], err = json.Marshal(scenario)
		if err != nil {
			t.Fatalf("Marshal scenario %d: %v", index, err)
		}
	}

	const replacements = 12
	type replacementResult struct {
		expected int
		actual   ScenarioResponse
		err      error
	}
	start := make(chan struct{})
	results := make(chan replacementResult, replacements)
	for index := 0; index < replacements; index++ {
		index := index
		go func() {
			<-start
			actual, replaceErr := adapter.ReplaceScenario(context.Background(), ScenarioRequest{Scenario: payloads[index%len(payloads)]})
			results <- replacementResult{expected: scenarios[index%len(scenarios)].MatchHistoryLimit, actual: actual, err: replaceErr}
		}()
	}
	close(start)
	for index := 0; index < replacements; index++ {
		result := <-results
		if result.err != nil {
			t.Fatalf("concurrent adapter ReplaceScenario: %v", result.err)
		}
		var actual simulator.Scenario
		if err := json.Unmarshal(result.actual.Scenario, &actual); err != nil {
			t.Fatalf("decode committed scenario: %v", err)
		}
		if actual.MatchHistoryLimit != result.expected {
			t.Fatalf("adapter returned a later scenario: got limit=%d, want %d", actual.MatchHistoryLimit, result.expected)
		}
	}
}

func TestSimulatorAdapterAtomicBatchRollsBackExplicitAndGenerated(t *testing.T) {
	runtime, err := simulator.NewSimulator(apiScenario())
	if err != nil {
		t.Fatalf("NewSimulator: %v", err)
	}
	defer runtime.Close()
	adapter := NewSimulatorAdapter(runtime)
	rule := RuleKey{Namespace: "api", RuleID: 1}
	_, err = adapter.CreateTicketsBatch(context.Background(), TicketBatchRequest{
		Atomic: true,
		Tickets: []TicketCreateRequest{
			{Ticket: Ticket{TicketID: 10}, Rule: rule, PlacementID: "p1"},
			{Ticket: Ticket{TicketID: 10}, Rule: rule, PlacementID: "p1"},
		},
	})
	if err == nil {
		t.Fatal("explicit atomic batch unexpectedly succeeded")
	}
	page, err := runtime.ListTickets(context.Background(), simulator.TicketQuery{Limit: 10})
	if err != nil {
		t.Fatalf("ListTickets after explicit rollback: %v", err)
	}
	if page.Total != 0 {
		t.Fatalf("explicit atomic batch left %d tickets", page.Total)
	}

	if _, err := adapter.CreateTicket(context.Background(), TicketCreateRequest{Ticket: Ticket{TicketID: 21}, Rule: rule, PlacementID: "p1"}); err != nil {
		t.Fatalf("seed existing ticket: %v", err)
	}
	_, err = adapter.CreateTicketsBatch(context.Background(), TicketBatchRequest{
		Atomic:        true,
		Count:         2,
		Rule:          rule,
		PlacementID:   "p1",
		StartTicketID: 20,
	})
	if err == nil {
		t.Fatal("generated atomic batch unexpectedly succeeded")
	}
	page, err = runtime.ListTickets(context.Background(), simulator.TicketQuery{Limit: 10})
	if err != nil {
		t.Fatalf("ListTickets after generated rollback: %v", err)
	}
	if page.Total != 1 || page.Items[0].TicketID != 21 {
		t.Fatalf("generated atomic rollback changed existing data: %#v", page.Items)
	}
}

func TestSimulatorAPIRejectsUnsafeTicketIDsAndNullScenario(t *testing.T) {
	runtime, err := simulator.NewSimulator(apiScenario())
	if err != nil {
		t.Fatalf("NewSimulator: %v", err)
	}
	defer runtime.Close()
	server := httptest.NewServer(NewHandler(NewSimulatorAdapter(runtime)))
	defer server.Close()
	rule := RuleKey{Namespace: "api", RuleID: 1}
	adapter := NewSimulatorAdapter(runtime)
	if _, err := adapter.CreateTicket(context.Background(), TicketCreateRequest{Ticket: Ticket{TicketID: MaxWireTicketID}, Rule: rule, PlacementID: "p1"}); err != nil {
		t.Fatalf("maximum safe ticket id rejected: %v", err)
	}
	if _, err := adapter.CreateTicket(context.Background(), TicketCreateRequest{Ticket: Ticket{TicketID: MaxWireTicketID + 1}, Rule: rule, PlacementID: "p1"}); err == nil {
		t.Fatal("unsafe ticket id was accepted")
	}
	response := doJSON(t, server.Client(), http.MethodPut, server.URL+"/api/v1/scenario", `{"scenario":null}`)
	if response.StatusCode != http.StatusBadRequest {
		data, _ := io.ReadAll(response.Body)
		response.Body.Close()
		t.Fatalf("null scenario status=%d body=%s", response.StatusCode, data)
	}
	response.Body.Close()
	scenario, err := runtime.GetScenario(context.Background())
	if err != nil || len(scenario.Rules) != 2 {
		t.Fatalf("null scenario changed runtime: err=%v rules=%d", err, len(scenario.Rules))
	}
	response = doJSON(t, server.Client(), http.MethodDelete, server.URL+"/api/v1/tickets/123456789", "")
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("missing delete status=%d, want 404", response.StatusCode)
	}
	response.Body.Close()
}

func TestSimulatorAPIExposesCapabilitiesAndRejectsUnsupportedGeneratorOptions(t *testing.T) {
	runtime, err := simulator.NewSimulator(simulator.Scenario{})
	if err != nil {
		t.Fatalf("NewSimulator: %v", err)
	}
	defer runtime.Close()
	capabilities, err := NewSimulatorAdapter(runtime).Capabilities(context.Background())
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	if len(capabilities.Selectors) == 0 || len(capabilities.CandidateScorers) == 0 || len(capabilities.SeedSelections) == 0 || len(capabilities.FactTypes) == 0 || len(capabilities.ScalarOperators) == 0 || len(capabilities.BitmapOperators) == 0 {
		t.Fatalf("capability catalog was truncated: %#v", capabilities)
	}
	foundFields := false
	for _, operator := range capabilities.ScalarOperators {
		if operator.Name == "strings_contains" {
			for _, field := range operator.Fields {
				if field == "needle" {
					foundFields = true
				}
			}
		}
	}
	if !foundFields {
		t.Fatal("detailed operator fields were not transported")
	}

	server := httptest.NewServer(NewHandler(&fakeService{}))
	defer server.Close()
	response := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/v1/tickets/custom", `{"count":1,"rule":{"ruleId":1},"generator":{"distribution":"random"}}`)
	if response.StatusCode != http.StatusBadRequest {
		data, _ := io.ReadAll(response.Body)
		response.Body.Close()
		t.Fatalf("unsupported generator option status=%d body=%s", response.StatusCode, data)
	}
	response.Body.Close()
	response = doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/v1/tickets/custom", `{"count":0,"rule":{"ruleId":1}}`)
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("zero custom count status=%d, want 400", response.StatusCode)
	}
	response.Body.Close()
}
