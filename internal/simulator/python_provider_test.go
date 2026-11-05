package simulator

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	stdRuntime "runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"matchSystem/internal/common"
	"matchSystem/internal/identity"
	"matchSystem/internal/matchsystem"
	"matchSystem/internal/matchsystem/fact"
)

func TestScenarioPythonProviderReadsAllFactScopes(t *testing.T) {
	python := testPythonExecutable(t)
	script := filepath.Join(t.TempDir(), "provider.py")
	source := `def provide(method, data):
    if method == "tick":
        return {"Int64Values": {"clock": data["Now"]}}
    if method == "object":
        return {"Uint64Lists": {"objectID": [7]}}
    if method == "initialize":
        return {"Int64Values": {"members": 1}}
    if method == "join":
        return {"Int64Values": {"members": data["MatchFactsBefore"]["Int64Values"]["members"] + 1}}
    raise ValueError("unknown method")
`
	source = `def describe():
    return {
        "tick": {"id": "python.tick", "version": "1", "facts": [{"name": "clock", "type": "int64", "scope": "tick"}]},
        "object": {"id": "python.object", "version": "1", "facts": [{"name": "objectID", "type": "uint64s", "scope": "object", "maxValues": 1}]},
        "match": {"id": "python.match", "version": "1", "facts": [{"name": "members", "type": "int64", "scope": "match"}]}
    }

` + source
	if err := os.WriteFile(script, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	key := identity.LogicalNodeKey{Rule: identity.RuleKey{Namespace: "python", RuleID: 1}, PlacementID: "p1"}
	ruleJSON := []byte(`{"schemaVersion":"match-rule/v1","ruleKey":{"namespace":"python","ruleId":1},"contract":{"schemaVersion":"logical-node-contract/v3","attributes":[],"facts":[{"name":"clock","type":"int64","scope":"tick"},{"name":"objectID","type":"uint64s","scope":"object","maxValues":1},{"name":"members","type":"int64","scope":"match"}],"indexes":[]},"prefilter":{"schemaVersion":"prefilter/v3","bitmap":{"resultType":"bitmap","expr":{"op":"none"}}},"evaluation":{"schemaVersion":"evaluation/v3","canJoin":{"schemaVersion":"expression-scalar/v3","resultType":"bool","expr":{"op":"uint64s_contains","values":{"op":"uint64s_ref","source":"candidate_facts","name":"objectID"},"needle":7}},"canComplete":{"schemaVersion":"expression-scalar/v3","resultType":"bool","expr":{"op":"bool_and","children":[{"op":"int64_gte","left":{"op":"int64_ref","source":"tick_facts","name":"clock"},"right":{"op":"int64_literal","value":123}},{"op":"int64_gte","left":{"op":"int64_ref","source":"match_facts","name":"members"},"right":{"op":"int64_literal","value":2}}]}},"scoring":{"type":"constant","params":{"value":0}},"seedSelection":{"type":"arrival","params":{}},"runtime":{"candidateScoringLimitPerSeed":500,"candidateLimitPerSeed":50,"maxPlayers":8,"attemptLimitPerProduceMatch":500,"attemptLimitPerMatchRound":500}}`)
	ruleJSON = []byte(strings.Replace(string(ruleJSON), `}},"scoring"`, `}}},"scoring"`, 1))
	ruleJSON = []byte(strings.Replace(string(ruleJSON), `500}}}`, `500}}`, 1))
	ruleJSON = []byte(strings.Replace(string(ruleJSON), `"attributes":[]`, `"attributes":[{"name":"group","type":"strings","maxValues":1}]`, 1))
	ruleJSON = []byte(strings.Replace(string(ruleJSON), `"indexes":[]`, `"indexes":[{"type":"multi_value","name":"group","keyType":"string","maxDocumentValues":1,"maxQueryValues":1}]`, 1))
	ruleJSON = []byte(strings.Replace(string(ruleJSON), `"expr":{"op":"none"}`, `"expr":{"op":"lookup_string","index":"group","values":{"schemaVersion":"expression-scalar/v3","resultType":"strings","expr":{"op":"strings_literal","values":["all"]}}}`, 1))
	rule := NewRuleSpec(key, "p1", ruleJSON)
	rule.PythonProvider = &PythonProviderConfig{Python: python, Script: script, TimeoutMS: 1000}
	sim, err := NewService(Scenario{PhysicalNodes: []PhysicalNodeSpec{NewPhysicalNodeSpec("p1", "inproc://p1")}, Rules: []RuleSpec{rule}})
	if err != nil {
		t.Fatal(err)
	}
	defer sim.Close()
	for _, id := range []uint64{1, 2} {
		if _, err := sim.AddTicket(context.Background(), TicketInput{Rule: key.Rule, TicketID: id, CreatedAt: 100, StringLists: map[string][]string{"group": {"all"}}}); err != nil {
			t.Fatal(err)
		}
	}
	result, err := sim.RunRound(context.Background(), 123, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Matches) != 1 || result.Matches[0].Facts.Int64Values["members"] != 2 {
		t.Fatalf("unexpected Python Match Facts: %#v", result)
	}
	if got := result.Matches[0].Tickets[0].ObjectFacts.Uint64Lists["objectID"]; len(got) != 1 || got[0] != 7 {
		t.Fatalf("unexpected Python Object Facts: %#v", result.Matches[0].Tickets[0].ObjectFacts)
	}
}

func TestScenarioPythonProviderScriptDescriptorsDriveAllScopes(t *testing.T) {
	python := testPythonExecutable(t)
	script := writePythonProviderScript(t, `def describe():
    return {
        "tick": {"id": "python.script", "version": "v2", "facts": [
            {"name": "clock", "type": "int64", "scope": "tick"},
            {"name": "debug", "type": "strings", "scope": "tick", "maxValues": 1}
        ]},
        "object": {"id": "python.script", "version": "v2", "facts": [
            {"name": "objectID", "type": "uint64s", "scope": "object", "maxValues": 1}
        ]},
        "match": {"id": "python.script", "version": "v2", "facts": [
            {"name": "members", "type": "int64", "scope": "match"}
        ]}
    }

def provide(method, data):
    if method == "tick":
        return {"Int64Values": {"clock": data["Now"]}}
    if method == "object":
        return {"Uint64Lists": {"objectID": [7]}}
    if method == "initialize":
        return {"Int64Values": {"members": 1}}
    if method == "join":
        return {"Int64Values": {"members": data["MatchFactsBefore"]["Int64Values"]["members"] + 1}}
    raise ValueError("unknown method")
`)
	key := identity.LogicalNodeKey{Rule: identity.RuleKey{Namespace: "python-speed", RuleID: 1}, PlacementID: "p1"}
	rule := NewRuleSpec(key, "p1", []byte(fixedPythonRuleJSON))
	rule.PythonProvider = &PythonProviderConfig{Python: python, Script: script, TimeoutMS: 1000}
	sim, err := NewService(Scenario{PhysicalNodes: []PhysicalNodeSpec{NewPhysicalNodeSpec("p1", "inproc://p1")}, Rules: []RuleSpec{rule}})
	if err != nil {
		t.Fatalf("Python describe handshake should load without scenario declarations: %v", err)
	}
	defer sim.Close()

	metadata, err := sim.FactMetadata(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	if metadata.TickProviderDescriptor == nil || metadata.TickProviderDescriptor.ID != "python.script" || len(metadata.TickProviderDescriptor.Facts) != 2 {
		t.Fatalf("script descriptor was not retained in runtime metadata: %#v", metadata.TickProviderDescriptor)
	}
	if metadata.TickProviderDescriptorSource != "python" || metadata.ObjectProviderDescriptorSource != "python" || metadata.MatchProviderDescriptorSource != "python" {
		t.Fatalf("unexpected descriptor sources: %#v", metadata)
	}

	for _, id := range []uint64{1, 2} {
		if _, err := sim.AddTicket(context.Background(), TicketInput{Rule: key.Rule, TicketID: id, CreatedAt: 100, StringLists: map[string][]string{"group": {"all"}}}); err != nil {
			t.Fatal(err)
		}
	}
	result, err := sim.RunRound(context.Background(), 123, 0)
	if err != nil || len(result.Matches) != 1 || result.Matches[0].Facts.Int64Values["members"] != 2 {
		t.Fatalf("script provider did not participate in all scopes: result=%#v err=%v", result, err)
	}

	encoded, err := json.Marshal(Scenario{PhysicalNodes: []PhysicalNodeSpec{NewPhysicalNodeSpec("p1", "inproc://p1")}, Rules: []RuleSpec{rule}})
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	if strings.Contains(text, "factProviderDescriptor") || strings.Contains(text, "objectFactProviderDescriptor") || strings.Contains(text, "matchFactProviderDescriptor") {
		t.Fatalf("Python handshake descriptors were written back to Scenario JSON: %s", text)
	}
	if strings.Contains(text, "descriptorSource") {
		t.Fatalf("removed descriptor source was retained in Scenario JSON: %s", text)
	}
	committed, err := sim.GetScenario(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	committedJSON, err := json.Marshal(committed)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(committedJSON), "factProviderDescriptor") || strings.Contains(string(committedJSON), "objectFactProviderDescriptor") || strings.Contains(string(committedJSON), "matchFactProviderDescriptor") {
		t.Fatalf("runtime GET backfilled script descriptors into Scenario JSON: %s", committedJSON)
	}
	roundTrip, err := ParseScenarioJSON(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if err := sim.ReplaceScenario(context.Background(), roundTrip); err != nil {
		t.Fatalf("script source did not survive scenario roundtrip: %v", err)
	}
}

func TestPythonProviderRejectsMalformedScriptDescriptors(t *testing.T) {
	python := testPythonExecutable(t)
	cases := map[string]string{
		"non_callable": `describe = None
`,
		"empty": `def describe():
    return {}
`,
		"none": `def describe():
    return None
`,
		"missing_id": `def describe():
    return {"tick": {"version": "v1", "facts": []}}
`,
		"wrong_type": `def describe():
    return {"tick": {"id": "x", "version": "v1", "facts": [{"name": "clock", "type": "float", "scope": "tick"}]}}
`,
		"invalid_limit": `def describe():
    return {"tick": {"id": "x", "version": "v1", "facts": [{"name": "labels", "type": "strings", "scope": "tick", "maxValues": 0}]}}
`,
	}
	for name, describe := range cases {
		t.Run(name, func(t *testing.T) {
			script := writePythonProviderScript(t, describe+`\ndef provide(method, data):
    return {}
`)
			worker, err := StartPythonProvider(PythonProviderConfig{Python: python, Script: script, TimeoutMS: 500}, nil)
			if err == nil {
				worker.Close()
				t.Fatal("malformed script descriptor was accepted")
			}
			if worker != nil {
				t.Fatal("failed handshake returned a live worker")
			}
		})
	}
}

func TestPythonProviderDescriptorReloadAndFailedReplacementKeepRuntimeAtomic(t *testing.T) {
	python := testPythonExecutable(t)
	descriptorScript := func(id string, includeMatch bool) string {
		match := `"match": {"id": "` + id + `", "version": "v1", "facts": [{"name": "members", "type": "int64", "scope": "match"}]},`
		if !includeMatch {
			match = ""
		}
		return `def describe():
    return {
        "tick": {"id": "` + id + `", "version": "v1", "facts": [{"name": "clock", "type": "int64", "scope": "tick"}]},
        "object": {"id": "` + id + `", "version": "v1", "facts": [{"name": "objectID", "type": "uint64s", "scope": "object", "maxValues": 1}]},
        ` + match + `
    }

def provide(method, data):
    if method == "tick":
        return {"Int64Values": {"clock": data["Now"]}}
    if method == "object":
        return {"Uint64Lists": {"objectID": [7]}}
    if method == "initialize":
        return {"Int64Values": {"members": 1}}
    if method == "join":
        return {"Int64Values": {"members": data["MatchFactsBefore"]["Int64Values"]["members"] + 1}}
    raise ValueError("unknown method")
`
	}
	script := writePythonProviderScript(t, descriptorScript("v1", true))
	key := identity.LogicalNodeKey{Rule: identity.RuleKey{Namespace: "python-speed", RuleID: 1}, PlacementID: "reload"}
	rule := NewRuleSpec(key, "p1", []byte(fixedPythonRuleJSON))
	rule.LogicalNode = key
	rule.PythonProvider = &PythonProviderConfig{Python: python, Script: script, TimeoutMS: 1000}
	scenario := Scenario{PhysicalNodes: []PhysicalNodeSpec{NewPhysicalNodeSpec("p1", "inproc://p1")}, Rules: []RuleSpec{rule}}
	sim, err := NewService(scenario)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sim.Close() })
	metadata, err := sim.FactMetadata(context.Background(), key)
	if err != nil || metadata.TickProviderDescriptor == nil || metadata.TickProviderDescriptor.ID != "v1" {
		t.Fatalf("initial descriptor: %#v %v", metadata, err)
	}
	oldWorker := snapshotPythonProviders(t, sim, []identity.LogicalNodeKey{key})[key]
	if err := os.WriteFile(script, []byte(descriptorScript("v2", true)), 0600); err != nil {
		t.Fatal(err)
	}
	if err := sim.ReplaceScenario(context.Background(), scenario); err != nil {
		t.Fatalf("descriptor update replacement: %v", err)
	}
	metadata, err = sim.FactMetadata(context.Background(), key)
	if err != nil || metadata.TickProviderDescriptor == nil || metadata.TickProviderDescriptor.ID != "v2" {
		t.Fatalf("reloaded descriptor: %#v %v", metadata, err)
	}
	newWorker := snapshotPythonProviders(t, sim, []identity.LogicalNodeKey{key})[key]
	if newWorker == oldWorker || oldWorker == nil || !oldWorker.Closed() {
		t.Fatalf("descriptor reload did not replace/reap worker: old=%p new=%p", oldWorker, newWorker)
	}
	if err := os.WriteFile(script, []byte(descriptorScript("v3", false)), 0600); err != nil {
		t.Fatal(err)
	}
	if err := sim.ReplaceScenario(context.Background(), scenario); err == nil {
		t.Fatal("replacement with missing Contract descriptor was accepted")
	}
	metadata, err = sim.FactMetadata(context.Background(), key)
	if err != nil || metadata.TickProviderDescriptor == nil || metadata.TickProviderDescriptor.ID != "v2" {
		t.Fatalf("failed replacement changed old runtime metadata: %#v %v", metadata, err)
	}
	if newWorker.Closed() {
		t.Fatal("failed replacement closed the published old worker")
	}
}

func TestScenarioPythonProviderHandshakeFailureKeepsOldRuntime(t *testing.T) {
	python := testPythonExecutable(t)
	scenario, key := testScenario()
	sim, err := NewService(scenario)
	if err != nil {
		t.Fatal(err)
	}
	defer sim.Close()
	bad := scenario
	bad.Rules = append([]RuleSpec(nil), scenario.Rules...)
	bad.Rules[0].PythonProvider = &PythonProviderConfig{Python: python, Script: filepath.Join(t.TempDir(), "missing.py"), TimeoutMS: 100}
	if err := sim.ReplaceScenario(context.Background(), bad); err == nil {
		t.Fatal("missing Python script was accepted")
	}
	if _, err := sim.AddTicket(context.Background(), TicketInput{Rule: key.Rule, TicketID: 901, CreatedAt: time.Now().UnixMilli(), ObjectFacts: FactSnapshot{StringLists: map[string][]string{"object_tag": {"ok"}}}}); err != nil {
		t.Fatalf("old runtime was not retained: %v", err)
	}
	encoded, err := json.Marshal(scenario)
	if err != nil || len(encoded) == 0 {
		t.Fatal("scenario did not remain serializable")
	}
}

func TestPythonProviderFailureIsTerminalAndInspectable(t *testing.T) {
	python := testPythonExecutable(t)
	script := filepath.Join(t.TempDir(), "provider.py")
	if err := os.WriteFile(script, []byte("def describe():\n    return {\"tick\": {\"id\": \"test.empty\", \"version\": \"v1\", \"facts\": []}}\n\ndef provide(method, data):\n    raise ValueError('deliberate provider failure')\n"), 0600); err != nil {
		t.Fatal(err)
	}
	worker, err := StartPythonProvider(PythonProviderConfig{Python: python, Script: script, TimeoutMS: 500}, []fact.Spec{{Name: "clock", Type: fact.TypeInt64, Scope: fact.ScopeTick}})
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Close()
	_, err = worker.Tick(context.Background(), matchsystem.TickFactInput{Now: 123})
	var providerErr *PythonProviderError
	if !errors.As(err, &providerErr) || providerErr.Method != "tick" {
		t.Fatalf("provider failure was not typed: %v", err)
	}
	if !worker.Closed() || worker.Failure() == nil {
		t.Fatalf("provider failure did not close and retain diagnostic: closed=%v failure=%v", worker.Closed(), worker.Failure())
	}
}

func TestProductionPythonProviderTimeoutReapsWorker(t *testing.T) {
	python := testPythonExecutable(t)
	script := writePythonProviderScript(t, `import time
def provide(method, data):
    time.sleep(60)
    return {"Int64Values": {"clock": 1}}
`)
	worker, err := StartPythonProvider(PythonProviderConfig{
		Python:    python,
		Script:    script,
		TimeoutMS: 75,
	}, []fact.Spec{{Name: "clock", Type: fact.TypeInt64, Scope: fact.ScopeTick}})
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Close()

	started := time.Now()
	err = pythonTickWithin(t, worker, context.Background(), 2*time.Second)
	if elapsed := time.Since(started); elapsed >= 2*time.Second {
		t.Fatalf("provider timeout was not returned promptly: %s", elapsed)
	}
	var providerErr *PythonProviderError
	if !errors.As(err, &providerErr) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("unexpected production provider timeout: %v", err)
	}
	if providerErr.Method != "tick" || worker.Failure() == nil {
		t.Fatalf("timeout did not retain terminal provider failure: method=%q failure=%v", providerErr.Method, worker.Failure())
	}
	assertPythonWorkerReaped(t, worker)
}

