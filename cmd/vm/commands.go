package vm

import "github.com/spf13/cobra"

// Handler organizes VM lifecycle subcommands by hypervisor interface semantics.
type Actions interface {
	Create(cmd *cobra.Command, args []string) error
	Run(cmd *cobra.Command, args []string) error
	Start(cmd *cobra.Command, args []string) error
	Stop(cmd *cobra.Command, args []string) error
	PS(cmd *cobra.Command, args []string) error
	Inspect(cmd *cobra.Command, args []string) error
	Console(cmd *cobra.Command, args []string) error
	RM(cmd *cobra.Command, args []string) error
	DryRun(cmd *cobra.Command, args []string) error
}

// Commands builds VM/hypervisor command set.
func Commands(h Actions) []*cobra.Command {
	createCmd := &cobra.Command{
		Use:   "create [flags] IMAGE",
		Short: "Create a VM from an image",
		Args:  cobra.ExactArgs(1),
		RunE:  h.Create,
	}
	createCmd.Flags().String("name", "", "VM name")
	createCmd.Flags().Int("cpu", 2, "boot CPUs")                //nolint:mnd
	createCmd.Flags().String("memory", "1G", "memory size")     //nolint:mnd
	createCmd.Flags().String("storage", "10G", "COW disk size") //nolint:mnd

	runCmd := &cobra.Command{
		Use:   "run [flags] IMAGE",
		Short: "Create and start a VM from an image",
		Args:  cobra.ExactArgs(1),
		RunE:  h.Run,
	}
	runCmd.Flags().String("name", "", "VM name")
	runCmd.Flags().Int("cpu", 2, "boot CPUs")                //nolint:mnd
	runCmd.Flags().String("memory", "1G", "memory size")     //nolint:mnd
	runCmd.Flags().String("storage", "10G", "COW disk size") //nolint:mnd

	startCmd := &cobra.Command{
		Use:   "start VM [VM...]",
		Short: "Start created/stopped VM(s)",
		Args:  cobra.MinimumNArgs(1),
		RunE:  h.Start,
	}

	stopCmd := &cobra.Command{
		Use:   "stop VM [VM...]",
		Short: "Stop running VM(s)",
		Args:  cobra.MinimumNArgs(1),
		RunE:  h.Stop,
	}

	psCmd := &cobra.Command{
		Use:   "ps",
		Short: "List VMs with status",
		RunE:  h.PS,
	}

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

	dryrunCmd := &cobra.Command{
		Use:   "dryrun [flags] IMAGE",
		Short: "Generate cloud-hypervisor launch command (dry run)",
		Args:  cobra.ExactArgs(1),
		RunE:  h.DryRun,
	}
	dryrunCmd.Flags().String("name", "cocoon-vm", "VM name")
	dryrunCmd.Flags().Int("cpu", 2, "boot CPUs")              //nolint:mnd
	dryrunCmd.Flags().Int("max-cpu", 8, "max CPUs")           //nolint:mnd
	dryrunCmd.Flags().Int("memory", 1024, "memory in MB")     //nolint:mnd
	dryrunCmd.Flags().Int("balloon", 0, "balloon size in MB") //nolint:mnd
	dryrunCmd.Flags().Int("storage", 10, "COW disk size in GB")
	dryrunCmd.Flags().String("cow", "", "COW disk path")
	dryrunCmd.Flags().String("ch", "cloud-hypervisor", "cloud-hypervisor binary path")

	return []*cobra.Command{
		dryrunCmd,
		runCmd,
		createCmd,
		startCmd,
		stopCmd,
		psCmd,
		inspectCmd,
		consoleCmd,
		rmCmd,
	}
}
