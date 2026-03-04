package others

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// Actions defines cross-cutting system operations.
type Actions interface {
	GC(cmd *cobra.Command, args []string) error
	Version(cmd *cobra.Command, args []string) error
}

// Commands builds system command set (gc, version, completion).
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
		{
			Use:       "completion [bash|zsh|fish|powershell]",
			Short:     "Generate shell completion script",
			Args:      cobra.ExactArgs(1),
			ValidArgs: []string{"bash", "zsh", "fish", "powershell"},
			RunE: func(cmd *cobra.Command, args []string) error {
				root := cmd.Root()
				switch args[0] {
				case "bash":
					return root.GenBashCompletion(os.Stdout)
				case "zsh":
					return root.GenZshCompletion(os.Stdout)
				case "fish":
					return root.GenFishCompletion(os.Stdout, true)
				case "powershell":
					return root.GenPowerShellCompletionWithDesc(os.Stdout)
				default:
					return fmt.Errorf("unsupported shell: %s", args[0])
				}
			},
		},
	}
}
