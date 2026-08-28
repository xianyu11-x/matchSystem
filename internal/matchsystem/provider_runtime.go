package matchsystem

import (
	"context"
	"errors"

	"matchSystem/internal/matchsystem/evaluation"
)

// invokeProvider adapts Fact provider errors/cancellation to the structured
// evaluation error boundary. Provider panics deliberately propagate to the
// owner process; providers are always called synchronously by that goroutine.
func invokeProvider(ctx context.Context, path string, invoke func() (Facts, error)) (Facts, error) {
	if canceled := ctx.Err(); canceled != nil {
		return Facts{}, providerCanceledError(path, canceled)
	}
	values, err := invoke()
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
			return Facts{}, providerCanceledError(path, err)
		}
		return Facts{}, providerError(path, err)
	}
	if canceled := ctx.Err(); canceled != nil {
		return Facts{}, providerCanceledError(path, canceled)
	}
	return values, nil
}

func providerError(path string, err error) error {
	return &evaluation.Error{Phase: "evaluate", Path: path, Code: "PROVIDER_ERROR", Err: err}
}

func providerCanceledError(path string, err error) error {
	return &evaluation.Error{Phase: "evaluate", Path: path, Code: "PROVIDER_CANCELED", Err: err}
}

func isContextTermination(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}