func TestProductionPythonProviderCancellationReapsWorker(t *testing.T) {
	python := testPythonExecutable(t)
	script := writePythonProviderScript(t, `import time
def provide(method, data):
    time.sleep(60)
    return {"Int64Values": {"clock": 1}}
`)
	worker, err := StartPythonProvider(PythonProviderConfig{
		Python:    python,
		Script:    script,
		TimeoutMS: 2000,
	}, []fact.Spec{{Name: "clock", Type: fact.TypeInt64, Scope: fact.ScopeTick}})
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	started := time.Now()
	go func() {
		_, callErr := worker.Tick(ctx, matchsystem.TickFactInput{Now: 1})
		result <- callErr
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	var callErr error
	select {
	case callErr = <-result:
	case <-time.After(2 * time.Second):
		forceTerminatePythonWorker(worker)
		t.Fatalf("provider cancellation did not return promptly")
	}
	if elapsed := time.Since(started); elapsed >= 2*time.Second {
		t.Fatalf("provider cancellation was not returned promptly: %s", elapsed)
	}
	if !errors.Is(callErr, context.Canceled) {
		t.Fatalf("unexpected production provider cancellation: %v", callErr)
	}
	if worker.Failure() != nil {
		t.Fatalf("caller cancellation was recorded as provider failure: %v", worker.Failure())
	}
	assertPythonWorkerReaped(t, worker)
}

func TestProductionPythonProviderDefaultPyLauncherReapsInterpreter(t *testing.T) {
	if stdRuntime.GOOS != "windows" {
		t.Skip("the py -3 launcher lifecycle is Windows-specific")
	}
	if _, err := exec.LookPath("py"); err != nil {
		t.Skip("Python launcher unavailable")
	}
	t.Setenv("PYTHON_PROVIDER_EXECUTABLE", "")
	marker := filepath.Join(t.TempDir(), "interpreter.pid")
	script := writePythonProviderScript(t, pythonProviderPIDSleepScript(marker))
	worker, err := StartPythonProvider(PythonProviderConfig{Script: script, TimeoutMS: 75}, []fact.Spec{{Name: "clock", Type: fact.TypeInt64, Scope: fact.ScopeTick}})
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Close()
	interpreterPID := readPythonProviderPID(t, marker)
	if worker.cmd == nil || worker.cmd.Process == nil {
		t.Fatal("default py -3 provider has no process handle")
	}
	launcherPID := worker.cmd.Process.Pid
	t.Logf("py -3 launcher pid=%d interpreter pid=%d", launcherPID, interpreterPID)
	if launcherPID == interpreterPID {
		t.Log("Python launcher reused the interpreter process; checking that process directly")
	}
	err = pythonTickWithin(t, worker, context.Background(), 2*time.Second)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("unexpected default py -3 provider timeout: %v", err)
	}
	assertPythonWorkerReaped(t, worker)
	waitForProcessExit(t, interpreterPID)
}

