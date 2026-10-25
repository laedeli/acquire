package store

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

// day is midnight UTC d days from today: how TMDB's air dates read.
func day(d int) *time.Time {
	t := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, d)
	return &t
}

func episodesOf(season int, airs ...*time.Time) []EpisodeRead {
	var out []EpisodeRead
	for i, a := range airs {
		out = append(out, EpisodeRead{Season: season, Episode: i + 1, Title: "Chapter " + string(rune('A'+i)), AirDate: a})
	}
	return out
}

func targetsByCoord(t *testing.T, s *Store, titleID string) map[string]Target {
	t.Helper()
	all, err := s.TitleTargets(context.Background(), titleID)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]Target{}
	for _, x := range all {
		out[x.ID] = x
	}
	return out
}

// A request follows a title: a new one is created from what the request
// says, monitored, and followed from now; one the import tracks is not made
// twice, but monitored again, and keeps when it was first followed.
// approvedFollow follows a title for a request an admin approved.
func approvedFollow(t *testing.T, s *Store, ti Title) Title {
	t.Helper()
	ctx := context.Background()
	f, _, err := s.FollowTitle(ctx, ti)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CreateWanted(ctx, Wanted{ID: "w-" + f.ID, TMDBID: f.TMDBID, MediaType: f.Kind, Title: f.Title, TitleID: f.ID,
		Approval: ApprovalApproved, DecidedBy: "admin"}); err != nil {
		t.Fatal(err)
	}
	f, err = s.TitleByID(ctx, f.ID)
	if err != nil || !f.Approved {
		t.Fatalf("an approved follow: %+v %v", f, err)
	}
	return f
}

