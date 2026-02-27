// Adapted from github.com/moby/term/proxy.go
// Copyright 2015 Docker, Inc. Licensed under the Apache License, Version 2.0.

package console

import "io"

// EscapeError is returned by EscapeProxy's Read when the configured
// escape key sequence is detected in the input stream.
type EscapeError struct{}

func (EscapeError) Error() string { return "read escape sequence" }

// escapeProxy wraps an io.Reader and watches for a configured escape key
// sequence. When the full sequence is matched, Read returns EscapeError.
// Partial matches are buffered and replayed if the sequence is not completed.
type escapeProxy struct {
	escapeKeys   []byte
	escapeKeyPos int
	r            io.Reader
	buf          []byte
}

// NewEscapeProxy returns a reader that transparently proxies reads from r
// until the escape key sequence is detected.
func NewEscapeProxy(r io.Reader, escapeKeys []byte) io.Reader {
	return &escapeProxy{
		escapeKeys: escapeKeys,
		r:          r,
	}
}

func (r *escapeProxy) Read(buf []byte) (n int, err error) {
	if len(r.escapeKeys) > 0 && r.escapeKeyPos == len(r.escapeKeys) {
		return 0, EscapeError{}
	}

	if len(r.buf) > 0 {
		n = copy(buf, r.buf)
		r.buf = r.buf[n:]
	}

	nr, err := r.r.Read(buf[n:])
	n += nr
	if len(r.escapeKeys) == 0 {
		return n, err
	}

	for i := 0; i < n; i++ {
		if buf[i] == r.escapeKeys[r.escapeKeyPos] {
			r.escapeKeyPos++

			// Full escape sequence matched.
			if r.escapeKeyPos == len(r.escapeKeys) {
				n = max(i+1-r.escapeKeyPos, 0)
				return n, EscapeError{}
			}
			continue
		}

		// Partial match failed — replay buffered escape bytes.
		if i < r.escapeKeyPos {
			preserve := make([]byte, 0, r.escapeKeyPos+n)
			preserve = append(preserve, r.escapeKeys[:r.escapeKeyPos]...)
			preserve = append(preserve, buf[:n]...)
			n = copy(buf, preserve)
			i += r.escapeKeyPos
			r.buf = append(r.buf, preserve[n:]...)
		}
		r.escapeKeyPos = 0
	}

	// Hide bytes that are part of a partial escape match from the caller.
	n -= r.escapeKeyPos
	if n < 0 {
		n = 0
	}
	return n, err
}
