// Package httpapi is acquire's REST surface + OIDC auth, and the bundle of the
// console the portal hosts. Commands are REST at the edges
// (request/grab/discover); live status is the SSE stream. Auth is issuer-only +
// realm-role checks: the user role may request, the admin role may
// grab/delete. Init is lazy + self-healing (recovers when the IdP comes up).
package httpapi

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"

	"github.com/laedeli/acquire/internal/app"
	"github.com/laedeli/acquire/internal/config"
	"github.com/laedeli/acquire/internal/importer"
	"github.com/laedeli/acquire/internal/release"
	"github.com/laedeli/acquire/internal/sse"
	"github.com/laedeli/acquire/internal/store"
)

type ctxKey int

const (
	subKey  ctxKey = iota
	roleKey        // []string realm roles
)

// Verifier does lazy, self-healing OIDC verification + realm-role extraction.
type Verifier struct {
	issuer string
	mu     sync.Mutex
	ver    *oidc.IDTokenVerifier
	ready  bool
}

func NewVerifier(issuer string) *Verifier { return &Verifier{issuer: strings.TrimSpace(issuer)} }

func (v *Verifier) enabled() bool { return v.issuer != "" }

func (v *Verifier) ensure(ctx context.Context) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.ready {
		return nil
	}
	p, err := oidc.NewProvider(ctx, v.issuer)
	if err != nil {
		return err
	}
	v.ver = p.Verifier(&oidc.Config{SkipClientIDCheck: true})
	v.ready = true
	return nil
}

func (v *Verifier) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !v.enabled() {
			// Auth disabled (dev) — treat as an admin so the flow works locally.
			ctx := context.WithValue(r.Context(), devBypassCtxKey{}, true)
			ctx = context.WithValue(ctx, roleKey, []string{})
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}
		authz := r.Header.Get("Authorization")
		if !strings.HasPrefix(authz, "Bearer ") {
			http.Error(w, "missing bearer token", http.StatusUnauthorized)
			return
		}
		if err := v.ensure(r.Context()); err != nil {
			http.Error(w, "oidc provider unavailable", http.StatusServiceUnavailable)
			return
		}
		tok, err := v.ver.Verify(r.Context(), strings.TrimPrefix(authz, "Bearer "))
		if err != nil {
			http.Error(w, "invalid token: "+err.Error(), http.StatusUnauthorized)
			return
		}
		var claims struct {
			RealmAccess struct {
				Roles []string `json:"roles"`
			} `json:"realm_access"`
		}
		_ = tok.Claims(&claims)
		ctx := context.WithValue(r.Context(), subKey, tok.Subject)
		ctx = context.WithValue(ctx, roleKey, claims.RealmAccess.Roles)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func rolesFrom(ctx context.Context) []string {
	if r, ok := ctx.Value(roleKey).([]string); ok {
		return r
	}
	return nil
}
func subFrom(ctx context.Context) string {
	if s, ok := ctx.Value(subKey).(string); ok {
		return s
	}
	return ""
}

// devBypassKey marks a request that skipped verification because no issuer is
// configured (local development). It is the ONLY thing that grants a role for
// free — a real token with an empty realm_access.roles must not be treated as
// an admin, which is what keying off len(roles)==0 used to do.
type devBypassCtxKey struct{}

func hasRole(ctx context.Context, role string) bool {
	if v, _ := ctx.Value(devBypassCtxKey{}).(bool); v {
		return true // auth disabled (no OIDC_ISSUER): local dev only
	}
	for _, r := range rolesFrom(ctx) {
		if r == role {
			return true
		}
	}
	return false
}

// webFS holds the console's bundle under web/embed: the federated module the
// portal loads, and no page. all: keeps the chunks whose names start with an
// underscore.
//
//go:embed all:web
var webFS embed.FS

const (
	// portalAppPath is acquire's app in the portal: the shell's route for the
	// app whose key is the manifest's service name, where the console opens in
	// place.
	portalAppPath = "/portal/app/acquire"
	// portalRoot is the portal's root, its launchpad.
	portalRoot = "/portal/"
)

