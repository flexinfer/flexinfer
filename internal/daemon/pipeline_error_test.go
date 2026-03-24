package daemon

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestClassifyInternalError_Timeout(t *testing.T) {
	err := fmt.Errorf("rpc call failed: %w", context.DeadlineExceeded)
	code, retryable := classifyInternalError(err, stageExecute)
	if code != "TIMEOUT" {
		t.Errorf("code = %q, want TIMEOUT", code)
	}
	if !retryable {
		t.Error("timeout should be retryable")
	}
}

func TestClassifyInternalError_TransportCorruption(t *testing.T) {
	err := errors.New("response id mismatch: expected 5 got 3")
	code, retryable := classifyInternalError(err, stageExecute)
	if code != "TRANSPORT_CORRUPTION" {
		t.Errorf("code = %q, want TRANSPORT_CORRUPTION", code)
	}
	if !retryable {
		t.Error("transport corruption should be retryable")
	}
}

func TestClassifyInternalError_TransportCorruptionAlt(t *testing.T) {
	err := errors.New("transport corruption detected")
	code, _ := classifyInternalError(err, stageRoute)
	if code != "TRANSPORT_CORRUPTION" {
		t.Errorf("code = %q, want TRANSPORT_CORRUPTION", code)
	}
}

func TestClassifyInternalError_RouteServerUnavailable(t *testing.T) {
	err := errors.New("server unavailable: mcp-git")
	code, retryable := classifyInternalError(err, stageRoute)
	if code != "SERVER_UNAVAILABLE" {
		t.Errorf("code = %q, want SERVER_UNAVAILABLE", code)
	}
	if retryable {
		t.Error("server unavailable should not be retryable")
	}
}

func TestClassifyInternalError_RouteLock(t *testing.T) {
	err := errors.New("lock timeout acquiring server connection")
	code, retryable := classifyInternalError(err, stageRoute)
	if code != "LOCK_TIMEOUT" {
		t.Errorf("code = %q, want LOCK_TIMEOUT", code)
	}
	if !retryable {
		t.Error("lock timeout should be retryable")
	}
}

func TestClassifyInternalError_RouteFallback(t *testing.T) {
	err := errors.New("some unknown routing error")
	code, retryable := classifyInternalError(err, stageRoute)
	if code != "CONNECTION_ERROR" {
		t.Errorf("code = %q, want CONNECTION_ERROR", code)
	}
	if !retryable {
		t.Error("connection error should be retryable")
	}
}

func TestClassifyInternalError_Execute(t *testing.T) {
	err := errors.New("connection reset by peer")
	code, retryable := classifyInternalError(err, stageExecute)
	if code != "TRANSPORT_FAILURE" {
		t.Errorf("code = %q, want TRANSPORT_FAILURE", code)
	}
	if !retryable {
		t.Error("execute transport failure should be retryable")
	}
}

func TestClassifyInternalError_Build(t *testing.T) {
	err := errors.New("failed to marshal request")
	code, retryable := classifyInternalError(err, stageBuild)
	if code != "SERVER_ERROR" {
		t.Errorf("code = %q, want SERVER_ERROR", code)
	}
	if retryable {
		t.Error("build server error should not be retryable")
	}
}

func TestClassifyInternalError_UnknownStage(t *testing.T) {
	err := errors.New("something went wrong")
	code, retryable := classifyInternalError(err, "unknown")
	if code != "SERVER_ERROR" {
		t.Errorf("code = %q, want SERVER_ERROR", code)
	}
	if retryable {
		t.Error("unknown stage should not be retryable")
	}
}
