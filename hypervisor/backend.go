package hypervisor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/projecteru2/core/log"

	"github.com/cocoonstack/cocoon/lock"
	"github.com/cocoonstack/cocoon/storage"
	"github.com/cocoonstack/cocoon/types"
	"github.com/cocoonstack/cocoon/utils"
)

// Shared constants for all hypervisor backends.
const (
	APISocketName   = "api.sock"
	ConsoleSockName = "console.sock"

	// CowSerial is the well-known virtio serial for the COW disk attached to OCI VMs.
	CowSerial = "cocoon-cow"

	// CreatingStateGCGrace bounds how long GC tolerates a "creating" VM.
	CreatingStateGCGrace = 24 * time.Hour

	// VMMemTransferTimeout is the single-shot timeout for snapshot/restore API calls.
	VMMemTransferTimeout = 10 * time.Minute

	// MinBalloonMemory is the minimum guest memory (256 MiB) below which
	// balloon is not enabled — the overhead is not worthwhile for tiny VMs.
	MinBalloonMemory = 256 << 20

	// DefaultBalloonDiv sizes the initial balloon as memory/DefaultBalloonDiv (25%).
	DefaultBalloonDiv = 4

	// GracefulStopPollInterval is how often we check if the guest has powered off
	// after sending a graceful shutdown signal (ACPI power-button or SendCtrlAltDel).
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

func (b *Backend) Type() string { return b.Typ }

// Inspect returns VM info for a single VM by ref (ID, name, or prefix).
func (b *Backend) Inspect(ctx context.Context, ref string) (*types.VM, error) {
	var result *types.VM
	return result, b.DB.With(ctx, func(idx *VMIndex) error {
		id, err := idx.Resolve(ref)
		if err != nil {
			return err
		}
		result = b.ToVM(idx.VMs[id])
		return nil
	})
}

// List returns VM info for all known VMs.
func (b *Backend) List(ctx context.Context) ([]*types.VM, error) {
	var result []*types.VM
	return result, b.DB.With(ctx, func(idx *VMIndex) error {
		result = utils.MapValues(idx.VMs, b.ToVM)
		return nil
	})
}

func (b *Backend) ToVM(rec *VMRecord) *types.VM {
	info := rec.VM // value copy
	info.Hypervisor = b.Typ
	if info.State == types.VMStateRunning {
		info.SocketPath = SocketPath(rec.RunDir)
		info.PID, _ = utils.ReadPIDFile(b.PIDFilePath(rec.RunDir))
	}
	info.SnapshotIDs = maps.Clone(info.SnapshotIDs)
	return &info
}

// ResolveRef resolves a single ref (ID, name, or prefix) to an exact VM ID.
func (b *Backend) ResolveRef(ctx context.Context, ref string) (string, error) {
	var id string
	return id, b.DB.With(ctx, func(idx *VMIndex) error {
		var err error
		id, err = idx.Resolve(ref)
		return err
	})
}

// ResolveRefs batch-resolves refs to exact VM IDs under a single lock.
func (b *Backend) ResolveRefs(ctx context.Context, refs []string) ([]string, error) {
	var ids []string
	return ids, b.DB.With(ctx, func(idx *VMIndex) error {
		var err error
		ids, err = idx.ResolveMany(refs)
		return err
	})
}

func (b *Backend) LoadRecord(ctx context.Context, id string) (VMRecord, error) {
	var rec VMRecord
	return rec, b.DB.With(ctx, func(idx *VMIndex) error {
		var err error
		rec, err = utils.LookupCopy(idx.VMs, id)
		return err
	})
}

// WithRunningVM calls fn if rec still points to a live VM process.
func (b *Backend) WithRunningVM(ctx context.Context, rec *VMRecord, fn func(pid int) error) error {
	pid, pidErr := utils.ReadPIDFile(b.PIDFilePath(rec.RunDir))
	if pidErr != nil && !errors.Is(pidErr, fs.ErrNotExist) {
		log.WithFunc(b.Typ+".WithRunningVM").Warnf(ctx, "read PID file: %v", pidErr)
	}
	if !utils.VerifyProcessCmdline(pid, b.Conf.BinaryName(), SocketPath(rec.RunDir)) {
		return ErrNotRunning
	}
	return fn(pid)
}

// WithPausedVM pauses, runs fn, resumes. Resume on the success path is eager
// so its error promotes to the return; on fn-error the deferred resume only
// logs (the inner error wins).
func (b *Backend) WithPausedVM(ctx context.Context, rec *VMRecord, pause, resume, fn func() error) error {
	return b.WithRunningVM(ctx, rec, func(_ int) error {
		if err := pause(); err != nil {
			return fmt.Errorf("pause: %w", err)
		}
		var resumed bool
		var resumeErr error
		logger := log.WithFunc(b.Typ + ".WithPausedVM")
		doResume := func() {
			if resumed {
				return
			}
			resumed = true
			resumeErr = resume()
			if resumeErr != nil {
				logger.Warnf(ctx, "resume VM %s: %v", rec.ID, resumeErr)
			}
		}
		defer doResume()
		if err := fn(); err != nil {
			return err
		}
		doResume()
		if resumeErr != nil {
			return fmt.Errorf("snapshot data captured but resume failed: %w", resumeErr)
		}
		return nil
	})
}

func (b *Backend) UpdateStates(ctx context.Context, ids []string, state types.VMState) error {
	if len(ids) == 0 {
		return nil
	}
	now := time.Now()
	return b.DB.Update(ctx, func(idx *VMIndex) error {
		for _, id := range ids {
			r := idx.VMs[id]
			if r == nil {
				continue
			}
			r.State = state
			r.UpdatedAt = now
			switch state {
			case types.VMStateRunning:
				r.StartedAt = &now
			case types.VMStateStopped:
				r.StoppedAt = &now
			}
		}
		return nil
	})
}

func (b *Backend) MarkError(ctx context.Context, id string) {
	if err := b.UpdateStates(ctx, []string{id}, types.VMStateError); err != nil {
		log.WithFunc(b.Typ+".MarkError").Warnf(ctx, "mark VM %s error: %v", id, err)
	}
}

func (b *Backend) ReserveVM(ctx context.Context, id string, vmCfg *types.VMConfig, blobIDs map[string]struct{}, runDir, logDir string) error {
	now := time.Now()
	return b.DB.Update(ctx, func(idx *VMIndex) error {
		if idx.VMs[id] != nil {
			return fmt.Errorf("id collision %q (retry)", id)
		}
		if dup, ok := idx.Names[vmCfg.Name]; ok {
			return fmt.Errorf("vm name %q already exists (id: %s)", vmCfg.Name, dup)
		}
		idx.VMs[id] = &VMRecord{
			VM: types.VM{
				ID: id, Hypervisor: b.Typ, State: types.VMStateCreating,
				Config: *vmCfg, CreatedAt: now, UpdatedAt: now,
			},
			ImageBlobIDs: blobIDs,
			RunDir:       runDir,
			LogDir:       logDir,
		}
		idx.Names[vmCfg.Name] = id
		return nil
	})
}

// CloneSetup handles the shared pre-clone sequence used by both
// backends' Clone and DirectClone entry points: validate CPU,
// reserve a placeholder record, create dirs, and return a cleanup function.
func (b *Backend) CloneSetup(ctx context.Context, vmID string, vmCfg *types.VMConfig, snapshotConfig *types.SnapshotConfig) (runDir, logDir string, now time.Time, cleanup func(), err error) {
	if err = ValidateHostCPU(vmCfg.CPU); err != nil {
		return "", "", time.Time{}, nil, err
	}
	now = time.Now()
	runDir = b.Conf.VMRunDir(vmID)
	logDir = b.Conf.VMLogDir(vmID)

	cleanup = func() {
		_ = RemoveVMDirs(runDir, logDir)
		b.RollbackCreate(ctx, vmID, vmCfg.Name)
	}

	if err = b.ReserveVM(ctx, vmID, vmCfg, snapshotConfig.ImageBlobIDs, runDir, logDir); err != nil {
		return "", "", time.Time{}, nil, fmt.Errorf("reserve VM record: %w", err)
	}
	if err = utils.EnsureDirs(runDir, logDir); err != nil {
		cleanup()
		return "", "", time.Time{}, nil, fmt.Errorf("ensure dirs: %w", err)
	}
	return runDir, logDir, now, cleanup, nil
}

// RollbackCreate removes a placeholder VM record from the DB.
func (b *Backend) RollbackCreate(ctx context.Context, id, name string) {
	if err := b.DB.Update(ctx, func(idx *VMIndex) error {
		delete(idx.VMs, id)
		if name != "" && idx.Names[name] == id {
			delete(idx.Names, name)
		}
		return nil
	}); err != nil {
		log.WithFunc(b.Typ+".RollbackCreate").Warnf(ctx, "rollback VM %s (name=%s): %v", id, name, err)
	}
}

func (b *Backend) ForEachVM(ctx context.Context, ids []string, op string, fn func(context.Context, string) error) ([]string, error) {
	logger := log.WithFunc(b.Typ + "." + op)
	result := utils.ForEach(ctx, ids, fn, b.Conf.EffectivePoolSize())
	for _, err := range result.Errors {
		logger.Warnf(ctx, "%s: %v", op, err)
	}
	return result.Succeeded, result.Err()
}

// KillForRestore stops a running VM and cleans up its runtime files in
// preparation for a restore. The terminate callback performs the
// backend-specific shutdown (e.g. CH graceful shutdown vs FC direct kill).
func (b *Backend) KillForRestore(ctx context.Context, vmID string, rec *VMRecord, terminate func(pid int) error, runtimeFiles []string) error {
	killErr := b.WithRunningVM(ctx, rec, terminate)
	if killErr != nil && !errors.Is(killErr, ErrNotRunning) {
		b.MarkError(ctx, vmID)
		return fmt.Errorf("stop running VM: %w", killErr)
	}
	CleanupRuntimeFiles(ctx, rec.RunDir, runtimeFiles)
	return nil
}

// DirectCloneBase runs the shared DirectClone sequence: reserve a
// placeholder via CloneSetup, copy snapshot files via cloneFiles, then
// hand off to afterExtract for backend-specific startup.
func (b *Backend) DirectCloneBase(
	ctx context.Context, vmID string, vmCfg *types.VMConfig,
	networkConfigs []*types.NetworkConfig, snapshotConfig *types.SnapshotConfig, srcDir string,
	cloneFiles func(dstDir, srcDir string) error,
	afterExtract func(ctx context.Context, vmID string, vmCfg *types.VMConfig, networkConfigs []*types.NetworkConfig, runDir, logDir string, now time.Time) (*types.VM, error),
) (_ *types.VM, err error) {
	runDir, logDir, now, cleanup, err := b.CloneSetup(ctx, vmID, vmCfg, snapshotConfig)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			cleanup()
		}
	}()

	if err = cloneFiles(runDir, srcDir); err != nil {
		return nil, fmt.Errorf("clone snapshot files: %w", err)
	}

	return afterExtract(ctx, vmID, vmCfg, networkConfigs, runDir, logDir, now)
}

