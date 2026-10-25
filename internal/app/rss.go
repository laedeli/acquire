package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/laedeli/acquire/internal/indexer"
	"github.com/laedeli/acquire/internal/release"
	"github.com/laedeli/acquire/internal/store"
)

// The sources' newest releases (rss-sync).
//
// The sweep searches the backlog a slice at a time, so a release that turns
// up in between waits for its target's turn. rss-sync reads each source's
// newest releases instead — t=tvsearch and t=movie without a query, or
// t=search over both categories for a source that offers neither, a hundred
// at a time — and matches each to what is wanted: a series' title or alias
// with the episodes the name holds, a movie's title with its year; checked
// against the identities the feed carries (tvdbid, imdbid, tmdbid) when it
// does. A cursor per source and feed — the releases looked at, by their guid,
// the last thousand acquire saw — makes each release looked at once, however
// the source dates it.
//
// A match is grabbed as the sweep grabs: ranked by the title's quality
// profile, the releases blocked for the target left out, only what an
// automatic grab may take (one episode, or one short run of missing ones;
// never a pack), within the storage floor and the downloads at once, through
// the same grab bound to targets — and only what the search and grab policy
// lets grab on its own (SearchSettings.Grabs). With nothing grabbed on its
// own, no feed is read: a read spends a query of the source's allowance.
//
// A source that fails or limits backs off as it does for any search (its
// health) and never fails the run; the others are read.
//
// A read spends a query of the source's daily allowance, so no feed is read
// while nothing could be grabbed — every download slot taken, no room for a
// download, the gateway out of reach —, and the feeds leave rssReserve of a
// source's daily limit to the sweep and to people's searches.

const (
	// rssPage is how many releases one read of a feed asks for, and
	// rssPages how many pages a feed is read back when it moved further
	// than one since its last read.
	rssPage  = 100
	rssPages = 3
	// rssRunTime bounds one run, its reads and its grabs.
	rssRunTime = 15 * time.Minute
	// rssReserve is the part of a source's daily query limit, in percent,
	// the feeds leave to the sweep and to people's searches.
	rssReserve = 30
)

// rssLimit is the part of a source's daily query limit the feeds may spend:
// all but rssReserve; nil, no limit, for a source without one.
func rssLimit(limit *int) *int {
	if limit == nil {
		return nil
	}
	n := *limit * (100 - rssReserve) / 100
	return &n
}

// RSSRun is what one run read, matched and grabbed.
type RSSRun struct {
	Feeds  int `json:"feeds"`
	Failed int `json:"failed"`
	// Limited are the feeds not read as their source spent the part of its
	// daily limit the feeds may spend.
	Limited int `json:"limited"`
	Read    int `json:"read"`
	New     int `json:"new"`
	Matched int `json:"matched"`
	Grabbed int `json:"grabbed"`
	// Stopped is why grabbing stopped for the rest of the run (no slot, no
	// room, no client); Halted why the run stopped (the gateway); Skipped why
	// no feed was read at all, as nothing could be grabbed.
	Stopped string `json:"stopped,omitempty"`
	Halted  string `json:"halted,omitempty"`
	Skipped string `json:"skipped,omitempty"`
}

func (s *Service) wakeRSS() {
	select {
	case s.rssWake <- struct{}{}:
	default:
	}
}

// RunRSS reads the sources' newest releases when the schedule says so, until
// ctx ends.
func (s *Service) RunRSS(ctx context.Context) {
	if s.st == nil {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.rssWake:
		}
		rctx, cancel := context.WithTimeout(ctx, rssRunTime)
		if _, err := s.SyncRSS(rctx); err != nil && ctx.Err() == nil {
			log.Printf("acquire rss-sync: %v", err)
		}
		cancel()
	}
}

// feed is one read of a source's newest releases: its name (the cursor's),
// the t= function and the categories it is scoped to.
type feed struct {
	name, kind, media string
}

