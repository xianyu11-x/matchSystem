package pythonprovider

import (
	"context"
	"errors"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"matchSystem/internal/identity"
	ms "matchSystem/internal/matchsystem"
	"matchSystem/internal/matchsystem/fact"
	"matchSystem/internal/simulator"
)

var specs = []fact.Spec{
	{Name: "clock", Type: fact.TypeInt64, Scope: fact.ScopeTick},
	{Name: "ids", Type: fact.TypeUint64s, Scope: fact.ScopeObject, MaxValues: 1},
	{Name: "labels", Type: fact.TypeStrings, Scope: fact.ScopeObject, MaxValues: 1},
	{Name: "members", Type: fact.TypeInt64, Scope: fact.ScopeMatch},
}

func start(t testing.TB, script string, timeout time.Duration) *Worker {
	t.Helper()
	python := os.Getenv("PYTHON_PROVIDER_EXECUTABLE")
	if python == "" {
		python = "python"
	}
	if _, err := exec.LookPath(python); err != nil {
		t.Skip("Python unavailable; set PYTHON_PROVIDER_EXECUTABLE")
	}
	w, err := Start(python, "runner.py", script, timeout, specs)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(w.Close)
	return w
}
func script(t testing.TB, source string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "provider.py")
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPrecisionAndThreeContracts(t *testing.T) {
	w := start(t, "example.py", 2*time.Second)
	for _, n := range []int64{math.MinInt64, math.MaxInt64, 9007199254740993} {
		got, err := w.Tick(context.Background(), ms.TickFactInput{Now: n})
		if err != nil || got.Int64Values["clock"] != n {
			t.Fatalf("int64 %d: %v %v", n, got, err)
		}
	}
	layout, err := fact.NewObjectLayout(specs)
	if err != nil {
		t.Fatal(err)
	}
	var slot fact.ObjectSlot
	slot.Init(layout)
	frame := fact.NewFrame(ms.Facts{}, 1, false)
	got, access, err := frame.Object(&slot, &ms.Ticket{TicketID: math.MaxUint64}, 0, w.Object)
	if err != nil || got.Uint64Lists["ids"][0] != math.MaxUint64 || got.StringLists["labels"][0] != "中文" || !access.ProviderCalled {
		t.Fatalf("object: %v %v", got, err)
	}
	_, access, err = frame.Object(&slot, &ms.Ticket{TicketID: math.MaxUint64}, 0, w.Object)
	if err != nil || !access.CacheHit || access.ProviderCalled {
		t.Fatal("object cache contract", access, err)
	}
	m, err := w.Initialize(context.Background(), ms.InitializeInput{})
	if err != nil || m.Int64Values["members"] != 1 {
		t.Fatal(m, err)
	}
	m, err = w.OnJoin(context.Background(), ms.JoinInput{MatchFactsBefore: m})
	if err != nil || m.Int64Values["members"] != 2 {
		t.Fatal(m, err)
	}
}

func TestErrorsAndReaping(t *testing.T) {
	cases := map[string]string{
		"exception":     "raise ValueError('deliberate')",
		"crash":         "__import__('os')._exit(7)",
		"malformed":     "__import__('os').write(1,b'not-json\\n'); return {}",
		"overflow":      "return {'Int64Values': {'clock': 9223372036854775808}}",
		"negative_uint": "return {'Uint64Lists': {'ids': [-1]}}",
		"uint_overflow": "return {'Uint64Lists': {'ids': [18446744073709551616]}}",
		"fraction":      "return {'Int64Values': {'clock': 1.5}}",
		"wrong_scope":   "return {'Int64Values': {'members': 1}}",
		"wrong_type":    "return {'StringLists': {'clock': ['x']}}",
		"unknown":       "return {'Int64Values': {'unknown': 1}}",
		"oversize":      "return {'StringLists': {'labels': ['x' * 1100000]}}",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			w := start(t, script(t, "def provide(method, data):\n    "+body+"\n"), 2*time.Second)
			if _, err := w.Tick(context.Background(), ms.TickFactInput{}); err == nil {
				t.Fatal("accepted invalid response")
			}
			if !w.closed || w.cmd.ProcessState == nil {
				t.Fatal("interpreter not reaped")
			}
			if _, err := w.Tick(context.Background(), ms.TickFactInput{}); err == nil {
				t.Fatal("failed worker reused")
			}
		})
	}
	for name, body := range map[string]string{"incomplete": "return {}", "limit": "return {'Uint64Lists': {'ids': [1,2]}}"} {
		t.Run(name, func(t *testing.T) {
			w := start(t, script(t, "def provide(method, data):\n    "+body+"\n"), 2*time.Second)
			scope := fact.ScopeMatch
			if name == "limit" {
				scope = fact.ScopeObject
			}
			if _, err := w.call(context.Background(), "initialize", nil, scope); err == nil {
				t.Fatal("accepted invalid contract")
			}
		})
	}
}