// Server wires the routes.
type Server struct {
	cfg config.Config
	svc *app.Service
	st  *store.Store
	br  *sse.Broker
	ver *Verifier
}

func NewServer(cfg config.Config, svc *app.Service, st *store.Store, br *sse.Broker) *Server {
	return &Server{cfg: cfg, svc: svc, st: st, br: br, ver: NewVerifier(cfg.OIDCIssuer)}
}

func (s *Server) Handler() http.Handler {
	r := chi.NewRouter()
	r.Use(chimw.RealIP, chimw.Recoverer)

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, map[string]string{"status": "ok"}) })
	// Unauthenticated on purpose: Prometheus scrapes without a bearer, and a
	// health surface that needs working auth is useless exactly when auth is
	// what broke. Neither exposes secrets — only counts.
	r.Get("/metrics", s.metrics)
	r.Get("/api/health/system", s.systemHealth)
	// Capability descriptor for the zae CLI — metadata, aggregated by the
	// platform's portal. See capability.go.
	r.Get("/.well-known/zaentrum-capability.json", s.capabilityHandler)
	// What the console reads before it renders, unauthenticated like the
	// health routes since neither value is a secret: the realm role that makes
	// a user an admin, and whether it shows "Find & grab" — true when a source
	// AND a client for its protocol exist, so a grab has somewhere to come
	// from and go to. Signing in is the portal's, so nothing here is for that.
	r.Get("/api/config", func(w http.ResponseWriter, rq *http.Request) {
		writeJSON(w, 200, map[string]any{
			"adminRole": s.cfg.AdminRole,
			"autoGrab":  s.svc.AutoGrabEnabled(rq.Context()),
		})
	})
	r.Get("/readyz", func(w http.ResponseWriter, rq *http.Request) {
		if err := s.st.Ping(rq.Context()); err != nil {
			http.Error(w, "db down", http.StatusServiceUnavailable)
			return
		}
		writeJSON(w, 200, map[string]string{"status": "ready"})
	})

	// Authenticated API. The event stream is in here too: it now carries download
	// telemetry, so it is read with fetch-streaming (which can send a bearer)
	// rather than EventSource.
	r.Group(func(r chi.Router) {
		r.Use(s.ver.middleware)
		r.Get("/api/events", s.br.Handler)
		r.Get("/api/wanted", s.listWanted)
		r.Post("/api/wanted", s.createWanted)                    // user role
		r.Delete("/api/wanted/{id}", s.deleteWanted)             // admin
		r.Post("/api/wanted/{id}/grab", s.grabWanted)            // admin (manual magnet)
		r.Post("/api/wanted/{id}/autograb", s.autograbWanted)    // admin (search + best)
		r.Post("/api/wanted/{id}/approve", s.decideWanted(true)) // admin
		r.Post("/api/wanted/{id}/decline", s.decideWanted(false))
		r.Get("/api/discover", s.discover)
		r.Get("/api/status", s.status)
		r.Get("/api/downloads", s.listDownloads)
		r.Get("/api/clients", s.listClients)
		r.Post("/api/downloads/{adapter}/{id}/{action}", s.controlDownload) // admin
		r.Get("/api/wanted/{id}/releases", s.listReleases)                  // admin
		r.Post("/api/wanted/{id}/pick", s.pickRelease)                      // admin
		r.Get("/api/search", s.search)                                      // admin — manual, across sources
		r.Post("/api/search/grab", s.grabFound)                             // admin
		r.Get("/api/profiles", s.listProfiles)
		r.Put("/api/profiles/{id}", s.saveProfile)      // admin
		r.Delete("/api/profiles/{id}", s.deleteProfile) // admin
		r.Post("/api/score/simulate", s.simulateScore)  // admin — pure, no indexer I/O
		r.Post("/api/import/intent", s.importIntent)    // admin — read incumbent, write titles
		r.Post("/api/import/inventory", s.deriveInv)    // admin — derive episodes from TMDB
		// The WANT model's read side. These were written but never registered —
		// unused methods compile, so nothing caught it until the console got a
		// 301 to the SPA instead of JSON. A test now asserts every exported
		// handler is reachable.
		r.Get("/api/series", s.listSeries)
		r.Get("/api/calendar", s.calendar)
		r.Get("/api/missing", s.missing)
		r.Get("/api/counts", s.counts)
		r.Get("/api/history", s.history)
		r.Get("/api/targets/{id}/search", s.searchTarget) // admin — TYPED search
		// Grabs bound to targets (targetsapi.go): a search and grab queued per
		// target or for a selection, a pick among a target's results, and the
		// holdings reconcile, asked now.
		r.Post("/api/targets/grab", s.grabTargets)             // admin
		r.Post("/api/targets/{id}/pick", s.pickTarget)         // admin
		r.Post("/api/holdings/reconcile", s.reconcileHoldings) // admin
		// Subtitles in the instance's languages (subtitlesapi.go): the tab's
		// header and list, a person's search, pick and removal, a selection
		// put first in line.
		r.Get("/api/subtitles/overview", s.subtitleOverview)
		r.Get("/api/subtitles", s.subtitleList)
		r.Post("/api/subtitles/find", s.findSubtitles)                       // admin
		r.Get("/api/subtitles/{itemId}/{language}/search", s.searchSubtitle) // admin
		r.Post("/api/subtitles/{itemId}/{language}/pick", s.pickSubtitle)    // admin
		r.Get("/api/subtitles/{itemId}/{language}/preview", s.previewSubtitle)
		r.Delete("/api/subtitles/{itemId}/{language}", s.removeSubtitle) // admin

		// Configuration, admin-only (configapi.go). The platform links its setup
		// checklist into the console tabs that edit these.
		r.Get("/api/setup", s.setup)
		r.Get("/api/indexers", s.listIndexers)
		r.Post("/api/indexers", s.createIndexer)
		r.Post("/api/indexers/test", s.testIndexer)
		r.Put("/api/indexers/{id}", s.updateIndexer)
		r.Delete("/api/indexers/{id}", s.deleteIndexer)
		r.Post("/api/indexers/{id}/test", s.testStoredIndexer)
		r.Get("/api/download-clients", s.listDownloadClients)
		r.Post("/api/download-clients", s.createDownloadClient)
		r.Get("/api/download-clients/types", s.downloadClientTypes)
		r.Post("/api/download-clients/test", s.testDownloadClient)
		r.Put("/api/download-clients/{id}", s.updateDownloadClient)
		r.Delete("/api/download-clients/{id}", s.deleteDownloadClient)
		r.Get("/api/settings/search", s.getSearchSettings)
		r.Put("/api/settings/search", s.putSearchSettings)
		r.Get("/api/settings/subtitles", s.getSubtitleSettings)
		r.Put("/api/settings/subtitles", s.putSubtitleSettings)
		r.Get("/api/settings/requests", s.getRequestSettings)
		r.Put("/api/settings/requests", s.putRequestSettings)
	})

	// The console, as the portal loads it through its app proxy: the federated
	// module's remote entry (/embed/assets/remoteEntry.js), its chunks and its
	// stylesheet.
	bundle, _ := fs.Sub(webFS, "web/embed")
	r.Get("/embed/*", serveBundle(bundle))
	r.Head("/embed/*", serveBundle(bundle))

	// Every other path. acquire has no page of its own: the portal is the
	// shell and the console opens in place there, on the portal's sign-in. A
	// browser that asks acquire for a page — its root, or any other path the
	// standalone console used to answer — is sent to acquire's app in the
	// portal (notFound).
	//
	// An unknown /api path must 404, never redirect. The standalone console's
	// file server answered a missing directory-ish path with 301 Moved
	// Permanently, and a browser caches a PERMANENT redirect. So an API route
	// that was temporarily absent — a handler written but not yet registered,
	// exactly what happened here once — poisoned every client that touched it:
	// the console kept looping on the cached 301 long after the server was
	// fixed, and no amount of redeploying helped. A 404 is honest and
	// uncacheable, and the redirect to the portal is a temporary one (302).
	r.NotFound(s.notFound)
	return r
}

