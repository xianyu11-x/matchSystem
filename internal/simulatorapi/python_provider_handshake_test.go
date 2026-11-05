package simulatorapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	stdRuntime "runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"matchSystem/internal/matchsystem"
	"matchSystem/internal/simulator"
)

func TestHTTPPythonHandshakeFailuresAreStructuredAndAtomic(t *testing.T) {
	python := testHTTPPythonExecutable(t)
	scenario := apiScenario()
	scenario.Rules = scenario.Rules[:1]
	runtime, err := simulator.NewSimulator(scenario)
	if err != nil {
		t.Fatalf("NewSimulator: %v", err)
	}
	defer runtime.Close()
	server := httptest.NewServer(NewHandler(NewSimulatorAdapter(runtime)))
	defer server.Close()

	tests := []struct {
		name           string
		descriptor     string
		wantDiagnostic string
	}{
		{
			name:           "contract mismatch after handshake",
			descriptor:     `{"id":"script.tick","version":"v1","facts":[{"name":"clock","type":"strings","scope":"tick","maxValues":1}]}`,
			wantDiagnostic: matchsystem.ProviderHandshakeFactTypeMismatch,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			marker := filepath.Join(t.TempDir(), "provider.pid")
			script := filepath.Join(t.TempDir(), "provider.py")
			source := fmt.Sprintf(`import os
from pathlib import Path
Path(%q).write_text(str(os.getpid()), encoding="ascii")
def describe():
    return {"tick": %s}
def provide(method, data):
    return {}
`, filepath.ToSlash(marker), test.descriptor)
			if err := os.WriteFile(script, []byte(source), 0600); err != nil {
				t.Fatal(err)
			}

			candidate := scenario.Clone()
			rule := &candidate.Rules[0]
			rule.RuleJSON = []byte(strings.Replace(
				string(apiRuleJSON("api", 1)),
				`"facts":[]`,
				`"facts":[{"name":"clock","type":"int64","scope":"tick"}]`,
				1,
			))
			rule.PythonProvider = &simulator.PythonProviderConfig{
				Python: python,
				Script: script,
			}
			body, err := json.Marshal(struct {
				Scenario simulator.Scenario `json:"scenario"`
			}{Scenario: candidate})
			if err != nil {
				t.Fatal(err)
			}
			response := doJSON(t, server.Client(), http.MethodPut, server.URL+"/api/v1/scenario", string(body))
			var failure ErrorResponse
			if err := json.NewDecoder(response.Body).Decode(&failure); err != nil {
				response.Body.Close()
				t.Fatalf("decode failure response: %v", err)
			}
			response.Body.Close()
			if response.StatusCode != http.StatusInternalServerError {
				t.Fatalf("status=%d body=%#v", response.StatusCode, failure)
			}
			if failure.Error.Code != "PYTHON_PROVIDER_FAILED" {
				t.Fatalf("error code=%q body=%#v", failure.Error.Code, failure)
			}
			details, ok := failure.Error.Details.(map[string]any)
			if !ok {
				t.Fatalf("error details=%#v, want structured Python diagnostic", failure.Error.Details)
			}
			if method, _ := details["method"].(string); method != "handshake" {
				t.Fatalf("diagnostic method=%q, details=%#v", method, details)
			}
			diagnostic, _ := details["diagnostic"].(string)
			if !strings.Contains(diagnostic, test.wantDiagnostic) {
				t.Fatalf("diagnostic=%q, want %q", diagnostic, test.wantDiagnostic)
			}

			pid := readHTTPPythonPID(t, marker)
			waitHTTPPythonExit(t, pid)
			get := doJSON(t, server.Client(), http.MethodGet, server.URL+"/api/v1/scenario", "")
			var retained ScenarioResponse
			if err := json.NewDecoder(get.Body).Decode(&retained); err != nil {
				get.Body.Close()
				t.Fatalf("decode retained scenario: %v", err)
			}
			get.Body.Close()
			var got simulator.Scenario
			if err := json.Unmarshal(retained.Scenario, &got); err != nil {
				t.Fatalf("decode retained scenario document: %v", err)
			}
			if len(got.Rules) != 1 || got.Rules[0].PythonProvider != nil {
				t.Fatalf("failed candidate replaced old Scenario: %#v", got)
			}
		})
	}
}

func testHTTPPythonExecutable(t *testing.T) string {
	t.Helper()
	python := strings.TrimSpace(os.Getenv("PYTHON_PROVIDER_EXECUTABLE"))
	if python == "" {
		python = "python"
	}
	if filepath.IsAbs(python) {
		if _, err := os.Stat(python); err != nil {
			t.Skipf("Python executable unavailable: %v", err)
		}
	} else if _, err := exec.LookPath(python); err != nil {
		t.Skipf("Python executable unavailable: %v", err)
	}
	if err := exec.Command(python, "--version").Run(); err != nil {
		t.Skipf("Python executable cannot start: %v", err)
	}
	return python
}

func readHTTPPythonPID(t *testing.T, marker string) int {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		data, err := os.ReadFile(marker)
		if err == nil {
			pid, parseErr := strconv.Atoi(strings.TrimSpace(string(data)))
			if parseErr == nil && pid > 0 {
				return pid
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("provider PID marker was not written: %s (%v)", marker, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func waitHTTPPythonExit(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		alive, err := httpPythonAlive(pid)
		if err != nil {
			t.Fatal(err)
		}
		if !alive {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("Python provider process %d was not reaped", pid)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func httpPythonAlive(pid int) (bool, error) {
	if stdRuntime.GOOS == "windows" {
		output, err := exec.Command("tasklist", "/FI", fmt.Sprintf("PID eq %d", pid), "/FO", "CSV", "/NH").CombinedOutput()
		if err != nil {
			return false, fmt.Errorf("tasklist provider PID %d: %w (%s)", pid, err, strings.TrimSpace(string(output)))
		}
		return strings.Contains(string(output), fmt.Sprintf(`"%d"`, pid)), nil
	}
	return exec.Command("kill", "-0", strconv.Itoa(pid)).Run() == nil, nil
}
