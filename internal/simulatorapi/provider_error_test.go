package simulatorapi

import (
	"context"
	"errors"
	"testing"

	"matchSystem/internal/simulator"
)

func TestAdaptRuntimeErrorKeepsPythonTimeoutObservable(t *testing.T) {
	runtimeErr := &simulator.PythonProviderError{
		Method: "tick",
		Err:    context.DeadlineExceeded,
	}
	adapted := adaptRuntimeError(runtimeErr)
	serviceErr, ok := adapted.(*ServiceError)
	if !ok {
		t.Fatalf("Python timeout was not adapted to a service error: %T %v", adapted, adapted)
	}
	if serviceErr.Status != 500 || serviceErr.Code != "PYTHON_PROVIDER_FAILED" {
		t.Fatalf("unexpected Python provider response: %#v", serviceErr)
	}
	if !errors.Is(serviceErr, context.DeadlineExceeded) {
		t.Fatalf("adapted error lost the provider timeout cause: %v", serviceErr)
	}
}
