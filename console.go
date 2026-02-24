package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/term"

	"github.com/projecteru2/cocoon/hypervisor"
)

const escapeChar = 0x1D // ctrl+]

// escapeState tracks the two-state escape detection machine.
type escapeState int

const (
	stateNormal  escapeState = iota
	stateEscaped             // ctrl+] received, waiting for command char
)

func cmdConsole(ctx context.Context, hyper hypervisor.Hypervisor, args []string) {
	if len(args) == 0 {
		fatalf("usage: cocoon console <vm-ref>")
	}
	ref := args[0]

	ptyPath, err := hyper.Console(ctx, ref)
	if err != nil {
		fatalf("console: %v", err)
	}

	pty, err := os.OpenFile(ptyPath, os.O_RDWR, 0) //nolint:gosec
	if err != nil {
		fatalf("open PTY %s: %v", ptyPath, err)
	}
	defer pty.Close() //nolint:errcheck

	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		fatalf("stdin is not a terminal")
	}

	oldState, err := term.MakeRaw(fd)
	if err != nil {
		fatalf("set raw mode: %v", err)
	}
	defer func() {
		_ = term.Restore(fd, oldState)
		fmt.Fprintf(os.Stderr, "\r\nDisconnected from %s.\r\n", ref)
	}()

	// Absorb SIGINT/SIGTERM to prevent bypassing terminal restore.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	go func() {
		for range sigCh {
		}
	}()

	cleanupWinch := handleSIGWINCH(os.Stdin, pty)
	defer cleanupWinch()

	fmt.Fprintf(os.Stderr, "Connected to %s (escape: ^]).\r\n", ref)

	if err := relayConsole(ctx, pty); err != nil {
		fmt.Fprintf(os.Stderr, "\r\nrelay error: %v\r\n", err)
	}
}

// relayConsole runs bidirectional I/O between the user terminal and the PTY.
func relayConsole(ctx context.Context, pty *os.File) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	errCh := make(chan error, 2) //nolint:mnd

	// PTY → stdout
	go func() {
		_, err := io.Copy(os.Stdout, pty)
		errCh <- err
		cancel()
	}()

	// stdin → PTY (with escape detection)
	go func() {
		err := relayStdinToPTY(ctx, os.Stdin, pty)
		errCh <- err
		cancel()
	}()

	select {
	case <-ctx.Done():
		select {
		case err := <-errCh:
			if err != nil && !isCleanExit(err) {
				return err
			}
		default:
		}
		return nil
	case err := <-errCh:
		if err == nil || isCleanExit(err) {
			select {
			case err2 := <-errCh:
				if err2 != nil && !isCleanExit(err2) {
					return err2
				}
			default:
			}
			return nil
		}
		return err
	}
}

// relayStdinToPTY reads from stdin and writes to the PTY with escape detection.
func relayStdinToPTY(ctx context.Context, stdin io.Reader, pty io.Writer) error {
	state := stateNormal
	buf := make([]byte, 1)

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		n, err := stdin.Read(buf)
		if n == 0 || err != nil {
			return err
		}
		b := buf[0]

		switch state {
		case stateNormal:
			if b == escapeChar {
				state = stateEscaped
				continue
			}
			if _, werr := pty.Write(buf[:1]); werr != nil {
				return werr
			}

		case stateEscaped:
			state = stateNormal
			switch b {
			case '.':
				return nil // disconnect
			case '?':
				helpMsg := "\r\nSupported escape sequences:\r\n" +
					"  ^].  Disconnect\r\n" +
					"  ^]?  This help\r\n" +
					"  ^]^] Send ^]\r\n"
				_, _ = os.Stdout.Write([]byte(helpMsg))
			case escapeChar:
				if _, werr := pty.Write([]byte{escapeChar}); werr != nil {
					return werr
				}
			default:
				// Unrecognized: forward both bytes.
				if _, werr := pty.Write([]byte{escapeChar, b}); werr != nil {
					return werr
				}
			}
		}
	}
}

// isCleanExit returns true for errors that indicate a normal PTY disconnect.
func isCleanExit(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, syscall.EIO)
}
