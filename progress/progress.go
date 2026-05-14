package progress

// Nop is a no-op tracker for callers that don't need progress reporting.
var Nop Tracker = funcTracker(func(any) {})

// Tracker receives progress events during image operations.
// Implementations must be safe for concurrent use from multiple goroutines.
type Tracker interface {
	OnEvent(any)
}

type funcTracker func(any)

// OnEvent dispatches a progress event to the wrapped callback.
func (f funcTracker) OnEvent(e any) { f(e) }

// NewTracker wraps a typed callback as a non-generic Tracker so Images can hold it in its interface.
func NewTracker[E any](fn func(E)) Tracker {
	return funcTracker(func(v any) {
		if e, ok := v.(E); ok {
			fn(e)
		}
	})
}