// feedsOf are the reads of a source that find releases for what is wanted
// now: episodes (tv), movies, or both.
func feedsOf(src indexer.Source, tv, movie bool) []feed {
	var out []feed
	tvTyped, movieTyped := src.Caps.OffersTVSearch(), src.Caps.OffersMovieSearch()
	if tv && tvTyped {
		out = append(out, feed{"tv", "tvsearch", "tv"})
	}
	if movie && movieTyped {
		out = append(out, feed{"movie", "movie", "movie"})
	}
	// What the typed functions do not cover is one search over the
	// categories it needs.
	switch needTV, needMovie := tv && !tvTyped, movie && !movieTyped; {
	case needTV && needMovie:
		out = append(out, feed{"search", "search", ""})
	case needTV:
		out = append(out, feed{"search", "search", "tv"})
	case needMovie:
		out = append(out, feed{"search", "search", "movie"})
	}
	return out
}

// SyncRSS is one run of rss-sync: every source's newest releases read since
// its last read, matched to what is wanted, and grabbed as the policy allows.
func (s *Service) SyncRSS(ctx context.Context) (RSSRun, error) {
	var run RSSRun
	pol := s.grabPolicy(ctx)
	if !pol.GrabsAnything() {
		return run, nil
	}
	episodes, movies, err := s.st.WantedForFeeds(ctx, store.FeedsWant{All: pol.AutoGrab, NewWithinDays: pol.newWithin(), DueOrder: pol.dueOrder()})
	if err != nil || (episodes == 0 && movies == 0) {
		return run, err
	}
	if why := s.cannotGrabNow(ctx); why != "" {
		run.Skipped = why
		s.noteRSSSkipped(why)
		return run, nil
	}
	s.noteRSSSkipped("")
	f, err := s.loadFleet(ctx, nil)
	if err != nil {
		return run, err
	}
	first, second := f.byProtocol(pol.PreferProtocol)
	var fresh []release.Release
	seen := map[string]bool{}
	for _, src := range append(first.sources, second.sources...) {
		for _, fd := range feedsOf(src, episodes > 0, movies > 0) {
			if ctx.Err() != nil {
				return run, ctx.Err()
			}
			run.Feeds++
			items, read, err := s.readFeed(ctx, f, src, fd)
			run.Read += read
			switch {
			case errors.Is(err, errQueryLimit):
				run.Limited++
				continue
			case err != nil:
				run.Failed++
				continue
			}
			for _, r := range items {
				key := strconv.FormatInt(r.IndexerID, 10) + "\x00" + releaseKey(r)
				if !seen[key] {
					seen[key] = true
					fresh = append(fresh, r)
				}
			}
		}
	}
	run.New = len(fresh)
	defer func() {
		if run.Feeds == 0 {
			return
		}
		line := fmt.Sprintf("acquire rss-sync: %d feed(s) read (%d failed, %d left for today: their source keeps the rest of its daily limit for the sweep and searches), "+
			"%d release(s), %d new, %d matched to what is wanted, %d grabbed",
			run.Feeds, run.Failed, run.Limited, run.Read, run.New, run.Matched, run.Grabbed)
		if run.Stopped != "" {
			line += "; stopped grabbing: " + run.Stopped
		}
		if run.Halted != "" {
			line += "; stopped: the gateway does not take grabs now: " + run.Halted
		}
		log.Print(line)
		if run.Grabbed > 0 {
			s.notify()
		}
	}()
	if len(fresh) == 0 {
		return run, nil
	}
	m, err := s.loadMatcher(ctx)
	if err != nil {
		return run, err
	}
	groups := s.matchFresh(ctx, m, pol, fresh, &run)
	s.grabMatched(ctx, pol, groups, &run)
	return run, nil
}

