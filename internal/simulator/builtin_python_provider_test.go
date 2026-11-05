package simulator

import (
	"context"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"matchSystem/internal/common"
	"matchSystem/internal/identity"
	"matchSystem/internal/matchsystem"
	"matchSystem/internal/matchsystem/fact"
)

func TestBuiltinPythonProviderMatchesGoDefaultsAcrossScopes(t *testing.T) {
	python := testPythonExecutable(t)
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	script := filepath.Join(filepath.Dir(sourceFile), "..", "..", "examples", "simulator", "builtin_provider.py")

	specs := []fact.Spec{
		{Name: WaitingCountFactName, Type: fact.TypeInt64, Scope: fact.ScopeTick},
		{Name: "preferredRoles", Type: fact.TypeStrings, Scope: fact.ScopeObject, MaxValues: 3},
		{Name: "latencyMs", Type: fact.TypeInt64, Scope: fact.ScopeObject},
		{Name: WaitingTimeFactName, Type: fact.TypeInt64, Scope: fact.ScopeObject},
		{Name: MemberCountFactName, Type: fact.TypeInt64, Scope: fact.ScopeMatch},
	}
	static := FactSnapshot{Int64Values: map[string]int64{WaitingCountFactName: 999}}
	worker, err := startPythonProvider(PythonProviderConfig{Python: python, Script: script, TimeoutMS: 1000}, specs, newPythonProviderStartupInput(specs, static.values()))
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Close()
	descriptors := worker.HandshakeDescriptors()
	if descriptors.Tick == nil || descriptors.Tick.ID != "simulator.tick-facts" ||
		descriptors.Object == nil || descriptors.Object.ID != "simulator.object-facts" ||
		descriptors.Match == nil || descriptors.Match.ID != "simulator.match-facts" {
		t.Fatalf("Python built-in descriptors do not match Go IDs: %#v", descriptors)
	}

	input := matchsystem.TickFactInput{
		Now: 1000,
		Node: matchsystem.LogicalNodeSnapshot{
			WaitingCount: 2,
		},
	}
	var goTick, pythonTick matchsystem.Facts
	for _, state := range []matchsystem.LogicalNodeState{
		matchsystem.LogicalNodeReady,
		matchsystem.LogicalNodeDraining,
		matchsystem.LogicalNodeStopped,
	} {
		input.Node.State = state
		goTick, err = simulatorTickFactProvider(static, specs).Provide(context.Background(), input)
		if err != nil {
			t.Fatal(err)
		}
		pythonTick, err = worker.Tick(context.Background(), input)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(goTick.Int64Values, pythonTick.Int64Values) {
			t.Fatalf("Tick values differ for state %s: Go=%#v Python=%#v", state, goTick, pythonTick)
		}
		if pythonTick.Int64Values[WaitingCountFactName] != 2 {
			t.Fatalf("static waitingCount was not overlaid for state %s: %#v", state, pythonTick)
		}
	}

	owner := identity.OwnerRef{
		LogicalNode:    identity.LogicalNodeKey{Rule: identity.RuleKey{Namespace: "builtin-python", RuleID: 1}, PlacementID: "default"},
		PhysicalNodeID: "p1",
	}
	ticket := &common.Ticket{TicketID: 7, CreatedAt: 700}
	objectFacts := FactSnapshot{
		StringLists: map[string][]string{"preferredRoles": {"solo", "duo"}},
		Int64Values: map[string]int64{"latencyMs": 25},
	}
	registry := NewObservationRegistry()
	if _, err := registry.RecordTicket(owner, common.RouteDecision{DecisionID: "d1", Owner: owner, Endpoint: "inproc://p1"}, ticket, objectFacts); err != nil {
		t.Fatal(err)
	}
	goObjectProvider := simulatorObjectFactProvider(registry, owner, specs)
	layout, err := fact.NewObjectLayout(specs)
	if err != nil {
		t.Fatal(err)
	}
	var goSlot fact.ObjectSlot
	goSlot.Init(layout)
	goFrame := fact.NewFrame(goTick, 1, false)
	goObject, _, err := goFrame.Object(&goSlot, ticket, 1000, goObjectProvider.Provide)
	if err != nil {
		t.Fatal(err)
	}

	var pythonSlot fact.ObjectSlot
	pythonSlot.Init(layout)
	pythonFrame := fact.NewFrame(pythonTick, 1, false)
	_, pythonProvider, _ := pythonProviderCallbacks(worker, registry, owner)
	pythonObject, _, err := pythonFrame.Object(&pythonSlot, ticket, 1000, pythonProvider.Provide)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(goObject, pythonObject) {
		t.Fatalf("Object values differ: Go=%#v Python=%#v", goObject, pythonObject)
	}
	if pythonObject.Int64Values[WaitingTimeFactName] != 300 {
		t.Fatalf("waitingTime=%d, want 300", pythonObject.Int64Values[WaitingTimeFactName])
	}

	goMatchProvider := defaultMatchFactProvider{specs: []fact.Spec{{Name: MemberCountFactName, Type: fact.TypeInt64, Scope: fact.ScopeMatch}}}
	goMatch, err := goMatchProvider.Initialize(context.Background(), matchsystem.InitializeInput{})
	if err != nil {
		t.Fatal(err)
	}
	pythonMatch, err := worker.Initialize(context.Background(), matchsystem.InitializeInput{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(goMatch.Int64Values, pythonMatch.Int64Values) || goMatch.Int64Values[MemberCountFactName] != 1 {
		t.Fatalf("Initialize values differ: Go=%#v Python=%#v", goMatch, pythonMatch)
	}
	goMatch, err = goMatchProvider.OnJoin(context.Background(), matchsystem.JoinInput{MatchFactsBefore: goMatch})
	if err != nil {
		t.Fatal(err)
	}
	pythonMatch, err = worker.OnJoin(context.Background(), matchsystem.JoinInput{MatchFactsBefore: pythonMatch})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(goMatch.Int64Values, pythonMatch.Int64Values) || goMatch.Int64Values[MemberCountFactName] != 2 {
		t.Fatalf("Join values differ: Go=%#v Python=%#v", goMatch, pythonMatch)
	}
}

func TestBuiltinPythonProviderProjectsContractSubset(t *testing.T) {
	python := testPythonExecutable(t)
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	script := filepath.Join(filepath.Dir(sourceFile), "..", "..", "examples", "simulator", "builtin_provider.py")
	specs := []fact.Spec{
		{Name: WaitingCountFactName, Type: fact.TypeInt64, Scope: fact.ScopeTick},
		{Name: MemberCountFactName, Type: fact.TypeInt64, Scope: fact.ScopeMatch},
	}
	static := FactSnapshot{Int64Values: map[string]int64{WaitingCountFactName: 999}}
	worker, err := startPythonProvider(PythonProviderConfig{Python: python, Script: script, TimeoutMS: 1000}, specs, newPythonProviderStartupInput(specs, static.values()))
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Close()

	tick, err := worker.Tick(context.Background(), matchsystem.TickFactInput{Node: matchsystem.LogicalNodeSnapshot{WaitingCount: 3}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(tick.StringLists, map[string][]string{}) || !reflect.DeepEqual(tick.Uint64Lists, map[string][]uint64{}) {
		t.Fatalf("subset Tick returned undeclared typed Facts: %#v", tick)
	}
	if len(tick.Int64Values) != 1 || tick.Int64Values[WaitingCountFactName] != 3 {
		t.Fatalf("subset Tick=%#v, want only waitingCount=3", tick)
	}
	match, err := worker.Initialize(context.Background(), matchsystem.InitializeInput{})
	if err != nil {
		t.Fatal(err)
	}
	if len(match.Int64Values) != 1 || match.Int64Values[MemberCountFactName] != 1 {
		t.Fatalf("subset Match=%#v, want only memberCount=1", match)
	}
}
