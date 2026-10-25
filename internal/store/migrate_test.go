package store

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Migrations here re-run on EVERY boot with no tracking table, so "is it
// idempotent" is not a code-review question — it is the difference between a
// rolling restart and a CrashLoopBackOff. These tests need a real Postgres;
// CI provides one via the `postgres` service. Skipped when TEST_DSN is unset so
// a laptop `go test ./...` stays green.
func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DSN")
	if dsn == "" {
		t.Skip("TEST_DSN not set; skipping migration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}
	// Each test starts from nothing.
	if _, err := pool.Exec(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public;`); err != nil {
		t.Fatalf("reset schema: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// A fresh database must converge, and a SECOND boot over the same database must
// change nothing and must not error.
func TestMigrateIsIdempotentOnAFreshDatabase(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	for i := 1; i <= 3; i++ {
		if err := migrate(ctx, pool); err != nil {
			t.Fatalf("boot %d: %v", i, err)
		}
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM quality_profiles`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("three boots produced %d profiles, want 1", n)
	}
}

// The case that actually matters: an EXISTING install already holds the seeded
// config from 004, which 005 has to repair in place. A fresh-database test
// cannot catch this, because 004's ON CONFLICT DO NOTHING means the seed and the
// repair are indistinguishable there.
func TestMigrateRepairsAProductionShapedProfileRow(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	// Boot an install that predates the repair: apply everything, then force the
	// profile row back to exactly what 004 seeded.
	if err := migrate(ctx, pool); err != nil {
		t.Fatalf("initial boot: %v", err)
	}
	const seeded = `{
	  "preferProtocol": "usenet",
	  "resolutions": ["1080p", "2160p", "720p"],
	  "preferredCodecs": ["x265", "hevc"],
	  "rejectTerms": ["cam", "camrip", "ts", "telesync", "screener", "workprint"],
	  "minSizeMb": 500, "maxSizeMb": 25000, "minSeeders": 1, "preferHdr": false
	}`
	if _, err := pool.Exec(ctx,
		`UPDATE quality_profiles SET config = $1::jsonb WHERE id = 'default'`, seeded); err != nil {
		t.Fatal(err)
	}

	if err := migrate(ctx, pool); err != nil {
		t.Fatalf("repair boot: %v", err)
	}

	var raw []byte
	if err := pool.QueryRow(ctx,
		`SELECT config FROM quality_profiles WHERE id = 'default'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}

	if _, ok := got["sourceScores"]; !ok {
		t.Error("sourceScores was not added — the ranker still scores every source zero")
	}
	if _, ok := got["maxSizeMbByResolution"]; !ok {
		t.Error("maxSizeMbByResolution was not added — one global cap still rejects real remuxes")
	}
	if res, _ := got["resolutions"].([]any); len(res) == 0 || res[0] != "2160p" {
		t.Errorf("resolutions = %v, want 2160p first", got["resolutions"])
	}
	// The unsafe bare terms must be GONE, not merged alongside the new list:
	// matched against the normalised name they reject the 2018 film "Cam".
	terms, _ := got["rejectTerms"].([]any)
	for _, term := range terms {
		if term == "cam" || term == "ts" {
			t.Errorf("unsafe bare term %q survived: %v", term, terms)
		}
	}
	if len(terms) == 0 {
		t.Error("rejectTerms was emptied entirely")
	}
	if src, _ := got["rejectSources"].([]any); len(src) == 0 {
		t.Error("rejectSources missing — nothing catches the cam family")
	}
	// Settings the operator may have tuned must survive.
	if got["preferProtocol"] != "usenet" {
		t.Errorf("preferProtocol was clobbered: %v", got["preferProtocol"])
	}
	// ...but the seeded 500 MB floor is itself a bug — it is a movie assumption
	// that rejects real TV — so an untouched seed value IS corrected.
	if got["minSizeMb"] != float64(100) {
		t.Errorf("the seeded 500 MB floor should have been corrected to 100, got %v", got["minSizeMb"])
	}

	// Re-running must now be a no-op: the guard is NOT (config ? 'sourceScores').
	var before time.Time
	if err := pool.QueryRow(ctx,
		`SELECT updated_at FROM quality_profiles WHERE id = 'default'`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := migrate(ctx, pool); err != nil {
		t.Fatalf("second repair boot: %v", err)
	}
	var after time.Time
	if err := pool.QueryRow(ctx,
		`SELECT updated_at FROM quality_profiles WHERE id = 'default'`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if !after.Equal(before) {
		t.Errorf("the repair re-fired on an already-repaired row (%v -> %v)", before, after)
	}
}

// An operator who edited their profile keeps their edits; they still get the
// ranker fix, because a ranker that prefers cams is not a preference.
func TestMigrateRepairsAnEditedProfileWithoutLosingEdits(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	if err := migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE quality_profiles
		   SET config = '{"preferProtocol":"torrent","minSeeders":5,"preferHdr":true,
		                  "resolutions":["1080p"],"rejectTerms":["cam"],
		                  "minSizeMb":250,"maxSizeMb":25000}'::jsonb,
		       updated_at = now() + interval '1 hour'
		 WHERE id = 'default'`); err != nil {
		t.Fatal(err)
	}
	if err := migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var raw []byte
	if err := pool.QueryRow(ctx,
		`SELECT config FROM quality_profiles WHERE id = 'default'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got["preferProtocol"] != "torrent" || got["minSeeders"] != float64(5) || got["preferHdr"] != true {
		t.Errorf("operator edits were lost: %v", got)
	}
	// A floor the operator chose is NOT the seeded value, so it must survive.
	if got["minSizeMb"] != float64(250) {
		t.Errorf("a deliberately-set minSizeMb was clobbered: %v", got["minSizeMb"])
	}
	if _, ok := got["sourceScores"]; !ok {
		t.Error("an edited profile did not receive the ranker repair")
	}
}

// migrateUpTo applies the embedded migrations that sort before name: the
// schema of an install from before that migration shipped.
func migrateUpTo(t *testing.T, pool *pgxpool.Pool, name string) {
	t.Helper()
	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".sql" || e.Name() >= name {
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

// 040 over an install that already tracks titles, targets and a grab's
// targets: every row keeps its state, the new columns read as their defaults,
// and every title's holdings are unknown until a reconcile learns them — so
// the backlog is empty rather than wrong until then. Re-running changes
// nothing.
func TestMigrate040OverAnExistingWantModel(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	migrateUpTo(t, pool, "040_target_grabs.sql")
	if _, err := pool.Exec(ctx, `
		INSERT INTO titles (id, tmdb_id, kind, title, tvdb_id) VALUES ('series:1396', 1396, 'series', 'Breaking Bad', 81189);
		INSERT INTO acquisition_targets (id, title_id, kind, season_number, episode_number, state, air_window_opens_at,
		                                 held_item_id, search_failures, search_backoff_until)
		VALUES ('series:1396:s01e001', 'series:1396', 'episode', 1, 1, 'wanted', now() - interval '1 day', '', 3, now() + interval '1 hour'),
		       ('series:1396:s01e002', 'series:1396', 'episode', 1, 2, 'held', now() - interval '1 day', 'item-2', 0, NULL);
		INSERT INTO grab_targets (adapter, client_job_id, target_id) VALUES ('nzbget', '9', 'series:1396:s01e001');`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := migrate(ctx, pool); err != nil {
			t.Fatalf("boot %d: %v", i+1, err)
		}
	}
	s := &Store{pool: pool}
	one, err := s.GetTarget(ctx, "series:1396:s01e001")
	if err != nil {
		t.Fatal(err)
	}
	if one.State != TargetWanted || one.SearchFailures != 3 || one.Detail != "" || one.GrabID != "" || one.SearchOutcome != "" {
		t.Errorf("an existing wanted target: %+v", one)
	}
	two, _ := s.GetTarget(ctx, "series:1396:s01e002")
	if two.State != TargetHeld || two.HeldItemID != "item-2" || two.HeldVia != "" || two.HeldPlayable {
		t.Errorf("an existing held target: %+v", two)
	}
	ti, err := s.TitleByID(ctx, "series:1396")
	if err != nil || ti.HoldingsKnown() || ti.CatalogID != "" {
		t.Errorf("an existing title: %+v %v", ti, err)
	}
	if miss, _ := s.Missing(ctx, 10); len(miss) != 0 {
		t.Errorf("before its first reconcile a title is missing nothing: %+v", miss)
	}
	var grabID string
	if err := pool.QueryRow(ctx, `SELECT grab_id FROM grab_targets WHERE client_job_id = '9'`).Scan(&grabID); err != nil || grabID != "" {
		t.Errorf("an existing grab_targets row: %q %v", grabID, err)
	}
	var n int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM target_grabs`).Scan(&n)
	if n != 0 {
		t.Errorf("target_grabs holds %d rows on an install that never grabbed a target", n)
	}
}

// 042 over an install that tracks titles, holds some of their episodes, misses
// others and has requests: every row keeps its state, nothing is followed,
// every target reads as the old backlog (added by nothing 042 knows), and the
// backlog is what it was — the same rows missing. Re-running changes nothing.
func TestMigrate042OverAnExistingInstall(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	migrateUpTo(t, pool, "042_follow.sql")
	if _, err := pool.Exec(ctx, `
		INSERT INTO titles (id, tmdb_id, kind, title, tvdb_id, status, holdings_at) VALUES
		  ('series:990396', 990396, 'series', 'Example Show', 9908118, 'continuing', now()),
		  ('movie:990603', 990603, 'movie', 'Example Film', NULL, 'released', now());
		INSERT INTO acquisition_targets (id, title_id, kind, season_number, episode_number, state, air_date, air_window_opens_at, held_item_id)
		VALUES ('series:990396:s01e001', 'series:990396', 'episode', 1, 1, 'wanted', current_date - 3, now() - interval '3 days', ''),
		       ('series:990396:s01e002', 'series:990396', 'episode', 1, 2, 'held', current_date - 3, now() - interval '3 days', 'item-2'),
		       ('movie:990603:movie', 'movie:990603', 'movie', NULL, NULL, 'wanted', NULL, NULL, '');
		INSERT INTO wanted_items (id, tmdb_id, media_type, title, status, detail)
		VALUES ('w_1', 990396, 'series', 'Example Show', 'failed', 'a series request cannot be handed over');`); err != nil {
		t.Fatal(err)
	}
	s := &Store{pool: pool}
	for i := 0; i < 2; i++ {
		if err := migrate(ctx, pool); err != nil {
			t.Fatalf("boot %d: %v", i+1, err)
		}
	}
	// Missing as before 042: the wanted episode and the movie the import made.
	after, err := s.Missing(ctx, 10)
	if err != nil || len(after) != 2 {
		t.Fatalf("missing after 042: %+v %v", after, err)
	}
	for _, id := range []string{"series:990396:s01e001", "series:990396:s01e002", "movie:990603:movie"} {
		tg, err := s.GetTarget(ctx, id)
		if err != nil || tg.AddedAt != nil || tg.EpisodeTitle != "" || tg.ReleasePending || tg.ReleaseKind != "" {
			t.Errorf("%s: %+v %v", id, tg, err)
		}
	}
	if held, _ := s.GetTarget(ctx, "series:990396:s01e002"); held.State != TargetHeld || held.HeldItemID != "item-2" {
		t.Errorf("a held target: %+v", held)
	}
	for _, id := range []string{"series:990396", "movie:990603"} {
		ti, err := s.TitleByID(ctx, id)
		if err != nil || ti.Followed() || ti.Origin != "" || ti.TMDBStatus != "" || ti.InventoryAt != nil || !ti.HoldingsKnown() {
			t.Errorf("%s: %+v %v", id, ti, err)
		}
	}
	w, err := s.GetWanted(ctx, "w_1")
	if err != nil || w.TitleID != "" || w.Status != "failed" {
		t.Errorf("a request from before: %+v %v", w, err)
	}
	var cursors int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM rss_cursors`).Scan(&cursors); err != nil || cursors != 0 {
		t.Errorf("rss_cursors: %d %v", cursors, err)
	}
}

// The registry lives in the migrations directory, and migrate() Execs every file
// it can see. Only *.sql is embedded, so a .md is invisible to it — but that is
// one edit away from being false. Widening the embed to migrations/* would make
// the boot path try to run Markdown as SQL and CrashLoopBackOff the service.
func TestOnlySQLFilesAreEmbedded(t *testing.T) {
	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	var n int
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if filepath.Ext(e.Name()) != ".sql" {
			t.Errorf("%s is embedded but is not SQL — migrate() would Exec it at boot", e.Name())
		}
		n++
	}
	if n == 0 {
		t.Fatal("no migrations embedded at all")
	}
}

// A duplicate numeric prefix means both files run, in an order nobody chose.
// Depending on which wins, that is either a silent no-op that fails at runtime
// forever, or a boot error with no version row to skip and nothing to restore.
func TestMigrationNumbersAreUnique(t *testing.T) {
	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]string{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || filepath.Ext(name) != ".sql" {
			continue
		}
		num, _, ok := strings.Cut(name, "_")
		if !ok || len(num) != 3 {
			t.Errorf("%s does not start with a 3-digit number", name)
			continue
		}
		if prev, dup := seen[num]; dup {
			t.Errorf("migration number %s claimed twice: %s and %s — see MIGRATIONS.md", num, prev, name)
		}
		seen[num] = name
	}
}
