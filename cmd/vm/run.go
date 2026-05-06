package vm

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/projecteru2/core/log"
	"github.com/spf13/cobra"

	cmdcore "github.com/cocoonstack/cocoon/cmd/core"
	"github.com/cocoonstack/cocoon/config"
	"github.com/cocoonstack/cocoon/hypervisor"
	"github.com/cocoonstack/cocoon/network"
	"github.com/cocoonstack/cocoon/snapshot"
	"github.com/cocoonstack/cocoon/types"
	"github.com/cocoonstack/cocoon/utils"
)

type nicHint struct {
	mac, ip, gw string
	prefix      int
}

func (h Handler) Create(cmd *cobra.Command, args []string) error {
	ctx, vm, _, err := h.createVM(cmd, args[0])
	if err != nil {
		return err
	}
	if done, jsonErr := cmdcore.MaybeOutputJSON(cmd, vm); done {
		return jsonErr
	}
	logger := log.WithFunc("cmd.vm.create")
	logger.Infof(ctx, "VM created: %s (name: %s, state: %s)", vm.ID, vm.Config.Name, vm.State)
	logger.Infof(ctx, "start with: cocoon vm start %s", vm.ID)
	return nil
}

func (h Handler) Run(cmd *cobra.Command, args []string) error {
	ctx, vm, hyper, err := h.createVM(cmd, args[0])
	if err != nil {
		return err
	}
	logger := log.WithFunc("cmd.vm.run")
	wantJSON := cmdcore.WantJSON(cmd)
	if !wantJSON {
		logger.Infof(ctx, "VM created: %s (name: %s)", vm.ID, vm.Config.Name)
	}

	started, err := hyper.Start(ctx, []string{vm.ID})
	if err != nil {
		return fmt.Errorf("start VM %s: %w", vm.ID, err)
	}
	if wantJSON {
		// Re-inspect to capture post-start state (running, PID, IP).
		// On failure, fall back to pre-start vm and surface the reason so the
		// caller doesn't silently consume stale JSON.
		info, inspectErr := hyper.Inspect(ctx, vm.ID)
		switch {
		case inspectErr != nil:
			logger.Warnf(ctx, "inspect after start failed: %v (json payload may be stale)", inspectErr)
		case info != nil:
			vm = info
		}
		return cmdcore.OutputJSON(vm)
	}
	for _, id := range started {
		logger.Infof(ctx, "started: %s", id)
	}
	return nil
}

func (h Handler) Clone(cmd *cobra.Command, args []string) error {
	ctx, conf, err := h.Init(cmd)
	if err != nil {
		return err
	}
	logger := log.WithFunc("cmd.vm.clone")

	fromDir, snapRef, err := snapshotSource(cmd, args, 0)
	if err != nil {
		return err
	}
	if fromDir != "" {
		return h.cloneFromDir(ctx, cmd, conf, fromDir, logger)
	}

	snapBackend, err := cmdcore.InitSnapshot(conf)
	if err != nil {
		return err
	}

	// Infer hypervisor backend from the snapshot's Hypervisor field.
	snapInfo, err := snapBackend.Inspect(ctx, snapRef)
	if err != nil {
		return fmt.Errorf("inspect snapshot %s: %w", snapRef, err)
	}
	if snapInfo.Hypervisor != "" {
		conf.UseFirecracker = snapInfo.Hypervisor == string(config.HypervisorFirecracker)
	}

	hyper, err := cmdcore.InitHypervisor(conf)
	if err != nil {
		return err
	}

	if da, ok := snapBackend.(snapshot.Direct); ok {
		if dcr, ok := hyper.(hypervisor.Direct); ok {
			return h.cloneDirect(ctx, cmd, conf, dcr, da, snapRef, logger)
		}
	}

	cfg, stream, err := snapBackend.Restore(ctx, snapRef)
	if err != nil {
		return fmt.Errorf("open snapshot %s: %w", snapRef, err)
	}
	defer stream.Close() //nolint:errcheck

	stop := context.AfterFunc(ctx, func() {
		stream.Close() //nolint:errcheck,gosec
	})
	defer stop()

	vmCfg, vmID, netProvider, networkConfigs, err := h.prepareClone(ctx, cmd, conf, cfg)
	if err != nil {
		return err
	}

	logger.Infof(ctx, "cloning VM from snapshot %s ...", snapRef)

	vm, cloneErr := hyper.Clone(ctx, vmID, vmCfg, networkConfigs, &cfg, stream)
	if cloneErr != nil {
		rollbackNetwork(ctx, netProvider, vmID)
		return fmt.Errorf("clone VM: %w", cloneErr)
	}

	if done, jsonErr := cmdcore.MaybeOutputJSON(cmd, vm); done {
		return jsonErr
	}
	logger.Infof(ctx, "VM cloned: %s (name: %s)", vm.ID, vm.Config.Name)
	printPostCloneHints(vm, networkConfigs)
	return nil
}

