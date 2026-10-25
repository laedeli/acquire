package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/laedeli/acquire/internal/endpoint"
	"github.com/laedeli/acquire/internal/indexer"
	"github.com/laedeli/acquire/internal/katalog"
	"github.com/laedeli/acquire/internal/release"
	"github.com/laedeli/acquire/internal/store"
	"github.com/laedeli/acquire/internal/zones"
)

// Search bounds. A source's daily allowance is the scarce resource, so these
// limit what a search may spend as much as how long it may take.
const (
	// maxSourceRequests is how many requests to search sources run at once,
	// across every search in the process.
	maxSourceRequests = 4
	// A manual search answers within manualDeadline with whatever arrived;
	// each source gets manualPerSource of it.
	manualDeadline  = 50 * time.Second
	manualPerSource = 20 * time.Second
	// Typed searches run from the backlog sweep, where nobody is waiting.
	typedPerSource = 45 * time.Second
	// A source that fails with a rate limit, a 5xx or a timeout is not asked
	// again for sourceBackoffBase, doubling per consecutive failure up to
	// sourceBackoffMax.
	sourceBackoffBase = 5 * time.Minute
	sourceBackoffMax  = 6 * time.Hour
	// releaseRefTTL bounds how long a search result can be grabbed from.
	releaseRefTTL = 24 * time.Hour
)

type searchTiming struct {
	manualDeadline  time.Duration
	manualPerSource time.Duration
	typedPerSource  time.Duration
}

var defaultTiming = searchTiming{
	manualDeadline: manualDeadline, manualPerSource: manualPerSource, typedPerSource: typedPerSource,
}

var (
	errNoSources = errors.New("no search source is configured — add one under search sources")
	// errGrabLimit: the source that offered a release has no downloads left
	// today. Another release, from another source, may still be grabbed.
	errGrabLimit = errors.New("daily release download limit reached")
	// errStaleRelease: a search result sealed by an earlier process, or too old.
	errStaleRelease = errors.New("this search result is no longer valid — search again")
	// errQueryLimit: a source has no queries left today, of the part of its
	// allowance the asker may spend.
	errQueryLimit = errors.New("daily query limit reached")
)

// Candidate is one release offered by a search, already scored by the active
// quality profile so the console can show what won and why.
type Candidate struct {
	Title      string `json:"title"`
	Indexer    string `json:"indexer"`
	Protocol   string `json:"protocol"`
	Size       int64  `json:"size"`
	Seeders    int    `json:"seeders"`
	Adapter    string `json:"adapter"`
	Source     string `json:"source"`
	Reason     string `json:"reason"`
	Best       bool   `json:"best"`
	Score      int    `json:"score"`
	Rejected   bool   `json:"rejected"`
	Resolution string `json:"resolution"`
	Codec      string `json:"codec"`
	SourceType string `json:"sourceType"`
	// Provenance: which search stage produced this and what identified it.
	// A console that shows "id" vs "text" tells an operator whether the match
	// is certain or merely plausible.
	Stage      string `json:"stage,omitempty"`
	MatchedVia string `json:"matchedVia,omitempty"`
	IndexerID  int64  `json:"indexerId,omitempty"`
	GUID       string `json:"guid,omitempty"`
	// Covers names the episodes a release holds (S04E07, S04E07-E08, S01) and
	// Pack marks whole seasons, for a target's search: a grab of it is bound
	// to every wanted target it holds.
	Covers string `json:"covers,omitempty"`
	Pack   bool   `json:"pack,omitempty"`
	// PubDate is when the source says the release was published; nil when
	// it says not.
	PubDate *time.Time `json:"pubDate,omitempty"`
	// Release is an opaque, sealed reference to the release's real link. A grab
	// sends it back. Source, which the console shows, is that link with its
	// credentials removed: a source's API key never reaches a browser.
	Release string `json:"release,omitempty"`

	// link is the real link, server-side only.
	link string
	// preAired is set on a release rejected for having been published before
	// its target aired (preAired): an automatic grab skips it, saying so; a
	// person may still pick it.
	preAired bool
}

// SearchResult is a manual search's answer.
type SearchResult struct {
	Candidates []Candidate `json:"candidates"`
	// Incomplete names the sources in scope that did not answer in full: timed
	// out, failed, or could not be asked (backing off, out of allowance, key
	// unreadable). A short list is then not mistaken for everything there is.
	Incomplete []string `json:"incomplete"`
}

// ── the fleet ──────────────────────────────────────────────────────────────

// fleet is who may be asked right now.
type fleet struct {
	configured bool              // any source row exists
	sources    []indexer.Source  // enabled and askable, keys opened
	limits     map[int64]*int    // daily query allowance per source
	unusable   []string          // names of enabled sources in scope that cannot be asked now
	reasons    map[string]string // why, by name
}

// sourceUnusable says why an enabled source cannot be asked now, or "".
func sourceUnusable(ix store.Indexer, now time.Time) string {
	if ix.BackoffUntil != nil && ix.BackoffUntil.After(now) {
		return "backing off until " + ix.BackoffUntil.UTC().Format("15:04 UTC")
	}
	if ix.QueryLimitDay != nil && ix.QueriesToday >= *ix.QueryLimitDay {
		return "daily query limit reached"
	}
	return ""
}

