package images

import "github.com/spf13/cobra"

// Handler organizes image-related subcommands by image interface semantics.
type Actions interface {
	Pull(cmd *cobra.Command, args []string) error
	List(cmd *cobra.Command, args []string) error
	Delete(cmd *cobra.Command, args []string) error
}

// Commands builds image command set.
func Commands(h Actions) []*cobra.Command {
	return []*cobra.Command{
		{
			Use:   "pull IMAGE [IMAGE...]",
			Short: "Pull OCI image(s) or cloud image URL(s)",
			Args:  cobra.MinimumNArgs(1),
			RunE:  h.Pull,
		},
		{
			Use:     "list",
			Aliases: []string{"ls"},
			Short:   "List locally stored images (all backends)",
			RunE:    h.List,
		},
		{
			Use:   "delete ID [ID...]",
			Short: "Delete locally stored image(s)",
			Args:  cobra.MinimumNArgs(1),
			RunE:  h.Delete,
		},
	}
}
