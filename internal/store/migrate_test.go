package store

import (
	"context"
	"encoding/json"
	"os"
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
	if got["minSizeMb"] != float64(500) {
		t.Errorf("minSizeMb was clobbered: %v", got["minSizeMb"])
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
		                  "minSizeMb":500,"maxSizeMb":25000}'::jsonb,
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
	if _, ok := got["sourceScores"]; !ok {
		t.Error("an edited profile did not receive the ranker repair")
	}
}