func TestFixedTrafficPythonProviderSpeedDoesNotChangeVirtualResults(t *testing.T) {
	python := testPythonExecutable(t)
	const source = `import time
def describe():
    return {
        "tick": {"id": "python.tick", "version": "1", "facts": [{"name": "clock", "type": "int64", "scope": "tick"}]},
        "object": {"id": "python.object", "version": "1", "facts": [{"name": "objectID", "type": "uint64s", "scope": "object", "maxValues": 1}]},
        "match": {"id": "python.match", "version": "1", "facts": [{"name": "members", "type": "int64", "scope": "match"}]}
    }

def provide(method, data):
    if method == "tick":
        {{SLEEP}}
        return {"Int64Values": {"clock": data["Now"]}}
    if method == "object":
        return {"Uint64Lists": {"objectID": [7]}}
    if method == "initialize":
        return {"Int64Values": {"members": 1}}
    if method == "join":
        return {"Int64Values": {"members": data["MatchFactsBefore"]["Int64Values"]["members"] + 1}}
    raise ValueError("unknown method")
`
	// The script receives the same logical inputs in both runs. A separate
	// process sleeps in the slow case, proving that host RPC duration does not
	// advance fixed virtual time or alter the seeded ticket order.
	run := func(t *testing.T, delay string) (TrafficStatus, []MatchRecord) {
		t.Helper()
		script := filepath.Join(t.TempDir(), "provider.py")
		body := strings.Replace(source, "{{SLEEP}}", "time.sleep("+delay+")", 1)
		if err := os.WriteFile(script, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		key := identity.LogicalNodeKey{Rule: identity.RuleKey{Namespace: "python-speed", RuleID: 1}, PlacementID: "p1"}
		rule := NewRuleSpec(key, "p1", []byte(fixedPythonRuleJSON))
		rule.PythonProvider = &PythonProviderConfig{Python: python, Script: script, TimeoutMS: 2000}
		sim, err := NewService(Scenario{PhysicalNodes: []PhysicalNodeSpec{NewPhysicalNodeSpec("p1", "inproc://p1")}, Rules: []RuleSpec{rule}})
		if err != nil {
			t.Fatal(err)
		}
		defer sim.Close()
		const start = int64(1_800_000_000_000)
		if _, err := sim.StartTraffic(TrafficConfig{
			Mode:            TrafficModeFixed,
			Distribution:    "burst",
			Rate:            1,
			BurstSize:       2,
			BurstIntervalMS: 100,
			MatchIntervalMS: 100,
			MaxMatches:      100,
			Seed:            41,
			Steps:           3,
			StepMS:          100,
			StartTimeMS:     start,
		}, BatchGeneratorSpec{Rule: key.Rule, FirstTicketID: 10, Seed: 43, StringLists: map[string][]string{"group": {"all"}}}); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(3 * time.Second)
		for {
			status := sim.Traffic()
			if status.State == "completed" {
				page, listErr := sim.ListMatches(context.Background(), MatchQuery{Limit: 100})
				if listErr != nil {
					t.Fatal(listErr)
				}
				return status, page.Items
			}
			if status.State == "failed" || time.Now().After(deadline) {
				t.Fatalf("fixed Python run did not complete: %+v", status)
			}
			time.Sleep(time.Millisecond)
		}
	}
	fastStatus, fastMatches := run(t, "0.0")
	slowStatus, slowMatches := run(t, "0.02")
	if fastStatus.VirtualNow != slowStatus.VirtualNow || fastStatus.Injected != slowStatus.Injected || fastStatus.Produced != slowStatus.Produced || len(fastMatches) != len(slowMatches) {
		t.Fatalf("provider wall time changed fixed result: fast=%+v slow=%+v", fastStatus, slowStatus)
	}
	for index := range fastMatches {
		fast, slow := fastMatches[index], slowMatches[index]
		if fast.Now != slow.Now || fast.DurationMs != slow.DurationMs || len(fast.Tickets) != len(slow.Tickets) || fast.Facts.Int64Values["members"] != 2 || slow.Facts.Int64Values["members"] != 2 {
			t.Fatalf("logical match result differs at %d: fast=%#v slow=%#v", index, fast, slow)
		}
		for ticketIndex := range fast.Tickets {
			if fast.Tickets[ticketIndex].TicketID != slow.Tickets[ticketIndex].TicketID || fast.Tickets[ticketIndex].CreatedAt != slow.Tickets[ticketIndex].CreatedAt {
				t.Fatalf("ticket order/time differs at match %d: fast=%#v slow=%#v", index, fast.Tickets, slow.Tickets)
			}
		}
	}
}

func TestProductionPythonProviderScenarioReplacementAndCloseReapWorkers(t *testing.T) {
	python := testPythonExecutable(t)
	initialScripts := []string{
		writePythonProviderScript(t, `def provide(method, data):
    return {}
`),
		writePythonProviderScript(t, `def provide(method, data):
    return {}
`),
	}
	scenario, keys := pythonProviderScenario(t, python, initialScripts)
	sim, err := NewService(scenario)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sim.Close() })
	oldWorkers := snapshotPythonProviders(t, sim, keys)

	replacementScripts := []string{
		writePythonProviderScript(t, `def provide(method, data):
    return {}
`),
		writePythonProviderScript(t, `def provide(method, data):
    return {}
`),
	}
	replacement := scenario.Clone()
	for index := range replacement.Rules {
		replacement.Rules[index].PythonProvider.Script = replacementScripts[index]
	}
	if err := sim.ReplaceScenario(context.Background(), replacement); err != nil {
		t.Fatal(err)
	}
	newWorkers := snapshotPythonProviders(t, sim, keys)
	for _, key := range keys {
		oldWorker := oldWorkers[key]
		newWorker := newWorkers[key]
		if oldWorker == nil || newWorker == nil || oldWorker == newWorker {
			t.Fatalf("scenario replacement did not publish independent workers for %s", key)
		}
		assertPythonWorkerReaped(t, oldWorker)
		if newWorker.Closed() {
			t.Fatalf("replacement worker was closed before service shutdown: %s", key)
		}
	}
	if err := sim.Close(); err != nil {
		t.Fatal(err)
	}
	for _, key := range keys {
		assertPythonWorkerReaped(t, newWorkers[key])
	}
}

