package hypervisor

import (
	"context"
	"io"
	"net/http"
	"os/exec"
	"time"

	"github.com/cocoonstack/cocoon/lock"
	"github.com/cocoonstack/cocoon/storage"
	"github.com/cocoonstack/cocoon/types"
)

const (
	APISocketName   = "api.sock"
	ConsoleSockName = "console.sock"
	VsockSockName   = "vsock.uds"

	// VsockGuestCID is constant — per-VM isolation comes from distinct UDS paths.
	VsockGuestCID = 3
	// VsockAgentPort is the cocoon-agent listen port.
	VsockAgentPort = 1024

	// CowSerial is the well-known virtio serial for the COW disk attached to OCI VMs.
	CowSerial = "cocoon-cow"

	// CreatingStateGCGrace bounds how long GC tolerates a "creating" VM.
	CreatingStateGCGrace = 24 * time.Hour

	// VMMemTransferTimeout is the single-shot timeout for snapshot/restore API calls.
	VMMemTransferTimeout = 10 * time.Minute

	// MinBalloonMemory: balloon overhead is not worthwhile below 256 MiB guest memory.
	MinBalloonMemory = 256 << 20

	// DefaultBalloonDiv sizes the initial balloon as memory/DefaultBalloonDiv (25%).
	DefaultBalloonDiv = 4

	// GracefulStopPollInterval polls between graceful shutdown signal and timeout escalation.
	GracefulStopPollInterval = 500 * time.Millisecond
)

// BackendConfig provides backend-specific values needed by shared Backend methods.
type BackendConfig interface {
	BinaryName() string
	PIDFileName() string
	TerminateGracePeriod() time.Duration
	SocketWaitTimeout() time.Duration
	EffectivePoolSize() int
	IndexFile() string
	RunDir() string
	LogDir() string
	VMRunDir(id string) string
	VMLogDir(id string) string
}

// Backend provides shared store operations for hypervisor backends.
type Backend struct {
	Typ    string
	Conf   BackendConfig
	DB     storage.Store[VMIndex]
	Locker lock.Locker
}

// LaunchSpec is the per-call input to Backend.LaunchVMProcess. Shared
// BinaryName / SocketWaitTimeout come from BackendConfig.
type LaunchSpec struct {
	Cmd       *exec.Cmd
	PIDPath   string
	SockPath  string
	NetnsPath string // empty = host netns
	OnFail    func() // optional cleanup on any error path
}

// RestoreSpec carries the backend-specific hooks for Backend.RestoreSequence.
type RestoreSpec struct {
	VMCfg        *types.VMConfig
	Snapshot     io.Reader
	Preflight    func(stagingDir string, rec *VMRecord) error
	Kill         func(ctx context.Context, vmID string, rec *VMRecord) error
	Wrap         func(rec *VMRecord, fn func() error) error // optional disk lock around merge+afterExtract
	BeforeMerge  func(rec *VMRecord) error                  // e.g. FC removes stale COW
	AfterExtract func(ctx context.Context, vmID string, vmCfg *types.VMConfig, rec *VMRecord) (*types.VM, error)
}

// DirectRestoreSpec is RestoreSpec for a local srcDir rather than a tar; Populate replaces staging+merge.
type DirectRestoreSpec struct {
	VMCfg        *types.VMConfig
	SrcDir       string
	Preflight    func(srcDir string, rec *VMRecord) error
	Kill         func(ctx context.Context, vmID string, rec *VMRecord) error
	Wrap         func(rec *VMRecord, fn func() error) error
	Populate     func(rec *VMRecord, srcDir string) error
	AfterExtract func(ctx context.Context, vmID string, vmCfg *types.VMConfig, rec *VMRecord) (*types.VM, error)
}

// CreateSpec carries the inputs to CreateSequence. Prepare returns the
// final storage configs (e.g. with COW + data disks attached); the rest of
// the sequence is uniform across backends.
type CreateSpec struct {
	VMCfg          *types.VMConfig
	StorageConfigs []*types.StorageConfig
	NetworkConfigs []*types.NetworkConfig
	BootConfig     *types.BootConfig
	Prepare        func(ctx context.Context, vmID string, vmCfg *types.VMConfig, storageConfigs []*types.StorageConfig, networkConfigs []*types.NetworkConfig, boot *types.BootConfig) ([]*types.StorageConfig, error)
}

// SnapshotSpec carries the backend-specific hooks for SnapshotSequence.
// hc is the shared http.Client built by SnapshotSequence so HTTP keep-alive
// reuses one CH/FC API socket connection across pause/capture/resume.
type SnapshotSpec struct {
	Pause        func(rec *VMRecord, hc *http.Client) error
	Resume       func(rec *VMRecord, hc *http.Client) error
	Capture      func(rec *VMRecord, hc *http.Client, tmpDir string) error
	Wrap         func(rec *VMRecord, fn func() error) error
	AfterCapture func(rec *VMRecord, tmpDir string) error
	BuildMeta    func(rec *VMRecord, tmpDir string) (*SnapshotMeta, error)
}

func (b *Backend) Type() string { return b.Typ }
