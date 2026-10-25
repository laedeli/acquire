package app

import (
	"context"
	"strings"
	"testing"
)

// undated is an episode as TVmaze lists one without a date: to be announced.
func undated(season, number int) map[string]any {
	return map[string]any{"id": season*1000 + number, "season": season, "number": number, "name": "",
		"airdate": "", "airtime": "", "airstamp": nil, "runtime": 60}
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