// notFound answers the paths no route takes (see Handler): a page is sent to
// acquire's app in the portal, anything else is a 404.
func (s *Server) notFound(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	switch {
	case strings.HasPrefix(p, "/api/"):
		http.Error(w, "no such endpoint: "+p, http.StatusNotFound)
	case r.Method != http.MethodGet && r.Method != http.MethodHead,
		// A script or stylesheet that is not there — of the bundle, or of the
		// standalone page's former assets — is a 404, not a page.
		p == "/embed" || strings.HasPrefix(p, "/embed/"), strings.HasPrefix(p, "/assets/"),
		// The portal's own paths are never answered here. Without a platform
		// address, sending them to the portal's root would send the browser
		// back here.
		p == "/portal" || strings.HasPrefix(p, "/portal/"):
		http.NotFound(w, r)
	default:
		// No body: there is no page here, not even one saying so.
		w.Header().Set("Location", s.portalApp(r.URL.Query().Get("q")))
		w.WriteHeader(http.StatusFound)
	}
}

// portalApp is where a browser finds acquire's console: acquire's app in the
// portal on the platform's address, carrying a search the console reads (?q=,
// as the "Request this" row passes it). The browser keeps the place in the
// console (#/downloads): a redirect without a fragment keeps the one asked
// for. Nothing else of the query is carried, so a stale sign-in callback of
// the former standalone console (?code=…&state=…) never reaches the portal's
// own. Without a platform address it is the portal's root on the host the
// browser asked.
func (s *Server) portalApp(q string) string {
	if s.cfg.PlatformURL == "" {
		return portalRoot
	}
	target := s.cfg.PlatformURL + portalAppPath
	if q = strings.TrimSpace(q); q != "" {
		target += "?q=" + url.QueryEscape(q)
	}
	return target
}

