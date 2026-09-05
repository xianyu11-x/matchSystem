package simulatorapi

import (
	"encoding/json"
	"matchSystem/internal/identity"
	"matchSystem/internal/simulator"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPAttributeGeneratorPrecision(t *testing.T) {
	svc := &fakeService{}
	h := NewHandler(svc)
	body := `{"count":1,"rule":{"namespace":"api","ruleId":1},"attributeGenerators":{"ids":{"type":"uint64s","set":"18446744073709551615"},"score":{"type":"int64","min":"-9223372036854775808","max":"-9223372036854775808"}}}`
	r := httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest(http.MethodPost, "/api/v1/tickets/custom", strings.NewReader(body)))
	if r.Code >= 300 {
		t.Fatal(r.Code, r.Body.String())
	}
	v, e := simulator.GenerateBatch(generatorSpec(svc.lastCustom))
	if e != nil {
		t.Fatal(e)
	}
	if v[0].Uint64Lists["ids"][0] != math.MaxUint64 || v[0].Int64Values["score"] != math.MinInt64 {
		t.Fatal(v)
	}
}

func TestTicketObservationLargeIntegersAreQuoted(t *testing.T) {
	v := Ticket{TicketID: 1, TypedValues: TypedValues{Uint64Lists: map[string][]uint64{"ids": {math.MaxUint64, 1}}, Int64Values: map[string]int64{"score": math.MinInt64}}}
	b, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(string(b), `"18446744073709551615"`) || !strings.Contains(string(b), `"-9223372036854775808"`) || !strings.Contains(string(b), `"ticketId":1`) {
		t.Fatal(string(b))
	}
}

func TestGeneratedHTTPBatchRetainsLargeObservedValues(t *testing.T) {
	scenario := apiScenario()
	rule := identity.RuleKey{Namespace: "api", RuleID: 1}
	raw := strings.Replace(string(apiRuleJSON("api", 1)), `"attributes":[]`, `"attributes":[{"name":"ids","type":"uint64s","maxValues":4096}]`, 1)
	scenario.Rules = []simulator.RuleSpec{simulator.NewRuleSpec(identity.LogicalNodeKey{Rule: rule, PlacementID: "p1"}, "p1", []byte(raw))}
	runtime, e := simulator.NewSimulator(scenario)
	if e != nil {
		t.Fatal(e)
	}
	defer runtime.Close()
	h := NewHandler(NewSimulatorAdapter(runtime))
	for _, endpoint := range []string{"custom", "batch"} {
		body := `{"count":1,"rule":{"namespace":"api","ruleId":1},"placementId":"p1","startTicketId":1,"attributeGenerators":{"ids":{"type":"uint64s","set":"18446744073709551615"}}}`
		if endpoint == "batch" {
			body = strings.Replace(body, `"startTicketId":1`, `"startTicketId":2`, 1)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/tickets/"+endpoint, strings.NewReader(body)))
		if w.Code >= 300 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/tickets", nil))
	if w.Code != 200 || strings.Count(w.Body.String(), `"18446744073709551615"`) != 2 {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestHTTPAttributeSharedSourceContract(t *testing.T) {
	svc := &fakeService{}
	h := NewHandler(svc)
	body := `{"count":2,"startTicketId":123,"rule":{"namespace":"api","ruleId":1},"attributeGenerators":{"id":{"type":"uint64s","source":"ticketId"},"copy":{"type":"uint64s","source":"shared","ref":"id"}}}`
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/tickets/custom", strings.NewReader(body)))
	if w.Code >= 300 {
		t.Fatal(w.Code, w.Body.String())
	}
	a, e := simulator.GenerateBatch(generatorSpec(svc.lastCustom))
	if e != nil {
		t.Fatal(e)
	}
	if a[1].Uint64Lists["copy"][0] != 124 {
		t.Fatal(a)
	}
}
