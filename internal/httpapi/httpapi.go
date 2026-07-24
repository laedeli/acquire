// Package httpapi is acquire's REST surface + OIDC auth. Commands are REST at
// the edges (request/grab/discover); live status is the SSE stream. Auth is
// issuer-only + realm-role checks: the user role may request, the admin role may
// grab/delete. Init is lazy + self-healing (recovers when the IdP comes up).
package httpapi

import (
	"context"
	"embed"
	"encoding/json"
	"io/fs"
	"net/http"
	"strings"
	"sync"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"

	"github.com/laedeli/acquire/internal/app"
	"github.com/laedeli/acquire/internal/config"
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
			ctx := context.WithValue(r.Context(), roleKey, []string{})
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
func hasRole(ctx context.Context, role string) bool {
	// Auth-disabled dev path stores an empty slice → allow.
	rs := rolesFrom(ctx)
	if rs == nil {
		return false
	}
	if len(rs) == 0 {
		return true
	}
	for _, r := range rs {
		if r == role {
			return true
		}
	}
	return false
}

//go:embed all:web
var webFS embed.FS

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
	// Unauthenticated discovery doc so the SPA can bootstrap OIDC (PKCE).
	r.Get("/api/config", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]any{
			"oidcIssuer":   s.cfg.OIDCIssuer,
			"oidcClientId": s.cfg.OIDCClientID,
			"adminRole":    s.cfg.AdminRole,
		})
	})
	r.Get("/readyz", func(w http.ResponseWriter, rq *http.Request) {
		if err := s.st.Ping(rq.Context()); err != nil {
			http.Error(w, "db down", http.StatusServiceUnavailable)
			return
		}
		writeJSON(w, 200, map[string]string{"status": "ready"})
	})

	// Payload-free change ping — public (EventSource can't send a bearer; the
	// stream carries no data, clients refetch the authed list on it).
	r.Get("/api/events", s.br.Handler)

	// Authenticated API.
	r.Group(func(r chi.Router) {
		r.Use(s.ver.middleware)
		r.Get("/api/wanted", s.listWanted)
		r.Post("/api/wanted", s.createWanted)         // user role
		r.Delete("/api/wanted/{id}", s.deleteWanted)  // admin
		r.Post("/api/wanted/{id}/grab", s.grabWanted) // admin
		r.Get("/api/discover", s.discover)
		r.Get("/api/status", s.status)
	})

	// Embedded SPA at /  (assets + index fallback).
	sub, _ := fs.Sub(webFS, "web")
	fileServer := http.FileServer(http.FS(sub))
	r.Handle("/*", spaFallback(sub, fileServer))
	return r
}

func (s *Server) listWanted(w http.ResponseWriter, r *http.Request) {
	items, err := s.st.ListWanted(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if items == nil {
		items = []store.Wanted{}
	}
	writeJSON(w, 200, items)
}

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
	out, err := s.svc.Request(r.Context(), in, subFrom(r.Context()))
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (s *Server) deleteWanted(w http.ResponseWriter, r *http.Request) {
	if !hasRole(r.Context(), s.cfg.AdminRole) {
		http.Error(w, "forbidden: requires "+s.cfg.AdminRole, http.StatusForbidden)
		return
	}
	if err := s.st.DeleteWanted(r.Context(), chi.URLParam(r, "id")); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
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
		Source  string `json:"source"`
		Adapter string `json:"adapter"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Source) == "" {
		http.Error(w, "source is required", http.StatusBadRequest)
		return
	}
	if err := s.svc.Grab(r.Context(), chi.URLParam(r, "id"), body.Source, body.Adapter); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, 200, map[string]string{"status": "grabbed"})
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
	wnt, err := s.st.FindWantedByTMDB(r.Context(), tmdbID)
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

// spaFallback serves static files, falling back to index.html for client routes.
func spaFallback(sub fs.FS, fileServer http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p == "" {
			p = "index.html"
		}
		if _, err := fs.Stat(sub, p); err != nil {
			// Not a real asset → serve index.html (SPA route).
			r2 := r.Clone(r.Context())
			r2.URL.Path = "/index.html"
			fileServer.ServeHTTP(w, r2)
			return
		}
		fileServer.ServeHTTP(w, r)
	}
}