// serveBundle serves the files of the console's bundle, mounted at /embed/. A
// directory or a missing file is a 404: never a listing, never a page.
func serveBundle(bundle fs.FS) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name, ok := strings.CutPrefix(path.Clean(r.URL.Path), "/embed/")
		if !ok {
			http.NotFound(w, r)
			return
		}
		if fi, err := fs.Stat(bundle, name); err != nil || fi.IsDir() {
			http.NotFound(w, r)
			return
		}
		http.ServeFileFS(w, r, bundle, name)
	}
}

// listReleases runs an interactive search for a request and returns the ranked
// candidates, so an admin can see what is on offer instead of trusting the
// automatic pick. Like the manual search it answers {candidates, incomplete}.
func (s *Server) listReleases(w http.ResponseWriter, r *http.Request) {
	if !hasRole(r.Context(), s.cfg.AdminRole) {
		http.Error(w, "forbidden: requires "+s.cfg.AdminRole, http.StatusForbidden)
		return
	}
	out, err := s.svc.Releases(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, 200, out)
}

// pickRelease grabs one specific candidate from the picker.
func (s *Server) pickRelease(w http.ResponseWriter, r *http.Request) {
	if !hasRole(r.Context(), s.cfg.AdminRole) {
		http.Error(w, "forbidden: requires "+s.cfg.AdminRole, http.StatusForbidden)
		return
	}
	var c app.Candidate
	if err := json.NewDecoder(r.Body).Decode(&c); err != nil || (c.Source == "" && c.Release == "") {
		http.Error(w, "release or source is required", http.StatusBadRequest)
		return
	}
	if err := s.svc.GrabCandidate(r.Context(), chi.URLParam(r, "id"), c); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, 200, map[string]string{"status": "grabbed"})
}

// search runs a free-text query across the search sources, optionally scoped
// to some of them (?indexers=3,7), ranked by the active quality profile. It
// answers within the search deadline with {candidates, incomplete}, naming the
// sources that did not answer.
func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	if !hasRole(r.Context(), s.cfg.AdminRole) {
		http.Error(w, "forbidden: requires "+s.cfg.AdminRole, http.StatusForbidden)
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeJSON(w, 200, app.SearchResult{Candidates: []app.Candidate{}, Incomplete: []string{}})
		return
	}
	var only []int64
	for _, part := range strings.Split(r.URL.Query().Get("indexers"), ",") {
		if n, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64); err == nil && n > 0 {
			only = append(only, n)
		}
	}
	out, err := s.svc.Search(r.Context(), q, only)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, 200, out)
}

