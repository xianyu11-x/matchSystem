package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractAndInjectPreserveTimeline(t *testing.T) {
	const timeline = `{"ticket":18446744073709551615,"now":-9223372036854775808}`
	var extractLogs, extractInstance, injectedTimeline, injectedRule string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method: %s", r.Method)
		}
		switch r.URL.Path {
		case "/api/v1/replay/extract":
			var request struct {
				Logs       string `json:"logs"`
				InstanceID string `json:"instanceId"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
			}
			extractLogs, extractInstance = request.Logs, request.InstanceID
			_ = json.NewEncoder(w).Encode(map[string]any{"instances": []any{}, "timelineJson": timeline})
		case "/api/v1/replay/run":
			var request struct {
				TimelineJSON string `json:"timelineJson"`
				TargetRuleID string `json:"targetRuleId"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
			}
			injectedTimeline, injectedRule = request.TimelineJSON, request.TargetRuleID
			_, _ = io.WriteString(w, `{"matched":1}`)
		default:
			t.Errorf("unknown path: %s", r.URL.Path)
		}
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "timeline.json")
	var output, errors bytes.Buffer
	if err := run([]string{"-api", server.URL + "/api/v1", "extract", "-logs", "-", "-instance", "node-A", "-output", path}, strings.NewReader("{\"log\":1}\n"), &output, &errors); err != nil {
		t.Fatalf("extract: %v %s", err, errors.String())
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != timeline || extractLogs != "{\"log\":1}\n" || extractInstance != "node-A" {
		t.Fatalf("extract result %q, logs %q, instance %q, err %v", data, extractLogs, extractInstance, err)
	}
	if err := run([]string{"inject", "-api", server.URL + "/api/v1", "-timeline", path, "-rule", "18446744073709551615"}, strings.NewReader(""), &output, &errors); err != nil {
		t.Fatalf("inject: %v %s", err, errors.String())
	}
	if injectedTimeline != timeline || injectedRule != "18446744073709551615" || output.String() != "{\"matched\":1}\n" {
		t.Fatalf("inject result: %q %q %q", injectedTimeline, injectedRule, output.String())
	}
}

func TestCLIRejectsBadRuleAndReportsServerErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":"missing sequence"}`)
	}))
	defer server.Close()
	var output, errors bytes.Buffer
	err := run([]string{"extract", "-api", server.URL}, strings.NewReader(""), &output, &errors)
	if err == nil || !strings.Contains(err.Error(), "400") || !strings.Contains(err.Error(), "missing sequence") {
		t.Fatalf("server error: %v", err)
	}
	err = run([]string{"inject", "-timeline", "-", "-rule", "18446744073709551616"}, strings.NewReader("{}"), &output, &errors)
	if err == nil {
		t.Fatal("accepted out-of-range rule ID")
	}
}

func TestExtractReadsMarkedAndLegacyRecordingFiles(t *testing.T) {
	for _, test := range []struct {
		name string
		data string
	}{
		{"matchscope-20261124-010203-123.log", "[match_replay] {\"timeline\":{\"instanceId\":\"a\",\"sequence\":1}}\n"},
		{"matchscope-20261124-010203-123.jsonl", "{\"msg\":\"[match_replay]\",\"timeline\":{\"instanceId\":\"a\",\"sequence\":1}}\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), test.name)
			if err := os.WriteFile(path, []byte(test.data), 0600); err != nil {
				t.Fatal(err)
			}
			var received string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					Logs string `json:"logs"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
				}
				received = request.Logs
				_, _ = io.WriteString(w, `{"instances":[]}`)
			}))
			defer server.Close()
			var output, errors bytes.Buffer
			if err := run([]string{"extract", "-api", server.URL, "-logs", path}, strings.NewReader(""), &output, &errors); err != nil {
				t.Fatalf("读取录制文件失败: %v %s", err, errors.String())
			}
			if received != test.data {
				t.Fatalf("录制文件内容改变: %q", received)
			}
		})
	}
}