// CloneFromStream runs the shared streaming Clone sequence: reserve a
// placeholder via CloneSetup, extract the snapshot tar into runDir, then
// hand off to afterExtract for backend-specific startup.
func (b *Backend) CloneFromStream(
	ctx context.Context, vmID string, vmCfg *types.VMConfig,
	networkConfigs []*types.NetworkConfig, snapshotConfig *types.SnapshotConfig, snapshot io.Reader,
	afterExtract func(ctx context.Context, vmID string, vmCfg *types.VMConfig, networkConfigs []*types.NetworkConfig, runDir, logDir string, now time.Time) (*types.VM, error),
) (_ *types.VM, err error) {
	runDir, logDir, now, cleanup, err := b.CloneSetup(ctx, vmID, vmCfg, snapshotConfig)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			cleanup()
		}
	}()

	if err = utils.ExtractTar(runDir, snapshot); err != nil {
		return nil, fmt.Errorf("extract snapshot: %w", err)
	}

	return afterExtract(ctx, vmID, vmCfg, networkConfigs, runDir, logDir, now)
}

// ResolveForRestore resolves VM ref and validates it's running.
func (b *Backend) ResolveForRestore(ctx context.Context, vmRef string) (string, *VMRecord, error) {
	vmID, err := b.ResolveRef(ctx, vmRef)
	if err != nil {
		return "", nil, err
	}
	rec, err := b.LoadRecord(ctx, vmID)
	if err != nil {
		return "", nil, err
	}
	if rec.State != types.VMStateRunning {
		return "", nil, fmt.Errorf("vm %s is %s, must be running to restore", vmID, rec.State)
	}
	return vmID, &rec, nil
}

