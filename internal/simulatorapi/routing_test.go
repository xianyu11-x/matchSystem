package simulatorapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/xianyu11-x/matchSystem/internal/simulator"
)

func TestRoutingHTTPUpdatesScenarioAndValidatesInput(t *testing.T) {
	runtime, err := simulator.NewSimulator(apiScenario())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	handler := NewHandler(NewSimulatorAdapter(runtime))
	request := func(method, path, body string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(method, path, strings.NewReader(body)))
		return recorder
	}

	response := request(http.MethodPut, "/api/v1/routing", `{"algorithm":"weighted_priority","ticketLimit":10}`)
	if response.Code != http.StatusOK || strings.TrimSpace(response.Body.String()) != `{"algorithm":"weighted_priority","ticketLimit":10}` {
		t.Fatalf("更新路由失败: status=%d body=%s", response.Code, response.Body.String())
	}
	scenarioResponse := request(http.MethodGet, "/api/v1/scenario", "")
	var scenarioWire struct {
		Scenario simulator.Scenario `json:"scenario"`
	}
	if err := json.Unmarshal(scenarioResponse.Body.Bytes(), &scenarioWire); err != nil || scenarioWire.Scenario.Routing != (simulator.RoutingConfig{Algorithm: simulator.RoutingWeightedPriority, TicketLimit: 10}) {
		t.Fatalf("场景未返回已提交配置: body=%s err=%v", scenarioResponse.Body.String(), err)
	}
	for _, body := range []string{`{"algorithm":"weighted_priority"}`, `{"algorithm":"unknown"}`, `{"ticketLimit":-1}`} {
		response := request(http.MethodPut, "/api/v1/routing", body)
		var result ErrorResponse
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || response.Code != http.StatusUnprocessableEntity || result.Error.Code != "VALIDATION_FAILED" || !strings.Contains(response.Body.String(), "$.routing.") {
			t.Fatalf("配置校验响应错误: body=%s status=%d response=%s", body, response.Code, response.Body.String())
		}
	}
	for _, body := range []string{"null", `{"algorithm":null}`, `{"ticketLimit":null}`, `{"ticketLimit":1.5}`, `{"unknown":true}`, `{"Algorithm":"weighted_rendezvous"}`, `{} {}`} {
		response := request(http.MethodPut, "/api/v1/routing", body)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("无效 JSON 未拒绝: body=%s status=%d response=%s", body, response.Code, response.Body.String())
		}
	}
	scenarioResponse = request(http.MethodGet, "/api/v1/scenario", "")
	if err := json.Unmarshal(scenarioResponse.Body.Bytes(), &scenarioWire); err != nil || scenarioWire.Scenario.Routing.TicketLimit != 10 {
		t.Fatalf("被拒绝的请求改变了路由: body=%s err=%v", scenarioResponse.Body.String(), err)
	}
	response = request(http.MethodPut, "/api/v1/routing", `{}`)
	if response.Code != http.StatusOK || strings.TrimSpace(response.Body.String()) != `{}` {
		t.Fatalf("恢复默认路由失败: status=%d body=%s", response.Code, response.Body.String())
	}
	if got := request(http.MethodGet, "/api/v1/routing", ""); got.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET routing status=%d", got.Code)
	}
	legacy := NewHandler(&fakeService{})
	unsupported := httptest.NewRecorder()
	legacy.ServeHTTP(unsupported, httptest.NewRequest(http.MethodPut, "/api/v1/routing", strings.NewReader(`{}`)))
	if unsupported.Code != http.StatusNotImplemented {
		t.Fatalf("旧服务未返回 501: %d", unsupported.Code)
	}
}

