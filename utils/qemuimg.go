package utils

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// RunQemuImg shells out to qemu-img with the supplied args (e.g.
// "create", "-f", "qcow2", ...) and wraps any non-zero exit with the
// trimmed combined output. Use this for operations that have no
// meaningful stdout (create, resize, convert). For queries that need
// a clean stdout payload — e.g. `info --output=json` — call
// exec.CommandContext directly.
//
// cocoon shells out to qemu-img because there is no mature Go qcow2
// writer that covers create/resize/convert with the same fidelity;
// upstream qemu-img is authoritative for the disk-format matrix.
func RunQemuImg(ctx context.Context, args ...string) error {
	if len(args) == 0 {
		return fmt.Errorf("qemu-img: no args")
	}
	out, err := exec.CommandContext(ctx, "qemu-img", args...).CombinedOutput() //nolint:gosec
	if err != nil {
		return fmt.Errorf("qemu-img %s: %s: %w", args[0], strings.TrimSpace(string(out)), err)
	}
	return nil
}
