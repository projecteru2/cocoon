package hypervisor

import (
	"context"
	"path/filepath"

	"github.com/projecteru2/core/log"

	"github.com/cocoonstack/cocoon/utils"
)

func (b *Backend) PIDFilePath(runDir string) string {
	return filepath.Join(runDir, b.Conf.PIDFileName())
}

// LogFilePath returns the per-VM hypervisor log file under logDir (named after the backend so write/read can't drift).
func (b *Backend) LogFilePath(logDir string) string {
	return filepath.Join(logDir, b.Typ+".log")
}

// LogPath resolves ref → log path via the persisted LogDir (survives --log-dir change); falls back to current Conf for legacy records.
func (b *Backend) LogPath(ctx context.Context, ref string) (string, error) {
	id, err := b.ResolveRef(ctx, ref)
	if err != nil {
		return "", err
	}
	logDir := b.Conf.VMLogDir(id)
	if rec, err := b.LoadRecord(ctx, id); err == nil && rec.LogDir != "" {
		logDir = rec.LogDir
	}
	return b.LogFilePath(logDir), nil
}

// ForEachVM runs fn over ids in parallel up to EffectivePoolSize, logging per-id failures.
func (b *Backend) ForEachVM(ctx context.Context, ids []string, op string, fn func(context.Context, string) error) ([]string, error) {
	logger := log.WithFunc(b.Typ + "." + op)
	result := utils.ForEach(ctx, ids, fn, b.Conf.EffectivePoolSize())
	for _, err := range result.Errors {
		logger.Warnf(ctx, "%s: %v", op, err)
	}
	return result.Succeeded, result.Err()
}

func SocketPath(runDir string) string { return filepath.Join(runDir, APISocketName) }

func ConsoleSockPath(runDir string) string { return filepath.Join(runDir, ConsoleSockName) }

func VsockSockPath(runDir string) string { return filepath.Join(runDir, VsockSockName) }

// BalloonSize returns (bytes, enabled); disabled on Windows (virtio-win driver loops on deflation) and below MinBalloonMemory.
func BalloonSize(memoryBytes int64, windows bool) (int64, bool) {
	if windows || memoryBytes < MinBalloonMemory {
		return 0, false
	}
	return memoryBytes / DefaultBalloonDiv, true
}
