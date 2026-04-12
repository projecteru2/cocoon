package hypervisor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/projecteru2/core/log"
	"github.com/vishvananda/netns"

	"github.com/cocoonstack/cocoon/types"
	"github.com/cocoonstack/cocoon/utils"
)

// RemoveVMDirs removes the run and log directories for a VM.
func RemoveVMDirs(runDir, logDir string) error {
	return errors.Join(
		os.RemoveAll(runDir),
		os.RemoveAll(logDir),
	)
}

// CleanupRuntimeFiles removes the given list of runtime files from runDir.
func CleanupRuntimeFiles(ctx context.Context, runDir string, files []string) {
	for _, name := range files {
		p := filepath.Join(runDir, name)
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			log.WithFunc("hypervisor.CleanupRuntimeFiles").Warnf(ctx, "cleanup %s: %v", p, err)
		}
	}
}

// ExtractBlobIDs extracts digest hexes from storage/boot paths for GC pinning.
func ExtractBlobIDs(storageConfigs []*types.StorageConfig, boot *types.BootConfig) map[string]struct{} {
	ids := make(map[string]struct{})
	if boot != nil && boot.KernelPath != "" {
		for _, s := range storageConfigs {
			if s.RO {
				ids[BlobHexFromPath(s.Path)] = struct{}{}
			}
		}
		ids[filepath.Base(filepath.Dir(boot.KernelPath))] = struct{}{}
		if boot.InitrdPath != "" {
			ids[filepath.Base(filepath.Dir(boot.InitrdPath))] = struct{}{}
		}
	} else if len(storageConfigs) > 0 {
		// Cloudimg: base qcow2 blob hex (before overlay replaces it).
		ids[BlobHexFromPath(storageConfigs[0].Path)] = struct{}{}
	}
	return ids
}

// BlobHexFromPath extracts the digest hex from a blob file path.
// e.g., "/var/lib/cocoon/oci/blobs/abc123.erofs" → "abc123"
func BlobHexFromPath(path string) string {
	base := filepath.Base(path)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

// PrefixToNetmask converts a CIDR prefix length to a dotted-decimal netmask string.
func PrefixToNetmask(prefix int) string {
	mask := net.CIDRMask(prefix, 32)
	return net.IP(mask).String()
}

// BuildIPParams generates kernel ip= parameters for NICs with static IPs.
func BuildIPParams(networkConfigs []*types.NetworkConfig, vmName string, dnsServers []string) string {
	var params strings.Builder
	fmt.Fprintf(&params, " cocoon.hostname=%s", vmName)
	var dns0, dns1 string
	if len(dnsServers) > 0 {
		dns0 = dnsServers[0]
	}
	if len(dnsServers) > 1 {
		dns1 = dnsServers[1]
	}
	for i, n := range networkConfigs {
		if n.Network == nil || n.Network.IP == "" {
			continue
		}
		param := fmt.Sprintf(" ip=%s::%s:%s:%s:eth%d:off",
			n.Network.IP, n.Network.Gateway,
			PrefixToNetmask(n.Network.Prefix), vmName, i)
		if dns0 != "" {
			param += ":" + dns0
			if dns1 != "" {
				param += ":" + dns1
			}
		}
		params.WriteString(param)
	}
	return params.String()
}

// CopyFile copies a single file preserving permissions.
func CopyFile(dst, src string) (err error) {
	srcFile, err := os.Open(src) //nolint:gosec
	if err != nil {
		return err
	}
	defer srcFile.Close() //nolint:errcheck

	fi, err := srcFile.Stat()
	if err != nil {
		return err
	}

	dstFile, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, fi.Mode()) //nolint:gosec
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, dstFile.Close()) }()

	_, err = io.Copy(dstFile, srcFile)
	return err
}

