package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/laedeli/acquire/internal/indexer"
	"github.com/laedeli/acquire/internal/release"
	"github.com/laedeli/acquire/internal/store"
)

// feedItem is one release in a source's newest releases.
type feedItem struct {
	title string
	pub   time.Time
	cat   int // 5040 TV, 2040 movies
	attrs map[string]string
}

// feedSource is a newznab source that answers its newest releases, newest
// first, a page at a time, scoped to the categories asked for. typed makes
// its caps offer tv-search and movie-search; max is its page size.
type feedSource struct {
	mu      sync.Mutex
	key     string
	typed   bool
	max     int
	mode    string // "" | 503
	items   []feedItem
	queries []url.Values
	srv     *httptest.Server
}

func newFeedSource(t *testing.T, typed bool, items ...feedItem) *feedSource {
	f := &feedSource{key: "FEEDKEY", typed: typed, items: items}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *feedSource) add(items ...feedItem) {
	f.mu.Lock()
	f.items = append(f.items, items...)
	f.mu.Unlock()
}

func (f *feedSource) reads() []url.Values {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []url.Values
	for _, q := range f.queries {
		if t := q.Get("t"); t != "caps" && t != "get" {
			out = append(out, q)
		}
	}
	return out
}

func (f *feedSource) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	q := r.URL.Query()
	f.queries = append(f.queries, q)
	mode, typed, max := f.mode, f.typed, f.max
	items := append([]feedItem(nil), f.items...)
	f.mu.Unlock()
	if mode == "503" {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	if q.Get("apikey") != f.key {
		_, _ = w.Write([]byte(`<error code="100" description="Incorrect user credentials"/>`))
		return
	}
	switch q.Get("t") {
	case "caps":
		avail := "no"
		if typed {
			avail = "yes"
		}
		limits := ""
		if max > 0 {
			limits = fmt.Sprintf(`<limits max="%d" default="%d"/>`, max, max)
		}
		_, _ = fmt.Fprintf(w, `<caps><server title="Feed"/>%s<searching><search available="yes" supportedParams="q"/>
		  <tv-search available="%s" supportedParams="q,season,ep"/><movie-search available="%s" supportedParams="q"/></searching>
		  <categories><category id="2000" name="Movies"/><category id="5000" name="TV"/></categories></caps>`, limits, avail, avail)
		return
	case "get":
		_, _ = w.Write([]byte(sampleNZB))
		return
	}
	parents := map[int]bool{}
	for _, c := range strings.Split(q.Get("cat"), ",") {
		if n, err := strconv.Atoi(c); err == nil {
			parents[n/1000] = true
		}
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].pub.After(items[j].pub) })
	var page []feedItem
	for _, it := range items {
		if len(parents) == 0 || parents[it.cat/1000] {
			page = append(page, it)
		}
	}
	offset, _ := strconv.Atoi(q.Get("offset"))
	limit, _ := strconv.Atoi(q.Get("limit"))
	page = page[min(offset, len(page)):]
	if limit > 0 && len(page) > limit {
		page = page[:limit]
	}
	var b strings.Builder
	b.WriteString(`<rss xmlns:newznab="http://www.newznab.com/DTD/2010/feeds/attributes/"><channel>`)
	for _, it := range page {
		id := url.QueryEscape(it.title)
		fmt.Fprintf(&b, `<item><title>%s</title><guid>g-%s</guid><pubDate>%s</pubDate>`+
			`<enclosure url="%s/api?t=get&amp;id=%s&amp;apikey=%s" length="%d" type="application/x-nzb"/>`+
			`<newznab:attr name="category" value="%d"/>`, it.title, id, it.pub.Format(time.RFC1123Z), f.srv.URL, id, f.key, int64(2<<30), it.cat)
		for k, v := range it.attrs {
			fmt.Fprintf(&b, `<newznab:attr name="%s" value="%s"/>`, k, v)
		}
		b.WriteString(`</item>`)
	}
	b.WriteString(`</channel></rss>`)
	_, _ = w.Write([]byte(b.String()))
}

func ago(d time.Duration) time.Time { return time.Now().Add(-d).Truncate(time.Second) }

func tv(title string, pub time.Time, attrs ...string) feedItem {
	it := feedItem{title: title, pub: pub, cat: 5040, attrs: map[string]string{}}
	for i := 0; i+1 < len(attrs); i += 2 {
		it.attrs[attrs[i]] = attrs[i+1]
	}
	return it
}

func film(title string, pub time.Time, attrs ...string) feedItem {
	it := tv(title, pub, attrs...)
	it.cat = 2040
	return it
}

