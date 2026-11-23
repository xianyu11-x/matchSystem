package simulatorapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/xianyu11-x/matchSystem/internal/simulator"
)

type ruleLibraryFakeService struct {
	fakeService
	snapshot         simulator.RuleLibrarySnapshot
	expectedRevision uint64
	rules            []json.RawMessage
}

func (f *ruleLibraryFakeService) RuleLibrary(context.Context) (simulator.RuleLibrarySnapshot, error) {
	return f.snapshot, f.err
}

func (f *ruleLibraryFakeService) ReplaceRuleLibrary(_ context.Context, revision uint64, rules []json.RawMessage) (simulator.RuleLibrarySnapshot, error) {
	f.expectedRevision = revision
	f.rules = rules
	return f.snapshot, f.err
}

func TestRuleLibraryHTTPWire(t *testing.T) {
	service := &ruleLibraryFakeService{snapshot: simulator.RuleLibrarySnapshot{Revision: 18446744073709551615, Rules: []json.RawMessage{json.RawMessage(`{"schemaVersion":"match-rule/v1"}`)}}}
	handler := NewHandler(service)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/rule-library", nil))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"revision":"18446744073709551615"`) {
		t.Fatalf("GET status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPut, "/api/v1/rule-library", strings.NewReader(`{"revision":"18446744073709551615","rules":[{"schemaVersion":"match-rule/v1"}]}`)))
	if recorder.Code != http.StatusOK || service.expectedRevision != 18446744073709551615 || len(service.rules) != 1 || string(service.rules[0]) != `{"schemaVersion":"match-rule/v1"}` {
		t.Fatalf("PUT status=%d body=%s revision=%d rules=%s", recorder.Code, recorder.Body.String(), service.expectedRevision, service.rules)
	}
	service.snapshot.Rules = nil
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/rule-library", nil))
	if !strings.Contains(recorder.Body.String(), `"rules":[]`) {
		t.Fatalf("empty library body=%s", recorder.Body.String())
	}
	service.err = NewServiceError(http.StatusConflict, "RULE_LIBRARY_CONFLICT", "revision is stale")
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPut, "/api/v1/rule-library", strings.NewReader(`{"revision":"1","rules":[]}`)))
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), `"RULE_LIBRARY_CONFLICT"`) {
		t.Fatalf("stale revision status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestRuleLibraryHTTPRejectsMalformedRequests(t *testing.T) {
	handler := NewHandler(&ruleLibraryFakeService{})
	for _, body := range []string{
		`{}`, `{"revision":null,"rules":[]}`, `{"revision":1,"rules":[]}`,
		`{"revision":"","rules":[]}`, `{"revision":"-1","rules":[]}`,
		`{"revision":"+1","rules":[]}`, `{"revision":"18446744073709551616","rules":[]}`,
		`{"revision":"1"}`, `{"revision":"1","rules":null}`, `{"revision":"1","rules":{}}`,
		`{"revision":"1","rules":[null]}`, `{"revision":"1","rules":[1]}`,
		`{"revision":"1","rules":[],"extra":true}`,
	} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPut, "/api/v1/rule-library", strings.NewReader(body)))
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("body=%s status=%d response=%s", body, recorder.Code, recorder.Body.String())
		}
	}
	recorder := httptest.NewRecorder()
	NewHandler(&fakeService{}).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/rule-library", nil))
	if recorder.Code != http.StatusNotImplemented {
		t.Fatalf("legacy service status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
