package httpapi

import "net/http"

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

type capability struct {
	Service  string       `json:"service"`
	Kind     string       `json:"kind"`
	Version  string       `json:"version,omitempty"`
	Commands []capCommand `json:"commands"`
	Checks   []capCheck   `json:"checks"`
	Topics   []string     `json:"topics"`
	UI       *capUI       `json:"ui,omitempty"`
}

// capVersion is stamped by the image build when it can; "": omitted.
var capVersion = ""

func capabilityDoc() capability {
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
			{Name: "autograb", Summary: "search indexers and grab the best release", Method: "POST", Path: "/api/wanted/{id}/autograb", Role: "admin"},
			{Name: "discover", Summary: "search for titles to request", Method: "GET", Path: "/api/discover"},
			{Name: "downloads", Summary: "list in-flight downloads", Method: "GET", Path: "/api/downloads"},
			{Name: "missing", Summary: "the backlog: monitored, aired, still wanted", Method: "GET", Path: "/api/missing"},
			{Name: "series", Summary: "tracked series with acquisition progress", Method: "GET", Path: "/api/series"},
			{Name: "calendar", Summary: "upcoming and recent episodes", Method: "GET", Path: "/api/calendar"},
			{Name: "status", Summary: "counts and pipeline state", Method: "GET", Path: "/api/status"},
		},
		Checks: []capCheck{
			// Deliberately the unauthenticated diagnostic: useful exactly when
			// auth is what broke. See systemHealth.
			{Name: "system", Path: "/api/health/system"},
		},
		Topics: []string{
			"download.client.started",
			"download.client.progress",
			"download.client.completed",
			"download.client.failed",
		},
		UI: &capUI{
			App: capApp{
				Title:       "acquire",
				Description: "requests and downloads",
				Icon:        "download",
			},
			Console: true,
			// The layout an operator wants on day one, declared rather than
			// hand-built per instance: acquire's own section with the five
			// places worth starting from.
			Space: &capSpace{Key: "acquire", Title: "acquire", Ord: 30},
			Tiles: []capTile{
				{Key: "requests", Title: "requests", Description: "who asked for what", Icon: "download", Target: "#/requests", Ord: 10},
				{Key: "downloads", Title: "downloads", Description: "the queue, live", Icon: "gauge", Target: "#/downloads", Ord: 20},
				{Key: "search", Title: "search", Description: "across all indexers", Icon: "radar", Target: "#/search", Ord: 30},
				{Key: "indexers", Title: "indexers", Description: "configured sources", Icon: "globe", Target: "#/indexers", Ord: 40},
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
	writeJSON(w, http.StatusOK, capabilityDoc())
}
