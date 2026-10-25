package app

import (
	"context"
	"errors"
	"fmt"
	"log"
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
// does. A cursor per source and feed — the newest publication time looked
// at, and the releases of that time — makes each release looked at once.
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

const (
	// rssPage is how many releases one read of a feed asks for, and
	// rssPages how many pages a feed is read back when it moved further
	// than one since its last read.
	rssPage  = 100
	rssPages = 3
	// rssRunTime bounds one run, its reads and its grabs.
	rssRunTime = 15 * time.Minute
)

// RSSRun is what one run read, matched and grabbed.
type RSSRun struct {
	Feeds   int `json:"feeds"`
	Failed  int `json:"failed"`
	Read    int `json:"read"`
	New     int `json:"new"`
	Matched int `json:"matched"`
	Grabbed int `json:"grabbed"`
	// Stopped is why grabbing stopped for the rest of the run (no slot, no
	// room, no client); Halted why the run stopped (the gateway).
	Stopped string `json:"stopped,omitempty"`
	Halted  string `json:"halted,omitempty"`
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
	episodes, movies, err := s.st.WantedForFeeds(ctx, pol.AutoGrab, pol.newWithin())
	if err != nil || (episodes == 0 && movies == 0) {
		return run, err
	}
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
			if err != nil {
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
		line := fmt.Sprintf("acquire rss-sync: %d feed(s) read (%d failed), %d release(s), %d new, %d matched to what is wanted, %d grabbed",
			run.Feeds, run.Failed, run.Read, run.New, run.Matched, run.Grabbed)
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
	s.grabMatched(ctx, groups, &run)
	return run, nil
}

// releaseKey is what tells two releases of a feed apart: its guid, else its
// name.
func releaseKey(r release.Release) string {
	if r.GUID != "" {
		return r.GUID
	}
	return r.Title
}

// readFeed reads one feed of a source since its cursor: the newest releases,
// paged back while a whole page is new, up to rssPages; once for a feed never
// read. It returns the releases not looked at before and how many it read, and
// moves the cursor to the newest. A release without a publication time cannot
// be placed against the cursor and is left to the sweep. A source that fails
// keeps its cursor; its health is the search's (ask).
func (s *Service) readFeed(ctx context.Context, f fleet, src indexer.Source, fd feed) ([]release.Release, int, error) {
	cur, err := s.st.GetRSSCursor(ctx, src.ID, fd.name)
	if err != nil {
		return nil, 0, err
	}
	page := rssPage
	if src.Caps != nil && src.Caps.Limits.Max > 0 && src.Caps.Limits.Max < page {
		page = src.Caps.Limits.Max
	}
	next := cur
	var fresh []release.Release
	read := 0
	for p := 0; p < rssPages; p++ {
		done, err := s.slot(ctx)
		if err != nil {
			return nil, read, err
		}
		items, err := s.ask(ctx, src, f.limits[src.ID], s.timing.typedPerSource, indexer.Query{
			Kind: fd.kind, Media: fd.media, Cats: src.FeedCategories(fd.media), Limit: page, Offset: p * page,
		})
		done()
		if err != nil {
			if p == 0 {
				return nil, read, err
			}
			break // the pages read are kept
		}
		read += len(items)
		reached := false
		for _, r := range items {
			if r.PubDate.IsZero() {
				continue
			}
			key := releaseKey(r)
			if cur.Seen(r.PubDate, key) {
				reached = true
				continue
			}
			fresh = append(fresh, r)
			// A source whose clock runs ahead must not move the cursor past
			// what it publishes next.
			at := r.PubDate
			if now := s.now(); at.After(now) {
				at = now
			}
			switch {
			case next.NewestAt == nil || at.After(*next.NewestAt):
				next.NewestAt, next.NewestGUIDs = &at, []string{key}
			case at.Equal(*next.NewestAt):
				next.NewestGUIDs = appendOnce(next.NewestGUIDs, key)
			}
		}
		if cur.NewestAt == nil || reached || len(items) < page {
			break
		}
	}
	next.Detail = fmt.Sprintf("%d read, %d new", read, len(fresh))
	if err := s.st.SaveRSSCursor(ctx, next); err != nil {
		return fresh, read, err
	}
	return fresh, read, nil
}

func appendOnce(list []string, v string) []string {
	for _, x := range list {
		if x == v {
			return list
		}
	}
	return append(list, v)
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
// to tell a remake from its original (Show.US.S01E01).
var countryWords = map[string]bool{"us": true, "uk": true, "au": true, "ca": true, "nz": true}

// seriesOf finds the series a release's name names before the episodes it
// holds: its title or an alias exactly, or followed by the series' year or a
// country. One series, or none: a name several tracked series share, and the
// feed's identities do not tell apart, matches none.
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
		} else if countryWords[last] {
			cands = m.series[head]
		}
	}
	return only(cands, r)
}

