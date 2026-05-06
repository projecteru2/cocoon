package hypervisor

import (
	"context"
	"path/filepath"
)

// LogFilePath returns the per-VM hypervisor log file under logDir, named
// after the backend type ("cloud-hypervisor.log" / "firecracker.log").
// Used at launch time (write) and by LogPath (read) so the two ends
// can't drift.
func (b *Backend) LogFilePath(logDir string) string {
	return filepath.Join(logDir, b.Typ+".log")
}

// LogPath resolves ref to a VM ID and returns its hypervisor log path.
func (b *Backend) LogPath(ctx context.Context, ref string) (string, error) {
	id, err := b.ResolveRef(ctx, ref)
	if err != nil {
		return "", err
	}
	return b.LogFilePath(b.Conf.VMLogDir(id)), nil
}
