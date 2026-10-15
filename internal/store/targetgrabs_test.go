package store

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"
)

// series makes a monitored series with aired episodes 1..n of season 1 and
// returns its title id and the target ids in episode order.
func series(t *testing.T, s *Store, tmdb int64, n int) (string, []string) {
	t.Helper()
	ctx := context.Background()
	if err := s.UpsertTitle(ctx, Title{TMDBID: tmdb, TVDBID: tmdb + 100000, Kind: "series", Title: fmt.Sprintf("Show %d", tmdb),
		Monitored: true, MonitorNew: true, SeriesType: "standard"}); err != nil {
		t.Fatal(err)
	}
	id := TitleID("series", tmdb)
	var ids []string
	aired := time.Now().Add(-48 * time.Hour)
	for e := 1; e <= n; e++ {
		season, ep := 1, e
		tid := TargetID(id, &season, &ep)
		if err := s.UpsertTarget(ctx, Target{ID: tid, TitleID: id, Kind: "episode",
			SeasonNumber: &season, EpisodeNumber: &ep, Monitored: true, State: TargetWanted}); err != nil {
			t.Fatal(err)
		}
		if err := s.SetAirWindow(ctx, tid, &aired, 0); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, tid)
	}
	return id, ids
}

func stateOf(t *testing.T, s *Store, id string) Target {
	t.Helper()
	tg, err := s.GetTarget(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return tg
}

// A title whose holdings acquire does not know is missing nothing it can tell:
// none of its episodes is listed, counted as missing or due — they may all be
// on disk. Counts says how much is left out.
func TestUnknownHoldingsAreNeitherMissingNorDue(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	known, _ := series(t, s, 1, 2)
	_, _ = series(t, s, 2, 3)
	knownHoldings(t, s, known)

	miss, err := s.Missing(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(miss) != 2 || miss[0].TitleID != known {
		t.Fatalf("missing = %+v, want only the two episodes of the series whose holdings are known", miss)
	}
	due, _ := s.DueTargets(ctx, 100)
	if len(due) != 2 {
		t.Fatalf("due = %d, want 2", len(due))
	}
	c, err := s.Counts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if c.Missing != 2 || c.HoldingsUnknown != 1 || c.UnknownTargets != 3 {
		t.Errorf("counts = %+v, want missing 2, one series of unknown holdings with 3 episodes left out", c)
	}
	rows, _ := s.SeriesOverview(ctx)
	for _, r := range rows {
		if r.TitleID != known && (r.Missing != 0 || r.HoldingsKnown) {
			t.Errorf("a series of unknown holdings reads %+v", r)
		}
	}
	// Learning that the holdings cannot be known takes the title out again.
	if _, err := s.HoldingsUnknown(ctx, []string{known}, "the catalog does not answer holdings"); err != nil {
		t.Fatal(err)
	}
	if miss, _ := s.Missing(ctx, 100); len(miss) != 0 {
		t.Errorf("missing after holdings became unknown: %+v", miss)
	}
	ti, _ := s.TitleByID(ctx, known)
	if ti.HoldingsKnown() || ti.HoldingsError != "the catalog does not answer holdings" {
		t.Errorf("title: %+v", ti)
	}
}

// Every search records its outcome and a next due time, so the sweep works
// through the whole backlog instead of returning the same first targets: a
// find comes back after its recheck, a miss backs off, and a target never
// searched always goes first.
func TestTheSweepRotatesThroughTheBacklog(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	id, all := series(t, s, 3, 15)
	knownHoldings(t, s, id)

	first, err := s.DueTargets(ctx, 10)
	if err != nil || len(first) != 10 {
		t.Fatalf("first batch: %d %v", len(first), err)
	}
	for i, tg := range first {
		r := SearchRecord{Outcome: SearchFound, Detail: "3 candidates", Again: 12 * time.Hour}
		if i%2 == 1 {
			r = SearchRecord{Outcome: SearchNone, Detail: "nothing usable", Base: 30 * time.Minute, Max: 24 * time.Hour}
		}
		if err := s.RecordSearch(ctx, tg.ID, r); err != nil {
			t.Fatal(err)
		}
	}
	second, _ := s.DueTargets(ctx, 10)
	if len(second) != 5 {
		t.Fatalf("second batch = %d, want the 5 never searched", len(second))
	}
	seen := map[string]bool{}
	for _, tg := range first {
		seen[tg.ID] = true
	}
	for _, tg := range second {
		if seen[tg.ID] {
			t.Errorf("%s came back although it was just searched", tg.ID)
		}
	}
	found, none := stateOf(t, s, first[0].ID), stateOf(t, s, first[1].ID)
	if found.SearchOutcome != SearchFound || found.SearchFailures != 0 || found.NextSearchAt == nil ||
		time.Until(*found.NextSearchAt) < 11*time.Hour || found.LastSearchAt == nil {
		t.Errorf("a find: %+v", found)
	}
	if none.SearchOutcome != SearchNone || none.SearchFailures != 1 || none.SearchDetail != "nothing usable" ||
		time.Until(*none.NextSearchAt) < 25*time.Minute {
		t.Errorf("a miss: %+v", none)
	}
	// Once every target has had its turn, the one searched longest ago is
	// next, whatever its outcome was.
	for _, tg := range second {
		_ = s.RecordSearch(ctx, tg.ID, SearchRecord{Outcome: SearchError, Detail: "source down", Base: time.Minute, Max: time.Hour})
	}
	_, _ = s.pool.Exec(ctx, `UPDATE acquisition_targets SET search_backoff_until = now() - interval '1 second'`)
	_, _ = s.pool.Exec(ctx, `UPDATE acquisition_targets SET last_search_at = now() - interval '1 day' WHERE id = $1`, all[14])
	third, _ := s.DueTargets(ctx, 1)
	if len(third) != 1 || third[0].ID != all[14] {
		t.Errorf("after a full turn the oldest search is next: %+v", third)
	}
}

// A grab holds the targets it covers from its reservation on: a second grab
// for any of them is refused until the first is released, and binding it to
// its client job moves them to grabbed.
func TestATargetGrabHoldsItsTargets(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	id, eps := series(t, s, 4, 4)
	held := eps[2]
	if err := s.HoldTarget(ctx, Hold{TargetID: held, ItemID: "item-3", Source: "derived", Detail: "in the library"}); err != nil {
		t.Fatal(err)
	}

	g, err := s.ReserveTargetGrab(ctx, TargetGrab{ID: "tg_1", TitleID: id, ReleaseTitle: "Show.S01.1080p", Covers: "S01", Via: "pick"},
		eps[0], eps, 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(g.TargetIDs)
	if want := []string{eps[0], eps[1], eps[3]}; strings.Join(g.TargetIDs, ",") != strings.Join(want, ",") || g.State != GrabPending {
		t.Fatalf("reserved %v (%s), want the three wanted episodes, not the held one", g.TargetIDs, g.State)
	}
	if st := stateOf(t, s, eps[0]); st.State != TargetWanted || st.GrabID != "tg_1" {
		t.Fatalf("a reserved target is still wanted until the hand-over: %+v", st)
	}

	_, err = s.ReserveTargetGrab(ctx, TargetGrab{ID: "tg_2", TitleID: id}, eps[1], eps[1:2], 15*time.Minute)
	var ng *NotGrabbableError
	if !errors.As(err, &ng) || !ng.Busy {
		t.Fatalf("a second grab of a reserved target: %v", err)
	}
	_, err = s.ReserveTargetGrab(ctx, TargetGrab{ID: "tg_3", TitleID: id}, held, eps[2:3], 15*time.Minute)
	if !errors.As(err, &ng) || ng.State != TargetHeld || ng.Error() != "the library holds it already" {
		t.Fatalf("a grab of a held target: %v", err)
	}

	// A pending reservation older than its TTL holds nothing: the hand-over
	// it was for never finished.
	_, _ = s.pool.Exec(ctx, `UPDATE target_grabs SET created_at = now() - interval '1 hour' WHERE id = 'tg_1'`)
	stale, err := s.ReserveTargetGrab(ctx, TargetGrab{ID: "tg_4", TitleID: id}, eps[1], eps[1:2], 15*time.Minute)
	if err != nil || len(stale.TargetIDs) != 1 {
		t.Fatalf("a stale reservation still held its targets: %+v %v", stale, err)
	}
	if err := s.ReleaseTargetGrab(ctx, "tg_4"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetTargetGrab(ctx, "tg_4"); !errors.Is(err, ErrNotFound) {
		t.Errorf("a released reservation's row is still there: %v", err)
	}
	if st := stateOf(t, s, eps[1]); st.GrabID != "" || st.State != TargetWanted {
		t.Errorf("released: %+v", st)
	}

	bound, err := s.BindTargetGrab(ctx, TargetGrab{ID: "tg_1", Adapter: "nzbget", ClientJobID: "101", Protocol: "usenet",
		Source: "https://idx.test/get/1"}, "grabbed NZB S01 from example via nzbget")
	if err != nil {
		t.Fatal(err)
	}
	if bound.State != GrabGrabbed || bound.Adapter != "nzbget" || bound.ClientJobID != "101" {
		t.Fatalf("bound: %+v", bound)
	}
	for _, e := range []string{eps[0], eps[3]} {
		if st := stateOf(t, s, e); st.State != TargetGrabbed || st.Detail != "grabbed NZB S01 from example via nzbget" {
			t.Errorf("%s after binding: %+v", e, st)
		}
	}
	// eps[1] was taken by the stale reservation meanwhile and released: it is
	// no longer the grab's, so binding leaves it alone.
	if st := stateOf(t, s, eps[1]); st.State != TargetWanted {
		t.Errorf("a target the grab lost was moved: %+v", st)
	}
	byJob, err := s.FindTargetGrabByJob(ctx, "", "101")
	if err != nil || byJob.ID != "tg_1" {
		t.Fatalf("by job: %+v %v", byJob, err)
	}
	files, _ := s.GrabFiles(ctx, "tg_1")
	if len(files) != 3 || files[0].State != "expected" {
		t.Errorf("grab_targets rows: %+v", files)
	}

	// The download finishing and failing move only the grab's own targets.
	if n, _ := s.MoveGrabTargets(ctx, "tg_1", []string{TargetGrabbed}, TargetFailed, "download failed: par repair"); n != 2 {
		t.Errorf("moved %d targets, want 2", n)
	}
	if st := stateOf(t, s, eps[3]); st.State != TargetFailed || !Grabbable(st.State) {
		t.Errorf("failed: %+v", st)
	}
}

// What the library holds moves targets both ways: an episode it holds is held
// (by the item named, an episode covered by another's file through that one),
// an episode it no longer holds is wanted again, and one whose grab is under
// way is left to its grab unless the library holds it already.
func TestSeriesHoldingsApplyBothWays(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	id, eps := series(t, s, 5, 5)
	if ok, _ := s.SetTargetState(ctx, eps[3], nil, TargetGrabbed, "grabbed"); !ok {
		t.Fatal("could not move")
	}
	if ok, _ := s.SetTargetState(ctx, eps[4], nil, TargetDownloaded, "downloaded"); !ok {
		t.Fatal("could not move")
	}
	ch, err := s.ApplySeriesHoldings(ctx, id, "series-1", "default", []HeldEpisode{
		{Season: 1, Episode: 1, ItemID: "e1", Playable: true},
		{Season: 1, Episode: 2, ItemID: "e2"},
		{Season: 1, Episode: 3, ItemID: "e3", Via: "e2"},
		{Season: 1, Episode: 5, ItemID: "e5"},
		{Season: 2, Episode: 1, ItemID: "untracked"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if ch.Held != 4 || ch.Released != 0 {
		t.Fatalf("change = %+v, want 4 held", ch)
	}
	one, three, four := stateOf(t, s, eps[0]), stateOf(t, s, eps[2]), stateOf(t, s, eps[3])
	if one.State != TargetHeld || one.HeldItemID != "e1" || !one.HeldPlayable || one.HeldSource != "derived" || one.Detail != "in the library" {
		t.Errorf("held: %+v", one)
	}
	if three.HeldItemID != "e3" || three.HeldVia != "e2" {
		t.Errorf("covered: %+v", three)
	}
	if four.State != TargetGrabbed {
		t.Errorf("an episode in flight that the library does not hold was touched: %+v", four)
	}
	if five := stateOf(t, s, eps[4]); five.State != TargetHeld {
		t.Errorf("an episode in flight that the library holds already: %+v", five)
	}
	ti, _ := s.TitleByID(ctx, id)
	if !ti.HoldingsKnown() || ti.CatalogID != "series-1" {
		t.Errorf("title after applying: %+v", ti)
	}

	// The same answer again changes nothing.
	again, _ := s.ApplySeriesHoldings(ctx, id, "series-1", "default", []HeldEpisode{
		{Season: 1, Episode: 1, ItemID: "e1", Playable: true}, {Season: 1, Episode: 2, ItemID: "e2"},
		{Season: 1, Episode: 3, ItemID: "e3", Via: "e2"}, {Season: 1, Episode: 5, ItemID: "e5"},
	})
	if again.Held != 0 || again.Released != 0 {
		t.Errorf("an unchanged answer moved %+v", again)
	}

	// The pipeline packaging the item holding a file makes the episodes it
	// covers playable too.
	if n, _ := s.MarkItemPlayable(ctx, "e2"); n != 2 {
		t.Errorf("packaged e2 made %d targets playable, want 2 (its own and the one it covers)", n)
	}
	// The library losing that file takes the covered episode with it.
	if n, _ := s.ClearHolding(ctx, "e2"); n != 2 {
		t.Errorf("removing e2 released %d targets, want 2", n)
	}
	if two := stateOf(t, s, eps[1]); two.State != TargetWanted || two.HeldItemID != "" || two.Detail != "no longer in the library; wanted again" {
		t.Errorf("after removal: %+v", two)
	}

	// An episode missing from the next answer is wanted again; of a series
	// that is no longer monitored, it is not hunted.
	_, _ = s.pool.Exec(ctx, `UPDATE titles SET monitored = false WHERE id = $1`, id)
	ch, _ = s.ApplySeriesHoldings(ctx, id, "series-1", "default", []HeldEpisode{{Season: 1, Episode: 1, ItemID: "e1", Playable: true}})
	if ch.Released != 1 {
		t.Errorf("released = %d, want 1 (episode 5)", ch.Released)
	}
	if five := stateOf(t, s, eps[4]); five.State != TargetWanted || five.Detail != "no longer in the library; not monitored, so not hunted" {
		t.Errorf("released from an unmonitored series: %+v", five)
	}
}

// A movie the library holds gets its target if it had none, and loses its
// holding again when the library does.
func TestAMovieHoldingMakesItsTarget(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	if err := s.UpsertTitle(ctx, Title{TMDBID: 603, Kind: "movie", Title: "The Matrix", IMDBID: "tt0133093", Monitored: true}); err != nil {
		t.Fatal(err)
	}
	id := TitleID("movie", 603)
	ch, err := s.ApplyMovieHolding(ctx, id, "m-1", "default", &HeldEpisode{ItemID: "m-1", Playable: true})
	if err != nil || ch.Held != 1 {
		t.Fatalf("hold: %+v %v", ch, err)
	}
	tg, err := s.TargetAt(ctx, id, nil, nil)
	if err != nil || tg.State != TargetHeld || tg.HeldItemID != "m-1" || !tg.HeldPlayable || tg.Kind != "movie" {
		t.Fatalf("movie target: %+v %v", tg, err)
	}
	ch, _ = s.ApplyMovieHolding(ctx, id, "", "default", nil)
	if ch.Released != 1 {
		t.Errorf("not held any more: %+v", ch)
	}
	if tg, _ := s.TargetAt(ctx, id, nil, nil); tg.State != TargetWanted {
		t.Errorf("after the library lost it: %+v", tg)
	}
}

func TestTitleByIdentity(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	_ = s.UpsertTitle(ctx, Title{TMDBID: 1396, TVDBID: 81189, IMDBID: "tt0903747", Kind: "series", Title: "Breaking Bad"})
	_ = s.UpsertTitle(ctx, Title{TMDBID: 1396, Kind: "movie", Title: "Not The Series"})
	for _, c := range []struct {
		tmdb, tvdb int64
		imdb       string
	}{{1396, 0, ""}, {0, 81189, ""}, {0, 0, "tt0903747"}, {999, 81189, ""}} {
		got, err := s.TitleByIdentity(ctx, "series", c.tmdb, c.tvdb, c.imdb)
		if err != nil || got.Title != "Breaking Bad" {
			t.Errorf("%+v: %+v %v", c, got, err)
		}
	}
	if _, err := s.TitleByIdentity(ctx, "series", 1, 2, "tt1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("an unknown identity: %v", err)
	}
	if got, _ := s.TitleByIdentity(ctx, "movie", 1396, 0, ""); got.Title != "Not The Series" {
		t.Errorf("the kind is part of the identity: %+v", got)
	}
}

// Grabs an admin asks for wait in a queue the worker takes one at a time:
// only targets that can be grabbed are queued, a claimed one is not handed
// out twice, a deferred one comes back after its wait.
func TestRequestedGrabsQueue(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	_, eps := series(t, s, 6, 3)
	_ = s.HoldTarget(ctx, Hold{TargetID: eps[2], ItemID: "x"})
	queued, err := s.RequestTargetGrabs(ctx, append(eps, "no-such-target"))
	if err != nil || len(queued) != 2 {
		t.Fatalf("queued %v %v, want the two wanted episodes", queued, err)
	}
	if st := stateOf(t, s, eps[0]); st.Detail != "queued for a search and grab" {
		t.Errorf("queued target says %q", st.Detail)
	}
	a, ok, err := s.ClaimTargetGrab(ctx, time.Minute)
	if err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	b, ok, _ := s.ClaimTargetGrab(ctx, time.Minute)
	if !ok || a == b {
		t.Fatalf("claims %q and %q", a, b)
	}
	if _, ok, _ := s.ClaimTargetGrab(ctx, time.Minute); ok {
		t.Fatal("a claimed request was handed out twice")
	}
	if err := s.DeferTargetGrabRequest(ctx, a, 0, "waiting for a free download slot"); err != nil {
		t.Fatal(err)
	}
	if again, ok, _ := s.ClaimTargetGrab(ctx, time.Minute); !ok || again != a {
		t.Fatalf("a deferred request did not come back: %q %v", again, ok)
	}
	_ = s.FinishTargetGrabRequest(ctx, a)
	_ = s.FinishTargetGrabRequest(ctx, b)
	_, _ = s.pool.Exec(ctx, `UPDATE acquisition_targets SET grab_claimed_until = NULL`)
	if _, ok, _ := s.ClaimTargetGrab(ctx, time.Minute); ok {
		t.Error("a finished request is still queued")
	}
}

// A target grab handed over counts as a download in flight until the
// gateway's events make it one, so a run of grabs cannot overshoot the limit.
func TestGrabsInFlightUntilTheDownloadShows(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	id, eps := series(t, s, 7, 2)
	g, err := s.ReserveTargetGrab(ctx, TargetGrab{ID: "tg_a", TitleID: id}, eps[0], eps[:1], time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := s.ActiveDownloads(ctx); n != 1 {
		t.Fatalf("a reservation counts: %d", n)
	}
	g.Adapter, g.ClientJobID = "nzbget", "7"
	if _, err := s.BindTargetGrab(ctx, g, "grabbed"); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.ActiveDownloads(ctx); n != 1 {
		t.Fatalf("a grab not yet reported: %d", n)
	}
	if _, err := s.UpsertDownload(ctx, Download{Adapter: "nzbget", ClientJobID: "7", WantedID: "tg_a", State: "downloading"}); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.ActiveDownloads(ctx); n != 1 {
		t.Fatalf("a reported grab is counted twice: %d", n)
	}
}
