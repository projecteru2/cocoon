package hypervisor

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

// DoPUT sends a PUT request over a Unix socket. Expects 204 No Content.
func DoPUT(ctx context.Context, hc *http.Client, path string, body []byte) error {
	var reqBody io.Reader
	if body != nil {
		reqBody = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, "http://localhost"+path, reqBody)
	if err != nil {
		return fmt.Errorf("build request %s: %w", path, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("PUT %s: %w", path, err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusNoContent {
		rb, err := io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("PUT %s → %d (read body: %w)", path, resp.StatusCode, err)
		}
		return &APIError{
			Code:    resp.StatusCode,
			Message: fmt.Sprintf("PUT %s → %d: %s", path, resp.StatusCode, rb),
		}
	}
	return nil
}

// DoGET sends a GET request over a Unix socket, returns the response body.
func DoGET(ctx context.Context, hc *http.Client, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://localhost"+path, nil)
	if err != nil {
		return nil, fmt.Errorf("build request %s: %w", path, err)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", path, err)
	}
	defer resp.Body.Close() //nolint:errcheck
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("GET %s read body: %w", path, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &APIError{
			Code:    resp.StatusCode,
			Message: fmt.Sprintf("GET %s → %d: %s", path, resp.StatusCode, body),
		}
	}
	return body, nil
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
func DoWithRetry(ctx context.Context, fn func() error) error {
	var lastErr error
	for i := 0; i <= MaxRetries; i++ {
		lastErr = fn()
		if lastErr == nil {
			return nil
		}
		if !IsRetryable(lastErr) {
			return lastErr
		}
		if i < MaxRetries {
			backoff := BaseBackoff * time.Duration(1<<i)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
		}
	}
	return lastErr
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
