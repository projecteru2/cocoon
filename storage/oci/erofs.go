package oci

import (
	"context"
	"fmt"
	"io"
	"os/exec"
)

const (
	erofsBlockSize   = 16384
	erofsCompression = "lz4hc"
)

// convertLayerToErofs converts an OCI layer tar stream to an EROFS filesystem.
// This mirrors start.sh's per-layer conversion:
//
//	mkfs.erofs --tar=f -zlz4hc -C16384 -T0 -U <uuid> output.erofs
//
// The tarReader must be an uncompressed tar stream. The caller handles decompression
// (typically via go-containerregistry's layer.Uncompressed()) and is responsible for
// closing the reader after this function returns.
func convertLayerToErofs(ctx context.Context, tarReader io.Reader, uuid, outputPath string) error {
	cmd := exec.CommandContext(ctx, "mkfs.erofs",
		"--tar=f",
		fmt.Sprintf("-z%s", erofsCompression),
		fmt.Sprintf("-C%d", erofsBlockSize),
		"-T0",
		"-U", uuid,
		outputPath,
	)
	cmd.Stdin = tarReader

	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("mkfs.erofs failed: %w (output: %s)", err, string(output))
	}
	return nil
}