// A title followed for requests no admin approved yet is held for their
// approval when the requests are what monitors it: nothing of it is
// searched or grabbed on its own. One an admin monitored before is not.
func TestAFollowHeldForApproval(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	requested, _, _ := s.FollowTitle(ctx, Title{TMDBID: 1, Kind: "series", Title: "Requested"})
	if err := s.UpsertTitle(ctx, Title{TMDBID: 2, Kind: "series", Title: "Monitored", Monitored: true}); err != nil {
		t.Fatal(err)
	}
	monitored, _, _ := s.FollowTitle(ctx, Title{TMDBID: 2, Kind: "series", Title: "Monitored"})
	for _, ti := range []Title{requested, monitored} {
		if err := s.CreateWanted(ctx, Wanted{ID: "w-" + ti.ID, TMDBID: ti.TMDBID, MediaType: "series", Title: ti.Title, TitleID: ti.ID,
			Approval: ApprovalAwaiting}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.ApplySeriesInventory(ctx, ti.ID, SeriesRead{Seasons: map[int]int{1: 1}, Read: []int{1},
			Episodes: episodesOf(1, day(-3)), MonitorAll: true}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.ApplySeriesHoldings(ctx, ti.ID, "", "default", nil); err != nil {
			t.Fatal(err)
		}
	}
	due := func() []string {
		ts, _ := s.DueTargetsBy(ctx, 10, DueOrder{NewWithinDays: 14})
		var ids []string
		for _, x := range ts {
			ids = append(ids, x.TitleID)
		}
		return ids
	}
	a, _ := s.TitleByID(ctx, requested.ID)
	b, _ := s.TitleByID(ctx, monitored.ID)
	if !a.HeldForApproval() || b.HeldForApproval() || a.Approved || b.Approved {
		t.Errorf("held for approval: %v %v", a.HeldForApproval(), b.HeldForApproval())
	}
	if got := due(); len(got) != 1 || got[0] != monitored.ID {
		t.Errorf("due = %v, want the one an admin monitored", got)
	}
	if err := s.DecideWanted(ctx, "w-"+requested.ID, ApprovalApproved, "admin"); err != nil {
		t.Fatal(err)
	}
	if got := due(); len(got) != 2 {
		t.Errorf("due once approved = %v", got)
	}
	// A declined request does not keep a follow: the last one that is not
	// declined gone, the follow ends.
	if err := s.DecideWanted(ctx, "w-"+monitored.ID, ApprovalDeclined, "admin"); err != nil {
		t.Fatal(err)
	}
	if err := s.UnfollowTitle(ctx, monitored.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.TitleByID(ctx, monitored.ID); got.Followed() || !got.Monitored {
		t.Errorf("declined: %+v", got)
	}
}

func TestFollowingATitle(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	ti, created, err := s.FollowTitle(ctx, Title{TMDBID: 990001, Kind: "series", Title: "The Example Saga", Year: 2026})
	if err != nil || !created {
		t.Fatalf("follow: %+v %v %v", ti, created, err)
	}
	if ti.ID != "series:990001" || !ti.Monitored || !ti.MonitorNew || ti.FollowedAt == nil || ti.Origin != OriginRequest ||
		ti.SortTitle != "example saga" || ti.Year != 2026 || ti.HoldingsKnown() || ti.InventoryAt != nil {
		t.Errorf("a followed title: %+v", ti)
	}

	if err := s.UpsertTitle(ctx, Title{TMDBID: 77, Kind: "series", Title: "Imported Show", Monitored: false, TVDBID: 7}); err != nil {
		t.Fatal(err)
	}
	again, created, err := s.FollowTitle(ctx, Title{TMDBID: 77, Kind: "series", Title: "Imported Show (renamed)"})
	if err != nil || created || again.ID != "series:77" || !again.Monitored || again.FollowedAt == nil ||
		again.Origin != "" || again.Title != "Imported Show" || again.TVDBID != 7 {
		t.Fatalf("following a tracked title: %+v %v %v", again, created, err)
	}
	first := *again.FollowedAt
	if later, _, _ := s.FollowTitle(ctx, Title{TMDBID: 77, Kind: "series", Title: "x"}); !later.FollowedAt.Equal(first) {
		t.Errorf("followed again: %v, want %v kept", later.FollowedAt, first)
	}
	var n int
	_ = s.pool.QueryRow(ctx, `SELECT count(*) FROM titles WHERE tmdb_id = 77`).Scan(&n)
	if n != 1 {
		t.Errorf("%d titles for one TMDB id", n)
	}
	rows, err := s.SeriesOverview(ctx)
	if err != nil || len(rows) != 2 || !rows[0].Followed || !rows[1].Followed {
		t.Errorf("the series overview marks what is followed: %+v %v", rows, err)
	}
	if _, _, err := s.FollowTitle(ctx, Title{Kind: "series", Title: "no id"}); err == nil {
		t.Error("a title without a TMDB id was followed")
	}

	// A tracked title an admin monitors, followed too.
	if err := s.UpsertTitle(ctx, Title{TMDBID: 78, Kind: "series", Title: "Monitored Show", Monitored: true, MonitorNew: false}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.FollowTitle(ctx, Title{TMDBID: 78, Kind: "series", Title: "Monitored Show"}); err != nil {
		t.Fatal(err)
	}

	// The follow ends with its last request, and the monitoring is what it
	// was before it began: a title the request made is not monitored any
	// more, one an admin had stopped monitoring is stopped again, one an
	// admin monitors is monitored still.
	for _, w := range []Wanted{{ID: "w1", TMDBID: 990001, MediaType: "series", Title: "x", TitleID: "series:990001"},
		{ID: "w2", TMDBID: 77, MediaType: "series", Title: "y", TitleID: "series:77"},
		{ID: "w3", TMDBID: 78, MediaType: "series", Title: "z", TitleID: "series:78"}} {
		if err := s.CreateWanted(ctx, w); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.UnfollowTitle(ctx, "series:990001"); err != nil {
		t.Fatal(err)
	}
	if still, _ := s.TitleByID(ctx, "series:990001"); !still.Followed() || !still.Monitored {
		t.Errorf("unfollowed while a request follows it: %+v", still)
	}
	for _, id := range []string{"w1", "w2", "w3"} {
		_ = s.DeleteWanted(ctx, id)
	}
	for id, want := range map[string][2]bool{"series:990001": {false, false}, "series:77": {false, false}, "series:78": {true, false}} {
		if err := s.UnfollowTitle(ctx, id); err != nil {
			t.Fatal(err)
		}
		got, _ := s.TitleByID(ctx, id)
		if got.Followed() || got.FollowedAt == nil || got.UnfollowedAt == nil || got.Monitored != want[0] || got.MonitorNew != want[1] {
			t.Errorf("%s after its last request: %+v, want monitored %v, monitor_new %v, its first follow kept", id, got, want[0], want[1])
		}
	}
	// Followed again: from the first follow on, the monitoring of then
	// kept for the next end.
	firstSaga := *ti.FollowedAt
	ti, _, _ = s.FollowTitle(ctx, Title{TMDBID: 990001, Kind: "series", Title: "The Example Saga"})
	if !ti.Followed() || !ti.Monitored || ti.UnfollowedAt != nil || ti.FollowedAt == nil || !ti.FollowedAt.Equal(firstSaga) {
		t.Errorf("followed again: %+v, want followed from %v", ti, firstSaga)
	}
}

// A read of TMDB makes a series' targets what TMDB says: new episodes added,
// marked as added now, their air dates and titles kept up to date, an episode
// TMDB no longer lists removed unless the library holds it or a grab is under
// way — and nothing anyone decided (monitoring, state) touched.
// An episode TMDB numbers anew — its TMDB id listed under other numbers —
// that the library holds is held at its new number too, by the same file,
// never wanted again; its old target keeps what it held, and goes nowhere.
func TestAnEpisodeNumberedAnewKeepsItsHolding(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	ti := approvedFollow(t, s, Title{TMDBID: 77, Kind: "series", Title: "Example"})
	read := SeriesRead{Status: "Returning Series", Running: true, Seasons: map[int]int{1: 2}, Read: []int{1},
		Episodes: []EpisodeRead{{TMDBID: 701, Season: 1, Episode: 1, AirDate: day(-20)}, {TMDBID: 702, Season: 1, Episode: 2, AirDate: day(-13)}}}
	if _, err := s.ApplySeriesInventory(ctx, ti.ID, read); err != nil {
		t.Fatal(err)
	}
	if err := s.HoldTarget(ctx, Hold{TargetID: ti.ID + ":s01e002", ItemID: "item-702", Playable: true}); err != nil {
		t.Fatal(err)
	}
	// TMDB puts a new episode first: the held one is E03 now.
	read.Seasons = map[int]int{1: 3}
	read.Episodes = []EpisodeRead{{TMDBID: 700, Season: 1, Episode: 1, AirDate: day(-27)},
		{TMDBID: 701, Season: 1, Episode: 2, AirDate: day(-20)}, {TMDBID: 702, Season: 1, Episode: 3, AirDate: day(-13)}}
	ch, err := s.ApplySeriesInventory(ctx, ti.ID, read)
	if err != nil {
		t.Fatal(err)
	}
	got := targetsByCoord(t, s, ti.ID)
	three := got[ti.ID+":s01e003"]
	if ch.Renumbered != 1 || three.State != TargetHeld || three.HeldItemID != "item-702" || !three.HeldPlayable || !three.EverHeld ||
		!strings.Contains(three.Detail, "as S01E02 before TMDB numbered it anew") {
		t.Errorf("the held episode at its new number: %+v (%+v)", three, ch)
	}
	if two := got[ti.ID+":s01e002"]; two.State != TargetHeld || two.HeldItemID != "item-702" {
		t.Errorf("its old target: %+v", two)
	}
}

func TestASeriesReadOfTMDB(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	if err := s.UpsertTitle(ctx, Title{TMDBID: 55, Kind: "series", Title: "Imported Show", Monitored: true, MonitorNew: false,
		TVDBID: 9905005, Status: "continuing"}); err != nil {
		t.Fatal(err)
	}
	id := "series:55"
	if _, err := s.pool.Exec(ctx, `UPDATE titles SET air_grace_hours = 6 WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	// The first read is the series' inventory: every season of it monitored,
	// as a derivation monitors it.
	first := SeriesRead{TVDBID: 9909999, IMDBID: "tt99000055", Name: "Imported Show: Renamed", Year: 2020, Status: "Returning Series",
		Running: true, Aliases: []string{"Imported Show", "Another Name"}, Seasons: map[int]int{1: 3}, Read: []int{1},
		Episodes: episodesOf(1, day(-30), day(-23), day(-16))}
	ch, err := s.ApplySeriesInventory(ctx, id, first)
	if err != nil {
		t.Fatal(err)
	}
	if ch != (InventoryChange{Added: 3, AddedAired: 3}) {
		t.Errorf("first read: %+v", ch)
	}
	// A season that comes later is a new season: monitored when its title
	// monitors new seasons, which this one does not.
	first.Seasons, first.Read = map[int]int{1: 3, 2: 2}, []int{2}
	first.Episodes = episodesOf(2, day(-2), day(5))
	if ch, err = s.ApplySeriesInventory(ctx, id, first); err != nil || ch != (InventoryChange{Added: 2, AddedAired: 1}) {
		t.Errorf("a new season: %+v %v", ch, err)
	}
	ti, _ := s.TitleByID(ctx, id)
	if ti.TVDBID != 9905005 || ti.IMDBID != "tt99000055" || ti.TMDBStatus != "Returning Series" || ti.InventoryAt == nil ||
		ti.Title != "Imported Show" || ti.Status != "continuing" {
		t.Errorf("an imported title keeps its id and name, and gains what it had not: %+v", ti)
	}
	aliases, _ := s.AliasesFor(ctx, id)
	sort.Strings(aliases)
	if !reflect.DeepEqual(aliases, []string{"Another Name", "Imported Show: Renamed"}) {
		t.Errorf("aliases = %v, want TMDB's name and its other titles, not the title's own name", aliases)
	}
	got := targetsByCoord(t, s, id)
	one := got[id+":s01e001"]
	if one.AddedAt == nil || one.EpisodeTitle != "Chapter A" || !one.Monitored || one.State != TargetWanted ||
		one.AirDate == nil || !one.AirDate.Equal(*day(-30)) || one.WindowAt == nil || !one.WindowAt.Equal(day(-30).Add(6*time.Hour)) {
		t.Errorf("a new target: %+v", one)
	}
	if two := got[id+":s02e001"]; two.Monitored {
		t.Errorf("a new season of a title that does not monitor new seasons is monitored: %+v", two)
	}

	// Someone unmonitored S01E02; the library holds S01E03; a grab is under
	// way for S02E01. TMDB then moves S02E02, drops S01E02, S01E03 and S02E01,
	// and announces S01E04 and a third season.
	if _, err := s.pool.Exec(ctx, `UPDATE acquisition_targets SET monitored = false WHERE id = $1`, id+":s01e002"); err != nil {
		t.Fatal(err)
	}
	if err := s.HoldTarget(ctx, Hold{TargetID: id + ":s01e003", ItemID: "item-3"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetTargetState(ctx, id+":s02e001", nil, TargetGrabbed, "grabbed"); err != nil {
		t.Fatal(err)
	}
	second := first
	second.Seasons, second.Read = map[int]int{1: 2, 3: 1}, []int{1, 3}
	second.Episodes = []EpisodeRead{
		{Season: 1, Episode: 1, Title: "Chapter A", AirDate: day(-30)},
		{Season: 1, Episode: 4, Title: "Chapter D", AirDate: day(-1)},
		{Season: 3, Episode: 1, Title: "", AirDate: nil},
	}
	ch, err = s.ApplySeriesInventory(ctx, id, second)
	if err != nil {
		t.Fatal(err)
	}
	// S02 is no longer listed, nor S01E02 and E03: gone, and kept — an answer
	// that lists a season short may be TMDB's fault — and grabbed by nobody.
	if ch != (InventoryChange{Added: 2, AddedAired: 1, Gone: 4}) {
		t.Errorf("second read: %+v", ch)
	}
	if n := len(targetsByCoord(t, s, id)); n != 7 {
		t.Errorf("after a read that lists four short: %d targets, want all 7", n)
	}
	if due, _ := s.DueTargets(ctx, 10); len(due) != 0 {
		t.Errorf("due: %+v", due)
	}
	// A read within the day agrees: nothing goes yet. A day later it does:
	// S02E01 is under way (kept), S02E02 wanted (removed); of S01, E02 is
	// wanted and unmonitored (removed), E03 held (kept).
	if ch, _ := s.ApplySeriesInventory(ctx, id, second); ch.Removed != 0 || ch.Gone != 0 {
		t.Errorf("a read within the day: %+v", ch)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE acquisition_targets SET tmdb_gone_at = now() - interval '25 hours' WHERE tmdb_gone_at IS NOT NULL`); err != nil {
		t.Fatal(err)
	}
	if ch, _ = s.ApplySeriesInventory(ctx, id, second); ch.Removed != 2 || ch.Kept != 2 || ch.Gone != 0 {
		t.Errorf("a read a day later: %+v", ch)
	}
	got = targetsByCoord(t, s, id)
	for _, gone := range []string{":s01e002", ":s02e002"} {
		if _, ok := got[id+gone]; ok {
			t.Errorf("%s is still there", gone)
		}
	}
	for _, kept := range []string{":s01e003", ":s02e001", ":s01e004", ":s03e001"} {
		if _, ok := got[id+kept]; !ok {
			t.Errorf("%s is gone", kept)
		}
	}
	if held := got[id+":s01e003"]; held.State != TargetHeld || held.HeldItemID != "item-3" {
		t.Errorf("a held episode TMDB dropped: %+v", held)
	}

	// Nothing listed at all is a fault, not the series: nothing goes.
	if ch, err := s.ApplySeriesInventory(ctx, id, SeriesRead{Status: "Returning Series"}); err != nil || ch.Removed != 0 {
		t.Errorf("an empty answer: %+v %v", ch, err)
	}
	if n := len(targetsByCoord(t, s, id)); n != 5 {
		t.Errorf("an empty answer left %d targets of 5", n)
	}

	// A title a request made takes TMDB's name and year.
	followed, _, _ := s.FollowTitle(ctx, Title{TMDBID: 66, Kind: "series", Title: "As Requested"})
	if _, err := s.ApplySeriesInventory(ctx, followed.ID, SeriesRead{Name: "As TMDB Names It", Year: 2026, Status: "Ended",
		Seasons: map[int]int{1: 1}, Read: []int{1}, Episodes: episodesOf(1, day(-400)), MonitorAll: true}); err != nil {
		t.Fatal(err)
	}
	if f, _ := s.TitleByID(ctx, followed.ID); f.Title != "As TMDB Names It" || f.Year != 2026 || f.Status != "ended" || f.SortTitle != "as tmdb names it" {
		t.Errorf("a title a request made: %+v", f)
	}
}

// A target added since its title's holdings were learned is of unknown
// holdings — not missing, not swept — until the next reconcile: the library
// may hold it already.
func TestATargetAddedSinceTheHoldingsWaitsForTheNextReconcile(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	ti, _, _ := s.FollowTitle(ctx, Title{TMDBID: 990001, Kind: "series", Title: "Example"})
	if _, err := s.ApplySeriesHoldings(ctx, ti.ID, "", "default", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplySeriesInventory(ctx, ti.ID, SeriesRead{Seasons: map[int]int{1: 2}, Read: []int{1},
		Episodes: episodesOf(1, day(-3), day(-2)), MonitorAll: true}); err != nil {
		t.Fatal(err)
	}
	if miss, _ := s.Missing(ctx, 10); len(miss) != 0 {
		t.Errorf("missing before the reconcile: %+v", miss)
	}
	if due, _ := s.DueTargets(ctx, 10); len(due) != 0 {
		t.Errorf("due before the reconcile: %+v", due)
	}
	if c, _ := s.Counts(ctx); c.UnknownTargets != 2 || c.Missing != 0 {
		t.Errorf("counts before the reconcile: %+v", c)
	}
	if _, err := s.ApplySeriesHoldings(ctx, ti.ID, "", "default", nil); err != nil {
		t.Fatal(err)
	}
	if miss, _ := s.Missing(ctx, 10); len(miss) != 2 {
		t.Errorf("missing after the reconcile: %+v", miss)
	}
}

// A requested movie is wanted from the start and searched from its release:
// not before a date TMDB names, nor at all while it names none. A movie's
// target the import made keeps its window.
func TestARequestedMovieWaitsForItsRelease(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	m := approvedFollow(t, s, Title{TMDBID: 990100, Kind: "movie", Title: "Example Clay Film", Year: 2026})
	if _, err := s.ApplyMovieHolding(ctx, m.ID, "", "default", nil); err != nil {
		t.Fatal(err)
	}
	read := MovieRead{IMDBID: "tt99009100", Name: "Example Clay Film", Year: 2026, Status: "Released"}
	tg, err := s.ApplyMovieInventory(ctx, m.ID, read)
	if err != nil {
		t.Fatal(err)
	}
	if tg.State != TargetWanted || !tg.ReleasePending || tg.WindowAt != nil || tg.AddedAt == nil {
		t.Fatalf("a movie TMDB dates no release for: %+v", tg)
	}
	due := func() int {
		d, _ := s.DueTargets(ctx, 10)
		return len(d)
	}
	// Known holdings, but added after them: a reconcile first.
	if _, err := s.ApplyMovieHolding(ctx, m.ID, "", "default", nil); err != nil {
		t.Fatal(err)
	}
	if n := due(); n != 0 {
		t.Errorf("searched without a release date: %d due", n)
	}
	if c, _ := s.Counts(ctx); c.Unaired != 1 {
		t.Errorf("a movie waiting for its release is not out yet: %+v", c)
	}
	read.Window, read.Kind = day(20), "digital"
	if tg, _ = s.ApplyMovieInventory(ctx, m.ID, read); tg.ReleasePending || tg.ReleaseKind != "digital" || !tg.AirDate.Equal(*day(20)) {
		t.Errorf("a release to come: %+v", tg)
	}
	if n := due(); n != 0 {
		t.Errorf("searched before its release: %d due", n)
	}
	read.Window = day(-1)
	if _, err := s.ApplyMovieInventory(ctx, m.ID, read); err != nil {
		t.Fatal(err)
	}
	if n := due(); n != 1 {
		t.Errorf("not searched once out: %d due", n)
	}
	if ti, _ := s.TitleByID(ctx, m.ID); ti.IMDBID != "tt99009100" || ti.TMDBStatus != "Released" || ti.Status != "released" {
		t.Errorf("the movie's title: %+v", ti)
	}

	// The import's movie, requested: its window is not the request's to set.
	if err := s.UpsertTitle(ctx, Title{TMDBID: 600, Kind: "movie", Title: "Old Film", Monitored: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertTarget(ctx, Target{ID: TargetID("movie:600", nil, nil), TitleID: "movie:600", Kind: "movie",
		Monitored: true, State: TargetWanted}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.FollowTitle(ctx, Title{TMDBID: 600, Kind: "movie", Title: "Old Film"}); err != nil {
		t.Fatal(err)
	}
	old, err := s.ApplyMovieInventory(ctx, "movie:600", MovieRead{Status: "Released"})
	if err != nil || old.ReleasePending || old.WindowAt != nil || old.AddedAt != nil {
		t.Errorf("the import's movie target: %+v %v", old, err)
	}
}

// What a run of the refresh reads: a followed series never read first, then
// the ones that run, the ones read longest ago first; one that runs is due
// again after the running interval, one that has ended after a week.
func TestWhichSeriesAreReadAgain(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	for _, ti := range []Title{
		{TMDBID: 1, Kind: "series", Title: "Ended Never Read", Monitored: true, Status: "ended"},
		{TMDBID: 2, Kind: "series", Title: "Continuing Never Read", Monitored: true, Status: "continuing"},
		{TMDBID: 3, Kind: "series", Title: "Returning Read An Hour Ago", Monitored: true},
		{TMDBID: 4, Kind: "series", Title: "Returning Read A Day Ago", Monitored: true},
		{TMDBID: 5, Kind: "series", Title: "Ended Read A Day Ago", Monitored: true},
		{TMDBID: 6, Kind: "series", Title: "Ended Read Two Weeks Ago", Monitored: true},
		{TMDBID: 7, Kind: "series", Title: "Ended Read A Day Ago With A New Episode", Monitored: true},
		{TMDBID: 8, Kind: "series", Title: "Not Monitored", Monitored: false},
	} {
		if err := s.UpsertTitle(ctx, ti); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := s.FollowTitle(ctx, Title{TMDBID: 9, Kind: "series", Title: "Followed Never Read"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `
		UPDATE titles SET tmdb_status = 'Returning Series', inventory_at = now() - interval '1 hour' WHERE tmdb_id = 3;
		UPDATE titles SET tmdb_status = 'Returning Series', inventory_at = now() - interval '1 day' WHERE tmdb_id = 4;
		UPDATE titles SET tmdb_status = 'Ended', inventory_at = now() - interval '1 day' WHERE tmdb_id IN (5, 7);
		UPDATE titles SET tmdb_status = 'Ended', inventory_at = now() - interval '14 days' WHERE tmdb_id = 6;`); err != nil {
		t.Fatal(err)
	}
	season, ep := 1, 1
	if err := s.UpsertTarget(ctx, Target{ID: TargetID("series:7", &season, &ep), TitleID: "series:7", Kind: "episode",
		SeasonNumber: &season, EpisodeNumber: &ep, Monitored: true, State: TargetWanted}); err != nil {
		t.Fatal(err)
	}
	_ = s.SetAirWindow(ctx, TargetID("series:7", &season, &ep), day(3), 0)
	due, err := s.SeriesInventoryDue(ctx, 5*time.Hour+30*time.Minute, 7*24*time.Hour, 50, SeriesAll)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, d := range due {
		got = append(got, d.Title.Title)
	}
	want := []string{"Followed Never Read", "Continuing Never Read", "Returning Read A Day Ago",
		"Ended Read A Day Ago With A New Episode", "Ended Never Read", "Ended Read Two Weeks Ago"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("due = %q\nwant  %q", got, want)
	}
	if lim, _ := s.SeriesInventoryDue(ctx, time.Hour, time.Hour, 2, SeriesAll); len(lim) != 2 {
		t.Errorf("limit: %d", len(lim))
	}
}

func TestWhichMoviesAreReadAgain(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	waiting, _, _ := s.FollowTitle(ctx, Title{TMDBID: 1, Kind: "movie", Title: "Waiting"})
	out, _, _ := s.FollowTitle(ctx, Title{TMDBID: 2, Kind: "movie", Title: "Out"})
	never, _, _ := s.FollowTitle(ctx, Title{TMDBID: 3, Kind: "movie", Title: "Never Read"})
	if err := s.UpsertTitle(ctx, Title{TMDBID: 4, Kind: "movie", Title: "Imported", Monitored: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplyMovieInventory(ctx, waiting.ID, MovieRead{Window: day(30), Kind: "digital"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplyMovieInventory(ctx, out.ID, MovieRead{Window: day(-30), Kind: "digital"}); err != nil {
		t.Fatal(err)
	}
	// In cinemas long ago, TMDB dating no release to watch at home: its
	// window is a guess, and it is read again until TMDB dates one.
	guess, _, _ := s.FollowTitle(ctx, Title{TMDBID: 5, Kind: "movie", Title: "Guessed"})
	if _, err := s.ApplyMovieInventory(ctx, guess.ID, MovieRead{Window: day(-10), Released: day(-100), Kind: "theatrical"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE titles SET inventory_at = now() - interval '2 days' WHERE inventory_at IS NOT NULL`); err != nil {
		t.Fatal(err)
	}
	due, err := s.MovieInventoryDue(ctx, 20*time.Hour, 10)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, d := range due {
		got = append(got, d.ID)
	}
	sort.Strings(got)
	want := []string{guess.ID, never.ID, waiting.ID}
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("due = %v, want %v", got, want)
	}
}

func TestSeasonsToRead(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	ti, _, _ := s.FollowTitle(ctx, Title{TMDBID: 5, Kind: "series", Title: "Long Show"})
	var eps []EpisodeRead
	for season := 1; season <= 5; season++ {
		eps = append(eps, EpisodeRead{Season: season, Episode: 1, AirDate: day(-400 + 30*season)})
	}
	eps = append(eps, EpisodeRead{Season: 3, Episode: 2, AirDate: day(-10)})
	if _, err := s.ApplySeriesInventory(ctx, ti.ID, SeriesRead{Seasons: map[int]int{1: 1, 2: 1, 3: 2, 4: 1, 5: 1},
		Read: []int{1, 2, 3, 4, 5}, Episodes: eps}); err != nil {
		t.Fatal(err)
	}
	listed := map[int]int{1: 1, 2: 3, 3: 2, 4: 1, 5: 1, 6: 4}
	got, err := s.SeasonsToRead(ctx, ti.ID, listed, false)
	if err != nil {
		t.Fatal(err)
	}
	// 2: its count changed; 3: an episode aired ten days ago; 6: new, and the last.
	if want := []int{2, 3, 6}; !reflect.DeepEqual(got, want) {
		t.Errorf("seasons to read = %v, want %v", got, want)
	}
	if all, _ := s.SeasonsToRead(ctx, ti.ID, listed, true); len(all) != 6 {
		t.Errorf("a full read reads %v", all)
	}
}

// What a request shows of the series it follows, and of the movie.
func TestTitleProgress(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	ti, _, _ := s.FollowTitle(ctx, Title{TMDBID: 990001, Kind: "series", Title: "Example"})
	if _, err := s.ApplySeriesInventory(ctx, ti.ID, SeriesRead{Status: "Returning Series", Running: true,
		Seasons: map[int]int{1: 5}, Read: []int{1},
		Episodes: episodesOf(1, day(-9), day(-9), day(-2), day(5), day(12)), MonitorAll: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplySeriesHoldings(ctx, ti.ID, "", "default", []HeldEpisode{{Season: 1, Episode: 1, ItemID: "i1", Playable: true}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetTargetState(ctx, ti.ID+":s01e002", nil, TargetGrabbed, "grabbed"); err != nil {
		t.Fatal(err)
	}
	m, _, _ := s.FollowTitle(ctx, Title{TMDBID: 990100, Kind: "movie", Title: "Film"})
	if _, err := s.ApplyMovieInventory(ctx, m.ID, MovieRead{Window: day(20), Kind: "digital"}); err != nil {
		t.Fatal(err)
	}
	got, err := s.TitleProgress(ctx, []string{ti.ID, m.ID, "series:404"})
	if err != nil {
		t.Fatal(err)
	}
	p := got[ti.ID]
	if p.Kind != "series" || !p.Running || p.Episodes != 5 || p.Held != 1 || p.Playable != 1 || p.OnTheWay != 1 ||
		p.Missing != 1 || p.Unaired != 2 || !p.HoldingsKnown || p.Next == nil || p.Next.Episode != 4 || p.Next.Title != "Chapter D" {
		t.Errorf("a series: %+v %+v", p, p.Next)
	}
	mp := got[m.ID]
	if mp.Kind != "movie" || mp.Movie == nil || mp.Movie.ReleaseKind != "digital" || mp.HoldingsKnown {
		t.Errorf("a movie: %+v", mp)
	}
	if _, ok := got["series:404"]; ok {
		t.Error("a title nobody tracks has progress")
	}
}

// The new things come first in the sweep when search.autoGrabNew is on:
// what aired in the last days, what a follow added, a requested movie.
func TestNewThingsFirst(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	// The old backlog: an imported series' episode, aired long ago, never searched.
	if err := s.UpsertTitle(ctx, Title{TMDBID: 1, Kind: "series", Title: "Old", Monitored: true}); err != nil {
		t.Fatal(err)
	}
	season, ep, recent := 1, 1, 2
	old, fresh := TargetID("series:1", &season, &ep), TargetID("series:1", &season, &recent)
	for _, id := range []string{old, fresh} {
		e := ep
		if id == fresh {
			e = recent
		}
		if err := s.UpsertTarget(ctx, Target{ID: id, TitleID: "series:1", Kind: "episode", SeasonNumber: &season,
			EpisodeNumber: &e, Monitored: true, State: TargetWanted}); err != nil {
			t.Fatal(err)
		}
	}
	_ = s.SetAirWindow(ctx, old, day(-300), 0)
	_ = s.SetAirWindow(ctx, fresh, day(-3), 0)
	// A followed series' episode from long ago, added by its follow.
	f := approvedFollow(t, s, Title{TMDBID: 2, Kind: "series", Title: "Followed"})
	if _, err := s.ApplySeriesInventory(ctx, f.ID, SeriesRead{Seasons: map[int]int{1: 1}, Read: []int{1},
		Episodes: episodesOf(1, day(-500)), MonitorAll: true}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"series:1", f.ID} {
		if _, err := s.ApplySeriesHoldings(ctx, id, "", "default", nil); err != nil {
			t.Fatal(err)
		}
	}
	// The old one was searched least recently: first in the plain order.
	_ = s.RecordSearch(ctx, fresh, SearchRecord{Outcome: SearchNone, Base: time.Millisecond, Max: time.Millisecond})
	_ = s.RecordSearch(ctx, f.ID+":s01e001", SearchRecord{Outcome: SearchNone, Base: time.Millisecond, Max: time.Millisecond})
	time.Sleep(10 * time.Millisecond)

	ids := func(ts []Target) (out []string) {
		for _, x := range ts {
			out = append(out, x.ID)
		}
		return out
	}
	plain, _ := s.DueTargets(ctx, 10)
	if got := ids(plain); len(got) != 3 || got[0] != old {
		t.Errorf("plain order = %v, want the one never searched first", got)
	}
	first, _ := s.DueTargetsBy(ctx, 2, DueOrder{NewWithinDays: 14})
	if got := ids(first); len(got) != 2 || got[0] == old || got[1] == old {
		t.Errorf("new first = %v, want the two new ones before the old backlog", got)
	}
	retry, _ := s.RetryableTargetsBy(ctx, 10, DueOrder{NewWithinDays: 14})
	if len(retry) != 2 {
		t.Errorf("retryable = %v", ids(retry))
	}
	eps, movies, err := s.WantedForFeeds(ctx, FeedsWant{NewWithinDays: 14})
	if err != nil || eps != 2 || movies != 0 {
		t.Errorf("new wants for the feeds: %d episodes, %d movies, %v", eps, movies, err)
	}
	if eps, _, _ := s.WantedForFeeds(ctx, FeedsWant{All: true, NewWithinDays: 14}); eps != 3 {
		t.Errorf("all wants for the feeds: %d", eps)
	}
	if eps, _, _ := s.WantedForFeeds(ctx, FeedsWant{NewWithinDays: 1}); eps != 1 {
		t.Errorf("new within a day: %d, want only the followed one", eps)
	}

	// What aired recently comes before what a request asked for, even
	// never searched: yesterday's episode before a requested back catalogue.
	_ = s.RecordSearch(ctx, fresh, SearchRecord{Outcome: SearchNone, Base: time.Millisecond, Max: time.Millisecond})
	_, _ = s.pool.Exec(ctx, `UPDATE acquisition_targets SET last_search_at = NULL WHERE id = $1`, f.ID+":s01e001")
	time.Sleep(10 * time.Millisecond)
	if got := ids(func() []Target { d, _ := s.DueTargetsBy(ctx, 3, DueOrder{NewWithinDays: 14}); return d }()); len(got) != 3 ||
		got[0] != fresh || got[1] != f.ID+":s01e001" || got[2] != old {
		t.Errorf("new first = %v, want what aired recently, then what was requested, then the old backlog", got)
	}

	// Held once, and lost: not new any more.
	if _, err := s.ApplySeriesHoldings(ctx, "series:1", "", "default", []HeldEpisode{{Season: 1, Episode: 2, ItemID: "item-2"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE acquisition_targets SET handed_over_at = NULL WHERE id = $1`, fresh); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplySeriesHoldings(ctx, "series:1", "", "default", nil); err != nil {
		t.Fatal(err)
	}
	if x, _ := s.GetTarget(ctx, fresh); x.State != TargetWanted || !x.EverHeld {
		t.Fatalf("held, then lost: %+v", x)
	}
	if eps, _, _ := s.WantedForFeeds(ctx, FeedsWant{NewWithinDays: 14}); eps != 1 {
		t.Errorf("new wants once the recent one was held and lost: %d, want the followed one", eps)
	}
}

// An automatic grab waits the air delay after an air date — 00:00 UTC of the
// day TMDB dates — and a requested movie's after the day it came out to
// watch at home; one whose window is set from its theatrical release waits
// for that window.
func TestTheAirDelay(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	if err := s.UpsertTitle(ctx, Title{TMDBID: 1, Kind: "series", Title: "Show", Monitored: true}); err != nil {
		t.Fatal(err)
	}
	season := 1
	for e, d := range map[int]int{1: 0, 2: -2} {
		e := e
		id := TargetID("series:1", &season, &e)
		if err := s.UpsertTarget(ctx, Target{ID: id, TitleID: "series:1", Kind: "episode", SeasonNumber: &season,
			EpisodeNumber: &e, Monitored: true, State: TargetWanted}); err != nil {
			t.Fatal(err)
		}
		_ = s.SetAirWindow(ctx, id, day(d), 0)
	}
	for id, w := range map[int64]MovieRead{
		2: {Window: day(-2), Released: day(-2), Kind: "digital"},     // out two days ago
		3: {Window: day(0), Released: day(0), Kind: "digital"},       // out today
		4: {Window: day(-1), Released: day(-91), Kind: "theatrical"}, // 90 days after cinemas
	} {
		f := approvedFollow(t, s, Title{TMDBID: id, Kind: "movie", Title: "Film"})
		if _, err := s.ApplyMovieInventory(ctx, f.ID, w); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"series:1", "movie:2", "movie:3", "movie:4"} {
		if _, err := s.ApplySeriesHoldings(ctx, id, "", "default", nil); err != nil {
			t.Fatal(err)
		}
	}
	due := func(delay time.Duration) string {
		ts, err := s.DueTargetsBy(ctx, 10, DueOrder{AirDelay: delay})
		if err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, x := range ts {
			ids = append(ids, x.ID)
		}
		sort.Strings(ids)
		return strings.Join(ids, " ")
	}
	if got := due(0); got != "movie:2:movie movie:3:movie movie:4:movie series:1:s01e001 series:1:s01e002" {
		t.Errorf("without a delay: %s", got)
	}
	if got := due(30 * time.Hour); got != "movie:2:movie movie:4:movie series:1:s01e002" {
		t.Errorf("30 hours after the air date: %s", got)
	}
	// The movie in cinemas only is searched, but no feed is read for it: no
	// automatic grab takes it.
	if eps, movies, _ := s.WantedForFeeds(ctx, FeedsWant{All: true, DueOrder: DueOrder{AirDelay: 30 * time.Hour}}); eps != 1 || movies != 1 {
		t.Errorf("the feeds 30 hours after the air date: %d episodes, %d movies", eps, movies)
	}
	m, _ := s.TargetAt(ctx, "movie:4", nil, nil)
	if m.AirDate == nil || !m.AirDate.Equal(*day(-91)) || m.WindowAt == nil || !m.WindowAt.Equal(*day(-1)) {
		t.Errorf("a movie searched 90 days after cinemas: air date %v, window %v", m.AirDate, m.WindowAt)
	}
}

// A feed's cursor keeps the releases looked at by their guid, however the
// source dates them, the last thousand of the last seven days by the
// source's own clock.
func TestRSSCursors(t *testing.T) {
	at := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	var c RSSCursor
	if c.Read() || c.Has("a") {
		t.Error("a feed never read has seen something")
	}
	c.Add("a", at)
	c.Add("b", at.Add(time.Hour)) // a source whose clock runs ahead
	c.Add("old", at.Add(-8*24*time.Hour))
	if !c.Has("a") || !c.Has("b") || c.Has("c") || !c.Read() {
		t.Errorf("the releases looked at: %+v", c)
	}
	ids, _ := c.kept()
	if len(ids) != 2 {
		t.Errorf("kept %d, want the two of the last week by the source's clock", len(ids))
	}
	for i := 0; i < RSSSeenMax+10; i++ {
		c.Add(fmt.Sprintf("n%d", i), at.Add(time.Duration(i)*time.Second))
	}
	if ids, at := c.kept(); len(ids) != RSSSeenMax || len(at) != RSSSeenMax || at[0] < at[len(at)-1] {
		t.Errorf("kept %d, want the newest %d first", len(ids), RSSSeenMax)
	}

	s := migrated(t)
	ctx := context.Background()
	got, err := s.GetRSSCursor(ctx, 7, "tv")
	if err != nil || got.Read() || got.IndexerID != 7 || got.Feed != "tv" {
		t.Fatalf("a feed never read: %+v %v", got, err)
	}
	c = RSSCursor{IndexerID: 7, Feed: "tv", Detail: "100 read"}
	c.Add("a", at)
	c.Add("b", at.Add(time.Hour))
	if err := s.SaveRSSCursor(ctx, c); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetRSSCursor(ctx, 7, "tv")
	if !got.Has("a") || !got.Has("b") || got.Has("c") || got.ReadAt == nil || got.Detail != "100 read" ||
		got.Seen[RSSID("b")] != at.Add(time.Hour).Unix() {
		t.Errorf("round trip: %+v", got)
	}
}
