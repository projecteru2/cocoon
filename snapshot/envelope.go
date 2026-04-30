package snapshot

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/cocoonstack/cocoon/types"
	"github.com/cocoonstack/cocoon/utils"
)

const (
	SnapshotJSONName = "snapshot.json"
	EnvelopeVersion  = 1
)

// ErrEnvelopeMissing wraps the not-found case so callers can render a
// dir-specific error instead of a raw open failure.
var ErrEnvelopeMissing = errors.New("snapshot envelope missing")

// ReadSnapshotEnvelope reads <dir>/snapshot.json into a SnapshotConfig.
func ReadSnapshotEnvelope(dir string) (types.SnapshotConfig, error) {
	path := filepath.Join(dir, SnapshotJSONName)
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return types.SnapshotConfig{}, fmt.Errorf("%s missing in %s: %w", SnapshotJSONName, dir, ErrEnvelopeMissing)
		}
		return types.SnapshotConfig{}, fmt.Errorf("read %s: %w", path, err)
	}
	envelope := types.SnapshotExport{}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return types.SnapshotConfig{}, fmt.Errorf("parse %s: %w", SnapshotJSONName, err)
	}
	if envelope.Version != EnvelopeVersion {
		return types.SnapshotConfig{}, fmt.Errorf("unsupported snapshot envelope version %d (want %d)", envelope.Version, EnvelopeVersion)
	}
	return envelope.Config, nil
}

// WriteSnapshotEnvelope writes <dir>/snapshot.json atomically so a concurrent
// reader can't see a partial write.
func WriteSnapshotEnvelope(dir string, cfg types.SnapshotConfig) error {
	return utils.AtomicWriteJSON(filepath.Join(dir, SnapshotJSONName),
		types.SnapshotExport{Version: EnvelopeVersion, Config: cfg})
}