// GracefulStop sends a shutdown signal, polls until the process exits,
// and escalates via the escalate closure if the timeout fires.
// Used by both CH (ACPI power-button) and FC (SendCtrlAltDel).
func (b *Backend) GracefulStop(ctx context.Context, vmID string, pid int, timeout time.Duration, signal, escalate func() error) error {
	logger := log.WithFunc(b.Typ + ".GracefulStop")
	if err := signal(); err != nil {
		logger.Warnf(ctx, "shutdown signal %s: %v — escalating", vmID, err)
		return escalate()
	}
	if err := utils.WaitFor(ctx, timeout, GracefulStopPollInterval, func() (bool, error) {
		return !utils.IsProcessAlive(pid), nil
	}); err == nil {
		return nil
	}
	// Distinguish user cancellation from timeout.
	if ctx.Err() != nil {
		return ctx.Err()
	}
	logger.Warnf(ctx, "VM %s did not shut down within %s, escalating", vmID, timeout)
	return escalate()
}

// AbortLaunch terminates a failed launch and removes runtime files.
func (b *Backend) AbortLaunch(ctx context.Context, pid int, sockPath, runDir string, runtimeFiles []string) {
	_ = utils.TerminateProcess(ctx, pid, b.Conf.BinaryName(), sockPath, b.Conf.TerminateGracePeriod())
	CleanupRuntimeFiles(ctx, runDir, runtimeFiles)
}