// cannotGrabNow says why no grab could be handed over now, or "": every
// download slot is taken, the gateway is out of reach, or no download client
// has room for a download.
func (s *Service) cannotGrabNow(ctx context.Context) string {
	if full, why := s.slotsFull(ctx); full {
		return why
	}
	if st := s.syncStatus(ctx); st.Configured && st.Checked && !st.Reachable {
		return "the gateway does not answer: " + st.LastError
	}
	routes := s.clientRoutes(ctx)
	why := "no download client is enabled"
	for _, p := range []string{"usenet", "torrent"} {
		id, ok := routes[p]
		if !ok {
			continue
		}
		c, err := s.resolveClient(ctx, id, p)
		if err == nil {
			// The smallest grab there is: the floor, and the headroom any
			// grab keeps.
			err = s.AdmitGrab(ctx, c, 1, p)
		}
		if err == nil {
			return ""
		}
		why = err.Error()
	}
	return why
}

// noteRSSSkipped logs why rss-sync reads nothing, once while the reason
// lasts, and once when it reads again.
func (s *Service) noteRSSSkipped(why string) {
	s.rssMu.Lock()
	prev := s.rssSkipped
	s.rssSkipped = why
	s.rssMu.Unlock()
	switch {
	case why != "" && why != prev:
		log.Printf("acquire rss-sync: no feed read while nothing can be grabbed: %s", why)
	case why == "" && prev != "":
		log.Print("acquire rss-sync: grabs can be handed over again; the feeds are read")
	}
}

// releaseKey is what tells two releases of a feed apart: its guid, else its
// name.
func releaseKey(r release.Release) string {
	if r.GUID != "" {
		return r.GUID
	}
	return r.Title
}

// readFeed reads one feed of a source back to what it looked at before (its
// cursor): the newest releases, paged back while a whole page is new, up to
// rssPages; one page for a feed never read. It returns the releases not looked
// at before and how many it read, and records them as looked at — by their
// guid, as of when acquire saw them, however the source dates them: a source
// whose clock runs ahead, that dates a release years ahead, that indexes one
// late and dates it long before, or whose newest page reaches back weeks, has
// each looked at once. A source that fails keeps its cursor; its health is the
// search's (ask).
func (s *Service) readFeed(ctx context.Context, f fleet, src indexer.Source, fd feed) ([]release.Release, int, error) {
	cur, err := s.st.GetRSSCursor(ctx, src.ID, fd.name)
	if err != nil {
		return nil, 0, err
	}
	page := rssPage
	if src.Caps != nil && src.Caps.Limits.Max > 0 && src.Caps.Limits.Max < page {
		page = src.Caps.Limits.Max
	}
	first := !cur.Read()
	var fresh []release.Release
	read := 0
	for p := 0; p < rssPages; p++ {
		done, err := s.slot(ctx)
		if err != nil {
			return nil, read, err
		}
		items, err := s.ask(ctx, src, rssLimit(f.limits[src.ID]), s.timing.typedPerSource, indexer.Query{
			Kind: fd.kind, Media: fd.media, Cats: src.FeedCategories(fd.media), Limit: page, Offset: p * page,
			// The identities and categories of each release: a newznab
			// source names them only when asked.
			Extended: true,
		})
		done()
		if err != nil {
			if p == 0 {
				return nil, read, err
			}
			break // the pages read are kept
		}
		read += len(items)
		now := s.now()
		reached := false
		for _, r := range items {
			key := releaseKey(r)
			if cur.Has(key) {
				reached = true
				continue
			}
			cur.Add(key, now)
			if len(r.Categories) == 0 {
				// What the feed is scoped to is what it files the release
				// under; a read of both says nothing.
				r.Categories = scopeOf(fd.media)
			}
			fresh = append(fresh, r)
		}
		if first || reached || len(items) < page {
			break
		}
	}
	cur.Detail = fmt.Sprintf("%d read, %d new", read, len(fresh))
	if err := s.st.SaveRSSCursor(ctx, cur); err != nil {
		return fresh, read, err
	}
	return fresh, read, nil
}

// scopeOf are the categories a feed of a media reads: TV's or movies', none
// for both.
func scopeOf(media string) []int {
	switch media {
	case "tv":
		return []int{indexer.CatTV}
	case "movie":
		return []int{indexer.CatMovie}
	}
	return nil
}

// ── matching ───────────────────────────────────────────────────────────────

// matcher finds the monitored title a release name names: by the key of its
// title or an alias (release.MatchKey).
type matcher struct {
	series map[string][]store.Title
	movies map[string][]store.Title
}

