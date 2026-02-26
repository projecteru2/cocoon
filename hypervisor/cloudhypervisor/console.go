package cloudhypervisor

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
)

// Console connects to the VM's console output and returns a bidirectional stream.
//
// For UEFI-boot VMs (cloudimg): connects to the serial socket (console.sock).
// For direct-boot VMs (OCI):    opens the virtio-console PTY allocated by CH.
//
// The endpoint is stored in VM.ConsolePath at start time.
// The caller is responsible for closing the returned ReadWriteCloser.
func (ch *CloudHypervisor) Console(ctx context.Context, ref string) (io.ReadWriteCloser, error) {
	info, err := ch.Inspect(ctx, ref)
	if err != nil {
		return nil, err
	}

	var conn io.ReadWriteCloser
	if err := ch.withRunningVM(info.ID, func(_ int) error {
		path := info.ConsolePath
		if path == "" {
			return fmt.Errorf("no console path for VM %s", info.ID)
		}

		fi, statErr := os.Stat(path)
		if statErr != nil {
			return fmt.Errorf("stat console path %s: %w", path, statErr)
		}

		if fi.Mode()&os.ModeSocket != 0 {
			c, dialErr := (&net.Dialer{}).DialContext(ctx, "unix", path)
			if dialErr != nil {
				return fmt.Errorf("connect to console socket %s: %w", path, dialErr)
			}
			conn = c
		} else {
			f, openErr := os.OpenFile(path, os.O_RDWR, 0) //nolint:gosec
			if openErr != nil {
				return fmt.Errorf("open console PTY %s: %w", path, openErr)
			}
			conn = f
		}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("console %s: %w", info.ID, err)
	}
	return conn, nil
}