// StartAll is the shared Start template: resolve refs, run startOne per ID,
// flip succeeded records to Running. Both backends call it as a one-liner.
func (b *Backend) StartAll(ctx context.Context, refs []string, startOne func(context.Context, string) error) ([]string, error) {
	ids, err := b.ResolveRefs(ctx, refs)
	if err != nil {
		return nil, err
	}
	succeeded, forEachErr := b.ForEachVM(ctx, ids, "Start", startOne)
	if batchErr := b.BatchMarkStarted(ctx, succeeded); batchErr != nil {
		log.WithFunc(b.Typ+".Start").Warnf(ctx, "batch state update: %v", batchErr)
	}
	return succeeded, forEachErr
}

// StopAll is the shared Stop template: resolve refs, run stopOne per ID,
// flip succeeded records to Stopped.
func (b *Backend) StopAll(ctx context.Context, refs []string, stopOne func(context.Context, string) error) ([]string, error) {
	ids, err := b.ResolveRefs(ctx, refs)
	if err != nil {
		return nil, err
	}
	succeeded, forEachErr := b.ForEachVM(ctx, ids, "Stop", stopOne)
	if batchErr := b.UpdateStates(ctx, succeeded, types.VMStateStopped); batchErr != nil {
		log.WithFunc(b.Typ+".Stop").Warnf(ctx, "batch state update: %v", batchErr)
	}
	return succeeded, forEachErr
}