// grabFound grabs a release found in the manual search. When it names an
// existing request it attaches to it; otherwise a request is created so the
// download still reaches the catalog.
func (s *Server) grabFound(w http.ResponseWriter, r *http.Request) {
	if !hasRole(r.Context(), s.cfg.AdminRole) {
		http.Error(w, "forbidden: requires "+s.cfg.AdminRole, http.StatusForbidden)
		return
	}
	var body struct {
		app.Candidate
		WantedID string `json:"wantedId"`
		Title    string `json:"title2"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || (body.Source == "" && body.Release == "") {
		http.Error(w, "release or source is required", http.StatusBadRequest)
		return
	}
	var err error
	if body.WantedID != "" {
		err = s.svc.GrabCandidate(r.Context(), body.WantedID, body.Candidate)
	} else {
		err = s.svc.GrabAdHoc(r.Context(), body.Candidate, body.Title, subFrom(r.Context()))
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, 200, map[string]string{"status": "grabbed"})
}

// listProfiles / saveProfile / deleteProfile back the settings tab.
func (s *Server) listProfiles(w http.ResponseWriter, r *http.Request) {
	out, err := s.svc.Profiles(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, 200, out)
}

func (s *Server) saveProfile(w http.ResponseWriter, r *http.Request) {
	if !hasRole(r.Context(), s.cfg.AdminRole) {
		http.Error(w, "forbidden: requires "+s.cfg.AdminRole, http.StatusForbidden)
		return
	}
	var p store.QualityProfile
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, "invalid profile", http.StatusBadRequest)
		return
	}
	p.ID = chi.URLParam(r, "id")
	if strings.TrimSpace(p.Name) == "" {
		p.Name = p.ID
	}
	if err := s.svc.SaveProfile(r.Context(), p); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, 200, p)
}

func (s *Server) deleteProfile(w http.ResponseWriter, r *http.Request) {
	if !hasRole(r.Context(), s.cfg.AdminRole) {
		http.Error(w, "forbidden: requires "+s.cfg.AdminRole, http.StatusForbidden)
		return
	}
	if err := s.svc.DeleteProfile(r.Context(), chi.URLParam(r, "id")); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// listDownloads returns live + recently finished downloads.
func (s *Server) listDownloads(w http.ResponseWriter, r *http.Request) {
	items, err := s.svc.Downloads(r.Context(), 100)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, 200, items)
}

// listClients reports per-download-client health and aggregate speed. Best
// effort: an unreachable gateway yields an empty list, not an error page.
func (s *Server) listClients(w http.ResponseWriter, r *http.Request) {
	items, err := s.svc.ClientsStatus(r.Context())
	if err != nil {
		writeJSON(w, 200, []any{})
		return
	}
	writeJSON(w, 200, items)
}

// controlDownload pauses, resumes or cancels a client job (admin only).
func (s *Server) controlDownload(w http.ResponseWriter, r *http.Request) {
	if !hasRole(r.Context(), s.cfg.AdminRole) {
		http.Error(w, "forbidden: requires "+s.cfg.AdminRole, http.StatusForbidden)
		return
	}
	action := chi.URLParam(r, "action")
	switch action {
	case "pause", "resume", "cancel":
	default:
		http.Error(w, "unknown action", http.StatusBadRequest)
		return
	}
	err := s.svc.ControlDownload(r.Context(), chi.URLParam(r, "adapter"), chi.URLParam(r, "id"), action)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, 200, map[string]string{"status": action})
}

// listWanted lists the requests, newest first. One that follows a series or a
// movie reads as what acquire does with the title now, with its progress.
func (s *Server) listWanted(w http.ResponseWriter, r *http.Request) {
	items, err := s.svc.Requests(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if items == nil {
		items = []app.RequestView{}
	}
	writeJSON(w, 200, items)
}

// createWanted records a request. One for a series or a movie with a TMDB id
// follows the title; asking for one again answers the request there is.
func (s *Server) createWanted(w http.ResponseWriter, r *http.Request) {
	if !hasRole(r.Context(), s.cfg.UserRole) && !hasRole(r.Context(), s.cfg.AdminRole) {
		http.Error(w, "forbidden: requires "+s.cfg.UserRole, http.StatusForbidden)
		return
	}
	var in store.Wanted
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || strings.TrimSpace(in.Title) == "" {
		http.Error(w, "title is required", http.StatusBadRequest)
		return
	}
	out, created, err := s.svc.Request(r.Context(), in, app.Requester{Sub: subFrom(r.Context()), Admin: hasRole(r.Context(), s.cfg.AdminRole)})
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	view, err := s.svc.RequestView(r.Context(), out)
	if err != nil {
		view = app.RequestView{Wanted: out}
	}
	code := http.StatusCreated
	if !created {
		code = http.StatusOK
	}
	writeJSON(w, code, view)
}

// decideWanted approves or declines a request that follows a title (admin).
// An approved request's title is grabbed on its own as the search and grab
// policy allows; a declined one stays, saying so, and its follow ends.
//
//	POST /api/wanted/{id}/approve
//	POST /api/wanted/{id}/decline
func (s *Server) decideWanted(approve bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.requireAdmin(w, r) {
			return
		}
		decide := s.svc.DeclineRequest
		if approve {
			decide = s.svc.ApproveRequest
		}
		out, err := decide(r.Context(), chi.URLParam(r, "id"), actorFrom(r.Context()))
		switch {
		case errors.Is(err, store.ErrNotFound):
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		case app.IsNoFollow(err):
			http.Error(w, err.Error(), http.StatusConflict)
			return
		case err != nil:
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		view, err := s.svc.RequestView(r.Context(), out)
		if err != nil {
			view = app.RequestView{Wanted: out}
		}
		writeJSON(w, http.StatusOK, view)
	}
}

// deleteWanted removes a request; the last one that follows a title ends its
// follow.
func (s *Server) deleteWanted(w http.ResponseWriter, r *http.Request) {
	if !hasRole(r.Context(), s.cfg.AdminRole) {
		http.Error(w, "forbidden: requires "+s.cfg.AdminRole, http.StatusForbidden)
		return
	}
	if err := s.svc.RemoveRequest(r.Context(), chi.URLParam(r, "id")); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) grabWanted(w http.ResponseWriter, r *http.Request) {
	if !hasRole(r.Context(), s.cfg.AdminRole) {
		http.Error(w, "forbidden: requires "+s.cfg.AdminRole, http.StatusForbidden)
		return
	}
	var body struct {
		Source string `json:"source"`
		// Client names a download client id; adapter is its older spelling.
		// Neither: route by what the source turns out to be.
		Client  string `json:"client"`
		Adapter string `json:"adapter"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Source) == "" {
		http.Error(w, "source is required", http.StatusBadRequest)
		return
	}
	client := body.Client
	if client == "" {
		client = body.Adapter
	}
	if err := s.svc.ApproveForGrab(r.Context(), chi.URLParam(r, "id"), actorFrom(r.Context())); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := s.svc.Grab(r.Context(), chi.URLParam(r, "id"), body.Source, client); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, 200, map[string]string{"status": "grabbed"})
}

