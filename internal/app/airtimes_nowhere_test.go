package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"testing"

	"github.com/laedeli/acquire/internal/store"
)

// undated is an episode as TVmaze lists one without a date: to be announced.
func undated(season, number int) map[string]any {
	return map[string]any{"id": season*1000 + number, "season": season, "number": number, "name": "",
		"airdate": "", "airtime": "", "airstamp": nil, "runtime": 60}
}

// failing puts a server in front of the service's TVmaze that answers status
// to the paths starting with prefix, and passes everything else on.
func failing(t *testing.T, svc *Service, prefix string, status int) {
	t.Helper()
	u, err := url.Parse(svc.sched.BaseURL)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(u)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, prefix) {
			w.WriteHeader(status)
			return
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	svc.sched.BaseURL = srv.URL
}

// An episode TMDB dates two days ago and TVmaze nowhere — listed with no
// date (postponed) from the first read, its date taken away later, or
// dropped from TVmaze's list — is held: nothing published on TMDB's day, the
// one it may have been moved from, is taken, by the sweep or the feeds; its
// target and its request say why. Once TVmaze dates it again, it is not.
func TestAnEpisodeTVmazeDatesNowhereIsHeld(t *testing.T) {
	ctx := context.Background()
	slot := local(-2, 23, 30, "America/New_York")
	const postponed = "TVmaze lists it with no date — postponed, perhaps —: no automatic grab takes it until TVmaze dates it"
	const pulled = "TVmaze listed it and does not any more — pulled, perhaps —: no automatic grab takes it until TVmaze lists it again"
	reread := func(r *followRig) {
		t.Helper()
		sqlExec(t, `UPDATE titles SET schedule_at = now() - interval '6 hours' WHERE id = 'series:990721'`)
		r.settle(t, true)
	}

	// Postponed from the first read.
	r, f, start := nightShowRead(t, func(f *fakeSchedule) { f.eps[501][0] = undated(1, 1) })
	one := target(t, r.svc, "series:990721:s01e001")
	if one.ScheduleHold != postponed || one.Detail != postponed || one.AirStamp != nil {
		t.Fatalf("E01 postponed: %+v", one)
	}
	if got := sweepFor(t, r, nightFake, slot); len(got) != 0 {
		t.Errorf("a release of TMDB's day was taken: %v", got)
	}
	w, _ := r.svc.st.FindRequestFor(ctx, 990721, []string{"series"})
	if v := r.view(t, w.ID); !strings.Contains(v.Detail, "1 episode held") {
		t.Errorf("the request: %q", v.Detail)
	}
	// TVmaze dates it again, as TMDB does: not held.
	f.set(func(f *fakeSchedule) { f.eps[501][0] = airs(1, 1, local(-2, 21, 0, "America/New_York")) })
	reread(r)
	if one := target(t, r.svc, "series:990721:s01e001"); one.ScheduleHold != "" || one.Detail != "" || one.AirStamp == nil || !one.AirStamp.Equal(start) {
		t.Errorf("E01 dated again: %+v", one)
	}

	// Its date taken away on a later read.
	r2, f2, _ := nightShowRead(t, nil)
	f2.set(func(f *fakeSchedule) { f.eps[501][0] = undated(1, 1) })
	reread(r2)
	if one := target(t, r2.svc, "series:990721:s01e001"); one.ScheduleHold != postponed || one.AirStamp != nil {
		t.Errorf("E01 made undated: %+v", one)
	}
	if got := sweepFor(t, r2, nightFake, slot); len(got) != 0 {
		t.Errorf("a release of TMDB's day was taken once TVmaze took the date away: %v", got)
	}

	// Dropped from TVmaze's list.
	r3, f3, _ := nightShowRead(t, nil)
	f3.set(func(f *fakeSchedule) { f.eps[501] = f.eps[501][1:] })
	reread(r3)
	if one := target(t, r3.svc, "series:990721:s01e001"); one.ScheduleHold != pulled || one.AirStamp != nil {
		t.Errorf("E01 dropped: %+v", one)
	}
	if got := sweepFor(t, r3, nightFake, slot); len(got) != 0 {
		t.Errorf("a release of an episode TVmaze no longer lists was taken: %v", got)
	}

	// The feeds alike.
	r4, _, _ := nightShowRead(t, func(f *fakeSchedule) { f.eps[501][0] = undated(1, 1) })
	addFeed(t, r4.svc, "feed", newFeedSource(t, true, tv(nightFake, slot)))
	autoGrabNew(t, r4.svc, "")
	if run, _ := r4.svc.SyncRSS(ctx); run.Grabbed != 0 {
		t.Errorf("the feeds took a release of an episode TVmaze dates nowhere: %+v", run)
	}
}