func (h Handler) Restore(cmd *cobra.Command, args []string) error {
	ctx, conf, err := h.Init(cmd)
	if err != nil {
		return err
	}
	logger := log.WithFunc("cmd.vm.restore")

	vmRef := args[0]
	fromDir, snapRef, err := snapshotSource(cmd, args, 1)
	if err != nil {
		return err
	}
	if fromDir != "" {
		return h.restoreFromDir(ctx, cmd, conf, vmRef, fromDir, logger)
	}

	hyper, err := cmdcore.FindHypervisor(ctx, conf, vmRef)
	if err != nil {
		return fmt.Errorf("find VM %s: %w", vmRef, err)
	}
	snapBackend, err := cmdcore.InitSnapshot(conf)
	if err != nil {
		return err
	}

	vm, err := hyper.Inspect(ctx, vmRef)
	if err != nil {
		return fmt.Errorf("inspect VM: %w", err)
	}
	snapInfo, err := snapBackend.Inspect(ctx, snapRef)
	if err != nil {
		return fmt.Errorf("inspect snapshot: %w", err)
	}
	if _, ok := vm.SnapshotIDs[snapInfo.ID]; !ok {
		return fmt.Errorf("snapshot %s does not belong to VM %s", snapRef, vmRef)
	}

	vmCfg, err := cmdcore.RestoreVMConfigFromFlags(cmd, vm, snapInfo.SnapshotConfig)
	if err != nil {
		return err
	}

	done, directErr := h.restoreDirect(ctx, cmd, snapRef, vmRef, vmCfg, snapBackend, hyper, logger)
	if done {
		return directErr
	}

	_, stream, err := snapBackend.Restore(ctx, snapRef)
	if err != nil {
		return fmt.Errorf("open snapshot: %w", err)
	}
	defer stream.Close() //nolint:errcheck

	stop := context.AfterFunc(ctx, func() {
		stream.Close() //nolint:errcheck,gosec
	})
	defer stop()

	logger.Infof(ctx, "restoring VM %s from snapshot %s ...", vmRef, snapRef)

	result, err := hyper.Restore(ctx, vmRef, vmCfg, stream)
	if err != nil {
		return fmt.Errorf("restore: %w", err)
	}

	if done, jsonErr := cmdcore.MaybeOutputJSON(cmd, result); done {
		return jsonErr
	}
	logger.Infof(ctx, "VM %s restored (state: %s)", result.ID, result.State)
	return nil
}

// restoreFromDir runs DirectRestore over an envelope-bearing dir. The
// envelope's snapshot ID is gated against vm.SnapshotIDs; a foreign ID
// requires --force so "overwrite VM with unrelated lineage" is opt-in.
func (h Handler) restoreFromDir(ctx context.Context, cmd *cobra.Command, conf *config.Config, vmRef, dir string, logger *log.Fields) error {
	cfg, err := snapshot.ReadSnapshotEnvelope(dir)
	if err != nil {
		return fmt.Errorf("load envelope: %w", err)
	}
	hyper, err := cmdcore.FindHypervisor(ctx, conf, vmRef)
	if err != nil {
		return fmt.Errorf("find VM %s: %w", vmRef, err)
	}
	dcr, ok := hyper.(hypervisor.Direct)
	if !ok {
		return fmt.Errorf("backend %s does not support direct restore", hyper.Type())
	}
	vm, err := hyper.Inspect(ctx, vmRef)
	if err != nil {
		return fmt.Errorf("inspect VM: %w", err)
	}
	if _, owned := vm.SnapshotIDs[cfg.ID]; !owned {
		force, _ := cmd.Flags().GetBool("force")
		if !force {
			return fmt.Errorf("snapshot envelope id %s does not belong to VM %s; pass --force to override", cfg.ID, vmRef)
		}
		logger.Warnf(ctx, "snapshot envelope id %s does not belong to VM %s; --force in effect", cfg.ID, vmRef)
	}
	vmCfg, err := cmdcore.RestoreVMConfigFromFlags(cmd, vm, cfg)
	if err != nil {
		return err
	}
	return h.runDirectRestore(ctx, cmd, dcr, vmRef, vmCfg, dir,
		fmt.Sprintf("dir %s", dir), logger)
}

