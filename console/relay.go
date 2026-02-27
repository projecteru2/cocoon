package console

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"
)

const (
	stateNormal    escapeState = iota
	stateLineStart             // After CR/LF or at session start — escape char recognized here.
	stateEscaped               // Escape char received at line start.

	DefaultEscapeChar byte = 0x1D
)

// escapeState tracks the three-state escape detection machine.
// Escape sequences are only recognized at the start of a line (after CR/LF
// or at session start), matching SSH client behavior.
type escapeState int

// Relay runs bidirectional I/O between the user terminal and the remote.
// rw can be a PTY (*os.File) or a Unix socket (net.Conn) — any io.ReadWriter.
// Returns nil on clean disconnect (escape sequence, EOF, or EIO).
func Relay(ctx context.Context, rw io.ReadWriter, escapeChar byte) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	errCh := make(chan error, 2) //nolint:mnd

	// remote → stdout (guest output to user).
	go func() {
		_, err := io.Copy(os.Stdout, rw)
		errCh <- err
		cancel()
	}()

	// stdin → remote (user input to guest), with escape detection.
	go func() {
		err := relayStdinToPTY(ctx, os.Stdin, rw, escapeChar)
		errCh <- err
		cancel()
	}()

	var firstErr error
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
	case firstErr = <-errCh:
	}

	if firstErr == nil || isCleanExit(firstErr) {
		// cancel() already fired — the other goroutine will exit promptly
		// (relayStdinToPTY checks ctx.Done(); io.Copy returns on close).
		if secondErr := <-errCh; secondErr != nil && !isCleanExit(secondErr) {
			return secondErr
		}
		return nil
	}
	return firstErr
}

// FormatEscapeChar returns a human-readable representation of the escape byte.
func FormatEscapeChar(b byte) string {
	if b >= 1 && b <= 0x1F {
		return "^" + string(rune(b+'@'))
	}
	return string(b)
}

// ParseEscapeChar parses the --escape-char flag value. It accepts:
//   - Caret notation for control characters: "^]", "^A", "^C", etc.
//   - A single printable or control character (raw byte).
func ParseEscapeChar(s string) (byte, error) {
	if len(s) == 2 && s[0] == '^' {
		c := s[1]
		if c >= '@' && c <= '_' {
			return validateEscapeByte(c-'@', s)
		}
		if c >= 'a' && c <= 'z' {
			return validateEscapeByte(c-'a'+1, s)
		}
		return 0, fmt.Errorf("invalid caret notation %q (use ^A through ^_ or ^a through ^z)", s)
	}
	if len(s) == 1 {
		return validateEscapeByte(s[0], s)
	}
	return 0, fmt.Errorf("escape-char must be a single character or ^X caret notation, got %q", s)
}

func validateEscapeByte(b byte, original string) (byte, error) {
	switch {
	case b == 0:
		return 0, fmt.Errorf("NUL cannot be used as escape character")
	case b == '\r' || b == '\n':
		return 0, fmt.Errorf("CR/LF cannot be used as escape character (conflicts with line-start detection)")
	case b == '.' || b == '?':
		return 0, fmt.Errorf("%q cannot be used as escape character (conflicts with escape sequence commands)", original)
	case b == 0x7F: //nolint:mnd
		return 0, fmt.Errorf("DEL (0x7F) cannot be used as escape character")
	case b >= 0x80: //nolint:mnd
		return 0, fmt.Errorf("non-ASCII byte 0x%02X cannot be used as escape character", b)
	}
	return b, nil
}

// isCleanExit returns true for errors that indicate a normal PTY disconnect.
func isCleanExit(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, syscall.EIO)
}

// relayStdinToPTY reads from stdin and writes to the PTY, with escape
// sequence detection. Returns nil on disconnect (escape-char + '.').
func relayStdinToPTY(ctx context.Context, stdin io.Reader, pty io.Writer, escapeChar byte) error {
	state := stateLineStart // Start of session acts as start of line.
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
			if b == '\r' || b == '\n' {
				state = stateLineStart
			}
			if _, werr := pty.Write(buf[:1]); werr != nil {
				return werr
			}

		case stateLineStart:
			if b == escapeChar {
				state = stateEscaped
				continue // Do not forward escape char yet.
			}
			if b == '\r' || b == '\n' {
				state = stateLineStart
			} else {
				state = stateNormal
			}
			if _, werr := pty.Write(buf[:1]); werr != nil {
				return werr
			}

		case stateEscaped:
			switch b {
			case '.':
				return nil // Disconnect.
			case '?':
				esc := FormatEscapeChar(escapeChar)
				helpMsg := "\r\nSupported escape sequences:\r\n" +
					"  " + esc + ".  Disconnect\r\n" +
					"  " + esc + "?  This help\r\n" +
					"  " + esc + esc + "  Send escape character\r\n"
				_, _ = os.Stdout.Write([]byte(helpMsg))
				state = stateLineStart
				continue
			case escapeChar:
				state = stateNormal
				if _, werr := pty.Write([]byte{escapeChar}); werr != nil {
					return werr
				}
			default:
				if b == '\r' || b == '\n' {
					state = stateLineStart
				} else {
					state = stateNormal
				}
				if _, werr := pty.Write([]byte{escapeChar, b}); werr != nil {
					return werr
				}
			}
		}
	}
}
