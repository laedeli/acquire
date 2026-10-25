package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/laedeli/acquire/internal/app"
	"github.com/laedeli/acquire/internal/gateway"
	"github.com/laedeli/acquire/internal/store"
)

// Configuration API: download clients, the search and grab settings, and the
// setup checklist. Every route is admin-only. Writes carry the revision they
// were based on in If-Match and get 409 when it is stale; invalid content gets
// 422 {"fieldErrors":[...]}; secrets are write-only.

// maxConfigBody bounds a configuration write; these documents are tiny.
const maxConfigBody = 1 << 20

func (s *Server) requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	if hasRole(r.Context(), s.cfg.AdminRole) {
		return true
	}
	http.Error(w, "forbidden: requires "+s.cfg.AdminRole, http.StatusForbidden)
	return false
}

// actorFrom names who made a change, for the audit trail.
func actorFrom(ctx context.Context) string {
	if sub := subFrom(ctx); sub != "" {
		return sub
	}
	if v, _ := ctx.Value(devBypassCtxKey{}).(bool); v {
		return "local"
	}
	return ""
}

// ifMatch reads the revision a write was based on. Absent means unconditional;
// ETag-style quoting and a weak prefix are accepted.
func ifMatch(r *http.Request) (int64, error) {
	v := strings.TrimSpace(r.Header.Get("If-Match"))
	if v == "" || v == "*" {
		return 0, nil
	}
	v = strings.Trim(strings.TrimPrefix(v, "W/"), `"`)
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 0 {
		return 0, errors.New("If-Match must be a revision number")
	}
	return n, nil
}

func decodeBody(w http.ResponseWriter, r *http.Request, into any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxConfigBody))
	if err := dec.Decode(into); err != nil {
		http.Error(w, "invalid body: "+err.Error(), http.StatusBadRequest)
		return false
	}
	return true
}

// writeConfigError maps a configuration failure to its status.
func writeConfigError(w http.ResponseWriter, err error) {
	var ve *app.ValidationError
	switch {
	case errors.As(err, &ve):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"fieldErrors": ve.Fields})
	case errors.Is(err, store.ErrNotFound):
		http.Error(w, "no such download client", http.StatusNotFound)
	case errors.Is(err, store.ErrStale):
		http.Error(w, "changed since it was read — reload and apply your edit again", http.StatusConflict)
	case errors.Is(err, store.ErrExists):
		http.Error(w, "a client with this id already exists", http.StatusConflict)
	case errors.Is(err, store.ErrInFlight):
		http.Error(w, "downloads are still in flight on this client — wait for them or cancel them first", http.StatusConflict)
	case errors.Is(err, app.ErrManaged):
		http.Error(w, "this download client runs with the addon and follows its deployment — change it in the addon's values (clients.*), not here", http.StatusConflict)
	case errors.Is(err, app.ErrNoKey):
		http.Error(w, "ACQUIRE_CONFIG_KEY is not set — credentials cannot be stored", http.StatusConflict)
	case errors.Is(err, gateway.ErrNoConfigAPI):
		http.Error(w, "the download gateway does not offer the configuration API — upgrade it and set ALLOWED_CLIENTS", http.StatusBadGateway)
	default:
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// setup is the checklist the platform shows for this addon.
//
//	GET /api/setup
func (s *Server) setup(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, s.svc.Setup(r.Context()))
}

