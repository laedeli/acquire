package store

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func sampleIndexer(t *testing.T, s *Store, name string) Indexer {
	t.Helper()
	id, err := s.NextIndexerID(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return Indexer{
		ID: id, Name: name, Protocol: "usenet", BaseURL: "https://source.test", APIPath: "/api",
		Categories: IndexerCategories{Movie: []int{2000}, TV: []int{5000, 5040}}, Enabled: true,
	}
}

func TestIndexerWritesAreConditionalAndAudited(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()

	ix, err := s.CreateIndexer(ctx, sampleIndexer(t, s, "example"),
		SecretWrite{Op: SecretSet, CT: []byte("sealed-1"), KID: "k1"}, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if ix.Revision != 1 || ix.APIKeyUpdatedAt == nil || !bytes.Equal(ix.APIKeyCT, []byte("sealed-1")) {
		t.Fatalf("created: %+v", ix)
	}
	if len(ix.Categories.TV) != 2 || ix.Categories.Movie[0] != 2000 {
		t.Fatalf("categories: %+v", ix.Categories)
	}
	if _, err := s.CreateIndexer(ctx, sampleIndexer(t, s, "example"), SecretWrite{}, "alice"); !errors.Is(err, ErrExists) {
		t.Fatalf("duplicate name: %v, want ErrExists", err)
	}

	ix.Priority = 5
	kept, err := s.UpdateIndexer(ctx, ix, SecretWrite{Op: SecretKeep}, ix.Revision, false, "bob")
	if err != nil {
		t.Fatal(err)
	}
	if kept.Priority != 5 || kept.Revision != 2 || !bytes.Equal(kept.APIKeyCT, []byte("sealed-1")) {
		t.Fatalf("keep lost the edit or the key: %+v", kept)
	}
	if _, err := s.UpdateIndexer(ctx, ix, SecretWrite{}, ix.Revision, false, "bob"); !errors.Is(err, ErrStale) {
		t.Fatalf("stale update: %v", err)
	}
	cleared, err := s.UpdateIndexer(ctx, kept, SecretWrite{Op: SecretClear}, kept.Revision, false, "bob")
	if err != nil || cleared.APIKeyCT != nil || cleared.APIKeyKID != "" || cleared.APIKeyUpdatedAt != nil {
		t.Fatalf("clear: %+v %v", cleared, err)
	}
	if err := s.DeleteIndexer(ctx, ix.ID, kept.Revision, "carol"); !errors.Is(err, ErrStale) {
		t.Fatalf("stale delete: %v", err)
	}
	if err := s.DeleteIndexer(ctx, ix.ID, cleared.Revision, "carol"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetIndexer(ctx, ix.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted source still readable: %v", err)
	}

	entries, err := s.RecentAudit(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	var actions []string
	for _, e := range entries {
		if e.Entity == "source" {
			actions = append(actions, e.Actor+":"+e.Action)
		}
	}
	want := []string{"carol:delete", "bob:update", "bob:update", "alice:create"}
	if len(actions) != len(want) {
		t.Fatalf("audit = %v, want %v", actions, want)
	}
	for i := range want {
		if actions[i] != want[i] {
			t.Fatalf("audit = %v, want %v", actions, want)
		}
	}
}

// A search must never reset an editor: health bookkeeping leaves the revision
// alone, and an admin write can clear it.
func TestIndexerHealth(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	ix, err := s.CreateIndexer(ctx, sampleIndexer(t, s, "example"), SecretWrite{}, "admin")
	if err != nil {
		t.Fatal(err)
	}

	base, max := 5*time.Minute, 6*time.Hour
	var last time.Duration
	for i := 0; i < 9; i++ {
		before := time.Now()
		if err := s.IndexerFailed(ctx, ix.ID, "HTTP 503", true, base, max); err != nil {
			t.Fatal(err)
		}
		got, _ := s.GetIndexer(ctx, ix.ID)
		wait := got.BackoffUntil.Sub(before).Round(time.Minute)
		want := base << i
		if want > max {
			want = max
		}
		if wait != want {
			t.Fatalf("failure %d: backoff %v, want %v", i+1, wait, want)
		}
		last = wait
	}
	if last != max {
		t.Fatalf("backoff never reached the ceiling: %v", last)
	}
	got, _ := s.GetIndexer(ctx, ix.ID)
	if got.Failures != 9 || got.LastError != "HTTP 503" || got.Revision != ix.Revision {
		t.Fatalf("health moved the revision or lost the count: %+v", got)
	}

	// A failure without backoff counts but does not push the next attempt out.
	if err := s.IndexerSucceeded(ctx, ix.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.IndexerFailed(ctx, ix.ID, "not a feed", false, base, max); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetIndexer(ctx, ix.ID)
	if got.Failures != 1 || got.BackoffUntil != nil || got.LastError != "not a feed" {
		t.Fatalf("plain failure: %+v", got)
	}

	// A rejected key disables the source: a configuration change, audited.
	if err := s.DisableIndexer(ctx, ix.ID, "credentials rejected", "acquire"); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetIndexer(ctx, ix.ID)
	if got.Enabled || got.Revision != ix.Revision+1 || got.LastError != "credentials rejected" {
		t.Fatalf("disable: %+v", got)
	}
	if err := s.DisableIndexer(ctx, ix.ID, "still rejected", "acquire"); err != nil {
		t.Fatal(err)
	}
	again, _ := s.GetIndexer(ctx, ix.ID)
	if again.Revision != got.Revision || again.LastError != "still rejected" {
		t.Fatalf("disabling a disabled source changed its revision: %+v", again)
	}

	// Enabling it again with a reset clears the failure state.
	again.Enabled = true
	reset, err := s.UpdateIndexer(ctx, again, SecretWrite{}, again.Revision, true, "admin")
	if err != nil || reset.Failures != 0 || reset.LastError != "" || reset.BackoffUntil != nil || !reset.Enabled {
		t.Fatalf("reset: %+v %v", reset, err)
	}

	if err := s.SetIndexerCaps(ctx, ix.ID, []byte(`{"server":{"title":"Example"}}`)); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetIndexer(ctx, ix.ID)
	if got.CapsAt == nil || !bytes.Contains(got.Caps, []byte("Example")) {
		t.Fatalf("caps: %s %v", got.Caps, got.CapsAt)
	}
}

// The allowance is checked and spent in one statement: many concurrent
// searches can never send more than the limit.
func TestIndexerUsageNeverOverspends(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	ix, err := s.CreateIndexer(ctx, sampleIndexer(t, s, "example"), SecretWrite{}, "admin")
	if err != nil {
		t.Fatal(err)
	}
	limit := 7
	var mu sync.Mutex
	allowed := 0
	var wg sync.WaitGroup
	for i := 0; i < 25; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := s.ReserveIndexerQuery(ctx, ix.ID, &limit)
			if err != nil {
				t.Error(err)
				return
			}
			if ok {
				mu.Lock()
				allowed++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if allowed != limit {
		t.Fatalf("%d queries allowed against a limit of %d", allowed, limit)
	}
	got, _ := s.GetIndexer(ctx, ix.ID)
	if got.QueriesToday != limit || got.GrabsToday != 0 {
		t.Fatalf("usage read with the row: %d queries, %d grabs", got.QueriesToday, got.GrabsToday)
	}

	// No limit counts and allows; grabs are counted separately.
	if ok, err := s.ReserveIndexerGrab(ctx, ix.ID, nil); !ok || err != nil {
		t.Fatalf("unlimited grab: %v %v", ok, err)
	}
	zero := 0
	if ok, _ := s.ReserveIndexerGrab(ctx, ix.ID, &zero); ok {
		t.Fatal("a zero allowance allowed a grab")
	}
	got, _ = s.GetIndexer(ctx, ix.ID)
	if got.GrabsToday != 1 {
		t.Fatalf("grabs today = %d", got.GrabsToday)
	}

	// Deleting the source removes its counters.
	if err := s.DeleteIndexer(ctx, ix.ID, 0, "admin"); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM indexer_usage WHERE indexer_id=$1`, ix.ID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("usage rows left behind: %d %v", n, err)
	}
}