func (h Handler) cloneDirect(ctx context.Context, cmd *cobra.Command, conf *config.Config, dcr hypervisor.Direct, da snapshot.Direct, snapRef string, logger *log.Fields) error {
	dataDir, cfg, err := da.DataDir(ctx, snapRef)
	if err != nil {
		return fmt.Errorf("open snapshot %s: %w", snapRef, err)
	}
	return h.cloneFromSrcDir(ctx, cmd, conf, dcr, cfg, dataDir,
		fmt.Sprintf("snapshot %s (direct)", snapRef), logger)
}

// cloneFromDir runs DirectClone over an envelope-bearing dir. The dir stays
// read-only across the call so concurrent clones of a golden image are safe.
func (h Handler) cloneFromDir(ctx context.Context, cmd *cobra.Command, conf *config.Config, dir string, logger *log.Fields) error {
	cfg, err := snapshot.ReadSnapshotEnvelope(dir)
	if err != nil {
		return fmt.Errorf("load envelope: %w", err)
	}
	// Local copy so flipping the backend selection doesn't leak to the caller's
	// shared *config.Config (CLI is fine, daemons embedding cocoon would notice).
	localConf := *conf
	if cfg.Hypervisor != "" {
		localConf.UseFirecracker = cfg.Hypervisor == string(config.HypervisorFirecracker)
	}
	hyper, err := cmdcore.InitHypervisor(&localConf)
	if err != nil {
		return err
	}
	dcr, ok := hyper.(hypervisor.Direct)
	if !ok {
		return fmt.Errorf("backend %s does not support direct clone", hyper.Type())
	}
	return h.cloneFromSrcDir(ctx, cmd, &localConf, dcr, cfg, dir,
		fmt.Sprintf("dir %s", dir), logger)
}

func (h Handler) cloneFromSrcDir(ctx context.Context, cmd *cobra.Command, conf *config.Config, dcr hypervisor.Direct, cfg types.SnapshotConfig, srcDir, sourceLabel string, logger *log.Fields) error {
	vmCfg, vmID, netProvider, networkConfigs, err := h.prepareClone(ctx, cmd, conf, cfg)
	if err != nil {
		return err
	}

	wantJSON := cmdcore.WantJSON(cmd)
	if !wantJSON {
		logger.Infof(ctx, "cloning VM from %s ...", sourceLabel)
	}

	vm, cloneErr := dcr.DirectClone(ctx, vmID, vmCfg, networkConfigs, &cfg, srcDir)
	if cloneErr != nil {
		rollbackNetwork(ctx, netProvider, vmID)
		return fmt.Errorf("clone VM: %w", cloneErr)
	}

	if wantJSON {
		return cmdcore.OutputJSON(vm)
	}
	logger.Infof(ctx, "VM cloned: %s (name: %s)", vm.ID, vm.Config.Name)
	printPostCloneHints(vm, networkConfigs)
	return nil
}

// snapshotSource resolves the snapshot source for clone/restore: either a
// directory via --from-dir or a positional SNAPSHOT ref. baseArgs is the
// number of leading positional args before SNAPSHOT (0 for clone, 1 for
// restore where args[0] is VM). Returns (fromDir, snapRef, err) with exactly
// one of fromDir/snapRef non-empty on success.
func snapshotSource(cmd *cobra.Command, args []string, baseArgs int) (string, string, error) {
	fromDir, _ := cmd.Flags().GetString("from-dir")
	if fromDir != "" {
		if len(args) > baseArgs {
			return "", "", fmt.Errorf("--from-dir and positional SNAPSHOT are mutually exclusive")
		}
		return fromDir, "", nil
	}
	if len(args) <= baseArgs {
		return "", "", fmt.Errorf("SNAPSHOT is required (or use --from-dir)")
	}
	return "", args[baseArgs], nil
}

