package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/laedeli/acquire/internal/store"
	"github.com/laedeli/acquire/internal/tvmaze"
)

// ── a schedule source of the tests' own ────────────────────────────────────

// fakeSchedule is a TVmaze of the tests' own: shows by the id they are
// looked up by ("thetvdb=…", "imdb=…"), and their episodes, each with its
// local day and hour and its airstamp; down answers 503. The ids are
// invented.
type fakeSchedule struct {
	mu    sync.Mutex
	shows map[string]int64
	eps   map[int64][]map[string]any
	down  bool
	calls []string
}

// newFakeSchedule gives a service the schedule source of the tests' own.
func newFakeSchedule(t *testing.T, svc *Service) *fakeSchedule {
	t.Helper()
	f := &fakeSchedule{shows: map[string]int64{}, eps: map[int64][]map[string]any{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.calls = append(f.calls, r.URL.RequestURI())
		if f.down {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		switch {
		case r.URL.Path == "/lookup/shows":
			for k, v := range r.URL.Query() {
				if id, ok := f.shows[k+"="+v[0]]; ok {
					http.Redirect(w, r, fmt.Sprintf("/shows/%d", id), http.StatusMovedPermanently)
					return
				}
			}
			http.NotFound(w, r)
		case strings.HasPrefix(r.URL.Path, "/shows/") && strings.HasSuffix(r.URL.Path, "/episodes"):
			id, _ := strconv.ParseInt(strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/shows/"), "/episodes"), 10, 64)
			eps, ok := f.eps[id]
			if !ok {
				http.NotFound(w, r)
				return
			}
			_ = json.NewEncoder(w).Encode(eps)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	c := tvmaze.New(srv.URL, "acquire-test")
	c.Every = time.Millisecond
	svc.sched = c
	return f
}

func (f *fakeSchedule) set(fn func(f *fakeSchedule)) {
	f.mu.Lock()
	fn(f)
	f.mu.Unlock()
}

func (f *fakeSchedule) called() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

// local is hh:mm on day d (days from today) in zone.
func local(d int, hh, mm int, zone string) time.Time {
	loc, err := time.LoadLocation(zone)
	if err != nil {
		panic(err)
	}
	t := day(d)
	return time.Date(t.Year(), t.Month(), t.Day(), hh, mm, 0, 0, loc)
}

// airs is an episode as TVmaze lists it, airing at at, in at's zone.
func airs(season, number int, at time.Time) map[string]any {
	return map[string]any{"id": season*1000 + number, "season": season, "number": number,
		"airdate": at.Format("2006-01-02"), "airtime": at.Format("15:04"), "airstamp": at.UTC().Format(time.RFC3339)}
}

// nightShow is a series that airs at 21:00 in New York: its first episode
// two days ago, its second tonight. The name and ids are invented.
func nightShow(r *followRig, f *fakeSchedule) {
	r.tmdb.set(func(t *fakeTMDB) {
		t.series[990721] = &tmdbSeries{name: "Example Night Show", status: "Returning Series", tvdb: 9907721, firstAir: day(-2),
			countries: []string{"US"}, seasons: map[int][]tmdbEpisode{1: {{title: "One", air: day(-2)}, {title: "Two", air: day(0)}}}}
	})
	f.set(func(f *fakeSchedule) {
		f.shows["thetvdb=9907721"] = 501
		f.eps[501] = []map[string]any{airs(1, 1, local(-2, 21, 0, "America/New_York")), airs(1, 2, local(0, 21, 0, "America/New_York"))}
	})
}

func autoGrabNew(t *testing.T, svc *Service, airTimes string) {
	t.Helper()
	if _, err := svc.SaveSearchSettings(context.Background(), SearchSettings{PreferProtocol: "usenet", MaxConcurrentGrabs: 10,
		AutoGrabNew: true, AirDelayHours: DefaultAirDelayHours, AirTimes: airTimes,
		AirDelayAfterAirstampMinutes: DefaultAirDelayAfterAirstampMinutes}, 0, "test"); err != nil {
		t.Fatal(err)
	}
}

// ── what a release published before it aired is ───────────────────────────

// A release named for an episode and published before it aired is most often
// a fake: with an air time, half an hour before it; without one, midnight of
// its air date where the series comes from — UTC when TMDB names no country
// acquire knows. Of a requested movie, midnight of the day it comes out to
// watch at home, where it does; one dated in cinemas only is told by nothing.
func TestWhenAReleaseIsPublishedTooEarly(t *testing.T) {
	stamp := local(-2, 21, 0, "America/New_York")
	season, ep := 1, 1
	episode := store.Target{Kind: "episode", SeasonNumber: &season, EpisodeNumber: &ep, AirDate: day(-2), AirStamp: ptr(stamp.UTC())}
	us := store.Title{Kind: "series", Countries: []string{"US"}}
	at := func(c time.Time) Candidate { return Candidate{Title: "Show.S01E01.1080p.WEB-GRP", PubDate: &c} }
	none := func() map[[2]int]store.Target { return nil }
	for _, tc := range []struct {
		name   string
		title  store.Title
		target store.Target
		pub    time.Time
		stamps bool
		want   string
	}{
		{"15 hours before an evening broadcast", us, episode, stamp.Add(-15 * time.Hour), true,
			"published before the episode aired (published " + stamp.Add(-15*time.Hour).UTC().Format("2006-01-02 15:04 UTC") +
				"; S01E01 aired " + stamp.UTC().Format("2006-01-02 15:04 UTC") + "): most likely a fake"},
		{"20 minutes before it", us, episode, stamp.Add(-20 * time.Minute), true, ""},
		{"air times off: the day, in New York", us, episode, stamp.Add(-15 * time.Hour), false, ""},
		{"air times off: before the day began in New York", us, episode, local(-2, 0, 0, "America/New_York").Add(-time.Minute), false,
			"published before the episode aired (published " + local(-2, 0, 0, "America/New_York").Add(-time.Minute).UTC().Format("2006-01-02 15:04 UTC") +
				"; S01E01 aired on " + dateOf(day(-2)) + ", America/New_York): most likely a fake"},
		{"no country: UTC", store.Title{Kind: "series"}, store.Target{Kind: "episode", SeasonNumber: &season, EpisodeNumber: &ep,
			AirDate: day(-2)}, day(-2).Add(-time.Minute), true, "published before the episode aired"},
		{"a movie out digitally, in the US", store.Title{Kind: "movie"}, store.Target{Kind: "movie", AirDate: day(-2), ReleaseKind: "digital",
			ReleaseCountry: "US"}, local(-2, 0, 0, "America/New_York").Add(-time.Minute), true,
			"published before the movie came out (published " + local(-2, 0, 0, "America/New_York").Add(-time.Minute).UTC().Format("2006-01-02 15:04 UTC") +
				"; it came out digitally on " + dateOf(day(-2)) + ", America/New_York): most likely a fake"},
		{"a movie in cinemas only", store.Title{Kind: "movie"}, store.Target{Kind: "movie", AirDate: day(-100), ReleaseKind: "theatrical"},
			*day(-200), true, ""},
	} {
		got := preAired(at(tc.pub), tc.title, tc.target, none, tc.stamps)
		if (tc.want == "") != (got == "") || !strings.HasPrefix(got, tc.want) {
			t.Errorf("%s:\n  got  %q\n  want %q", tc.name, got, tc.want)
		}
	}
}

// The feeds skip a release published before its episode aired — saying so
// on the episode, never blocking it — and take nothing of an episode within
// its air delay. The real episode, published after, is grabbed.
func TestTheFeedsSkipWhatWasPublishedBeforeTheAirDate(t *testing.T) {
	r := newFollowRig(t)
	ctx := context.Background()
	_, eps := track(t, r.svc, 990711, "Show Name", 2, true)
	_ = r.svc.st.SetAirWindow(ctx, eps[0].ID, day(-1), 0)
	_ = r.svc.st.SetAirWindow(ctx, eps[1].ID, day(0), 0)
	fake := "Show.Name.S01E01.1080p.WEB.h264-FAKE"
	feedSrc := newFeedSource(t, true,
		tv(fake, day(-1).Add(-6*time.Hour)),
		tv("Show.Name.S01E02.1080p.WEB.h264-GRP", *day(0)))
	addFeed(t, r.svc, "feed", feedSrc)
	if d := r.svc.envSearchSettings(); d.AirDelayHours != 30 || d.AirTimes != AirTimesTVmaze || d.AirDelayAfterAirstampMinutes != 60 {
		t.Fatalf("the defaults: %+v", d)
	}
	autoGrabNew(t, r.svc, "")
	run, err := r.svc.SyncRSS(ctx)
	if err != nil || run.Grabbed != 0 || len(r.gw.added()) != 0 {
		t.Fatalf("the fake and an episode within its delay: %+v %v (adds %d)", run, err, len(r.gw.added()))
	}
	if blocked, _ := r.svc.st.BlockedFor(ctx, eps[0].ID); len(blocked) != 0 {
		t.Errorf("the fake is blocked: %v", blocked)
	}
	if st := target(t, r.svc, eps[0].ID); !strings.Contains(st.Detail, "1 release(s) in a source's newest releases published before it aired") {
		t.Errorf("E01 says nothing of the release skipped: %q", st.Detail)
	}
	if st := target(t, r.svc, eps[1].ID); st.State != store.TargetWanted {
		t.Errorf("E02, aired today, grabbed within the delay: %+v", st)
	}

	// The real E01 turns up; without a delay, a second E02 is grabbed too.
	feedSrc.add(tv("Show.Name.S01E01.1080p.WEB.h264-GRP", ago(time.Minute)), tv("Show.Name.S01E02.720p.WEB.h264-GRP", *day(0)))
	if _, err := r.svc.SaveSearchSettings(ctx, SearchSettings{PreferProtocol: "usenet", MaxConcurrentGrabs: 10, AutoGrabNew: true}, 0, "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.SyncRSS(ctx); err != nil {
		t.Fatal(err)
	}
	got := r.grabbed(t)
	if got["S01E01"] != "Show.Name.S01E01.1080p.WEB.h264-GRP" || got["S01E02"] != "Show.Name.S01E02.720p.WEB.h264-GRP" {
		t.Errorf("grabbed %v", got)
	}
}

// The sweep searches nothing before its air delay is over, and skips a
// release published before its target aired — never blocking it: a grab now
// in the missing tab says why it took nothing, and a person may pick the
// release. The same holds for a requested movie and the day it came out to
// watch at home, where it came out.
func TestTheSweepSkipsWhatWasPublishedBeforeTheAirDate(t *testing.T) {
	fake, real := "Show.Name.S01E01.2160p.WEB-DL.DDP5.1.x265-FAKE", "Show.Name.S01E01.720p.WEB.h264-GRP"
	r := newTargetRig(t, fake, real)
	ctx := context.Background()
	_, eps := track(t, r.svc, 990711, "Show Name", 2, true)
	_ = r.svc.st.SetAirWindow(ctx, eps[0].ID, day(-2), 0)
	_ = r.svc.st.SetAirWindow(ctx, eps[1].ID, day(0), 0)
	r.src.mu.Lock()
	r.src.pubs = map[string]time.Time{fake: *day(-3), real: ago(time.Hour)}
	r.src.mu.Unlock()
	if _, err := r.svc.SaveSearchSettings(ctx, SearchSettings{PreferProtocol: "usenet", MaxConcurrentGrabs: 3, AutoGrab: true,
		AirDelayHours: 30}, 0, "test"); err != nil {
		t.Fatal(err)
	}
	cands, err := r.svc.SearchTarget(ctx, eps[0].ID)
	if err != nil || len(cands) != 2 || cands[0].Title != real || !cands[1].Rejected || cands[1].PubDate == nil ||
		!strings.Contains(cands[1].Reason, "published before the episode aired (published "+dateOf(day(-3))+" 00:00 UTC; S01E01 aired on "+dateOf(day(-2))+", UTC)") {
		t.Fatalf("the typed search: %+v %v", cands, err)
	}
	if err := r.svc.SweepBacklog(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if got := handedOver(t, r); len(got) != 1 || got[0] != real {
		t.Errorf("the sweep handed over %v, want the real E01 only", got)
	}
	if blocked, _ := r.svc.st.BlockedFor(ctx, eps[0].ID); len(blocked) != 0 {
		t.Errorf("the fake is blocked for E01: %v", blocked)
	}
	if st := target(t, r.svc, eps[1].ID); st.LastSearchAt != nil {
		t.Errorf("E02, aired today, searched within the delay: %+v", st)
	}

	// Only a fake for E02, out of its delay: grab now says why it takes
	// nothing — not "no releases" —, the sweep records it, and a pick takes
	// it.
	_ = r.svc.st.SetAirWindow(ctx, eps[1].ID, day(-2), 0)
	early := "Show.Name.S01E02.1080p.WEB.h264-FAKE"
	r.src.mu.Lock()
	r.src.titles = []string{early}
	r.src.pubs = map[string]time.Time{early: *day(-3)}
	r.src.mu.Unlock()
	_, err = r.svc.GrabTarget(ctx, eps[1].ID, "missing")
	if !errors.Is(err, errNoReleases) || strings.Contains(err.Error(), "no releases found") ||
		!strings.Contains(err.Error(), "1 release(s) found for Show Name S01E02, 1 of them published before it aired") {
		t.Errorf("grab now: %v", err)
	}
	if st := target(t, r.svc, eps[1].ID); !strings.Contains(st.SearchDetail, "1 release(s), every one published before it aired") {
		t.Errorf("the search recorded: %q", st.SearchDetail)
	}
	cands, _ = r.svc.SearchTarget(ctx, eps[1].ID)
	if len(cands) != 1 || !cands[0].Rejected {
		t.Fatalf("the picker's releases: %+v", cands)
	}
	if _, err := r.svc.GrabTargetCandidate(ctx, eps[1].ID, cands[0]); err != nil {
		t.Errorf("a person picks the release: %v", err)
	}

	// A requested movie, out at home two days ago in the US: a release from
	// before the day began there.
	m := newFollowRig(t)
	m.tmdb.set(func(f *fakeTMDB) {
		f.movies[990100] = map[string]any{"id": 990100, "title": "Example Clay Film", "status": "Released", "release_date": dateOf(day(-60)),
			"release_dates": map[string]any{"results": []map[string]any{{"iso_3166_1": "US",
				"release_dates": []map[string]any{{"type": 4, "release_date": day(-2).Format(time.RFC3339)}}}}}}
	})
	if _, _, err := m.svc.Request(ctx, store.Wanted{TMDBID: 990100, MediaType: "movie", Title: "Example Clay Film", Year: 2026}, asAdmin("friend")); err != nil {
		t.Fatal(err)
	}
	m.settle(t, false)
	if tg, _ := m.svc.st.TargetAt(ctx, "movie:990100", nil, nil); tg.ReleaseCountry != "US" {
		t.Errorf("where it came out: %+v", tg)
	}
	before, out := "Example.Clay.Film.2026.2160p.WEB-DL.DDP5.1.x265-FAKE", "Example.Clay.Film.2026.1080p.WEB-DL.x264-GRP"
	src := newFakeSource(t, "usenet", "K", before, out)
	src.pubs = map[string]time.Time{before: local(-2, 0, 0, "America/New_York").Add(-time.Hour), out: ago(time.Hour)}
	addSource(t, m.svc, "example", src, nil)
	if _, err := m.svc.SaveSearchSettings(ctx, SearchSettings{PreferProtocol: "usenet", MaxConcurrentGrabs: 3, AutoGrabNew: true,
		AirDelayHours: 30}, 0, "test"); err != nil {
		t.Fatal(err)
	}
	if err := m.svc.SweepBacklog(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if got := handedOver(t, m.targetRig); len(got) != 1 || got[0] != out {
		t.Errorf("the sweep handed over %v, want the movie's release from after it came out", got)
	}
	if blocked, _ := m.svc.st.BlockedFor(ctx, "movie:990100:movie"); len(blocked) != 0 {
		t.Errorf("the early release is blocked: %v", blocked)
	}
}

// A broadcast east of UTC — in Tokyo, the night TMDB dates — is out before
// the day begins in UTC: its release is no fake, and grabbed; one published
// before the day began in Tokyo is skipped. Nothing is blocked.
func TestAirDaysBeginWhereTheSeriesComesFrom(t *testing.T) {
	legit, fake := "Show.Name.S01E01.1080p.WEB.h264-GRP", "Show.Name.S01E01.1080p.WEB.h264-FAKE"
	r := newTargetRig(t, legit, fake)
	ctx := context.Background()
	r.src.mu.Lock()
	// Broadcast at 02:00 in Tokyo on the day TMDB dates, out an hour later;
	// the fake from the evening before, in Tokyo.
	r.src.pubs = map[string]time.Time{legit: local(-2, 3, 0, "Asia/Tokyo"), fake: local(-3, 19, 0, "Asia/Tokyo")}
	r.src.mu.Unlock()
	title, eps := track(t, r.svc, 990731, "Show Name", 1, true)
	_ = r.svc.st.SetAirWindow(ctx, eps[0].ID, day(-2), 0)
	sqlExec(t, `UPDATE titles SET origin_country = '{JP}' WHERE id = $1`, title.ID)
	cands, err := r.svc.SearchTarget(ctx, eps[0].ID)
	if err != nil || len(cands) != 2 {
		t.Fatalf("the typed search: %+v %v", cands, err)
	}
	for _, c := range cands {
		if c.Rejected != (c.Title == fake) ||
			(c.Rejected && !strings.Contains(c.Reason, "S01E01 aired on "+dateOf(day(-2))+", Asia/Tokyo")) {
			t.Errorf("%s: rejected %v (%s)", c.Title, c.Rejected, c.Reason)
		}
	}
	if _, err := r.svc.GrabTarget(ctx, eps[0].ID, "missing"); err != nil {
		t.Fatalf("grab now: %v", err)
	}
	if got := handedOver(t, r); len(got) != 1 || got[0] != legit {
		t.Errorf("handed over %v", got)
	}
	if blocked, _ := r.svc.st.BlockedFor(ctx, eps[0].ID); len(blocked) != 0 {
		t.Errorf("blocked: %v", blocked)
	}
}

// ── exact air times ────────────────────────────────────────────────────────

// With the moment an episode airs from the schedule source, a release posted
// on its air day but hours before an evening broadcast is a fake the feeds
// skip; the episode is grabbed an hour after it aired, and one that airs
// tonight not before. With air times off, the day alone tells — and the
// evening's fake is no longer told from the real.
func TestExactAirTimesInTheFeeds(t *testing.T) {
	r := newFollowRig(t)
	ctx := context.Background()
	f := newFakeSchedule(t, r.svc)
	nightShow(r, f)
	if _, _, err := r.svc.Request(ctx, store.Wanted{TMDBID: 990721, MediaType: "series", Title: "Example Night Show"}, asAdmin("admin")); err != nil {
		t.Fatal(err)
	}
	run := r.settle(t, false)
	one, two := target(t, r.svc, "series:990721:s01e001"), target(t, r.svc, "series:990721:s01e002")
	first, tonight := local(-2, 21, 0, "America/New_York").UTC(), local(0, 21, 0, "America/New_York").UTC()
	if run.Scheduled != 1 || one.AirStamp == nil || !one.AirStamp.Equal(first) || two.AirStamp == nil || !two.AirStamp.Equal(tonight) {
		t.Fatalf("the air times: %+v; %v %v", run, one.AirStamp, two.AirStamp)
	}
	fake := "Example.Night.Show.S01E01.2160p.WEB.h265-FAKE"
	feedSrc := newFeedSource(t, true,
		tv(fake, first.Add(-15*time.Hour)),
		tv("Example.Night.Show.S01E02.1080p.WEB.h264-EARLY", ago(time.Minute)))
	addFeed(t, r.svc, "feed", feedSrc)
	autoGrabNew(t, r.svc, "")
	if run, err := r.svc.SyncRSS(ctx); err != nil || run.Grabbed != 0 {
		t.Fatalf("the fake of the air day, and an episode that airs tonight: %+v %v", run, err)
	}
	if st := target(t, r.svc, one.ID); st.State != store.TargetWanted || !strings.Contains(st.Detail, "published before it aired") {
		t.Errorf("E01: %+v", st)
	}
	if blocked, _ := r.svc.st.BlockedFor(ctx, one.ID); len(blocked) != 0 {
		t.Errorf("blocked: %v", blocked)
	}

	feedSrc.add(tv("Example.Night.Show.S01E01.1080p.WEB.h264-GRP", first.Add(20*time.Minute)))
	if _, err := r.svc.SyncRSS(ctx); err != nil {
		t.Fatal(err)
	}
	if got := r.grabbed(t); len(got) != 1 || got["S01E01"] != "Example.Night.Show.S01E01.1080p.WEB.h264-GRP" {
		t.Errorf("grabbed %v, want the real E01 alone", got)
	}

	// Air times off: the day begins at midnight in New York, and the
	// evening's fake of an episode is taken for the episode.
	r2 := newFollowRig(t)
	f2 := newFakeSchedule(t, r2.svc)
	nightShow(r2, f2)
	if _, _, err := r2.svc.Request(ctx, store.Wanted{TMDBID: 990721, MediaType: "series", Title: "Example Night Show"}, asAdmin("admin")); err != nil {
		t.Fatal(err)
	}
	r2.settle(t, false)
	addFeed(t, r2.svc, "feed", newFeedSource(t, true, tv(fake, first.Add(-15*time.Hour))))
	autoGrabNew(t, r2.svc, AirTimesOff)
	if run, _ := r2.svc.SyncRSS(ctx); run.Grabbed != 1 {
		t.Errorf("with air times off: %+v", run)
	}
}

// The sweep searches an episode only once its delay after the moment it
// aired is over, and skips the fake from before the broadcast.
func TestExactAirTimesInTheSweep(t *testing.T) {
	fake, real := "Example.Night.Show.S01E01.2160p.WEB.h265-FAKE", "Example.Night.Show.S01E01.1080p.WEB.h264-GRP"
	r := newFollowRig(t)
	ctx := context.Background()
	f := newFakeSchedule(t, r.svc)
	nightShow(r, f)
	if _, _, err := r.svc.Request(ctx, store.Wanted{TMDBID: 990721, MediaType: "series", Title: "Example Night Show"}, asAdmin("admin")); err != nil {
		t.Fatal(err)
	}
	r.settle(t, false)
	first := local(-2, 21, 0, "America/New_York").UTC()
	src := newFakeSource(t, "usenet", "K", fake, real)
	src.pubs = map[string]time.Time{fake: first.Add(-15 * time.Hour), real: first.Add(25 * time.Minute)}
	addSource(t, r.svc, "example", src, nil)
	autoGrabNew(t, r.svc, "")
	due, _ := r.svc.st.DueTargetsBy(ctx, 10, r.svc.grabPolicy(ctx).dueOrder())
	if len(due) != 1 || due[0].ID != "series:990721:s01e001" {
		t.Errorf("due: %v, want E01 alone — E02 airs tonight", ids(due))
	}
	if err := r.svc.SweepBacklog(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if got := handedOver(t, r.targetRig); len(got) != 1 || got[0] != real {
		t.Errorf("the sweep handed over %v, want the real E01", got)
	}
	if blocked, _ := r.svc.st.BlockedFor(ctx, "series:990721:s01e001"); len(blocked) != 0 {
		t.Errorf("blocked: %v", blocked)
	}
}

// A series' show id is looked up once, its air times read again as it is,
// and a schedule that dates an episode more than a day from TMDB gives it no
// air time — the two count it differently. TMDB moving an episode drops its
// air time; the source failing keeps what it gave, says so and asks it no
// more this run; a show it does not know is looked up again a week later.
func TestTheScheduleIsReadAndKept(t *testing.T) {
	r := newFollowRig(t)
	ctx := context.Background()
	f := newFakeSchedule(t, r.svc)
	r.tmdb.set(func(t *fakeTMDB) {
		t.series[990741] = &tmdbSeries{name: "Example Long Show", status: "Returning Series", tvdb: 9907741, firstAir: day(-9), countries: []string{"US"},
			seasons: map[int][]tmdbEpisode{1: {{title: "One", air: day(-9)}, {title: "Two", air: day(-2)}}}}
		t.series[990742] = &tmdbSeries{name: "Example Unknown Show", status: "Returning Series", tvdb: 9907742, firstAir: day(-9),
			seasons: map[int][]tmdbEpisode{1: {{title: "One", air: day(-9)}}}}
	})
	f.set(func(f *fakeSchedule) {
		f.shows["thetvdb=9907741"] = 503
		f.eps[503] = []map[string]any{airs(1, 1, local(-9, 21, 0, "America/New_York")), airs(1, 2, local(-5, 21, 0, "America/New_York"))}
	})
	for _, id := range []int64{990741, 990742} {
		if _, _, err := r.svc.Request(ctx, store.Wanted{TMDBID: id, MediaType: "series", Title: "x"}, asAdmin("admin")); err != nil {
			t.Fatal(err)
		}
	}
	if run := r.settle(t, false); run.Scheduled != 1 || run.ScheduleError != "" {
		t.Errorf("the first read: %+v", run)
	}
	one, two := target(t, r.svc, "series:990741:s01e001"), target(t, r.svc, "series:990741:s01e002")
	if one.AirStamp == nil || two.AirStamp != nil {
		t.Errorf("E01 %v, E02 %v — the schedule dates E02 three days from TMDB", one.AirStamp, two.AirStamp)
	}
	known, _ := r.svc.st.TitleByID(ctx, "series:990741")
	unknown, _ := r.svc.st.TitleByID(ctx, "series:990742")
	if known.ScheduleID != 503 || known.ScheduleAt == nil || unknown.ScheduleID != 0 || unknown.ScheduleAt == nil ||
		!strings.Contains(unknown.ScheduleError, "TVmaze does not know it by its TVDB or IMDb id") {
		t.Errorf("the titles: %+v %+v", known, unknown)
	}
	lookups := func() int {
		n := 0
		for _, c := range f.called() {
			if strings.HasPrefix(c, "/lookup/") {
				n++
			}
		}
		return n
	}
	if n := lookups(); n != 2 {
		t.Errorf("%d lookups, want one per show", n)
	}

	// Six hours on, a series that runs is read again: its episodes, not its
	// id; the one TVmaze did not know waits its week.
	sqlExec(t, `UPDATE titles SET inventory_at = now() - interval '6 hours', schedule_at = now() - interval '6 hours'
	            WHERE id IN ('series:990741', 'series:990742')`)
	calls := len(f.called())
	if run := r.settle(t, true); run.Scheduled != 1 || lookups() != 2 || len(f.called()) != calls+1 {
		t.Errorf("read again: %+v, calls %v", run, f.called()[calls:])
	}
	sqlExec(t, `UPDATE titles SET inventory_at = now() - interval '6 hours', schedule_at = now() - interval '8 days'
	            WHERE id = 'series:990742'`)
	if r.settle(t, true); lookups() != 3 {
		t.Errorf("the unknown show a week on: %d lookups", lookups())
	}

	// The source failing: the air times it gave stay; the run says why.
	f.set(func(f *fakeSchedule) { f.down = true })
	sqlExec(t, `UPDATE titles SET inventory_at = now() - interval '6 hours', schedule_at = now() - interval '6 hours'
	            WHERE id = 'series:990741'`)
	if run := r.settle(t, true); !strings.Contains(run.ScheduleError, "503") {
		t.Errorf("the source down: %+v", run)
	}
	if st := target(t, r.svc, one.ID); st.AirStamp == nil || !st.AirStamp.Equal(*one.AirStamp) {
		t.Errorf("E01's air time with the source down: %v", st.AirStamp)
	}

	// TMDB moves E01 by three days: its air time is no one's any more.
	r.tmdb.set(func(t *fakeTMDB) { t.series[990741].seasons[1][0].air = day(-6) })
	sqlExec(t, `UPDATE titles SET inventory_at = now() - interval '6 hours' WHERE id = 'series:990741'`)
	r.settle(t, true)
	if st := target(t, r.svc, one.ID); st.AirStamp != nil || !st.AirDate.Equal(*day(-6)) {
		t.Errorf("E01 moved by TMDB: %v %v", st.AirStamp, st.AirDate)
	}
}

func ids(ts []store.Target) []string {
	var out []string
	for _, t := range ts {
		out = append(out, t.ID)
	}
	return out
}

// When an automatic grab takes an episode, as the policy says it — and what
// the policy may say.
func TestWhenAnAutomaticGrabTakesAnEpisode(t *testing.T) {
	for _, tc := range []struct {
		pol  SearchSettings
		want string
	}{
		{SearchSettings{AirDelayHours: 30, AirTimes: AirTimesTVmaze, AirDelayAfterAirstampMinutes: 60},
			"an hour after it airs (TVmaze's air times), else 30 hours after its air date"},
		{SearchSettings{AirDelayHours: 30, AirDelayAfterAirstampMinutes: 90}, "90 minutes after it airs (TVmaze's air times), else 30 hours after its air date"},
		{SearchSettings{AirTimes: AirTimesTVmaze}, "from the moment it airs (TVmaze's air times), else from its air date on"},
		{SearchSettings{AirDelayHours: 30, AirTimes: AirTimesOff, AirDelayAfterAirstampMinutes: 120}, "30 hours after its air date"},
	} {
		if got := tc.pol.grabbedWhen(); got != tc.want {
			t.Errorf("%+v: %q, want %q", tc.pol, got, tc.want)
		}
	}
	base := SearchSettings{PreferProtocol: "usenet", MaxConcurrentGrabs: 3, NewWithinDays: 14}
	for _, tc := range []struct {
		patch func(*SearchSettings)
		field string
	}{
		{func(p *SearchSettings) { p.AirTimes = "everywhere" }, "airTimes"},
		{func(p *SearchSettings) { p.AirDelayAfterAirstampMinutes = -1 }, "airDelayAfterAirstampMinutes"},
		{func(p *SearchSettings) { p.AirDelayAfterAirstampMinutes = MaxAirDelayAfterAirstampMinutes + 1 }, "airDelayAfterAirstampMinutes"},
	} {
		p := base
		tc.patch(&p)
		if fe := validateSearchSettings(p); len(fe) != 1 || fe[0].Field != tc.field {
			t.Errorf("%+v: %+v", p, fe)
		}
	}
	for _, ok := range []string{"", AirTimesTVmaze, AirTimesOff} {
		p := base
		p.AirTimes = ok
		if fe := validateSearchSettings(p); len(fe) != 0 {
			t.Errorf("airTimes %q: %+v", ok, fe)
		}
	}
}

// A requested movie TMDB dates in cinemas only is searched from 90 days after
// it came there — a guess — and never grabbed on its own meanwhile: what
// turns up during the cinema run is most often a recording made there. An
// admin grabs it. Once TMDB dates a release to watch at home, it is grabbed
// on its own, and a release published before that day is skipped.
func TestAMovieInCinemasOnlyIsNotGrabbedOnItsOwn(t *testing.T) {
	r := newFollowRig(t)
	ctx := context.Background()
	theatrical := day(-100)
	dates := []map[string]any{{"type": 3, "release_date": theatrical.Format(time.RFC3339)}}
	r.tmdb.set(func(f *fakeTMDB) {
		f.movies[990100] = map[string]any{"id": 990100, "title": "Example Clay Film", "status": "Released", "release_date": dateOf(theatrical),
			"imdb_id": "tt99009100", "release_dates": map[string]any{"results": []map[string]any{{"iso_3166_1": "US", "release_dates": dates}}}}
	})
	w, _, err := r.svc.Request(ctx, store.Wanted{TMDBID: 990100, MediaType: "movie", Title: "Example Clay Film", Year: 2026}, asAdmin("admin"))
	if err != nil {
		t.Fatal(err)
	}
	r.settle(t, false)
	during := "Example.Clay.Film.2026.1080p.WEB-DL.x264-FAKE"
	feedSrc := newFeedSource(t, true, film(during, *day(-50)))
	addFeed(t, r.svc, "feed", feedSrc)
	src := newFakeSource(t, "usenet", "K", during)
	src.pubs = map[string]time.Time{during: *day(-50)}
	addSource(t, r.svc, "example", src, nil)
	if _, err := r.svc.SaveSearchSettings(ctx, SearchSettings{PreferProtocol: "usenet", MaxConcurrentGrabs: 10, AutoGrab: true,
		AirDelayHours: DefaultAirDelayHours}, 0, "test"); err != nil {
		t.Fatal(err)
	}
	if run, _ := r.svc.SyncRSS(ctx); run.Feeds != 0 || run.Grabbed != 0 {
		t.Errorf("the feeds, for a movie in cinemas only: %+v", run)
	}
	if err := r.svc.SweepBacklog(ctx, 10); err != nil {
		t.Fatal(err)
	}
	tg := target(t, r.svc, "movie:990100:movie")
	if len(r.gw.added()) != 0 || tg.LastSearchAt == nil || tg.State != store.TargetWanted {
		t.Errorf("the sweep searches it and grabs nothing: %+v (adds %d)", tg, len(r.gw.added()))
	}
	if v := r.view(t, w.ID); v.AutoGrab == nil || *v.AutoGrab || !strings.Contains(v.Detail, "until TMDB dates one, only an admin grabs it") {
		t.Errorf("the request: %s %q %v", v.Status, v.Detail, v.AutoGrab)
	}
	// An admin grabs it.
	if err := r.svc.AutoGrab(ctx, w.ID); err != nil {
		t.Errorf("find & grab: %v", err)
	}
	if len(r.gw.added()) != 1 {
		t.Errorf("handed over %d", len(r.gw.added()))
	}

	// A second movie: TMDB dates a digital release five days ago. The
	// recording from the cinema run is skipped; the release from after is
	// grabbed on its own.
	r2 := newFollowRig(t)
	r2.tmdb.set(func(f *fakeTMDB) {
		f.movies[990101] = map[string]any{"id": 990101, "title": "Example Other Film", "status": "Released", "release_date": dateOf(theatrical),
			"release_dates": map[string]any{"results": []map[string]any{{"iso_3166_1": "US", "release_dates": append(dates,
				map[string]any{"type": 4, "release_date": day(-5).Format(time.RFC3339)})}}}}
	})
	if _, _, err := r2.svc.Request(ctx, store.Wanted{TMDBID: 990101, MediaType: "movie", Title: "Example Other Film", Year: 2026}, asAdmin("admin")); err != nil {
		t.Fatal(err)
	}
	r2.settle(t, false)
	early, home := "Example.Other.Film.2026.1080p.WEB-DL.x264-FAKE", "Example.Other.Film.2026.1080p.WEB-DL.x264-GRP"
	addFeed(t, r2.svc, "feed", newFeedSource(t, true, film(early, *day(-50)), film(home, *day(-4))))
	if _, err := r2.svc.SaveSearchSettings(ctx, SearchSettings{PreferProtocol: "usenet", MaxConcurrentGrabs: 10, AutoGrab: true,
		AirDelayHours: DefaultAirDelayHours}, 0, "test"); err != nil {
		t.Fatal(err)
	}
	if run, _ := r2.svc.SyncRSS(ctx); run.Grabbed != 1 || r2.grabbed(t)[""] != home {
		t.Errorf("out at home: %+v %v", run, r2.grabbed(t))
	}
}