// autograbWanted searches the search sources and grabs the best release.
func (s *Server) autograbWanted(w http.ResponseWriter, r *http.Request) {
	if !hasRole(r.Context(), s.cfg.AdminRole) {
		http.Error(w, "forbidden: requires "+s.cfg.AdminRole, http.StatusForbidden)
		return
	}
	if err := s.svc.ApproveForGrab(r.Context(), chi.URLParam(r, "id"), actorFrom(r.Context())); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := s.svc.AutoGrab(r.Context(), chi.URLParam(r, "id")); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, 200, map[string]string{"status": "grabbing"})
}

func (s *Server) discover(w http.ResponseWriter, r *http.Request) {
	hits := s.svc.Discover(r.Context(), r.URL.Query().Get("q"))
	if hits == nil {
		hits = []app.DiscoverHit{}
	}
	writeJSON(w, 200, hits)
}

// status returns the newest request state for a tmdb id (the chino slot's feed).
func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	var tmdbID int64
	if v := r.URL.Query().Get("tmdbId"); v != "" {
		for _, c := range v {
			if c < '0' || c > '9' {
				tmdbID = 0
				break
			}
			tmdbID = tmdbID*10 + int64(c-'0')
		}
	}
	if tmdbID == 0 {
		writeJSON(w, 200, map[string]any{"status": ""})
		return
	}
	wnt, err := s.svc.RequestStatus(r.Context(), tmdbID)
	if err != nil {
		writeJSON(w, 200, map[string]any{"status": ""})
		return
	}
	writeJSON(w, 200, map[string]any{"status": wnt.Status, "id": wnt.ID})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// simulateScore ranks hypothetical releases against a profile without touching
