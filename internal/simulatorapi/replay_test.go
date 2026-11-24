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

type replayFakeService struct {
	fakeService
	extractRequest simulator.ReplayExtractRequest
	replayRequest  simulator.ReplayRequest
	timeline       string
}

func (f *replayFakeService) ExtractTimeline(_ context.Context, request simulator.ReplayExtractRequest) (simulator.ReplayExtractResult, error) {
	f.extractRequest = request
	return simulator.ReplayExtractResult{Instances: []simulator.ReplayInstance{}, TimelineJSON: f.timeline}, f.err
}

func (f *replayFakeService) ReplayTimeline(_ context.Context, request simulator.ReplayRequest) (simulator.ReplayResult, error) {
	f.replayRequest = request
	return simulator.ReplayResult{InstanceID: "node", TargetRuleID: request.TargetRuleID, Status: "interrupted", InterruptedAt: "18446744073709551615", Error: "conflict", Events: []simulator.ReplayEventResult{}}, f.err
}

func TestReplayHTTPPreservesRawTimelineAndReportsInterruption(t *testing.T) {
	timeline := `{"sequence":18446744073709551615,"value":-9223372036854775808}`
	service := &replayFakeService{timeline: timeline}
	handler := NewHandler(service)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/replay/extract", strings.NewReader(`{"logs":"","instanceId":"node"}`)))
	var extracted simulator.ReplayExtractResult
	if err := json.Unmarshal(recorder.Body.Bytes(), &extracted); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != http.StatusOK || service.extractRequest.Logs == nil || *service.extractRequest.Logs != "" || extracted.TimelineJSON != timeline {
		t.Fatalf("extract status=%d request=%#v result=%#v", recorder.Code, service.extractRequest, extracted)
	}
	request, err := json.Marshal(simulator.ReplayRequest{TimelineJSON: extracted.TimelineJSON, TargetRuleID: "18446744073709551615"})
	if err != nil {
		t.Fatal(err)
	}
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/replay/run", strings.NewReader(string(request))))
	if recorder.Code != http.StatusOK || service.replayRequest.TimelineJSON != timeline || service.replayRequest.TargetRuleID != "18446744073709551615" || !strings.Contains(recorder.Body.String(), `"status":"interrupted"`) || !strings.Contains(recorder.Body.String(), `"interruptedAt":"18446744073709551615"`) {
		t.Fatalf("replay status=%d request=%#v body=%s", recorder.Code, service.replayRequest, recorder.Body.String())
	}
}

func TestReplayHTTPRejectsInvalidRequestsAndSupportsOptionalService(t *testing.T) {
	for _, path := range []string{"/api/v1/replay/extract", "/api/v1/replay/run"} {
		recorder := httptest.NewRecorder()
		NewHandler(&fakeService{}).ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`)))
		if recorder.Code != http.StatusNotImplemented {
			t.Fatalf("%s optional service status=%d", path, recorder.Code)
		}
		body := `{"instanceId":"node"}`
		if strings.HasSuffix(path, "/run") {
			body = `{"targetRuleId":"1"}`
		}
		recorder = httptest.NewRecorder()
		NewHandler(&replayFakeService{}, WithMaxBodyBytes(8)).ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("%s body limit status=%d", path, recorder.Code)
		}
		recorder = httptest.NewRecorder()
		NewHandler(&replayFakeService{}).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s method status=%d", path, recorder.Code)
		}
	}
}
