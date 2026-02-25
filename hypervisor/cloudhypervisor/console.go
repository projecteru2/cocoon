package cloudhypervisor

import (
	"context"
	"fmt"
	"io"
	"net"
)

// Console connects to the VM's console socket and returns a bidirectional stream.
//
// For UEFI-boot VMs (cloudimg): CH binds the serial port (ttyS0) to console.sock.
// For direct-boot VMs (OCI):    CH binds the virtio-console (hvc0) to console.sock.
//
// The caller is responsible for closing the returned ReadCloser.
func (ch *CloudHypervisor) Console(ctx context.Context, ref string) (io.ReadCloser, error) {
	info, err := ch.Inspect(ctx, ref)
	if err != nil {
		return nil, err
	}

	var conn io.ReadCloser
	if err := ch.withRunningVM(info.ID, func(_ int) error {
		sockPath := ch.conf.CHVMConsoleSock(info.ID)
		c, dialErr := (&net.Dialer{}).DialContext(ctx, "unix", sockPath)
		if dialErr != nil {
			return fmt.Errorf("connect to console socket %s: %w", sockPath, dialErr)
		}
		conn = c
		return nil
	}); err != nil {
		return nil, fmt.Errorf("console %s: %w", info.ID, err)
	}
	return conn, nil
}
