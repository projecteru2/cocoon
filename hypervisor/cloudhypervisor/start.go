package cloudhypervisor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"github.com/projecteru2/core/log"

	"github.com/cocoonstack/cocoon/hypervisor"
	"github.com/cocoonstack/cocoon/types"
)

func (ch *CloudHypervisor) Start(ctx context.Context, refs []string) ([]string, error) {
	return ch.StartAll(ctx, refs, ch.startOne)
}

func (ch *CloudHypervisor) startOne(ctx context.Context, id string) error {
	rec, err := ch.PrepareStart(ctx, id, runtimeFiles)
	if err != nil {
		return err
	}
	if rec == nil {
		return nil
	}
	if vErr := types.ValidateStorageConfigs(rec.StorageConfigs); vErr != nil {
		ch.MarkError(ctx, id)
		return fmt.Errorf("storage invariants violated: %w", vErr)
	}

	sockPath := hypervisor.SocketPath(rec.RunDir)
	consoleSock := hypervisor.ConsoleSockPath(rec.RunDir)

	vmCfg := buildVMConfig(ctx, rec, consoleSock)
	args := buildCLIArgs(vmCfg, sockPath)
	ch.saveCmdline(ctx, rec, args)

	withNetwork := len(rec.NetworkConfigs) > 0
	if _, err = ch.launchProcess(ctx, rec, sockPath, args, withNetwork); err != nil {
		ch.MarkError(ctx, id)
		return fmt.Errorf("launch VM: %w", err)
	}
	return nil
}

func (ch *CloudHypervisor) launchProcess(ctx context.Context, rec *hypervisor.VMRecord, socketPath string, args []string, withNetwork bool) (int, error) {
	processLog := filepath.Join(rec.LogDir, "cloud-hypervisor.log")
	logFile, err := os.Create(processLog) //nolint:gosec
	if err != nil {
		log.WithFunc("cloudhypervisor.launchProcess").Warnf(ctx, "create process log: %v", err)
	} else {
		defer logFile.Close() //nolint:errcheck
	}

	cmd := exec.Command(ch.conf.CHBinary, args...) //nolint:gosec
	// Setpgid so CH survives if this process exits.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if logFile != nil {
		cmd.Stdout = logFile
		cmd.Stderr = logFile
	}

	// CNI mode: enter per-VM netns before fork. Bridge mode: TAP is in host netns.
	netnsPath := ""
	if withNetwork && rec.NetworkConfigs[0].NetnsPath != "" {
		netnsPath = rec.NetworkConfigs[0].NetnsPath
	}

	pid, err := ch.LaunchVMProcess(ctx, hypervisor.LaunchSpec{
		Cmd:       cmd,
		PIDPath:   ch.PIDFilePath(rec.RunDir),
		SockPath:  socketPath,
		NetnsPath: netnsPath,
	})
	if err != nil {
		return 0, err
	}

	// Daemon mode: parent must wait() or zombie blocks IsProcessAlive on stop/delete.
	go cmd.Wait() //nolint:errcheck
	return pid, nil
}
