package app

import (
	"bytes"
	"context"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/laedeli/acquire/internal/store"
	"github.com/laedeli/acquire/internal/tvmaze"
)

// nightShowRead is the night show, the schedule shaped first, requested and
// read; and the start of its first episode as TMDB dates it.
func nightShowRead(t *testing.T, shape func(f *fakeSchedule)) (*followRig, *fakeSchedule, time.Time) {
	t.Helper()
	r := newFollowRig(t)
	f := newFakeSchedule(t, r.svc)
	nightShow(r, f)
	if shape != nil {
		f.set(shape)
	}
	requestNightShow(t, r)
	return r, f, local(-2, 21, 0, "America/New_York").UTC()
}

// sweepFor is what the sweep hands over of one release published at pub,
// grabbing the new things on their own.
func sweepFor(t *testing.T, r *followRig, name string, pub time.Time) []string {
	t.Helper()
	src := newFakeSource(t, "usenet", "K", name)
	src.pubs = map[string]time.Time{name: pub}
	addSource(t, r.svc, "example", src, nil)
	autoGrabNew(t, r.svc, "")
	if err := r.svc.SweepBacklog(context.Background(), 10); err != nil {
		t.Fatal(err)
	}
	return handedOver(t, r.targetRig)
}

const nightFake = "Example.Night.Show.S01E01.2160p.WEB.h265-FAKE"

// An episode moved back a week: TVmaze dates it five days on, TMDB has not
// caught up and still dates it two days ago. It is told by TVmaze's day and
// hour — a release posted at the old slot is no release of it, and the sweep
// does not even search it before it airs. Moved earlier by TVmaze a week, the
// later of the two tells: TMDB's day, at the broadcast's usual hour.
func TestAnEpisodeMovedIsToldByTheLaterDay(t *testing.T) {
	r, _, _ := nightShowRead(t, func(f *fakeSchedule) { f.eps[501][0] = airs(1, 1, local(5, 21, 0, "America/New_York")) })
	one := target(t, r.svc, "series:990721:s01e001")
	if one.AirStamp == nil || !one.AirStamp.Equal(local(5, 21, 0, "America/New_York")) || one.ScheduleDay == nil || !one.ScheduleDay.Equal(*day(5)) ||
		one.ScheduleHold != "" || !one.AirDate.Equal(*day(-2)) {
		t.Fatalf("E01 moved by TVmaze: %+v", one)
	}
	if got := sweepFor(t, r, nightFake, local(-2, 20, 0, "America/New_York")); len(got) != 0 {
		t.Errorf("a release at the old slot of an episode that airs in five days was taken: %v", got)
	}
	if st := target(t, r.svc, one.ID); st.LastSearchAt != nil {
		t.Errorf("searched before it airs: %+v", st)
	}
	ti, _ := r.svc.st.TitleByID(context.Background(), "series:990721")
	cut, aired, _ := airCut(ti, one, true)
	if !cut.Equal(local(5, 22, 0, "America/New_York")) || !strings.Contains(aired, "(TVmaze's date; TMDB dates it "+dateOf(day(-2))+")") {
		t.Errorf("the cut: %s %q", cut, aired)
	}

	// TVmaze a week earlier than TMDB: TMDB's day, at the usual hour.
	r2, _, start := nightShowRead(t, func(f *fakeSchedule) { f.eps[501][0] = airs(1, 1, local(-9, 21, 0, "America/New_York")) })
	one = target(t, r2.svc, "series:990721:s01e001")
	if one.AirStamp == nil || !one.AirStamp.Equal(start) || !one.AirStampEstimated || one.ScheduleDay != nil || one.ScheduleHold != "" {
		t.Fatalf("E01 a week earlier at TVmaze: %+v", one)
	}
	if got := sweepFor(t, r2, nightFake, local(-9, 22, 30, "America/New_York")); len(got) != 0 {
		t.Errorf("a release at TVmaze's earlier slot was taken: %v", got)
	}
}