func TestPythonProviderCandidateBuildFailureReapsAllStartedWorkers(t *testing.T) {
	python := testPythonExecutable(t)
	markerDir := t.TempDir()
	markers := []string{filepath.Join(markerDir, "worker-1.pid"), filepath.Join(markerDir, "worker-2.pid")}
	scripts := []string{
		writePythonProviderScript(t, pythonProviderPIDScript(markers[0])),
		writePythonProviderScript(t, pythonProviderPIDScript(markers[1])),
		filepath.Join(markerDir, "missing-provider.py"),
	}
	scenario, _ := pythonProviderScenario(t, python, scripts)
	if _, err := NewService(scenario); err == nil {
		t.Fatal("candidate runtime with a missing later provider unexpectedly succeeded")
	}
	for _, marker := range markers {
		pid := readPythonProviderPID(t, marker)
		waitForProcessExit(t, pid)
	}
}

const fixedPythonRuleJSON = `{"schemaVersion":"match-rule/v1","ruleKey":{"namespace":"python-speed","ruleId":1},"contract":{"schemaVersion":"logical-node-contract/v3","attributes":[{"name":"group","type":"strings","maxValues":1}],"facts":[{"name":"clock","type":"int64","scope":"tick"},{"name":"objectID","type":"uint64s","scope":"object","maxValues":1},{"name":"members","type":"int64","scope":"match"}],"indexes":[{"type":"multi_value","name":"group","keyType":"string","maxDocumentValues":1,"maxQueryValues":1}]},"prefilter":{"schemaVersion":"prefilter/v3","bitmap":{"resultType":"bitmap","expr":{"op":"lookup_string","index":"group","values":{"schemaVersion":"expression-scalar/v3","resultType":"strings","expr":{"op":"strings_literal","values":["all"]}}}}},"evaluation":{"schemaVersion":"evaluation/v3","canJoin":{"schemaVersion":"expression-scalar/v3","resultType":"bool","expr":{"op":"uint64s_contains","values":{"op":"uint64s_ref","source":"candidate_facts","name":"objectID"},"needle":7}},"canComplete":{"schemaVersion":"expression-scalar/v3","resultType":"bool","expr":{"op":"bool_and","children":[{"op":"int64_gte","left":{"op":"int64_ref","source":"tick_facts","name":"clock"},"right":{"op":"int64_literal","value":0}},{"op":"int64_gte","left":{"op":"int64_ref","source":"match_facts","name":"members"},"right":{"op":"int64_literal","value":2}}]}}},"scoring":{"type":"constant","params":{"value":0}},"seedSelection":{"type":"arrival","params":{}},"runtime":{"candidateScoringLimitPerSeed":500,"candidateLimitPerSeed":50,"maxPlayers":8,"attemptLimitPerProduceMatch":500,"attemptLimitPerMatchRound":500}}`

