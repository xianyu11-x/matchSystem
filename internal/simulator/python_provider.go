package simulator

// This file contains the production host adapter for scenario-configured
// Python providers. It deliberately lives in the simulator package so a
// scenario can own the worker lifecycle without making the match core aware
// of processes or a scripting language.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	stdRuntime "runtime"
	"sort"
	"strings"
	"sync"
	"time"

	ms "matchSystem/internal/matchsystem"
	"matchSystem/internal/matchsystem/fact"
)

const (
	pythonProviderFrameLimit      = 1 << 20
	defaultPythonProviderTimeout  = 2 * time.Second
	pythonProviderDiagnosticLimit = 16 << 10
)

// ErrPythonProvider identifies a process-backed provider failure. Callers can
// use errors.As to inspect the method and bounded stderr diagnostic while the
// wrapped cause keeps context cancellation and deadline checks working.
var ErrPythonProvider = errors.New("python provider failed")

// PythonProviderError is returned after a provider process fails, times out,
// violates the JSON protocol, or returns Facts outside the Rule Contract.
// The worker is closed before the error is returned; a subsequent successful
// call requires a new runtime/scenario publication.
type PythonProviderError struct {
	Method     string
	Diagnostic string
	Err        error
}

// PythonProviderDescriptors is the metadata returned by a script's required
// describe() function during the startup handshake. Each descriptor belongs
// to one provider boundary and contains only the Facts produced at that
// boundary. The values are detached from the worker and safe for callers to
// retain.
type PythonProviderDescriptors struct {
	Tick   *ms.ProviderDescriptor
	Object *ms.ProviderDescriptor
	Match  *ms.ProviderDescriptor
}

func (d PythonProviderDescriptors) any() bool {
	return d.Tick != nil || d.Object != nil || d.Match != nil
}

func (d PythonProviderDescriptors) clone() PythonProviderDescriptors {
	return PythonProviderDescriptors{
		Tick:   cloneProviderDescriptor(d.Tick),
		Object: cloneProviderDescriptor(d.Object),
		Match:  cloneProviderDescriptor(d.Match),
	}
}

