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
