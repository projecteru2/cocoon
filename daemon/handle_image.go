package daemon

import (
	"net/http"

	"github.com/projecteru2/cocoon/progress"
)

func (d *Daemon) handleListImages(w http.ResponseWriter, r *http.Request) {
	images, err := d.svc.ListImages(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusOK, images)
}

func (d *Daemon) handleInspectImage(w http.ResponseWriter, r *http.Request) {
	ref := r.PathValue("ref")

	img, err := d.svc.InspectImage(r.Context(), ref)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusOK, img)
}

func (d *Daemon) handleRemoveImages(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Refs []string `json:"refs"`
	}

	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	deleted, err := d.svc.RemoveImages(r.Context(), req.Refs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"deleted": deleted})
}

// handlePullImage pulls an image synchronously.
// TODO: replace with SSE streaming for progress updates.
func (d *Daemon) handlePullImage(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Ref string `json:"ref"`
	}

	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	if err := d.svc.PullImage(r.Context(), req.Ref, progress.Nop); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "ref": req.Ref})
}
