package cloudhypervisor

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/cocoonstack/cocoon/types"
)

type patchOptions struct {
	storageConfigs []*types.StorageConfig
	consoleSock    string
	directBoot     bool
	cpu            int
	memory         int64
}

// patchCHConfig patches specific fields in config.json while preserving all
// unknown fields that CH adds internally (platform, cpus.topology, etc.).
// Uses a dual-parse approach: typed struct for reading/validation, raw JSON map for writing.
func patchCHConfig(path string, opts *patchOptions) error {
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}

	var chCfg chVMConfig
	if e := json.Unmarshal(data, &chCfg); e != nil {
		return fmt.Errorf("decode %s: %w", path, e)
	}

	var raw map[string]json.RawMessage
	if e := json.Unmarshal(data, &raw); e != nil {
		return fmt.Errorf("decode raw %s: %w", path, e)
	}

	// Disk paths: patch only "path" in each element, preserving pci_segment, id, etc.
	if len(opts.storageConfigs) != len(chCfg.Disks) {
		return fmt.Errorf("disk count mismatch: storageConfigs=%d, CH config=%d",
			len(opts.storageConfigs), len(chCfg.Disks))
	}
	if diskRaw, ok := raw["disks"]; ok {
		patched, patchErr := patchRawArray(diskRaw, len(opts.storageConfigs), func(i int, elem map[string]json.RawMessage) error {
			return setField(elem, "path", opts.storageConfigs[i].Path)
		})
		if patchErr != nil {
			return fmt.Errorf("patch disks: %w", patchErr)
		}
		raw["disks"] = patched
	}

	// Serial/console: full replace (snapshot carries stale /dev/pts/N paths).
	if opts.directBoot {
		_ = setField(raw, "serial", &chRuntimeFile{Mode: "Off"})
		_ = setField(raw, "console", &chRuntimeFile{Mode: "Pty"})
	} else {
		_ = setField(raw, "serial", &chRuntimeFile{Mode: "Socket", Socket: opts.consoleSock})
		_ = setField(raw, "console", &chRuntimeFile{Mode: "Off"})
	}

	// CPU: patch only "boot_vcpus", preserving topology, max_phys_bits, etc.
	if opts.cpu > 0 {
		if cpuRaw, ok := raw["cpus"]; ok {
			patched, patchErr := patchRawObject(cpuRaw, func(obj map[string]json.RawMessage) error {
				return setField(obj, "boot_vcpus", opts.cpu)
			})
			if patchErr != nil {
				return fmt.Errorf("patch cpus: %w", patchErr)
			}
			raw["cpus"] = patched
		}
	}

	// Memory + balloon.
	if opts.memory > 0 {
		if memRaw, ok := raw["memory"]; ok {
			patched, patchErr := patchRawObject(memRaw, func(obj map[string]json.RawMessage) error {
				return setField(obj, "size", opts.memory)
			})
			if patchErr != nil {
				return fmt.Errorf("patch memory: %w", patchErr)
			}
			raw["memory"] = patched
		}
		if balloonErr := patchBalloonRaw(raw, chCfg.Balloon, opts.memory); balloonErr != nil {
			return fmt.Errorf("patch balloon: %w", balloonErr)
		}
	}

	out, err := json.Marshal(raw)
	if err != nil {
		return fmt.Errorf("marshal patched config: %w", err)
	}
	return os.WriteFile(path, out, 0o600) //nolint:gosec
}

// patchBalloonRaw handles the balloon device in the raw config map.
func patchBalloonRaw(raw map[string]json.RawMessage, existing *chBalloon, memory int64) error {
	if memory < minBalloonMemory {
		delete(raw, "balloon")
		return nil
	}
	newSize := memory / defaultBalloon
	// Existing balloon: patch only "size", preserving device id and other CH fields.
	if existing != nil {
		if balloonRaw, ok := raw["balloon"]; ok {
			patched, err := patchRawObject(balloonRaw, func(obj map[string]json.RawMessage) error {
				return setField(obj, "size", newSize)
			})
			if err != nil {
				return fmt.Errorf("patch balloon size: %w", err)
			}
			raw["balloon"] = patched
			return nil
		}
	}
	// No existing balloon — create fresh.
	return setField(raw, "balloon", &chBalloon{
		Size:              newSize,
		DeflateOnOOM:      true,
		FreePageReporting: true,
	})
}

// patchStateJSON does string replacement in state.json for stale values.
//
// Disk paths: CH's vm.restore uses config.json (not state.json) to open disk files.
// The disk_path in serialized DiskConfig is informational — patching prevents
// debugging confusion from stale paths.
func patchStateJSON(path string, replacements map[string]string) error {
	if len(replacements) == 0 {
		return nil
	}
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	content := string(data)
	for oldVal, newVal := range replacements {
		content = strings.ReplaceAll(content, oldVal, newVal)
	}
	return os.WriteFile(path, []byte(content), 0o600) //nolint:gosec
}

// --- Raw JSON helpers ---

// setField marshals value and stores it in obj[key].
func setField(obj map[string]json.RawMessage, key string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("marshal field %q: %w", key, err)
	}
	obj[key] = raw
	return nil
}

// patchRawArray unmarshals a JSON array, applies fn to each element's raw map,
// and returns the patched array. Validates array length == count.
func patchRawArray(raw json.RawMessage, count int, fn func(int, map[string]json.RawMessage) error) (json.RawMessage, error) {
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err != nil {
		return nil, fmt.Errorf("decode array: %w", err)
	}
	if len(arr) != count {
		return nil, fmt.Errorf("array length mismatch: got %d, want %d", len(arr), count)
	}
	for i := range arr {
		var elem map[string]json.RawMessage
		if err := json.Unmarshal(arr[i], &elem); err != nil {
			return nil, fmt.Errorf("decode element %d: %w", i, err)
		}
		if err := fn(i, elem); err != nil {
			return nil, err
		}
		patched, err := json.Marshal(elem)
		if err != nil {
			return nil, fmt.Errorf("marshal element %d: %w", i, err)
		}
		arr[i] = patched
	}
	return json.Marshal(arr)
}

// patchRawObject unmarshals a JSON object, applies fn, and returns the patched object.
func patchRawObject(raw json.RawMessage, fn func(map[string]json.RawMessage) error) (json.RawMessage, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, fmt.Errorf("decode object: %w", err)
	}
	if err := fn(obj); err != nil {
		return nil, err
	}
	return json.Marshal(obj)
}
