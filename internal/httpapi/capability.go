package httpapi

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/laedeli/acquire/internal/config"
)

// The capability descriptor: how acquire extends the zae CLI without zae ever
// compiling in its name. The platform's portal aggregates this document from
// /.well-known/zaentrum-capability.json on every running service; zae renders
// what it finds. Install the addon and the commands exist; remove it and they
// are gone — the CLI equivalent of "zero rows, zero UI".
//
// Contract: capability schema v1, documented platform-side
// (zaentrum wiki → extending-cli). Two self-imposed rules:
//   - every command here must be a REAL route — capability_test.go walks the
//     router and fails the build on drift, because a CLI surface that
//     describes an unshipped API is the documentation bug all over again;
//   - paths are service-relative. The caller reaches them through whatever
//     front door it has (in-cluster, or the portal's app proxy).

type capCommand struct {
	Name    string `json:"name"`
	Summary string `json:"summary"`
	Method  string `json:"method"`
	Path    string `json:"path"`
	Role    string `json:"role,omitempty"`
}

type capCheck struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// capUI is what the addon contributes to the portal and the product apps. The
// platform reads it when an admin installs the addon in settings and creates
// the app, the tile and the slot rows itself. Slot URLs are portal-relative;
// the platform absolutises them at install.
type capUI struct {
	App     capApp    `json:"app"`
	Console bool      `json:"console"`
	Space   *capSpace `json:"space,omitempty"`
	Tiles   []capTile `json:"tiles,omitempty"`
	Slots   []capSlot `json:"slots"`
}

// capSpace is a launchpad section of this addon's own. It goes away with the
// addon, so nothing of ours is left sitting in someone else's section.
type capSpace struct {
	Key   string `json:"key"`
	Title string `json:"title"`
	Ord   int    `json:"ord"`
}

// capTile is one entry point into this console. Targets are hash routes of
// the SPA — the places an operator actually starts from, not a mirror of
// every view the app has.
type capTile struct {
	Key         string `json:"key"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Icon        string `json:"icon"`
	Target      string `json:"target"`
	Ord         int    `json:"ord"`
}

type capApp struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Icon        string `json:"icon"`
}

type capSlot struct {
	Key   string `json:"key"`
	Slot  string `json:"slot"`
	Kind  string `json:"kind"`
	Label string `json:"label"`
	Icon  string `json:"icon"`
	URL   string `json:"url"`
	Ord   int    `json:"ord"`
}

// capComponent is one workload of the addon. The platform matches it by
// workload name to what is actually deployed in the addon's namespace and shows
// the group's state; it never deploys anything itself.
type capComponent struct {
	Name     string   `json:"name"`
	Workload string   `json:"workload"`
	Role     string   `json:"role"` // primary | required | optional
	Summary  string   `json:"summary"`
	Topics   []string `json:"topics,omitempty"`
}

// capSetup points the platform at acquire's own checklist (a GET path; the
// status contract is internal/app/setup.go) and names the console tab where
// each item is edited. Configuration values never leave acquire.
type capSetup struct {
	Path     string            `json:"path"`
	Sections []capSetupSection `json:"sections"`
}

type capSetupSection struct {
	Key         string `json:"key"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Required    bool   `json:"required"`
	Target      string `json:"target"`
	Ord         int    `json:"ord"`
}

type capability struct {
	Service    string         `json:"service"`
	Kind       string         `json:"kind"`
	Version    string         `json:"version,omitempty"`
	Commands   []capCommand   `json:"commands"`
	Checks     []capCheck     `json:"checks"`
	Topics     []string       `json:"topics"`
	Components []capComponent `json:"components,omitempty"`
	Setup      *capSetup      `json:"setup,omitempty"`
	UI         *capUI         `json:"ui,omitempty"`
}

// capVersion is stamped by the image build when it can; "": omitted.
var capVersion = ""

// dnsLabel is a DNS-1123 label: what a workload or component name must be.
var dnsLabel = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)

// gatewayWorkload is the gateway's Deployment and Service name: the first label
// of the host acquire reaches it at (http://download-gateway:8080 gives
// download-gateway). "" when no gateway is configured or the host is not a
// service name (an IP address, say), since then there is nothing to match.
func gatewayWorkload(gatewayURL string) string {
	u, err := url.Parse(strings.TrimSpace(gatewayURL))
	if err != nil || u.Hostname() == "" {
		return ""
	}
	label, _, _ := strings.Cut(strings.ToLower(u.Hostname()), ".")
	if !dnsLabel.MatchString(label) || strings.Trim(label, "0123456789") == "" {
		return ""
	}
	return label
}

// capabilityComponents declares acquire itself and the gateway it drives. The
// download clients are external endpoints configured inside acquire, not
// components of the addon.
func capabilityComponents(cfg config.Config) []capComponent {
	out := []capComponent{{
		Name:     "acquire",
		Workload: "acquire",
		Role:     "primary",
		Summary:  "requests, search, grab decisions and the console; serves this manifest",
		Topics:   []string{"acquire.schedule.due", "acquire.schedule.saga.due"},
	}}
	if wl := gatewayWorkload(cfg.GatewayURL); wl != "" && wl != "acquire" {
		out = append(out, capComponent{
			Name:     "download-gateway",
			Workload: wl,
			Role:     "required",
			Summary:  "the download plane: runs the configured download clients and reports their progress",
			Topics: []string{
				"download.client.started",
				"download.client.progress",
				"download.client.completed",
				"download.client.failed",
			},
		})
	}
	return out
}