func (s *Service) loadMatcher(ctx context.Context) (matcher, error) {
	m := matcher{series: map[string][]store.Title{}, movies: map[string][]store.Title{}}
	aliases, err := s.st.AllAliases(ctx)
	if err != nil {
		return m, err
	}
	for kind, into := range map[string]map[string][]store.Title{"series": m.series, "movie": m.movies} {
		titles, err := s.st.TitlesOfKind(ctx, kind, true)
		if err != nil {
			return m, err
		}
		for _, t := range titles {
			keys := map[string]bool{release.MatchKey(t.Title): true}
			for _, a := range aliases[t.ID] {
				keys[release.MatchKey(a)] = true
			}
			for k := range keys {
				if k != "" {
					into[k] = append(into[k], t)
				}
			}
		}
	}
	return m, nil
}

// countryWords are the countries a series' name in a release may end with,
// to tell a remake from its original (Show.US.S01E01), and the country each
// is (ISO 3166-1).
var countryWords = map[string]string{"us": "US", "uk": "GB", "au": "AU", "ca": "CA", "nz": "NZ"}

// seriesOf finds the series a release's name names before the episodes it
// holds: its title or an alias exactly — one that names a country too, as
// "Show Name (US)" does —, else followed by the series' year, else followed
// by the country it comes from, as TMDB says, or the feed's TVDB or TMDB id
// names it: a bare name with a country after it is that country's series,
// not one of the same name from elsewhere. One series, or none: a name
// several tracked series share, and the feed's identities do not tell apart,
// matches none.
func (m matcher) seriesOf(r release.Release, name string) (store.Title, bool) {
	key := release.MatchKey(name)
	cands := m.series[key]
	words := strings.Fields(key)
	if len(cands) == 0 && len(words) > 1 {
		last, head := words[len(words)-1], strings.Join(words[:len(words)-1], " ")
		if y := release.YearOf(last); y > 0 {
			for _, t := range m.series[head] {
				if t.Year == 0 || t.Year == y {
					cands = append(cands, t)
				}
			}
		} else if country := countryWords[last]; country != "" {
			for _, t := range m.series[head] {
				if slices.Contains(t.Countries, country) || (r.TVDBID > 0 && r.TVDBID == t.TVDBID) || (r.TMDBID > 0 && r.TMDBID == t.TMDBID) {
					cands = append(cands, t)
				}
			}
		}
	}
	return only(cands, r)
}

// movieOf finds the movie a release's name names: its title or an alias,
// then its year, then tags only (release.TagsOnly) — no trailer, making-of or
// other extra (release.Extra), and no longer title that starts with its
// name. A year off by one is taken only when the feed's IMDb id says it is
// the movie.
func (m matcher) movieOf(r release.Release) (store.Title, bool) {
	for _, at := range release.SplitYears(r.Title) {
		var cands []store.Title
		for _, t := range m.movies[release.MatchKey(at.Head)] {
			if t.Year == at.Year || (t.Year != 0 && abs(t.Year-at.Year) == 1 && imdbNumber(r.IMDBID) > 0 && imdbNumber(r.IMDBID) == imdbNumber(t.IMDBID)) {
				cands = append(cands, t)
			}
		}
		if t, ok := only(cands, r); ok {
			if release.Extra(at.Rest) != "" || !release.TagsOnly(at.Rest, "") {
				return store.Title{}, false
			}
			return t, true
		}
	}
	return store.Title{}, false
}

// only is the one candidate the feed's identities do not contradict.
func only(cands []store.Title, r release.Release) (store.Title, bool) {
	var out []store.Title
	for _, t := range cands {
		if identitiesAgree(t, r) {
			out = append(out, t)
		}
	}
	if len(out) != 1 {
		return store.Title{}, false
	}
	return out[0], true
}