// loadFleet reads the sources; only, when non-empty, restricts it to those ids.
func (s *Service) loadFleet(ctx context.Context, only []int64) (fleet, error) {
	f := fleet{limits: map[int64]*int{}, reasons: map[string]string{}}
	if s.st == nil {
		return f, errNoSources
	}
	rows, err := s.st.ListIndexers(ctx)
	if err != nil {
		return f, fmt.Errorf("cannot read the search sources: %w", err)
	}
	f.configured = len(rows) > 0
	scope := map[int64]bool{}
	for _, id := range only {
		scope[id] = true
	}
	now := time.Now()
	for _, ix := range rows {
		if !ix.Enabled || (len(scope) > 0 && !scope[ix.ID]) {
			continue
		}
		reason := sourceUnusable(ix, now)
		src, err := s.openSource(ix)
		if err != nil {
			reason = "its API key cannot be opened"
		}
		if reason != "" {
			f.unusable = append(f.unusable, ix.Name)
			f.reasons[ix.Name] = reason
			continue
		}
		f.sources = append(f.sources, src)
		f.limits[ix.ID] = ix.QueryLimitDay
	}
	return f, nil
}

// byProtocol splits a fleet into the preferred protocol's sources and the rest.
func (f fleet) byProtocol(prefer string) (first, second fleet) {
	first = fleet{configured: f.configured, limits: f.limits, reasons: f.reasons}
	second = fleet{configured: f.configured, limits: f.limits, reasons: f.reasons}
	for _, src := range f.sources {
		if src.Protocol == prefer {
			first.sources = append(first.sources, src)
		} else {
			second.sources = append(second.sources, src)
		}
	}
	return first, second
}

// openSource turns a stored source into one that can be queried.
func (s *Service) openSource(ix store.Indexer) (indexer.Source, error) {
	src := indexer.Source{
		ID: ix.ID, Name: ix.Name, Protocol: ix.Protocol, BaseURL: ix.BaseURL, APIPath: ix.APIPath,
		Categories: indexer.Categories{Movie: ix.Categories.Movie, TV: ix.Categories.TV},
	}
	if len(ix.Caps) > 0 {
		var caps indexer.Caps
		if json.Unmarshal(ix.Caps, &caps) == nil {
			src.Caps = &caps
		}
	}
	if len(ix.APIKeyCT) > 0 {
		table, id, field := store.IndexerKeyAAD(ix.ID)
		key, err := s.box.Open(table, id, field, ix.APIKeyCT, ix.APIKeyKID)
		if err != nil {
			return indexer.Source{}, err
		}
		src.APIKey = string(key)
	}
	return src, nil
}

// ── one request ────────────────────────────────────────────────────────────

// slot waits for one of the process-wide request slots.
func (s *Service) slot(ctx context.Context) (func(), error) {
	select {
	case s.slots <- struct{}{}:
		return func() { <-s.slots }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// ask sends one query to one source, against its daily allowance and under its
// own timeout, and records the outcome as the source's health. The caller
// holds a slot.
func (s *Service) ask(ctx context.Context, src indexer.Source, limit *int, perSource time.Duration, q indexer.Query) ([]release.Release, error) {
	ok, err := s.st.ReserveIndexerQuery(ctx, src.ID, limit)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("%s: %w", src.Name, errQueryLimit)
	}
	cctx, cancel := context.WithTimeout(ctx, perSource)
	items, err := s.ixc.Search(cctx, src, q)
	cancel()
	s.recordOutcome(ctx, src, err)
	return items, err
}

// recordOutcome turns a request's result into the source's health: a rejected
// key disables the source, a rate limit or an outage backs it off, anything
// else is noted.
func (s *Service) recordOutcome(ctx context.Context, src indexer.Source, err error) {
	if err != nil && ctx.Err() != nil {
		// The search as a whole ran out of time or was abandoned. That is not
		// this source's failure.
		return
	}
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err == nil {
		_ = s.st.IndexerSucceeded(wctx, src.ID)
		return
	}
	msg := clip(err.Error(), 300)
	switch indexer.KindOf(err) {
	case indexer.KindCredentials:
		log.Printf("acquire: search source %s rejected its API key; disabling it: %s", src.Name, msg)
		_ = s.st.DisableIndexer(wctx, src.ID, clip(msg+" — disabled until the key is fixed", 300), "acquire")
	case indexer.KindRateLimited, indexer.KindUnavailable:
		log.Printf("acquire: search source %s: %s; backing off", src.Name, msg)
		_ = s.st.IndexerFailed(wctx, src.ID, msg, true, sourceBackoffBase, sourceBackoffMax)
	default:
		log.Printf("acquire: search source %s: %s", src.Name, msg)
		_ = s.st.IndexerFailed(wctx, src.ID, msg, false, 0, 0)
	}
}

// searchAll sends one query to every source of a fleet, at most
// maxSourceRequests at a time, taking slots in the fleet's order so the sources
// asked first are the ones that matter most. It returns what arrived and the
// names of the sources that did not answer.
func (s *Service) searchAll(ctx context.Context, f fleet, q indexer.Query, perSource time.Duration) ([]release.Release, []string) {
	var mu sync.Mutex
	var wg sync.WaitGroup
	var out []release.Release
	var incomplete []string
	for i, src := range f.sources {
		done, err := s.slot(ctx)
		if err != nil {
			// Out of time before these could even be asked.
			for _, rest := range f.sources[i:] {
				incomplete = append(incomplete, rest.Name)
			}
			break
		}
		wg.Add(1)
		go func(src indexer.Source) {
			defer wg.Done()
			defer done()
			items, err := s.ask(ctx, src, f.limits[src.ID], perSource, q)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				incomplete = append(incomplete, src.Name)
				return
			}
			out = append(out, items...)
		}(src)
	}
	wg.Wait()
	return out, incomplete
}

