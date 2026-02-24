package cmd

import (
	"context"
	"fmt"

	"github.com/projecteru2/core/log"
	"github.com/spf13/cobra"

	"github.com/projecteru2/cocoon/images/cloudimg"
	"github.com/projecteru2/cocoon/images/oci"
	"github.com/projecteru2/cocoon/progress"
	cloudimgProgress "github.com/projecteru2/cocoon/progress/cloudimg"
	ociProgress "github.com/projecteru2/cocoon/progress/oci"
)

var pullCmd = &cobra.Command{
	Use:   "pull IMAGE [IMAGE...]",
	Short: "Pull OCI image(s) or cloud image URL(s)",
	Args:  cobra.MinimumNArgs(1),
	RunE:  runPull,
}

func runPull(cmd *cobra.Command, args []string) error {
	ctx := commandContext(cmd)
	_, ociStore, cloudimgStore, err := initImageBackends(ctx)
	if err != nil {
		return err
	}

	for _, image := range args {
		if isURL(image) {
			if err := pullCloudimg(ctx, cloudimgStore, image); err != nil {
				return err
			}
		} else {
			if err := pullOCI(ctx, ociStore, image); err != nil {
				return err
			}
		}
	}
	return nil
}

func pullOCI(ctx context.Context, store *oci.OCI, image string) error {
	logger := log.WithFunc("cmd.pullOCI")
	tracker := progress.NewTracker(func(e ociProgress.Event) {
		switch e.Phase {
		case ociProgress.PhasePull:
			logger.Infof(ctx, "pulling OCI image %s (%d layers)", image, e.Total)
		case ociProgress.PhaseLayer:
			logger.Infof(ctx, "[%d/%d] %s done", e.Index+1, e.Total, e.Digest)
		case ociProgress.PhaseCommit:
			logger.Infof(ctx, "committing...")
		case ociProgress.PhaseDone:
			logger.Infof(ctx, "done: %s", image)
		}
	})
	if err := store.Pull(ctx, image, tracker); err != nil {
		return fmt.Errorf("pull %s: %w", image, err)
	}
	return nil
}

func pullCloudimg(ctx context.Context, store *cloudimg.CloudImg, url string) error {
	logger := log.WithFunc("cmd.pullCloudimg")
	tracker := progress.NewTracker(func(e cloudimgProgress.Event) {
		switch e.Phase {
		case cloudimgProgress.PhaseDownload:
			switch {
			case e.BytesDone == 0 && e.BytesTotal > 0:
				logger.Infof(ctx, "downloading cloud image %s (%s)", url, formatSize(e.BytesTotal))
			case e.BytesDone == 0:
				logger.Infof(ctx, "downloading cloud image %s", url)
			case e.BytesTotal > 0:
				pct := float64(e.BytesDone) / float64(e.BytesTotal) * 100
				fmt.Printf("\r  %s / %s (%.1f%%)", formatSize(e.BytesDone), formatSize(e.BytesTotal), pct)
			default:
				fmt.Printf("\r  %s downloaded", formatSize(e.BytesDone))
			}
		case cloudimgProgress.PhaseConvert:
			fmt.Println() // end progress line
			logger.Infof(ctx, "converting to qcow2...")
		case cloudimgProgress.PhaseCommit:
			logger.Infof(ctx, "committing...")
		case cloudimgProgress.PhaseDone:
			logger.Infof(ctx, "done: %s", url)
		}
	})
	if err := store.Pull(ctx, url, tracker); err != nil {
		return fmt.Errorf("pull %s: %w", url, err)
	}
	return nil
}
