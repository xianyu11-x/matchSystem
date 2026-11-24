package simulatorapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/xianyu11-x/matchSystem/internal/simulator"
	"github.com/xianyu11-x/matchSystem/pkg/common"
	"github.com/xianyu11-x/matchSystem/pkg/identity"
)

func TestTrafficHTTPDistinguishesExplicitZeroFromMissing(t *testing.T) {
	runtime, err := simulator.NewService(apiScenario())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	server := httptest.NewServer(NewHandler(NewSimulatorAdapter(runtime)))
	defer server.Close()
	requestBody := func(field string) string {
		return fmt.Sprintf(`{"config":{"mode":"fixed","distribution":"constant","rate":10,"matchIntervalMs":100,"steps":4,"stepMs":100,"startTimeMs":1000%s},"generator":{"rule":{"ruleId":1},"startTicketId":100}}`, field)
	}
	for _, test := range []struct{ name, field string }{
		{"missing", ""},
		{"null", `,"maxMatches":null`},
		{"negative", `,"maxMatches":-1`},
		{"too large", `,"maxMatches":10001`},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/v1/traffic", requestBody(test.field))
			defer response.Body.Close()
			if response.StatusCode != http.StatusBadRequest {
				t.Fatalf("无效 maxMatches 应返回 400，实际为 %d", response.StatusCode)
			}
		})
	}
	response := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/v1/traffic", requestBody(`,"maxMatches":0`))
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		t.Fatalf("显式零配额应成功，实际为 %d", response.StatusCode)
	}
	response.Body.Close()
	deadline := time.Now().Add(3 * time.Second)
	for runtime.Traffic().State == "running" && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	status := runtime.Traffic()
	if status.State != "completed" || status.Injected != 3 || status.Rounds != 0 || status.Produced != 0 || status.Config.MaxMatches != 0 {
		t.Fatalf("显式零配额流量状态不正确: %+v", status)
	}
}

func TestRoundHTTPUsesPerNodeQuotaAndLegacyDefaults(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
		want int
	}{
		{"per node", `{"now":1000,"maxMatches":1}`, 2},
		{"legacy alias", `{"now":1000,"matchLimit":1}`, 2},
		{"zero falls back to legacy alias", `{"now":1000,"maxMatches":0,"matchLimit":1}`, 2},
		{"omitted means unlimited", `{"now":1000}`, 4},
	} {
		t.Run(test.name, func(t *testing.T) {
			scenario := apiScenario()
			runtime, err := simulator.NewService(scenario)
			if err != nil {
				t.Fatal(err)
			}
			defer runtime.Close()
			for i, rule := range scenario.Rules {
				owner := common.RouteDecision{Owner: identity.OwnerRef{PhysicalNodeID: rule.PhysicalNodeID, LogicalNode: rule.LogicalNode}}
				for j := 0; j < 2; j++ {
					id := uint64(i*10 + j + 1)
					if _, err := runtime.AddRoutedTicket(context.Background(), owner, simulator.TicketInput{Rule: rule.LogicalNode.Rule, TicketID: id, CreatedAt: int64(id)}); err != nil {
						t.Fatal(err)
					}
				}
			}
			server := httptest.NewServer(NewHandler(NewSimulatorAdapter(runtime)))
			defer server.Close()
			response := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/v1/rounds", test.body)
			defer response.Body.Close()
			if response.StatusCode != http.StatusOK {
				t.Fatalf("手动轮次 HTTP 状态=%d", response.StatusCode)
			}
			var round RoundResponse
			if err := json.NewDecoder(response.Body).Decode(&round); err != nil {
				t.Fatal(err)
			}
			if round.Produced != test.want || len(round.Matches) != test.want {
				t.Fatalf("手动轮次结果=%+v，期望 %d 局", round, test.want)
			}
		})
	}
}
