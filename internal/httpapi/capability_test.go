package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
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

// The ui section is what the platform materialises on install; it must be
// self-consistent so that installing through settings cannot half-work.
func TestCapabilityUIIsInstallable(t *testing.T) {
	ui := capabilityDoc().UI
	if ui == nil || ui.App.Title == "" || !ui.Console {
		t.Fatalf("acquire declares an app with a console: %+v", ui)
	}
	if len(ui.Slots) != 1 || ui.Slots[0].Key != "search-request" || ui.Slots[0].Slot != "search.empty" {
		t.Fatalf("acquire contributes exactly the search-request row: %+v", ui.Slots)
	}
	if !strings.HasPrefix(ui.Slots[0].URL, "/portal/app/acquire") {
		t.Errorf("slot url must be portal-relative and open this addon: %q", ui.Slots[0].URL)
	}
}

// The declared launchpad layout must be installable as-is: its own section,
// entry points that are real routes of this console, no duplicate keys.
func TestCapabilityTilesAreInstallable(t *testing.T) {
	ui := capabilityDoc().UI
	if ui.Space == nil || ui.Space.Key != "acquire" {
		t.Fatalf("acquire brings its own launchpad section: %+v", ui.Space)
	}
	if len(ui.Tiles) == 0 {
		t.Fatal("acquire declares its entry points")
	}
	seen := map[string]bool{}
	for _, tl := range ui.Tiles {
		if tl.Key == "" || tl.Title == "" {
			t.Errorf("tile needs a key and a title: %+v", tl)
		}
		if seen[tl.Key] {
			t.Errorf("duplicate tile key %q — the platform would collapse them", tl.Key)
		}
		seen[tl.Key] = true
		// Targets open a view INSIDE this console, never somewhere else.
		if !strings.HasPrefix(tl.Target, "#/") {
			t.Errorf("tile %q target must be a hash route of this SPA: %q", tl.Key, tl.Target)
		}
		if tl.Description == "" {
			t.Errorf("tile %q should say what it is for", tl.Key)
		}
	}
}

// The same drift-killer the commands have, for the launchpad tiles: every
// declared target must be a tab the console actually renders. A renamed tab
// would otherwise leave a tile that opens the app on its fallback view, and
// nothing would fail until someone clicked it.
func TestCapabilityTileTargetsAreRealTabs(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "web", "src", "Console.tsx"))
	if err != nil {
		t.Skip("console source not present (source-only build):", err)
	}
	m := regexp.MustCompile(`(?s)const TABS = \[(.*?)\]`).FindSubmatch(src)
	if m == nil {
		t.Fatal("could not find the TABS list in web/src/Console.tsx — has the console been restructured?")
	}
	tabs := map[string]bool{}
	for _, q := range regexp.MustCompile(`'([^']+)'`).FindAllSubmatch(m[1], -1) {
		tabs[string(q[1])] = true
	}
	if len(tabs) == 0 {
		t.Fatal("parsed no tabs out of the TABS list")
	}
	for _, tl := range capabilityDoc().UI.Tiles {
		tab := strings.TrimPrefix(tl.Target, "#/")
		if !tabs[tab] {
			t.Errorf("tile %q targets %q, which is not a tab of the console (have: %v)", tl.Key, tl.Target, keysOf(tabs))
		}
	}
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
