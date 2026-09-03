package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// Every command the descriptor declares must be a real route with the declared
// method. This is the drift-killer: a CLI surface describing an unshipped API
// is the documentation bug that motivated the whole capability design, and a
// test is the only place cheap enough to stop it forever.
func TestCapabilityCommandsAreRealRoutes(t *testing.T) {
	s := &Server{}
	h, ok := s.Handler().(chi.Router)
	if !ok {
		t.Fatal("handler is not a chi router; the walk below needs one")
	}
	routes := map[string]bool{}
	_ = chi.Walk(h, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		routes[method+" "+strings.TrimSuffix(route, "/")] = true
		return nil
	})
	for _, c := range capabilityDoc().Commands {
		key := c.Method + " " + c.Path
		if !routes[key] {
			t.Errorf("descriptor declares %q but the router does not serve it", key)
		}
	}
	for _, c := range capabilityDoc().Checks {
		if !routes["GET "+c.Path] {
			t.Errorf("descriptor declares check %q but GET %s is not routed", c.Name, c.Path)
		}
	}
}

// The descriptor endpoint itself: unauthenticated, valid JSON, names itself.
func TestCapabilityEndpoint(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest(http.MethodGet, "/.well-known/zaentrum-capability.json", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("descriptor must be public: got %d", rec.Code)
	}
	var d struct {
		Service string `json:"service"`
		Kind    string `json:"kind"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&d); err != nil {
		t.Fatalf("descriptor is not valid JSON: %v", err)
	}
	if d.Service != "acquire" || d.Kind != "addon" {
		t.Fatalf("descriptor must identify itself: %+v", d)
	}
}
