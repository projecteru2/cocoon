package vm

import (
	"github.com/spf13/cobra"

	cmdcore "github.com/cocoonstack/cocoon/cmd/core"
)

// Actions defines VM lifecycle operations.
type Actions interface {
	Create(cmd *cobra.Command, args []string) error
	Run(cmd *cobra.Command, args []string) error
	Clone(cmd *cobra.Command, args []string) error
	Start(cmd *cobra.Command, args []string) error
	Stop(cmd *cobra.Command, args []string) error
	List(cmd *cobra.Command, args []string) error
	Inspect(cmd *cobra.Command, args []string) error
	Console(cmd *cobra.Command, args []string) error
	RM(cmd *cobra.Command, args []string) error
	Restore(cmd *cobra.Command, args []string) error
	Debug(cmd *cobra.Command, args []string) error
	Status(cmd *cobra.Command, args []string) error
	FsAttach(cmd *cobra.Command, args []string) error
	FsDetach(cmd *cobra.Command, args []string) error
	DeviceAttach(cmd *cobra.Command, args []string) error
	DeviceDetach(cmd *cobra.Command, args []string) error
}

// Command builds the "vm" parent command with all subcommands.
func Command(h Actions) *cobra.Command {
	vmCmd := &cobra.Command{
		Use:   "vm",
		Short: "Manage virtual machines",
	}

	createCmd := &cobra.Command{
		Use:   "create [flags] IMAGE",
		Short: "Create a VM from an image",
		Args:  cobra.ExactArgs(1),
		RunE:  h.Create,
	}
	addVMFlags(createCmd)
	cmdcore.AddOutputFlag(createCmd)

	runCmd := &cobra.Command{
		Use:   "run [flags] IMAGE",
		Short: "Create and start a VM from an image",
		Args:  cobra.ExactArgs(1),
		RunE:  h.Run,
	}
	addVMFlags(runCmd)
	cmdcore.AddOutputFlag(runCmd)

	cloneCmd := &cobra.Command{
		Use:   "clone [flags] SNAPSHOT",
		Short: "Clone a new VM from a snapshot",
		Args:  cobra.ExactArgs(1),
		RunE:  h.Clone,
	}
	addCloneFlags(cloneCmd)
	cmdcore.AddOutputFlag(cloneCmd)

	startCmd := &cobra.Command{
		Use:   "start VM [VM...]",
		Short: "Start created/stopped VM(s)",
		Args:  cobra.MinimumNArgs(1),
		RunE:  h.Start,
	}
	cmdcore.AddOutputFlag(startCmd)

	stopCmd := &cobra.Command{
		Use:   "stop VM [VM...]",
		Short: "Stop running VM(s)",
		Args:  cobra.MinimumNArgs(1),
		RunE:  h.Stop,
	}
	stopCmd.Flags().Bool("force", false, "force stop (skip graceful shutdown, immediate SIGTERM/SIGKILL)")
	stopCmd.Flags().Int("timeout", 0, "ACPI shutdown timeout in seconds (0 = use config default)")
	cmdcore.AddOutputFlag(stopCmd)

	listCmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List VMs with status",
		RunE:    h.List,
	}
	cmdcore.AddFormatFlag(listCmd)

	inspectCmd := &cobra.Command{
		Use:   "inspect VM",
		Short: "Show detailed VM info (JSON)",
		Args:  cobra.ExactArgs(1),
		RunE:  h.Inspect,
	}

	consoleCmd := &cobra.Command{
		Use:   "console VM",
		Short: "Attach interactive console to a running VM",
		Args:  cobra.ExactArgs(1),
		RunE:  h.Console,
	}
	consoleCmd.Flags().String("escape-char", "^]", "escape character (single char or ^X caret notation)")

	rmCmd := &cobra.Command{
		Use:   "rm [flags] VM [VM...]",
		Short: "Delete VM(s) (--force to stop running VMs first)",
		Args:  cobra.MinimumNArgs(1),
		RunE:  h.RM,
	}
	rmCmd.Flags().Bool("force", false, "force delete running VMs")
	cmdcore.AddOutputFlag(rmCmd)

	restoreCmd := &cobra.Command{
		Use:   "restore [flags] VM SNAPSHOT",
		Short: "Restore a running VM to a previous snapshot",
		Args:  cobra.ExactArgs(2),
		RunE:  h.Restore,
	}
	restoreCmd.Flags().Int("cpu", 0, "boot CPUs (0 = keep current)")
	restoreCmd.Flags().String("memory", "", "memory size (empty = keep current)")
	restoreCmd.Flags().String("storage", "", "COW disk size (empty = keep current)")
	restoreCmd.Flags().Bool("on-demand", false, "use UFFD on-demand memory loading for faster restore (CH only; snapshot file must remain on disk)")
	cmdcore.AddOutputFlag(restoreCmd)

	debugCmd := &cobra.Command{
		Use:   "debug [flags] IMAGE",
		Short: "Generate hypervisor launch command (dry run)",
		Args:  cobra.ExactArgs(1),
		RunE:  h.Debug,
	}
	addVMFlags(debugCmd)
	debugCmd.Flags().Int("max-cpu", 8, "max CPUs")           //nolint:mnd
	debugCmd.Flags().Int("balloon", 0, "balloon size in MB") //nolint:mnd
	debugCmd.Flags().String("cow", "", "COW disk path")
	debugCmd.Flags().String("ch", "cloud-hypervisor", "cloud-hypervisor binary path")

	statusCmd := &cobra.Command{
		Use:   "status [VM...]",
		Short: "Watch VM status in real time",
		RunE:  h.Status,
	}
	statusCmd.Flags().IntP("interval", "n", 5, "poll interval in seconds") //nolint:mnd
	statusCmd.Flags().Bool("event", false, "event stream mode (append changes instead of refreshing)")
	statusCmd.Flags().String("format", "", "output format: json (event mode only)")

	vmCmd.AddCommand(
		createCmd,
		runCmd,
		cloneCmd,
		startCmd,
		stopCmd,
		listCmd,
		inspectCmd,
		consoleCmd,
		rmCmd,
		restoreCmd,
		debugCmd,
		statusCmd,
		buildFsCommand(h),
		buildDeviceCommand(h),
	)
	return vmCmd
}

