package firecracker

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/cocoonstack/cocoon/hypervisor"
	"github.com/cocoonstack/cocoon/types"
)

const pidFileName = "fc.pid"

var runtimeFiles = []string{hypervisor.APISocketName, pidFileName, hypervisor.ConsoleSockName}

func (fc *Firecracker) preflightRestore(srcDir string, rec *hypervisor.VMRecord) error {
	return hypervisor.PreflightRestore(srcDir, fc.conf.RootDir, fc.conf.Config.RunDir, rec, snapshotIntegrity)
}

// snapshotIntegrity runs the cross-backend file/role checks then asserts the
// FC-specific vmstate+mem files exist (FC vmstate is binary so the sidecar is
// the only cocoon-side disk-shape source — no chCfg counterpart).
func snapshotIntegrity(srcDir string, sidecar []*types.StorageConfig) error {
	if err := hypervisor.ValidateSnapshotIntegrity(srcDir, sidecar); err != nil {
		return err
	}
	for _, fname := range []string{snapshotVMStateFile, snapshotMemFile} {
		if _, statErr := os.Stat(filepath.Join(srcDir, fname)); statErr != nil {
			return fmt.Errorf("snapshot file %s missing: %w", fname, statErr)
		}
	}
	return nil
}
