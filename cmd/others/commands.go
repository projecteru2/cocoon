package others

import "github.com/spf13/cobra"

// Handler organizes cross-cutting system subcommands.
type Actions interface {
	GC(cmd *cobra.Command, args []string) error
	Version(cmd *cobra.Command, args []string) error
}

// Commands builds system command set.
func Commands(h Actions) []*cobra.Command {
	return []*cobra.Command{
		{
			Use:   "gc",
			Short: "Remove unreferenced blobs, boot files, and VM dirs",
			RunE:  h.GC,
		},
		{
			Use:   "version",
			Short: "Show version, git revision, and build timestamp",
			RunE:  h.Version,
		},
	}
}