// ── searches ───────────────────────────────────────────────────────────────

// Search is the console's manual search: a free-text query across the enabled
// sources (optionally scoped to some of them), ranked by the active profile.
// It answers within the manual deadline with what arrived, naming the sources
// that did not.
func (s *Service) Search(ctx context.Context, query string, only []int64) (SearchResult, error) {
	return s.search(ctx, query, "", only)
}

func (s *Service) search(parent context.Context, query, media string, only []int64) (SearchResult, error) {
	ctx, cancel := context.WithTimeout(parent, s.timing.manualDeadline)
	defer cancel()
	f, err := s.loadFleet(ctx, only)
	if err != nil {
		return SearchResult{}, err
	}
	if !f.configured {
		return SearchResult{}, errNoSources
	}
	if len(f.sources) == 0 && len(f.unusable) == 0 {
		if len(only) > 0 {
			return SearchResult{}, errors.New("none of the chosen search sources is enabled")
		}
		return SearchResult{}, errors.New("no search source is enabled — enable one under search sources")
	}
	// The preferred protocol's sources take the first slots; within it, the
	// order the admin set.
	first, second := f.byProtocol(s.grabPolicy(ctx).PreferProtocol)
	f.sources = append(first.sources, second.sources...)

	releases, incomplete := s.searchAll(ctx, f, indexer.Query{Kind: "search", Term: query, Media: media}, s.timing.manualPerSource)
	incomplete = append(incomplete, f.unusable...)
	sort.Strings(incomplete)
	if incomplete == nil {
		incomplete = []string{}
	}
	// Ranking reads the profile and the clients after the deadline may have
	// passed; it is not part of the search's time budget.
	profile := s.st.DefaultProfile(parent)
	return SearchResult{Candidates: s.rank(parent, releases, profile), Incomplete: incomplete}, nil
}

// Releases runs the interactive search for one request — same ranking as
// auto-grab, but shown rather than acted on.
func (s *Service) Releases(ctx context.Context, wantedID string) (SearchResult, error) {
	w, err := s.st.GetWanted(ctx, wantedID)
	if err != nil {
		return SearchResult{}, err
	}
	// A request that follows its movie is the movie's target: its typed
	// search, by its identity.
	if t, _, follows, err := s.requestTarget(ctx, w); follows {
		if err != nil {
			return SearchResult{}, err
		}
		cands, err := s.SearchTarget(ctx, t.ID)
		if err != nil {
			return SearchResult{}, err
		}
		return SearchResult{Candidates: cands, Incomplete: []string{}}, nil
	}
	return s.search(ctx, wantedQuery(w), mediaOf(w), nil)
}

func wantedQuery(w store.Wanted) string {
	if w.Year != 0 {
		return w.Title + " " + itoa(w.Year)
	}
	return w.Title
}

func mediaOf(w store.Wanted) string {
	if w.MediaType == "series" || w.MediaType == "episode" {
		return "tv"
	}
	return "movie"
}

