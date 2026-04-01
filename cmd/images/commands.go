package images

import (
	"github.com/spf13/cobra"

	cmdcore "github.com/cocoonstack/cocoon/cmd/core"
)

// Actions defines image management operations.
type Actions interface {
	Pull(cmd *cobra.Command, args []string) error
	Import(cmd *cobra.Command, args []string) error
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
	listCmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List locally stored images (all backends)",
		RunE:    h.List,
	}
	cmdcore.AddFormatFlag(listCmd)

	importCmd := &cobra.Command{
		Use:   "import NAME",
		Short: "Import local tar(s) as OCI image or qcow2(s) as cloud image",
		Long: `Import local files as a cocoon image.

File type is auto-detected from the first file:
  - qcow2 (QFI magic): files are concatenated (split reassembly) and stored as a cloud image
  - tar: each file becomes an EROFS layer in an OCI image

Mixing tar and qcow2 files is not allowed.`,
		Args: cobra.ExactArgs(1),
		RunE: h.Import,
	}
	importCmd.Flags().StringArray("file", nil, "file(s) to import (required, repeatable)")
	_ = importCmd.MarkFlagRequired("file")

	imageCmd.AddCommand(
		&cobra.Command{
			Use:   "pull IMAGE [IMAGE...]",
			Short: "Pull OCI image(s) or cloud image URL(s)",
			Args:  cobra.MinimumNArgs(1),
			RunE:  h.Pull,
		},
		importCmd,
		listCmd,
		&cobra.Command{
			Use:   "rm ID [ID...]",
			Short: "Delete locally stored image(s)",
			Args:  cobra.MinimumNArgs(1),
			RunE:  h.RM,
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