func TestScenarioRoutingHTTPImportExport(t *testing.T) {
	runtime, err := simulator.NewSimulator(simulator.Scenario{})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	handler := NewHandler(NewSimulatorAdapter(runtime))
	scenario := apiScenario()
	scenario.Routing = simulator.RoutingConfig{Algorithm: simulator.RoutingQueueWeightedRandom, TicketLimit: 5}
	scenario.PhysicalNodes[0].RouteUpdateIntervalMS = 1400
	body, err := json.Marshal(struct {
		Scenario simulator.Scenario `json:"scenario"`
	}{Scenario: scenario})
	if err != nil {
		t.Fatal(err)
	}
	put := httptest.NewRecorder()
	handler.ServeHTTP(put, httptest.NewRequest(http.MethodPut, "/api/v1/scenario", strings.NewReader(string(body))))
	if put.Code != http.StatusOK {
		t.Fatalf("导入场景失败: %d %s", put.Code, put.Body.String())
	}
	get := httptest.NewRecorder()
	handler.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/api/v1/scenario", nil))
	var result struct {
		Scenario simulator.Scenario `json:"scenario"`
	}
	if err := json.Unmarshal(get.Body.Bytes(), &result); err != nil || result.Scenario.Routing != scenario.Routing || result.Scenario.PhysicalNodes[0].RouteUpdateIntervalMS != 1400 {
		t.Fatalf("导出场景配置有误: %s err=%v", get.Body.String(), err)
	}
	// 语义错误必须由场景替换接口报告，并保留已提交配置。
	invalid := strings.Replace(string(body), `"algorithm":"queue_weighted_random","ticketLimit":5`, `"algorithm":"weighted_priority","ticketLimit":0`, 1)
	put = httptest.NewRecorder()
	handler.ServeHTTP(put, httptest.NewRequest(http.MethodPut, "/api/v1/scenario", strings.NewReader(invalid)))
	if put.Code != http.StatusUnprocessableEntity || !strings.Contains(put.Body.String(), "INVALID_TICKET_LIMIT") {
		t.Fatalf("错误场景未被拒绝: %d %s", put.Code, put.Body.String())
	}
	get = httptest.NewRecorder()
	handler.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/api/v1/scenario", nil))
	if err := json.Unmarshal(get.Body.Bytes(), &result); err != nil || result.Scenario.Routing != scenario.Routing {
		t.Fatalf("无效导入改变了场景: %s err=%v", get.Body.String(), err)
	}
}

func TestPhysicalNodeRouteIntervalHTTPRoundTrip(t *testing.T) {
	runtime, err := simulator.NewSimulator(simulator.Scenario{})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	handler := NewHandler(NewSimulatorAdapter(runtime))
	request := func(method, path, body string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(method, path, strings.NewReader(body)))
		return recorder
	}
	create := `{"physicalNode":{"id":"1","endpoint":"inproc://1","enabled":true,"routeUpdateIntervalMs":1200},"deployments":[]}`
	if response := request(http.MethodPost, "/api/v1/physical-nodes", create); response.Code != http.StatusNoContent {
		t.Fatalf("创建节点失败: %d %s", response.Code, response.Body.String())
	}
	update := strings.Replace(create, `"routeUpdateIntervalMs":1200`, `"routeUpdateIntervalMs":2300`, 1)
	if response := request(http.MethodPut, "/api/v1/physical-nodes", update); response.Code != http.StatusNoContent {
		t.Fatalf("更新节点失败: %d %s", response.Code, response.Body.String())
	}
	topology := request(http.MethodGet, "/api/v1/topology", "")
	var status TopologyResponse
	if err := json.Unmarshal(topology.Body.Bytes(), &status); err != nil || len(status.PhysicalNodes) != 1 || status.PhysicalNodes[0].RouteUpdateIntervalMS != 2300 {
		t.Fatalf("拓扑间隔未更新: %s err=%v", topology.Body.String(), err)
	}
	get := request(http.MethodGet, "/api/v1/scenario", "")
	if !strings.Contains(get.Body.String(), `"routeUpdateIntervalMs":2300`) {
		t.Fatalf("场景未保存节点间隔: %s", get.Body.String())
	}
	for _, interval := range []string{"-1", "9223372036855"} {
		body := strings.Replace(update, `"routeUpdateIntervalMs":2300`, `"routeUpdateIntervalMs":`+interval, 1)
		response := request(http.MethodPut, "/api/v1/physical-nodes", body)
		if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), "INVALID_ROUTE_UPDATE_INTERVAL") {
			t.Fatalf("节点间隔校验失败: %d %s", response.Code, response.Body.String())
		}
	}
	for _, interval := range []string{"null", "1.5", `"1000"`} {
		body := strings.Replace(update, `"routeUpdateIntervalMs":2300`, `"routeUpdateIntervalMs":`+interval, 1)
		response := request(http.MethodPut, "/api/v1/physical-nodes", body)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("无效节点间隔未拒绝: %d %s", response.Code, response.Body.String())
		}
	}
	reset := strings.Replace(update, `,"routeUpdateIntervalMs":2300`, ``, 1)
	if response := request(http.MethodPut, "/api/v1/physical-nodes", reset); response.Code != http.StatusNoContent {
		t.Fatalf("重置节点间隔失败: %d %s", response.Code, response.Body.String())
	}
	topology = request(http.MethodGet, "/api/v1/topology", "")
	if err := json.Unmarshal(topology.Body.Bytes(), &status); err != nil || len(status.PhysicalNodes) != 1 || status.PhysicalNodes[0].RouteUpdateIntervalMS != simulator.DefaultRouteUpdateIntervalMS {
		t.Fatalf("拓扑默认间隔错误: %s err=%v", topology.Body.String(), err)
	}
}