func (h Handler) prepareClone(ctx context.Context, cmd *cobra.Command, conf *config.Config, cfg types.SnapshotConfig) (*types.VMConfig, string, network.Network, []*types.NetworkConfig, error) {
	vmCfg, err := cmdcore.CloneVMConfigFromFlags(cmd, cfg)
	if err != nil {
		return nil, "", nil, nil, err
	}
	vmID := utils.GenerateID()
	if vmCfg.Name == "" {
		vmCfg.Name = "cocoon-clone-" + network.VMIDPrefix(vmID)
	}
	if err = vmCfg.Validate(); err != nil {
		return nil, "", nil, nil, err
	}

	// Auto-pull base image if --pull is set (cross-node clone).
	if pull, _ := cmd.Flags().GetBool("pull"); pull && vmCfg.Image != "" && vmCfg.ImageType != "" {
		backends, initErr := cmdcore.InitImageBackends(ctx, conf)
		if initErr != nil {
			return nil, "", nil, nil, fmt.Errorf("init image backends: %w", initErr)
		}
		cmdcore.EnsureImage(ctx, backends, vmCfg)
	}

	bridgeDev, _ := cmd.Flags().GetString("bridge")
	netProvider, networkConfigs, err := initNetwork(ctx, conf, vmID, cfg.NICs, vmCfg, tapQueues(vmCfg.CPU, conf.UseFirecracker), bridgeDev)
	if err != nil {
		return nil, "", nil, nil, err
	}

	return vmCfg, vmID, netProvider, networkConfigs, nil
}

func (h Handler) restoreDirect(ctx context.Context, cmd *cobra.Command, snapRef, vmRef string, vmCfg *types.VMConfig, snapBackend snapshot.Snapshot, hyper hypervisor.Hypervisor, logger *log.Fields) (bool, error) {
	da, ok := snapBackend.(snapshot.Direct)
	if !ok {
		return false, nil
	}
	dcr, ok := hyper.(hypervisor.Direct)
	if !ok {
		return false, nil
	}
	dataDir, _, err := da.DataDir(ctx, snapRef)
	if err != nil {
		return true, fmt.Errorf("open snapshot: %w", err)
	}
	return true, h.runDirectRestore(ctx, cmd, dcr, vmRef, vmCfg, dataDir,
		fmt.Sprintf("snapshot %s", snapRef), logger)
}

// runDirectRestore is the shared tail for the snapshot-DB and --from-dir
// restore paths: log, DirectRestore, output.
func (h Handler) runDirectRestore(ctx context.Context, cmd *cobra.Command, dcr hypervisor.Direct, vmRef string, vmCfg *types.VMConfig, srcDir, sourceLabel string, logger *log.Fields) error {
	wantJSON := cmdcore.WantJSON(cmd)
	if !wantJSON {
		logger.Infof(ctx, "restoring VM %s from %s (direct) ...", vmRef, sourceLabel)
	}
	result, err := dcr.DirectRestore(ctx, vmRef, vmCfg, srcDir)
	if err != nil {
		return fmt.Errorf("restore: %w", err)
	}
	if wantJSON {
		return cmdcore.OutputJSON(result)
	}
	logger.Infof(ctx, "VM %s restored (state: %s)", result.ID, result.State)
	return nil
}

