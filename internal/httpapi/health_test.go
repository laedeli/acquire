package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The health surface must never gate on auth: Prometheus scrapes without a
// bearer, and a diagnostic that needs working auth is useless precisely when
// auth is what broke.
func TestHealthSurfaceIsUnauthenticated(t *testing.T) {
	s := &Server{}
	for _, path := range []string{"/metrics", "/api/health/system"} {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		// No role in the context at all.
		switch path {
		case "/metrics":
			s.metrics(w, r)
		default:
			s.systemHealth(w, r)
		}
		if w.Code == http.StatusForbidden || w.Code == http.StatusUnauthorized {
			t.Errorf("%s returned %d — it must not require auth", path, w.Code)
		}
	}
}

// systemHealth must stay 200 even when degraded. acquire runs at replicas: 1,
// so a probe that fails on a degraded dependency removes the only pod and takes
// away the console you would use to fix it.
func TestSystemHealthStays200WhenDegraded(t *testing.T) {
	s := &Server{} // nil store: every check fails
	r := httptest.NewRequest(http.MethodGet, "/api/health/system", nil)
	w := httptest.NewRecorder()
	s.systemHealth(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d — a degraded system must still answer 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "degraded") {
		t.Errorf("a failing system did not report degraded: %s", w.Body.String())
	}
}

// The exposition must be valid Prometheus text even when everything is broken —
// a scrape that returns malformed output loses the metrics that would explain
// the breakage.
func TestMetricsIsWellFormedWhenEverythingFails(t *testing.T) {
	s := &Server{}
	r := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	w := httptest.NewRecorder()
	s.metrics(w, r)
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("content-type = %q", ct)
	}
	for _, line := range strings.Split(strings.TrimSpace(w.Body.String()), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if len(strings.Fields(line)) != 2 {
			t.Errorf("malformed sample: %q", line)
		}
	}
}
