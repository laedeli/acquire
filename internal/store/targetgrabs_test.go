package store

import (
	"context"
	"errors"
	"fmt"
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
	if h, err := s.HoldingsSummary(ctx); err != nil || h.Known != 0 || h.Unknown != 2 || h.Reason != "the catalog does not answer holdings" {
		t.Errorf("summary: %+v %v", h, err)
	}
	knownHoldings(t, s, known)
	if h, _ := s.HoldingsSummary(ctx); h.Known != 1 || h.Unknown != 1 || h.Reason != "" || h.LastAt == nil {
		t.Errorf("summary with one known: %+v", h)
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
// for any of them is refused until the first is bound or released — there is
// no timeout after which a reservation lapses, since its hand-over may have
// reached the gateway — and binding it to its client job moves them to
// grabbed.
func TestATargetGrabHoldsItsTargets(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	id, eps := series(t, s, 4, 4)
	held := eps[2]
	if err := s.HoldTarget(ctx, Hold{TargetID: held, ItemID: "item-3", Source: "derived", Detail: "in the library"}); err != nil {
		t.Fatal(err)
	}

	g, err := s.ReserveTargetGrab(ctx, TargetGrab{ID: "tg_1", TitleID: id, ReleaseTitle: "Show.S01.1080p", Covers: "S01", Via: "pick"},
		eps[0], eps)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{eps[0], eps[1], eps[3]}; strings.Join(g.TargetIDs, ",") != strings.Join(want, ",") || g.State != GrabPending {
		t.Fatalf("reserved %v (%s), want the three wanted episodes, not the held one", g.TargetIDs, g.State)
	}
	if st := stateOf(t, s, eps[0]); st.State != TargetWanted || st.ReservedBy != "tg_1" || st.GrabID != "" {
		t.Fatalf("a reserved target is still wanted until the hand-over: %+v", st)
	}

	_, err = s.ReserveTargetGrab(ctx, TargetGrab{ID: "tg_2", TitleID: id}, eps[1], eps[1:2])
	var ng *NotGrabbableError
	if !errors.As(err, &ng) || !ng.Busy {
		t.Fatalf("a second grab of a reserved target: %v", err)
	}
	_, err = s.ReserveTargetGrab(ctx, TargetGrab{ID: "tg_3", TitleID: id}, held, eps[2:3])
	if !errors.As(err, &ng) || ng.State != TargetHeld || ng.Error() != "the library holds it already" {
		t.Fatalf("a grab of a held target: %v", err)
	}
	// An hour on, the reservation still holds: whether its hand-over reached
	// the gateway is for the job's events, or the stale-grab sweep, to say.
	_, _ = s.pool.Exec(ctx, `UPDATE target_grabs SET created_at = now() - interval '1 hour' WHERE id = 'tg_1'`)
	if _, err := s.ReserveTargetGrab(ctx, TargetGrab{ID: "tg_4", TitleID: id}, eps[1], eps[1:2]); !errors.As(err, &ng) || !ng.Busy {
		t.Fatalf("an old reservation lapsed: %v", err)
	}

	// A reservation released gives its targets back as they were.
	other, err := s.ReserveTargetGrab(ctx, TargetGrab{ID: "tg_5", TitleID: id}, "", eps)
	if err == nil || len(other.TargetIDs) != 0 {
		t.Fatalf("nothing was left to reserve: %+v %v", other, err)
	}
	if err := s.ReleaseTargetGrab(ctx, "tg_1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetTargetGrab(ctx, "tg_1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("a released reservation's row is still there: %v", err)
	}
	if st := stateOf(t, s, eps[1]); st.ReservedBy != "" || st.State != TargetWanted {
		t.Errorf("released: %+v", st)
	}

	g, err = s.ReserveTargetGrab(ctx, TargetGrab{ID: "tg_6", TitleID: id, ReleaseTitle: "Show.S01.1080p"}, eps[0], []string{eps[0], eps[3]})
	if err != nil {
		t.Fatal(err)
	}
	bound, err := s.BindTargetGrab(ctx, TargetGrab{ID: "tg_6", Adapter: "nzbget", ClientJobID: "101", Protocol: "usenet",
		Source: "https://idx.test/get/1"}, "grabbed NZB S01 from example via nzbget")
	if err != nil {
		t.Fatal(err)
	}
	if bound.State != GrabGrabbed || bound.Adapter != "nzbget" || bound.ClientJobID != "101" {
		t.Fatalf("bound: %+v", bound)
	}
	for _, e := range []string{eps[0], eps[3]} {
		if st := stateOf(t, s, e); st.State != TargetGrabbed || st.GrabID != "tg_6" || st.ReservedBy != "" ||
			st.Detail != "grabbed NZB S01 from example via nzbget" {
			t.Errorf("%s after binding: %+v", e, st)
		}
	}
	if st := stateOf(t, s, eps[1]); st.State != TargetWanted {
		t.Errorf("a target the grab did not reserve was moved: %+v", st)
	}
	// Binding again moves nothing: a grab is bound once.
	if _, err := s.FreeTargetGrab(ctx, "tg_6", "cancelled"); err != nil {
		t.Fatal(err)
	}
	if again, err := s.BindTargetGrab(ctx, TargetGrab{ID: "tg_6", Adapter: "nzbget", ClientJobID: "101"}, "grabbed"); err != nil ||
		again.State != GrabFailed || stateOf(t, s, eps[0]).State != TargetWanted {
		t.Errorf("binding a failed grab: %+v %v", again, err)
	}
	byJob, err := s.FindTargetGrabByJob(ctx, "", "101")
	if err != nil || byJob.ID != "tg_6" {
		t.Fatalf("by job: %+v %v", byJob, err)
	}
	files, _ := s.GrabFiles(ctx, "tg_6")
	if len(files) != 2 || files[0].State != "expected" {
		t.Errorf("grab_targets rows: %+v", files)
	}
}

// Moves of a grab's targets are conditional: only targets still the grab's
// and in the states expected move, so a target a reconcile held meanwhile,
// or a failed one a late event names, is left as it is.
func TestGrabTargetMovesAreConditional(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	id, eps := series(t, s, 9, 3)
	g, err := s.ReserveTargetGrab(ctx, TargetGrab{ID: "tg_c", TitleID: id}, eps[0], eps)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.BindTargetGrab(ctx, TargetGrab{ID: g.ID, Adapter: "nzbget", ClientJobID: "1"}, "grabbed"); err != nil {
		t.Fatal(err)
	}
	_ = s.HoldTarget(ctx, Hold{TargetID: eps[1], ItemID: "x"})
	if n, _ := s.MoveGrabTargets(ctx, g.ID, []string{TargetGrabbed}, TargetDownloaded, "downloaded"); n != 2 {
		t.Errorf("moved %d, want the two still grabbed", n)
	}
	if moved, _ := s.MoveGrabTarget(ctx, g.ID, eps[1], []string{TargetDownloaded}, TargetFailed, "x"); moved {
		t.Error("a held target was moved")
	}
	if n, _ := s.FailGrabTargets(ctx, g.ID, eps, []string{TargetDownloaded}, "refused", true); n != 2 {
		t.Errorf("failed %d, want 2", n)
	}
	if n, _ := s.MoveGrabTargets(ctx, g.ID, []string{TargetGrabbed}, TargetDownloaded, "a repeated completed event"); n != 0 {
		t.Errorf("a repeated event moved %d failed targets back", n)
	}
	if st := stateOf(t, s, eps[1]); st.State != TargetHeld {
		t.Errorf("held: %+v", st)
	}
}

// A target its release failed is wanted again after a backoff — 6 hours,
// doubling to 48 — up to five failures; after the fifth it stays failed and
// says so. Held, it starts again from none.
func TestFailureRecovery(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	id, eps := series(t, s, 10, 1)
	waits := []time.Duration{6 * time.Hour, 12 * time.Hour, 24 * time.Hour, 48 * time.Hour}
	for i := 0; i < MaxGrabFailures; i++ {
		gid := fmt.Sprintf("tg_f%d", i)
		if _, err := s.ReserveTargetGrab(ctx, TargetGrab{ID: gid, TitleID: id}, eps[0], eps); err != nil {
			t.Fatalf("attempt %d: %v", i+1, err)
		}
		if _, err := s.BindTargetGrab(ctx, TargetGrab{ID: gid, Adapter: "nzbget", ClientJobID: gid}, "grabbed"); err != nil {
			t.Fatal(err)
		}
		if n, _ := s.FailGrabTargets(ctx, gid, eps, []string{TargetGrabbed}, "download failed: par repair", true); n != 1 {
			t.Fatalf("attempt %d failed %d", i+1, n)
		}
		st := stateOf(t, s, eps[0])
		if st.GrabFailures != i+1 {
			t.Fatalf("attempt %d: failures %d", i+1, st.GrabFailures)
		}
		if i+1 == MaxGrabFailures {
			if st.RetryAt != nil || !strings.HasPrefix(st.Detail, LeftForAHuman) {
				t.Fatalf("after the fifth failure: %+v", st)
			}
			break
		}
		if st.RetryAt == nil || time.Until(*st.RetryAt) < waits[i]-time.Minute || time.Until(*st.RetryAt) > waits[i]+time.Minute {
			t.Fatalf("attempt %d: retry at %v, want in %v", i+1, st.RetryAt, waits[i])
		}
		if n, _ := s.RecoverFailedTargets(ctx); n != 0 {
			t.Fatal("recovered before its time")
		}
		_, _ = s.pool.Exec(ctx, `UPDATE acquisition_targets SET retry_at = now() - interval '1 second' WHERE id = $1`, eps[0])
		if n, _ := s.RecoverFailedTargets(ctx); n != 1 {
			t.Fatal("not recovered once its time came")
		}
		if st := stateOf(t, s, eps[0]); st.State != TargetWanted || st.NextSearchAt != nil ||
			!strings.Contains(st.Detail, "wanted again, its failed releases blocked") {
			t.Fatalf("recovered: %+v", st)
		}
	}
	_, _ = s.pool.Exec(ctx, `UPDATE acquisition_targets SET retry_at = now() - interval '1 second' WHERE id = $1`, eps[0])
	if n, _ := s.RecoverFailedTargets(ctx); n != 0 {
		t.Error("a target left for a human was tried again")
	}
	_ = s.HoldTarget(ctx, Hold{TargetID: eps[0], ItemID: "x"})
	if st := stateOf(t, s, eps[0]); st.GrabFailures != 0 || st.RetryAt != nil {
		t.Errorf("held: %+v", st)
	}
}

// A reconcile does not take back an episode acquire handed over minutes ago
// and the catalog answered that it holds: katalog-api may not list it yet.
func TestAReconcileRespectsAFreshHandOver(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	id, eps := series(t, s, 11, 2)
	_ = s.HoldTarget(ctx, Hold{TargetID: eps[0], ItemID: "fresh", Source: "grab", HandedOver: true})
	_ = s.HoldTarget(ctx, Hold{TargetID: eps[1], ItemID: "old", Source: "grab", HandedOver: true})
	_, _ = s.pool.Exec(ctx, `UPDATE acquisition_targets SET handed_over_at = now() - interval '2 hours' WHERE id = $1`, eps[1])
	ch, err := s.ApplySeriesHoldings(ctx, id, "", "default", nil)
	if err != nil || ch.Released != 1 {
		t.Fatalf("released %+v %v, want only the one handed over hours ago", ch, err)
	}
	if st := stateOf(t, s, eps[0]); st.State != TargetHeld || st.HeldItemID != "fresh" {
		t.Errorf("a fresh hand-over was taken back: %+v", st)
	}
}

// A grab with no word of it for two days, that nothing works on any more, is
// stale; one whose download still reports, or whose import still runs, is not.
// Freeing a grab gives back what it reserved and what was in flight with it.
func TestStaleGrabsAndFreeingThem(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	id, eps := series(t, s, 12, 3)
	for i, e := range eps {
		gid := fmt.Sprintf("tg_s%d", i)
		if _, err := s.ReserveTargetGrab(ctx, TargetGrab{ID: gid, TitleID: id}, e, []string{e}); err != nil {
			t.Fatal(err)
		}
		if i > 0 {
			if _, err := s.BindTargetGrab(ctx, TargetGrab{ID: gid, Adapter: "nzbget", ClientJobID: gid}, "grabbed"); err != nil {
				t.Fatal(err)
			}
		}
	}
	_, _ = s.pool.Exec(ctx, `UPDATE target_grabs SET updated_at = now() - interval '3 days'`)
	// tg_s1's download reported an hour ago; tg_s2 has an import working.
	_, _ = s.UpsertDownload(ctx, Download{Adapter: "nzbget", ClientJobID: "tg_s1", WantedID: "tg_s1", State: "downloading"})
	_, _ = s.EnqueueImport(ctx, Import{Adapter: "nzbget", ClientJobID: "tg_s2", WantedID: "tg_s2"})
	stale, err := s.StaleTargetGrabs(ctx, 48*time.Hour)
	if err != nil || len(stale) != 1 || stale[0].ID != "tg_s0" || stale[0].State != GrabPending {
		t.Fatalf("stale = %+v %v, want only the pending one", stale, err)
	}
	if n, _ := s.FreeTargetGrab(ctx, "tg_s0", "no word of it"); n != 1 {
		t.Errorf("freed %d", n)
	}
	if st := stateOf(t, s, eps[0]); st.State != TargetWanted || st.ReservedBy != "" || st.Detail != "no word of it" {
		t.Errorf("freed reservation: %+v", st)
	}
	if n, _ := s.FreeTargetGrab(ctx, "tg_s1", "cancelled"); n != 1 {
		t.Errorf("freed %d", n)
	}
	if st := stateOf(t, s, eps[1]); st.State != TargetWanted {
		t.Errorf("freed grab: %+v", st)
	}
	if g, _ := s.GetTargetGrab(ctx, "tg_s1"); g.State != GrabFailed {
		t.Errorf("grab: %+v", g)
	}
	if err := s.UnbindGrabTarget(ctx, "tg_s2", eps[2]); err != nil {
		t.Fatal(err)
	}
	if g, _ := s.GetTargetGrab(ctx, "tg_s2"); len(g.TargetIDs) != 0 {
		t.Errorf("unbound: %+v", g)
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
	g, err := s.ReserveTargetGrab(ctx, TargetGrab{ID: "tg_a", TitleID: id}, eps[0], eps[:1])
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := s.ActiveDownloads(ctx); n != 0 {
		t.Fatalf("the reservation being admitted counted against itself: %d", n)
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

// Two grabs reserving the same target at the same moment, on two connections:
// exactly one takes it. The claim is one conditional statement, so the second
// sees the first's reservation once the row lock lets it through.
func TestAReservationIsTakenOnce(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	id, eps := series(t, s, 8, 30)
	for round, ep := range eps {
		start := make(chan struct{})
		results := make(chan error, 2)
		for i := 0; i < 2; i++ {
			gid := fmt.Sprintf("tg_r%d_%d", round, i)
			go func() {
				<-start
				_, err := s.ReserveTargetGrab(ctx, TargetGrab{ID: gid, TitleID: id}, ep, []string{ep})
				results <- err
			}()
		}
		close(start)
		won := 0
		for i := 0; i < 2; i++ {
			if err := <-results; err == nil {
				won++
			} else if ng := (*NotGrabbableError)(nil); !errors.As(err, &ng) {
				t.Fatalf("round %d: %v", round, err)
			}
		}
		if won != 1 {
			t.Fatalf("round %d: %d reservations took the same target", round, won)
		}
	}
}

// A target reserved for a hand-over is not due for the sweep — searching it
// would spend a source's allowance on a grab that cannot be taken — and the
// library holding it ends its reservation, so it is not left reserved for a
// grab that will never bind it.
func TestAReservationKeepsTheSweepAwayUntilItEnds(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	id, eps := series(t, s, 13, 2)
	knownHoldings(t, s, id)
	if _, err := s.ReserveTargetGrab(ctx, TargetGrab{ID: "tg_r1", TitleID: id}, eps[0], eps); err != nil {
		t.Fatal(err)
	}
	if due, err := s.DueTargets(ctx, 10); err != nil || len(due) != 0 {
		t.Fatalf("reserved targets are due: %+v %v", due, err)
	}
	if _, err := s.ApplySeriesHoldings(ctx, id, "", "default", []HeldEpisode{{Season: 1, Episode: 2, ItemID: "x2"}}); err != nil {
		t.Fatal(err)
	}
	if st := stateOf(t, s, eps[1]); st.State != TargetHeld || st.ReservedBy != "" {
		t.Fatalf("held, but still reserved: %+v", st)
	}
	bound, err := s.BindTargetGrab(ctx, TargetGrab{ID: "tg_r1", Adapter: "nzbget", ClientJobID: "1"}, "grabbed")
	if err != nil {
		t.Fatal(err)
	}
	if st := stateOf(t, s, eps[1]); st.State != TargetHeld || st.GrabID == bound.ID {
		t.Errorf("the grab bound a target the library holds: %+v", st)
	}
}

// Under a cap, the downloads at once are counted and the reservation taken
// under one lock: reservations being handed over count, so grabbers racing
// for the last slot — the sweep, the feeds, the grab queue — take it once.
func TestReservationsUnderTheCap(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	id, eps := series(t, s, 9, 12)
	start := make(chan struct{})
	results := make(chan error, len(eps))
	for i, ep := range eps {
		gid := fmt.Sprintf("tg_cap%d", i)
		go func() {
			<-start
			_, err := s.ReserveTargetGrabWithin(ctx, TargetGrab{ID: gid, TitleID: id}, ep, []string{ep}, 2)
			results <- err
		}()
	}
	close(start)
	won := 0
	for range eps {
		err := <-results
		var full *AtCapacityError
		switch {
		case err == nil:
			won++
		case errors.As(err, &full):
			if full.Max != 2 || full.InFlight < 2 {
				t.Errorf("refused: %+v", full)
			}
		default:
			t.Fatal(err)
		}
	}
	if won != 2 {
		t.Fatalf("%d reservations under a cap of 2", won)
	}
	// Handed over and not reported yet, they still count; once the
	// gateway's downloads end, the slots are free.
	if _, err := s.pool.Exec(ctx, `UPDATE target_grabs SET state = 'grabbed', adapter = 'nzbget', client_job_id = id`); err != nil {
		t.Fatal(err)
	}
	other, more := series(t, s, 11, 1)
	if _, err := s.ReserveTargetGrabWithin(ctx, TargetGrab{ID: "tg_cap_late", TitleID: other}, more[0], more, 2); err == nil {
		t.Error("reserved while two grabs are on their way")
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO downloads (adapter, client_job_id, state, title)
		SELECT adapter, client_job_id, 'completed', release_title FROM target_grabs`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReserveTargetGrabWithin(ctx, TargetGrab{ID: "tg_cap_free", TitleID: other}, more[0], more, 2); err != nil {
		t.Errorf("once the downloads ended: %v", err)
	}
}

// What the library holds is known as of when the catalog was asked: a target
// a read of TMDB added meanwhile is of unknown holdings until the next
// reconcile.
func TestHoldingsAreKnownAsOfTheAsking(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	id, eps := series(t, s, 10, 1)
	asked := time.Now().Add(-time.Minute)
	if _, err := s.pool.Exec(ctx, `UPDATE acquisition_targets SET added_at = now() - interval '30 seconds' WHERE id = $1`, eps[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplySeriesHoldingsAt(ctx, id, "", "default", nil, asked); err != nil {
		t.Fatal(err)
	}
	ti, _ := s.TitleByID(ctx, id)
	if ti.HoldingsAt == nil || ti.HoldingsAt.Sub(asked).Abs() > time.Millisecond {
		t.Errorf("holdings known as of %v, want %v", ti.HoldingsAt, asked)
	}
	if missing, _ := s.Missing(ctx, 10); len(missing) != 0 {
		t.Errorf("a target added after the asking is missing: %+v", missing)
	}
}