func (h Handler) createVM(cmd *cobra.Command, image string) (context.Context, *types.VM, hypervisor.Hypervisor, error) {
	ctx, conf, err := h.Init(cmd)
	if err != nil {
		return nil, nil, nil, err
	}

	if fc, _ := cmd.Flags().GetBool("fc"); fc {
		conf.UseFirecracker = true
	}

	vmCfg, err := cmdcore.VMConfigFromFlags(cmd, image)
	if err != nil {
		return nil, nil, nil, err
	}

	// Validate backend/boot-mode constraints before initializing backends.
	if conf.UseFirecracker && vmCfg.Windows {
		return nil, nil, nil, fmt.Errorf("--fc and --windows are mutually exclusive: Firecracker does not support Windows guests")
	}
	if conf.UseFirecracker && vmCfg.SharedMemory {
		return nil, nil, nil, fmt.Errorf("--fc and --shared-memory are mutually exclusive: Firecracker does not support vhost-user-fs hot-plug")
	}
	bridgeDev, _ := cmd.Flags().GetString("bridge")
	if bridgeDev != "" && vmCfg.Network != "" {
		return nil, nil, nil, fmt.Errorf("--bridge and --network are mutually exclusive")
	}

	backends, hyper, err := cmdcore.InitBackends(ctx, conf)
	if err != nil {
		return nil, nil, nil, err
	}

	storageConfigs, bootCfg, err := cmdcore.ResolveImage(ctx, backends, vmCfg)
	if err != nil {
		return nil, nil, nil, err
	}
	if vmCfg.Windows && bootCfg.KernelPath != "" {
		return nil, nil, nil, fmt.Errorf("--windows requires cloudimg (UEFI boot), got OCI direct boot image")
	}
	if conf.UseFirecracker && bootCfg.KernelPath == "" {
		return nil, nil, nil, fmt.Errorf("--fc requires OCI images (direct kernel boot): Firecracker does not support UEFI/cloudimg boot")
	}
	cmdcore.EnsureFirmwarePath(conf, bootCfg)

	vmID := utils.GenerateID()

	nics, _ := cmd.Flags().GetInt("nics")
	netProvider, networkConfigs, err := initNetwork(ctx, conf, vmID, nics, vmCfg, tapQueues(vmCfg.CPU, conf.UseFirecracker), bridgeDev)
	if err != nil {
		return nil, nil, nil, err
	}

	info, createErr := hyper.Create(ctx, vmID, vmCfg, storageConfigs, networkConfigs, bootCfg)
	if createErr != nil {
		rollbackNetwork(ctx, netProvider, vmID)
		return nil, nil, nil, fmt.Errorf("create VM: %w", createErr)
	}
	return ctx, info, hyper, nil
}

// tapQueues: FC=1, CH=CPU count.
func tapQueues(cpu int, useFC bool) int {
	if useFC {
		return 1
	}
	return cpu
}

func initNetwork(ctx context.Context, conf *config.Config, vmID string, nics int, vmCfg *types.VMConfig, queues int, bridgeDev string) (network.Network, []*types.NetworkConfig, error) {
	if nics <= 0 {
		return nil, nil, nil
	}
	var netProvider network.Network
	var err error
	if bridgeDev != "" {
		netProvider, err = cmdcore.InitBridgeNetwork(conf, bridgeDev)
	} else {
		netProvider, err = cmdcore.InitNetwork(conf)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("init network: %w", err)
	}
	// Override CPU for TAP queue count — FC uses single-queue, CH uses per-vCPU queues.
	// The network layer derives TAP queues from vmCfg.CPU.
	origCPU := vmCfg.CPU
	vmCfg.CPU = queues
	configs, err := netProvider.Config(ctx, vmID, nics, vmCfg)
	vmCfg.CPU = origCPU
	if err != nil {
		return nil, nil, fmt.Errorf("configure network: %w", err)
	}
	return netProvider, configs, nil
}

func rollbackNetwork(ctx context.Context, netProvider network.Network, vmID string) {
	if netProvider == nil {
		return
	}
	if _, delErr := netProvider.Delete(ctx, []string{vmID}); delErr != nil {
		log.WithFunc("cmd.vm.rollbackNetwork").Warnf(ctx, "rollback network for %s: %v", vmID, delErr)
	}
}

func printPostCloneHints(vm *types.VM, networkConfigs []*types.NetworkConfig) {
	if vm.Config.Windows {
		fmt.Println()
		fmt.Println("Windows clone: NICs hot-swapped with new MAC addresses.")
		fmt.Println("  DHCP networks: no action needed.")
		fmt.Println("  Static IP: configure via SAC serial console (cocoon vm console):")
		fmt.Println("    https://github.com/cloud-hypervisor/cloud-hypervisor/blob/main/docs/windows.md")
		fmt.Println()
		return
	}

	isCloudimg := vm.Config.ImageType == "cloudimg"

	fmt.Println()
	fmt.Println("Run inside the guest to finish setup:")
	fmt.Println()
	fmt.Println("  # Release memory for balloon")
	fmt.Println("  echo 3 > /proc/sys/vm/drop_caches")

	// FC clone: guest MAC is baked in vmstate (source VM's MAC).
	// Must change guest MAC before networkd config takes effect.
	if vm.Hypervisor == string(config.HypervisorFirecracker) {
		printFCMACHints(networkConfigs)
	}

	fmt.Println()
	fmt.Println("  # Clean old network configs from snapshot and write new ones (MAC-based)")
	fmt.Println("  rm -f /etc/systemd/network/10-*.network")

	if isCloudimg {
		printCloudimgNetworkHints()
	} else {
		printOCINetworkHints(vm, networkConfigs)
	}
	fmt.Println()
}