// HandshakeDescriptors returns the descriptors captured during the one
// startup handshake. The returned values are detached from the worker.
func (w *PythonWorker) HandshakeDescriptors() PythonProviderDescriptors {
	if w == nil {
		return PythonProviderDescriptors{}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.descriptors.clone()
}

func (e *PythonProviderError) Error() string {
	if e == nil {
		return ""
	}
	message := "python provider failed"
	if e.Method != "" {
		message += " during " + e.Method
	}
	if e.Err != nil {
		message += ": " + e.Err.Error()
	}
	if e.Diagnostic != "" {
		message += " (stderr: " + e.Diagnostic + ")"
	}
	return message
}

func (e *PythonProviderError) Unwrap() error {
	if e == nil {
		return ErrPythonProvider
	}
	if e.Err == nil {
		return ErrPythonProvider
	}
	return errors.Join(ErrPythonProvider, e.Err)
}

// PythonWorker is one owner-serialized JSON-lines process. It implements the
// three matchsystem provider ports and is intentionally independent for every
// LogicalNode, including placements that use the same script path.
type PythonWorker struct {
	mu          sync.Mutex
	cmd         *exec.Cmd
	stdin       io.WriteCloser
	stdout      io.ReadCloser
	reader      *bufio.Scanner
	validator   *fact.Validator
	timeout     time.Duration
	diagnostic  *pythonDiagnosticBuffer
	closed      bool
	failure     error
	descriptors PythonProviderDescriptors
}

// StartPythonProvider starts and handshakes one Python worker. A successful
// return means that the script imported, exposed callable provide and
// describe methods, and returned the implementation-owned capability set in
// the startup handshake. At least one provider scope must be described, even
// when a scope's Fact list is empty.
func StartPythonProvider(config PythonProviderConfig, specs []fact.Spec) (*PythonWorker, error) {
	if strings.TrimSpace(config.Script) == "" {
		return nil, fmt.Errorf("pythonProvider.script is required")
	}
	if config.TimeoutMS < 0 {
		return nil, fmt.Errorf("pythonProvider.timeoutMs must not be negative")
	}
	timeout := defaultPythonProviderTimeout
	if config.TimeoutMS > 0 {
		timeout = time.Duration(config.TimeoutMS) * time.Millisecond
	}
	if timeout <= 0 {
		return nil, fmt.Errorf("pythonProvider.timeoutMs must be positive")
	}
	validator, err := fact.NewValidator(specs)
	if err != nil {
		return nil, err
	}
	script, err := filepath.Abs(config.Script)
	if err != nil {
		return nil, fmt.Errorf("resolve python script: %w", err)
	}
	if info, statErr := os.Stat(script); statErr != nil {
		return nil, fmt.Errorf("stat python script %q: %w", script, statErr)
	} else if info.IsDir() {
		return nil, fmt.Errorf("python script %q is a directory", script)
	}

	python, pythonArgs, err := discoverPython(config.Python)
	if err != nil {
		return nil, err
	}
	args := append([]string(nil), pythonArgs...)
	if strings.TrimSpace(config.Runner) != "" {
		runner, resolveErr := filepath.Abs(config.Runner)
		if resolveErr != nil {
			return nil, fmt.Errorf("resolve python runner: %w", resolveErr)
		}
		args = append(args, "-u", "-B", runner, script)
	} else {
		args = append(args, "-u", "-B", "-c", pythonProviderRunnerSource, script)
	}
	cmd := exec.Command(python, args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("open python stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("open python stdout: %w", err)
	}
	diagnostic := &pythonDiagnosticBuffer{limit: pythonProviderDiagnosticLimit}
	cmd.Stderr = diagnostic
	cmd.WaitDelay = 100 * time.Millisecond
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, fmt.Errorf("start python provider: %w", err)
	}
	reader := bufio.NewScanner(stdout)
	reader.Buffer(make([]byte, 4096), pythonProviderFrameLimit)
	worker := &PythonWorker{
		cmd: cmd, stdin: stdin, stdout: stdout, reader: reader,
		validator: validator, timeout: timeout, diagnostic: diagnostic,
	}
	// Process startup and script import are host operations. A very small
	// per-RPC timeout must not make a healthy interpreter fail before the
	// configured runtime budget can even be used. Restore the configured
	// budget before publishing the worker.
	if worker.timeout < defaultPythonProviderTimeout {
		worker.timeout = defaultPythonProviderTimeout
	}
	if _, err := worker.call(context.Background(), "handshake", nil, fact.Scope(""), false); err != nil {
		worker.Close()
		return nil, fmt.Errorf("python provider handshake: %w", err)
	}
	worker.timeout = timeout
	return worker, nil
}

// NewPythonProvider is an alias for hosts that use constructor naming.
func NewPythonProvider(config PythonProviderConfig, specs []fact.Spec) (*PythonWorker, error) {
	return StartPythonProvider(config, specs)
}

func discoverPython(configured string) (string, []string, error) {
	if value := strings.TrimSpace(configured); value != "" {
		if _, err := exec.LookPath(value); err != nil && !filepath.IsAbs(value) {
			return "", nil, fmt.Errorf("python executable %q is unavailable: %w", value, err)
		}
		return value, nil, nil
	}
	if value := strings.TrimSpace(os.Getenv("PYTHON_PROVIDER_EXECUTABLE")); value != "" {
		if _, err := exec.LookPath(value); err != nil && !filepath.IsAbs(value) {
			return "", nil, fmt.Errorf("PYTHON_PROVIDER_EXECUTABLE %q is unavailable: %w", value, err)
		}
		return value, nil, nil
	}
	// On Windows, the `python` command may resolve to the Microsoft Store
	// execution alias even when no interpreter is installed at that path.
	// Prefer the Python launcher, which resolves an installed Python 3, before
	// probing that alias. An explicit config or environment variable still wins.
	if stdRuntime.GOOS == "windows" {
		if _, err := exec.LookPath("py"); err == nil {
			return "py", []string{"-3"}, nil
		}
	}
	for _, candidate := range []string{"python", "python3"} {
		if _, err := exec.LookPath(candidate); err == nil {
			return candidate, nil, nil
		}
	}
	if _, err := exec.LookPath("py"); err == nil {
		return "py", []string{"-3"}, nil
	}
	return "", nil, errors.New("python executable was not found; set pythonProvider.python or PYTHON_PROVIDER_EXECUTABLE")
}

// Close terminates and reaps this worker. It is safe to call repeatedly and
// also waits for an in-flight owner call before returning.
func (w *PythonWorker) Close() {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closeLocked()
}

func (w *PythonWorker) closeLocked() {
	if w.closed {
		return
	}
	w.closed = true
	if w.stdin != nil {
		_ = w.stdin.Close()
	}
	if w.cmd != nil && w.cmd.Process != nil {
		_ = w.cmd.Process.Kill()
	}
	if w.stdout != nil {
		_ = w.stdout.Close()
	}
	if w.cmd != nil {
		_ = w.cmd.Wait()
	}
}

// Closed reports whether this worker has entered terminal failure or has
// been explicitly released.
func (w *PythonWorker) Closed() bool {
	if w == nil {
		return true
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.closed
}

// Failure returns the first terminal provider failure, if any. An explicit
// Close does not create a failure. Hosts use this after a core operation as a
// second channel for errors that the core may have treated as one candidate's
// failed evaluation before successfully producing another candidate's match.
func (w *PythonWorker) Failure() error {
	if w == nil {
		return &PythonProviderError{Method: "", Err: errors.New("worker is nil")}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.failure
}

// Diagnostic returns the bounded stderr tail captured from the process.
func (w *PythonWorker) Diagnostic() string {
	if w == nil || w.diagnostic == nil {
		return ""
	}
	return w.diagnostic.String()
}

type pythonRPCResult struct {
	data []byte
	err  error
}

// The descriptor wire types intentionally use lower-case schema names. The
// existing provider value protocol predates JSON tags and keeps its
// PascalCase Facts maps; descriptor metadata is a portable scenario/API
// document and therefore follows the same lower-case shape as
// simulatorapi.FactProviderDescriptor.
type pythonProviderDescriptorWire struct {
	ID      string                       `json:"id"`
	Version string                       `json:"version"`
	Facts   []pythonProviderFactSpecWire `json:"facts,omitempty"`
}

type pythonProviderFactSpecWire struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Scope       string `json:"scope"`
	MaxValues   int    `json:"maxValues,omitempty"`
	Description string `json:"description,omitempty"`
}

type pythonProviderDescriptorsWire struct {
	Tick   json.RawMessage `json:"tick"`
	Object json.RawMessage `json:"object"`
	Match  json.RawMessage `json:"match"`
}

func parsePythonProviderDescriptor(value *pythonProviderDescriptorWire, scope fact.Scope) (*ms.ProviderDescriptor, error) {
	if value == nil {
		return nil, nil
	}
	if strings.TrimSpace(value.ID) == "" {
		return nil, fmt.Errorf("%s descriptor id is required", scope)
	}
	if strings.TrimSpace(value.Version) == "" {
		return nil, fmt.Errorf("%s descriptor version is required", scope)
	}
	descriptor := &ms.ProviderDescriptor{
		ID: value.ID, Version: value.Version,
		Facts: make([]fact.Spec, len(value.Facts)),
	}
	for index, item := range value.Facts {
		typeValue, err := factTypeFromJSON(item.Type)
		if err != nil {
			return nil, fmt.Errorf("%s descriptor facts[%d].type: %w", scope, index, err)
		}
		descriptor.Facts[index] = fact.Spec{
			Name: item.Name, Type: typeValue, Scope: fact.Scope(item.Scope),
			MaxValues: item.MaxValues, Description: item.Description,
		}
	}
	if _, err := fact.NewValidator(descriptor.Facts); err != nil {
		return nil, fmt.Errorf("%s descriptor: %w", scope, err)
	}
	return descriptor, nil
}

func parsePythonProviderDescriptors(value *pythonProviderDescriptorsWire) (PythonProviderDescriptors, error) {
	if value == nil {
		return PythonProviderDescriptors{}, nil
	}
	parseScope := func(raw json.RawMessage, scope fact.Scope) (*ms.ProviderDescriptor, error) {
		if len(raw) == 0 {
			return nil, nil
		}
		if strings.TrimSpace(string(raw)) == "null" {
			return nil, fmt.Errorf("%s descriptor must be an object", scope)
		}
		var descriptor pythonProviderDescriptorWire
		if err := decodeStrictJSON(raw, &descriptor); err != nil {
			return nil, fmt.Errorf("decode %s descriptor: %w", scope, err)
		}
		return parsePythonProviderDescriptor(&descriptor, scope)
	}
	tick, err := parseScope(value.Tick, fact.ScopeTick)
	if err != nil {
		return PythonProviderDescriptors{}, err
	}
	object, err := parseScope(value.Object, fact.ScopeObject)
	if err != nil {
		return PythonProviderDescriptors{}, err
	}
	match, err := parseScope(value.Match, fact.ScopeMatch)
	if err != nil {
		return PythonProviderDescriptors{}, err
	}
	return PythonProviderDescriptors{Tick: tick, Object: object, Match: match}, nil
}

func parsePythonProviderDescriptorsRaw(raw json.RawMessage) (PythonProviderDescriptors, bool, error) {
	if len(raw) == 0 {
		return PythonProviderDescriptors{}, false, nil
	}
	if strings.TrimSpace(string(raw)) == "null" {
		return PythonProviderDescriptors{}, true, errors.New("handshake Descriptors must be an object")
	}
	var wire pythonProviderDescriptorsWire
	if err := decodeStrictJSON(raw, &wire); err != nil {
		return PythonProviderDescriptors{}, true, fmt.Errorf("decode handshake Descriptors: %w", err)
	}
	descriptors, err := parsePythonProviderDescriptors(&wire)
	if err == nil && !descriptors.any() {
		err = errors.New("handshake Descriptors must declare at least one scope")
	}
	return descriptors, true, err
}

func factTypeFromJSON(value string) (fact.Type, error) {
	switch value {
	case "strings":
		return fact.TypeStrings, nil
	case "int64":
		return fact.TypeInt64, nil
	case "uint64s":
		return fact.TypeUint64s, nil
	default:
		return 0, fmt.Errorf("unsupported Fact type %q", value)
	}
}

// call executes one serialized request. scope is zero for the startup
// handshake, where no Facts payload is expected.
func (w *PythonWorker) call(ctx context.Context, method string, input any, scope fact.Scope, validate bool) (ms.Facts, error) {
	if w == nil {
		return ms.Facts{}, &PythonProviderError{Method: method, Err: errors.New("worker is nil")}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		if w.failure != nil {
			return ms.Facts{}, w.failure
		}
		return ms.Facts{}, &PythonProviderError{Method: method, Err: errors.New("worker is closed")}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	callCtx, cancel := context.WithTimeout(ctx, w.timeout)
	defer cancel()
	if err := callCtx.Err(); err != nil {
		return ms.Facts{}, err
	}
	request, err := json.Marshal(struct {
		Version int
		Method  string
		Input   any
	}{1, method, input})
	if err != nil {
		return ms.Facts{}, w.failLocked(method, err)
	}
	if len(request)+1 >= pythonProviderFrameLimit {
		return ms.Facts{}, w.failLocked(method, errors.New("request exceeds 1 MiB"))
	}
	done := make(chan pythonRPCResult, 1)
	go func() {
		if _, writeErr := w.stdin.Write(append(request, '\n')); writeErr != nil {
			done <- pythonRPCResult{err: writeErr}
			return
		}
		if !w.reader.Scan() {
			scanErr := w.reader.Err()
			if scanErr == nil {
				scanErr = io.EOF
			}
			done <- pythonRPCResult{err: scanErr}
			return
		}
		done <- pythonRPCResult{data: append([]byte(nil), w.reader.Bytes()...)}
	}()
	var response pythonRPCResult
	select {
	case response = <-done:
	case <-callCtx.Done():
		if ctx.Err() != nil {
			w.closeLocked()
			<-done
			return ms.Facts{}, ctx.Err()
		}
		failure := w.wrapError(method, callCtx.Err())
		if w.failure == nil {
			w.failure = failure
		}
		w.closeLocked()
		<-done
		return ms.Facts{}, failure
	}
	if response.err != nil {
		return ms.Facts{}, w.failLocked(method, response.err)
	}
	var wire struct {
		Version     int             `json:"Version"`
		Ready       bool            `json:"Ready"`
		Facts       ms.Facts        `json:"Facts"`
		Descriptors json.RawMessage `json:"Descriptors"`
		Error       string          `json:"Error"`
	}
	decoder := json.NewDecoder(bytes.NewReader(response.data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return ms.Facts{}, w.failLocked(method, fmt.Errorf("decode response: %w", err))
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			err = errors.New("trailing response data")
		}
		return ms.Facts{}, w.failLocked(method, err)
	}
	if wire.Version != 1 {
		return ms.Facts{}, w.failLocked(method, errors.New("unsupported protocol version"))
	}
	if wire.Error != "" {
		return ms.Facts{}, w.failLocked(method, fmt.Errorf("python: %s", wire.Error))
	}
	if method == "handshake" {
		if !wire.Ready {
			return ms.Facts{}, w.failLocked(method, errors.New("python provider did not become ready"))
		}
		descriptors, descriptorsPresent, descriptorErr := parsePythonProviderDescriptorsRaw(wire.Descriptors)
		if descriptorErr != nil {
			return ms.Facts{}, w.failLocked(method, descriptorErr)
		}
		if !descriptorsPresent {
			return ms.Facts{}, w.failLocked(method, errors.New("python provider handshake must include Descriptors"))
		}
		w.descriptors = descriptors
		return ms.Facts{}, nil
	}
	if len(wire.Descriptors) > 0 {
		return ms.Facts{}, w.failLocked(method, errors.New("Descriptors are only valid in handshake responses"))
	}
	if validate {
		if scope == fact.ScopeMatch {
			err = w.validator.ValidateCompleteMatch("python", wire.Facts)
		} else {
			_, err = w.validator.ValidateLayer("python", wire.Facts, scope)
		}
		if err != nil {
			return ms.Facts{}, w.failLocked(method, err)
		}
	}
	return wire.Facts, nil
}

func (w *PythonWorker) wrapError(method string, err error) error {
	if err == nil {
		return nil
	}
	return &PythonProviderError{Method: method, Diagnostic: w.Diagnostic(), Err: err}
}

func (w *PythonWorker) failLocked(method string, err error) error {
	failure := w.wrapError(method, err)
	if w.failure == nil {
		w.failure = failure
	}
	w.closeLocked()
	return failure
}

func (w *PythonWorker) fail(method string, err error) error {
	if w == nil {
		return &PythonProviderError{Method: method, Err: err}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.failLocked(method, err)
}

func (w *PythonWorker) Tick(ctx context.Context, input ms.TickFactInput) (ms.Facts, error) {
	return w.call(ctx, "tick", input, fact.ScopeTick, true)
}

func (w *PythonWorker) Initialize(ctx context.Context, input ms.InitializeInput) (ms.Facts, error) {
	return w.call(ctx, "initialize", input, fact.ScopeMatch, true)
}

func (w *PythonWorker) OnJoin(ctx context.Context, input ms.JoinInput) (ms.Facts, error) {
	return w.call(ctx, "join", input, fact.ScopeMatch, true)
}

// Object adapts the process response to the schema-bound writer.
func (w *PythonWorker) Object(ticket *ms.Ticket, now int64, tick ms.Facts, out ms.ObjectFactWriter) error {
	values, err := w.call(context.Background(), "object", struct {
		Ticket    *ms.Ticket
		Now       int64
		TickFacts ms.Facts
	}{ticket, now, tick}, fact.ScopeObject, true)
	if err != nil {
		return err
	}
	for _, name := range sortedFactNames(values.StringLists) {
		if err := out.SetStrings(name, values.StringLists[name]); err != nil {
			return w.fail("object", err)
		}
	}
	for _, name := range sortedFactNames(values.Uint64Lists) {
		if err := out.SetUint64s(name, values.Uint64Lists[name]); err != nil {
			return w.fail("object", err)
		}
	}
	for _, name := range sortedFactNames(values.Int64Values) {
		if err := out.SetInt64(name, values.Int64Values[name]); err != nil {
			return w.fail("object", err)
		}
	}
	return nil
}

func sortedFactNames[V any](values map[string]V) []string {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

type pythonDiagnosticBuffer struct {
	mu    sync.Mutex
	data  []byte
	limit int
}

func (b *pythonDiagnosticBuffer) Write(data []byte) (int, error) {
	if b == nil {
		return len(data), nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.data = append(b.data, data...)
	if len(b.data) > b.limit {
		b.data = append([]byte(nil), b.data[len(b.data)-b.limit:]...)
	}
	return len(data), nil
}

func (b *pythonDiagnosticBuffer) String() string {
	if b == nil {
		return ""
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return strings.TrimSpace(string(append([]byte(nil), b.data...)))
}

// pythonProviderRunnerSource is intentionally tiny and has no third-party
// dependency. Script stdout is redirected to stderr so protocol framing stays
// valid. The handshake catches import/syntax errors while the runtime is
// being built, before the new scenario is published.
const pythonProviderRunnerSource = `import contextlib
import json
import pathlib
import sys

sys.stdin.reconfigure(encoding="utf-8")
sys.stdout.reconfigure(encoding="utf-8")
namespace = {"__name__": "provider", "__file__": sys.argv[1]}
try:
    with contextlib.redirect_stdout(sys.stderr):
        exec(compile(pathlib.Path(sys.argv[1]).read_bytes(), sys.argv[1], "exec"), namespace)
    if not callable(namespace.get("provide")):
        raise ValueError("script must define callable provide(method, data)")
    if not callable(namespace.get("describe")):
        raise ValueError("script must define callable describe()")
except Exception as exc:
    print(json.dumps({"Version": 1, "Error": str(exc)[:4096]}, ensure_ascii=True, allow_nan=False), flush=True)
    raise SystemExit(1)
for line in sys.stdin:
    try:
        request = json.loads(line)
        if request["Version"] != 1:
            raise ValueError("unsupported protocol")
        if request["Method"] == "handshake":
            response = {"Version": 1, "Ready": True}
            describe = namespace["describe"]
            with contextlib.redirect_stdout(sys.stderr):
                descriptors = describe()
            response["Descriptors"] = descriptors
        else:
            with contextlib.redirect_stdout(sys.stderr):
                facts = namespace["provide"](request["Method"], request.get("Input"))
            response = {"Version": 1, "Facts": facts}
    except Exception as exc:
        response = {"Version": 1, "Error": str(exc)[:4096]}
    print(json.dumps(response, ensure_ascii=True, allow_nan=False), flush=True)
`

func (w *PythonWorker) handshakeDescriptor(scope fact.Scope) (ms.ProviderDescriptor, error) {
	if w == nil {
		return ms.ProviderDescriptor{}, errors.New("worker is nil")
	}
	descriptors := w.HandshakeDescriptors()
	var descriptor *ms.ProviderDescriptor
	switch scope {
	case fact.ScopeTick:
		descriptor = descriptors.Tick
	case fact.ScopeObject:
		descriptor = descriptors.Object
	case fact.ScopeMatch:
		descriptor = descriptors.Match
	default:
		return ms.ProviderDescriptor{}, fmt.Errorf("unsupported Provider scope %q", scope)
	}
	if descriptor == nil {
		return ms.ProviderDescriptor{}, fmt.Errorf("handshake Descriptors has no %s provider", scope)
	}
	return descriptor.Clone(), nil
}

type pythonTickProvider struct{ worker *PythonWorker }

func (p pythonTickProvider) Provide(ctx context.Context, input ms.TickFactInput) (ms.Facts, error) {
	return p.worker.Tick(ctx, input)
}

func (p pythonTickProvider) Describe() (ms.ProviderDescriptor, error) {
	return p.worker.handshakeDescriptor(fact.ScopeTick)
}

type pythonObjectProvider struct{ worker *PythonWorker }

func (p pythonObjectProvider) Provide(ticket *ms.Ticket, now int64, tick ms.Facts, out ms.ObjectFactWriter) error {
	return p.worker.Object(ticket, now, tick, out)
}

func (p pythonObjectProvider) Describe() (ms.ProviderDescriptor, error) {
	return p.worker.handshakeDescriptor(fact.ScopeObject)
}

type pythonMatchProvider struct{ worker *PythonWorker }

func (p pythonMatchProvider) Initialize(ctx context.Context, input ms.InitializeInput) (ms.Facts, error) {
	return p.worker.Initialize(ctx, input)
}

func (p pythonMatchProvider) OnJoin(ctx context.Context, input ms.JoinInput) (ms.Facts, error) {
	return p.worker.OnJoin(ctx, input)
}

func (p pythonMatchProvider) Describe() (ms.ProviderDescriptor, error) {
	return p.worker.handshakeDescriptor(fact.ScopeMatch)
}

func pythonProviderCallbacks(worker *PythonWorker) (any, any, any) {
	if worker == nil {
		return nil, nil, nil
	}
	descriptors := worker.HandshakeDescriptors()
	var tick, object, match any
	if descriptors.Tick != nil {
		tick = pythonTickProvider{worker: worker}
	}
	if descriptors.Object != nil {
		object = pythonObjectProvider{worker: worker}
	}
	if descriptors.Match != nil {
		match = pythonMatchProvider{worker: worker}
	}
	return tick, object, match
}
