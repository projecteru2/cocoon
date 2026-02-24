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
	// HTTPTimeout is the per-request timeout for hypervisor REST API calls.
	HTTPTimeout = 30 * time.Second
	// MaxRetries is the number of retry attempts for transient API errors.
	MaxRetries = 3
	// BaseBackoff is the initial backoff duration; doubled on each retry.
	BaseBackoff = 100 * time.Millisecond
)

// APIError carries the HTTP status code from a hypervisor REST API response.
type APIError struct {
	Code    int
	Message string
}

func (e *APIError) Error() string { return e.Message }

// NewSocketHTTPClient creates an HTTP client that dials a Unix domain socket.
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

// DoPUT sends a PUT request over a Unix socket and expects 204 No Content.
// Returns an *APIError for non-204 responses.
func DoPUT(ctx context.Context, socketPath, path string, body []byte) error {
	hc := NewSocketHTTPClient(socketPath)
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
		rb, _ := io.ReadAll(resp.Body)
		return &APIError{
			Code:    resp.StatusCode,
			Message: fmt.Sprintf("PUT %s → %d: %s", path, resp.StatusCode, rb),
		}
	}
	return nil
}

// CheckSocket verifies that a Unix domain socket is connectable.
func CheckSocket(socketPath string) error {
	conn, err := net.DialTimeout("unix", socketPath, 2*time.Second)
	if err != nil {
		return err
	}
	return conn.Close()
}

// DoWithRetry retries fn up to MaxRetries times with exponential backoff
// for transient errors (connection failures, HTTP 5xx, 429).
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
				return lastErr
			case <-time.After(backoff):
			}
		}
	}
	return lastErr
}

// IsRetryable returns true for transient errors worth retrying:
// connection-level failures and HTTP 5xx/429 responses.
func IsRetryable(err error) bool {
	var ae *APIError
	if errors.As(err, &ae) {
		return ae.Code >= 500 || ae.Code == http.StatusTooManyRequests
	}
	// Non-APIError = connection-level failure, always retry.
	return true
}
