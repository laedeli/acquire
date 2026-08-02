package store

import (
	"context"
	"testing"
	"time"
)

// The movie case has BOTH season_number and episode_number NULL. Under default
// Postgres semantics NULLs are distinct, so a plain unique index would allow
// unlimited duplicate movie targets for the same title. NULLS NOT DISTINCT is
// what makes the constraint mean what it says.
func TestMovieTargetsCannotDuplicate(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	_, err := s.pool.Exec(ctx,
		`INSERT INTO titles (id, tmdb_id, kind, title) VALUES ('t1', 693134, 'movie', 'Dune: Part Two')`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO acquisition_targets (id, title_id, kind) VALUES ('a1','t1','movie')`); err != nil {
		t.Fatal(err)
	}
	_, err = s.pool.Exec(ctx,
		`INSERT INTO acquisition_targets (id, title_id, kind) VALUES ('a2','t1','movie')`)
	if err == nil {
		t.Fatal("a second movie target for the same title was accepted — NULLS NOT DISTINCT is not in effect")
	}
}

// Two episodes of the same show must coexist; the same episode twice must not.
func TestEpisodeCoordinatesAreUnique(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	_, _ = s.pool.Exec(ctx,
		`INSERT INTO titles (id, tmdb_id, kind, title, tvdb_id) VALUES ('t2', 1396, 'series', 'Breaking Bad', 81189)`)
	for _, ep := range []int{1, 2} {
		if _, err := s.pool.Exec(ctx, `
			INSERT INTO acquisition_targets (id, title_id, kind, season_number, episode_number)
			VALUES ($1,'t2','episode',2,$2)`, "e"+string(rune('0'+ep)), ep); err != nil {
			t.Fatalf("episode %d: %v", ep, err)
		}
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO acquisition_targets (id, title_id, kind, season_number, episode_number)
		VALUES ('dup','t2','episode',2,1)`)
	if err == nil {
		t.Error("S02E01 was accepted twice")
	}
}

// held_score is a CACHE of Score(held_quality, profile). The profile it was
// computed under has to be stored, or a profile change silently leaves the
// cutoff backlog wrong with nothing to detect it.
func TestHeldScoreCarriesItsProfileForInvalidation(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	_, _ = s.pool.Exec(ctx,
		`INSERT INTO titles (id, tmdb_id, kind, title) VALUES ('t3', 1, 'movie', 'X')`)
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO acquisition_targets
		  (id, title_id, kind, state, held_item_id, held_score, held_score_profile, held_source)
		VALUES ('h1','t3','movie','held','uuid-1',1746,'default','grab')`); err != nil {
		t.Fatal(err)
	}
	// The invalidation query a profile save must run.
	tag, err := s.pool.Exec(ctx,
		`UPDATE acquisition_targets SET held_scored_at = NULL WHERE held_score_profile = $1`, "default")
	if err != nil {
		t.Fatal(err)
	}
	if tag.RowsAffected() != 1 {
		t.Errorf("invalidation matched %d rows, want 1", tag.RowsAffected())
	}
	// Legacy files have no grab to derive a score from and must be
	// distinguishable, so an upgrade cannot fire on a guess.
	var src string
	_ = s.pool.QueryRow(ctx, `SELECT held_source FROM acquisition_targets WHERE id='h1'`).Scan(&src)
	if src != "grab" {
		t.Errorf("held_source = %q", src)
	}
}

// One grab satisfies N targets — that is what a season pack IS, and losing it
// is why the current code imports 1 of 10 episodes and calls it fulfilled.
func TestOneGrabCanSatisfyManyTargets(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	_, _ = s.pool.Exec(ctx,
		`INSERT INTO titles (id, tmdb_id, kind, title) VALUES ('t4', 2, 'series', 'Silo')`)
	for i := 1; i <= 10; i++ {
		_, _ = s.pool.Exec(ctx, `
			INSERT INTO acquisition_targets (id, title_id, kind, season_number, episode_number)
			VALUES ($1,'t4','episode',1,$2)`, "s1e"+string(rune('a'+i)), i)
		if _, err := s.pool.Exec(ctx, `
			INSERT INTO grab_targets (adapter, client_job_id, target_id)
			VALUES ('nzbget','job-1',$1)`, "s1e"+string(rune('a'+i))); err != nil {
			t.Fatalf("episode %d: %v", i, err)
		}
	}
	var n int
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM grab_targets WHERE adapter='nzbget' AND client_job_id='job-1'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 10 {
		t.Fatalf("one grab mapped to %d targets, want 10", n)
	}
}

// No foreign keys anywhere: ADD CONSTRAINT has no IF NOT EXISTS, and these
// files re-run on every boot. 003_downloads_soft_ref.sql set the precedent.
func TestWantModelHasNoForeignKeys(t *testing.T) {
	s := migrated(t)
	rows, err := s.pool.Query(context.Background(), `
		SELECT table_name, count(*) FROM information_schema.table_constraints
		 WHERE constraint_type = 'FOREIGN KEY'
		   AND table_name IN ('titles','seasons','title_aliases','acquisition_targets','grab_targets')
		 GROUP BY table_name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var tbl string
		var n int
		_ = rows.Scan(&tbl, &n)
		t.Errorf("%s has %d foreign key(s) — re-running this migration will fail", tbl, n)
	}
}