// DeleteAll is the shared Delete template: gracefully stop a running VM when
// force=true via the supplied stopOne, remove the VM dirs, then delete the
// index record. The dir-cleanup intentionally precedes the DB delete so a
// failed cleanup leaves the record intact and the user can retry rm.
func (b *Backend) DeleteAll(ctx context.Context, refs []string, force bool, stopOne func(context.Context, string) error) ([]string, error) {
	ids, err := b.ResolveRefs(ctx, refs)
	if err != nil {
		return nil, err
	}
	return b.ForEachVM(ctx, ids, "Delete", func(ctx context.Context, id string) error {
		rec, loadErr := b.LoadRecord(ctx, id)
		if loadErr != nil {
			return loadErr
		}
		if runningErr := b.WithRunningVM(ctx, &rec, func(_ int) error {
			if !force {
				return fmt.Errorf("running (force required)")
			}
			return stopOne(ctx, id)
		}); runningErr != nil && !errors.Is(runningErr, ErrNotRunning) {
			return fmt.Errorf("stop before delete: %w", runningErr)
		}
		if rmErr := RemoveVMDirs(rec.RunDir, rec.LogDir); rmErr != nil {
			return fmt.Errorf("cleanup VM dirs: %w", rmErr)
		}
		return b.DB.Update(ctx, func(idx *VMIndex) error {
			r := idx.VMs[id]
			if r == nil {
				return ErrNotFound
			}
			delete(idx.Names, r.Config.Name)
			delete(idx.VMs, id)
			return nil
		})
	})
}

func (b *Backend) BatchMarkStarted(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	now := time.Now()
	return b.DB.Update(ctx, func(idx *VMIndex) error {
		for _, id := range ids {
			r := idx.VMs[id]
			if r == nil {
				continue
			}
			r.State = types.VMStateRunning
			r.StartedAt = &now
			r.UpdatedAt = now
			r.FirstBooted = true
		}
		return nil
	})
}

// CleanStalePlaceholders removes "creating" records past GC grace period.
func (b *Backend) CleanStalePlaceholders(_ context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	cutoff := time.Now().Add(-CreatingStateGCGrace)
	return b.DB.WriteRaw(func(idx *VMIndex) error {
		utils.CleanStaleRecords(idx.VMs, idx.Names, ids,
			func(r *VMRecord) string { return r.Config.Name },
			func(r *VMRecord) bool {
				return r.State == types.VMStateCreating && r.UpdatedAt.Before(cutoff)
			},
		)
		return nil
	})
}

