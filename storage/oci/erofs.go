package oci

import (
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os/exec"
)

const (
	size    = 16384
	zipType = "lz4hc"
)

// convertLayerToErofs converts an OCI layer tar stream to an EROFS filesystem.
// This mirrors start.sh's per-layer conversion:
//
//	gzip -dc layer.tar.gz | mkfs.erofs --tar=f -zlz4hc -C16384 -T0 -U <uuid> output.erofs
//
// If isGzip is true, the layerReader is decompressed via compress/gzip first.
// The caller is responsible for closing layerReader after this function returns.
func convertLayerToErofs(ctx context.Context, layerReader io.Reader, isGzip bool, uuid, outputPath string) error {
	reader := layerReader
	if isGzip {
		gr, err := gzip.NewReader(layerReader)
		if err != nil {
			return fmt.Errorf("create gzip reader: %w", err)
		}
		defer gr.Close() //nolint:errcheck
		reader = gr
	}

	cmd := exec.CommandContext(ctx, "mkfs.erofs",
		"--tar=f",
		fmt.Sprintf("-z%s", zipType),
		fmt.Sprintf("-C%d", size),
		"-T0",
		"-U", uuid,
		outputPath,
	)
	cmd.Stdin = reader

	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("mkfs.erofs failed: %w (output: %s)", err, string(output))
	}
	return nil
}
