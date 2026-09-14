package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/laedeli/acquire/internal/app"
	"github.com/laedeli/acquire/internal/store"
)

// Search sources: the newznab and torznab endpoints acquire searches. Same
// rules as the rest of the configuration API (configapi.go): admin-only,
// If-Match revisions on writes, 422 field errors, write-only API keys.

// writeSourceError is writeConfigError with the source's own wording.
func writeSourceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		http.Error(w, "no such search source", http.StatusNotFound)
	case errors.Is(err, store.ErrExists):
		http.Error(w, "a search source with this name already exists", http.StatusConflict)
	default:
		writeConfigError(w, err)
	}
}

// sourceID reads the {id} of a source route; false (after a 404) when it is
// not one.
func sourceID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "no such search source", http.StatusNotFound)
		return 0, false
	}
	return id, true
}

// listIndexers returns the search sources with their health and usage.
//
//	GET /api/indexers
func (s *Server) listIndexers(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	out, err := s.svc.ListSources(r.Context())
	if err != nil {
		writeSourceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// createIndexer adds a search source and reads its caps.
//
//	POST /api/indexers
func (s *Server) createIndexer(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var in app.SourceInput
	if !decodeBody(w, r, &in) {
		return
	}
	out, err := s.svc.CreateSource(r.Context(), in, actorFrom(r.Context()))
	if err != nil {
		writeSourceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

// updateIndexer replaces a search source's editable fields.
//
//	PUT /api/indexers/{id}   If-Match: <revision>
func (s *Server) updateIndexer(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	id, ok := sourceID(w, r)
	if !ok {
		return
	}
	rev, err := ifMatch(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var in app.SourceInput
	if !decodeBody(w, r, &in) {
		return
	}
	out, err := s.svc.UpdateSource(r.Context(), id, in, rev, actorFrom(r.Context()))
	if err != nil {
		writeSourceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// deleteIndexer removes a search source.
//
//	DELETE /api/indexers/{id}   If-Match: <revision>
func (s *Server) deleteIndexer(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	id, ok := sourceID(w, r)
	if !ok {
		return
	}
	rev, err := ifMatch(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.svc.DeleteSource(r.Context(), id, rev, actorFrom(r.Context())); err != nil {
		writeSourceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// testIndexer asks a source that is not saved (or an edit of one that is),
// recording nothing.
//
//	POST /api/indexers/test
func (s *Server) testIndexer(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var in app.SourceInput
	if !decodeBody(w, r, &in) {
		return
	}
	res, err := s.svc.TestSource(r.Context(), in)
	if err != nil {
		var ve *app.ValidationError
		if errors.As(err, &ve) {
			writeSourceError(w, err)
			return
		}
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// testStoredIndexer asks a saved source as stored; the outcome is its health.
//
//	POST /api/indexers/{id}/test
func (s *Server) testStoredIndexer(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	id, ok := sourceID(w, r)
	if !ok {
		return
	}
	res, err := s.svc.TestStoredSource(r.Context(), id)
	if err != nil {
		writeSourceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}
