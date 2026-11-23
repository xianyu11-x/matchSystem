package simulatorapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/xianyu11-x/matchSystem/internal/simulator"
)

func TestTrafficHTTPPlacementTargetsExactNode(t *testing.T) {
	scenario := apiScenario()
	runtime, err := simulator.NewSimulator(scenario)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	server := httptest.NewServer(NewHandler(NewSimulatorAdapter(runtime)))
	defer server.Close()
	body := fmt.Sprintf(`{"config":{"mode":"fixed","distribution":"constant","rate":10,"matchIntervalMs":10000,"maxMatches":1,"seed":42,"steps":4,"stepMs":100,"startTimeMs":1000},"generator":{"rule":{"namespace":"请求别名","ruleId":1},"placementId":"%d","startTicketId":100,"seed":5}}`, testPlacement("p2"))
	response := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/v1/traffic", body)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("启动定向流量失败: %d", response.StatusCode)
	}
	response.Body.Close()
	deadline := time.Now().Add(3 * time.Second)
	for runtime.Traffic().State == "running" && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	status := runtime.Traffic()
	if status.State != "completed" || status.Injected != 3 {
		t.Fatalf("定向流量未完成: %+v", status)
	}
	page, err := runtime.ListTickets(context.Background(), simulator.TicketQuery{Limit: 100})
	if err != nil || len(page.Items) != 3 {
		t.Fatalf("读取定向流量失败: %+v, %v", page, err)
	}
	for _, item := range page.Items {
		if item.Owner.PhysicalNodeID != testPhysical("p2") || item.Owner.LogicalNode.PlacementID != testPlacement("p2") {
			t.Fatalf("流量注入了错误节点: %+v", item.Owner)
		}
	}

	missing := fmt.Sprintf(`{"config":{"distribution":"constant","rate":10,"matchIntervalMs":100,"maxMatches":1},"generator":{"rule":{"ruleId":1},"placementId":"%d"}}`, testPlacement("missing"))
	response = doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/v1/traffic", missing)
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("不存在的节点应返回 404，实际为 %d", response.StatusCode)
	}
	response.Body.Close()

	physical := scenario.PhysicalNodes[1]
	physical.Enabled = false
	rule := scenario.Rules[1]
	if err := runtime.UpdatePhysicalNodeFromLibrary(context.Background(), physical, []simulator.RuleDeploymentSpec{{
		LogicalNode: rule.LogicalNode, Weight: rule.Weight, Enabled: true, KeepTickFacts: true,
	}}); err != nil {
		t.Fatal(err)
	}
	response = doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/v1/traffic", body)
	defer response.Body.Close()
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("禁用的节点应返回 409，实际为 %d", response.StatusCode)
	}
}