// AutoGrab searches for the request's title, ranks the releases by the active
// profile, and grabs the best one through the download client that handles its
// protocol.
//
// It searches the preferred protocol's sources first and the others only when
// they offer nothing usable: every query spends a source's allowance.
func (s *Service) AutoGrab(ctx context.Context, wantedID string) error {
	w, err := s.st.GetWanted(ctx, wantedID)
	if err != nil {
		return err
	}
	// A request that follows its movie is grabbed as the movie's target, so
	// the request, the sweep and the newest releases never grab it twice.
	if t, _, follows, err := s.requestTarget(ctx, w); follows {
		if err != nil {
			return err
		}
		_, err = s.GrabTarget(ctx, t.ID, "request")
		if err != nil {
			wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			defer cancel()
			s.targetGrabFailed(wctx, t, err)
		}
		s.notify()
		return err
	}
	f, err := s.loadFleet(ctx, nil)
	if err != nil {
		return err
	}
	if !f.configured {
		return errNoSources
	}
	if len(f.sources) == 0 {
		return fmt.Errorf("no search source can be asked right now%s", describeUnusable(f))
	}
	profile := s.st.DefaultProfile(ctx)
	first, second := f.byProtocol(s.grabPolicy(ctx).PreferProtocol)
	var ranked []Candidate
	var incomplete []string
	for _, stage := range []fleet{first, second} {
		if len(stage.sources) == 0 {
			continue
		}
		sctx, cancel := context.WithTimeout(ctx, s.timing.manualDeadline)
		releases, inc := s.searchAll(sctx, stage, indexer.Query{Kind: "search", Term: wantedQuery(w), Media: mediaOf(w)}, s.timing.manualPerSource)
		cancel()
		incomplete = append(incomplete, inc...)
		ranked = acceptable(s.rank(ctx, releases, profile))
		if len(ranked) > 0 {
			break
		}
	}
	if len(ranked) == 0 {
		detail := "no releases matched the quality profile"
		if len(incomplete) > 0 {
			sort.Strings(incomplete)
			detail += " (no answer from " + strings.Join(incomplete, ", ") + ")"
		}
		s.setStatus(ctx, wantedID, "failed", clip(detail, 300))
		return errNoReleases
	}
	var lastErr error
	for _, best := range ranked {
		out, err := s.handOff(ctx, candidateHandoff(w, best, best.Reason))
		if errors.Is(err, errGrabLimit) {
			// That source's downloads are spent for today; the next release
			// may come from another.
			lastErr = err
			continue
		}
		if err != nil {
			s.failGrab(ctx, wantedID, err)
			return err
		}
		s.setStatus(ctx, wantedID, "downloading",
			"grabbed "+protoLabel(out.Protocol)+" from "+best.Indexer+" via "+out.Client.ID)
		return nil
	}
	s.failGrab(ctx, wantedID, lastErr)
	return lastErr
}

// acceptable drops what the profile rejected; never auto-grab those.
func acceptable(ranked []Candidate) []Candidate {
	for len(ranked) > 0 && ranked[len(ranked)-1].Rejected {
		ranked = ranked[:len(ranked)-1]
	}
	return ranked
}

func describeUnusable(f fleet) string {
	if len(f.unusable) == 0 {
		return " — no search source is enabled"
	}
	parts := make([]string, 0, len(f.unusable))
	for _, name := range f.unusable {
		parts = append(parts, name+" ("+f.reasons[name]+")")
	}
	return ": " + strings.Join(parts, ", ")
}

// SearchTarget finds releases for one acquisition target using TYPED queries.
//
// This is the path that replaces free-text search for anything we actually
// track. The difference is not precision-for-its-own-sake: a free-text search
// for "Breaking Bad" returned "The.Bad.Guys.Breaking.In.S02E05" as its top hit
// in a live probe, and the ranker has no title term with which to reject it.
//
// The console's manual Search stays free-text on purpose — a human typing a
// query wants exactly what they typed.
func (s *Service) SearchTarget(ctx context.Context, targetID string) ([]Candidate, error) {
	t, title, err := s.st.TargetWithTitle(ctx, targetID)
	if err != nil {
		return nil, err
	}
	f, err := s.loadFleet(ctx, nil)
	if err != nil {
		return nil, err
	}
	if !f.configured {
		return nil, errNoSources
	}
	if len(f.sources) == 0 {
		return nil, fmt.Errorf("no search source can be asked right now%s", describeUnusable(f))
	}

	aliases, _ := s.st.AliasesFor(ctx, title.ID)
	kind := "series"
	if t.Kind == "movie" {
		kind = "movie"
	}
	engine := &indexer.Engine{Ask: func(ctx context.Context, src indexer.Source, q indexer.Query) ([]release.Release, error) {
		done, err := s.slot(ctx)
		if err != nil {
			return nil, err
		}
		defer done()
		return s.ask(ctx, src, f.limits[src.ID], s.timing.typedPerSource, q)
	}}
	results, err := engine.Search(ctx, indexer.Target{
		Title: title.Title, Aliases: aliases,
		TVDBID: title.TVDBID, IMDBID: title.IMDBID,
		Season: t.SeasonNumber, Episode: t.EpisodeNumber,
		Kind: kind, Year: title.Year,
	}, f.sources)
	if err != nil {
		return nil, err
	}

	profile, _ := s.ScoringProfile(ctx, title.ProfileID)
	routes := s.clientRoutes(ctx)
	// A release whose download failed for this target is not offered as the
	// best again: ranking is deterministic, so without this a grab after a
	// failure picks the identical release.
	blocked, err := s.st.BlockedFor(ctx, targetID)
	if err != nil {
		log.Printf("acquire: blocklist of %s unreadable, not applied: %v", targetID, err)
	}
	days := s.airDays(ctx, t)
	stamps := s.grabPolicy(ctx).exactAirTimes()
	out := make([]Candidate, 0, len(results))
	for _, r := range results {
		c := s.candidate(r.Release, profile, routes)
		// How it was found and what identified it, so the console can show
		// an id match as certain rather than merely plausible.
		c.Stage, c.MatchedVia = r.Stage, r.Match.Via
		if t.Kind != "movie" {
			cov := release.ParseCoverage(c.Title)
			c.Covers, c.Pack = cov.String(), cov.Pack()
		}
		markRejected(&c, title, t, blocked, days, stamps)
		out = append(out, c)
	}
	sortCandidates(out)
	return out, nil
}

