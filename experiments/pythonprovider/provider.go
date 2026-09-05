// Package pythonprovider is a research adapter, not a simulator product API.
// One Worker belongs to exactly one logical node and is called serially by its owner.
package pythonprovider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"time"

	ms "matchSystem/internal/matchsystem"
	"matchSystem/internal/matchsystem/fact"
)

const maxFrame = 1 << 20

type Worker struct {
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	stdout    io.ReadCloser
	reader    *bufio.Scanner
	timeout   time.Duration
	validator *fact.Validator
	closed    bool
}

// Start starts an independent interpreter. Caller must Close before discarding it.
// Python scripts are trusted local code; child process trees are not sandboxed.
func Start(python, runner, script string, timeout time.Duration, specs []fact.Spec) (*Worker, error) {
	if timeout <= 0 {
		return nil, errors.New("positive timeout required")
	}
	v, err := fact.NewValidator(specs)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(python, "-u", "-B", runner, script)
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		in.Close()
		return nil, err
	}
	// Discard stderr: diagnostic floods cannot grow host memory or block the pipe.
	cmd.Stderr = io.Discard
	cmd.WaitDelay = 100 * time.Millisecond
	if err = cmd.Start(); err != nil {
		in.Close()
		out.Close()
		return nil, err
	}
	r := bufio.NewScanner(out)
	r.Buffer(make([]byte, 4096), maxFrame)
	return &Worker{cmd: cmd, stdin: in, stdout: out, reader: r, timeout: timeout, validator: v}, nil
}

// Close kills and reaps the directly owned interpreter; safe to repeat, owner-only.
func (w *Worker) Close() {
	if w.closed {
		return
	}
	w.closed = true
	w.stdin.Close()
	_ = w.cmd.Process.Kill()
	w.stdout.Close()
	_ = w.cmd.Wait()
}

func (w *Worker) call(ctx context.Context, method string, input any, scope fact.Scope) (ms.Facts, error) {
	if w.closed {
		return ms.Facts{}, errors.New("worker closed; explicit replacement required")
	}
	ctx, cancel := context.WithTimeout(ctx, w.timeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return ms.Facts{}, err
	}
	request, err := json.Marshal(struct {
		Version int
		Method  string
		Input   any
	}{1, method, input})
	if err != nil {
		return ms.Facts{}, err
	}
	if len(request)+1 >= maxFrame {
		return ms.Facts{}, errors.New("request exceeds 1 MiB")
	}
	type result struct {
		data []byte
		err  error
	}
	done := make(chan result, 1)
	go func() {
		if _, err := w.stdin.Write(append(request, '\n')); err != nil {
			done <- result{err: err}
			return
		}
		if !w.reader.Scan() {
			err := w.reader.Err()
			if err == nil {
				err = io.EOF
			}
			done <- result{err: err}
			return
		}
		done <- result{data: append([]byte(nil), w.reader.Bytes()...)}
	}()
	var r result
	select {
	case r = <-done:
	case <-ctx.Done():
		w.Close()
		<-done
		return ms.Facts{}, ctx.Err()
	}
	if r.err != nil {
		w.Close()
		return ms.Facts{}, r.err
	}
	var response struct {
		Version int
		Facts   ms.Facts
		Error   string
	}
	d := json.NewDecoder(bytes.NewReader(r.data))
	d.DisallowUnknownFields()
	if err = d.Decode(&response); err == nil {
		var extra any
		if d.Decode(&extra) != io.EOF {
			err = errors.New("trailing response data")
		}
	}
	if err == nil && response.Version != 1 {
		err = errors.New("unsupported protocol version")
	}
	if err == nil && response.Error != "" {
		err = fmt.Errorf("python: %s", response.Error)
	}
	if err == nil {
		if scope == fact.ScopeMatch {
			err = w.validator.ValidateCompleteMatch("python", response.Facts)
		} else {
			_, err = w.validator.ValidateLayer("python", response.Facts, scope)
		}
	}
	// Fail closed; no automatic replay of calls with potentially stateful scripts.
	if err != nil {
		w.Close()
		return ms.Facts{}, err
	}
	return response.Facts, nil
}

func (w *Worker) Tick(ctx context.Context, input ms.TickFactInput) (ms.Facts, error) {
	return w.call(ctx, "tick", input, fact.ScopeTick)
}
func (w *Worker) Initialize(ctx context.Context, input ms.InitializeInput) (ms.Facts, error) {
	return w.call(ctx, "initialize", input, fact.ScopeMatch)
}
func (w *Worker) OnJoin(ctx context.Context, input ms.JoinInput) (ms.Facts, error) {
	return w.call(ctx, "join", input, fact.ScopeMatch)
}
func (w *Worker) Object(ticket *ms.Ticket, now int64, tick ms.Facts, out ms.ObjectFactWriter) error {
	values, err := w.call(context.Background(), "object", struct {
		Ticket    *ms.Ticket
		Now       int64
		TickFacts ms.Facts
	}{ticket, now, tick}, fact.ScopeObject)
	if err != nil {
		return err
	}
	for k, v := range values.StringLists {
		if err := out.SetStrings(k, v); err != nil {
			return err
		}
	}
	for k, v := range values.Uint64Lists {
		if err := out.SetUint64s(k, v); err != nil {
			return err
		}
	}
	for k, v := range values.Int64Values {
		if err := out.SetInt64(k, v); err != nil {
			return err
		}
	}
	return nil
}
