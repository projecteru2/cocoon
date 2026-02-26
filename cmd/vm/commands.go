package vm

import "github.com/spf13/cobra"

// Actions defines VM lifecycle operations.
type Actions interface {
	Create(cmd *cobra.Command, args []string) error
	Run(cmd *cobra.Command, args []string) error
	Start(cmd *cobra.Command, args []string) error
	Stop(cmd *cobra.Command, args []string) error
	List(cmd *cobra.Command, args []string) error
	Inspect(cmd *cobra.Command, args []string) error
	Console(cmd *cobra.Command, args []string) error
	RM(cmd *cobra.Command, args []string) error
	Debug(cmd *cobra.Command, args []string) error
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

	runCmd := &cobra.Command{
		Use:   "run [flags] IMAGE",
		Short: "Create and start a VM from an image",
		Args:  cobra.ExactArgs(1),
		RunE:  h.Run,
	}
	addVMFlags(runCmd)

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

	listCmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List VMs with status",
		RunE:    h.List,
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

	debugCmd := &cobra.Command{
		Use:   "debug [flags] IMAGE",
		Short: "Generate cloud-hypervisor launch command (dry run)",
		Args:  cobra.ExactArgs(1),
		RunE:  h.Debug,
	}
	addVMFlags(debugCmd)
	debugCmd.Flags().Int("max-cpu", 8, "max CPUs")           //nolint:mnd
	debugCmd.Flags().Int("balloon", 0, "balloon size in MB") //nolint:mnd
	debugCmd.Flags().String("cow", "", "COW disk path")
	debugCmd.Flags().String("ch", "cloud-hypervisor", "cloud-hypervisor binary path")

	vmCmd.AddCommand(
		createCmd,
		runCmd,
		startCmd,
		stopCmd,
		listCmd,
		inspectCmd,
		consoleCmd,
		rmCmd,
		debugCmd,
	)
	return vmCmd
}

func addVMFlags(cmd *cobra.Command) {
	cmd.Flags().String("name", "", "VM name")
	cmd.Flags().Int("cpu", 2, "boot CPUs")                //nolint:mnd
	cmd.Flags().String("memory", "1G", "memory size")     //nolint:mnd
	cmd.Flags().String("storage", "10G", "COW disk size") //nolint:mnd
	cmd.Flags().Int("nics", 1, "number of network interfaces (0 = no network)")
}