// MergeDirInto moves every file under src into the corresponding
// path under dst, overwriting existing entries. Used by the restore
// path to stage a snapshot extraction to a scratch dir first, then
// swap staged files into the live runDir only after the extraction
// has been proven usable — without this, a truncated or corrupt
// snapshot stream would destroy the running VM's state before the
// replacement was in a usable form.
//
// Parent directories are created as needed. Files are moved via
// os.Rename (same-filesystem assumption, which holds because the
// staging dir lives next to the destination). Directories in src
// that already exist in dst are merged recursively; the staging dir
// itself is left empty on success and should be removed by the
// caller. filepath.Walk visits each directory before its children,
// so by the time a file is processed its parent already exists in
// dst from the previous directory-visit step — no per-file MkdirAll
// is needed.
func MergeDirInto(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		if err := os.Rename(path, target); err != nil {
			return fmt.Errorf("rename %s to %s: %w", path, target, err)
		}
		return nil
	})
}

// ValidateHostCPU rejects VM configs that request more vCPUs than the
// host has cores. Called at every entry point that accepts a new
// VMConfig (Create, Restore, Clone, DirectClone, DirectRestore) so
// backends can trust rec.Config.CPU == runtime vCPU count. Without
// this, the CH backend used to silently clamp in buildVMConfig, which
// left network queue counts (derived from the unclamped vmCfg.CPU)
// and persisted VM records (kept the original request) out of sync
// with the actual runtime boot_vcpus value.
func ValidateHostCPU(cpu int) error {
	maxCPU := runtime.NumCPU()
	if cpu > maxCPU {
		return fmt.Errorf("requested %d vCPUs exceeds host cores (%d)", cpu, maxCPU)
	}
	return nil
}

// VerifyBaseFiles checks that all read-only layer files and boot files exist.
func VerifyBaseFiles(storageConfigs []*types.StorageConfig, boot *types.BootConfig) error {
	for _, sc := range storageConfigs {
		if !sc.RO {
			continue
		}
		if _, err := os.Stat(sc.Path); err != nil {
			return fmt.Errorf("base layer %s: %w", sc.Path, err)
		}
	}
	if boot == nil {
		return nil
	}
	for _, check := range []struct{ name, path string }{
		{"kernel", boot.KernelPath},
		{"initrd", boot.InitrdPath},
		{"firmware", boot.FirmwarePath},
	} {
		if check.path == "" {
			continue
		}
		if _, err := os.Stat(check.path); err != nil {
			return fmt.Errorf("%s %s: %w", check.name, check.path, err)
		}
	}
	return nil
}

// WaitForSocket polls until socketPath is connectable, the process exits, or
// the timeout/context fires.
func WaitForSocket(ctx context.Context, socketPath string, pid int, timeout time.Duration, processName string) error {
	return utils.WaitFor(ctx, timeout, 100*time.Millisecond, func() (bool, error) { //nolint:mnd
		if utils.CheckSocket(socketPath) == nil {
			return true, nil
		}
		if !utils.IsProcessAlive(pid) {
			return false, fmt.Errorf("%s exited before socket was ready", processName)
		}
		return false, nil
	})
}

// EnterNetns locks the OS thread, saves the current netns, and switches
// to the target netns. The forked child process inherits the new netns.
// Returns a restore function that must be deferred by the caller.
func EnterNetns(nsPath string) (restore func(), err error) {
	runtime.LockOSThread()

	orig, err := netns.Get()
	if err != nil {
		runtime.UnlockOSThread()
		return nil, fmt.Errorf("get current netns: %w", err)
	}

	target, err := netns.GetFromPath(nsPath)
	if err != nil {
		_ = orig.Close()
		runtime.UnlockOSThread()
		return nil, fmt.Errorf("open netns %s: %w", nsPath, err)
	}
	defer target.Close() //nolint:errcheck

	if err := netns.Set(target); err != nil {
		_ = orig.Close()
		runtime.UnlockOSThread()
		return nil, fmt.Errorf("setns %s: %w", nsPath, err)
	}

	return func() {
		_ = netns.Set(orig)
		_ = orig.Close()
		runtime.UnlockOSThread()
	}, nil
}