// GCCollect removes orphan VM directories and stale DB records.
// Kills leftover hypervisor processes before removing directories.
// Runs under the GC orchestrator's flock — uses lock-free DB access.
func (b *Backend) GCCollect(ctx context.Context, ids []string) error {
	var errs []error
	for _, id := range ids {
		runDir, logDir := b.Conf.VMRunDir(id), b.Conf.VMLogDir(id)
		_ = b.DB.ReadRaw(func(idx *VMIndex) error {
			if rec := idx.VMs[id]; rec != nil {
				runDir, logDir = rec.RunDir, rec.LogDir
			}
			return nil
		})
		b.killOrphanProcess(ctx, runDir)
		if err := RemoveVMDirs(runDir, logDir); err != nil {
			errs = append(errs, err)
		}
	}
	if err := b.CleanStalePlaceholders(ctx, ids); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func (b *Backend) PIDFilePath(runDir string) string {
	return filepath.Join(runDir, b.Conf.PIDFileName())
}

func (b *Backend) RecordSnapshot(ctx context.Context, vmID, tmpDir string) (snapID string, err error) {
	defer func() {
		if err != nil {
			os.RemoveAll(tmpDir) //nolint:errcheck,gosec
		}
	}()

	snapID = utils.GenerateID()
	if err = b.DB.Update(ctx, func(idx *VMIndex) error {
		r := idx.VMs[vmID]
		if r == nil {
			return fmt.Errorf("vm %s disappeared from index", vmID)
		}
		if r.SnapshotIDs == nil {
			r.SnapshotIDs = make(map[string]struct{})
		}
		r.SnapshotIDs[snapID] = struct{}{}
		return nil
	}); err != nil {
		return "", fmt.Errorf("record snapshot on VM: %w", err)
	}
	return snapID, nil
}

func (b *Backend) BuildSnapshotConfig(snapID string, rec *VMRecord) *types.SnapshotConfig {
	cfg := &types.SnapshotConfig{
		ID:           snapID,
		Hypervisor:   b.Typ,
		NICs:         len(rec.NetworkConfigs),
		ImageBlobIDs: maps.Clone(rec.ImageBlobIDs),
		Config:       rec.Config.Config,
	}
	return cfg
}

// FinalizeRestore updates DB and assembles returned VM after restore.
func (b *Backend) FinalizeRestore(ctx context.Context, vmID string, vmCfg *types.VMConfig, rec *VMRecord, pid int) (*types.VM, error) {
	now := time.Now()
	if err := b.DB.Update(ctx, func(idx *VMIndex) error {
		r := idx.VMs[vmID]
		if r == nil {
			return fmt.Errorf("vm %s disappeared from index", vmID)
		}
		r.Config = *vmCfg
		r.State = types.VMStateRunning
		r.StartedAt = &now
		r.UpdatedAt = now
		return nil
	}); err != nil {
		return nil, fmt.Errorf("update record: %w", err)
	}

	info := rec.VM
	info.Config = *vmCfg
	info.State = types.VMStateRunning
	info.PID = pid
	info.SocketPath = SocketPath(rec.RunDir)
	info.StartedAt = &now
	info.UpdatedAt = now
	return &info, nil
}

// PrepareStart loads record, validates state, ensures dirs are ready.
func (b *Backend) PrepareStart(ctx context.Context, id string, runtimeFiles []string) (*VMRecord, error) {
	rec, err := b.LoadRecord(ctx, id)
	if err != nil {
		return nil, err
	}

	runErr := b.WithRunningVM(ctx, &rec, func(_ int) error { return nil })
	switch {
	case runErr == nil:
		return nil, nil // already running
	case errors.Is(runErr, ErrNotRunning):
		// expected — proceed to start
	default:
		return nil, fmt.Errorf("reconcile running VM %s: %w", id, runErr)
	}

	if err = utils.EnsureDirs(rec.RunDir, rec.LogDir); err != nil {
		return nil, fmt.Errorf("ensure dirs: %w", err)
	}
	CleanupRuntimeFiles(ctx, rec.RunDir, runtimeFiles)
	return &rec, nil
}

// FinalizeCreate writes populated VM record to DB, replacing placeholder.
func (b *Backend) FinalizeCreate(ctx context.Context, id string, info *types.VM, bootCfg *types.BootConfig, blobIDs map[string]struct{}) error {
	return b.DB.Update(ctx, func(idx *VMIndex) error {
		existing := idx.VMs[id]
		if existing == nil {
			return fmt.Errorf("vm %s disappeared from index", id)
		}
		idx.VMs[id] = &VMRecord{
			VM:           *info,
			BootConfig:   bootCfg,
			ImageBlobIDs: blobIDs,
			RunDir:       existing.RunDir,
			LogDir:       existing.LogDir,
		}
		return nil
	})
}

func (b *Backend) FinalizeClone(ctx context.Context, vmID string, info *types.VM, bootCfg *types.BootConfig, blobIDs map[string]struct{}) error {
	return b.DB.Update(ctx, func(idx *VMIndex) error {
		r := idx.VMs[vmID]
		if r == nil {
			return fmt.Errorf("vm %s disappeared from index", vmID)
		}
		r.VM = *info
		r.BootConfig = bootCfg
		r.FirstBooted = true
		if blobIDs != nil {
			r.ImageBlobIDs = blobIDs
		}
		return nil
	})
}

func (b *Backend) HandleStopResult(ctx context.Context, id, runDir string, runtimeFiles []string, shutdownErr error) error {
	if shutdownErr != nil && !errors.Is(shutdownErr, ErrNotRunning) {
		b.MarkError(ctx, id)
		return shutdownErr
	}
	CleanupRuntimeFiles(ctx, runDir, runtimeFiles)
	return nil
}

// killOrphanProcess terminates a leftover hypervisor process if the PID
// file exists and the process matches the expected binary name.
func (b *Backend) killOrphanProcess(ctx context.Context, runDir string) {
	pid, err := utils.ReadPIDFile(b.PIDFilePath(runDir))
	if err != nil {
		return
	}
	sockPath := SocketPath(runDir)
	if !utils.VerifyProcessCmdline(pid, b.Conf.BinaryName(), sockPath) {
		return
	}
	_ = utils.TerminateProcess(ctx, pid, b.Conf.BinaryName(), sockPath, b.Conf.TerminateGracePeriod())
}

func SocketPath(runDir string) string { return filepath.Join(runDir, APISocketName) }

func ConsoleSockPath(runDir string) string { return filepath.Join(runDir, ConsoleSockName) }

type LaunchSpec struct {
	Cmd       *exec.Cmd
	PIDPath   string
	SockPath  string
	NetnsPath string // empty = host netns
	OnFail    func() // optional cleanup on any error path
}

// LaunchVMProcess starts spec.Cmd and waits for the API socket. On any error
// after Start, the process is killed and the PID file is removed. Caller
// reaps cmd via cmd.Wait() in a goroutine on success.
func (b *Backend) LaunchVMProcess(ctx context.Context, spec LaunchSpec) (pid int, err error) {
	started := false
	pidWritten := false
	binaryName := b.Conf.BinaryName()
	defer func() {
		if err == nil {
			return
		}
		if started {
			_ = spec.Cmd.Process.Kill()
			_ = spec.Cmd.Wait()
		}
		if pidWritten {
			_ = os.Remove(spec.PIDPath)
		}
		if spec.OnFail != nil {
			spec.OnFail()
		}
	}()

	if spec.NetnsPath != "" {
		restore, nsErr := EnterNetns(spec.NetnsPath)
		if nsErr != nil {
			return 0, fmt.Errorf("enter netns: %w", nsErr)
		}
		defer restore()
	}

	if err = spec.Cmd.Start(); err != nil {
		return 0, fmt.Errorf("exec %s: %w", binaryName, err)
	}
	started = true
	pid = spec.Cmd.Process.Pid

	if err = utils.WritePIDFile(spec.PIDPath, pid); err != nil {
		return 0, fmt.Errorf("write PID file: %w", err)
	}
	pidWritten = true

	if err = WaitForSocket(ctx, spec.SockPath, pid, b.Conf.SocketWaitTimeout(), binaryName); err != nil {
		return 0, err
	}
	return pid, nil
}

// PrepareStagingDir extracts the snapshot tar into a sibling staging dir.
func PrepareStagingDir(runDir string, snapshot io.Reader) (stagingDir string, cleanup func(), err error) {
	stagingDir = runDir + ".restore-staging"
	if err = os.RemoveAll(stagingDir); err != nil {
		return "", nil, fmt.Errorf("clear staging dir: %w", err)
	}
	if err = os.MkdirAll(stagingDir, 0o700); err != nil {
		return "", nil, fmt.Errorf("create staging dir: %w", err)
	}
	cleanup = func() { os.RemoveAll(stagingDir) } //nolint:errcheck,gosec
	if err = utils.ExtractTar(stagingDir, snapshot); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("extract snapshot: %w", err)
	}
	return stagingDir, cleanup, nil
}