func TestTimeoutAndCancellation(t *testing.T) {
	p := script(t, "import time\ndef provide(method, data):\n    if data['Now'] == 1: time.sleep(60)\n    return {'Int64Values': {'clock': 0}}\n")
	for _, cancelEarly := range []bool{false, true} {
		w := start(t, p, 2*time.Second)
		if _, err := w.Tick(context.Background(), ms.TickFactInput{}); err != nil {
			t.Fatal(err)
		}
		w.timeout = 50 * time.Millisecond
		ctx, cancel := context.WithCancel(context.Background())
		if cancelEarly {
			time.AfterFunc(10*time.Millisecond, cancel)
		}
		begin := time.Now()
		_, err := w.Tick(ctx, ms.TickFactInput{Now: 1})
		cancel()
		if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		if time.Since(begin) > time.Second || w.cmd.ProcessState == nil {
			t.Fatal("timeout/reap exceeded bound")
		}
		t.Logf("cancel=%v elapsed=%s reaped=true", cancelEarly, time.Since(begin))
	}
}

func TestExplicitReloadAndProcessIsolation(t *testing.T) {
	p := script(t, "counter = 0\ndef provide(method, data):\n    global counter\n    counter += 1\n    return {'Int64Values': {'clock': counter}}\n")
	a, b := start(t, p, 2*time.Second), start(t, p, 2*time.Second)
	for _, want := range []int64{1, 2} {
		v, e := a.Tick(context.Background(), ms.TickFactInput{})
		if e != nil || v.Int64Values["clock"] != want {
			t.Fatal(v, e)
		}
	}
	v, e := b.Tick(context.Background(), ms.TickFactInput{})
	if e != nil || v.Int64Values["clock"] != 1 || a.cmd.Process.Pid == b.cmd.Process.Pid {
		t.Fatal("state leaked", v, e)
	}
	if err := os.WriteFile(p, []byte("def provide(method, data):\n    return {'Int64Values': {'clock': 99}}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	v, e = b.Tick(context.Background(), ms.TickFactInput{})
	if e != nil || v.Int64Values["clock"] != 2 {
		t.Fatal("unexpected implicit reload", v, e)
	}
	a.Close()
	a = start(t, p, 2*time.Second)
	v, e = a.Tick(context.Background(), ms.TickFactInput{})
	if e != nil || v.Int64Values["clock"] != 99 {
		t.Fatal("edited script not loaded", v, e)
	}
}

const ruleJSON = `{"schemaVersion":"match-rule/v1","ruleKey":{"namespace":"python","ruleId":1},
"contract":{"schemaVersion":"logical-node-contract/v3","attributes":[{"name":"partition","type":"strings","maxValues":1}],"indexes":[{"type":"multi_value","name":"partition","keyType":"string","maxDocumentValues":1,"maxQueryValues":1}],"facts":[
{"name":"clock","type":"int64","scope":"tick"},{"name":"ids","type":"uint64s","scope":"object","maxValues":1},
{"name":"labels","type":"strings","scope":"object","maxValues":1},{"name":"members","type":"int64","scope":"match"}]},
"prefilter":{"schemaVersion":"prefilter/v3","bitmap":{"resultType":"bitmap","expr":{"op":"lookup_string","index":"partition","values":{"schemaVersion":"expression-scalar/v3","resultType":"strings","expr":{"op":"strings_literal","values":["blue"]}}}}},
"evaluation":{"schemaVersion":"evaluation/v3","canJoin":{"schemaVersion":"expression-scalar/v3","resultType":"bool","expr":{"op":"bool_literal","value":true}},
"canComplete":{"schemaVersion":"expression-scalar/v3","resultType":"bool","expr":{"op":"int64_gte","left":{"op":"int64_ref","source":"match_facts","name":"members"},"right":{"op":"int64_literal","value":2}}}},
"scoring":{"type":"constant","params":{"value":0}},"seedSelection":{"type":"arrival","params":{}},
"runtime":{"candidateScoringLimitPerSeed":500,"candidateLimitPerSeed":50,"maxPlayers":8,"attemptLimitPerProduceMatch":500,"attemptLimitPerMatchRound":500}}`

func TestLogicalNodesProduceMatch(t *testing.T) {
	for _, placement := range []string{"a", "b"} {
		w := start(t, "example.py", 2*time.Second)
		desc := func(scope fact.Scope) *ms.ProviderDescriptor {
			d := &ms.ProviderDescriptor{ID: "python.example", Version: "1"}
			for _, s := range specs {
				if s.Scope == scope {
					d.Facts = append(d.Facts, s)
				}
			}
			return d
		}
		node, err := ms.NewLogicalNode(ms.LogicalNodeSpec{Key: identity.LogicalNodeKey{Rule: identity.RuleKey{Namespace: "python", RuleID: 1}, PlacementID: identity.PlacementID(placement)}, RuleJSON: []byte(ruleJSON), FactProvider: w.Tick, ObjectFactProvider: w.Object, MatchFactProvider: w, FactProviderDescriptor: desc(fact.ScopeTick), ObjectFactProviderDescriptor: desc(fact.ScopeObject), MatchFactProviderDescriptor: desc(fact.ScopeMatch), MatchFactSnapshotMode: ms.MatchFactSnapshotModeDeepCopy})
		if err != nil {
			t.Fatal(err)
		}
		for _, id := range []uint64{math.MaxUint64, 9007199254740993} {
			if err := node.Add(&ms.Ticket{TicketID: id, StringLists: map[string][]string{"partition": {"blue"}}}); err != nil {
				t.Fatal(err)
			}
		}
		if err := node.BeginMatchRound(123); err != nil {
			t.Fatal(err)
		}
		match, err := node.ProduceMatch(context.Background())
		if err != nil || match == nil || len(match.Tickets) != 2 || match.Facts.Int64Values["members"] != 2 {
			t.Fatalf("%s: %v %v", placement, match, err)
		}
		if match.ObjectFacts[math.MaxUint64].Uint64Lists["ids"][0] != math.MaxUint64 {
			t.Fatal("committed precision loss")
		}
		t.Logf("node=%s members=%d maxUint64=%d", placement, len(match.Tickets), match.ObjectFacts[math.MaxUint64].Uint64Lists["ids"][0])
	}
}

func TestLatencyExperiment(t *testing.T) {
	begin := time.Now()
	w := start(t, "example.py", 2*time.Second)
	if _, err := w.Tick(context.Background(), ms.TickFactInput{}); err != nil {
		t.Fatal(err)
	}
	t.Logf("startup plus first RPC=%s", time.Since(begin))
	for _, size := range []int{0, 4096, 65536} {
		samples := make([]time.Duration, 300)
		input := ms.InitializeInput{SeedAttributes: &ms.Ticket{StringLists: map[string][]string{"payload": {strings.Repeat("x", size)}}}}
		for i := range samples {
			begin := time.Now()
			if _, err := w.Initialize(context.Background(), input); err != nil {
				t.Fatal(err)
			}
			samples[i] = time.Since(begin)
		}
		sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
		var total time.Duration
		for _, d := range samples {
			total += d
		}
		t.Logf("payload=%d n=300 mean=%s p50=%s p95=%s p99=%s", size, total/300, samples[150], samples[284], samples[296])
	}
}

func BenchmarkWarmRPC(b *testing.B) {
	w := start(b, "example.py", 2*time.Second)
	if _, err := w.Tick(context.Background(), ms.TickFactInput{}); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := w.Tick(context.Background(), ms.TickFactInput{Now: int64(i)}); err != nil {
			b.Fatal(err)
		}
	}
}
func BenchmarkNativeCallback(b *testing.B) {
	var sink ms.Facts
	provider := func(_ context.Context, in ms.TickFactInput) (ms.Facts, error) {
		return ms.Facts{Int64Values: map[string]int64{"clock": in.Now}}, nil
	}
	for i := 0; i < b.N; i++ {
		sink, _ = provider(context.Background(), ms.TickFactInput{Now: int64(i)})
	}
	if sink.Int64Values["clock"] < 0 {
		b.Fatal(sink)
	}
}

func TestSimulatorRunRound(t *testing.T) {
	w := start(t, "example.py", 2*time.Second)
	key := identity.LogicalNodeKey{Rule: identity.RuleKey{Namespace: "python", RuleID: 1}, PlacementID: "sim"}
	rule := simulator.NewRuleSpec(key, "host", []byte(ruleJSON))
	rule.FactProvider, rule.ObjectFactProvider, rule.MatchFactProvider = w.Tick, w.Object, w
	desc := func(scope fact.Scope) *ms.ProviderDescriptor {
		d := &ms.ProviderDescriptor{ID: "python.example", Version: "1"}
		for _, s := range specs {
			if s.Scope == scope {
				d.Facts = append(d.Facts, s)
			}
		}
		return d
	}
	rule.FactProviderDescriptor, rule.ObjectFactProviderDescriptor, rule.MatchFactProviderDescriptor = desc(fact.ScopeTick), desc(fact.ScopeObject), desc(fact.ScopeMatch)
	sim, err := simulator.NewSimulator(simulator.Scenario{SchemaVersion: simulator.ScenarioSchemaVersion, PhysicalNodes: []simulator.PhysicalNodeSpec{simulator.NewPhysicalNodeSpec("host", "inproc://host")}, Rules: []simulator.RuleSpec{rule}})
	if err != nil {
		t.Fatal(err)
	}
	defer sim.Close()
	for _, id := range []uint64{math.MaxUint64, 9007199254740993} {
		if _, err := sim.AddTicket(context.Background(), simulator.TicketInput{Rule: key.Rule, TicketID: id, StringLists: map[string][]string{"partition": {"blue"}}}); err != nil {
			t.Fatal(err)
		}
	}
	result, err := sim.RunRound(context.Background(), 123, 1)
	if err != nil || len(result.Matches) != 1 {
		t.Fatal(result, err)
	}
	match := result.Matches[0]
	if len(match.Tickets) != 2 || match.Facts.Int64Values["members"] != 2 || match.Tickets[0].ObjectFacts.Uint64Lists["ids"][0] != math.MaxUint64 {
		t.Fatal("simulator lost provider values", match)
	}
	t.Logf("simulator RunRound: %+v", result.Matches[0])
}

func TestObjectTimeoutReaps(t *testing.T) {
	w := start(t, script(t, "import time\ndef provide(method, data):\n    time.sleep(60)\n"), 50*time.Millisecond)
	layout, err := fact.NewObjectLayout(specs)
	if err != nil {
		t.Fatal(err)
	}
	var slot fact.ObjectSlot
	slot.Init(layout)
	begin := time.Now()
	_, _, err = fact.NewFrame(ms.Facts{}, 1, false).Object(&slot, &ms.Ticket{TicketID: 1}, 0, w.Object)
	if !errors.Is(err, context.DeadlineExceeded) || !w.closed || w.cmd.ProcessState == nil || time.Since(begin) > time.Second {
		t.Fatal("object timeout did not reap", err)
	}
	t.Logf("Object without context: elapsed=%s reaped=true", time.Since(begin))
}
