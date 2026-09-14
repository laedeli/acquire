package httpapi

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
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
		t.Errorf("handler(s) defined but never routed — they will 301 to the SPA: %v", unrouted)
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