type RestoreSpec struct {
	VMCfg         *types.VMConfig
	Snapshot      io.Reader
	OverrideCheck func(rec *VMRecord, vmCfg *types.VMConfig) error
	Preflight     func(stagingDir string, rec *VMRecord) error
	Kill          func(ctx context.Context, vmID string, rec *VMRecord) error
	Wrap          func(rec *VMRecord, fn func() error) error // optional disk lock around merge+afterExtract
	BeforeMerge   func(rec *VMRecord) error                  // e.g. FC removes stale COW
	AfterExtract  func(ctx context.Context, vmID string, vmCfg *types.VMConfig, rec *VMRecord) (*types.VM, error)
}

// DirectRestoreSpec is RestoreSpec for a local srcDir rather than a tar; Populate replaces staging+merge.
type DirectRestoreSpec struct {
	VMCfg         *types.VMConfig
	SrcDir        string
	OverrideCheck func(rec *VMRecord, vmCfg *types.VMConfig) error
	Preflight     func(srcDir string, rec *VMRecord) error
	Kill          func(ctx context.Context, vmID string, rec *VMRecord) error
	Wrap          func(rec *VMRecord, fn func() error) error
	Populate      func(rec *VMRecord, srcDir string) error
	AfterExtract  func(ctx context.Context, vmID string, vmCfg *types.VMConfig, rec *VMRecord) (*types.VM, error)
}

