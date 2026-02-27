package console

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"
)

const DefaultEscapeChar byte = 0x1D // Ctrl+]

// Relay runs bidirectional I/O between the user terminal and the remote.
// rw can be a PTY (*os.File) or a Unix socket (net.Conn) — any io.ReadWriter.
// escapeKeys is the byte sequence that triggers a detach (e.g. {0x1D, '.'}).
// Returns nil on clean disconnect (escape sequence, EOF, or EIO).
func Relay(ctx context.Context, rw io.ReadWriter, escapeKeys []byte) error {
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
		proxy := NewEscapeProxy(ctxReader(ctx, os.Stdin), escapeKeys)
		_, err := io.Copy(rw, proxy)
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
		// Non-blocking: io.Copy on the remote side does NOT check ctx — it
		// only returns when rw is closed.  The caller's defer conn.Close()
		// handles that after Relay returns.  errCh has cap 2 so the
		// goroutine won't leak.
		select {
		case secondErr := <-errCh:
			if secondErr != nil && !isCleanExit(secondErr) {
				return secondErr
			}
		default:
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
		return 0, fmt.Errorf("CR/LF cannot be used as escape character")
	case b == 0x7F: //nolint:mnd
		return 0, fmt.Errorf("DEL (0x7F) cannot be used as escape character")
	case b >= 0x80: //nolint:mnd
		return 0, fmt.Errorf("non-ASCII byte 0x%02X cannot be used as escape character", b)
	}
	_ = original // used in older validation messages, kept for signature compat
	return b, nil
}

// isCleanExit returns true for errors that indicate a normal disconnect.
func isCleanExit(err error) bool {
	var escErr EscapeError
	return errors.Is(err, io.EOF) || errors.Is(err, syscall.EIO) || errors.As(err, &escErr)
}

// ctxReader wraps an io.Reader so that reads are abandoned when ctx is canceled.
// This is needed because os.Stdin.Read blocks and cannot be interrupted.
type ctxReaderWrapper struct {
	ctx context.Context
	r   io.Reader
}

func ctxReader(ctx context.Context, r io.Reader) io.Reader {
	return &ctxReaderWrapper{ctx: ctx, r: r}
}

func (cr *ctxReaderWrapper) Read(p []byte) (int, error) {
	type result struct {
		n   int
		err error
	}
	ch := make(chan result, 1)
	go func() {
		n, err := cr.r.Read(p)
		ch <- result{n, err}
	}()
	select {
	case <-cr.ctx.Done():
		return 0, cr.ctx.Err()
	case r := <-ch:
		return r.n, r.err
	}
}
