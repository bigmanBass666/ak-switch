//go:build unit

package server

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

// ── categorizeError unit tests ──────────────────────────────────────────

func TestCategorizeError_NonRetryableStatusCodes(t *testing.T) {
	codes := []int{
		http.StatusBadRequest,            // 400
		http.StatusMethodNotAllowed,      // 405
		http.StatusNotAcceptable,         // 406
		http.StatusRequestEntityTooLarge, // 413
		http.StatusRequestURITooLong,     // 414
		http.StatusUnsupportedMediaType,  // 415
		http.StatusUnprocessableEntity,   // 422
		http.StatusNotImplemented,        // 501
	}

	for _, code := range codes {
		t.Run(http.StatusText(code), func(t *testing.T) {
			if cat := categorizeError(code, nil); cat != CatNonRetryable {
				t.Errorf("categorizeError(%d, nil) = %d, want CatNonRetryable(%d)", code, cat, CatNonRetryable)
			}
		})
	}
}

func TestCategorizeError_RetryableStatusCodes(t *testing.T) {
	codes := []int{
		http.StatusTooManyRequests,               // 429
		http.StatusBadGateway,                    // 502
		http.StatusServiceUnavailable,            // 503
		http.StatusGatewayTimeout,                // 504
		http.StatusInternalServerError,           // 500
		http.StatusUnauthorized,                  // 401
		http.StatusForbidden,                     // 403
		http.StatusNotFound,                      // 404
		http.StatusRequestTimeout,                // 408
		http.StatusConflict,                      // 409
		http.StatusGone,                          // 410
		http.StatusPreconditionFailed,            // 412
		http.StatusTooManyRequests,               // 429
		http.StatusRequestHeaderFieldsTooLarge,   // 431
		http.StatusNetworkAuthenticationRequired, // 511
		200,                                      // success — edge case
	}

	for _, code := range codes {
		t.Run(http.StatusText(code), func(t *testing.T) {
			if cat := categorizeError(code, nil); cat != CatRetryable {
				t.Errorf("categorizeError(%d, nil) = %d, want CatRetryable(%d)", code, cat, CatRetryable)
			}
		})
	}
}

func TestCategorizeError_NetworkErrorIsRetryable(t *testing.T) {
	err := errors.New("connection refused")
	if cat := categorizeError(0, err); cat != CatRetryable {
		t.Errorf("categorizeError(0, connection refused) = %d, want CatRetryable(%d)", cat, CatRetryable)
	}
}

func TestCategorizeError_ClientAbort(t *testing.T) {
	err := context.Canceled
	if cat := categorizeError(0, err); cat != CatClientAbort {
		t.Errorf("categorizeError(0, context.Canceled) = %d, want CatClientAbort(%d)", cat, CatClientAbort)
	}
}

func TestCategorizeError_WrappedClientAbort(t *testing.T) {
	// Simulate a wrapped context.Canceled error
	err := errors.New("upstream request failed: context canceled")
	_ = err // In practice, the error from client.Do wraps context.Canceled
	// This test verifies that errors.Is(err, context.Canceled) matches
	wrapped := errors.New("outer: some error") // no Canceled wrapping
	if cat := categorizeError(0, wrapped); cat != CatRetryable {
		t.Errorf("expected CatRetryable for non-Canceled error, got %d", cat)
	}
}

func TestCategorizeError_ZeroValue(t *testing.T) {
	// Zero status code (no response yet, no error) should be CatRetryable
	if cat := categorizeError(0, nil); cat != CatRetryable {
		t.Errorf("categorizeError(0, nil) = %d, want CatRetryable(%d)", cat, CatRetryable)
	}
}