// markRejected rejects, with the reason, a release blocked for the target, an
// extra of the title (extraOf), or one published before the target aired
// (preAired; stamps: the moments the schedule source gives are used). An
// automatic grab takes none of them; a person may pick one.
func markRejected(c *Candidate, title store.Title, t store.Target, blocked map[string]string, days func() map[[2]int]store.Target, stamps bool) {
	if why, ok := blocked[c.Title]; ok {
		c.Rejected, c.Reason = true, clip("blocked for this target: "+why+" · "+c.Reason, 300)
		return
	}
	if why := extraOf(c.Title, title, t); why != "" {
		c.Rejected, c.Reason = true, clip(why+" · "+c.Reason, 300)
		return
	}
	if why := preAired(*c, title, t, days, stamps); why != "" {
		c.Rejected, c.Reason, c.preAired = true, clip(why+" · "+c.Reason, 300), true
	}
}

// extraOf says why a release is an extra of its title rather than the title,
// or "": its name says trailer, teaser, featurette, extras, bonus, making of,
// behind the scenes, sample, interview or promo (release.Extra) after the
// movie's year or after the episodes it holds — never in the title's own
// words before them.
func extraOf(name string, title store.Title, t store.Target) string {
	rest, what := "", "the episode"
	if t.Kind == "movie" {
		what = "the movie"
		for i, at := range release.SplitYears(name) {
			if i == 0 || at.Year == title.Year {
				rest = at.Rest
			}
			if at.Year == title.Year {
				break
			}
		}
	} else if _, cov, after := release.SplitSeries(name); cov.Found {
		rest = after
	}
	if w := release.Extra(rest); w != "" {
		return fmt.Sprintf("its name says %q: an extra, not %s", w, what)
	}
	return ""
}

// preAired says why a release cannot be what its name says, or "" when it
// can: it was published before its target aired (airCut) — of a release of
// several episodes, the last of them to air —, or before the day a requested
// movie came out to watch at home. A release named for an episode before it
// was broadcast is most often a fake. A release whose source names no
// publication time is not told; days reads the episodes of the title, once,
// for a release of several.
func preAired(c Candidate, title store.Title, t store.Target, days func() map[[2]int]store.Target, stamps bool) string {
	if c.PubDate == nil {
		return ""
	}
	cut, aired, ok := airCut(title, t, stamps)
	what := coords(t)
	if t.Kind != "movie" {
		if cov := release.ParseCoverage(c.Title); cov.Multi() {
			m := days()
			for _, e := range cov.Episodes {
				x, found := m[[2]int{cov.Season, e}]
				if !found {
					continue
				}
				if xc, xa, xok := airCut(title, x, stamps); xok && (!ok || xc.After(cut)) {
					cut, aired, ok, what = xc, xa, true, coords(x)
				}
			}
		}
	}
	if !ok || !c.PubDate.Before(cut) {
		return ""
	}
	at := c.PubDate.UTC().Format("2006-01-02 15:04 UTC")
	if t.Kind == "movie" {
		return fmt.Sprintf("published before the movie came out (published %s; it %s): most likely a fake", at, aired)
	}
	return fmt.Sprintf("published before the episode aired (published %s; %s %s): most likely a fake", at, what, aired)
}

// airCut is the moment before which no real release of a target can exist,
// and how it is known. Of an episode whose air time the schedule source gives
// (stamps), airSlack before it; of any other episode, midnight of its air
// date where its series comes from (TMDB's origin country; UTC when it names
// none acquire knows) — TMDB dates the day as broadcast there, not the hour.
// Of a requested movie, midnight of the day it comes out to watch at home,
// where it does. ok is false when nothing dates it, and for a movie dated in
// cinemas only: no release to watch at home is dated to tell one by.
func airCut(title store.Title, t store.Target, stamps bool) (cut time.Time, aired string, ok bool) {
	if t.AirDate == nil {
		return time.Time{}, "", false
	}
	day := t.AirDate.UTC().Format("2006-01-02")
	if t.Kind == "movie" {
		how := map[string]string{"digital": "digitally", "physical": "on disc", "tv": "on TV"}[t.ReleaseKind]
		if how == "" {
			return time.Time{}, "", false
		}
		loc := zones.Of(append([]string{t.ReleaseCountry}, title.Countries...)...)
		return zones.Midnight(*t.AirDate, loc), fmt.Sprintf("came out %s on %s, %s", how, day, loc), true
	}
	if stamps && t.AirStamp != nil {
		return t.AirStamp.Add(-airSlack), "aired " + t.AirStamp.UTC().Format("2006-01-02 15:04 UTC"), true
	}
	loc := zones.Of(title.Countries...)
	return zones.Midnight(*t.AirDate, loc), fmt.Sprintf("aired on %s, %s", day, loc), true
}

