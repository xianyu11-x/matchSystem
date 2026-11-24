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
	"path/filepath"
	"syscall"
	"time"

	"github.com/xianyu11-x/matchSystem/internal/simulator"
	"github.com/xianyu11-x/matchSystem/internal/simulatorapi"
	"github.com/xianyu11-x/matchSystem/pkg/identity"
)

func main() {
	address := flag.String("addr", "127.0.0.1:8080", "HTTP listen address")
	logDir := flag.String("log-dir", "Logs", "recording directory")
	flag.Parse()

	logger := log.New(os.Stderr, "simulator-api: ", log.LstdFlags)
	if err := run(*address, *logDir, os.Stdout, logger); err != nil {
		logger.Print(err)
		os.Exit(1)
	}
}

func run(address, logDir string, readyOutput *os.File, logger *log.Logger) (runErr error) {
	file, err := createRecordingFile(logDir)
	if err != nil {
		return err
	}
	defer func() {
		if err := file.Close(); err != nil {
			closeErr := fmt.Errorf("关闭录制日志 %q 失败，文件可能不完整: %w", file.Name(), err)
			if runErr == nil {
				runErr = closeErr
			} else {
				logger.Print(closeErr)
			}
		}
	}()
	logger.Printf("录制日志: %s", file.Name())
	runtime, err := simulator.NewSimulatorWithReplayLogOutput(defaultScenario(), file, func(err error) {
		logger.Printf("录制日志 %q 不完整，已停止磁盘写入: %v", file.Name(), err)
	})
	if err != nil {
		return err
	}
	defer runtime.Close()
	handler := simulatorapi.NewHandler(simulatorapi.NewSimulatorAdapter(runtime))
	server := &http.Server{
		Addr:              address,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	defer listener.Close()

	// stdout is a sidecar control channel: emit exactly one machine-readable
	// readiness line and keep all human-readable logs on stderr.
	if err := writeReady(readyOutput, listener.Addr().String()); err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-ctx.Done()
		shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownContext); err != nil {
			logger.Printf("HTTP 服务未能完成优雅关闭: %v", err)
			_ = server.Close()
		}
	}()

	logger.Printf("listening on %s", listener.Addr())
	if err := server.Serve(listener); err != nil {
		if errors.Is(err, http.ErrServerClosed) {
			<-shutdownDone
			return nil
		}
		return err
	}
	return nil
}

func createRecordingFile(dir string) (*os.File, error) {
	if dir == "" {
		return nil, errors.New("录制日志目录不能为空")
	}
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("解析录制日志目录 %q 失败: %w", dir, err)
	}
	if err := os.MkdirAll(absolute, 0755); err != nil {
		return nil, fmt.Errorf("创建录制日志目录 %q 失败: %w", absolute, err)
	}
	name := "matchscope-" + time.Now().Format("20060102-150405") + "-*.log"
	file, err := os.CreateTemp(absolute, name)
	if err != nil {
		return nil, fmt.Errorf("在 %q 创建录制日志失败: %w", absolute, err)
	}
	return file, nil
}

func defaultScenario() simulator.Scenario {
	key := identity.LogicalNodeKey{
		Rule:        identity.RuleKey{Namespace: "1", RuleID: 1},
		PlacementID: 1,
	}
	ruleJSON := []byte(`{"schemaVersion":"match-rule/v1","ruleKey":{"namespace":"1","ruleId":"1"},"contract":{"schemaVersion":"logical-node-contract/v3","attributes":[{"name":"region","type":"strings","maxValues":1},{"name":"modes","type":"strings","maxValues":4},{"name":"playerLevel","type":"int64"}],"facts":[{"name":"preferredRoles","type":"strings","scope":"object","maxValues":3},{"name":"latencyMs","type":"int64","scope":"object"},{"name":"memberCount","type":"int64","scope":"match","description":"本次 Match 中的 Ticket 成员数量（包含 seed）"}],"indexes":[]},"prefilter":{"schemaVersion":"prefilter/v3","bitmap":{"resultType":"bitmap","expr":{"op":"none"}}},"evaluation":{"schemaVersion":"evaluation/v3","canJoin":{"schemaVersion":"expression-scalar/v3","resultType":"bool","expr":{"op":"bool_literal","value":true}},"canComplete":{"schemaVersion":"expression-scalar/v3","resultType":"bool","expr":{"op":"bool_literal","value":true}}},"scoring":{"type":"created_at","params":{"direction":"descending"}},"seedSelection":{"type":"arrival","params":{}},"runtime":{"candidateScoringLimitPerSeed":500,"candidateLimitPerSeed":50,"maxPlayers":8,"attemptLimitPerProduceMatch":500,"attemptLimitPerMatchRound":500}}`)
	return simulator.Scenario{
		SchemaVersion: simulator.ScenarioSchemaVersion,
		PhysicalNodes: []simulator.PhysicalNodeSpec{simulator.NewPhysicalNodeSpec(1, "inproc://simulator-1")},
		Rules: []simulator.RuleSpec{func() simulator.RuleSpec {
			rule := simulator.NewRuleSpec(key, 1, ruleJSON)
			return rule
		}()},
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