// One show TVmaze fails for does not keep the others from being read: a run
// asks TVmaze no more only after it failed for three series in a row, and the
// series it failed for come after the ones not tried yet, on the next run.
func TestOneShowTVmazeFailsForDoesNotStallTheRest(t *testing.T) {
	r := newFollowRig(t)
	ctx := context.Background()
	f := newFakeSchedule(t, r.svc)
	for i, id := range []int64{990811, 990812} {
		title, eps := track(t, r.svc, id, []string{"Example Alpha", "Example Beta"}[i], 1, true)
		_ = r.svc.st.SetAirWindow(ctx, eps[0].ID, day(-1), 0)
		sqlExec(t, `UPDATE titles SET tvdb_id = $2, status = 'continuing' WHERE id = $1`, title.ID, 9908110+int64(i+1))
	}
	f.set(func(f *fakeSchedule) {
		f.shows["thetvdb=9908111"] = 811
		f.records[811] = map[string]any{"name": "Example Alpha"}
		f.eps[811] = []map[string]any{airs(1, 1, local(-1, 21, 0, "America/New_York"))}
		f.shows["thetvdb=9908112"] = 812
		f.records[812] = map[string]any{"name": "Example Beta"}
		f.eps[812] = []map[string]any{airs(1, 1, local(-1, 21, 0, "America/New_York"))}
	})
	failing(t, r.svc, "/shows/811", http.StatusInternalServerError)
	run := r.settle(t, true)
	alpha, _ := r.svc.st.TitleByID(ctx, "series:990811")
	beta, _ := r.svc.st.TitleByID(ctx, "series:990812")
	if beta.ScheduleAt == nil || alpha.ScheduleAt != nil || run.ScheduleFailures != 1 || run.ScheduleStopped || run.Scheduled != 1 {
		t.Fatalf("the first run: %+v; Alpha %v, Beta %v", run, alpha.ScheduleAt, beta.ScheduleAt)
	}
	st, _ := r.svc.AirTimesStatus(ctx)
	if st.Pending != 1 || !strings.Contains(st.LastError, "500") {
		t.Errorf("the status: %+v", st)
	}
	// The one TVmaze failed for comes after a series not tried yet.
	gamma, eps := track(t, r.svc, 990813, "Example Gamma", 1, true)
	_ = r.svc.st.SetAirWindow(ctx, eps[0].ID, day(-1), 0)
	sqlExec(t, `UPDATE titles SET tvdb_id = 9908113, status = 'continuing' WHERE id = $1`, gamma.ID)
	due, _ := r.svc.st.SchedulesDue(ctx, scheduleEveryOf, 10, false)
	if len(due) < 2 || due[0].ID != gamma.ID || due[len(due)-1].ID != "series:990811" {
		t.Errorf("due: %v", ids2(due))
	}

	// Three in a row: the run asks TVmaze no more.
	r2 := newFollowRig(t)
	newFakeSchedule(t, r2.svc)
	for i := int64(1); i <= 4; i++ {
		title, eps := track(t, r2.svc, 990820+i, "Example Show", 1, true)
		_ = r2.svc.st.SetAirWindow(ctx, eps[0].ID, day(-1), 0)
		sqlExec(t, `UPDATE titles SET tvdb_id = $2, status = 'continuing' WHERE id = $1`, title.ID, 9908200+i)
	}
	failing(t, r2.svc, "/lookup", http.StatusServiceUnavailable)
	run = r2.settle(t, true)
	if run.ScheduleFailures != 3 || !run.ScheduleStopped {
		t.Errorf("TVmaze down for every series: %+v", run)
	}
}

func ids2(ts []store.Title) []string {
	var out []string
	for _, t := range ts {
		out = append(out, t.ID)
	}
	return out
}

