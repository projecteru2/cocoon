package utils

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

func TestForEach_AllSucceed(t *testing.T) {
	result := ForEach(context.Background(), []string{"a", "b", "c"}, func(_ context.Context, _ string) error {
		return nil
	})

	if len(result.Succeeded) != 3 {
		t.Errorf("succeeded: got %d, want 3", len(result.Succeeded))
	}
	if len(result.Errors) != 0 {
		t.Errorf("errors: got %d, want 0", len(result.Errors))
	}
	if result.Err() != nil {
		t.Errorf("Err(): got %v, want nil", result.Err())
	}
}

func TestForEach_AllFail(t *testing.T) {
	result := ForEach(context.Background(), []string{"x", "y"}, func(_ context.Context, id string) error {
		return fmt.Errorf("fail %s", id)
	})

	if len(result.Succeeded) != 0 {
		t.Errorf("succeeded: got %d, want 0", len(result.Succeeded))
	}
	if len(result.Errors) != 2 {
		t.Errorf("errors: got %d, want 2", len(result.Errors))
	}
	if result.Err() == nil {
		t.Error("Err(): expected non-nil")
	}
}

func TestForEach_PartialFailure(t *testing.T) {
	result := ForEach(context.Background(), []string{"ok", "fail", "ok2"}, func(_ context.Context, id string) error {
		if id == "fail" {
			return fmt.Errorf("error on %s", id)
		}
		return nil
	})

	if len(result.Succeeded) != 2 {
		t.Errorf("succeeded: got %v, want 2", result.Succeeded)
	}
	if len(result.Errors) != 1 {
		t.Errorf("errors: got %d, want 1", len(result.Errors))
	}
}

func TestForEach_EmptyIDs(t *testing.T) {
	result := ForEach(context.Background(), nil, func(_ context.Context, _ string) error {
		t.Fatal("should not be called")
		return nil
	})

	if len(result.Succeeded) != 0 || len(result.Errors) != 0 {
		t.Errorf("expected empty result, got %+v", result)
	}
	if result.Err() != nil {
		t.Errorf("Err(): got %v, want nil", result.Err())
	}
}

func TestBatchResult_Err_NilForNoErrors(t *testing.T) {
	r := BatchResult{Succeeded: []string{"a"}}
	if r.Err() != nil {
		t.Errorf("expected nil, got %v", r.Err())
	}
}

func TestForEach_SingleID(t *testing.T) {
	result := ForEach(context.Background(), []string{"only"}, func(_ context.Context, id string) error {
		if id != "only" {
			t.Errorf("unexpected id: %s", id)
		}
		return nil
	})
	if len(result.Succeeded) != 1 {
		t.Errorf("succeeded: got %d, want 1", len(result.Succeeded))
	}
}

func TestForEach_Concurrent(t *testing.T) {
	var peak atomic.Int32
	var cur atomic.Int32

	result := ForEach(context.Background(), []string{"a", "b", "c", "d", "e"}, func(_ context.Context, _ string) error {
		n := cur.Add(1)
		for {
			old := peak.Load()
			if n <= old || peak.CompareAndSwap(old, n) {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
		cur.Add(-1)
		return nil
	})

	if len(result.Succeeded) != 5 {
		t.Errorf("succeeded: got %d, want 5", len(result.Succeeded))
	}
	if p := peak.Load(); p < 2 {
		t.Errorf("expected concurrent execution (peak=%d), got sequential", p)
	}
}

func TestForEach_WithConcurrencyLimit(t *testing.T) {
	var peak atomic.Int32
	var cur atomic.Int32

	result := ForEach(context.Background(), []string{"a", "b", "c", "d", "e"}, func(_ context.Context, _ string) error {
		n := cur.Add(1)
		for {
			old := peak.Load()
			if n <= old || peak.CompareAndSwap(old, n) {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
		cur.Add(-1)
		return nil
	}, 2)

	if len(result.Succeeded) != 5 {
		t.Errorf("succeeded: got %d, want 5", len(result.Succeeded))
	}
	if p := peak.Load(); p > 2 {
		t.Errorf("concurrency limit violated: peak=%d, limit=2", p)
	}
	if p := peak.Load(); p < 2 {
		t.Errorf("concurrency limit not reached: peak=%d, expected 2", p)
	}
}
