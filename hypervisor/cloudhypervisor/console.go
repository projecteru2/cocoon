package cloudhypervisor

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"

	"github.com/projecteru2/core/log"

	"github.com/cocoonstack/cocoon/hypervisor"
)

// Console connects to the VM's console output and returns a bidirectional stream.
//
// For UEFI-boot VMs (cloudimg): connects to the serial socket (console.sock).
// For direct-boot VMs (OCI):    opens the virtio-console PTY allocated by CH.
//
// The console path is resolved lazily on first access via the CH API
// (OCI/PTY) or the deterministic socket path (UEFI), so callers like
// Clone and Start don't need to query it upfront.
// The caller is responsible for closing the returned ReadWriteCloser.
func (ch *CloudHypervisor) Console(ctx context.Context, ref string) (io.ReadWriteCloser, error) {
	id, err := ch.ResolveRef(ctx, ref)
	if err != nil {
		return nil, err
	}

	rec, err := ch.LoadRecord(ctx, id)
	if err != nil {
		return nil, err
	}

	var conn io.ReadWriteCloser
	if err := ch.WithRunningVM(ctx, &rec, func(_ int) error {
		// Resolve on demand: query CH API for PTY (OCI) or use deterministic socket (UEFI).
		path := resolveConsole(ctx, id, hypervisor.SocketPath(rec.RunDir),
			hypervisor.ConsoleSockPath(rec.RunDir),
			isDirectBoot(rec.BootConfig))
		if path == "" {
			return fmt.Errorf("no console path for VM %s", id)
		}

		log.WithFunc("cloudhypervisor.Console").Debugf(ctx, "resolved console path for VM %s: %s", id, path)
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
		return nil, fmt.Errorf("console %s: %w", id, err)
	}
	return conn, nil
}