func capabilityDoc(cfg config.Config) capability {
	return capability{
		Service: "acquire",
		Kind:    "addon",
		Version: capVersion,
		// A curated surface, not a route dump: the commands an operator
		// reaches for at a terminal. Role annotations let the CLI hide or
		// fail early; the API enforces them regardless.
		Commands: []capCommand{
			{Name: "wanted", Summary: "list requests and their state", Method: "GET", Path: "/api/wanted"},
			{Name: "request", Summary: "request a title (tmdb id)", Method: "POST", Path: "/api/wanted", Role: "user"},
			{Name: "grab", Summary: "grab a specific release for a request", Method: "POST", Path: "/api/wanted/{id}/grab", Role: "admin"},
			{Name: "autograb", Summary: "search the search sources and grab the best release", Method: "POST", Path: "/api/wanted/{id}/autograb", Role: "admin"},
			{Name: "discover", Summary: "search for titles to request", Method: "GET", Path: "/api/discover"},
			{Name: "downloads", Summary: "list in-flight downloads", Method: "GET", Path: "/api/downloads"},
			{Name: "missing", Summary: "the backlog: monitored, aired, still wanted", Method: "GET", Path: "/api/missing"},
			{Name: "series", Summary: "tracked series with acquisition progress", Method: "GET", Path: "/api/series"},
			{Name: "calendar", Summary: "upcoming and recent episodes", Method: "GET", Path: "/api/calendar"},
			{Name: "status", Summary: "counts and pipeline state", Method: "GET", Path: "/api/status"},
			{Name: "setup", Summary: "is acquire configured: sources, clients, grab policy", Method: "GET", Path: "/api/setup", Role: "admin"},
			{Name: "sources", Summary: "the search sources", Method: "GET", Path: "/api/indexers", Role: "admin"},
			{Name: "clients", Summary: "the download clients and their live state", Method: "GET", Path: "/api/download-clients", Role: "admin"},
		},
		Checks: []capCheck{
			// Deliberately the unauthenticated diagnostic: useful exactly when
			// auth is what broke. See systemHealth.
			{Name: "system", Path: "/api/health/system"},
		},
		// What acquire itself emits. The download.client.* topics belong to the
		// gateway component, which is where they come from.
		Topics:     []string{"acquire.schedule.due", "acquire.schedule.saga.due"},
		Components: capabilityComponents(cfg),
		Setup: &capSetup{
			Path: "/api/setup",
			Sections: []capSetupSection{
				{Key: "sources", Title: "search sources", Description: "where acquire searches for releases", Required: true, Target: "#/indexers", Ord: 10},
				{Key: "clients", Title: "download clients", Description: "the download programs acquire hands releases to", Required: true, Target: "#/clients", Ord: 20},
				{Key: "grab-policy", Title: "search and grab", Description: "protocol preference, free-space floor and concurrency", Required: false, Target: "#/settings", Ord: 30},
			},
		},
		UI: &capUI{
			App: capApp{
				Title:       "acquire",
				Description: "requests and downloads",
				Icon:        "download",
			},
			Console: true,
			// The layout an operator wants on day one, declared rather than
			// hand-built per instance: acquire's own section with the places
			// worth starting from.
			Space: &capSpace{Key: "acquire", Title: "acquire", Ord: 30},
			Tiles: []capTile{
				{Key: "requests", Title: "requests", Description: "who asked for what", Icon: "download", Target: "#/requests", Ord: 10},
				{Key: "downloads", Title: "downloads", Description: "the queue, live", Icon: "gauge", Target: "#/downloads", Ord: 20},
				{Key: "search", Title: "search", Description: "across all search sources", Icon: "radar", Target: "#/search", Ord: 30},
				{Key: "indexers", Title: "search sources", Description: "where releases are searched", Icon: "globe", Target: "#/indexers", Ord: 40},
				{Key: "clients", Title: "clients", Description: "where downloads run", Icon: "server", Target: "#/clients", Ord: 45},
				{Key: "settings", Title: "quality profiles", Description: "what counts as good", Icon: "settings", Target: "#/settings", Ord: 50},
			},
			// The one row the addon contributes: "Request this" on an empty
			// search, carrying the query into the discover view. Key matches
			// the row the reference deploy registered by hand, so installing
			// through the platform is idempotent over an existing install.
			Slots: []capSlot{{
				Key:   "search-request",
				Slot:  "search.empty",
				Kind:  "link",
				Label: "Request this",
				Icon:  "download",
				URL:   "/portal/app/acquire?q={q}#/discover",
				Ord:   10,
			}},
		},
	}
}

// capabilityHandler serves the descriptor. Unauthenticated like /healthz and
// for the same reason: it is metadata about a surface, and the aggregator
// fetches it without credentials.
func (s *Server) capabilityHandler(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, capabilityDoc(s.cfg))
}