// TMDB moving an episode holds it until TVmaze is read again; with another
// show TVmaze fails for read first, the series is still read in the same run
// and the hold lifts. A hold made while TVmaze is down is lifted by the
// 15-minute retry, not left until the series is due again hours later.
func TestAHoldAfterAMoveLifts(t *testing.T) {
	ctx := context.Background()
	r, f, _ := nightShowRead(t, nil)
	r.tmdb.set(func(t *fakeTMDB) {
		t.series[990831] = &tmdbSeries{name: "Example Alpha", status: "Returning Series", tvdb: 9908831, firstAir: day(-30),
			countries: []string{"US"}, seasons: map[int][]tmdbEpisode{1: {{title: "Pilot", air: day(-1)}}}}
	})
	f.set(func(f *fakeSchedule) {
		f.shows["thetvdb=9908831"] = 831
		f.records[831] = map[string]any{"name": "Example Alpha"}
		f.eps[831] = []map[string]any{airs(1, 1, local(-1, 21, 0, "America/New_York"))}
	})
	failing(t, r.svc, "/shows/831", http.StatusInternalServerError)
	if _, _, err := r.svc.Request(ctx, store.Wanted{TMDBID: 990831, MediaType: "series", Title: "Example Alpha"}, asAdmin("admin")); err != nil {
		t.Fatal(err)
	}
	r.settle(t, false)
	// TMDB moves the night show's second episode; TVmaze agrees.
	r.tmdb.set(func(t *fakeTMDB) { t.series[990721].seasons[1][1].air = day(1) })
	f.set(func(f *fakeSchedule) { f.eps[501][1] = airs(1, 2, local(1, 21, 0, "America/New_York")) })
	sqlExec(t, `UPDATE titles SET inventory_at = now() - interval '7 hours' WHERE id = 'series:990831'`)
	sqlExec(t, `UPDATE titles SET inventory_at = now() - interval '6 hours' WHERE id = 'series:990721'`)
	r.settle(t, true)
	if two := target(t, r.svc, "series:990721:s01e002"); two.ScheduleHold != "" || two.AirStamp == nil || !two.AirStamp.Equal(local(1, 21, 0, "America/New_York")) {
		t.Errorf("E02 behind a show TVmaze fails for: %+v", two)
	}

	// A move while TVmaze is down: held; the retry lifts it once TVmaze
	// answers, the series' own read not due for hours.
	r2, f2, _ := nightShowRead(t, nil)
	f2.set(func(f *fakeSchedule) { f.down = true })
	r2.tmdb.set(func(t *fakeTMDB) { t.series[990721].seasons[1][0].air = day(-3) })
	f2.set(func(f *fakeSchedule) { f.eps[501][0] = airs(1, 1, local(-3, 21, 0, "America/New_York")) })
	sqlExec(t, `UPDATE titles SET inventory_at = now() - interval '6 hours' WHERE id = 'series:990721'`)
	r2.settle(t, true)
	if one := target(t, r2.svc, "series:990721:s01e001"); one.ScheduleHold != store.ScheduleMovedHold {
		t.Fatalf("E01 moved with TVmaze down: %+v", one)
	}
	if st, _ := r2.svc.AirTimesStatus(ctx); st.HeldMoved != 1 || st.Held != 1 {
		t.Errorf("the status: %+v", st)
	}
	f2.set(func(f *fakeSchedule) { f.down = false })
	r2.svc.retrySchedules(ctx)
	if one := target(t, r2.svc, "series:990721:s01e001"); one.ScheduleHold != "" || one.AirStamp == nil || !one.AirStamp.Equal(local(-3, 21, 0, "America/New_York")) {
		t.Errorf("E01 after the retry: %+v", one)
	}
}

// A series that ended, whose air times TVmaze keeps failing for, is counted
// as failing, and comes after the series due that TVmaze has not failed for:
// a running series' read of a move TVmaze knows is made, and the old slot's
// release is not taken.
func TestAFailingEndedSeries(t *testing.T) {
	ctx := context.Background()
	r, f, _ := nightShowRead(t, nil)
	ended, eps := track(t, r.svc, 990841, "Example Gamma", 1, true)
	_ = r.svc.st.SetAirWindow(ctx, eps[0].ID, day(-400), 0)
	sqlExec(t, `UPDATE titles SET tvdb_id = 9908841, tmdb_status = 'Ended', status = 'ended', inventory_at = now() WHERE id = $1`, ended.ID)
	f.set(func(f *fakeSchedule) {
		f.shows["thetvdb=9908841"] = 841
		f.records[841] = map[string]any{"name": "Example Gamma"}
		f.eps[841] = []map[string]any{airs(1, 1, local(-400, 21, 0, "America/New_York"))}
		// TVmaze moves the night show's first episode a week on.
		f.eps[501][0] = airs(1, 1, local(5, 21, 0, "America/New_York"))
	})
	failing(t, r.svc, "/shows/841", http.StatusInternalServerError)
	sqlExec(t, `UPDATE titles SET schedule_at = now() - interval '6 hours' WHERE id = 'series:990721'`)
	r.settle(t, true)
	r.svc.retrySchedules(ctx)
	st, _ := r.svc.AirTimesStatus(ctx)
	if st.FailingEnded != 1 {
		t.Errorf("the status: %+v", st)
	}
	if one := target(t, r.svc, "series:990721:s01e001"); one.ScheduleDay == nil || !one.ScheduleDay.Equal(*day(5)) {
		t.Errorf("E01, moved at TVmaze: %+v", one)
	}
	if got := sweepFor(t, r, nightFake, local(-2, 23, 30, "America/New_York")); len(got) != 0 {
		t.Errorf("a release of the old slot was taken: %v", got)
	}
}

// Switching grabbing on its own on while series read by 0.6.0's rules wait
// to be read again says so, not that they have no air times.
func TestTheConfirmationForSeriesReadBefore(t *testing.T) {
	r, _, _ := nightShowRead(t, nil)
	ctx := context.Background()
	sqlExec(t, `UPDATE titles SET schedule_version = 0 WHERE id = 'series:990721'`)
	doc, _ := r.svc.GetSearchSettings(ctx)
	in := doc.SearchSettings
	in.AutoGrabNew = true
	_, err := r.svc.PutSearchSettings(ctx, in, doc.Revision, "admin", false)
	var missing *AirTimesMissing
	if !errors.As(err, &missing) || err.Error() != "1 of the 1 series that run was read by an earlier version of acquire's rules, and is read again: "+
		"nothing of it is grabbed on its own until it is read, on the next refreshes — confirm to switch grabbing on its own on now" {
		t.Errorf("the confirmation: %v", err)
	}
}
