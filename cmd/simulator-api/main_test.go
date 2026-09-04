package main

import (
	"encoding/json"
	"net"
	"net/url"
	"os"
	"testing"

	"matchSystem/internal/matchsystem"
	"matchSystem/internal/simulator"
)

func TestWriteReadyUsesBoundPort(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := writeReady(writer, listener.Addr().String()); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	var ready struct {
		Type       string `json:"type"`
		APIBaseURL string `json:"apiBaseUrl"`
	}
	if err := json.NewDecoder(reader).Decode(&ready); err != nil {
		t.Fatal(err)
	}
	reader.Close()
	if ready.Type != "ready" {
		t.Fatalf("ready type=%q", ready.Type)
	}
	parsed, err := url.Parse(ready.APIBaseURL)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Scheme != "http" || parsed.Host != listener.Addr().String() {
		t.Fatalf("ready URL=%q listener=%q", ready.APIBaseURL, listener.Addr())
	}
}

func TestDefaultScenarioAdvertisesBuiltinFactSuperset(t *testing.T) {
	scenario := defaultScenario()
	if len(scenario.Rules) != 1 {
		t.Fatalf("default scenario rules=%d, want 1", len(scenario.Rules))
	}
	rule := scenario.Rules[0]

	if rule.FactProviderDescriptor == nil {
		t.Fatal("default scenario has no Tick Fact provider descriptor")
	}
	if got := rule.FactProviderDescriptor.Facts; len(got) != 1 ||
		got[0].Name != simulator.WaitingCountFactName ||
		got[0].Type != matchsystem.FactTypeInt64 || got[0].Scope != matchsystem.FactScopeTick {
		t.Fatalf("default Tick descriptor facts=%#v, want only standard waitingCount", got)
	}

	if rule.ObjectFactProviderDescriptor == nil {
		t.Fatal("default scenario has no Object Fact provider descriptor")
	}
	objectNames := make(map[string]bool, len(rule.ObjectFactProviderDescriptor.Facts))
	for _, spec := range rule.ObjectFactProviderDescriptor.Facts {
		objectNames[spec.Name] = true
	}
	if !objectNames[simulator.WaitingTimeFactName] {
		t.Fatalf("default Object descriptor does not advertise %q: %#v", simulator.WaitingTimeFactName, rule.ObjectFactProviderDescriptor.Facts)
	}
	if objectNames["waitTime"] || objectNames["waiting-time"] || objectNames["wait-time"] {
		t.Fatalf("default Object descriptor advertises compatibility aliases: %#v", rule.ObjectFactProviderDescriptor.Facts)
	}

	if rule.MatchFactProviderDescriptor == nil {
		t.Fatal("default scenario has no Match Fact provider descriptor")
	}
	matchNames := make(map[string]bool, len(rule.MatchFactProviderDescriptor.Facts))
	for _, spec := range rule.MatchFactProviderDescriptor.Facts {
		matchNames[spec.Name] = true
	}
	if !matchNames[simulator.MemberCountFactName] {
		t.Fatalf("default Match descriptor does not advertise %q: %#v", simulator.MemberCountFactName, rule.MatchFactProviderDescriptor.Facts)
	}
}
