package utils

import (
	"context"
	"fmt"
	"time"
)

// WaitFor polls check at the given interval until it returns (true, nil),
// returns a non-nil error, or the timeout/context expires.
func WaitFor(ctx context.Context, timeout, interval time.Duration, check func() (done bool, err error)) error {
	deadline := time.Now().Add(timeout)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout after %s", timeout)
		}
		done, err := check()
		if err != nil {
			return err
		}
		if done {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
	}
}