// an indexer or a download client. Scoring is pure, so this is the only way to
// prove on a LIVE pod what the ranker will actually do — the alternative is
// grabbing something to find out. Admin-only: it reveals the profile config.
//
// POST /api/score/simulate
//
//	{"profileId":"default",              // omitted -> the default profile
//	 "candidates":[{"title":"…","protocol":"usenet","sizeMb":7000,"seeders":0}]}
func (s *Server) simulateScore(w http.ResponseWriter, r *http.Request) {
	if !hasRole(r.Context(), s.cfg.AdminRole) {
		http.Error(w, "forbidden: requires "+s.cfg.AdminRole, http.StatusForbidden)
		return
	}
	var body struct {
		ProfileID  string              `json:"profileId"`
		Profile    *release.Profile    `json:"profile"`
		Candidates []release.Candidate `json:"candidates"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	if len(body.Candidates) == 0 {
		http.Error(w, "candidates is required", http.StatusBadRequest)
		return
	}
	if len(body.Candidates) > 200 {
		http.Error(w, "at most 200 candidates", http.StatusBadRequest)
		return
	}

	// An inline profile lets an operator try a change before saving it;
	// otherwise use the stored one so this reports what would REALLY happen.
	var prof release.Profile
	var usedID string
	if body.Profile != nil {
		prof, usedID = *body.Profile, "(inline)"
	} else {
		prof, usedID = s.svc.ScoringProfile(r.Context(), body.ProfileID)
	}

	type scored struct {
		Title    string       `json:"title"`
		Score    int          `json:"score"`
		Rejected bool         `json:"rejected"`
		Reason   string       `json:"reason"`
		Reasons  []string     `json:"reasons"`
		Info     release.Info `json:"info"`
	}
	out := make([]scored, 0, len(body.Candidates))
	for _, c := range body.Candidates {
		v, in := release.Score(c, prof)
		out = append(out, scored{
			Title: c.Title, Score: v.Score, Rejected: v.Rejected,
			Reason: v.Summary(), Reasons: v.Reasons, Info: in,
		})
	}
	// Best first, rejections last — the same order the picker sees.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Rejected != out[j].Rejected {
			return !out[i].Rejected
		}
		return out[i].Score > out[j].Score
	})
	writeJSON(w, 200, map[string]any{"profileId": usedID, "results": out})
}

// importIntent pulls tracked titles out of an incumbent automation service.
//
// An explicit operator action, NOT a boot backfill. A one-way data change on a
// startup path turns a data problem into an outage class: there is no version
// row to skip past, and the pod CrashLoopBackOffs with the fix unreachable.
// dryRun is the default so the first thing anyone does is look.
//
//	POST /api/import/intent
//	{"baseUrl":"http://series:8989","apiKey":"…","kind":"series","apply":false}
func (s *Server) importIntent(w http.ResponseWriter, r *http.Request) {
	if !hasRole(r.Context(), s.cfg.AdminRole) {
		http.Error(w, "forbidden: requires "+s.cfg.AdminRole, http.StatusForbidden)
		return
	}
	var body struct {
		BaseURL string `json:"baseUrl"`
		APIKey  string `json:"apiKey"`
		Kind    string `json:"kind"`
		Apply   bool   `json:"apply"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	if body.BaseURL == "" || (body.Kind != "series" && body.Kind != "movie") {
		http.Error(w, `baseUrl is required and kind must be "series" or "movie"`, http.StatusBadRequest)
		return
	}
	res, err := s.svc.ImportIntent(r.Context(), importer.Source{
		BaseURL: body.BaseURL, APIKey: body.APIKey, Kind: body.Kind,
	}, !body.Apply)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, 200, res)
}

