package evaluation

import "fmt"

// Error is the structured error boundary for JSON parsing, plan compilation,
// and one-attempt evaluation.
type Error struct {
	Phase string
	Path  string
	Code  string
	Err   error
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Path == "" {
		return fmt.Sprintf("evaluation %s [%s]: %v", e.Phase, e.Code, e.Err)
	}
	return fmt.Sprintf("evaluation %s at %s [%s]: %v", e.Phase, e.Path, e.Code, e.Err)
}

func (e *Error) Unwrap() error { return e.Err }

func jsonError(path, code, format string, args ...any) error {
	return &Error{Phase: "json", Path: path, Code: code, Err: fmt.Errorf(format, args...)}
}

func compileError(path, code, format string, args ...any) error {
	return &Error{Phase: "compile", Path: path, Code: code, Err: fmt.Errorf(format, args...)}
}

func evaluateError(path, code, format string, args ...any) error {
	return &Error{Phase: "evaluate", Path: path, Code: code, Err: fmt.Errorf(format, args...)}
}