// Dated days apart by two that cannot be told the same episode — TVmaze names
// it otherwise —, an episode is held: no automatic grab takes it, its target
// and its request say so. Once the two agree, it is not held any more.
func TestAnEpisodeTheTwoCannotTellApartIsHeld(t *testing.T) {
	ctx := context.Background()
	r, f, start := nightShowRead(t, func(f *fakeSchedule) {
		e := airs(1, 1, local(5, 21, 0, "America/New_York"))
		e["name"] = "A Different Night"
		f.eps[501][0] = e
	})
	one := target(t, r.svc, "series:990721:s01e001")
	want := "TVmaze dates an episode S01E01 on " + dateOf(day(5)) + ", TMDB on " + dateOf(day(-2)) + ", and they may not be the same episode"
	if !strings.HasPrefix(one.ScheduleHold, want) || one.Detail != one.ScheduleHold || one.AirStamp != nil {
		t.Fatalf("E01 held: %+v", one)
	}
	w, _ := r.svc.st.FindRequestFor(ctx, 990721, []string{"series"})
	if v := r.view(t, w.ID); !strings.Contains(v.Detail, "1 episode held, not grabbed on its own until TVmaze and TMDB agree on it or it has an air time") {
		t.Errorf("the request: %q", v.Detail)
	}
	if st, _ := r.svc.AirTimesStatus(ctx); st.Held != 1 {
		t.Errorf("the status: %+v", st)
	}
	if got := sweepFor(t, r, nightFake, start.Add(3*time.Hour)); len(got) != 0 {
		t.Errorf("a held episode was grabbed: %v", got)
	}
	// TVmaze dates it as TMDB does again.
	f.set(func(f *fakeSchedule) { f.eps[501][0] = airs(1, 1, local(-2, 21, 0, "America/New_York")) })
	sqlExec(t, `UPDATE titles SET schedule_at = now() - interval '6 hours' WHERE id = 'series:990721'`)
	r.settle(t, true)
	if one := target(t, r.svc, "series:990721:s01e001"); one.ScheduleHold != "" || one.Detail != "" || one.AirStamp == nil || !one.AirStamp.Equal(start) {
		t.Errorf("E01 once the two agree: %+v", one)
	}
}

// An episode of a broadcast TVmaze does not list is told by the usual hour
// of its season's other broadcasts, on TMDB's day: the fake from the morning
// before the evening's broadcast is skipped, the release from after it ended
// taken. Without a usual hour — no episode of it dated to the hour, no
// schedule of the show — it is held until it has an air time.
func TestAnEpisodeTVmazeDoesNotList(t *testing.T) {
	r, _, start := nightShowRead(t, func(f *fakeSchedule) { f.eps[501] = f.eps[501][1:] })
	one := target(t, r.svc, "series:990721:s01e001")
	if one.AirStamp == nil || !one.AirStamp.Equal(start) || !one.AirStampEstimated || one.ScheduleHold != "" {
		t.Fatalf("E01, not listed: %+v", one)
	}
	if got := sweepFor(t, r, nightFake, start.Add(-15*time.Hour)); len(got) != 0 {
		t.Errorf("the morning's fake was taken: %v", got)
	}
	if st := target(t, r.svc, one.ID); !strings.Contains(st.SearchDetail, "every one published before it aired") {
		t.Errorf("E01 says nothing of the fake: %q", st.SearchDetail)
	}
	real := "Example.Night.Show.S01E01.1080p.WEB.h264-GRP"
	r2, _, _ := nightShowRead(t, func(f *fakeSchedule) { f.eps[501] = f.eps[501][1:] })
	if got := sweepFor(t, r2, real, start.Add(70*time.Minute)); len(got) != 1 || got[0] != real {
		t.Errorf("the real E01 after its estimated broadcast: %v", got)
	}

	// The show's schedule tells when no episode of the season does.
	r3, _, _ := nightShowRead(t, func(f *fakeSchedule) {
		f.records[501]["schedule"] = map[string]any{"time": "20:00", "days": []string{"Friday"}}
		for _, e := range f.eps[501] {
			e["airtime"] = ""
		}
	})
	if one := target(t, r3.svc, "series:990721:s01e001"); one.AirStamp == nil || !one.AirStamp.Equal(local(-2, 20, 0, "America/New_York")) || !one.AirStampEstimated {
		t.Errorf("E01 by the show's schedule: %+v", one)
	}
	// Neither: held.
	r4, _, _ := nightShowRead(t, func(f *fakeSchedule) { f.eps[501] = f.eps[501][1:2]; f.eps[501][0]["airtime"] = "" })
	one = target(t, r4.svc, "series:990721:s01e001")
	if one.AirStamp != nil || !strings.HasPrefix(one.ScheduleHold, "TVmaze does not list it, and the series' broadcasts have no usual hour") {
		t.Errorf("E01 without a usual hour: %+v", one)
	}
	if got := sweepFor(t, r4, nightFake, start.Add(3*time.Hour)); len(got) != 0 {
		t.Errorf("a held episode was grabbed: %v", got)
	}
}

