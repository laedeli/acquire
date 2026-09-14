package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/laedeli/acquire/internal/storage"
)

// Setup states, worst last.
const (
	SetupReady      = "ready"
	SetupDegraded   = "degraded"
	SetupNeedsSetup = "needs-setup"
)

// SetupSection is one line of the setup checklist. Summary is plain text for an
// operator, at most 200 characters, and never carries a credential.
type SetupSection struct {
	Key     string `json:"key"`
	State   string `json:"state"`
	Summary string `json:"summary"`
}

// SetupStatus is GET /api/setup: the addon's own answer to "is this usable?",
// which the platform shows next to the addon and links into the console.
type SetupStatus struct {
	State    string         `json:"state"`
	Sections []SetupSection `json:"sections"`
}

// setupRequired names the sections that decide the overall state. grab-policy
// is optional: its defaults are usable.
var setupRequired = map[string]bool{"sources": true, "clients": true}

func stateRank(s string) int {
	switch s {
	case SetupReady:
		return 0
	case SetupDegraded:
		return 1
	}
	return 2
}

// worstRequired is the overall state: the worst required section.
func worstRequired(sections []SetupSection) string {
	state := SetupReady
	for _, sec := range sections {
		if setupRequired[sec.Key] && stateRank(sec.State) > stateRank(state) {
			state = sec.State
		}
	}
	return state
}

// Setup evaluates the checklist.
func (s *Service) Setup(ctx context.Context) SetupStatus {
	cov := s.Coverage(ctx)
	sections := []SetupSection{
		s.sourcesSection(cov),
		s.clientsSection(ctx, cov),
		s.grabPolicySection(ctx),
	}
	for i := range sections {
		sections[i].Summary = clip(sections[i].Summary, 200)
	}
	return SetupStatus{State: worstRequired(sections), Sections: sections}
}

func (s *Service) sourcesSection(cov Coverage) SetupSection {
	sec := SetupSection{Key: "sources"}
	sum := cov.SourceSummary
	switch {
	case cov.SourcesErr != nil:
		sec.State, sec.Summary = SetupDegraded, "the search sources could not be read"
	case !cov.SourcesKnown:
		sec.State, sec.Summary = SetupNeedsSetup, "no search source is configured — add one"
	case sum.Total() == 0:
		sec.State, sec.Summary = SetupNeedsSetup, "no search source is enabled — enable or add one"
		if len(sum.Disabled) > 0 {
			sec.Summary += "; disabled: " + strings.Join(sum.Disabled, "; ")
		}
	case len(sum.Locked) > 0:
		sec.State = SetupDegraded
		sec.Summary = "the API key of " + strings.Join(sum.Locked, ", ") + " cannot be opened"
		if !s.box.Enabled() {
			sec.Summary += " — ACQUIRE_CONFIG_KEY is not set"
		} else {
			sec.Summary += " — was ACQUIRE_CONFIG_KEY changed without ACQUIRE_CONFIG_KEY_PREVIOUS?"
		}
	case sum.TotalUsable() == 0:
		sec.State = SetupDegraded
		sec.Summary = "no enabled source can be asked now: " + strings.Join(sum.Waiting, ", ")
	default:
		sec.State, sec.Summary = SetupReady, countsByProtocol(cov.Sources, "source")
		if len(sum.Waiting) > 0 {
			sec.Summary += "; waiting: " + strings.Join(sum.Waiting, ", ")
		}
		if len(sum.Disabled) > 0 {
			sec.Summary += "; disabled: " + strings.Join(sum.Disabled, "; ")
		}
	}
	return sec
}

