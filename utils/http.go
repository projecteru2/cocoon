package utils

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"time"
)

const (
	HTTPTimeout = 30 * time.Second
	MaxRetries  = 3
	BaseBackoff = 100 * time.Millisecond
)

// APIError carries the HTTP status code from a REST API response.
type APIError struct {
	Code    int
	Message string
}

// Error returns the API error message.
func (e *APIError) Error() string { return e.Message }

// NewSocketHTTPClient creates an HTTP client that dials a Unix socket.
func NewSocketHTTPClient(socketPath string) *http.Client {
	return NewSocketHTTPClientWithTimeout(socketPath, HTTPTimeout)
}

// NewSocketHTTPClientWithTimeout is like NewSocketHTTPClient but with a custom
// per-call Timeout. Use this for endpoints where the default HTTPTimeout is too
// tight (e.g. hypervisor snapshot/restore which transfer the entire guest memory
// synchronously and can take minutes on multi-GiB VMs or slow storage).
func NewSocketHTTPClientWithTimeout(socketPath string, timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", socketPath)
			},
		},
	}
}

// DoAPI sends an HTTP request and validates the response status code.
// url must be a fully-formed URL (e.g., "http://localhost/api/v1/vm.shutdown").
// Returns the response body on success. For 204 No Content the body is nil.
func DoAPI(ctx context.Context, hc *http.Client, method, url string, body []byte, expectedStatus int) ([]byte, error) {
	var reqBody io.Reader
	if body != nil {
		reqBody = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		return nil, fmt.Errorf("build request %s %s: %w", method, url, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", method, url, err)
	}
	defer resp.Body.Close() //nolint:errcheck
	rb, readErr := io.ReadAll(resp.Body)
	if resp.StatusCode != expectedStatus {
		msg := fmt.Sprintf("%s %s → %d: %s", method, url, resp.StatusCode, rb)
		if readErr != nil {
			msg += fmt.Sprintf(" (body read error: %v)", readErr)
		}
		return nil, &APIError{Code: resp.StatusCode, Message: msg}
	}
	if readErr != nil {
		return nil, fmt.Errorf("read response body %s %s: %w", method, url, readErr)
	}
	return rb, nil
}

// CheckSocket verifies that a Unix socket is connectable.
func CheckSocket(socketPath string) error {
	conn, err := net.DialTimeout("unix", socketPath, 2*time.Second)
	if err != nil {
		return err
	}
	return conn.Close()
}

// DoWithRetry retries fn with exponential backoff for transient errors.
func DoWithRetry[T any](ctx context.Context, fn func() (T, error)) (T, error) {
	var zero T
	var lastErr error
	for i := 0; i <= MaxRetries; i++ {
		result, err := fn()
		if err == nil {
			return result, nil
		}
		lastErr = err
		if !IsRetryable(err) {
			return zero, err
		}
		if i < MaxRetries {
			backoff := BaseBackoff * time.Duration(1<<i)
			timer := time.NewTimer(backoff)
			select {
			case <-ctx.Done():
				timer.Stop()
				return zero, ctx.Err()
			case <-timer.C:
			}
		}
	}
	return zero, lastErr
}

// IsRetryable returns true for transient errors (connection failures, 5xx, 429).
func IsRetryable(err error) bool {
	var ae *APIError
	if errors.As(err, &ae) {
		return ae.Code >= 500 || ae.Code == http.StatusTooManyRequests
	}
	// Non-APIError = connection-level failure, always retry.
	return true
}

// DoAPIWithRetry wraps DoAPI in DoWithRetry and tolerates extra success codes
// (e.g. some endpoints return 200 OK while their idiomatic success is 204).
// successCodes[0] is the primary code passed to DoAPI; codes[1:] are accepted
// as success on retry (silent nil-body return).
func DoAPIWithRetry(ctx context.Context, hc *http.Client, method, url string, body []byte, successCodes ...int) ([]byte, error) {
	return DoWithRetry(ctx, func() ([]byte, error) {
		return doAPI(ctx, hc, method, url, body, successCodes...)
	})
}

// DoAPIOnce sends a single request without DoWithRetry. Use for endpoints
// whose action is non-idempotent: a retry after the request landed but the
// response was lost would surface as a duplicate / conflict error rather
// than a real failure (e.g. CH vm.add-fs / vm.add-device, snapshot/create).
func DoAPIOnce(ctx context.Context, hc *http.Client, method, url string, body []byte, successCodes ...int) ([]byte, error) {
	return doAPI(ctx, hc, method, url, body, successCodes...)
}

// doAPI is DoAPI with two conveniences: it defaults to 204 No Content when
// successCodes is empty, and accepts successCodes[1:] as success (returning
// a nil body, mirroring the retry path's "tolerated alt" contract).
func doAPI(ctx context.Context, hc *http.Client, method, url string, body []byte, successCodes ...int) ([]byte, error) {
	primary := http.StatusNoContent
	if len(successCodes) > 0 {
		primary = successCodes[0]
	}
	resp, apiErr := DoAPI(ctx, hc, method, url, body, primary)
	if apiErr == nil {
		return resp, nil
	}
	var ae *APIError
	if errors.As(apiErr, &ae) && len(successCodes) > 1 && slices.Contains(successCodes[1:], ae.Code) {
		return nil, nil
	}
	return nil, apiErr
}