// movieOf finds the movie a release's name names: its title or an alias,
// then its year. A year off by one is taken only when the feed's IMDb id says
// it is the movie.
func (m matcher) movieOf(r release.Release) (store.Title, bool) {
	words := strings.Fields(release.MatchKey(r.Title))
	for i := 1; i < len(words); i++ {
		y := release.YearOf(words[i])
		if y == 0 {
			continue
		}
		var cands []store.Title
		for _, t := range m.movies[strings.Join(words[:i], " ")] {
			if t.Year == y || (t.Year != 0 && abs(t.Year-y) == 1 && imdbNumber(r.IMDBID) > 0 && imdbNumber(r.IMDBID) == imdbNumber(t.IMDBID)) {
				cands = append(cands, t)
			}
		}
		if t, ok := only(cands, r); ok {
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
// differs.
func identitiesAgree(t store.Title, r release.Release) bool {
	switch {
	case r.TVDBID > 0 && t.TVDBID > 0 && r.TVDBID != t.TVDBID:
		return false
	case r.TMDBID > 0 && t.TMDBID > 0 && r.TMDBID != t.TMDBID:
		return false
	case imdbNumber(r.IMDBID) > 0 && imdbNumber(t.IMDBID) > 0 && imdbNumber(r.IMDBID) != imdbNumber(t.IMDBID):
		return false
	}
	return true
}

// imdbNumber is the number of an IMDb id (tt0903747 is 903747), 0 for none.
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
		if name, cov := release.SeriesPart(r.Title); cov.Found {
			title, ok := m.seriesOf(r, name)
			if !ok || cov.Pack() {
				continue
			}
			for _, t := range targetsOf(title.ID) {
				if t.SeasonNumber != nil && t.EpisodeNumber != nil && cov.Covers(*t.SeasonNumber, *t.EpisodeNumber) &&
					grabbableNow(title, t, now) && pol.Grabs(title, t, now) {
					run.Matched++
					add(title, t, r)
					break
				}
			}
			continue
		}
		title, ok := m.movieOf(r)
		if !ok {
			continue
		}
		for _, t := range targetsOf(title.ID) {
			if t.Kind == "movie" && grabbableNow(title, t, now) && pol.Grabs(title, t, now) {
				run.Matched++
				add(title, t, r)
			}
		}
	}
	return order
}

// grabbableNow reports whether a target is what the sweep may grab now
// (store's sweepable): wanted, not reserved, monitored with its title, of
// holdings acquire knows — learned after the target was added —, and out: an
// episode aired, a movie released.
func grabbableNow(title store.Title, t store.Target, now time.Time) bool {
	switch {
	case t.State != store.TargetWanted, t.ReservedBy != "", !t.Monitored, !title.Monitored, !title.HoldingsKnown():
		return false
	case t.AddedAt != nil && t.AddedAt.After(*title.HoldingsAt):
		return false
	case t.WindowAt != nil:
		return !t.WindowAt.After(now)
	}
	return t.Kind == "movie" && !t.ReleasePending
}

// grabMatched grabs, for each target matched, the best of its releases an
// automatic grab may take, as the sweep grabs what it found.
func (s *Service) grabMatched(ctx context.Context, groups []*matched, run *RSSRun) {
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
		cands := make([]Candidate, 0, len(g.releases))
		for _, r := range g.releases {
			c := s.candidate(r, profile, routes)
			c.Stage, c.MatchedVia = "rss", "feed"
			if g.target.Kind != "movie" {
				cov := release.ParseCoverage(c.Title)
				c.Covers, c.Pack = cov.String(), cov.Pack()
			}
			if why, ok := blocked[c.Title]; ok {
				c.Rejected, c.Reason = true, clip("blocked for this target: "+why+" · "+c.Reason, 300)
			}
			cands = append(cands, c)
		}
		sortCandidates(cands)
		usable := s.autoCandidates(ctx, g.target, cands)
		if len(usable) == 0 {
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
