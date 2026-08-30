// Command simulator-api exposes the simulator HTTP transport.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"matchSystem/internal/identity"
	"matchSystem/internal/simulator"
	"matchSystem/internal/simulatorapi"
)

func main() {
	address := flag.String("addr", "127.0.0.1:8080", "HTTP listen address")
	flag.Parse()

	runtime, err := simulator.NewService(defaultScenario())
	if err != nil {
		log.New(os.Stderr, "simulator-api: ", log.LstdFlags).Fatal(err)
	}
	defer runtime.Close()
	handler := simulatorapi.NewHandler(simulatorapi.NewSimulatorAdapter(runtime))
	server := &http.Server{
		Addr:              *address,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}
	listener, err := net.Listen("tcp", *address)
	if err != nil {
		log.New(os.Stderr, "simulator-api: ", log.LstdFlags).Fatal(err)
	}
	defer listener.Close()

	// stdout is a sidecar control channel: emit exactly one machine-readable
	// readiness line and keep all human-readable logs on stderr.
	if err := writeReady(os.Stdout, listener.Addr().String()); err != nil {
		log.New(os.Stderr, "simulator-api: ", log.LstdFlags).Fatal(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownContext)
	}()

	logger := log.New(os.Stderr, "simulator-api: ", log.LstdFlags)
	logger.Printf("listening on %s", listener.Addr())
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Fatal(err)
	}
}

func defaultScenario() simulator.Scenario {
	key := identity.LogicalNodeKey{
		Rule:        identity.RuleKey{Namespace: "demo", RuleID: 1},
		PlacementID: "default",
	}
	ruleJSON := []byte(`{"schemaVersion":"match-rule/v1","ruleKey":{"namespace":"demo","ruleId":1},"contract":{"schemaVersion":"logical-node-contract/v3","attributes":[{"name":"region","type":"strings","maxValues":1},{"name":"modes","type":"strings","maxValues":4},{"name":"playerLevel","type":"int64"}],"facts":[{"name":"preferredRoles","type":"strings","scope":"object","maxValues":3},{"name":"latencyMs","type":"int64","scope":"object"}],"indexes":[]},"prefilter":{"schemaVersion":"prefilter/v3","bitmap":{"resultType":"bitmap","expr":{"op":"none"}}},"evaluation":{"schemaVersion":"evaluation/v3","canJoin":{"schemaVersion":"expression-scalar/v3","resultType":"bool","expr":{"op":"bool_literal","value":true}},"canComplete":{"schemaVersion":"expression-scalar/v3","resultType":"bool","expr":{"op":"bool_literal","value":true}}},"scoring":{"type":"created_at","params":{"direction":"descending"}},"seedSelection":{"type":"arrival","params":{}},"runtime":{"candidateLimitPerSeed":128,"maxPlayers":8,"attemptLimitPerProduceMatch":500,"attemptLimitPerMatchRound":500}}`)
	return simulator.Scenario{
		SchemaVersion: simulator.ScenarioSchemaVersion,
		PhysicalNodes: []simulator.PhysicalNodeSpec{simulator.NewPhysicalNodeSpec("simulator-1", "inproc://simulator-1")},
		Rules:         []simulator.RuleSpec{simulator.NewRuleSpec(key, "simulator-1", ruleJSON)},
	}
}

func writeReady(output *os.File, address string) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("parse listener address %q: %w", address, err)
	}
	if host == "" || host == "0.0.0.0" || host == "::" || host == "[::]" {
		host = "127.0.0.1"
	}
	ready := struct {
		Type       string `json:"type"`
		APIBaseURL string `json:"apiBaseUrl"`
	}{
		Type:       "ready",
		APIBaseURL: "http://" + net.JoinHostPort(host, port),
	}
	return json.NewEncoder(output).Encode(ready)
}
