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

	"github.com/laedeli/acquire/internal/config"
)

// testCfg is a deployment with a gateway, so the descriptor declares both
// components.
var testCfg = config.Config{GatewayURL: "http://download-gateway:8080"}

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
	for _, c := range capabilityDoc(testCfg).Commands {
		key := c.Method + " " + c.Path
		if !routes[key] {
			t.Errorf("descriptor declares %q but the router does not serve it", key)
		}
	}
	for _, c := range capabilityDoc(testCfg).Checks {
		if !routes["GET "+c.Path] {
			t.Errorf("descriptor declares check %q but GET %s is not routed", c.Name, c.Path)
		}
	}
}

// realRoutes walks the router into "METHOD /path" keys.
func realRoutes(t *testing.T) map[string]bool {
	t.Helper()
	h, ok := (&Server{}).Handler().(chi.Router)
	if !ok {
		t.Fatal("handler is not a chi router; the walk below needs one")
	}
	routes := map[string]bool{}
	_ = chi.Walk(h, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		routes[method+" "+strings.TrimSuffix(route, "/")] = true
		return nil
	})
	return routes
}

// consoleTabs parses the TABS list out of the console source, or skips when the
// source is not present.
func consoleTabs(t *testing.T) map[string]bool {
	t.Helper()
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
	return tabs
}

// The component group must be one the platform can match against what is
// deployed: DNS-1123 names and workloads, unique, exactly one primary.
func TestCapabilityComponentsAreWellFormed(t *testing.T) {
	comps := capabilityDoc(testCfg).Components
	if len(comps) != 2 {
		t.Fatalf("with a gateway configured acquire declares itself and the gateway: %+v", comps)
	}
	names, workloads := map[string]bool{}, map[string]bool{}
	primaries := 0
	for _, c := range comps {
		if !dnsLabel.MatchString(c.Name) || !dnsLabel.MatchString(c.Workload) {
			t.Errorf("component %+v: name and workload must be DNS-1123 labels", c)
		}
		if names[c.Name] || workloads[c.Workload] {
			t.Errorf("component %+v: duplicate name or workload", c)
		}
		names[c.Name], workloads[c.Workload] = true, true
		switch c.Role {
		case "primary":
			primaries++
		case "required", "optional":
		default:
			t.Errorf("component %s: role %q", c.Name, c.Role)
		}
		if c.Summary == "" || len(c.Summary) > 120 {
			t.Errorf("component %s: summary must be 1-120 characters", c.Name)
		}
	}
	if primaries != 1 {
		t.Errorf("exactly one primary component, got %d", primaries)
	}
	if comps[0].Workload != "acquire" || comps[0].Role != "primary" {
		t.Errorf("acquire itself is the primary: %+v", comps[0])
	}
	gw := comps[1]
	if gw.Workload != "download-gateway" || gw.Role != "required" {
		t.Errorf("gateway component: %+v", gw)
	}
	// The download events come from the gateway, so they are declared there
	// and no longer at the top level.
	for _, topic := range []string{"download.client.started", "download.client.completed"} {
		found := false
		for _, g := range gw.Topics {
			found = found || g == topic
		}
		if !found {
			t.Errorf("gateway component does not declare %s", topic)
		}
	}
	for _, topic := range capabilityDoc(testCfg).Topics {
		if strings.HasPrefix(topic, "download.client.") {
			t.Errorf("top-level topics still carry %s", topic)
		}
	}
}

func TestGatewayWorkloadComesFromTheGatewayAddress(t *testing.T) {
	cases := map[string]string{
		"http://download-gateway:8080":               "download-gateway",
		"http://dl-gw.media.svc.cluster.local:8080/": "dl-gw",
		"https://Download-Gateway":                   "download-gateway",
		"":                                           "",
		"http://10.0.0.5:8080":                       "",
		"http://under_score:8080":                    "",
	}
	for in, want := range cases {
		if got := gatewayWorkload(in); got != want {
			t.Errorf("gatewayWorkload(%q) = %q, want %q", in, got, want)
		}
	}
	// No gateway configured: nothing to match, so no component.
	if comps := capabilityDoc(config.Config{}).Components; len(comps) != 1 || comps[0].Role != "primary" {
		t.Errorf("without a gateway only acquire is declared: %+v", comps)
	}
}

// setup.path must be a real GET route, and every section must point at a tab
// the console renders — the same drift-killer the tiles have.
func TestCapabilitySetupIsServedAndLinked(t *testing.T) {
	setup := capabilityDoc(testCfg).Setup
	if setup == nil {
		t.Fatal("acquire declares its setup checklist")
	}
	if !strings.HasPrefix(setup.Path, "/") || strings.Contains(setup.Path, "//") || strings.Contains(setup.Path, "..") {
		t.Errorf("setup.path must be a relative path: %q", setup.Path)
	}
	if !realRoutes(t)["GET "+setup.Path] {
		t.Errorf("setup.path %s is not a GET route", setup.Path)
	}
	tabs := consoleTabs(t)
	keys := map[string]bool{}
	required := 0
	for _, sec := range setup.Sections {
		if !dnsLabel.MatchString(sec.Key) || keys[sec.Key] {
			t.Errorf("section key %q must be a unique DNS-1123 label", sec.Key)
		}
		keys[sec.Key] = true
		if sec.Title == "" || len(sec.Title) > 60 {
			t.Errorf("section %s: title must be 1-60 characters", sec.Key)
		}
		if !strings.HasPrefix(sec.Target, "#/") || !tabs[strings.TrimPrefix(sec.Target, "#/")] {
			t.Errorf("section %s targets %q, which is not a tab of the console", sec.Key, sec.Target)
		}
		if sec.Required {
			required++
		}
	}
	// The checklist keys are the ones GET /api/setup answers with.
	for _, k := range []string{"sources", "clients", "grab-policy"} {
		if !keys[k] {
			t.Errorf("setup does not declare section %q", k)
		}
	}
	if required != 2 {
		t.Errorf("sources and clients are required, grab-policy optional; %d required", required)
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
	ui := capabilityDoc(testCfg).UI
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
	ui := capabilityDoc(testCfg).UI
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
	tabs := consoleTabs(t)
	for _, tl := range capabilityDoc(testCfg).UI.Tiles {
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

// The configuration surface is reachable from a terminal, and only for admins.
func TestCapabilityDeclaresConfigurationCommands(t *testing.T) {
	want := map[string]string{"setup": "/api/setup", "sources": "/api/indexers", "clients": "/api/download-clients"}
	for _, c := range capabilityDoc(testCfg).Commands {
		if path, ok := want[c.Name]; ok {
			if c.Method != "GET" || c.Path != path || c.Role != "admin" {
				t.Errorf("command %s: %+v", c.Name, c)
			}
			delete(want, c.Name)
		}
	}
	if len(want) > 0 {
		t.Errorf("missing commands: %v", want)
	}
	var clientsTile bool
	for _, tl := range capabilityDoc(testCfg).UI.Tiles {
		if tl.Key == "indexers" && tl.Title != "search sources" {
			t.Errorf("the indexers tile is titled %q, want \"search sources\"", tl.Title)
		}
		clientsTile = clientsTile || (tl.Key == "clients" && tl.Target == "#/clients")
	}
	if !clientsTile {
		t.Error("no clients tile targeting #/clients")
	}
}
