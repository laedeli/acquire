package httpapi

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/laedeli/acquire/internal/app"
	"github.com/laedeli/acquire/internal/store"
)

// Grabs bound to targets, from the console's Missing tab, and the holdings
// reconcile. Every route is admin-only.

// maxTargetGrabs bounds one bulk request: the queue does them one at a time,
// each a typed search, and a source's daily allowance is the scarce resource.
const maxTargetGrabs = 200

// grabTargets queues a search and grab of the best release an automatic grab
// may take for each target named — one, or a whole selection — done one at a
// time by acquire's grab worker. A target that is held or has a grab under
// way is skipped. The targets' rows say what became of each.
//
//	POST /api/targets/grab   {"targetIds":["series:1396:s02e005", …]}
func (s *Server) grabTargets(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var body struct {
		TargetIDs []string `json:"targetIds"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	switch {
	case len(body.TargetIDs) == 0:
		http.Error(w, "targetIds is required", http.StatusBadRequest)
		return
	case len(body.TargetIDs) > maxTargetGrabs:
		http.Error(w, "at most 200 targets at once", http.StatusBadRequest)
		return
	}
	queued, err := s.svc.RequestTargetGrabs(r.Context(), body.TargetIDs)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"queued": queued, "skipped": len(body.TargetIDs) - len(queued)})
}

// pickTarget grabs one release picked among a target's typed search results
// (GET /api/targets/{id}/search), sending back its release reference. The
// grab is bound to every wanted target the release covers: a file of several
// episodes, or a season pack, covers more than the one it was found for.
//
//	POST /api/targets/{id}/pick   {candidate}
func (s *Server) pickTarget(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var c app.Candidate
	if !decodeBody(w, r, &c) {
		return
	}
	if c.Source == "" && c.Release == "" {
		http.Error(w, "release or source is required", http.StatusBadRequest)
		return
	}
	g, err := s.svc.GrabTargetCandidate(r.Context(), chi.URLParam(r, "id"), c)
	var ng *store.NotGrabbableError
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, map[string]any{"status": "grabbed", "grab": g})
	case errors.Is(err, store.ErrNotFound):
		http.Error(w, "no such target", http.StatusNotFound)
	case errors.As(err, &ng):
		http.Error(w, err.Error(), http.StatusConflict)
	default:
		http.Error(w, err.Error(), http.StatusBadGateway)
	}
}

// reconcileHoldings asks katalog-api now what the library holds of every
// tracked title, as the holdings-reconcile schedule does. It runs in the
// background; the Missing tab follows.
//
//	POST /api/holdings/reconcile
func (s *Server) reconcileHoldings(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	s.svc.RequestReconcile()
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "reconciling"})
}
