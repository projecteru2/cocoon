package snapshot

import "github.com/spf13/cobra"

// Actions defines snapshot management operations.
type Actions interface {
	Save(cmd *cobra.Command, args []string) error
	List(cmd *cobra.Command, args []string) error
	Inspect(cmd *cobra.Command, args []string) error
	RM(cmd *cobra.Command, args []string) error
}

// Command builds the "snapshot" parent command with all subcommands.
func Command(h Actions) *cobra.Command {
	snapshotCmd := &cobra.Command{
		Use:   "snapshot",
		Short: "Manage VM snapshots",
	}

	saveCmd := &cobra.Command{
		Use:   "save [flags] VM",
		Short: "Create a snapshot from a running VM",
		Args:  cobra.ExactArgs(1),
		RunE:  h.Save,
	}
	saveCmd.Flags().String("name", "", "snapshot name")
	saveCmd.Flags().String("description", "", "snapshot description")

	listCmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List all snapshots",
		RunE:    h.List,
	}
	listCmd.Flags().StringP("format", "o", "table", `output format: "table" or "json"`)

	inspectCmd := &cobra.Command{
		Use:   "inspect SNAPSHOT",
		Short: "Show detailed snapshot info (JSON)",
		Args:  cobra.ExactArgs(1),
		RunE:  h.Inspect,
	}

	rmCmd := &cobra.Command{
		Use:   "rm SNAPSHOT [SNAPSHOT...]",
		Short: "Delete snapshot(s)",
		Args:  cobra.MinimumNArgs(1),
		RunE:  h.RM,
	}

	snapshotCmd.AddCommand(saveCmd, listCmd, inspectCmd, rmCmd)
	return snapshotCmd
}
