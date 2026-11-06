package prefilter

import "fmt"

// Error identifies a JSON, compile-time or runtime prefilter failure.
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
		return fmt.Sprintf("prefilter %s [%s]: %v", e.Phase, e.Code, e.Err)
	}
	return fmt.Sprintf("prefilter %s at %s [%s]: %v", e.Phase, e.Path, e.Code, e.Err)
}

func (e *Error) Unwrap() error { return e.Err }

func compileError(path, code, format string, args ...interface{}) error {
	return &Error{Phase: "compile", Path: path, Code: code, Err: fmt.Errorf(format, args...)}
}

func jsonError(path, code, format string, args ...interface{}) error {
	return &Error{Phase: "json", Path: path, Code: code, Err: fmt.Errorf(format, args...)}
}

func evaluationError(path, code, format string, args ...interface{}) error {
	return &Error{Phase: "evaluate", Path: path, Code: code, Err: fmt.Errorf(format, args...)}
}