// airDays reads, once and when asked, the episodes of a target's title, by
// season and episode: what preAired compares a release of several episodes
// with. None for a movie, or when they cannot be read.
func (s *Service) airDays(ctx context.Context, t store.Target) func() map[[2]int]store.Target {
	var m map[[2]int]store.Target
	return func() map[[2]int]store.Target {
		if m != nil {
			return m
		}
		m = map[[2]int]store.Target{}
		if t.Kind == "movie" {
			return m
		}
		all, err := s.st.TitleTargets(ctx, t.TitleID)
		if err != nil {
			return m
		}
		for _, x := range all {
			if x.SeasonNumber != nil && x.EpisodeNumber != nil {
				m[[2]int{*x.SeasonNumber, *x.EpisodeNumber}] = x
			}
		}
		return m
	}
}

// preAiredCount is how many of the releases found were published before
// their target aired.
func preAiredCount(cands []Candidate) int {
	n := 0
	for _, c := range cands {
		if c.preAired {
			n++
		}
	}
	return n
}

// onlyPreAired is a target's search and grab that found releases, every one
// an automatic grab takes published before the target aired: not taken on
// their own, but there to pick. It is no "nothing found".
type onlyPreAired struct {
	found, preAired int
	what            string
}

func (e onlyPreAired) Error() string {
	return fmt.Sprintf("%d release(s) found for %s, %d of them published before it aired — most likely fakes, not taken on their own; "+
		"pick one under pick a release if you are sure", e.found, e.what, e.preAired)
}

// Is makes it count as no release an automatic grab takes.
func (e onlyPreAired) Is(target error) bool { return target == errNoReleases }

// autoCandidates are the releases an automatic grab of a target may take —
// the sweep's, the sources' newest releases', and the Missing tab's search
// and grab — best first: the ones the profile accepts that hold the target
// alone, or one run of at most katalog.MaxCovered episodes in a row (the most
// one file goes over for), every one of them missing: a target acquire
// tracks, aired, monitored, wanted or failed, not reserved by another grab,
// not left for someone to look at, and of holdings acquire knows — the
// library may hold one of unknown holdings already. A grab that comes on its
// own (pol) takes a run only when the policy lets it take every episode of it
// (SearchSettings.Grabs): with only the new things grabbed on their own, a
// release of a new episode and the old backlog's is not one of them. Anything
// more — a longer range, episodes that are not one run, a season pack,
// several seasons — spends disk and bandwidth on what may not be missing, or
// cannot go over as one file: it is grabbed only when someone picks it.
func (s *Service) autoCandidates(ctx context.Context, t store.Target, ranked []Candidate, pol *SearchSettings) []Candidate {
	ok := acceptable(ranked)
	if t.Kind == "movie" {
		return ok
	}
	var missing map[[2]int]bool // read for the first release of several episodes
	var out []Candidate
	for _, c := range ok {
		cov := release.ParseCoverage(c.Title)
		switch {
		case !cov.Found:
			// It names nothing: it is grabbed for this target alone (covering).
		case cov.Pack(), !cov.Run(), len(cov.Episodes) > katalog.MaxCovered:
			continue
		case t.SeasonNumber == nil || t.EpisodeNumber == nil || !cov.Covers(*t.SeasonNumber, *t.EpisodeNumber):
			continue
		case len(cov.Episodes) > 1:
			if missing == nil {
				missing = s.missingEpisodes(ctx, t.TitleID, pol)
			}
			all := true
			for _, e := range cov.Episodes {
				all = all && (e == *t.EpisodeNumber || missing[[2]int{cov.Season, e}])
			}
			if !all {
				continue
			}
		}
		out = append(out, c)
	}
	return out
}

// missingEpisodes are the episodes of a title an automatic grab may bind
// besides the one it is for: missing, of holdings acquire knows, and — for a
// grab that comes on its own (pol) — what the policy lets it take. None when
// they cannot be read: then only a release of that one episode is taken.
func (s *Service) missingEpisodes(ctx context.Context, titleID string, pol *SearchSettings) map[[2]int]bool {
	out := map[[2]int]bool{}
	title, err := s.st.TitleByID(ctx, titleID)
	if err != nil || !title.HoldingsKnown() {
		return out
	}
	all, err := s.st.TitleTargets(ctx, titleID)
	if err != nil {
		return out
	}
	now := time.Now()
	for _, x := range all {
		if x.SeasonNumber == nil || x.EpisodeNumber == nil || !x.Monitored || !aired(x, now) || x.TMDBGoneAt != nil ||
			!store.Grabbable(x.State) || x.ReservedBy != "" || leftForAHuman(x) ||
			(x.AddedAt != nil && x.AddedAt.After(*title.HoldingsAt)) || (pol != nil && !pol.Grabs(title, x, now)) {
			continue
		}
		out[[2]int{*x.SeasonNumber, *x.EpisodeNumber}] = true
	}
	return out
}

// leftForAHuman reports whether a target failed as often as it is tried on
// its own, and waits for someone to look at it.
func leftForAHuman(t store.Target) bool {
	return t.State == store.TargetFailed && t.RetryAt == nil && t.GrabFailures >= store.MaxGrabFailures
}

// TargetSearchable reports whether a typed search is even possible, so the
// caller can say why rather than returning an empty list. Zero of 70 sources
// accepted a tmdbId when measured, so a series without a tvdbId can only be
// searched as text.
func TargetSearchable(t store.Title) bool {
	if t.Kind == "series" {
		return t.TVDBID > 0
	}
	return t.IMDBID != "" || t.TMDBID > 0
}

