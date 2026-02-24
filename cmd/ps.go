package cmd

import (
	"fmt"
	"os"
	"sort"
	"text/tabwriter"
	"time"

	units "github.com/docker/go-units"
	"github.com/spf13/cobra"
)

var psCmd = &cobra.Command{
	Use:   "ps",
	Short: "List VMs with status",
	RunE:  runPS,
}

func runPS(cmd *cobra.Command, _ []string) error {
	ctx := commandContext(cmd)
	hyper, err := initHypervisor()
	if err != nil {
		return err
	}

	vms, err := hyper.List(ctx)
	if err != nil {
		return fmt.Errorf("ps: %w", err)
	}
	if len(vms) == 0 {
		fmt.Println("No VMs found.")
		return nil
	}

	sort.Slice(vms, func(i, j int) bool { return vms[i].CreatedAt.Before(vms[j].CreatedAt) })

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "ID\tNAME\tSTATE\tCPU\tMEMORY\tIMAGE\tCREATED")
	for _, vm := range vms {
		state := reconcileState(vm)
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\t%s\t%s\n",
			vm.ID,
			vm.Config.Name,
			state,
			vm.Config.CPU,
			units.BytesSize(float64(vm.Config.Memory)),
			vm.Config.Image,
			vm.CreatedAt.Local().Format(time.DateTime),
		)
	}
	w.Flush() //nolint:errcheck,gosec
	return nil
}
