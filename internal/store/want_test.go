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
