package daemon

import "net/http"

func (d *Daemon) handleGC(w http.ResponseWriter, r *http.Request) {
	if err := d.svc.RunGC(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