// listDownloadClients returns the clients with their live gateway state.
//
//	GET /api/download-clients
func (s *Server) listDownloadClients(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	out, err := s.svc.ListClients(r.Context())
	if err != nil {
		writeConfigError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// downloadClientTypes lists the client types the gateway runs.
//
//	GET /api/download-clients/types
func (s *Server) downloadClientTypes(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	types, source := s.svc.ClientTypes(r.Context())
	// The console can say "the gateway could not be asked" when this is the
	// fallback list rather than the gateway's own.
	w.Header().Set("X-Acquire-Types-Source", source)
	writeJSON(w, http.StatusOK, types)
}

// createDownloadClient adds a client and pushes the set to the gateway.
//
//	POST /api/download-clients
func (s *Server) createDownloadClient(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var in app.ClientInput
	if !decodeBody(w, r, &in) {
		return
	}
	out, err := s.svc.CreateClient(r.Context(), in, actorFrom(r.Context()))
	if err != nil {
		writeConfigError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

// updateDownloadClient replaces a client's editable fields.
//
//	PUT /api/download-clients/{id}   If-Match: <revision>
func (s *Server) updateDownloadClient(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	rev, err := ifMatch(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var in app.ClientInput
	if !decodeBody(w, r, &in) {
		return
	}
	out, err := s.svc.UpdateClient(r.Context(), chi.URLParam(r, "id"), in, rev, actorFrom(r.Context()))
	if err != nil {
		writeConfigError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// deleteDownloadClient removes a client that has nothing in flight.
//
//	DELETE /api/download-clients/{id}   If-Match: <revision>
func (s *Server) deleteDownloadClient(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	rev, err := ifMatch(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	res, err := s.svc.DeleteClient(r.Context(), chi.URLParam(r, "id"), rev, actorFrom(r.Context()))
	if err != nil {
		writeConfigError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sync": res})
}

// testDownloadClient reaches a client through the gateway without saving it.
//
//	POST /api/download-clients/test
func (s *Server) testDownloadClient(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var in app.ClientInput
	if !decodeBody(w, r, &in) {
		return
	}
	res, err := s.svc.TestClient(r.Context(), in)
	if err != nil {
		var ve *app.ValidationError
		if errors.As(err, &ve) || errors.Is(err, gateway.ErrNoConfigAPI) {
			writeConfigError(w, err)
			return
		}
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// searchSettingsAnswer is the search and grab policy, with how far TVmaze's
// air times are of the series that run (airTimesStatus, read only).
type searchSettingsAnswer struct {
	app.SearchSettingsDoc
	AirTimesStatus *app.AirTimesStatus `json:"airTimesStatus,omitempty"`
}

func (s *Server) searchSettingsAnswer(r *http.Request, doc app.SearchSettingsDoc) searchSettingsAnswer {
	out := searchSettingsAnswer{SearchSettingsDoc: doc}
	if st, err := s.svc.AirTimesStatus(r.Context()); err == nil {
		out.AirTimesStatus = &st
	}
	return out
}

// getSearchSettings returns the search and grab policy.
//
//	GET /api/settings/search
func (s *Server) getSearchSettings(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	doc, err := s.svc.GetSearchSettings(r.Context())
	if err != nil {
		writeConfigError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.searchSettingsAnswer(r, doc))
}

// getRequestSettings returns how requests are taken: whether a request from
// someone who is not an admin is approved at once (autoApprove).
//
//	GET /api/settings/requests
func (s *Server) getRequestSettings(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, s.svc.RequestSettings(r.Context()))
}

// putRequestSettings stores them.
//
//	PUT /api/settings/requests   If-Match: <revision>
func (s *Server) putRequestSettings(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	rev, err := ifMatch(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	in := s.svc.RequestSettings(r.Context())
	if !decodeBody(w, r, &in) {
		return
	}
	doc, err := s.svc.PutRequestSettings(r.Context(), in, rev, actorFrom(r.Context()))
	if err != nil {
		writeConfigError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, doc)
}

// putSearchSettings stores the search and grab policy. The document is read
// over the policy stored: a field it does not name — one a console from
// before it does not know — keeps its value. Switching grabbing on its own
// on while series that run have no air times from TVmaze yet answers 409
// with confirm "airTimes", until it is sent again with ?confirm=airTimes.
//
//	PUT /api/settings/search[?confirm=airTimes]   If-Match: <revision>
func (s *Server) putSearchSettings(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	rev, err := ifMatch(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	cur, err := s.svc.GetSearchSettings(r.Context())
	if err != nil {
		writeConfigError(w, err)
		return
	}
	in := cur.SearchSettings
	if !decodeBody(w, r, &in) {
		return
	}
	doc, err := s.svc.PutSearchSettings(r.Context(), in, rev, actorFrom(r.Context()), r.URL.Query().Get("confirm") == "airTimes")
	var missing *app.AirTimesMissing
	if errors.As(err, &missing) {
		writeJSON(w, http.StatusConflict, map[string]any{"message": err.Error(), "confirm": "airTimes", "airTimesStatus": missing.Status})
		return
	}
	if err != nil {
		writeConfigError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.searchSettingsAnswer(r, doc))
}
