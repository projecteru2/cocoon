package utils

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// RunQemuImg shells out to qemu-img and wraps any non-zero exit with the
// trimmed combined output. Use this for operations without meaningful
// stdout (create, resize, convert); for queries that need a clean stdout
// payload (e.g. `info --output=json`), call exec.CommandContext directly.
func RunQemuImg(ctx context.Context, args ...string) error {
	if len(args) == 0 {
		return fmt.Errorf("qemu-img: no args")
	}
	// shell out because no Go qcow2 writer covers create/resize/convert at qemu-img's fidelity.
	out, err := exec.CommandContext(ctx, "qemu-img", args...).CombinedOutput() //nolint:gosec
	if err != nil {
		return fmt.Errorf("qemu-img %s: %s: %w", args[0], strings.TrimSpace(string(out)), err)
	}
	return nil
}
