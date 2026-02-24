package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/projecteru2/cocoon/version"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Show version, git revision, and build timestamp",
	Run: func(_ *cobra.Command, _ []string) {
		fmt.Print(version.String())
	},
}
