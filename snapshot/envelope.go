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
	// SnapshotJSONName is the canonical envelope filename. Written as the
	// first tar entry by Export and required at the root of any directory
	// consumed by `vm clone --from-dir` or `vm restore --from-dir`.
	SnapshotJSONName = "snapshot.json"

	// envelopeVersion is the wire format version this build produces and accepts.
	envelopeVersion = 1
)

// ErrEnvelopeMissing is returned when the directory has no snapshot.json.
var ErrEnvelopeMissing = errors.New("snapshot envelope missing")

// ReadSnapshotEnvelope reads <dir>/snapshot.json and returns the embedded
// SnapshotConfig. Returns ErrEnvelopeMissing wrapped when the file is absent
// so callers can surface a precise message instead of a generic open error.
func ReadSnapshotEnvelope(dir string) (*types.SnapshotConfig, error) {
	path := filepath.Join(dir, SnapshotJSONName)
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%s missing in %s: %w", SnapshotJSONName, dir, ErrEnvelopeMissing)
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var envelope types.SnapshotExport
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, fmt.Errorf("parse %s: %w", SnapshotJSONName, err)
	}
	if envelope.Version != envelopeVersion {
		return nil, fmt.Errorf("unsupported snapshot envelope version %d (want %d)", envelope.Version, envelopeVersion)
	}
	return &envelope.Config, nil
}

// WriteSnapshotEnvelope writes <dir>/snapshot.json atomically so a partial
// write can't be consumed by a concurrent reader.
func WriteSnapshotEnvelope(dir string, cfg *types.SnapshotConfig) error {
	envelope := types.SnapshotExport{Version: envelopeVersion, Config: *cfg}
	return utils.AtomicWriteJSON(filepath.Join(dir, SnapshotJSONName), &envelope)
}