func printFCMACHints(networkConfigs []*types.NetworkConfig) {
	fmt.Println()
	fmt.Println("  # Fix guest MAC addresses (FC clone retains source VM's MAC)")
	for i, nc := range networkConfigs {
		if nc == nil || nc.MAC == "" {
			continue
		}
		fmt.Printf("  ip link set dev eth%d down\n", i)
		fmt.Printf("  ip link set dev eth%d address %s\n", i, nc.MAC)
		fmt.Printf("  ip link set dev eth%d up\n", i)
	}
}

func printCloudimgNetworkHints() {
	fmt.Println("  cloud-init clean --logs --seed --configs network && cloud-init init --local && cloud-init init")
	fmt.Println("  cloud-init modules --mode=config && systemctl restart systemd-networkd")
}

func printOCINetworkHints(vm *types.VM, networkConfigs []*types.NetworkConfig) {
	fmt.Println()
	fmt.Printf("  # Set hostname\n")
	fmt.Printf("  hostnamectl set-hostname %s\n", vm.Config.Name)

	var staticNICs []nicHint
	var dhcpMACs []string
	for _, nc := range networkConfigs {
		if nc == nil || nc.MAC == "" {
			continue
		}
		if nc.Network != nil && nc.Network.IP != "" {
			staticNICs = append(staticNICs, nicHint{
				mac:    nc.MAC,
				ip:     nc.Network.IP,
				prefix: nc.Network.Prefix,
				gw:     nc.Network.Gateway,
			})
		} else {
			dhcpMACs = append(dhcpMACs, nc.MAC)
		}
	}

	if len(staticNICs) == 0 && len(dhcpMACs) == 0 {
		return
	}

	if len(staticNICs) > 0 {
		printBashArray("macs", staticNICs, func(n nicHint) string { return n.mac })
		printBashArray("addrs", staticNICs, func(n nicHint) string { return fmt.Sprintf("%s/%d", n.ip, n.prefix) })

		hasGW := slices.ContainsFunc(staticNICs, func(n nicHint) bool { return n.gw != "" })
		if hasGW {
			printBashArray("gws", staticNICs, func(n nicHint) string { return n.gw })
		}

		fmt.Println("  for i in \"${!macs[@]}\"; do")
		fmt.Println("    f=\"/etc/systemd/network/10-${macs[$i]//:/}.network\"")
		writeNet := `    printf '[Match]\nMACAddress=` + `%s\n\n[Network]\nAddress=%s\n' "${macs[$i]}" "${addrs[$i]}" > "$f"`
		fmt.Println(writeNet)
		if hasGW {
			writeGW := `    [ -n "${gws[$i]}" ] && printf 'Gateway=` + `%s\n' "${gws[$i]}" >> "$f"`
			fmt.Println(writeGW)
		}
		fmt.Println("  done")
	}

	if len(dhcpMACs) > 0 {
		fmt.Println("  # DHCP NICs")
		for _, mac := range dhcpMACs {
			sanitized := strings.ReplaceAll(mac, ":", "")
			writeDHCP := fmt.Sprintf(`  printf '[Match]\nMACAddress=%s\n\n[Network]\nDHCP=ipv4\n'`+` > "/etc/systemd/network/10-%s.network"`, mac, sanitized)
			fmt.Println(writeDHCP)
		}
	}

	fmt.Println("  systemctl restart systemd-networkd")
}

func printBashArray(name string, nics []nicHint, field func(nicHint) string) {
	fmt.Printf("  %s=(", name)
	for i, n := range nics {
		if i > 0 {
			fmt.Print(" ")
		}
		fmt.Printf("'%s'", field(n))
	}
	fmt.Println(")")
}