// ── ranking ────────────────────────────────────────────────────────────────

// rank scores every release against a profile and returns them best first,
// rejected last. This replaces "NZB first, then biggest", which kept choosing
// bloated multi-language remuxes.
func (s *Service) rank(ctx context.Context, releases []release.Release, profile release.Profile) []Candidate {
	routes := s.clientRoutes(ctx)
	out := make([]Candidate, 0, len(releases))
	for _, r := range releases {
		out = append(out, s.candidate(r, profile, routes))
	}
	sortCandidates(out)
	return out
}

func (s *Service) candidate(r release.Release, profile release.Profile, routes map[string]string) Candidate {
	v, info := release.Score(release.Candidate{
		Title: r.Title, Protocol: r.Protocol,
		SizeMb: r.Size / (1024 * 1024), Seeders: r.Seeders,
	}, profile)
	link := r.Source()
	var pub *time.Time
	if !r.PubDate.IsZero() {
		at := r.PubDate.UTC()
		pub = &at
	}
	return Candidate{
		Title: r.Title, Indexer: r.Indexer, Protocol: r.Protocol, Size: r.Size, Seeders: r.Seeders, PubDate: pub,
		Adapter: routes[r.Protocol], Source: endpoint.Redact(link),
		Reason: v.Summary(), Score: v.Score, Rejected: v.Rejected,
		Resolution: info.Resolution, Codec: info.Codec, SourceType: info.Source,
		IndexerID: r.IndexerID, GUID: r.GUID,
		Release: s.sealRef(releaseRef{
			IndexerID: r.IndexerID, Indexer: r.Indexer, Protocol: r.Protocol, GUID: r.GUID, Link: link,
			Title: r.Title,
		}),
		link: link,
	}
}

func sortCandidates(out []Candidate) {
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Rejected != out[j].Rejected {
			return !out[i].Rejected
		}
		return out[i].Score > out[j].Score
	})
	for i := range out {
		out[i].Best = i == 0 && !out[i].Rejected
	}
}

// ── grabbing a search result ───────────────────────────────────────────────

// releaseRef is what a Candidate's Release reference seals. Title is the
// release's name, which decides the episodes a grab of it covers.
type releaseRef struct {
	IndexerID int64  `json:"i"`
	Indexer   string `json:"n"`
	Protocol  string `json:"p"`
	GUID      string `json:"g,omitempty"`
	Link      string `json:"l"`
	Title     string `json:"t,omitempty"`
	Expires   int64  `json:"e"`
}

func (s *Service) sealRef(ref releaseRef) string {
	ref.Expires = time.Now().Add(releaseRefTTL).Unix()
	body, err := json.Marshal(ref)
	if err != nil {
		return ""
	}
	ct, _, err := s.refs.Seal("release", "ref", "v1", body)
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(ct)
}

func (s *Service) openRef(token string) (releaseRef, error) {
	ct, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return releaseRef{}, errStaleRelease
	}
	body, err := s.refs.Open("release", "ref", "v1", ct, "")
	if err != nil {
		return releaseRef{}, errStaleRelease
	}
	var ref releaseRef
	if json.Unmarshal(body, &ref) != nil || time.Now().Unix() > ref.Expires {
		return releaseRef{}, errStaleRelease
	}
	return ref, nil
}

// resolveCandidate fills in what a candidate from the console really points at.
// With a Release reference, everything that matters comes from the sealed
// reference, never from the body; without one, Source is a link the admin
// supplied and there is no search source to account it to.
func (s *Service) resolveCandidate(c *Candidate) error {
	c.link, c.IndexerID, c.GUID = "", 0, ""
	if c.Release == "" {
		c.link = c.Source
		return nil
	}
	ref, err := s.openRef(c.Release)
	if err != nil {
		return err
	}
	c.link, c.Protocol, c.IndexerID, c.Indexer, c.GUID = ref.Link, ref.Protocol, ref.IndexerID, ref.Indexer, ref.GUID
	if ref.Title != "" {
		c.Title = ref.Title
	}
	return nil
}

// candidateHandoff turns a ranked release into a hand-off. The candidate's
// client hint is ignored: routing is decided at grab time from the protocol,
// against the clients configured NOW.
func candidateHandoff(w store.Wanted, c Candidate, reason string) handoff {
	var seeders *int32
	if c.Protocol == "torrent" {
		n := int32(c.Seeders)
		seeders = &n
	}
	return handoff{
		WantedID: w.ID, Title: w.Title, Link: c.link, Protocol: c.Protocol,
		ReleaseTitle: c.Title, Indexer: c.Indexer, IndexerID: c.IndexerID, ReleaseGUID: c.GUID,
		Size: c.Size, Seeders: seeders, Reason: reason,
	}
}