func buildFsCommand(h Actions) *cobra.Command {
	fsCmd := &cobra.Command{
		Use:   "fs",
		Short: "Attach/detach a vhost-user-fs share to a running VM (CH only)",
	}

	attachCmd := &cobra.Command{
		Use:   "attach VM",
		Short: "Attach a vhost-user-fs device to a running VM",
		Args:  cobra.ExactArgs(1),
		RunE:  h.FsAttach,
	}
	attachCmd.Flags().String("socket", "", "absolute path to a virtiofsd unix socket (required)")
	attachCmd.Flags().String("tag", "", "guest mount tag (required; also detach key)")
	attachCmd.Flags().Int("num-queues", 0, "request queues (0 = default 1)")
	attachCmd.Flags().Int("queue-size", 0, "queue depth (0 = default 1024)") //nolint:mnd
	_ = attachCmd.MarkFlagRequired("socket")
	_ = attachCmd.MarkFlagRequired("tag")
	cmdcore.AddOutputFlag(attachCmd)

	detachCmd := &cobra.Command{
		Use:   "detach VM",
		Short: "Detach a vhost-user-fs device from a running VM",
		Args:  cobra.ExactArgs(1),
		RunE:  h.FsDetach,
	}
	detachCmd.Flags().String("tag", "", "guest mount tag (required)")
	_ = detachCmd.MarkFlagRequired("tag")
	cmdcore.AddOutputFlag(detachCmd)

	fsCmd.AddCommand(attachCmd, detachCmd)
	return fsCmd
}

func buildDeviceCommand(h Actions) *cobra.Command {
	devCmd := &cobra.Command{
		Use:   "device",
		Short: "Attach/detach a VFIO PCI passthrough device to a running VM (CH only)",
	}

	attachCmd := &cobra.Command{
		Use:   "attach VM",
		Short: "Attach a VFIO PCI device to a running VM",
		Args:  cobra.ExactArgs(1),
		RunE:  h.DeviceAttach,
	}
	attachCmd.Flags().String("pci", "", "BDF (01:00.0 / 0000:01:00.0) or sysfs path /sys/bus/pci/devices/<bdf>")
	attachCmd.Flags().String("id", "", "optional device id; CH auto-generates if empty (must not start with cocoon-)")
	_ = attachCmd.MarkFlagRequired("pci")
	cmdcore.AddOutputFlag(attachCmd)

	detachCmd := &cobra.Command{
		Use:   "detach VM",
		Short: "Detach a VFIO PCI device from a running VM",
		Args:  cobra.ExactArgs(1),
		RunE:  h.DeviceDetach,
	}
	detachCmd.Flags().String("id", "", "device id returned by attach (required)")
	_ = detachCmd.MarkFlagRequired("id")
	cmdcore.AddOutputFlag(detachCmd)

	devCmd.AddCommand(attachCmd, detachCmd)
	return devCmd
}

