package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/laedeli/acquire/internal/app"
	"github.com/laedeli/acquire/internal/opensubtitles"
	"github.com/laedeli/acquire/internal/store"
	"github.com/laedeli/acquire/internal/subtitles"
)

// The console's subtitles tab: the overview and the list are any signed-in
// user's to read; a search, a pick, a removal, asking for titles first and the
// settings are an admin's.

// maxSubtitleFinds bounds one request to find subtitles for a selection.
const maxSubtitleFinds = 500

// subtitleOverview is the tab's header: whether the work runs, the
// languages and how many titles have each, the day's downloads and the tabs'
// counts (of one language with ?language=).
//
//	GET /api/subtitles/overview
func (s *Server) subtitleOverview(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.svc.SubtitleOverview(r.Context(), r.URL.Query().Get("language")))
}

// subtitleList lists a tab's titles and languages, each with its status in
// words: ?tab=attention|notfound|waiting|way|has, ?language=, ?q= (title or
// S04E07), ?limit= (at most 5000).
//
//	GET /api/subtitles
func (s *Server) subtitleList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	tab := q.Get("tab")
	switch tab {
	case "", app.SubStatusAttention, app.SubStatusNotFound, app.SubStatusWaiting, app.SubStatusWay, app.SubStatusHas:
	default:
		http.Error(w, "tab must be attention, notfound, waiting, way or has", http.StatusBadRequest)
		return
	}
	items, err := s.svc.SubtitleList(r.Context(), tab, q.Get("language"), q.Get("q"), intParam(r, "limit", 1000, 1, 5000))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func subtitleKey(r *http.Request) store.SubtitleKey {
	return store.SubtitleKey{ItemID: chi.URLParam(r, "itemId"), Language: chi.URLParam(r, "language")}
}

// writeSubtitleError maps a subtitle action's failure to its status.
func writeSubtitleError(w http.ResponseWriter, err error) {
	var qe *opensubtitles.QuotaError
	switch {
	case errors.Is(err, app.ErrSubtitlesOff), errors.Is(err, app.ErrNoDownloadsLeft):
		http.Error(w, err.Error(), http.StatusConflict)
	case errors.Is(err, store.ErrNotFound):
		http.Error(w, "acquire doesn't search subtitles in that language for this title", http.StatusNotFound)
	case errors.As(err, &qe):
		http.Error(w, err.Error(), http.StatusTooManyRequests)
	default: // the source, or the catalog, answered otherwise
		http.Error(w, err.Error(), http.StatusBadGateway)
	}
}

// searchSubtitle searches one title in one language: every candidate in the
// order an automatic choice ranks them, each saying how it fits the file and
// why one is never taken on its own. A search costs no download.
//
//	GET /api/subtitles/{itemId}/{language}/search
func (s *Server) searchSubtitle(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	res, err := s.svc.SearchSubtitleTitle(r.Context(), subtitleKey(r))
	if err != nil {
		writeSubtitleError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// pickSubtitle downloads the candidate picked and attaches it to the title,
// at once: a download from the account's own, not the automatic budget.
//
//	POST /api/subtitles/{itemId}/{language}/pick   {candidate}
func (s *Server) pickSubtitle(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var c subtitles.Candidate
	if !decodeBody(w, r, &c) {
		return
	}
	if c.FileID == 0 {
		http.Error(w, "fileId is required", http.StatusBadRequest)
		return
	}
	if err := s.svc.PickSubtitle(r.Context(), subtitleKey(r), c); err != nil {
		writeSubtitleError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "attached"})
}

// removeSubtitle removes the subtitle acquire attached for the title's
// language; its file is never taken again for it.
//
//	DELETE /api/subtitles/{itemId}/{language}
func (s *Server) removeSubtitle(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if err := s.svc.RemoveSubtitle(r.Context(), subtitleKey(r)); err != nil {
		if strings.HasPrefix(err.Error(), "acquire added no ") {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		writeSubtitleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// previewSubtitle is the beginning of the subtitle acquire attached.
//
//	GET /api/subtitles/{itemId}/{language}/preview
func (s *Server) previewSubtitle(w http.ResponseWriter, r *http.Request) {
	text, err := s.svc.SubtitlePreview(r.Context(), subtitleKey(r))
	if err != nil {
		writeSubtitleError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"text": text})
}

// findSubtitles puts titles' languages first in line: searched on the next
// pass, their matches downloaded first. 202 with those it put there.
//
//	POST /api/subtitles/find   {"keys": [{"itemId": "…", "language": "de"}, …]}
func (s *Server) findSubtitles(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var body struct {
		Keys []store.SubtitleKey `json:"keys"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	switch {
	case len(body.Keys) == 0:
		http.Error(w, "keys is required", http.StatusBadRequest)
		return
	case len(body.Keys) > maxSubtitleFinds:
		http.Error(w, "at most 500 at once", http.StatusBadRequest)
		return
	}
	asked, err := s.svc.FindSubtitles(r.Context(), body.Keys)
	if err != nil {
		writeSubtitleError(w, err)
		return
	}
	if asked == nil {
		asked = []store.SubtitleKey{}
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"queued": asked, "skipped": len(body.Keys) - len(asked)})
}

// getSubtitleSettings reads the subtitle settings: whether the work downloads
// on its own, and whether it takes machine-translated subtitles.
//
//	GET /api/settings/subtitles
func (s *Server) getSubtitleSettings(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, s.svc.SubtitleSettings(r.Context()))
}

// putSubtitleSettings stores them.
//
//	PUT /api/settings/subtitles   If-Match: <revision>
func (s *Server) putSubtitleSettings(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	rev, err := ifMatch(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var in app.SubtitleSettings
	if !decodeBody(w, r, &in) {
		return
	}
	doc, err := s.svc.PutSubtitleSettings(r.Context(), in, rev, actorFrom(r.Context()))
	if err != nil {
		writeConfigError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, doc)
}
