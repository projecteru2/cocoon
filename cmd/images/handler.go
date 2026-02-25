package images

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/projecteru2/core/log"
	"github.com/spf13/cobra"

	cmdcore "github.com/projecteru2/cocoon/cmd/core"
	"github.com/projecteru2/cocoon/images/cloudimg"
	"github.com/projecteru2/cocoon/images/oci"
	"github.com/projecteru2/cocoon/progress"
	cloudimgProgress "github.com/projecteru2/cocoon/progress/cloudimg"
	ociProgress "github.com/projecteru2/cocoon/progress/oci"
	"github.com/projecteru2/cocoon/types"
)

type Handler struct {
	cmdcore.BaseHandler
}

func (h Handler) Pull(cmd *cobra.Command, args []string) error {
	ctx, conf, err := h.Init(cmd)
	if err != nil {
		return err
	}
	_, ociStore, cloudimgStore, err := cmdcore.InitImageBackends(ctx, conf)
	if err != nil {
		return err
	}

	for _, image := range args {
		if cmdcore.IsURL(image) {
			if err := h.pullCloudimg(ctx, cloudimgStore, image); err != nil {
				return err
			}
		} else {
			if err := h.pullOCI(ctx, ociStore, image); err != nil {
				return err
			}
		}
	}
	return nil
}

func (h Handler) List(cmd *cobra.Command, _ []string) error {
	ctx, conf, err := h.Init(cmd)
	if err != nil {
		return err
	}
	backends, _, _, err := cmdcore.InitImageBackends(ctx, conf)
	if err != nil {
		return err
	}

	var all []*types.Image
	for _, b := range backends {
		imgs, err := b.List(ctx)
		if err != nil {
			return fmt.Errorf("list %s: %w", b.Type(), err)
		}
		all = append(all, imgs...)
	}
	if len(all) == 0 {
		fmt.Println("No images found.")
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "TYPE\tNAME\tDIGEST\tSIZE\tCREATED")
	for _, img := range all {
		digest := img.ID
		if len(digest) > 19 {
			digest = digest[:19]
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			img.Type,
			img.Name,
			digest,
			cmdcore.FormatSize(img.Size),
			img.CreatedAt.Local().Format(time.DateTime),
		)
	}
	w.Flush() //nolint:errcheck,gosec
	return nil
}

func (h Handler) RM(cmd *cobra.Command, args []string) error {
	ctx, conf, err := h.Init(cmd)
	if err != nil {
		return err
	}
	logger := log.WithFunc("cmd.image.rm")
	backends, _, _, err := cmdcore.InitImageBackends(ctx, conf)
	if err != nil {
		return err
	}

	var allDeleted []string
	for _, b := range backends {
		deleted, err := b.Delete(ctx, args)
		if err != nil {
			return fmt.Errorf("delete %s: %w", b.Type(), err)
		}
		allDeleted = append(allDeleted, deleted...)
	}
	for _, ref := range allDeleted {
		logger.Infof(ctx, "deleted: %s", ref)
	}
	if len(allDeleted) == 0 {
		logger.Info(ctx, "no matching images found")
	}
	return nil
}

func (h Handler) Inspect(cmd *cobra.Command, args []string) error {
	ctx, conf, err := h.Init(cmd)
	if err != nil {
		return err
	}
	backends, _, _, err := cmdcore.InitImageBackends(ctx, conf)
	if err != nil {
		return err
	}

	ref := args[0]
	for _, b := range backends {
		img, err := b.Inspect(ctx, ref)
		if err != nil {
			continue
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(img)
	}
	return fmt.Errorf("image %q not found", ref)
}

func (h Handler) pullOCI(ctx context.Context, store *oci.OCI, image string) error {
	logger := log.WithFunc("cmd.pullOCI")
	tracker := progress.NewTracker(func(e ociProgress.Event) {
		switch e.Phase {
		case ociProgress.PhasePull:
			logger.Infof(ctx, "pulling OCI image %s (%d layers)", image, e.Total)
		case ociProgress.PhaseLayer:
			logger.Infof(ctx, "[%d/%d] %s done", e.Index+1, e.Total, e.Digest)
		case ociProgress.PhaseCommit:
			logger.Info(ctx, "committing...")
		case ociProgress.PhaseDone:
			logger.Infof(ctx, "done: %s", image)
		}
	})
	if err := store.Pull(ctx, image, tracker); err != nil {
		return fmt.Errorf("pull %s: %w", image, err)
	}
	return nil
}

func (h Handler) pullCloudimg(ctx context.Context, store *cloudimg.CloudImg, url string) error {
	logger := log.WithFunc("cmd.pullCloudimg")
	tracker := progress.NewTracker(func(e cloudimgProgress.Event) {
		switch e.Phase {
		case cloudimgProgress.PhaseDownload:
			switch {
			case e.BytesDone == 0 && e.BytesTotal > 0:
				logger.Infof(ctx, "downloading cloud image %s (%s)", url, cmdcore.FormatSize(e.BytesTotal))
			case e.BytesDone == 0:
				logger.Infof(ctx, "downloading cloud image %s", url)
			case e.BytesTotal > 0:
				pct := float64(e.BytesDone) / float64(e.BytesTotal) * 100
				fmt.Printf("\r  %s / %s (%.1f%%)", cmdcore.FormatSize(e.BytesDone), cmdcore.FormatSize(e.BytesTotal), pct)
			default:
				fmt.Printf("\r  %s downloaded", cmdcore.FormatSize(e.BytesDone))
			}
		case cloudimgProgress.PhaseConvert:
			fmt.Println()
			logger.Info(ctx, "converting to qcow2...")
		case cloudimgProgress.PhaseCommit:
			logger.Info(ctx, "committing...")
		case cloudimgProgress.PhaseDone:
			logger.Infof(ctx, "done: %s", url)
		}
	})
	if err := store.Pull(ctx, url, tracker); err != nil {
		return fmt.Errorf("pull %s: %w", url, err)
	}
	return nil
}
