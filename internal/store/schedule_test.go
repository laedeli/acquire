package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// scheduleSeries is a followed series with two episodes, E01 two days ago,
// E02 in five, read at TMDB.
func scheduleSeries(t *testing.T, s *Store, airTimes bool) Title {
	t.Helper()
	ctx := context.Background()
	ti := approvedFollow(t, s, Title{TMDBID: 990881, Kind: "series", Title: "Example Night Show"})
	if _, err := s.ApplySeriesInventory(ctx, ti.ID, SeriesRead{Status: "Returning Series", Running: true, Seasons: map[int]int{1: 2},
		Read: []int{1}, AirTimes: airTimes, Episodes: []EpisodeRead{{TMDBID: 901, Season: 1, Episode: 1, AirDate: day(-2)},
			{TMDBID: 902, Season: 1, Episode: 2, AirDate: day(5)}}}); err != nil {
		t.Fatal(err)
	}
	return ti
}

func at(d *time.Time, h time.Duration) *time.Time {
	v := d.Add(h)
	return &v
}

// A read of the schedule source writes what it made of each episode: the
// moment it airs, estimated or not, the source's later day, the episode it
// took, a hold said on the target while it lasts; an episode it names
// nothing of keeps nothing of the source's, and one whose day TMDB changed
// since the read saw it is not written. The series counts as read by these
// rules.
func TestApplyingWhatAReadMadeOfEachEpisode(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	ti := scheduleSeries(t, s, true)
	one, two := ti.ID+":s01e001", ti.ID+":s01e002"
	if _, err := s.ApplySchedule(ctx, ti.ID, ScheduledShow{ID: 501, Kind: ScheduleBroadcast, Country: "US", Targets: []ScheduledTarget{
		{TargetID: one, AirDate: day(-2), EpisodeID: 11, Stamp: at(day(-2), 25*time.Hour), Runtime: 60},
		{TargetID: two, AirDate: day(5), EpisodeID: 12, Hold: "held for a reason"},
	}}); err != nil {
		t.Fatal(err)
	}
	a, _ := s.GetTarget(ctx, one)
	b, _ := s.GetTarget(ctx, two)
	got, _ := s.TitleByID(ctx, ti.ID)
	if a.AirStamp == nil || a.ScheduleEpisodeID != 11 || a.AirRuntime != 60 || a.ScheduleHold != "" || got.ScheduleVersion != ScheduleVersion {
		t.Errorf("E01: %+v; the title's version %d", a, got.ScheduleVersion)
	}
	if b.ScheduleHold != "held for a reason" || b.Detail != "held for a reason" || b.AirStamp != nil || b.ScheduleEpisodeID != 12 {
		t.Errorf("E02 held: %+v", b)
	}
	// The next read: E01 estimated on TVmaze's later day, E02 not held any
	// more; nothing said of a third.
	if _, err := s.ApplySchedule(ctx, ti.ID, ScheduledShow{ID: 501, Kind: ScheduleBroadcast, Targets: []ScheduledTarget{
		{TargetID: one, AirDate: day(-2), EpisodeID: 11, Stamp: at(day(5), 25*time.Hour), Estimated: true, Day: day(5)},
		{TargetID: two, AirDate: day(5), EpisodeID: 12},
	}}); err != nil {
		t.Fatal(err)
	}
	a, _ = s.GetTarget(ctx, one)
	b, _ = s.GetTarget(ctx, two)
	if !a.AirStampEstimated || a.ScheduleDay == nil || !a.ScheduleDay.Equal(*day(5)) || a.AirRuntime != 0 {
		t.Errorf("E01 estimated on the later day: %+v", a)
	}
	if b.ScheduleHold != "" || b.Detail != "" {
		t.Errorf("E02 no longer held: %+v", b)
	}
	// A read that saw E01 on a day TMDB has moved since writes nothing of it;
	// one that names E02 no more takes everything of the source's off it.
	if _, err := s.ApplySchedule(ctx, ti.ID, ScheduledShow{ID: 501, Kind: ScheduleBroadcast, Targets: []ScheduledTarget{
		{TargetID: one, AirDate: day(-3), EpisodeID: 99, Hold: "stale"},
	}}); err != nil {
		t.Fatal(err)
	}
	a, _ = s.GetTarget(ctx, one)
	b, _ = s.GetTarget(ctx, two)
	if a.ScheduleEpisodeID != 11 || a.ScheduleHold != "" {
		t.Errorf("E01 written from a read of another day: %+v", a)
	}
	if b.ScheduleEpisodeID != 0 || b.AirStamp != nil {
		t.Errorf("E02 named no more: %+v", b)
	}
}