// RestoreSequence: validate CPU → resolve → stage → preflight → kill → merge → AfterExtract.
func (b *Backend) RestoreSequence(ctx context.Context, vmRef string, spec RestoreSpec) (*types.VM, error) {
	if err := ValidateHostCPU(spec.VMCfg.CPU); err != nil {
		return nil, err
	}
	vmID, rec, err := b.ResolveForRestore(ctx, vmRef)
	if err != nil {
		return nil, err
	}
	if spec.OverrideCheck != nil {
		if checkErr := spec.OverrideCheck(rec, spec.VMCfg); checkErr != nil {
			return nil, checkErr
		}
	}

	stagingDir, cleanupStaging, err := PrepareStagingDir(rec.RunDir, spec.Snapshot)
	if err != nil {
		return nil, err
	}
	defer cleanupStaging()

	if preflightErr := spec.Preflight(stagingDir, rec); preflightErr != nil {
		return nil, fmt.Errorf("snapshot preflight: %w", preflightErr)
	}
	if killErr := spec.Kill(ctx, vmID, rec); killErr != nil {
		return nil, killErr
	}

	var result *types.VM
	inner := func() error {
		if spec.BeforeMerge != nil {
			if err := spec.BeforeMerge(rec); err != nil {
				return err
			}
		}
		if mergeErr := MergeDirInto(stagingDir, rec.RunDir); mergeErr != nil {
			b.MarkError(ctx, vmID)
			return fmt.Errorf("apply staged snapshot: %w", mergeErr)
		}
		var afterErr error
		result, afterErr = spec.AfterExtract(ctx, vmID, spec.VMCfg, rec)
		return afterErr
	}
	if spec.Wrap != nil {
		if err := spec.Wrap(rec, inner); err != nil {
			return nil, err
		}
	} else if err := inner(); err != nil {
		return nil, err
	}
	return result, nil
}

// DirectRestoreSequence: like RestoreSequence but Populate replaces staging+merge.
func (b *Backend) DirectRestoreSequence(ctx context.Context, vmRef string, spec DirectRestoreSpec) (*types.VM, error) {
	if err := ValidateHostCPU(spec.VMCfg.CPU); err != nil {
		return nil, err
	}
	vmID, rec, err := b.ResolveForRestore(ctx, vmRef)
	if err != nil {
		return nil, err
	}
	if spec.OverrideCheck != nil {
		if checkErr := spec.OverrideCheck(rec, spec.VMCfg); checkErr != nil {
			return nil, checkErr
		}
	}

	if preflightErr := spec.Preflight(spec.SrcDir, rec); preflightErr != nil {
		return nil, fmt.Errorf("snapshot preflight: %w", preflightErr)
	}
	if killErr := spec.Kill(ctx, vmID, rec); killErr != nil {
		return nil, killErr
	}

	var result *types.VM
	inner := func() error {
		if populateErr := spec.Populate(rec, spec.SrcDir); populateErr != nil {
			b.MarkError(ctx, vmID)
			return populateErr
		}
		var afterErr error
		result, afterErr = spec.AfterExtract(ctx, vmID, spec.VMCfg, rec)
		return afterErr
	}
	if spec.Wrap != nil {
		if wrapErr := spec.Wrap(rec, inner); wrapErr != nil {
			return nil, wrapErr
		}
	} else if innerErr := inner(); innerErr != nil {
		return nil, innerErr
	}
	return result, nil
}
