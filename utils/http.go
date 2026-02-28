package utils

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
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

func (e *APIError) Error() string { return e.Message }

// NewSocketHTTPClient creates an HTTP client that dials a Unix socket.
func NewSocketHTTPClient(socketPath string) *http.Client {
	return &http.Client{
		Timeout: HTTPTimeout,
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
	rb, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != expectedStatus {
		return nil, &APIError{
			Code:    resp.StatusCode,
			Message: fmt.Sprintf("%s %s → %d: %s", method, url, resp.StatusCode, rb),
		}
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
			select {
			case <-ctx.Done():
				return zero, ctx.Err()
			case <-time.After(backoff):
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