// TVmaze refusing every call — a 403 — is TVmaze not answering: the series
// stays unread, the switch of grabbing on its own waits for an admin's word,
// and the morning's fake is not taken. An answer that cannot be read leaves
// its series unread too, and the run goes on to the next.
func TestTVmazeRefusingLeavesTheSeriesUnread(t *testing.T) {
	r := newFollowRig(t)
	ctx := context.Background()
	f := newFakeSchedule(t, r.svc)
	nightShow(r, f)
	forbid := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	defer forbid.Close()
	c := tvmaze.New(forbid.URL, "acquire-test")
	c.Every = time.Millisecond
	r.svc.sched = c
	run := requestNightShow(t, r)
	ti, _ := r.svc.st.TitleByID(ctx, "series:990721")
	st, _ := r.svc.AirTimesStatus(ctx)
	if !strings.Contains(run.ScheduleError, "403") || ti.ScheduleAt != nil || st.Pending != 1 || st.Unknown != 0 {
		t.Fatalf("a 403: %+v; %+v; %+v", run, ti, st)
	}
	doc, _ := r.svc.GetSearchSettings(ctx)
	in := doc.SearchSettings
	in.AutoGrabNew = true
	var missing *AirTimesMissing
	if _, err := r.svc.PutSearchSettings(ctx, in, doc.Revision, "admin", false); !errors.As(err, &missing) {
		t.Errorf("switched on with TVmaze refusing: %v", err)
	}
	if got := sweepFor(t, r, nightFake, local(-2, 6, 0, "America/New_York")); len(got) != 0 {
		t.Errorf("the morning's fake was taken: %v", got)
	}

	// One show's record cannot be read, the next one's can.
	r2 := newFollowRig(t)
	f2 := newFakeSchedule(t, r2.svc)
	nightShow(r2, f2)
	f2.set(func(f *fakeSchedule) {
		f.records[501]["id"] = 777 // the record of another show
	})
	r2.tmdb.set(func(t *fakeTMDB) {
		t.series[990722] = &tmdbSeries{name: "Example Day Show", status: "Returning Series", tvdb: 9907722, firstAir: day(-2),
			countries: []string{"US"}, seasons: map[int][]tmdbEpisode{1: {{title: "One", air: day(-2)}}}}
	})
	f2.set(func(f *fakeSchedule) {
		f.shows["thetvdb=9907722"] = 502
		f.records[502] = map[string]any{"name": "Example Day Show"}
		f.eps[502] = []map[string]any{airs(1, 1, local(-2, 13, 0, "America/New_York"))}
	})
	for _, id := range []int64{990721, 990722} {
		if _, _, err := r2.svc.Request(ctx, store.Wanted{TMDBID: id, MediaType: "series", Title: "x"}, asAdmin("admin")); err != nil {
			t.Fatal(err)
		}
	}
	run = r2.settle(t, false)
	bad, _ := r2.svc.st.TitleByID(ctx, "series:990721")
	good, _ := r2.svc.st.TitleByID(ctx, "series:990722")
	if run.ScheduleError != "" || run.Scheduled != 1 || bad.ScheduleAt != nil || !strings.Contains(bad.ScheduleError, "cannot be read") ||
		good.ScheduleAt == nil {
		t.Errorf("an answer that cannot be read: %+v; %+v; %+v", run, bad, good)
	}
}

