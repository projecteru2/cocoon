package oci

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
)

const (
	erofsBlockSize   = 4096
	erofsCompression = "lz4hc"
)

// startErofsConversion pipes a tar stream into mkfs.erofs; caller writes+closes stdin, then cmd.Wait().
func startErofsConversion(ctx context.Context, uuid, outputPath string) (cmd *exec.Cmd, stdin io.WriteCloser, output *bytes.Buffer, err error) {
	// shell out because no Go EROFS writer library; mkfs.erofs is authoritative.
	cmd = exec.CommandContext(ctx, "mkfs.erofs", //nolint:gosec
		"--tar=f",
		fmt.Sprintf("-z%s", erofsCompression),
		fmt.Sprintf("-C%d", erofsBlockSize),
		"-T0",
		"-U", uuid,
		outputPath,
	)

	stdin, err = cmd.StdinPipe()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("create stdin pipe: %w", err)
	}

	output = &bytes.Buffer{}
	cmd.Stdout = output
	cmd.Stderr = output

	if err = cmd.Start(); err != nil {
		return nil, nil, nil, fmt.Errorf("start mkfs.erofs: %w", err)
	}
	return cmd, stdin, output, nil
}
