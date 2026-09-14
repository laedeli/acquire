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
	"github.com/laedeli/acquire/internal/release"
	"github.com/laedeli/acquire/internal/store"
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
	// Release is an opaque, sealed reference to the release's real link. A grab
	// sends it back. Source, which the console shows, is that link with its
	// credentials removed: a source's API key never reaches a browser.
	Release string `json:"release,omitempty"`

	// link is the real link, server-side only.
	link string
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
		return nil, fmt.Errorf("%s: daily query limit reached", src.Name)
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
	out := make([]Candidate, 0, len(results))
	for _, r := range results {
		c := s.candidate(r.Release, profile, routes)
		// How it was found and what identified it, so the console can show
		// an id match as certain rather than merely plausible.
		c.Stage, c.MatchedVia = r.Stage, r.Match.Via
		out = append(out, c)
	}
	sortCandidates(out)
	return out, nil
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
	return Candidate{
		Title: r.Title, Indexer: r.Indexer, Protocol: r.Protocol, Size: r.Size, Seeders: r.Seeders,
		Adapter: routes[r.Protocol], Source: endpoint.Redact(link),
		Reason: v.Summary(), Score: v.Score, Rejected: v.Rejected,
		Resolution: info.Resolution, Codec: info.Codec, SourceType: info.Source,
		IndexerID: r.IndexerID, GUID: r.GUID,
		Release: s.sealRef(releaseRef{
			IndexerID: r.IndexerID, Indexer: r.Indexer, Protocol: r.Protocol, GUID: r.GUID, Link: link,
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

// releaseRef is what a Candidate's Release reference seals.
type releaseRef struct {
	IndexerID int64  `json:"i"`
	Indexer   string `json:"n"`
	Protocol  string `json:"p"`
	GUID      string `json:"g,omitempty"`
	Link      string `json:"l"`
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
	if err := s.resolveCandidate(&c); err != nil {
		return err
	}
	w, err := s.st.GetWanted(ctx, wantedID)
	if err != nil {
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
func (s *Service) GrabAdHoc(ctx context.Context, c Candidate, title string, sub string) error {
	// Check the reference before creating a request that could not be grabbed.
	probe := c
	if err := s.resolveCandidate(&probe); err != nil {
		return err
	}
	if title == "" {
		title = release.Parse(c.Title).Title
	}
	if title == "" {
		title = c.Title
	}
	w, err := s.Request(ctx, store.Wanted{Title: title, MediaType: "movie"}, sub)
	if err != nil {
		return err
	}
	return s.GrabCandidate(ctx, w.ID, c)
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
