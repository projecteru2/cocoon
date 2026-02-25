package images

import "github.com/spf13/cobra"

// Actions defines image management operations.
type Actions interface {
	Pull(cmd *cobra.Command, args []string) error
	List(cmd *cobra.Command, args []string) error
	RM(cmd *cobra.Command, args []string) error
	Inspect(cmd *cobra.Command, args []string) error
}

// Command builds the "image" parent command with all subcommands.
func Command(h Actions) *cobra.Command {
	imageCmd := &cobra.Command{
		Use:   "image",
		Short: "Manage images",
	}
	imageCmd.AddCommand(
		&cobra.Command{
			Use:   "pull IMAGE [IMAGE...]",
			Short: "Pull OCI image(s) or cloud image URL(s)",
			Args:  cobra.MinimumNArgs(1),
			RunE:  h.Pull,
		},
		&cobra.Command{
			Use:     "list",
			Aliases: []string{"ls"},
			Short:   "List locally stored images (all backends)",
			RunE:    h.List,
		},
		&cobra.Command{
			Use:   "rm ID [ID...]",
			Short: "Delete locally stored image(s)",
			Args:    cobra.MinimumNArgs(1),
			RunE:    h.RM,
		},
		&cobra.Command{
			Use:   "inspect IMAGE",
			Short: "Show detailed image info (JSON)",
			Args:  cobra.ExactArgs(1),
			RunE:  h.Inspect,
		},
	)
	return imageCmd
}