// identitiesAgree reports whether what a feed says a release is of does not
// name another title than t: no TVDB, TMDB or IMDb id both carry that
// differs. The IMDb id only of a movie: of an episode a feed may carry the
// episode's own.
func identitiesAgree(t store.Title, r release.Release) bool {
	switch {
	case r.TVDBID > 0 && t.TVDBID > 0 && r.TVDBID != t.TVDBID:
		return false
	case r.TMDBID > 0 && t.TMDBID > 0 && r.TMDBID != t.TMDBID:
		return false
	case t.Kind == "movie" && imdbNumber(r.IMDBID) > 0 && imdbNumber(t.IMDBID) > 0 && imdbNumber(r.IMDBID) != imdbNumber(t.IMDBID):
		return false
	}
	return true
}

// imdbNumber is the number of an IMDb id (tt0012345 is 12345), 0 for none.
func imdbNumber(id string) int64 {
	n, err := strconv.ParseInt(strings.TrimPrefix(strings.ToLower(strings.TrimSpace(id)), "tt"), 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// matched is a target a release in the feeds is for, and the releases.
type matched struct {
	title    store.Title
	target   store.Target
	releases []release.Release
}

// matchFresh matches the new releases to the targets they could be grabbed
// for: for a series' release the first of the episodes it holds that an
// automatic grab may take now, for a movie's its target. A season pack is
// never grabbed on its own, nor an episode or a movie that is not out yet —
// a release named for one before it airs is most often a fake.
func (s *Service) matchFresh(ctx context.Context, m matcher, pol SearchSettings, fresh []release.Release, run *RSSRun) []*matched {
	now := s.now()
	targets := map[string][]store.Target{}
	targetsOf := func(titleID string) []store.Target {
		if ts, ok := targets[titleID]; ok {
			return ts
		}
		ts, err := s.st.TitleTargets(ctx, titleID)
		if err != nil {
			log.Printf("acquire rss-sync: the targets of %s: %v", titleID, err)
		}
		targets[titleID] = ts
		return ts
	}
	byTarget := map[string]*matched{}
	var order []*matched
	add := func(title store.Title, t store.Target, r release.Release) {
		g := byTarget[t.ID]
		if g == nil {
			g = &matched{title: title, target: t}
			byTarget[t.ID] = g
			order = append(order, g)
		}
		g.releases = append(g.releases, r)
	}
	for _, r := range fresh {
		// A series only from what a feed files as TV, a movie only from what
		// it files as a movie.
		media := r.Media()
		if name, cov, rest := release.SplitSeries(r.Title); cov.Found {
			if media != "tv" {
				continue
			}
			title, ok := m.seriesOf(r, name)
			if !ok || cov.Pack() || release.Extra(rest) != "" {
				continue
			}
			for _, t := range targetsOf(title.ID) {
				if t.SeasonNumber != nil && t.EpisodeNumber != nil && cov.Covers(*t.SeasonNumber, *t.EpisodeNumber) &&
					grabbableNow(title, t, now, pol) && pol.Grabs(title, t, now) {
					// After the episodes, tags only — the episode's title may
					// come first.
					if release.TagsOnly(rest, t.EpisodeTitle) {
						run.Matched++
						add(title, t, r)
					}
					break
				}
			}
			continue
		}
		if media != "movie" {
			continue
		}
		title, ok := m.movieOf(r)
		if !ok {
			continue
		}
		for _, t := range targetsOf(title.ID) {
			if t.Kind == "movie" && grabbableNow(title, t, now, pol) && pol.Grabs(title, t, now) {
				run.Matched++
				add(title, t, r)
			}
		}
	}
	return order
}

// grabbableNow reports whether a target is what the sweep may grab now
// (store's sweepable): wanted, not reserved, monitored with its title, listed
// by TMDB, of holdings acquire knows — learned after the target was added —,
// and open to an automatic grab (autoOpen).
func grabbableNow(title store.Title, t store.Target, now time.Time, pol SearchSettings) bool {
	switch {
	case t.State != store.TargetWanted, t.ReservedBy != "", !t.Monitored, !title.Monitored, !title.HoldingsKnown(), t.TMDBGoneAt != nil:
		return false
	case t.AddedAt != nil && t.AddedAt.After(*title.HoldingsAt):
		return false
	}
	return autoOpen(t, now, pol)
}

// autoOpen reports whether an automatic grab may take a target now, as
// store's autoOpenSQL says (openAt); a movie the import tracks, without a
// window, is out.
func autoOpen(t store.Target, now time.Time, pol SearchSettings) bool {
	if t.WindowAt == nil {
		return t.Kind == "movie" && !t.ReleasePending
	}
	return !openAt(t, pol).After(now)
}

// openAt is when an automatic grab may take a target with a window: of an
// episode whose air time the schedule source gives, the delay after it;
// else its window, and the air delay after its air date (00:00 UTC of the
// day TMDB dates it).
func openAt(t store.Target, pol SearchSettings) time.Time {
	if pol.exactAirTimes() && t.AirStamp != nil {
		return t.AirStamp.Add(pol.stampDelay())
	}
	at := *t.WindowAt
	if t.AirDate != nil && t.AirDate.Add(pol.airDelay()).After(at) {
		at = t.AirDate.Add(pol.airDelay())
	}
	return at
}

// grabMatched grabs, for each target matched, the best of its releases an
// automatic grab may take, as the sweep grabs what it found.
func (s *Service) grabMatched(ctx context.Context, pol SearchSettings, groups []*matched, run *RSSRun) {
	sort.SliceStable(groups, func(i, j int) bool {
		a, b := groups[i].target, groups[j].target
		if a.TitleID != b.TitleID {
			return a.TitleID < b.TitleID
		}
		return a.ID < b.ID
	})
	routes := s.clientRoutes(ctx)
	grabbing := true
	for _, g := range groups {
		if ctx.Err() != nil {
			return
		}
		profile, _ := s.ScoringProfile(ctx, g.title.ProfileID)
		blocked, err := s.st.BlockedFor(ctx, g.target.ID)
		if err != nil {
			log.Printf("acquire rss-sync: blocklist of %s unreadable, not applied: %v", g.target.ID, err)
		}
		days := s.airDays(ctx, g.target)
		cands := make([]Candidate, 0, len(g.releases))
		for _, r := range g.releases {
			c := s.candidate(r, profile, routes)
			c.Stage, c.MatchedVia = "rss", "feed"
			if g.target.Kind != "movie" {
				cov := release.ParseCoverage(c.Title)
				c.Covers, c.Pack = cov.String(), cov.Pack()
			}
			markRejected(&c, g.title, g.target, blocked, days, pol.exactAirTimes())
			cands = append(cands, c)
		}
		sortCandidates(cands)
		usable := s.autoCandidates(ctx, g.target, cands, &pol)
		if len(usable) == 0 {
			if n := preAiredCount(cands); n > 0 {
				// Skipped, not blocked: the target says why it is still
				// missing, and a person may pick the release.
				_ = s.st.SetTargetDetail(ctx, g.target.ID, clip(fmt.Sprintf(
					"%d release(s) in a source's newest releases published before it aired — most likely fakes, skipped", n), 300))
			}
			continue
		}
		if !grabbing {
			s.foundButWaiting(ctx, g.target, "in a source's newest releases; "+run.Stopped)
			continue
		}
		if full, why := s.slotsFull(ctx); full {
			grabbing, run.Stopped = false, why
			s.foundButWaiting(ctx, g.target, "in a source's newest releases; "+why)
			continue
		}
		_, err = s.grabBest(ctx, g.title, g.target, usable, "rss")
		var capErr atCapacity
		var r refused
		var ng *store.NotGrabbableError
		switch {
		case err == nil:
			run.Grabbed++
		case errors.As(err, &ng):
			// Another grab of this run took it, a file of several holding it.
		case errors.As(err, &capErr), errors.As(err, &r):
			grabbing, run.Stopped = false, err.Error()
			s.foundButWaiting(ctx, g.target, "in a source's newest releases; "+err.Error())
		default:
			s.targetGrabFailed(ctx, g.target, err)
		}
		if err != nil && gatewayDown(err) {
			run.Halted = err.Error()
			return
		}
	}
}