// More than a third of the series that run not known to TVmaze: the status
// warns, and so does the refresh's log.
func TestManySeriesUnknownToTVmaze(t *testing.T) {
	r := newFollowRig(t)
	ctx := context.Background()
	f := newFakeSchedule(t, r.svc)
	for i := int64(1); i <= 3; i++ {
		title, _ := track(t, r.svc, 990790+i, "Example Show", 1, true)
		sqlExec(t, `UPDATE titles SET tvdb_id = $2 WHERE id = $1`, title.ID, 9907790+i)
	}
	f.set(func(f *fakeSchedule) {
		f.shows["thetvdb=9907791"] = 591
		f.eps[591] = []map[string]any{airs(1, 1, local(-2, 21, 0, "America/New_York"))}
	})
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)
	r.settle(t, true)
	st, err := r.svc.AirTimesStatus(ctx)
	if err != nil || st.Unknown != 2 || st.Running != 3 ||
		st.Warning != "2 of the 3 series that run are not known to TVmaze — more than a third: their TVDB or IMDb ids may be missing or wrong; "+
			"they are told by their air dates alone" {
		t.Fatalf("the status: %+v %v", st, err)
	}
	if !strings.Contains(buf.String(), "acquire inventory-refresh: 2 of the 3 series that run are not known to TVmaze") {
		t.Errorf("the log: %s", buf.String())
	}
}

// The broadcast rule by the feeds and the sweep alike: an hour-long
// broadcast's release is refused before it ends, taken after; a two-hour
// one's for its whole length. A streamed episode dated to the minute: refused
// more than 30 minutes before it streams, taken after.
func TestTheBroadcastRuleByTheFeedsAndTheSweep(t *testing.T) {
	for _, c := range []struct {
		off  time.Duration
		want bool
	}{{-30 * time.Minute, false}, {-time.Minute, false}, {30 * time.Minute, false}, {55 * time.Minute, false}, {65 * time.Minute, true}} {
		r, _, start := nightShowRead(t, nil)
		if got := sweepFor(t, r, nightFake, start.Add(c.off)); (len(got) > 0) != c.want {
			t.Errorf("the sweep, published %v from the start: %v, want taken %v", c.off, got, c.want)
		}
		r2, _, _ := nightShowRead(t, nil)
		addFeed(t, r2.svc, "feed", newFeedSource(t, true, tv(nightFake, start.Add(c.off))))
		autoGrabNew(t, r2.svc, "")
		if run, _ := r2.svc.SyncRSS(context.Background()); (run.Grabbed > 0) != c.want {
			t.Errorf("the feeds, published %v from the start: %+v, want taken %v", c.off, run, c.want)
		}
	}
	r, _, start := nightShowRead(t, func(f *fakeSchedule) {
		e := airs(1, 1, local(-2, 21, 0, "America/New_York"))
		e["runtime"] = 120
		f.eps[501][0] = e
	})
	if got := sweepFor(t, r, nightFake, start.Add(100*time.Minute)); len(got) != 0 {
		t.Errorf("100 minutes into a two-hour broadcast: %v", got)
	}
	for _, c := range []struct {
		off  time.Duration
		want bool
	}{{-45 * time.Minute, false}, {-10 * time.Minute, true}, {20 * time.Minute, true}} {
		r, _, _ := nightShowRead(t, func(f *fakeSchedule) {
			f.records[501] = streamed("Example Night Show", "US")
			f.eps[501] = []map[string]any{airs(1, 1, local(-2, 3, 0, "America/New_York")), airs(1, 2, local(0, 3, 0, "America/New_York"))}
		})
		if got := sweepFor(t, r, nightFake, local(-2, 3, 0, "America/New_York").Add(c.off)); (len(got) > 0) != c.want {
			t.Errorf("streamed at 03:00, published %v from it: %v, want taken %v", c.off, got, c.want)
		}
	}
}
