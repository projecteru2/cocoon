package console

import (
	"os"
	"os/signal"
	"syscall"
	"unsafe"

	"golang.org/x/term"
)

// HandleSIGWINCH propagates the initial terminal size and listens for
// SIGWINCH to relay resize events to the PTY. Returns a cleanup function.
func HandleSIGWINCH(local *os.File, remote *os.File) func() {
	propagateTerminalSize(local, remote)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGWINCH)
	go func() {
		for range sigCh {
			propagateTerminalSize(local, remote)
		}
	}()

	return func() {
		signal.Stop(sigCh)
		close(sigCh)
	}
}

func propagateTerminalSize(local *os.File, remote *os.File) {
	width, height, err := term.GetSize(int(local.Fd()))
	if err != nil {
		return
	}
	_ = setWinSize(remote, width, height)
}

// winSize matches the kernel's struct winsize for the TIOCSWINSZ ioctl.
type winSize struct {
	Rows uint16
	Cols uint16
	X    uint16
	Y    uint16
}

func setWinSize(f *os.File, cols, rows int) error {
	ws := winSize{Rows: uint16(rows), Cols: uint16(cols)} //nolint:gosec
	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		f.Fd(),
		syscall.TIOCSWINSZ,
		uintptr(unsafe.Pointer(&ws)), //nolint:gosec
	)
	if errno != 0 {
		return errno
	}
	return nil
}
