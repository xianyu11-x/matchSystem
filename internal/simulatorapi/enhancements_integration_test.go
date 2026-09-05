package simulatorapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"matchSystem/internal/simulator"
)

// Exercise the new generator through the continuous scheduler and real HTTP
// adapter, rather than testing these independently and assuming they compose.
func TestEnhancementsGeneratedTrafficMatchHistory(t *testing.T) {
	scenario := apiScenario()
	var rule map[string]any
	if err := json.Unmarshal(scenario.Rules[0].RuleJSON, &rule); err != nil {
		t.Fatal(err)
	}
	rule["contract"].(map[string]any)["attributes"] = []any{
		map[string]any{"name": "identity", "type": "int64"},
		map[string]any{"name": "copy", "type": "int64"},
		map[string]any{"name": "level", "type": "int64"},
		map[string]any{"name": "pool", "type": "uint64s", "maxValues": 4},
	}
	document, err := json.Marshal(rule)
	if err != nil {
		t.Fatal(err)
	}
	for i := range scenario.Rules {
		scenario.Rules[i].RuleJSON = document
	}
	runtime, err := simulator.NewSimulator(scenario)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	server := httptest.NewServer(NewHandler(NewSimulatorAdapter(runtime)))
	t.Cleanup(server.Close)
	request := func(method, path, body string, destination any) {
		t.Helper()
		response := doJSON(t, server.Client(), method, server.URL+"/api/v1"+path, body)
		defer response.Body.Close()
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			data, _ := io.ReadAll(response.Body)
			t.Fatalf("%s %s: %d %s", method, path, response.StatusCode, data)
		}
		if destination != nil {
			if err := json.NewDecoder(response.Body).Decode(destination); err != nil {
				t.Fatal(err)
			}
		}
	}
	const body = `{"config":{"distribution":"constant","rate":50,"matchIntervalMs":100,"maxMatches":2,"seed":17},"generator":{"rule":{"namespace":"api","ruleId":1},"count":1,"seed":19,"startTicketId":1000,"attributeGenerators":{"identity":{"type":"int64","source":"ticketId"},"copy":{"type":"int64","source":"shared","ref":"identity"},"level":{"type":"int64","min":"-100","max":"100","distribution":"triangular"},"pool":{"type":"uint64s","set":"1-100,200-400","count":4,"distribution":"high"}}}}`
	request(http.MethodPost, "/traffic", body, nil)
	var status struct {
		State    string `json:"state"`
		Injected uint64 `json:"injected"`
		Produced uint64 `json:"produced"`
		Rounds   uint64 `json:"rounds"`
		Error    string `json:"error"`
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		request(http.MethodGet, "/traffic", "", &status)
		if status.State == "failed" {
			t.Fatalf("traffic failed: %s", status.Error)
		}
		if status.Produced >= 4 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no matches before deadline: %+v", status)
		}
		time.Sleep(20 * time.Millisecond)
	}
	request(http.MethodDelete, "/traffic", "", &status)
	if status.State != "stopped" || status.Produced > status.Rounds*2 {
		t.Fatalf("stop/count limit: %+v", status)
	}
	injected := status.Injected
	time.Sleep(150 * time.Millisecond)
	request(http.MethodGet, "/traffic", "", &status)
	if status.Injected != injected {
		t.Fatal("tickets injected after stop returned")
	}
	var page struct {
		Items []struct {
			MatchID    string   `json:"matchId"`
			Duration   int64    `json:"durationMs"`
			Processing *int64   `json:"processingDurationNs"`
			Tickets    []Ticket `json:"tickets"`
		} `json:"items"`
	}
	request(http.MethodGet, "/matches?limit=100", "", &page)
	if len(page.Items) < 4 {
		t.Fatalf("missing retained matches: %d", len(page.Items))
	}
	for _, match := range page.Items {
		if match.Processing == nil || *match.Processing < 0 || match.Duration < 0 || len(match.Tickets) == 0 {
			t.Fatalf("invalid match observation: %+v", match)
		}
		for _, ticket := range match.Tickets {
			if ticket.Int64Values["identity"] != int64(ticket.TicketID) || ticket.Int64Values["copy"] != int64(ticket.TicketID) {
				t.Fatalf("lost derived attributes: %+v", ticket)
			}
			if level := ticket.Int64Values["level"]; level < -100 || level > 100 {
				t.Fatalf("level out of range: %d", level)
			}
			values := ticket.Uint64Lists["pool"]
			if len(values) != 4 {
				t.Fatalf("unexpected collection: %v", values)
			}
			seen := map[uint64]bool{}
			for _, value := range values {
				if seen[value] || !((value >= 1 && value <= 100) || (value >= 200 && value <= 400)) {
					t.Fatalf("invalid sampled collection: %v", values)
				}
				seen[value] = true
			}
		}
	}
}
