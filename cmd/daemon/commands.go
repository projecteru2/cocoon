package daemon

import "github.com/spf13/cobra"

// Actions defines daemon operations.
type Actions interface {
	Start(cmd *cobra.Command, args []string) error
}

// Command builds the "daemon" command.
func Command(h Actions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Start the cocoon HTTP API daemon (foreground)",
		RunE:  h.Start,
	}

	cmd.Flags().String("listen", "", "listen address: unix socket path or host:port (default: $RUN_DIR/cocoon.sock)")

	return cmd
}
