package firecracker

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/cocoonstack/cocoon/hypervisor"
)

const pidFileName = "fc.pid"

var runtimeFiles = []string{hypervisor.APISocketName, pidFileName, hypervisor.ConsoleSockName}

// preflightRestore is the FC preflight: common checks, role-sequence match,
// vmstate + mem files. FC vmstate is binary so the sidecar is the only
// cocoon-side disk-shape source — there's no chCfg counterpart.
func (fc *Firecracker) preflightRestore(srcDir string, rec *hypervisor.VMRecord) error {
	meta, err := loadSnapshotMeta(srcDir, fc.conf.RootDir, fc.conf.Config.RunDir)
	if err != nil {
		return err
	}
	if err := hypervisor.ValidateSnapshotIntegrity(srcDir, meta.StorageConfigs); err != nil {
		return err
	}
	for _, fname := range []string{snapshotVMStateFile, snapshotMemFile} {
		if _, statErr := os.Stat(filepath.Join(srcDir, fname)); statErr != nil {
			return fmt.Errorf("snapshot file %s missing: %w", fname, statErr)
		}
	}
	return hypervisor.ValidateRoleSequence(meta.StorageConfigs, rec.StorageConfigs)
}