// TMDB moving an episode's day: an air time of the source's not more than a
// day before TMDB's new day stays, one more than a day before — or estimated
// for the old day — goes, the source's later day goes; with air times used,
// the episode is held, saying so, until the source is read again. Another
// episode at its numbers — TMDB's id of it changed — forgets the source's
// episode taken for it.
func TestTMDBMovingAnEpisodeHoldsItForTVmaze(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	ti := scheduleSeries(t, s, true)
	one, two := ti.ID+":s01e001", ti.ID+":s01e002"
	if _, err := s.ApplySchedule(ctx, ti.ID, ScheduledShow{ID: 501, Kind: ScheduleBroadcast, Targets: []ScheduledTarget{
		{TargetID: one, AirDate: day(-2), EpisodeID: 11, Stamp: at(day(4), 25*time.Hour), Day: day(4)},
		{TargetID: two, AirDate: day(5), EpisodeID: 12, Stamp: at(day(5), 25*time.Hour), Estimated: true},
	}}); err != nil {
		t.Fatal(err)
	}
	// TMDB catches up with E01 (now on day 4, the day of its air time) and
	// moves E02 by a day; E02 is another episode now (TMDB id 903).
	if _, err := s.ApplySeriesInventory(ctx, ti.ID, SeriesRead{Status: "Returning Series", Running: true, Seasons: map[int]int{1: 2},
		Read: []int{1}, AirTimes: true, Episodes: []EpisodeRead{{TMDBID: 901, Season: 1, Episode: 1, AirDate: day(4)},
			{TMDBID: 903, Season: 1, Episode: 2, AirDate: day(6)}}}); err != nil {
		t.Fatal(err)
	}
	a, _ := s.GetTarget(ctx, one)
	b, _ := s.GetTarget(ctx, two)
	if a.AirStamp == nil || a.ScheduleDay != nil || a.ScheduleHold != ScheduleMovedHold || a.Detail != ScheduleMovedHold {
		t.Errorf("E01, TMDB caught up: %+v", a)
	}
	if b.AirStamp != nil || b.AirStampEstimated || b.ScheduleHold != ScheduleMovedHold || b.ScheduleEpisodeID != 0 {
		t.Errorf("E02 moved, another episode: %+v", b)
	}
	// TMDB moves E01 to day 9, five days after its air time: the air time
	// goes.
	if _, err := s.ApplySeriesInventory(ctx, ti.ID, SeriesRead{Status: "Returning Series", Running: true, Seasons: map[int]int{1: 2},
		Read: []int{1}, AirTimes: true, Episodes: []EpisodeRead{{TMDBID: 901, Season: 1, Episode: 1, AirDate: day(9)},
			{TMDBID: 903, Season: 1, Episode: 2, AirDate: day(6)}}}); err != nil {
		t.Fatal(err)
	}
	if a, _ := s.GetTarget(ctx, one); a.AirStamp != nil || a.AirRuntime != 0 {
		t.Errorf("E01 moved after its air time: %+v", a)
	}

	// Air times not used: nothing is held.
	s2 := migrated(t)
	ti2 := scheduleSeries(t, s2, false)
	if _, err := s2.ApplySchedule(ctx, ti2.ID, ScheduledShow{ID: 501, Targets: []ScheduledTarget{{TargetID: ti2.ID + ":s01e001", AirDate: day(-2)}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s2.ApplySeriesInventory(ctx, ti2.ID, SeriesRead{Status: "Returning Series", Running: true, Seasons: map[int]int{1: 2},
		Read: []int{1}, Episodes: []EpisodeRead{{TMDBID: 901, Season: 1, Episode: 1, AirDate: day(-1)},
			{TMDBID: 902, Season: 1, Episode: 2, AirDate: day(5)}}}); err != nil {
		t.Fatal(err)
	}
	if a, _ := s2.GetTarget(ctx, ti2.ID+":s01e001"); a.ScheduleHold != "" {
		t.Errorf("held with air times off: %+v", a)
	}
}

// A series the source does not know any more is told by its days: nothing
// of it is held for the source, no hour estimated stays; the air times the
// source gave stay. Of another show behind its id, nothing stays.
func TestASeriesTheSourceDoesNotKnow(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	ti := scheduleSeries(t, s, true)
	one, two := ti.ID+":s01e001", ti.ID+":s01e002"
	apply := func() {
		t.Helper()
		if _, err := s.ApplySchedule(ctx, ti.ID, ScheduledShow{ID: 501, Kind: ScheduleBroadcast, Targets: []ScheduledTarget{
			{TargetID: one, AirDate: day(-2), EpisodeID: 11, Stamp: at(day(-2), 25*time.Hour)},
			{TargetID: two, AirDate: day(5), EpisodeID: 12, Stamp: at(day(5), 25*time.Hour), Estimated: true, Hold: ""},
		}}); err != nil {
			t.Fatal(err)
		}
	}
	apply()
	if err := s.ScheduleFailed(ctx, ti.ID, "TVmaze does not know it", ScheduleUnknown); err != nil {
		t.Fatal(err)
	}
	a, _ := s.GetTarget(ctx, one)
	b, _ := s.GetTarget(ctx, two)
	got, _ := s.TitleByID(ctx, ti.ID)
	if a.AirStamp == nil || b.AirStamp != nil || got.ScheduleID != 0 || got.ScheduleVersion != ScheduleVersion || got.ScheduleAt == nil {
		t.Errorf("unknown: E01 %+v, E02 %+v, %+v", a, b, got)
	}
	apply()
	if err := s.ScheduleFailed(ctx, ti.ID, "another show", ScheduleOtherShow); err != nil {
		t.Fatal(err)
	}
	if a, _ := s.GetTarget(ctx, one); a.AirStamp != nil || a.ScheduleEpisodeID != 0 {
		t.Errorf("another show: %+v", a)
	}
	// TVmaze down: nothing changes, and a series never read stays so.
	if err := s.ScheduleFailed(ctx, ti.ID, "TVmaze did not answer", ScheduleDown); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.TitleByID(ctx, ti.ID); got.ScheduleError != "TVmaze did not answer" || got.ScheduleAt == nil {
		t.Errorf("down: %+v", got)
	}
}

// migrateAllBut applies every embedded migration but the one named: an
// install of the release before it.
func migrateAllBut(t *testing.T, pool *pgxpool.Pool, name string) {
	t.Helper()
	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".sql" || e.Name() == name {
			continue
		}
		body, err := migrationFS.ReadFile("migrations/" + e.Name())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(context.Background(), string(body)); err != nil {
			t.Fatalf("apply %s: %v", e.Name(), err)
		}
	}
}

// 043 over a 0.6.0 install: a series whose air times 0.6.0 read counts as
// read by earlier rules — due first, and nothing of it grabbed on its own
// until it is read again —; what its episodes carry stays; a second boot
// changes nothing.
func TestMigrate043OverA060Install(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	migrateAllBut(t, pool, "043_airtimes.sql")
	if _, err := pool.Exec(ctx, `
		INSERT INTO titles (id, tmdb_id, kind, title, status, monitored, schedule_id, schedule_at)
		VALUES ('series:990882', 990882, 'series', 'Example Show', 'continuing', true, 502, now() - interval '1 hour');
		INSERT INTO acquisition_targets (id, title_id, kind, season_number, episode_number, state, air_date, air_window_opens_at, monitored, air_stamp)
		VALUES ('series:990882:s01e001', 'series:990882', 'episode', 1, 1, 'wanted', current_date, now(), true, now());`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := migrate(ctx, pool); err != nil {
			t.Fatalf("boot %d: %v", i+1, err)
		}
	}
	s := &Store{pool: pool}
	ti, err := s.TitleByID(ctx, "series:990882")
	if err != nil || ti.ScheduleVersion != 0 || ti.ScheduleID != 502 {
		t.Fatalf("the title: %+v %v", ti, err)
	}
	due, _ := s.SchedulesDue(ctx, ScheduleEvery{Running: 5 * time.Hour, Unknown: 7 * 24 * time.Hour, Other: 30 * 24 * time.Hour}, 10, false)
	if len(due) != 1 || due[0].ID != ti.ID {
		t.Errorf("due: %v", due)
	}
	if a, _ := s.AirTimesOfRunning(ctx); a.Pending != 1 || a.Read != 0 {
		t.Errorf("of the series that run: %+v", a)
	}
	if tg, _ := s.GetTarget(ctx, "series:990882:s01e001"); tg.AirStamp == nil || tg.ScheduleHold != "" || tg.AirStampEstimated {
		t.Errorf("the episode: %+v", tg)
	}
}