// GrabCandidate hands a specific release (chosen in a picker or the manual
// search) to the client for its protocol, recording which one it was.
func (s *Service) GrabCandidate(ctx context.Context, wantedID string, c Candidate) error {
	w, err := s.st.GetWanted(ctx, wantedID)
	if err != nil {
		return err
	}
	if t, _, follows, err := s.requestTarget(ctx, w); follows {
		if err != nil {
			return err
		}
		_, err = s.GrabTargetCandidate(ctx, t.ID, c)
		return err
	}
	if err := s.resolveCandidate(&c); err != nil {
		return err
	}
	reason := c.Reason
	if reason == "" {
		reason = "picked manually"
	}
	out, err := s.handOff(ctx, candidateHandoff(w, c, reason))
	if err != nil {
		s.failGrab(ctx, wantedID, err)
		return err
	}
	s.setStatus(ctx, wantedID, "downloading",
		"grabbed "+protoLabel(out.Protocol)+" from "+c.Indexer+" via "+out.Client.ID)
	return nil
}

// GrabAdHoc grabs a release found in the manual search that no request covers
// yet: it records the wanted item first, so the download still lands in the
// catalog and the console can follow it like any other request.
//
// A release that names episodes or seasons is no movie: it is grabbed for the
// tracked series its name names, bound to the targets it holds, as a pick
// from a target's own search would be. One that names no tracked series, or
// several, is refused rather than filed as a movie request — which used to
// hand an episode to the catalog as a movie named after its show. So is one
// that names no episode but a tracked series (its year too, when it gives
// one): what it holds cannot be told.
func (s *Service) GrabAdHoc(ctx context.Context, c Candidate, title string, sub string) error {
	// Check the reference before creating a request that could not be grabbed.
	probe := c
	if err := s.resolveCandidate(&probe); err != nil {
		return err
	}
	if release.ParseCoverage(probe.Title).Found {
		series, err := s.trackedSeriesNamed(ctx, probe.Title)
		if err != nil {
			return err
		}
		if probe.Reason == "" {
			probe.Reason = "picked in the search"
		}
		_, err = s.grabRelease(ctx, series, store.Target{}, probe, "search")
		return err
	}
	hits, err := s.seriesMatching(ctx, probe.Title)
	if err != nil {
		return err
	}
	year := release.Parse(probe.Title).Year
	for _, t := range hits {
		if year == 0 || t.Year == 0 || year == t.Year {
			return fmt.Errorf("%s names no episode or season of %s, a series acquire tracks — it is not filed as a movie; "+
				"grab it from the series' missing episodes, whose releases name what they hold", probe.Title, t.Title)
		}
	}
	if title == "" {
		title = release.Parse(c.Title).Title
	}
	if title == "" {
		title = c.Title
	}
	w, _, err := s.Request(ctx, store.Wanted{Title: title, MediaType: "movie"}, Requester{Sub: sub, Admin: true})
	if err != nil {
		return err
	}
	return s.GrabCandidate(ctx, w.ID, c)
}

// trackedSeriesNamed finds the one tracked series a release name names, by its
// title or an alias, as a typed search verifies a release.
func (s *Service) trackedSeriesNamed(ctx context.Context, name string) (store.Title, error) {
	hits, err := s.seriesMatching(ctx, name)
	if err != nil {
		return store.Title{}, err
	}
	switch len(hits) {
	case 0:
		return store.Title{}, fmt.Errorf("%s holds episodes of a series acquire does not track — "+
			"it is not filed as a movie; track the series and grab it from its missing episodes", name)
	case 1:
		return hits[0], nil
	}
	// "Show" and "Show Origins" both match "Show.Origins.S01E01": the longest
	// title is the one the name means, when one is longer than all others.
	sort.Slice(hits, func(i, j int) bool { return len(hits[i].Title) > len(hits[j].Title) })
	if len(hits[0].Title) > len(hits[1].Title) {
		return hits[0], nil
	}
	names := make([]string, 0, len(hits))
	for _, t := range hits {
		names = append(names, t.Title)
	}
	return store.Title{}, fmt.Errorf("%s matches several tracked series (%s) — grab it from the right one's missing episodes",
		name, strings.Join(names, ", "))
}

// seriesMatching are the tracked series a release name names, by title or an
// alias.
func (s *Service) seriesMatching(ctx context.Context, name string) ([]store.Title, error) {
	all, err := s.st.TitlesOfKind(ctx, "series", false)
	if err != nil {
		return nil, err
	}
	aliases, err := s.st.AllAliases(ctx)
	if err != nil {
		return nil, err
	}
	var hits []store.Title
	for _, t := range all {
		if indexer.Verify(t.Title, aliases[t.ID], name, nil, nil, false).OK {
			hits = append(hits, t)
		}
	}
	return hits, nil
}

// spendGrab counts a release file download against the source that offered
// it, refusing when its daily allowance is spent.
func (s *Service) spendGrab(ctx context.Context, indexerID int64) error {
	ix, err := s.st.GetIndexer(ctx, indexerID)
	if errors.Is(err, store.ErrNotFound) {
		return nil // removed since the search; nothing left to count against
	}
	if err != nil {
		return err
	}
	ok, err := s.st.ReserveIndexerGrab(ctx, indexerID, ix.GrabLimitDay)
	if err != nil {
		return err
	}
	if !ok {
		return refused{fmt.Errorf("%s has used its %d release downloads for today: %w", ix.Name, *ix.GrabLimitDay, errGrabLimit)}
	}
	return nil
}
