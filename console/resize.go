//go:build !windows

package console

import (
	"os"
	"os/signal"
	"syscall"

	csconsole "github.com/containerd/console"
)

// HandleResize propagates the initial terminal size from local to remote
// and listens for SIGWINCH to relay subsequent resize events.
// Returns a cleanup function that stops the signal handler.
func HandleResize(local, remote csconsole.Console) func() {
	_ = remote.ResizeFrom(local)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGWINCH)
	go func() {
		for range sigCh {
			_ = remote.ResizeFrom(local)
		}
	}()

	return func() {
		signal.Stop(sigCh)
		close(sigCh)
	}
}
