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
// looked up by ("thetvdb=…", "imdb=…"), each show's record — its name, its
// network or web channel, its runtime; a broadcast on a network in the United
// States when none is set — and its episodes, each with its local day and
// hour and its airstamp, served in one call as TVmaze serves them
// (/shows/{id}?embed=episodes); down answers 503. The ids are invented.
type fakeSchedule struct {
	mu      sync.Mutex
	shows   map[string]int64
	records map[int64]map[string]any
	eps     map[int64][]map[string]any
	down    bool
	calls   []string
}

// newFakeSchedule gives a service the schedule source of the tests' own.
func newFakeSchedule(t *testing.T, svc *Service) *fakeSchedule {
	t.Helper()
	f := &fakeSchedule{shows: map[string]int64{}, records: map[int64]map[string]any{}, eps: map[int64][]map[string]any{}}
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
		case strings.HasPrefix(r.URL.Path, "/shows/") && r.URL.Query().Get("embed") == "episodes":
			id, _ := strconv.ParseInt(strings.TrimPrefix(r.URL.Path, "/shows/"), 10, 64)
			eps, ok := f.eps[id]
			if !ok {
				http.NotFound(w, r)
				return
			}
			rec := map[string]any{"id": id, "name": "", "runtime": nil,
				"network": map[string]any{"name": "Example Network", "country": map[string]any{"code": "US", "timezone": "America/New_York"}}}
			for k, v := range f.records[id] {
				rec[k] = v
			}
			rec["_embedded"] = map[string]any{"episodes": eps}
			_ = json.NewEncoder(w).Encode(rec)
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

// streamed is a show's record as TVmaze has one streamed: no network, a web
// channel of country ("" for a service everywhere).
func streamed(name, country string) map[string]any {
	ch := map[string]any{"name": "Example Streams", "country": nil}
	if country != "" {
		ch["country"] = map[string]any{"code": country}
	}
	return map[string]any{"name": name, "network": nil, "webChannel": ch}
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

// airs is an episode as TVmaze lists it, airing at at, in at's zone, an hour
// long.
func airs(season, number int, at time.Time) map[string]any {
	return map[string]any{"id": season*1000 + number, "season": season, "number": number,
		"airdate": at.Format("2006-01-02"), "airtime": at.Format("15:04"), "airstamp": at.UTC().Format(time.RFC3339), "runtime": 60}
}

// nightShow is a series broadcast at 21:00 in New York, an hour long: its
// first episode two days ago, its second tonight. The name and ids are
// invented.
func nightShow(r *followRig, f *fakeSchedule) {
	r.tmdb.set(func(t *fakeTMDB) {
		t.series[990721] = &tmdbSeries{name: "Example Night Show", status: "Returning Series", tvdb: 9907721, firstAir: day(-2),
			countries: []string{"US"}, seasons: map[int][]tmdbEpisode{1: {{title: "One", air: day(-2)}, {title: "Two", air: day(0)}}}}
	})
	f.set(func(f *fakeSchedule) {
		f.shows["thetvdb=9907721"] = 501
		f.records[501] = map[string]any{"name": "Example Night Show", "runtime": 60}
		f.eps[501] = []map[string]any{airs(1, 1, local(-2, 21, 0, "America/New_York")), airs(1, 2, local(0, 21, 0, "America/New_York"))}
	})
}

// requestNightShow requests the night show as an admin and reads it.
func requestNightShow(t *testing.T, r *followRig) InventoryRun {
	t.Helper()
	if _, _, err := r.svc.Request(context.Background(), store.Wanted{TMDBID: 990721, MediaType: "series", Title: "Example Night Show"}, asAdmin("admin")); err != nil {
		t.Fatal(err)
	}
	return r.settle(t, false)
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
// a fake. With TVmaze's air times: of a broadcast, anything published before
// it ended — its start and runtime, an hour when TVmaze gives none —; of a
// streamed episode, anything published more than half an hour before it
// streams, or — dated to the day alone — before a streaming service of the
// United States puts one out, 03:00 in New York. Without them, midnight of its
// air date where the series comes from — UTC when TMDB names no country
// acquire knows. Of a requested movie, midnight of the day it comes out to
// watch at home, where it does; one dated in cinemas only is told by nothing.
func TestWhenAReleaseIsPublishedTooEarly(t *testing.T) {
	stamp := local(-2, 21, 0, "America/New_York")
	season, ep := 1, 1
	episode := store.Target{Kind: "episode", SeasonNumber: &season, EpisodeNumber: &ep, AirDate: day(-2), AirStamp: ptr(stamp.UTC()), AirRuntime: 60}
	half := episode
	half.AirRuntime = 30
	unknownLength := episode
	unknownLength.AirRuntime = 0
	dayOnly := episode
	dayOnly.AirStamp = nil
	us := store.Title{Kind: "series", Countries: []string{"US"}, ScheduleKind: store.ScheduleBroadcast, ScheduleCountry: "US"}
	stream := store.Title{Kind: "series", Countries: []string{"US"}, ScheduleKind: store.ScheduleStreaming}
	german := store.Title{Kind: "series", Countries: []string{"DE"}, ScheduleKind: store.ScheduleStreaming, ScheduleCountry: "DE"}
	at := func(c time.Time) Candidate { return Candidate{Title: "Show.S01E01.1080p.WEB-GRP", PubDate: &c} }
	utc := func(c time.Time) string { return c.UTC().Format("2006-01-02 15:04 UTC") }
	none := func() map[[2]int]store.Target { return nil }
	for _, tc := range []struct {
		name   string
		title  store.Title
		target store.Target
		pub    time.Time
		stamps bool
		want   string
	}{
		{"a broadcast: 15 hours before it", us, episode, stamp.Add(-15 * time.Hour), true,
			"published before the episode aired (published " + utc(stamp.Add(-15*time.Hour)) +
				"; S01E01 aired " + utc(stamp) + ", a broadcast until " + stamp.Add(time.Hour).UTC().Format("15:04 UTC") + "): most likely a fake"},
		{"a broadcast: 10 minutes before it", us, episode, stamp.Add(-10 * time.Minute), true, "published before the episode aired"},
		{"a broadcast: 20 minutes into it", us, episode, stamp.Add(20 * time.Minute), true, "published before the episode aired"},
		{"a broadcast: a minute after it ended", us, episode, stamp.Add(61 * time.Minute), true, ""},
		{"a half-hour broadcast, ended", us, half, stamp.Add(31 * time.Minute), true, ""},
		{"a broadcast of a runtime TVmaze does not give: an hour", us, unknownLength, stamp.Add(50 * time.Minute), true, "published before the episode aired"},
		{"streamed: 20 minutes before it streams", stream, episode, stamp.Add(-20 * time.Minute), true, ""},
		{"streamed: 40 minutes before it", stream, episode, stamp.Add(-40 * time.Minute), true,
			"published before the episode aired (published " + utc(stamp.Add(-40*time.Minute)) + "; S01E01 streams from " + utc(stamp) + "): most likely a fake"},
		{"streamed from the US, dated to the day: before 21:00 in New York the evening before", stream, dayOnly, local(-3, 20, 30, "America/New_York"), true,
			"published before the episode aired (published " + utc(local(-3, 20, 30, "America/New_York")) + "; S01E01 streams on " + dateOf(day(-2)) +
				", from " + dateOf(day(-3)) + " 21:00 America/New_York at the earliest): most likely a fake"},
		{"streamed from the US, dated to the day: 21:30 in New York the evening before", stream, dayOnly, local(-3, 21, 30, "America/New_York"), true, ""},
		{"streamed from the US, dated to the day: 02:30 on the day", stream, dayOnly, local(-2, 2, 30, "America/New_York"), true, ""},
		{"streamed from a country without a drop hour: its midnight", german, dayOnly, local(-2, 0, 30, "Europe/Berlin"), true, ""},
		{"streamed from a country without a drop hour: before its midnight", german, dayOnly, local(-3, 23, 30, "Europe/Berlin"), true,
			"published before the episode aired (published " + utc(local(-3, 23, 30, "Europe/Berlin")) + "; S01E01 aired on " + dateOf(day(-2)) + ", Europe/Berlin)"},
		{"a broadcast dated to the day: midnight in New York", us, dayOnly, local(-2, 0, 30, "America/New_York"), true, ""},
		{"not read at TMDB yet: midnight where its network is", store.Title{Kind: "series", ScheduleKind: store.ScheduleBroadcast, ScheduleCountry: "US"},
			dayOnly, local(-2, 0, 0, "America/New_York").Add(-time.Minute), true,
			"published before the episode aired (published " + utc(local(-2, 0, 0, "America/New_York").Add(-time.Minute)) + "; S01E01 aired on " +
				dateOf(day(-2)) + ", America/New_York)"},
		{"not read at TMDB yet, air times off: UTC", store.Title{Kind: "series", ScheduleKind: store.ScheduleBroadcast, ScheduleCountry: "US"},
			dayOnly, local(-2, 0, 0, "America/New_York").Add(-time.Minute), false, ""},
		{"air times off: the day, in New York", us, episode, stamp.Add(-15 * time.Hour), false, ""},
		{"air times off: before the day began in New York", us, episode, local(-2, 0, 0, "America/New_York").Add(-time.Minute), false,
			"published before the episode aired (published " + utc(local(-2, 0, 0, "America/New_York").Add(-time.Minute)) +
				"; S01E01 aired on " + dateOf(day(-2)) + ", America/New_York): most likely a fake"},
		{"air times off: a streamed episode by its day alone", stream, dayOnly, local(-2, 2, 30, "America/New_York"), false, ""},
		{"no country: UTC", store.Title{Kind: "series"}, store.Target{Kind: "episode", SeasonNumber: &season, EpisodeNumber: &ep,
			AirDate: day(-2)}, day(-2).Add(-time.Minute), true, "published before the episode aired"},
		{"a movie out digitally, in the US", store.Title{Kind: "movie"}, store.Target{Kind: "movie", AirDate: day(-2), ReleaseKind: "digital",
			ReleaseCountry: "US"}, local(-2, 0, 0, "America/New_York").Add(-time.Minute), true,
			"published before the movie came out (published " + utc(local(-2, 0, 0, "America/New_York").Add(-time.Minute)) +
				"; it came out digitally on " + dateOf(day(-2)) + ", America/New_York): most likely a fake"},
		{"a movie in cinemas only", store.Title{Kind: "movie"}, store.Target{Kind: "movie", AirDate: day(-100), ReleaseKind: "theatrical"},
			*day(-200), true, ""},
	} {
		got := preAired(at(tc.pub), tc.title, tc.target, none, tc.stamps)
		if (tc.want == "") != (got == "") || !strings.HasPrefix(got, tc.want) {
			t.Errorf("%s:\n  got  %q\n  want %q", tc.name, got, tc.want)
		}
	}

	// When an automatic grab may take them: an hour after the broadcast
	// ended, an hour after it streams; dated to the day, the air delay after
	// it; nothing of a series whose air times were never read.
	pol := SearchSettings{AirTimes: AirTimesTVmaze, AirDelayHours: 30, AirDelayAfterAirstampMinutes: 60}
	read := time.Now()
	us.ScheduleAt, stream.ScheduleAt = &read, &read
	us.ScheduleVersion, stream.ScheduleVersion = store.ScheduleVersion, store.ScheduleVersion
	episode.WindowAt, half.WindowAt, dayOnly.WindowAt = day(-2), day(-2), day(-2)
	for _, tc := range []struct {
		name   string
		title  store.Title
		target store.Target
		want   time.Time
	}{
		{"a broadcast", us, episode, stamp.Add(2 * time.Hour)},
		{"a half-hour broadcast", us, half, stamp.Add(90 * time.Minute)},
		{"streamed", stream, episode, stamp.Add(time.Hour)},
		{"dated to the day", stream, dayOnly, day(-2).Add(30 * time.Hour)},
	} {
		if got := openAt(tc.title, tc.target, pol); !got.Equal(tc.want) {
			t.Errorf("%s opens at %s, want %s", tc.name, got.UTC(), tc.want.UTC())
		}
	}
	never, earlier := us, us
	never.ScheduleAt = nil
	earlier.ScheduleVersion = store.ScheduleVersion - 1
	if autoOpen(never, episode, time.Now(), pol) || autoOpen(earlier, episode, time.Now(), pol) || !autoOpen(us, episode, time.Now(), pol) {
		t.Error("an episode of a series whose air times were never read, or read by earlier rules, is open")
	}
	held := episode
	held.ScheduleHold = "held"
	if autoOpen(us, held, time.Now(), pol) {
		t.Error("an episode held for TVmaze is open")
	}
	pol.AirTimes = AirTimesOff
	if !autoOpen(never, episode, time.Now(), pol) || !autoOpen(us, held, time.Now(), pol) {
		t.Error("with air times off, an episode waits for them")
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

// With the moment a broadcast airs from the schedule source, a release posted
// on its air day but hours before the evening's broadcast is a fake the feeds
// skip, and so is one posted while it is on; the episode is grabbed an hour
// after the broadcast ended, and one that airs tonight not before. With air
// times off, the day alone tells — and the evening's fake is no longer told
// from the real.
func TestExactAirTimesInTheFeeds(t *testing.T) {
	r := newFollowRig(t)
	ctx := context.Background()
	f := newFakeSchedule(t, r.svc)
	nightShow(r, f)
	run := requestNightShow(t, r)
	one, two := target(t, r.svc, "series:990721:s01e001"), target(t, r.svc, "series:990721:s01e002")
	first, tonight := local(-2, 21, 0, "America/New_York").UTC(), local(0, 21, 0, "America/New_York").UTC()
	if run.Scheduled != 1 || one.AirStamp == nil || !one.AirStamp.Equal(first) || two.AirStamp == nil || !two.AirStamp.Equal(tonight) ||
		one.AirRuntime != 60 {
		t.Fatalf("the air times: %+v; %v %v %d", run, one.AirStamp, two.AirStamp, one.AirRuntime)
	}
	if ti, _ := r.svc.st.TitleByID(ctx, "series:990721"); ti.ScheduleKind != store.ScheduleBroadcast || ti.ScheduleCountry != "US" {
		t.Errorf("how it airs: %q %q", ti.ScheduleKind, ti.ScheduleCountry)
	}
	fake, during := "Example.Night.Show.S01E01.2160p.WEB.h265-FAKE", "Example.Night.Show.S01E01.1080p.WEB.h264-DURING"
	feedSrc := newFeedSource(t, true,
		tv(fake, first.Add(-15*time.Hour)),
		tv(during, first.Add(20*time.Minute)),
		tv("Example.Night.Show.S01E02.1080p.WEB.h264-EARLY", ago(time.Minute)))
	addFeed(t, r.svc, "feed", feedSrc)
	autoGrabNew(t, r.svc, "")
	if run, err := r.svc.SyncRSS(ctx); err != nil || run.Grabbed != 0 {
		t.Fatalf("the fake of the air day, one during the broadcast, and an episode that airs tonight: %+v %v", run, err)
	}
	if st := target(t, r.svc, one.ID); st.State != store.TargetWanted || !strings.Contains(st.Detail, "2 release(s) in a source's newest releases published before it aired") {
		t.Errorf("E01: %+v", st)
	}
	if blocked, _ := r.svc.st.BlockedFor(ctx, one.ID); len(blocked) != 0 {
		t.Errorf("blocked: %v", blocked)
	}

	feedSrc.add(tv("Example.Night.Show.S01E01.1080p.WEB.h264-GRP", first.Add(65*time.Minute)))
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
	requestNightShow(t, r2)
	addFeed(t, r2.svc, "feed", newFeedSource(t, true, tv(fake, first.Add(-15*time.Hour))))
	autoGrabNew(t, r2.svc, AirTimesOff)
	if run, _ := r2.svc.SyncRSS(ctx); run.Grabbed != 1 {
		t.Errorf("with air times off: %+v", run)
	}
}

// The sweep searches an episode only once its delay after its broadcast
// ended is over, and skips the fake from before the broadcast.
func TestExactAirTimesInTheSweep(t *testing.T) {
	fake, real := "Example.Night.Show.S01E01.2160p.WEB.h265-FAKE", "Example.Night.Show.S01E01.1080p.WEB.h264-GRP"
	r := newFollowRig(t)
	ctx := context.Background()
	f := newFakeSchedule(t, r.svc)
	nightShow(r, f)
	requestNightShow(t, r)
	first := local(-2, 21, 0, "America/New_York").UTC()
	src := newFakeSource(t, "usenet", "K", fake, real)
	src.pubs = map[string]time.Time{fake: first.Add(-15 * time.Hour), real: first.Add(70 * time.Minute)}
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

// A fake posted ten minutes before an evening's broadcast, or twenty minutes
// into it, is often the only release there is when the sweep opens the
// episode an hour after the broadcast ended: it is skipped, never blocked,
// and the episode says why it is still missing.
func TestAFakeAroundTheBroadcastIsNotTaken(t *testing.T) {
	fake := "Example.Night.Show.S01E01.2160p.WEB.h265-FAKE"
	for _, off := range []time.Duration{-10 * time.Minute, 20 * time.Minute} {
		r := newFollowRig(t)
		ctx := context.Background()
		nightShow(r, newFakeSchedule(t, r.svc))
		requestNightShow(t, r)
		start := local(-2, 21, 0, "America/New_York").UTC()
		src := newFakeSource(t, "usenet", "K", fake)
		src.pubs = map[string]time.Time{fake: start.Add(off)}
		addSource(t, r.svc, "example", src, nil)
		autoGrabNew(t, r.svc, "")
		if err := r.svc.SweepBacklog(ctx, 10); err != nil {
			t.Fatal(err)
		}
		if got := handedOver(t, r.targetRig); len(got) != 0 {
			t.Errorf("published %v from the start of the broadcast: handed over %v", off, got)
		}
		one := target(t, r.svc, "series:990721:s01e001")
		if one.State != store.TargetWanted || one.LastSearchAt == nil || !strings.Contains(one.SearchDetail, "every one published before it aired") {
			t.Errorf("published %v from the start: %s %q", off, one.State, one.SearchDetail)
		}
		if blocked, _ := r.svc.st.BlockedFor(ctx, one.ID); len(blocked) != 0 {
			t.Errorf("published %v from the start: blocked %v", off, blocked)
		}
	}
}

// A streamed series dated to the minute: a release posted twenty minutes
// before it streams is the episode, taken an hour after; one posted an hour
// before is a fake. Dated to the day alone, a series streamed from the United
// States streams from 21:00 in New York the evening before, at the earliest:
// a release from before is skipped, one from that evening on taken, and the
// episode waits the air delay after its air date.
func TestAStreamedSeries(t *testing.T) {
	r := newFollowRig(t)
	ctx := context.Background()
	f := newFakeSchedule(t, r.svc)
	drop := local(-2, 3, 0, "America/New_York")
	r.tmdb.set(func(t *fakeTMDB) {
		t.series[990751] = &tmdbSeries{name: "Example Stream", status: "Returning Series", tvdb: 9907751, firstAir: day(-2),
			countries: []string{"US"}, seasons: map[int][]tmdbEpisode{1: {{title: "One", air: day(-2)}, {title: "Two", air: day(-2)}}}}
	})
	dayOnly := airs(1, 2, drop)
	dayOnly["airtime"] = ""
	f.set(func(f *fakeSchedule) {
		f.shows["thetvdb=9907751"] = 551
		f.records[551] = streamed("Example Stream", "US")
		f.eps[551] = []map[string]any{airs(1, 1, drop), dayOnly}
	})
	if _, _, err := r.svc.Request(ctx, store.Wanted{TMDBID: 990751, MediaType: "series", Title: "Example Stream"}, asAdmin("admin")); err != nil {
		t.Fatal(err)
	}
	r.settle(t, false)
	one, two := target(t, r.svc, "series:990751:s01e001"), target(t, r.svc, "series:990751:s01e002")
	if one.AirStamp == nil || two.AirStamp != nil {
		t.Fatalf("the air times: %v %v", one.AirStamp, two.AirStamp)
	}
	feedSrc := newFeedSource(t, true,
		tv("Example.Stream.S01E01.1080p.WEB.h264-FAKE", drop.Add(-time.Hour)),
		tv("Example.Stream.S01E01.1080p.WEB.h264-GRP", drop.Add(-20*time.Minute)),
		tv("Example.Stream.S01E02.1080p.WEB.h264-FAKE", local(-3, 20, 30, "America/New_York")),
		tv("Example.Stream.S01E02.720p.WEB.h264-GRP", local(-3, 21, 20, "America/New_York")))
	addFeed(t, r.svc, "feed", feedSrc)
	autoGrabNew(t, r.svc, "")
	if _, err := r.svc.SyncRSS(ctx); err != nil {
		t.Fatal(err)
	}
	if got := r.grabbed(t); got["S01E01"] != "Example.Stream.S01E01.1080p.WEB.h264-GRP" || got["S01E02"] != "Example.Stream.S01E02.720p.WEB.h264-GRP" {
		t.Errorf("grabbed %v", got)
	}
	if blocked, _ := r.svc.st.BlockedFor(ctx, two.ID); len(blocked) != 0 {
		t.Errorf("blocked: %v", blocked)
	}
}

// TVmaze down when a series is first read: it has no air times, and an
// automatic grab takes nothing of it — not the fake of the day of its
// broadcast, which the day alone would not tell — until they are read; the
// request says it waits for them. Read, the fake is skipped and the real
// episode grabbed.
func TestASeriesWaitsForItsAirTimes(t *testing.T) {
	r := newFollowRig(t)
	ctx := context.Background()
	f := newFakeSchedule(t, r.svc)
	nightShow(r, f)
	f.set(func(f *fakeSchedule) { f.down = true })
	run := requestNightShow(t, r)
	if !strings.Contains(run.ScheduleError, "503") {
		t.Errorf("the first read: %+v", run)
	}
	start := local(-2, 21, 0, "America/New_York").UTC()
	fake, real := "Example.Night.Show.S01E01.2160p.WEB.h265-FAKE", "Example.Night.Show.S01E01.1080p.WEB.h264-GRP"
	feed := addFeed(t, r.svc, "feed", newFeedSource(t, true, tv(fake, start.Add(-15*time.Hour))))
	autoGrabNew(t, r.svc, "")
	if due, _ := r.svc.st.DueTargetsBy(ctx, 10, r.svc.grabPolicy(ctx).dueOrder()); len(due) != 0 {
		t.Errorf("due without air times: %v", ids(due))
	}
	if run, _ := r.svc.SyncRSS(ctx); run.Grabbed != 0 || run.Feeds != 0 {
		t.Errorf("the feeds without air times: %+v", run)
	}
	if err := r.svc.DeleteSource(ctx, feed.ID, 0, "admin"); err != nil {
		t.Fatal(err)
	}
	src := newFakeSource(t, "usenet", "K", fake, real)
	src.pubs = map[string]time.Time{fake: start.Add(-15 * time.Hour), real: start.Add(70 * time.Minute)}
	addSource(t, r.svc, "example", src, nil)
	if err := r.svc.SweepBacklog(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if got := handedOver(t, r.targetRig); len(got) != 0 || len(r.gw.added()) != 0 {
		t.Errorf("handed over without air times: %v", got)
	}
	if st := target(t, r.svc, "series:990721:s01e001"); st.LastSearchAt != nil || st.AirStamp != nil {
		t.Errorf("E01 without air times: %+v", st)
	}
	if w, err := r.svc.st.FindRequestFor(ctx, 990721, []string{"series"}); err != nil ||
		!strings.Contains(r.view(t, w.ID).Detail, "nothing of it is grabbed on its own until TVmaze's air times of it are read (TVmaze did not answer: TVmaze /lookup/shows: HTTP 503)") {
		t.Errorf("the request without air times: %q %v", r.view(t, w.ID).Detail, err)
	}

	// TVmaze answers again: the next read gives the air times, the fake is
	// skipped, the real E01 grabbed.
	f.set(func(f *fakeSchedule) { f.down = false })
	sqlExec(t, `UPDATE titles SET inventory_at = now() - interval '6 hours' WHERE id = 'series:990721'`)
	if run := r.settle(t, true); run.Scheduled != 1 {
		t.Fatalf("read again: %+v", run)
	}
	if err := r.svc.SweepBacklog(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if got := handedOver(t, r.targetRig); len(got) != 1 || got[0] != real {
		t.Errorf("handed over %v, want the real E01", got)
	}
}

// An episode TVmaze lists without an hour keeps no air time: of a broadcast,
// its day tells.
func TestAnEpisodeWithoutAnHour(t *testing.T) {
	r := newFollowRig(t)
	f := newFakeSchedule(t, r.svc)
	nightShow(r, f)
	f.set(func(f *fakeSchedule) {
		for _, e := range f.eps[501] {
			e["airtime"] = ""
		}
	})
	if run := requestNightShow(t, r); run.Scheduled != 1 {
		t.Errorf("the read: %+v", run)
	}
	if one := target(t, r.svc, "series:990721:s01e001"); one.AirStamp != nil || one.AirRuntime != 0 {
		t.Errorf("an air time kept without an hour: %v %d", one.AirStamp, one.AirRuntime)
	}
}

// Another show behind the series' TVDB id — a wrong id —, airing the same
// day at 06:00: its name is neither the series' nor one of its aliases, so
// none of its air times is taken — the series is told by its air dates —,
// the title says why, and it is looked up again in a week. Air times taken
// from it before go too.
func TestAnotherShowBehindTheSameID(t *testing.T) {
	r := newFollowRig(t)
	ctx := context.Background()
	f := newFakeSchedule(t, r.svc)
	nightShow(r, f)
	requestNightShow(t, r)
	if one := target(t, r.svc, "series:990721:s01e001"); one.AirStamp == nil {
		t.Fatalf("the right show's air time: %+v", one)
	}
	f.set(func(f *fakeSchedule) {
		f.records[501] = map[string]any{"name": "Example Morning Show"}
		f.eps[501] = []map[string]any{airs(1, 1, local(-2, 6, 0, "America/New_York")), airs(1, 2, local(0, 6, 0, "America/New_York"))}
	})
	sqlExec(t, `UPDATE titles SET inventory_at = now() - interval '6 hours', schedule_at = now() - interval '6 hours' WHERE id = 'series:990721'`)
	if run := r.settle(t, true); run.Scheduled != 0 || run.ScheduleError != "" {
		t.Errorf("the read: %+v", run)
	}
	for _, id := range []string{"series:990721:s01e001", "series:990721:s01e002"} {
		if st := target(t, r.svc, id); st.AirStamp != nil || st.AirRuntime != 0 {
			t.Errorf("%s keeps another show's air time: %v", id, st.AirStamp)
		}
	}
	ti, _ := r.svc.st.TitleByID(ctx, "series:990721")
	if ti.ScheduleID != 0 || ti.ScheduleAt == nil || ti.ScheduleKind != "" ||
		!strings.Contains(ti.ScheduleError, `TVmaze's show 501 by its TVDB or IMDb id is "Example Morning Show", not this series`) {
		t.Errorf("the title: %+v", ti)
	}
	pol := r.svc.grabPolicy(ctx)
	if at := openAt(ti, target(t, r.svc, "series:990721:s01e001"), pol); !at.Equal(day(-2).Add(pol.airDelay())) {
		t.Errorf("E01 opens at %s, want by its air date", at.UTC())
	}
}

// A show's name at TVmaze is the series' when it is its title or one of its
// aliases, as release names compare, without a qualifier in brackets or a
// leading "the" — or holds it, or is held by it, two words or more.
func TestTheSameShow(t *testing.T) {
	for _, tc := range []struct {
		name, title string
		aliases     []string
		same        bool
	}{
		{"Example Night Show", "Example Night Show", nil, true},
		{"Example Night Show (US)", "The Example Night Show", nil, true},
		{"Exámple: Night Show", "Example Night Show", nil, true},
		{"La Serie del Ejemplo", "Example Series", []string{"La serie del ejemplo"}, true},
		{"Example Agency's Night Show", "Night Show", nil, true},
		{"Example Morning Show", "Example Night Show", []string{"Night Show Example"}, false},
		{"Show", "Example Night Show", nil, false},
		{"", "Example Night Show", nil, true},
	} {
		if got := sameShow(tc.name, tc.title, tc.aliases); got != tc.same {
			t.Errorf("%q for %q %v: %v", tc.name, tc.title, tc.aliases, got)
		}
	}
}

// The air times of every series that runs are read on the first scheduled
// runs, at TVmaze's pace and outside TMDB's budget: a library whose TMDB
// reads take days has its air times in the first run, the ones with an
// episode near its air date first. How far they are, the settings say.
func TestTheFirstRunsReadEverySeriesAirTimes(t *testing.T) {
	r := newFollowRig(t)
	ctx := context.Background()
	f := newFakeSchedule(t, r.svc)
	r.svc.inventoryBudget = 2
	for i := int64(1); i <= 5; i++ {
		title, _ := track(t, r.svc, 990760+i, fmt.Sprintf("Example Show %d", i), 1, true)
		sqlExec(t, `UPDATE titles SET tvdb_id = $2 WHERE id = $1`, title.ID, 9907760+i)
		f.set(func(f *fakeSchedule) {
			f.shows[fmt.Sprintf("thetvdb=%d", 9907760+i)] = 560 + i
			f.eps[560+i] = []map[string]any{airs(1, 1, local(-2, 21, 0, "America/New_York"))}
		})
	}
	// One more that TVmaze does not know, and one it cannot be asked about.
	unknown, _ := track(t, r.svc, 990770, "Example Unknown Show", 1, true)
	sqlExec(t, `UPDATE titles SET tvdb_id = 9907770 WHERE id = $1`, unknown.ID)
	track(t, r.svc, 990771, "Example Show Without Ids", 1, true)
	if st, err := r.svc.AirTimesStatus(ctx); err != nil || !st.On || st.Running != 7 || st.Pending != 7 || st.Read != 0 {
		t.Fatalf("before the first run: %+v %v", st, err)
	}
	run := r.settle(t, true)
	if run.Calls > 2 || run.Scheduled != 5 || run.ScheduleCalls != 11 || run.ScheduleError != "" {
		t.Errorf("the first run: %+v", run)
	}
	for i := int64(1); i <= 5; i++ {
		if st := target(t, r.svc, fmt.Sprintf("series:%d:s01e001", 990760+i)); st.AirStamp == nil {
			t.Errorf("Example Show %d has no air time", i)
		}
	}
	st, err := r.svc.AirTimesStatus(ctx)
	if err != nil || st.Running != 7 || st.Read != 5 || st.Unknown != 2 || st.Pending != 0 {
		t.Errorf("after it: %+v %v", st, err)
	}
	// Read, they are not asked again until they are due.
	calls := len(f.called())
	if run := r.settle(t, true); run.ScheduleCalls != 0 || len(f.called()) != calls {
		t.Errorf("the next run: %+v", run)
	}
}

// Switching grabbing on its own on while series that run have no air times
// yet is refused, saying how many, until an admin confirms it; switching it
// on with air times off, or once they are read, is not.
func TestGrabbingOnItsOwnWaitsForAirTimes(t *testing.T) {
	r := newFollowRig(t)
	ctx := context.Background()
	f := newFakeSchedule(t, r.svc)
	title, _ := track(t, r.svc, 990781, "Example Show", 1, true)
	sqlExec(t, `UPDATE titles SET tvdb_id = 9907781 WHERE id = $1`, title.ID)
	f.set(func(f *fakeSchedule) {
		f.shows["thetvdb=9907781"] = 581
		f.eps[581] = []map[string]any{airs(1, 1, local(-2, 21, 0, "America/New_York"))}
	})
	on := SearchSettings{PreferProtocol: "usenet", MaxConcurrentGrabs: 3, AutoGrabNew: true, AirDelayHours: 30, AirDelayAfterAirstampMinutes: 60}
	_, err := r.svc.PutSearchSettings(ctx, on, 0, "admin", false)
	var missing *AirTimesMissing
	if !errors.As(err, &missing) || missing.Status.Pending != 1 ||
		err.Error() != "1 of the 1 series that run has no air times from TVmaze yet: nothing of it is grabbed on its own until they are read, "+
			"on the next refreshes — confirm to switch grabbing on its own on now" {
		t.Fatalf("switched on without air times: %v", err)
	}
	if p := r.svc.grabPolicy(ctx); p.AutoGrabNew {
		t.Errorf("refused, yet on: %+v", p)
	}
	off := on
	off.AirTimes = AirTimesOff
	if _, err := r.svc.PutSearchSettings(ctx, off, 0, "admin", false); err != nil {
		t.Errorf("switched on with air times off: %v", err)
	}
	if _, err := r.svc.SaveSearchSettings(ctx, SearchSettings{PreferProtocol: "usenet", MaxConcurrentGrabs: 3}, 0, "test"); err != nil {
		t.Fatal(err)
	}
	if doc, err := r.svc.PutSearchSettings(ctx, on, 0, "admin", true); err != nil || !doc.AutoGrabNew {
		t.Errorf("confirmed: %+v %v", doc, err)
	}
	// On already: another change goes through.
	on.MaxConcurrentGrabs = 4
	if _, err := r.svc.PutSearchSettings(ctx, on, 0, "admin", false); err != nil {
		t.Errorf("a change with it on: %v", err)
	}
	// Read, everything missing switches on without asking.
	r.settle(t, true)
	on.AutoGrab = true
	if _, err := r.svc.PutSearchSettings(ctx, on, 0, "admin", false); err != nil {
		t.Errorf("everything missing, air times read: %v", err)
	}
}

// A series whose air times were never read because TVmaze did not answer is
// tried again every 15 minutes, not on the next refresh hours away.
func TestAirTimesNeverReadAreTriedAgain(t *testing.T) {
	r := newFollowRig(t)
	f := newFakeSchedule(t, r.svc)
	nightShow(r, f)
	f.set(func(f *fakeSchedule) { f.down = true })
	requestNightShow(t, r)
	r.svc.retrySchedules(context.Background())
	if ti, _ := r.svc.st.TitleByID(context.Background(), "series:990721"); ti.ScheduleAt != nil {
		t.Errorf("read with TVmaze down: %+v", ti)
	}
	f.set(func(f *fakeSchedule) { f.down = false })
	r.svc.retrySchedules(context.Background())
	if one := target(t, r.svc, "series:990721:s01e001"); one.AirStamp == nil {
		t.Errorf("not read again: %+v", one)
	}
}

// TVmaze too slow to answer: the run goes on without it, and says so.
func TestTVmazeTooSlow(t *testing.T) {
	r := newFollowRig(t)
	nightShow(r, newFakeSchedule(t, r.svc))
	block := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		select {
		case <-block:
		case <-time.After(2 * time.Second):
		}
	}))
	defer slow.Close()
	defer close(block)
	c := tvmaze.New(slow.URL, "acquire-test")
	c.Every = time.Millisecond
	c.HTTP.Timeout = 200 * time.Millisecond
	r.svc.sched = c
	if run := requestNightShow(t, r); run.Titles != 1 || run.Scheduled != 0 || run.ScheduleError == "" {
		t.Errorf("with TVmaze too slow: %+v", run)
	}
}

// A series' show id is looked up once, its air times read again as it is,
// and a schedule that dates an episode more than a day before TMDB gives it
// none of its own: of the two, the later day tells — the broadcast's usual
// hour on TMDB's day. TMDB moving an episode drops its air time and holds it
// until TVmaze is read again; the source failing keeps what it gave, says so
// and asks it no more this run; a show it does not know is looked up again a
// week later.
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
	if one.AirStamp == nil || one.AirStampEstimated || two.AirStamp == nil || !two.AirStampEstimated ||
		!two.AirStamp.Equal(local(-2, 21, 0, "America/New_York")) || two.ScheduleDay != nil || two.ScheduleHold != "" {
		t.Errorf("E01 %v, E02 %+v — the schedule dates E02 three days before TMDB: TMDB's day, at the usual hour", one.AirStamp, two)
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

	// TMDB moves E01 by three days: its air time is no one's any more, and
	// with TVmaze down it is held until TVmaze is read again.
	r.tmdb.set(func(t *fakeTMDB) { t.series[990741].seasons[1][0].air = day(-6) })
	sqlExec(t, `UPDATE titles SET inventory_at = now() - interval '6 hours' WHERE id = 'series:990741'`)
	r.settle(t, true)
	if st := target(t, r.svc, one.ID); st.AirStamp != nil || !st.AirDate.Equal(*day(-6)) || st.ScheduleHold != store.ScheduleMovedHold ||
		st.Detail != store.ScheduleMovedHold {
		t.Errorf("E01 moved by TMDB: %v %v %q %q", st.AirStamp, st.AirDate, st.ScheduleHold, st.Detail)
	}
	// TVmaze answers again: TVmaze's day is the earlier, so TMDB's tells, at
	// the usual hour; the hold goes.
	f.set(func(f *fakeSchedule) { f.down = false })
	r.svc.retrySchedules(ctx)
	sqlExec(t, `UPDATE titles SET schedule_at = now() - interval '6 hours' WHERE id = 'series:990741'`)
	r.settle(t, true)
	if st := target(t, r.svc, one.ID); st.ScheduleHold != "" || st.Detail != "" || st.AirStamp == nil || !st.AirStampEstimated ||
		!st.AirStamp.Equal(local(-6, 21, 0, "America/New_York")) {
		t.Errorf("E01 read again: %+v", st)
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
			"an hour after its broadcast ends or it streams (TVmaze's air times), else 30 hours after its air date"},
		{SearchSettings{AirDelayHours: 30, AirDelayAfterAirstampMinutes: 90},
			"90 minutes after its broadcast ends or it streams (TVmaze's air times), else 30 hours after its air date"},
		{SearchSettings{AirDelayHours: 30, AirDelayAfterAirstampMinutes: 120},
			"2 hours after its broadcast ends or it streams (TVmaze's air times), else 30 hours after its air date"},
		{SearchSettings{AirTimes: AirTimesTVmaze}, "once its broadcast ends or it streams (TVmaze's air times), else from its air date on"},
		{SearchSettings{AirDelayHours: 30, AirTimes: AirTimesOff, AirDelayAfterAirstampMinutes: 120}, "30 hours after its air date"},
		{SearchSettings{AirDelayHours: 30, AirTimes: AirTimesTVmaze, noSchedule: true}, "30 hours after its air date"},
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