// A file that katalog loses must make the target wanted again. Modelling WANT
// separately from HAVE is pointless if the projection is one-way.
func TestClearingAHoldingMakesTheTargetWantedAgain(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	_, _ = s.pool.Exec(ctx, `INSERT INTO titles (id,tmdb_id,kind,title) VALUES ('t5',5,'movie','M')`)
	if err := s.UpsertTarget(ctx, Target{ID: "g1", TitleID: "t5", Kind: "movie", Monitored: true, State: "wanted"}); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyHolding(ctx, "g1", "uuid-9", "M.2024.1080p.WEB-DL.x265-G", "default", "grab",
		map[string]any{"resolution": "1080p"}, 1497); err != nil {
		t.Fatal(err)
	}
	var state string
	_ = s.pool.QueryRow(ctx, `SELECT state FROM acquisition_targets WHERE id='g1'`).Scan(&state)
	if state != "held" {
		t.Fatalf("state after holding = %q, want held", state)
	}
	n, err := s.ClearHolding(ctx, "uuid-9")
	if err != nil || n != 1 {
		t.Fatalf("ClearHolding = %d, %v", n, err)
	}
	_ = s.pool.QueryRow(ctx, `SELECT state FROM acquisition_targets WHERE id='g1'`).Scan(&state)
	if state != "wanted" {
		t.Errorf("a deleted file left the target %q — it should be wanted again", state)
	}
}