func (s *Service) clientsSection(ctx context.Context, cov Coverage) SetupSection {
	sec := SetupSection{Key: "clients"}
	needs := func(msg string) SetupSection {
		sec.State, sec.Summary = SetupNeedsSetup, msg
		return sec
	}
	if !s.box.Enabled() {
		if p := s.box.Problem(); p != "" {
			return needs(p + " — credentials cannot be stored")
		}
		return needs("ACQUIRE_CONFIG_KEY is not set — credentials cannot be stored")
	}
	if !s.gw.Enabled() {
		return needs("DOWNLOAD_GATEWAY_URL is not set — acquire has no download gateway")
	}
	if cov.ClientsErr != nil {
		sec.State, sec.Summary = SetupDegraded, "the download clients could not be read"
		return sec
	}
	clients, err := s.st.ListDownloadClients(ctx)
	if err != nil {
		sec.State, sec.Summary = SetupDegraded, "the download clients could not be read"
		return sec
	}
	var enabled []string
	for _, c := range clients {
		if c.Enabled {
			enabled = append(enabled, fmt.Sprintf("%s (%s)", c.ID, strings.Join(c.Protocols, ", ")))
		}
	}
	if len(enabled) == 0 {
		return needs("no download client is enabled — add one")
	}

	var problems []string
	sync := s.syncStatus(ctx)
	switch {
	case !sync.Checked:
		problems = append(problems, "the download gateway has not been reached yet")
	case !sync.Reachable:
		problems = append(problems, "the download gateway is unreachable")
	case !sync.ConfigAPI:
		problems = append(problems, "the download gateway does not offer the configuration API — upgrade it and set ALLOWED_CLIENTS")
	case sync.GatewayRevision != sync.Revision:
		problems = append(problems, fmt.Sprintf("the gateway runs configuration revision %d, acquire holds %d", sync.GatewayRevision, sync.Revision))
	}
	if len(sync.Errors) > 0 {
		ids := make([]string, 0, len(sync.Errors))
		for _, e := range sync.Errors {
			ids = append(ids, e.ID)
		}
		problems = append(problems, "the gateway refused: "+strings.Join(ids, ", "))
	}
	if sync.Reachable && sync.ConfigAPI {
		cctx, cancel := context.WithTimeout(ctx, 4*time.Second)
		statuses, err := s.gw.ClientsStatus(cctx)
		cancel()
		if err == nil {
			var down []string
			for _, st := range statuses {
				id := st.ID
				if id == "" {
					id = st.Name
				}
				for _, c := range clients {
					if c.Enabled && c.ID == id && !st.Reachable {
						down = append(down, id)
					}
				}
			}
			if len(down) > 0 {
				sort.Strings(down)
				problems = append(problems, "unreachable: "+strings.Join(down, ", "))
			}
		}
	}
	for _, p := range cov.Gaps() {
		problems = append(problems, fmt.Sprintf("%s sources have no client that handles %s", p, p))
	}
	for _, c := range clients {
		if c.Enabled && c.LocalPath != "" && !readableDir(c.LocalPath) {
			problems = append(problems, fmt.Sprintf("%s: %s is not readable by acquire", c.ID, c.LocalPath))
		}
	}
	if len(problems) > 0 {
		sec.State, sec.Summary = SetupDegraded, strings.Join(problems, "; ")
		return sec
	}
	sec.State = SetupReady
	sec.Summary = fmt.Sprintf("%d enabled: %s", len(enabled), strings.Join(enabled, ", "))
	return sec
}

func (s *Service) grabPolicySection(ctx context.Context) SetupSection {
	sec := SetupSection{Key: "grab-policy", State: SetupReady}
	pol := s.grabPolicy(ctx)
	sec.Summary = fmt.Sprintf("prefer %s · refuse below %d GB free · at most %d downloads at once",
		pol.PreferProtocol, pol.StorageFloorGB, pol.MaxConcurrentGrabs)
	free, err := storage.Guard{Path: s.cfg.DownloadsRoot}.Free()
	switch {
	case err != nil:
		sec.State, sec.Summary = SetupDegraded, "cannot read free space where downloads land; grabs are refused"
	case free < pol.StorageFloorGB<<30:
		sec.State, sec.Summary = SetupDegraded,
			fmt.Sprintf("%d GB free is below the %d GB floor; grabs are refused", free>>30, pol.StorageFloorGB)
	}
	return sec
}

// readableDir reports whether acquire can list a directory.
func readableDir(p string) bool {
	f, err := os.Open(p)
	if err != nil {
		return false
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.IsDir() {
		return false
	}
	_, err = f.Readdirnames(1)
	return err == nil || errors.Is(err, io.EOF)
}

func nonZero(m map[string]int) []string {
	var out []string
	for k, v := range m {
		if v > 0 {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// countsByProtocol renders {"usenet":2,"torrent":5} as "7 enabled: 5 torrent, 2 usenet".
func countsByProtocol(m map[string]int, noun string) string {
	total := 0
	var parts []string
	for _, p := range nonZero(m) {
		total += m[p]
		parts = append(parts, fmt.Sprintf("%d %s", m[p], p))
	}
	plural := noun
	if total != 1 {
		plural += "s"
	}
	return fmt.Sprintf("%d %s enabled: %s", total, plural, strings.Join(parts, ", "))
}

// clip shortens s to at most n bytes, ending in an ellipsis when cut.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n - len("…")
	for cut > 0 && (s[cut]&0xC0) == 0x80 {
		cut-- // do not split a UTF-8 sequence
	}
	return s[:cut] + "…"
}