func testPythonExecutable(t *testing.T) string {
	t.Helper()
	python := strings.TrimSpace(os.Getenv("PYTHON_PROVIDER_EXECUTABLE"))
	if python == "" && stdRuntime.GOOS == "windows" {
		if _, err := exec.LookPath("py"); err == nil {
			if output, resolveErr := exec.Command("py", "-3", "-c", "import sys; print(sys.executable)").Output(); resolveErr == nil {
				python = strings.TrimSpace(string(output))
			}
		}
	}
	if python == "" {
		python = "python"
	}
	if _, err := exec.LookPath(python); err != nil || (filepath.IsAbs(python) && !fileExists(python)) {
		t.Skip("Python executable unavailable; set PYTHON_PROVIDER_EXECUTABLE")
	}
	return python
}

func writePythonProviderScript(t *testing.T, source string) string {
	t.Helper()
	if !strings.Contains(source, "describe") {
		source = "def describe():\n    return {\"tick\": {\"id\": \"test.empty\", \"version\": \"v1\", \"facts\": []}}\n\n" + source
	}
	path := filepath.Join(t.TempDir(), "provider.py")
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func pythonTickWithin(t *testing.T, worker *PythonWorker, ctx context.Context, limit time.Duration) error {
	t.Helper()
	result := make(chan error, 1)
	go func() {
		_, err := worker.Tick(ctx, matchsystem.TickFactInput{Now: 1})
		result <- err
	}()
	timer := time.NewTimer(limit)
	defer timer.Stop()
	select {
	case err := <-result:
		return err
	case <-timer.C:
		forceTerminatePythonWorker(worker)
		select {
		case <-result:
		case <-time.After(limit):
			t.Fatalf("provider call remained blocked after forced process termination")
		}
		return context.DeadlineExceeded
	}
}

func forceTerminatePythonWorker(worker *PythonWorker) {
	if worker == nil {
		return
	}
	if worker.stdin != nil {
		_ = worker.stdin.Close()
	}
	if worker.cmd != nil && worker.cmd.Process != nil {
		_ = worker.cmd.Process.Kill()
	}
	if worker.stdout != nil {
		_ = worker.stdout.Close()
	}
}

func assertPythonWorkerReaped(t *testing.T, worker *PythonWorker) {
	t.Helper()
	if worker == nil || !worker.Closed() {
		t.Fatalf("Python worker is not closed: %#v", worker)
	}
	if worker.cmd == nil || worker.cmd.ProcessState == nil {
		t.Fatalf("Python worker process was not reaped: %#v", worker.cmd)
	}
}

func pythonProviderScenario(t *testing.T, python string, scripts []string) (Scenario, []identity.LogicalNodeKey) {
	t.Helper()
	base, _ := testScenario()
	// Lifecycle workers in this helper only exercise process ownership and do
	// not advertise capabilities. Keep the rule contract empty so startup does
	// not require a provider descriptor from those intentionally minimal scripts.
	base.Rules[0].RuleJSON = bytes.Replace(
		base.Rules[0].RuleJSON,
		[]byte(`"facts":[{"name":"object_tag","type":"strings","scope":"object","maxValues":2}]`),
		[]byte(`"facts":[]`),
		1,
	)
	scenario := Scenario{SchemaVersion: base.SchemaVersion}
	keys := make([]identity.LogicalNodeKey, 0, len(scripts))
	for index, script := range scripts {
		physical := identity.PhysicalNodeID(fmt.Sprintf("python-provider-%d", index+1))
		key := identity.LogicalNodeKey{
			Rule:        identity.RuleKey{Namespace: "python-lifecycle", RuleID: int32(index + 1)},
			PlacementID: identity.PlacementID(physical),
		}
		rule := base.Rules[0]
		rule.LogicalNode = key
		rule.PhysicalNodeID = physical
		rule.RuleJSON = bytes.Replace(
			append([]byte(nil), base.Rules[0].RuleJSON...),
			[]byte(`"namespace":"test","ruleId":1`),
			[]byte(fmt.Sprintf(`"namespace":"python-lifecycle","ruleId":%d`, index+1)),
			1,
		)
		rule.PythonProvider = &PythonProviderConfig{Python: python, Script: script, TimeoutMS: 500}
		scenario.PhysicalNodes = append(scenario.PhysicalNodes, NewPhysicalNodeSpec(physical, common.Endpoint("inproc://"+string(physical))))
		scenario.Rules = append(scenario.Rules, rule)
		keys = append(keys, key)
	}
	return scenario, keys
}

func snapshotPythonProviders(t *testing.T, sim *Simulator, keys []identity.LogicalNodeKey) map[identity.LogicalNodeKey]*PythonWorker {
	t.Helper()
	sim.mu.RLock()
	defer sim.mu.RUnlock()
	if sim.runtime == nil {
		t.Fatal("simulator runtime is not published")
	}
	workers := make(map[identity.LogicalNodeKey]*PythonWorker, len(keys))
	for _, key := range keys {
		workers[key] = sim.runtime.providers[key]
	}
	return workers
}

func pythonProviderPIDScript(marker string) string {
	return fmt.Sprintf(`import os
from pathlib import Path
Path(%q).write_text(str(os.getpid()), encoding="ascii")
def provide(method, data):
    return {}
`, filepath.ToSlash(marker))
}

func pythonProviderPIDSleepScript(marker string) string {
	return fmt.Sprintf(`import os
import time
from pathlib import Path
Path(%q).write_text(str(os.getpid()), encoding="ascii")
def provide(method, data):
    time.sleep(60)
    return {"Int64Values": {"clock": 1}}
`, filepath.ToSlash(marker))
}

func readPythonProviderPID(t *testing.T, marker string) int {
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

func waitForProcessExit(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		alive, err := pythonProcessAlive(pid)
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

func pythonProcessAlive(pid int) (bool, error) {
	if stdRuntime.GOOS == "windows" {
		output, err := exec.Command("tasklist", "/FI", fmt.Sprintf("PID eq %d", pid), "/FO", "CSV", "/NH").CombinedOutput()
		if err != nil {
			return false, fmt.Errorf("tasklist provider PID %d: %w (%s)", pid, err, strings.TrimSpace(string(output)))
		}
		return bytes.Contains(output, []byte(fmt.Sprintf(`"%d"`, pid))), nil
	}
	return exec.Command("kill", "-0", strconv.Itoa(pid)).Run() == nil, nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