// An upgrade must never fire on a score guessed off a legacy file. 16,169
// existing episode files have no grab row behind them.
func TestCutoffNeverUpgradesADerivedScore(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	_, _ = s.pool.Exec(ctx, `INSERT INTO titles (id,tmdb_id,kind,title) VALUES ('t6',6,'series','S')`)
	for i, src := range []string{"grab", "derived"} {
		id := "c" + string(rune('1'+i))
		ep := i + 1
		if err := s.UpsertTarget(ctx, Target{ID: id, TitleID: "t6", Kind: "episode",
			SeasonNumber: intp(1), EpisodeNumber: &ep, Monitored: true, State: "wanted"}); err != nil {
			t.Fatal(err)
		}
		if err := s.ApplyHolding(ctx, id, "u"+id, "rel", "default", src, map[string]any{}, 100); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.CutoffUnmet(ctx, 1000, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "c1" {
		t.Errorf("cutoff returned %+v — only the real grab may be upgraded", got)
	}
}

// Unaired episodes must not be searched: indexer quota is the scarce resource,
// and a release that cannot exist yet burns it for nothing.
func TestUnairedEpisodesAreNotDue(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	_, _ = s.pool.Exec(ctx, `INSERT INTO titles (id,tmdb_id,kind,title) VALUES ('t7',7,'series','S')`)
	for i, when := range []string{"now() - interval '1 day'", "now() + interval '7 days'"} {
		ep := i + 1
		_, err := s.pool.Exec(ctx, `
			INSERT INTO acquisition_targets (id,title_id,kind,season_number,episode_number,air_window_opens_at)
			VALUES ($1,'t7','episode',1,$2, `+when+`)`, "u"+string(rune('1'+i)), ep)
		if err != nil {
			t.Fatal(err)
		}
	}
	due, err := s.DueTargets(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 || due[0].ID != "u1" {
		t.Errorf("due = %+v, want only the aired episode", due)
	}
}

// Backoff must actually push the next attempt out, and grow.
func TestSearchBackoffGrows(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	_, _ = s.pool.Exec(ctx, `INSERT INTO titles (id,tmdb_id,kind,title) VALUES ('t8',8,'movie','M')`)
	_ = s.UpsertTarget(ctx, Target{ID: "b1", TitleID: "t8", Kind: "movie", Monitored: true, State: "wanted"})
	var prev time.Duration
	for i := 1; i <= 3; i++ {
		if err := s.BackoffSearch(ctx, "b1", 10*time.Minute, 24*time.Hour); err != nil {
			t.Fatal(err)
		}
		var secs float64
		_ = s.pool.QueryRow(ctx,
			`SELECT EXTRACT(EPOCH FROM (search_backoff_until - now())) FROM acquisition_targets WHERE id='b1'`).Scan(&secs)
		cur := time.Duration(secs) * time.Second
		if cur <= prev {
			t.Errorf("attempt %d backoff %v did not grow past %v", i, cur, prev)
		}
		prev = cur
	}
	// And it must not be due while backed off.
	due, _ := s.DueTargets(ctx, 10)
	if len(due) != 0 {
		t.Errorf("a backed-off target was returned as due: %+v", due)
	}
}

func intp(v int) *int { return &v }

// The import must be safe to re-run: ids derive from identity, so a second pass
// updates the same rows instead of building a parallel library.
func TestImportIsIdempotent(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	tt := Title{TMDBID: 1396, Kind: "series", Title: "Breaking Bad", TVDBID: 81189,
		Monitored: true, MonitorNew: true, SeriesType: "standard"}
	for i := 0; i < 3; i++ {
		if err := s.UpsertTitle(ctx, tt); err != nil {
			t.Fatalf("pass %d: %v", i, err)
		}
		if err := s.ReplaceAliases(ctx, TitleID("series", 1396), []string{"Totalna Melina"}, "incumbent"); err != nil {
			t.Fatal(err)
		}
	}
	var titles, aliases int
	_ = s.pool.QueryRow(ctx, `SELECT count(*) FROM titles`).Scan(&titles)
	_ = s.pool.QueryRow(ctx, `SELECT count(*) FROM title_aliases`).Scan(&aliases)
	if titles != 1 || aliases != 1 {
		t.Fatalf("three imports produced %d titles and %d aliases, want 1 and 1", titles, aliases)
	}
}

// An operator's console settings are not the import's to overwrite, and an id we
// already resolved must never be blanked by a source that lacks it.
func TestImportPreservesOperatorSettingsAndResolvedIDs(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	base := Title{TMDBID: 1396, Kind: "series", Title: "Breaking Bad", TVDBID: 81189, Monitored: true}
	if err := s.UpsertTitle(ctx, base); err != nil {
		t.Fatal(err)
	}
	id := TitleID("series", 1396)
	if _, err := s.pool.Exec(ctx,
		`UPDATE titles SET profile_id='uhd', air_grace_hours=6 WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	// Re-import from a source that has no tvdb id and no imdb id.
	thin := base
	thin.TVDBID, thin.IMDBID = 0, ""
	if err := s.UpsertTitle(ctx, thin); err != nil {
		t.Fatal(err)
	}
	var profile string
	var grace int
	var tvdb int64
	_ = s.pool.QueryRow(ctx,
		`SELECT profile_id, air_grace_hours, COALESCE(tvdb_id,0) FROM titles WHERE id=$1`, id).
		Scan(&profile, &grace, &tvdb)
	if profile != "uhd" || grace != 6 {
		t.Errorf("operator settings clobbered: profile=%q grace=%d", profile, grace)
	}
	if tvdb != 81189 {
		t.Errorf("a resolved tvdb id was blanked by a source that lacked one (got %d)", tvdb)
	}
}

// Replacing aliases must remove ones the source dropped, not accumulate forever.
func TestAliasesAreReplacedNotAccumulated(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	_ = s.UpsertTitle(ctx, Title{TMDBID: 1, Kind: "series", Title: "S"})
	id := TitleID("series", 1)
	_ = s.ReplaceAliases(ctx, id, []string{"Alpha", "Beta"}, "incumbent")
	_ = s.ReplaceAliases(ctx, id, []string{"Beta"}, "incumbent")
	var n int
	_ = s.pool.QueryRow(ctx, `SELECT count(*) FROM title_aliases WHERE title_id=$1`, id).Scan(&n)
	if n != 1 {
		t.Errorf("aliases = %d, want 1 — a dropped alias must not linger", n)
	}
	// A different source's aliases are independent.
	_ = s.ReplaceAliases(ctx, id, []string{"Gamma"}, "xem")
	_ = s.pool.QueryRow(ctx, `SELECT count(*) FROM title_aliases WHERE title_id=$1`, id).Scan(&n)
	if n != 2 {
		t.Errorf("aliases across sources = %d, want 2", n)
	}
}

// The air window is what keeps a sweep off unaired episodes.
func TestAirWindowAppliesGrace(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	_ = s.UpsertTitle(ctx, Title{TMDBID: 2, Kind: "series", Title: "S"})
	id := TitleID("series", 2)
	season, ep := 1, 1
	tid := TargetID(id, &season, &ep)
	if err := s.UpsertTarget(ctx, Target{ID: tid, TitleID: id, Kind: "episode",
		SeasonNumber: &season, EpisodeNumber: &ep, Monitored: true, State: "wanted"}); err != nil {
		t.Fatal(err)
	}
	air := time.Now().Add(-2 * time.Hour)
	if err := s.SetAirWindow(ctx, tid, &air, 6); err != nil {
		t.Fatal(err)
	}
	// Aired two hours ago with six hours of grace: not yet due.
	due, _ := s.DueTargets(ctx, 10)
	if len(due) != 0 {
		t.Errorf("grace period ignored, target already due: %+v", due)
	}
	if err := s.SetAirWindow(ctx, tid, &air, 0); err != nil {
		t.Fatal(err)
	}
	due, _ = s.DueTargets(ctx, 10)
	if len(due) != 1 {
		t.Errorf("with no grace the aired episode should be due, got %d", len(due))
	}

	// The case a date-truncated window gets backwards: a late-evening broadcast.
	// Computed from air_date it would open at 06:00 the SAME DAY — before the
	// episode exists — and we would burn indexer quota on a release that cannot
	// be there yet.
	tonight := time.Now().UTC().Truncate(24 * time.Hour).Add(21 * time.Hour)
	if tonight.Before(time.Now()) {
		tonight = tonight.AddDate(0, 0, 1)
	}
	if err := s.SetAirWindow(ctx, tid, &tonight, 6); err != nil {
		t.Fatal(err)
	}
	var opens time.Time
	_ = s.pool.QueryRow(ctx,
		`SELECT air_window_opens_at FROM acquisition_targets WHERE id=$1`, tid).Scan(&opens)
	if !opens.After(tonight) {
		t.Errorf("window opens %v, before the %v broadcast — grace was measured from midnight", opens, tonight)
	}
	due, _ = s.DueTargets(ctx, 10)
	if len(due) != 0 {
		t.Errorf("an episode that has not aired yet was returned as due: %+v", due)
	}
}

// Saving a profile must invalidate the cached scores computed under it, or the
// cutoff sweep silently compares new preferences against old numbers.
func TestSavingAProfileInvalidatesCachedScores(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	_ = s.UpsertTitle(ctx, Title{TMDBID: 42, Kind: "movie", Title: "M"})
	id := TitleID("movie", 42)
	tid := TargetID(id, nil, nil)
	_ = s.UpsertTarget(ctx, Target{ID: tid, TitleID: id, Kind: "movie", Monitored: true, State: "wanted"})
	if err := s.ApplyHolding(ctx, tid, "uuid-1", "M.2024.1080p.WEB-DL.x265-G",
		"default", "grab", map[string]any{}, 1497); err != nil {
		t.Fatal(err)
	}
	var scoredAt *time.Time
	_ = s.pool.QueryRow(ctx, `SELECT held_scored_at FROM acquisition_targets WHERE id=$1`, tid).Scan(&scoredAt)
	if scoredAt == nil {
		t.Fatal("held_scored_at was not set when the holding was applied")
	}
	n, err := s.InvalidateHeldScores(ctx, "default")
	if err != nil || n != 1 {
		t.Fatalf("invalidate = %d, %v", n, err)
	}
	_ = s.pool.QueryRow(ctx, `SELECT held_scored_at FROM acquisition_targets WHERE id=$1`, tid).Scan(&scoredAt)
	if scoredAt != nil {
		t.Error("the cached score survived a profile change")
	}
	// A different profile's scores are untouched.
	_ = s.ApplyHolding(ctx, tid, "uuid-1", "rel", "uhd", "grab", map[string]any{}, 10)
	n, _ = s.InvalidateHeldScores(ctx, "default")
	if n != 0 {
		t.Errorf("invalidating 'default' touched %d rows scored under another profile", n)
	}
}

// The read views must agree with each other and with the underlying rows.
// A Series view whose counts disagree with the Missing view is worse than none.
func TestReadViewsAgree(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	_ = s.UpsertTitle(ctx, Title{TMDBID: 1396, Kind: "series", Title: "Breaking Bad",
		TVDBID: 81189, Monitored: true})
	id := TitleID("series", 1396)
	now := time.Now()
	// 2 aired (one held, one wanted) + 1 unaired.
	for i, spec := range []struct {
		off  time.Duration
		held bool
	}{{-48 * time.Hour, true}, {-24 * time.Hour, false}, {72 * time.Hour, false}} {
		season, ep := 1, i+1
		tid := TargetID(id, &season, &ep)
		if err := s.UpsertTarget(ctx, Target{ID: tid, TitleID: id, Kind: "episode",
			SeasonNumber: &season, EpisodeNumber: &ep, Monitored: true, State: "wanted"}); err != nil {
			t.Fatal(err)
		}
		air := now.Add(spec.off)
		if err := s.SetAirWindow(ctx, tid, &air, 0); err != nil {
			t.Fatal(err)
		}
		if spec.held {
			if err := s.ApplyHolding(ctx, tid, "u"+tid, "rel", "default", "grab", map[string]any{}, 100); err != nil {
				t.Fatal(err)
			}
		}
	}

	rows, err := s.SeriesOverview(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("series rows = %d", len(rows))
	}
	r := rows[0]
	if r.Episodes != 3 || r.Held != 1 || r.Missing != 1 || r.Unaired != 1 {
		t.Errorf("series counts = %+v, want episodes=3 held=1 missing=1 unaired=1", r)
	}

	miss, err := s.Missing(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(miss) != r.Missing {
		t.Errorf("Missing view has %d rows but the Series view says %d", len(miss), r.Missing)
	}
	if len(miss) == 1 && !miss[0].Searchable {
		t.Error("a series with a tvdb id was reported unsearchable")
	}

	c, err := s.Counts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if c.Targets != 3 || c.Held != 1 || c.Missing != 1 || c.Unaired != 1 || c.Series != 1 {
		t.Errorf("counts = %+v", c)
	}

	cal, err := s.Calendar(ctx, now.AddDate(0, 0, -7), now.AddDate(0, 0, 7))
	if err != nil {
		t.Fatal(err)
	}
	if len(cal) != 3 {
		t.Errorf("calendar returned %d episodes across the window, want 3", len(cal))
	}
}

// A title with no id an indexer accepts must be reported as unsearchable rather
// than sitting in the backlog looking like an ordinary miss.
func TestMissingFlagsUnsearchableTitles(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	_ = s.UpsertTitle(ctx, Title{TMDBID: 999, Kind: "series", Title: "No Ids Here", Monitored: true})
	id := TitleID("series", 999)
	season, ep := 1, 1
	tid := TargetID(id, &season, &ep)
	_ = s.UpsertTarget(ctx, Target{ID: tid, TitleID: id, Kind: "episode",
		SeasonNumber: &season, EpisodeNumber: &ep, Monitored: true, State: "wanted"})
	air := time.Now().Add(-24 * time.Hour)
	_ = s.SetAirWindow(ctx, tid, &air, 0)

	miss, err := s.Missing(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(miss) != 1 {
		t.Fatalf("missing = %d", len(miss))
	}
	if miss[0].Searchable {
		t.Error("a series with no tvdb id was reported searchable — it would sit in the backlog with no explanation")
	}
}
