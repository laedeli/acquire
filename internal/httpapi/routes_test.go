package httpapi

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/laedeli/acquire/internal/app"
	"github.com/laedeli/acquire/internal/config"
)

// Every handler must be reachable.
//
// Go compiles an unrouted method perfectly happily — it is simply unused — so a
// route registration that silently fails to land produces a binary that builds,
// passes every unit test, deploys, and then serves a 301 to the SPA where the
// console expected JSON. That is exactly what happened: six handlers shipped
// defined-but-unrouted, and the only symptom was ERR_TOO_MANY_REDIRECTS in a
// browser.
//
// This reads the source rather than the router because chi exposes no reliable
// way to enumerate registered patterns without walking internals.
func TestEveryHandlerIsRouted(t *testing.T) {
	src, err := os.ReadFile("httpapi.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)

	// Handlers live in every file of the package; routes are all registered
	// in httpapi.go.
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	// Handlers look like: func (s *Server) name(w http.ResponseWriter, r *http.Request) {
	// — no result, which is what separates them from helpers such as
	// requireAdmin that take the same parameters.
	defRE := regexp.MustCompile(`func \(s \*Server\) (\w+)\(w http\.ResponseWriter, \w+ \*http\.Request\) \{`)
	var unrouted []string
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		def, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range defRE.FindAllStringSubmatch(string(def), -1) {
			name := m[1]
			// A handler is routed if it appears as `s.<name>)` in a route call.
			if !strings.Contains(body, "s."+name+")") {
				unrouted = append(unrouted, f+": "+name)
			}
		}
	}
	if len(unrouted) > 0 {
		t.Errorf("handler(s) defined but never routed — their paths answer 404, not JSON: %v", unrouted)
	}
}

// An unknown /api path must 404, never redirect.
//
// http.FileServer answers a missing directory-ish path with 301 Moved
// Permanently, and browsers cache permanent redirects. When six handlers were
// briefly unrouted, every client that called them cached a 301 to the SPA — and
// kept looping on it long after the server was fixed, so redeploying appeared
// to do nothing. A 404 cannot poison a cache.
func TestUnknownAPIPathReturns404NotARedirect(t *testing.T) {
	s := &Server{}
	h := s.Handler()
	for _, p := range []string{"/api/nope", "/api/missing/deeper", "/api/"} {
		r := httptest.NewRequest(http.MethodGet, p, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code >= 300 && w.Code < 400 {
			t.Errorf("%s -> %d %q; a redirect on an API path gets cached and poisons the client",
				p, w.Code, w.Header().Get("Location"))
		}
	}
}

func request(h http.Handler, method, target string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(method, target, nil))
	return w
}

// acquire has no page of its own. A browser that asks it for one — its root,
// or any path the standalone console used to answer — is sent to acquire's app
// in the portal, with a search the console reads (?q=) and nothing else of the
// query: a stale sign-in callback must not reach the portal's. The redirect is
// temporary and carries no page.
func TestPagesSendTheBrowserToTheAppInThePortal(t *testing.T) {
	h := (&Server{cfg: config.Config{PlatformURL: "https://zaentrum.example.org"}}).Handler()
	app := "https://zaentrum.example.org/portal/app/acquire"
	for target, want := range map[string]string{
		"/":                     app,
		"/index.html":           app,
		"/requests":             app,
		"/acquire/":             app,
		"/?q=dune%20part%20two": app + "?q=dune+part+two",
		"/?code=abc&state=xyz":  app,
		"/?q=":                  app,
	} {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			w := request(h, method, target)
			if w.Code != http.StatusFound || w.Header().Get("Location") != want {
				t.Errorf("%s %s -> %d %q, want 302 %q", method, target, w.Code, w.Header().Get("Location"), want)
			}
			if w.Body.Len() != 0 {
				t.Errorf("%s %s answers a body: %q", method, target, w.Body.String())
			}
		}
	}
}

// Without the platform's address the browser goes to the portal's root on the
// host it asked. The portal's own paths are never sent there: on that host
// they would come straight back.
func TestPagesFallBackToThePortalsRoot(t *testing.T) {
	h := (&Server{}).Handler()
	for _, target := range []string{"/", "/?q=dune", "/index.html"} {
		if w := request(h, http.MethodGet, target); w.Code != http.StatusFound || w.Header().Get("Location") != "/portal/" {
			t.Errorf("GET %s -> %d %q, want 302 /portal/", target, w.Code, w.Header().Get("Location"))
		}
	}
	for _, target := range []string{"/portal", "/portal/", "/portal/app/acquire"} {
		if w := request(h, http.MethodGet, target); w.Code != http.StatusNotFound {
			t.Errorf("GET %s -> %d %q, want 404", target, w.Code, w.Header().Get("Location"))
		}
	}
}

// The bundle the portal loads is served: the remote entry exposing the
// console, and its stylesheet.
func TestTheConsoleBundleIsServed(t *testing.T) {
	h := (&Server{}).Handler()
	for target, ctype := range map[string]string{
		"/embed/assets/remoteEntry.js": "javascript",
		"/embed/assets/console.css":    "text/css",
	} {
		w := request(h, http.MethodGet, target)
		if w.Code != http.StatusOK || !strings.Contains(w.Header().Get("Content-Type"), ctype) || w.Body.Len() == 0 {
			t.Errorf("GET %s -> %d %q (%d bytes), want 200 %s", target, w.Code, w.Header().Get("Content-Type"), w.Body.Len(), ctype)
		}
		if strings.HasSuffix(target, ".js") && !strings.Contains(w.Body.String(), "./Console") {
			t.Errorf("%s does not expose ./Console", target)
		}
	}
}

// Nothing acquire serves is a page: the bundle holds no HTML, a directory is
// never listed, a missing file or a former asset of the standalone page is a
// 404, and only reading is ever sent to the portal.
func TestNoPageOfItsOwn(t *testing.T) {
	err := fs.WalkDir(webFS, ".", func(p string, _ fs.DirEntry, err error) error {
		if err == nil && strings.HasSuffix(p, ".html") {
			t.Errorf("embedded page %s", p)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	h := (&Server{cfg: config.Config{PlatformURL: "https://zaentrum.example.org"}}).Handler()
	for _, target := range []string{
		"/embed", "/embed/", "/embed/assets", "/embed/assets/", "/embed/index.html",
		"/embed/assets/missing.js", "/assets/index-dvpfoucm.js",
	} {
		if w := request(h, http.MethodGet, target); w.Code != http.StatusNotFound {
			t.Errorf("GET %s -> %d %q, want 404", target, w.Code, w.Header().Get("Location"))
		}
	}
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		if w := request(h, method, "/"); w.Code < 400 {
			t.Errorf("%s / -> %d, want an error", method, w.Code)
		}
	}
}

// /api/config carries what the console reads, and nothing for a sign-in of
// acquire's own: signing in is the portal's.
func TestConfigCarriesWhatTheConsoleReads(t *testing.T) {
	cfg := config.Config{AdminRole: "zaentrum-admin", OIDCIssuer: "https://sso.example.org/realms/example"}
	h := (&Server{cfg: cfg, svc: app.New(cfg, nil, nil, nil, nil, nil, nil)}).Handler()
	w := request(h, http.MethodGet, "/api/config")
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); w.Code != http.StatusOK || err != nil {
		t.Fatalf("GET /api/config -> %d %q (%v)", w.Code, w.Body.String(), err)
	}
	if len(got) != 2 || got["adminRole"] != "zaentrum-admin" || got["autoGrab"] != false {
		t.Errorf("GET /api/config = %v, want only adminRole zaentrum-admin and autoGrab false", got)
	}
}