func addVMFlags(cmd *cobra.Command) {
	cmd.Flags().Bool("fc", false, "use Firecracker backend instead of Cloud Hypervisor (OCI images only)")
	cmd.Flags().String("name", "", "VM name")
	cmd.Flags().Int("cpu", 2, "boot CPUs")                //nolint:mnd
	cmd.Flags().String("memory", "1G", "memory size")     //nolint:mnd
	cmd.Flags().String("storage", "10G", "COW disk size") //nolint:mnd
	cmd.Flags().Int("nics", 1, "number of network interfaces (0 = no network); multiple NICs with auto IP config only works for cloudimg; OCI images auto-configure only the last NIC, others require manual setup inside the guest")
	cmd.Flags().Int("queue-size", 0, "virtio-net ring depth per queue (0 = default 512; tradeoff: larger improves download throughput, smaller improves RPC latency)") //nolint:mnd
	cmd.Flags().Int("disk-queue-size", 0, "virtio-blk ring depth per device (0 = default 512; CH only, ignored by FC)")                                                //nolint:mnd
	cmd.Flags().String("network", "", "CNI conflist name (empty = default); mutually exclusive with --bridge")
	cmd.Flags().String("bridge", "", "use TAP-on-bridge instead of CNI (value is bridge device, e.g. cni0); VM gets IP via DHCP from the bridge")
	cmd.Flags().String("user", "root", "guest username for cloud-init (cloudimg only)")
	cmd.Flags().String("password", "cocoon", "guest password for cloud-init (cloudimg only)")
	cmd.Flags().Bool("no-direct-io", false, "disable O_DIRECT on writable disks (use page cache instead; CH only)")
	cmd.Flags().Bool("windows", false, "Windows guest (UEFI boot, kvm_hyperv=on, no cidata)")
	cmd.Flags().Bool("shared-memory", false, "enable CH memory shared=on; required to attach vhost-user-fs later (CH only, fixed for VM lifetime)")
	cmd.Flags().StringArray("data-disk", nil, "extra data disk: size=20G[,name=...][,fstype=ext4|none][,mount=/mnt/x][,directio=on|off|auto]; repeatable")
}

func addCloneFlags(cmd *cobra.Command) {
	cmd.Flags().String("name", "", "VM name (default: cocoon-clone-<id>)")
	cmd.Flags().Int("cpu", 0, "boot CPUs (0 = inherit from snapshot)")
	cmd.Flags().String("memory", "", "memory size (empty = inherit from snapshot)")
	cmd.Flags().String("storage", "", "COW disk size (empty = inherit from snapshot)")
	cmd.Flags().Int("nics", 0, "number of NICs (0 = inherit from snapshot)")
	cmd.Flags().Int("queue-size", 0, "virtio-net ring depth per queue (0 = inherit from snapshot)")       //nolint:mnd
	cmd.Flags().Int("disk-queue-size", 0, "virtio-blk ring depth per device (0 = inherit from snapshot)") //nolint:mnd
	cmd.Flags().String("network", "", "CNI conflist name (empty = inherit from source VM)")
	cmd.Flags().String("bridge", "", "use TAP-on-bridge instead of CNI (value is bridge device, e.g. cni0)")
	cmd.Flags().Bool("no-direct-io", false, "disable O_DIRECT on writable disks (inherit from snapshot if not set)")
	cmd.Flags().Bool("on-demand", false, "use UFFD on-demand memory loading for faster clone (CH only; snapshot file must remain on disk)")
	cmd.Flags().Bool("pull", false, "auto-pull base image if not found locally (for cross-node clone)")
}