// deriveInv turns tracked series into acquisition targets from TMDB.
//
//	POST /api/import/inventory  {"limit":10,"apply":false}
func (s *Server) deriveInv(w http.ResponseWriter, r *http.Request) {
	if !hasRole(r.Context(), s.cfg.AdminRole) {
		http.Error(w, "forbidden: requires "+s.cfg.AdminRole, http.StatusForbidden)
		return
	}
	var body struct {
		Limit int  `json:"limit"`
		Apply bool `json:"apply"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	res, err := s.svc.DeriveInventory(r.Context(), s.svc.TMDB(), body.Limit, !body.Apply)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, 200, res)
}

// listSeries is the Series view: every tracked series with its acquisition
// progress. Counts are aggregated in one query rather than per series — 402
// rows makes an N+1 immediately visible.
func (s *Server) listSeries(w http.ResponseWriter, r *http.Request) {
	out, err := s.st.SeriesOverview(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, 200, map[string]any{"series": out})
}

// calendar answers "what airs when" — a capability nothing in the stack has
// today, because until now no air date was persisted anywhere.
//
//	GET /api/calendar?days=14&back=7
func (s *Server) calendar(w http.ResponseWriter, r *http.Request) {
	days := intParam(r, "days", 14, 1, 90)
	back := intParam(r, "back", 7, 0, 90)
	now := time.Now()
	out, err := s.st.Calendar(r.Context(), now.AddDate(0, 0, -back), now.AddDate(0, 0, days))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, 200, map[string]any{"episodes": out})
}

// missing is the backlog: monitored, aired, not held, of titles whose holdings
// acquire knows — with what is under way for each. Rows in search backoff are
// INCLUDED and flagged rather than hidden — a backlog view that omits
// everything currently failing is the least useful version of itself. It says
// how many titles' holdings are unknown, and why, since those titles are left
// out, and what grabs on its own: everything missing (search.autoGrab), or
// the new things (search.autoGrabNew, aired in the last newWithinDays days),
// airDelayHours after their air date.
func (s *Server) missing(w http.ResponseWriter, r *http.Request) {
	out, err := s.st.Missing(r.Context(), intParam(r, "limit", 500, 1, 5000))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	holdings, err := s.st.HoldingsSummary(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	pol := s.svc.GrabPolicy(r.Context())
	writeJSON(w, 200, map[string]any{"missing": out, "holdings": holdings,
		"autoGrab": pol.AutoGrab, "autoGrabNew": pol.AutoGrabNew, "newWithinDays": pol.NewWithinDays, "airDelayHours": pol.AirDelayHours})
}

func (s *Server) counts(w http.ResponseWriter, r *http.Request) {
	c, err := s.st.Counts(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, 200, c)
}

// intParam reads a bounded integer query parameter, so a hand-typed URL cannot
// ask for a million rows.
func intParam(r *http.Request, name string, def, lo, hi int) int {
	v, err := strconv.Atoi(r.URL.Query().Get(name))
	if err != nil {
		return def
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// searchTarget runs a typed search for one tracked target.
//
// Distinct from /api/search, which is free text on purpose: a human typing a
// query wants what they typed. A TARGET has ids and coordinates, so it can be
// searched precisely — and must be, because the ranker has no title term and
// will score a different show quite happily.
func (s *Server) searchTarget(w http.ResponseWriter, r *http.Request) {
	if !hasRole(r.Context(), s.cfg.AdminRole) {
		http.Error(w, "forbidden: requires "+s.cfg.AdminRole, http.StatusForbidden)
		return
	}
	out, err := s.svc.SearchTarget(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, 200, map[string]any{"candidates": out})
}

// history is the activity view. It reads the append-only table, which has no
// foreign key precisely so a record outlives the request it describes.
func (s *Server) history(w http.ResponseWriter, r *http.Request) {
	out, err := s.st.RecentHistory(r.Context(), intParam(r, "limit", 100, 1, 500))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, 200, map[string]any{"history": out})
}
