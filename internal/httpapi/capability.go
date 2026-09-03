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

type capability struct {
	Service  string       `json:"service"`
	Kind     string       `json:"kind"`
	Version  string       `json:"version,omitempty"`
	Commands []capCommand `json:"commands"`
	Checks   []capCheck   `json:"checks"`
	Topics   []string     `json:"topics"`
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
	}
}

// capabilityHandler serves the descriptor. Unauthenticated like /healthz and
// for the same reason: it is metadata about a surface, and the aggregator
// fetches it without credentials.
func (s *Server) capabilityHandler(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, capabilityDoc())
}