func addFeed(t *testing.T, svc *Service, name string, f *feedSource) SourceWriteResult {
	t.Helper()
	res, err := svc.CreateSource(context.Background(), SourceInput{Name: name, Protocol: "usenet", BaseURL: f.srv.URL,
		APIKey: SecretInput{Present: true, Value: f.key}}, "admin")
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// grabbed are the releases the gateway was handed, by what each covers; each
// must have come from the feeds.
func (r *followRig) grabbed(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, add := range r.gw.added() {
		id, _ := add["wanted_item_id"].(string)
		g, err := r.svc.st.GetTargetGrab(context.Background(), id)
		if err != nil {
			t.Fatalf("an add for %q: %v", id, err)
		}
		if g.Via != "rss" {
			t.Errorf("%s came via %q", g.Covers, g.Via)
		}
		out[g.Covers] = g.ReleaseTitle
	}
	return out
}

// The case the feeds are for: a series a request follows, its episodes
// airing week by week. Each release that turns up in a source's newest
// releases is looked at once, matched by the series' title or an alias and
// the episode its name holds, and grabbed as the sweep grabs — the best of
// the releases of an episode, never a pack, never an episode before it airs
// (a release named for one is most often a fake), never one the feed's
// identities say is another series.
func TestTheFeedsGrabTheNewEpisodesAsTheyTurnUp(t *testing.T) {
	r := newFollowRig(t)
	ctx := context.Background()
	r.tmdb.set(func(f *fakeTMDB) { f.series[990001] = sevenHarbours() })
	if _, _, err := r.svc.Request(ctx, store.Wanted{TMDBID: 990001, MediaType: "series", Title: "Example Saga: Seven Harbours"}, asAdmin("user")); err != nil {
		t.Fatal(err)
	}
	r.settle(t, false)
	feedSrc := newFeedSource(t, true,
		tv("Example.Saga.Seven.Harbours.S01E07.720p.WEB.h264-GRP", ago(3*time.Hour)),
		tv("Example.Saga.Seven.Harbours.S01E07.1080p.WEB.h264-GRP", ago(3*time.Hour), "tvdbid", "9904242"),
		tv("Example.Saga.Seven.Harbours.S01E08.1080p.WEB.h264-GRP", ago(2*time.Hour)),
		tv("Example.Saga.Seven.Harbours.S01E09.1080p.WEB.h264-GRP", ago(2*time.Hour), "tvdbid", "1"),
		tv("Example.Saga.Seven.Harbours.S01E11.1080p.WEB.h264-GRP", ago(time.Hour)),
		tv("Example.Saga.Seven.Harbours.S01.1080p.WEB.h264-GRP", ago(time.Hour)),
		tv("Seven.Harbours.S01E04.1080p.WEB.h264-GRP", ago(50*time.Minute)),
		tv("Unrelated.Show.S02E01.1080p.WEB.h264-GRP", ago(40*time.Minute)),
		film("Some.Film.2026.1080p.WEB-DL.x264-GRP", ago(30*time.Minute)),
	)
	addFeed(t, r.svc, "feed", feedSrc)

	// Nothing grabs on its own: no feed is read, no query spent.
	if run, err := r.svc.SyncRSS(ctx); err != nil || run.Feeds != 0 || len(feedSrc.reads()) != 0 {
		t.Fatalf("with nothing grabbed on its own: %+v %v (reads %d)", run, err, len(feedSrc.reads()))
	}
	if _, err := r.svc.SaveSearchSettings(ctx, SearchSettings{PreferProtocol: "usenet", MaxConcurrentGrabs: 10, AutoGrabNew: true}, 0, "test"); err != nil {
		t.Fatal(err)
	}
	run, err := r.svc.SyncRSS(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := r.grabbed(t)
	want := map[string]string{
		"S01E07": "Example.Saga.Seven.Harbours.S01E07.1080p.WEB.h264-GRP",
		"S01E08": "Example.Saga.Seven.Harbours.S01E08.1080p.WEB.h264-GRP",
		"S01E04": "Seven.Harbours.S01E04.1080p.WEB.h264-GRP",
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("grabbed %v\nwant    %v", got, want)
	}
	if run.Feeds != 1 || run.Read != 8 || run.New != 8 || run.Grabbed != 3 {
		t.Errorf("the run: %+v", run)
	}
	reads := feedSrc.reads()
	if len(reads) != 1 || reads[0].Get("t") != "tvsearch" || reads[0].Get("cat") != "5000" || reads[0].Get("q") != "" || reads[0].Get("limit") != "100" ||
		reads[0].Get("extended") != "1" {
		t.Errorf("reads = %v, want one tv read of the TV category (no movie is wanted), every attribute asked for", reads)
	}
	for _, e := range []string{"s01e007", "s01e008", "s01e004"} {
		if st := target(t, r.svc, "series:990001:"+e); st.State != store.TargetGrabbed {
			t.Errorf("%s: %+v", e, st)
		}
	}
	for _, e := range []string{"s01e009", "s01e011"} {
		if st := target(t, r.svc, "series:990001:"+e); st.State != store.TargetWanted {
			t.Errorf("%s: %+v", e, st)
		}
	}

	// Read again: nothing it looked at is looked at twice.
	again, err := r.svc.SyncRSS(ctx)
	if err != nil || again.New != 0 || again.Grabbed != 0 || len(r.gw.added()) != 3 {
		t.Errorf("read again: %+v %v (adds %d)", again, err, len(r.gw.added()))
	}
	// The right E09 turns up.
	feedSrc.add(tv("Example.Saga.Seven.Harbours.S01E09.1080p.WEB.h264-OTHER", ago(time.Minute), "tvdbid", "9904242"))
	third, _ := r.svc.SyncRSS(ctx)
	if third.New != 1 || third.Grabbed != 1 {
		t.Errorf("the next release: %+v", third)
	}
	if got := r.grabbed(t); got["S01E09"] != "Example.Saga.Seven.Harbours.S01E09.1080p.WEB.h264-OTHER" {
		t.Errorf("E09: %v", got)
	}
}

// What the policy grabs: with only the new things, an episode that aired
// days ago, not the old backlog; with everything, the old backlog too.
func TestTheFeedsGrabWhatThePolicyAllows(t *testing.T) {
	r := newFollowRig(t)
	ctx := context.Background()
	_, eps := track(t, r.svc, 990711, "Show Name", 2, true)
	_ = r.svc.st.SetAirWindow(ctx, eps[0].ID, day(-300), 0)
	_ = r.svc.st.SetAirWindow(ctx, eps[1].ID, day(-2), 0)
	feedSrc := newFeedSource(t, false,
		tv("Show.Name.S01E01.1080p.WEB.h264-GRP", ago(2*time.Hour)),
		tv("Show.Name.S01E02.1080p.WEB.h264-GRP", ago(time.Hour)))
	addFeed(t, r.svc, "feed", feedSrc)
	if _, err := r.svc.SaveSearchSettings(ctx, SearchSettings{PreferProtocol: "usenet", MaxConcurrentGrabs: 10, AutoGrabNew: true}, 0, "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.SyncRSS(ctx); err != nil {
		t.Fatal(err)
	}
	if got := r.grabbed(t); len(got) != 1 || got["S01E02"] == "" {
		t.Errorf("new only: %v", got)
	}
	if st := target(t, r.svc, eps[0].ID); st.State != store.TargetWanted || st.SearchOutcome != "" {
		t.Errorf("the old backlog: %+v", st)
	}
	// A source that offers no typed search is read with t=search, its TV
	// category.
	if reads := feedSrc.reads(); len(reads) != 1 || reads[0].Get("t") != "search" || reads[0].Get("cat") != "5000" {
		t.Errorf("reads = %v", reads)
	}

	// Everything: the old one, which the next release of it brings.
	if _, err := r.svc.SaveSearchSettings(ctx, SearchSettings{PreferProtocol: "usenet", MaxConcurrentGrabs: 10, AutoGrab: true}, 0, "test"); err != nil {
		t.Fatal(err)
	}
	feedSrc.add(tv("Show.Name.S01E01.2160p.WEB.h265-GRP", ago(time.Minute)))
	if _, err := r.svc.SyncRSS(ctx); err != nil {
		t.Fatal(err)
	}
	if got := r.grabbed(t); got["S01E01"] != "Show.Name.S01E01.2160p.WEB.h265-GRP" {
		t.Errorf("everything: %v", got)
	}
}

// A requested movie is grabbed from the feeds once it is out, by its title
// and year — never before.
func TestTheFeedsGrabARequestedMovieOnceOut(t *testing.T) {
	r := newFollowRig(t)
	ctx := context.Background()
	out := day(3)
	r.tmdb.set(func(f *fakeTMDB) {
		f.movies[990100] = map[string]any{"id": 990100, "title": "Example Clay Film", "status": "Released", "release_date": "2026-09-11",
			"imdb_id": "tt99009100", "release_dates": map[string]any{"results": []map[string]any{{"iso_3166_1": "US",
				"release_dates": []map[string]any{{"type": 4, "release_date": out.Format(time.RFC3339)}}}}}}
	})
	if _, _, err := r.svc.Request(ctx, store.Wanted{TMDBID: 990100, MediaType: "movie", Title: "Example Clay Film", Year: 2026}, asAdmin("friend")); err != nil {
		t.Fatal(err)
	}
	r.settle(t, false)
	feedSrc := newFeedSource(t, true,
		film("Example.Clay.Film.2026.1080p.WEB-DL.x264-GRP", ago(time.Hour)),
		film("Example.Clay.Film.2026.HDCAM.x264-GRP", ago(time.Hour)))
	addFeed(t, r.svc, "feed", feedSrc)
	if _, err := r.svc.SaveSearchSettings(ctx, SearchSettings{PreferProtocol: "usenet", MaxConcurrentGrabs: 10, AutoGrabNew: true}, 0, "test"); err != nil {
		t.Fatal(err)
	}
	if run, _ := r.svc.SyncRSS(ctx); run.Feeds != 0 || len(r.gw.added()) != 0 {
		t.Errorf("before it is out: %+v (adds %d)", run, len(r.gw.added()))
	}
	// Out now; a release from before its date, still in the feed, is looked
	// at once it is.
	r.tmdb.set(func(f *fakeTMDB) {
		f.movies[990100]["release_dates"] = map[string]any{"results": []map[string]any{{"iso_3166_1": "US",
			"release_dates": []map[string]any{{"type": 4, "release_date": day(-1).Format(time.RFC3339)}}}}}
	})
	sqlExec(t, `UPDATE titles SET inventory_at = now() - interval '1 day' WHERE id = 'movie:990100'`)
	r.settle(t, true)
	run, err := r.svc.SyncRSS(ctx)
	if err != nil || run.Grabbed != 1 {
		t.Fatalf("once out: %+v %v", run, err)
	}
	if got := r.grabbed(t); got[""] != "Example.Clay.Film.2026.1080p.WEB-DL.x264-GRP" {
		t.Errorf("grabbed %v, the WEB-DL and not the cam", got)
	}
	if reads := feedSrc.reads(); len(reads) != 1 || reads[0].Get("t") != "movie" || reads[0].Get("cat") != "2000" {
		t.Errorf("reads = %v", reads)
	}
}

// A release of several episodes is grabbed on its own only when the policy
// lets an automatic grab take every one of them, and acquire knows what the
// library holds of each: with only the new things grabbed on their own, a
// file of a new episode and one of the old backlog waits for someone to pick
// it.
func TestARunOfEpisodesIsGrabbedOnlyAsThePolicyTakesEach(t *testing.T) {
	r := newFollowRig(t)
	ctx := context.Background()
	_, eps := track(t, r.svc, 990711, "Show Name", 4, true)
	_ = r.svc.st.SetAirWindow(ctx, eps[0].ID, day(-300), 0) // the old backlog
	_ = r.svc.st.SetAirWindow(ctx, eps[1].ID, day(-2), 0)   // new
	_ = r.svc.st.SetAirWindow(ctx, eps[2].ID, day(-2), 0)   // new
	_ = r.svc.st.SetAirWindow(ctx, eps[3].ID, day(-2), 0)   // new, of unknown holdings
	sqlExec(t, `UPDATE acquisition_targets SET added_at = now() + interval '1 minute' WHERE id = $1`, eps[3].ID)
	feedSrc := newFeedSource(t, true,
		tv("Show.Name.S01E01E02.1080p.WEB.h264-GRP", ago(2*time.Hour)),
		tv("Show.Name.S01E03E04.1080p.WEB.h264-GRP", ago(2*time.Hour)))
	addFeed(t, r.svc, "feed", feedSrc)
	if _, err := r.svc.SaveSearchSettings(ctx, SearchSettings{PreferProtocol: "usenet", MaxConcurrentGrabs: 10, AutoGrabNew: true}, 0, "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.SyncRSS(ctx); err != nil {
		t.Fatal(err)
	}
	if got := r.grabbed(t); len(got) != 0 {
		t.Errorf("grabbed %v: the old E01 with the new E02, or E04 of unknown holdings", got)
	}
	// Everything grabs on its own: E01 and E02 go as one; E04 still waits for
	// the catalog.
	if _, err := r.svc.SaveSearchSettings(ctx, SearchSettings{PreferProtocol: "usenet", MaxConcurrentGrabs: 10, AutoGrab: true}, 0, "test"); err != nil {
		t.Fatal(err)
	}
	feedSrc.add(tv("Show.Name.S01E01E02.720p.WEB.h264-GRP", ago(time.Minute)), tv("Show.Name.S01E03E04.720p.WEB.h264-GRP", ago(time.Minute)))
	if _, err := r.svc.SyncRSS(ctx); err != nil {
		t.Fatal(err)
	}
	if got := r.grabbed(t); len(got) != 1 || got["S01E01-E02"] != "Show.Name.S01E01E02.720p.WEB.h264-GRP" {
		t.Errorf("with everything grabbed on its own: %v", got)
	}
}

// An episode the library held once and lost — deleted, or missed by a
// reconcile — is not one of the new things any more: with only those grabbed
// on their own, the feeds do not grab it again; with everything, they do.
func TestWhatWasHeldOnceIsNotNewAgain(t *testing.T) {
	r := newFollowRig(t)
	ctx := context.Background()
	title, eps := track(t, r.svc, 990711, "Show Name", 1, true)
	_ = r.svc.st.SetAirWindow(ctx, eps[0].ID, day(-1), 0)
	if _, err := r.svc.st.ApplySeriesHoldings(ctx, title.ID, "", "default", []store.HeldEpisode{{Season: 1, Episode: 1, ItemID: "item-1"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.st.ApplySeriesHoldings(ctx, title.ID, "", "default", nil); err != nil {
		t.Fatal(err)
	}
	feedSrc := newFeedSource(t, true, tv("Show.Name.S01E01.1080p.WEB.h264-GRP", ago(time.Hour)))
	addFeed(t, r.svc, "feed", feedSrc)
	if _, err := r.svc.SaveSearchSettings(ctx, SearchSettings{PreferProtocol: "usenet", MaxConcurrentGrabs: 10, AutoGrabNew: true}, 0, "test"); err != nil {
		t.Fatal(err)
	}
	if run, _ := r.svc.SyncRSS(ctx); run.Feeds != 0 || len(r.gw.added()) != 0 {
		t.Errorf("with only the new things: %+v", run)
	}
	if _, err := r.svc.SaveSearchSettings(ctx, SearchSettings{PreferProtocol: "usenet", MaxConcurrentGrabs: 10, AutoGrab: true}, 0, "test"); err != nil {
		t.Fatal(err)
	}
	if run, _ := r.svc.SyncRSS(ctx); run.Grabbed != 1 {
		t.Errorf("with everything: %+v", run)
	}
}

// A requested movie is matched only by a release the feed files as a movie
// that names its title, its year and then tags only: no trailer, no
// making-of, no longer title that starts with its name, nothing a TV category
// carries. A series is matched only by what the feed files as TV, its
// episodes followed by tags — the episode's title may come first —, never a
// sample.
func TestTheFeedsMatchByCategoryAndTags(t *testing.T) {
	r := newFollowRig(t)
	ctx := context.Background()
	r.tmdb.set(func(f *fakeTMDB) {
		f.series[990001] = sevenHarbours()
		f.movies[990100] = map[string]any{"id": 990100, "title": "Example Clay Film", "status": "Released", "release_date": dateOf(day(-60)),
			"release_dates": map[string]any{"results": []map[string]any{{"iso_3166_1": "US",
				"release_dates": []map[string]any{{"type": 4, "release_date": day(-2).Format(time.RFC3339)}}}}}}
	})
	for _, w := range []store.Wanted{{TMDBID: 990001, MediaType: "series", Title: "Example Saga: Seven Harbours"},
		{TMDBID: 990100, MediaType: "movie", Title: "Example Clay Film", Year: 2026}} {
		if _, _, err := r.svc.Request(ctx, w, asAdmin("user")); err != nil {
			t.Fatal(err)
		}
	}
	r.settle(t, false)
	feedSrc := newFeedSource(t, true,
		film("Example.Clay.Film.2026.Official.Trailer.1080p.WEB-DL.x264-GRP", ago(3*time.Hour)),
		tv("Example.Clay.Film.2026.The.Making.Of.1080p.HDTV.x264-GRP", ago(3*time.Hour)),
		tv("Example.Clay.Film.2026.1080p.WEB-DL.x264-GRP", ago(3*time.Hour)),
		film("Example.Clay.Film.2026.Part.Two.1080p.WEB-DL.x264-GRP", ago(3*time.Hour)),
		film("Example.Saga.Seven.Harbours.S01E08.1080p.WEB.h264-GRP", ago(2*time.Hour)),
		tv("Example.Saga.Seven.Harbours.S01E07.Harbour.7.1080p.WEB.h264-GRP", ago(2*time.Hour)),
		tv("Example.Saga.Seven.Harbours.S01E09.Recap.Special.1080p.WEB.h264-GRP", ago(2*time.Hour)),
		tv("Example.Saga.Seven.Harbours.S01E04.Sample.1080p.WEB.h264-GRP", ago(2*time.Hour)),
	)
	addFeed(t, r.svc, "feed", feedSrc)
	if _, err := r.svc.SaveSearchSettings(ctx, SearchSettings{PreferProtocol: "usenet", MaxConcurrentGrabs: 10, AutoGrabNew: true}, 0, "test"); err != nil {
		t.Fatal(err)
	}
	run, err := r.svc.SyncRSS(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := r.grabbed(t); len(got) != 1 || got["S01E07"] != "Example.Saga.Seven.Harbours.S01E07.Harbour.7.1080p.WEB.h264-GRP" {
		t.Errorf("grabbed %v (%+v), want E07 alone", got, run)
	}
	// The movie's own release, filed as a movie.
	feedSrc.add(film("Example.Clay.Film.2026.1080p.WEB-DL.x264-GRP", ago(time.Minute)))
	if _, err := r.svc.SyncRSS(ctx); err != nil {
		t.Fatal(err)
	}
	if got := r.grabbed(t); got[""] != "Example.Clay.Film.2026.1080p.WEB-DL.x264-GRP" {
		t.Errorf("the movie: %v", got)
	}

	// A typed search rejects an extra as well.
	src := newFakeSource(t, "usenet", "K", "Example.Clay.Film.2026.Official.Trailer.2160p.WEB-DL.x265-GRP")
	addSource(t, r.svc, "example", src, nil)
	cands, err := r.svc.SearchTarget(ctx, "movie:990100:movie")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cands {
		if strings.Contains(c.Title, "Trailer") != c.Rejected ||
			(c.Rejected && !strings.Contains(c.Reason, `its name says "trailer": an extra, not the movie`)) {
			t.Errorf("a typed search of the movie: %s rejected %v (%s)", c.Title, c.Rejected, c.Reason)
		}
	}
}

// No feed is read while nothing could be grabbed — every download slot
// taken, the gateway out of reach, no room for a download —: a read spends a
// query of the source's daily allowance and moves its cursor past what it
// matched. Once a grab can be handed over the feeds are read, and what turned
// up meanwhile is grabbed. The feeds leave 30 % of a source's daily limit to
// the sweep and to people's searches.
func TestTheFeedsAreNotReadWhileNothingCanBeGrabbed(t *testing.T) {
	r := newFollowRig(t)
	ctx := context.Background()
	_, eps := track(t, r.svc, 990711, "Show Name", 3, true)
	for _, e := range eps {
		_ = r.svc.st.SetAirWindow(ctx, e.ID, day(-1), 0)
	}
	feedSrc := newFeedSource(t, true, tv("Show.Name.S01E01.1080p.WEB.h264-GRP", ago(time.Hour)))
	res := addFeed(t, r.svc, "feed", feedSrc)
	save := func(floor int64) {
		t.Helper()
		if _, err := r.svc.SaveSearchSettings(ctx, SearchSettings{PreferProtocol: "usenet", MaxConcurrentGrabs: 1, AutoGrabNew: true,
			StorageFloorGB: floor}, 0, "test"); err != nil {
			t.Fatal(err)
		}
	}
	save(0)
	skipped := func(want string) {
		t.Helper()
		run, err := r.svc.SyncRSS(ctx)
		if err != nil || !strings.Contains(run.Skipped, want) || run.Feeds != 0 || len(feedSrc.reads()) != 0 {
			t.Errorf("%s: %+v %v (reads %d)", want, run, err, len(feedSrc.reads()))
		}
	}
	sqlExec(t, `INSERT INTO downloads (adapter, client_job_id, state, title) VALUES ('nzbget', 'job-1', 'downloading', 'other')`)
	skipped("1 download(s) already in flight (cap 1)")
	sqlExec(t, `UPDATE downloads SET state = 'completed'`)

	r.gw.mu.Lock()
	r.gw.down = true
	r.gw.mu.Unlock()
	_ = r.svc.sync.Check(ctx)
	skipped("the gateway does not answer")
	r.gw.mu.Lock()
	r.gw.down = false
	r.gw.mu.Unlock()
	_ = r.svc.sync.Check(ctx)

	r.svc.freeSpace = func(string) (int64, error) { return 10 << 30, nil }
	save(5)
	skipped("but only 10.0 GB free")
	save(100)
	skipped("below the 100.0 GB floor")
	save(0)
	r.svc.freeSpace = func(string) (int64, error) { return 1 << 40, nil }

	run, err := r.svc.SyncRSS(ctx)
	if err != nil || run.Grabbed != 1 || len(feedSrc.reads()) != 1 {
		t.Fatalf("once a grab can be handed over: %+v %v (reads %d)", run, err, len(feedSrc.reads()))
	}

	// A daily limit of three: the feeds spend two, the third is the
	// sweep's.
	three := 3
	if _, err := r.svc.UpdateSource(ctx, res.ID, SourceInput{Name: "feed", Protocol: "usenet", BaseURL: feedSrc.srv.URL,
		QueryLimitDay: OptionalInt{Set: true, Value: &three}}, 0, "admin"); err != nil {
		t.Fatal(err)
	}
	sqlExec(t, `UPDATE downloads SET state = 'completed'`)
	sqlExec(t, `UPDATE target_grabs SET state = 'failed'`)
	sqlExec(t, `UPDATE indexer_usage SET queries = 1 WHERE indexer_id = $1`, res.ID)
	feedSrc.add(tv("Show.Name.S01E02.1080p.WEB.h264-GRP", ago(time.Minute)))
	if run, _ := r.svc.SyncRSS(ctx); run.Limited != 0 || len(feedSrc.reads()) != 2 {
		t.Errorf("the second read of the day: %+v (reads %d)", run, len(feedSrc.reads()))
	}
	sqlExec(t, `UPDATE target_grabs SET state = 'failed'`)
	if run, _ := r.svc.SyncRSS(ctx); run.Limited != 1 || run.Failed != 0 || len(feedSrc.reads()) != 2 {
		t.Errorf("the third, the sweep's: %+v (reads %d)", run, len(feedSrc.reads()))
	}
}

// A source that fails backs off as for any search and fails nothing: the
// others are read and grabbed from, and the next run leaves it alone while
// it backs off. Its cursor stays where it was.
func TestAFailingSourceDoesNotFailTheRun(t *testing.T) {
	r := newFollowRig(t)
	ctx := context.Background()
	_, eps := track(t, r.svc, 990711, "Show Name", 1, true)
	_ = r.svc.st.SetAirWindow(ctx, eps[0].ID, day(-1), 0)
	down := newFeedSource(t, true, tv("Show.Name.S01E01.2160p.WEB.h265-DOWN", ago(time.Hour)))
	good := newFeedSource(t, true, tv("Show.Name.S01E01.1080p.WEB.h264-GRP", ago(time.Hour)))
	downRes := addFeed(t, r.svc, "down", down)
	addFeed(t, r.svc, "good", good)
	down.mu.Lock()
	down.mode = "503"
	down.mu.Unlock()
	if _, err := r.svc.SaveSearchSettings(ctx, SearchSettings{PreferProtocol: "usenet", MaxConcurrentGrabs: 10, AutoGrabNew: true}, 0, "test"); err != nil {
		t.Fatal(err)
	}
	run, err := r.svc.SyncRSS(ctx)
	if err != nil || run.Failed != 1 || run.Grabbed != 1 {
		t.Fatalf("one source down: %+v %v", run, err)
	}
	ix, _ := r.svc.st.GetIndexer(ctx, downRes.ID)
	if ix.BackoffUntil == nil {
		t.Errorf("the failing source does not back off: %+v", ix)
	}
	if cur, _ := r.svc.st.GetRSSCursor(ctx, downRes.ID, "tv"); cur.Read() {
		t.Errorf("a failed read moved the cursor: %+v", cur)
	}
	before := len(down.reads())
	if _, err := r.svc.SyncRSS(ctx); err != nil || len(down.reads()) != before {
		t.Errorf("a source backing off was read: %v (%d → %d)", err, before, len(down.reads()))
	}
}

// A feed that moved further than a page since its last read is read back a
// page at a time until the releases it read before, up to three pages; one
// never read is read once.
func TestAFeedIsReadBackToItsCursor(t *testing.T) {
	r := newFollowRig(t)
	ctx := context.Background()
	_, eps := track(t, r.svc, 990711, "Show Name", 9, true)
	for _, e := range eps {
		_ = r.svc.st.SetAirWindow(ctx, e.ID, day(-1), 0)
	}
	feedSrc := newFeedSource(t, true, tv("Other.Show.S01E01.1080p.WEB-GRP", ago(10*time.Hour)), tv("Other.Show.S01E02.1080p.WEB-GRP", ago(9*time.Hour)))
	feedSrc.max = 2
	addFeed(t, r.svc, "feed", feedSrc)
	if _, err := r.svc.SaveSearchSettings(ctx, SearchSettings{PreferProtocol: "usenet", MaxConcurrentGrabs: 20, AutoGrabNew: true}, 0, "test"); err != nil {
		t.Fatal(err)
	}
	if run, _ := r.svc.SyncRSS(ctx); run.Read != 2 || len(feedSrc.reads()) != 1 {
		t.Fatalf("a feed never read: %+v (reads %d)", run, len(feedSrc.reads()))
	}
	// Five releases since: two pages of new ones, then the cursor.
	for e := 1; e <= 5; e++ {
		feedSrc.add(tv(fmt.Sprintf("Show.Name.S01E%02d.1080p.WEB-GRP", e), ago(time.Duration(6-e)*time.Minute)))
	}
	run, err := r.svc.SyncRSS(ctx)
	if err != nil || run.New != 5 || run.Grabbed != 5 {
		t.Errorf("read back: %+v %v", run, err)
	}
	var offsets []string
	for _, q := range feedSrc.reads()[1:] {
		offsets = append(offsets, q.Get("offset")+"/"+q.Get("limit"))
	}
	if strings.Join(offsets, " ") != "/2 2/2 4/2" {
		t.Errorf("pages = %q", offsets)
	}
	// Ten more since: three pages, and the rest left to the sweep.
	for e := 1; e <= 10; e++ {
		feedSrc.add(tv(fmt.Sprintf("Third.Show.S01E%02d.1080p.WEB-GRP", e), time.Now().Add(time.Duration(e)*time.Second-time.Minute)))
	}
	if run, _ := r.svc.SyncRSS(ctx); run.Read != 6 {
		t.Errorf("a feed far ahead: %+v", run)
	}
}

// Each release is looked at once, however the source dates it: one whose
// clock runs ahead of acquire's is not read again on every run, and a
// release it indexes late, dated before what was looked at, is read once it
// is listed.
func TestEachReleaseIsLookedAtOnce(t *testing.T) {
	r := newFollowRig(t)
	ctx := context.Background()
	_, eps := track(t, r.svc, 990711, "Show Name", 1, true)
	_ = r.svc.st.SetAirWindow(ctx, eps[0].ID, day(-1), 0)
	ahead := time.Now().Add(time.Hour).Truncate(time.Second)
	var items []feedItem
	for i := 0; i < 5; i++ {
		items = append(items, tv(fmt.Sprintf("Other.Show.S01E%02d.1080p.WEB-GRP", i+1), ahead.Add(-time.Duration(i)*time.Minute)))
	}
	feedSrc := newFeedSource(t, true, items...)
	addFeed(t, r.svc, "feed", feedSrc)
	if _, err := r.svc.SaveSearchSettings(ctx, SearchSettings{PreferProtocol: "usenet", MaxConcurrentGrabs: 10, AutoGrabNew: true}, 0, "test"); err != nil {
		t.Fatal(err)
	}
	var news []int
	for i := 0; i < 3; i++ {
		run, _ := r.svc.SyncRSS(ctx)
		news = append(news, run.New)
	}
	if fmt.Sprint(news) != "[5 0 0]" {
		t.Errorf("new per run: %v, want the five once", news)
	}
	// Indexed now, published before the newest looked at.
	feedSrc.add(tv("Show.Name.S01E01.1080p.WEB.h264-GRP", ago(30*time.Minute)))
	if run, _ := r.svc.SyncRSS(ctx); run.New != 1 || run.Grabbed != 1 {
		t.Errorf("a release indexed late: %+v", run)
	}
}

func TestFeedsOfASource(t *testing.T) {
	typed := indexer.Source{Caps: &indexer.Caps{TVSearch: indexer.SearchMode{Available: true}, MovieSearch: indexer.SearchMode{Available: true}}}
	tvOnly := indexer.Source{Caps: &indexer.Caps{TVSearch: indexer.SearchMode{Available: true}}}
	plain := indexer.Source{}
	for _, tc := range []struct {
		name      string
		src       indexer.Source
		tv, movie bool
		want      string
	}{
		{"typed, both wanted", typed, true, true, "tv:tvsearch:tv movie:movie:movie"},
		{"typed, episodes", typed, true, false, "tv:tvsearch:tv"},
		{"tv search only, both wanted", tvOnly, true, true, "tv:tvsearch:tv search:search:movie"},
		{"no typed search", plain, true, true, "search:search:"},
		{"no typed search, movies", plain, false, true, "search:search:movie"},
		{"nothing wanted", typed, false, false, ""},
	} {
		var got []string
		for _, f := range feedsOf(tc.src, tc.tv, tc.movie) {
			got = append(got, f.name+":"+f.kind+":"+f.media)
		}
		if strings.Join(got, " ") != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, strings.Join(got, " "), tc.want)
		}
	}
}

// A release names a tracked title by its title or an alias — exactly, or
// with the series' year or a country after it —, a movie with its year; a
// name several share, or the feed's identities contradict, names none.
func TestMatchingAReleaseToATitle(t *testing.T) {
	m := matcher{series: map[string][]store.Title{}, movies: map[string][]store.Title{}}
	add := func(into map[string][]store.Title, t store.Title, names ...string) {
		for _, n := range append([]string{t.Title}, names...) {
			into[release.MatchKey(n)] = append(into[release.MatchKey(n)], t)
		}
	}
	saga := store.Title{ID: "series:990001", Title: "Example Saga: Seven Harbours", Year: 2026, TVDBID: 9904242, Kind: "series"}
	remake := store.Title{ID: "series:2", Title: "Twin Show", Year: 2019, Kind: "series"}
	original := store.Title{ID: "series:3", Title: "Twin Show", Year: 1990, Kind: "series"}
	bureau := store.Title{ID: "series:4", Title: "Example Bureau", Year: 2011, Kind: "series", Countries: []string{"GB"}, TVDBID: 77}
	american := store.Title{ID: "series:5", Title: "Example Office", Year: 2005, Kind: "series", Countries: []string{"US"}}
	named := store.Title{ID: "series:6", Title: "Example Clinic (US)", Year: 2001, Kind: "series"}
	add(m.series, saga, "Seven Harbours")
	add(m.series, remake)
	add(m.series, original)
	add(m.series, bureau)
	add(m.series, american)
	add(m.series, named)
	clay := store.Title{ID: "movie:990100", Title: "Example Clay Film", Year: 2026, IMDBID: "tt99009100", Kind: "movie"}
	add(m.movies, clay)
	for _, tc := range []struct {
		name string
		r    release.Release
		want string
	}{
		{"title", release.Release{Title: "Example.Saga.Seven.Harbours.S01E07.1080p.WEB"}, saga.ID},
		{"alias", release.Release{Title: "Seven.Harbours.S01E07.1080p.WEB"}, saga.ID},
		{"its year", release.Release{Title: "Twin.Show.2019.S01E01.1080p.WEB"}, remake.ID},
		{"the other year", release.Release{Title: "Twin.Show.1990.S01E01.DVDRip"}, original.ID},
		{"a name two share", release.Release{Title: "Twin.Show.S01E01.1080p.WEB"}, ""},
		{"two told apart by the feed", release.Release{Title: "Twin.Show.S01E01.1080p.WEB", TVDBID: 0}, ""},
		{"another country's series of the name", release.Release{Title: "Example.Bureau.US.S02E01.720p.HDTV"}, ""},
		{"its own country", release.Release{Title: "Example.Bureau.UK.S02E01.720p.HDTV"}, bureau.ID},
		{"another country, the feed names it", release.Release{Title: "Example.Bureau.US.S02E01.720p.HDTV", TVDBID: 77}, bureau.ID},
		{"its country, as TMDB says", release.Release{Title: "Example.Office.US.S02E01.720p.HDTV"}, american.ID},
		{"a title that names its country", release.Release{Title: "Example.Clinic.US.S01E01.720p.HDTV"}, named.ID},
		{"an episode's own IMDb id", release.Release{Title: "Seven.Harbours.S01E07.1080p.WEB", IMDBID: "tt99000009"}, saga.ID},
		{"the feed says another series", release.Release{Title: "Seven.Harbours.S01E07.1080p.WEB", TVDBID: 7}, ""},
		{"the feed agrees", release.Release{Title: "Seven.Harbours.S01E07.1080p.WEB", TVDBID: 9904242}, saga.ID},
		{"a longer name", release.Release{Title: "Example.Saga.Seven.Harbours.Aftershow.S01E07.1080p"}, ""},
		{"a movie", release.Release{Title: "Example.Clay.Film.2026.1080p.WEB-DL"}, clay.ID},
		{"a movie, another year", release.Release{Title: "Example.Clay.Film.2025.1080p.WEB-DL"}, ""},
		{"a movie a year off, its IMDb id", release.Release{Title: "Example.Clay.Film.2025.1080p.WEB-DL", IMDBID: "tt99009100"}, clay.ID},
		{"a movie, another IMDb id", release.Release{Title: "Example.Clay.Film.2026.1080p.WEB-DL", IMDBID: "tt99000001"}, ""},
		{"a movie without its year", release.Release{Title: "Example.Clay.Film.1080p.WEB-DL"}, ""},
	} {
		got := ""
		if name, cov := release.SeriesPart(tc.r.Title); cov.Found {
			if ti, ok := m.seriesOf(tc.r, name); ok {
				got = ti.ID
			}
		} else if ti, ok := m.movieOf(tc.r); ok {
			got = ti.ID
		}
		if got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}
