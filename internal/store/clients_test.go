package store

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"
)

func sampleClient(id string) DownloadClient {
	return DownloadClient{
		ID: id, Type: "nzbget", BaseURL: "http://worker:6789", Auth: "basic", Username: "control",
		Protocols: []string{"usenet"}, Category: "acquire", RemotePath: "/downloads", LocalPath: "/media/downloads",
		Enabled: true,
	}
}

// The revision the gateway is told must only ever move forward — including
// when a client is deleted, or a shrunken set would look unchanged.
func TestClientRevisionMovesForwardOnEveryWrite(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()

	if rev, err := s.ClientsRevision(ctx); err != nil || rev != 0 {
		t.Fatalf("fresh revision = %d, %v; want 0", rev, err)
	}
	a, err := s.CreateDownloadClient(ctx, sampleClient("nzbget"),
		SecretWrite{Op: SecretSet, CT: []byte("sealed"), KID: "abcd1234"}, "admin")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.CreateDownloadClient(ctx, sampleClient("nzbget-2"), SecretWrite{}, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if b.Generation <= a.Generation {
		t.Fatalf("generations did not increase: %d then %d", a.Generation, b.Generation)
	}
	rev1, _ := s.ClientsRevision(ctx)
	if rev1 != b.Generation {
		t.Fatalf("revision %d, want the highest generation %d", rev1, b.Generation)
	}

	// Deleting the LOWER-generation row must still advance the revision.
	if err := s.DeleteDownloadClient(ctx, "nzbget", a.Generation, "admin"); err != nil {
		t.Fatal(err)
	}
	rev2, _ := s.ClientsRevision(ctx)
	if rev2 <= rev1 {
		t.Fatalf("delete left the revision at %d (was %d)", rev2, rev1)
	}
}

// The sequence shows a write's generation before the write commits. The read a
// push makes must not pair that revision with the rows from before the write,
// or the gateway would report the new revision while running the old client.
func TestClientsConfigPairsTheRevisionWithItsRows(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	a, err := s.CreateDownloadClient(ctx, sampleClient("nzbget"), SecretWrite{}, "admin")
	if err != nil {
		t.Fatal(err)
	}

	// A write in progress, taking the lock the way every client write does.
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockClientsForWrite(ctx, tx); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE download_clients SET base_url = 'http://moved:6789',
		generation = nextval('download_clients_generation_seq') WHERE id = 'nzbget'`); err != nil {
		t.Fatal(err)
	}
	ahead, _ := s.ClientsRevision(ctx)
	if ahead <= a.Generation {
		t.Fatalf("revision %d did not move ahead of %d", ahead, a.Generation)
	}

	type config struct {
		rev     int64
		clients []DownloadClient
		err     error
	}
	got := make(chan config, 1)
	go func() {
		rev, clients, err := s.ClientsConfig(ctx)
		got <- config{rev, clients, err}
	}()
	select {
	case c := <-got:
		t.Fatalf("read revision %d with %+v while the write was uncommitted", c.rev, c.clients)
	case <-time.After(300 * time.Millisecond):
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	c := <-got
	if c.err != nil || c.rev != ahead || len(c.clients) != 1 || c.clients[0].BaseURL != "http://moved:6789" {
		t.Fatalf("after commit: revision %d, clients %+v, err %v; want revision %d with the moved client", c.rev, c.clients, c.err, ahead)
	}

	// Writes still go through while nothing reads, and the pair stays exact.
	if err := s.DeleteDownloadClient(ctx, "nzbget", 0, "admin"); err != nil {
		t.Fatal(err)
	}
	rev, clients, err := s.ClientsConfig(ctx)
	if err != nil || rev <= ahead || len(clients) != 0 {
		t.Fatalf("after delete: revision %d, clients %+v, err %v", rev, clients, err)
	}
}

func TestClientWritesAreConditionalAndAudited(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()

	c, err := s.CreateDownloadClient(ctx, sampleClient("nzbget"),
		SecretWrite{Op: SecretSet, CT: []byte("sealed-1"), KID: "k1"}, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateDownloadClient(ctx, sampleClient("nzbget"), SecretWrite{}, "alice"); !errors.Is(err, ErrExists) {
		t.Fatalf("duplicate id: %v, want ErrExists", err)
	}
	if c.SecretUpdatedAt == nil || !bytes.Equal(c.SecretCT, []byte("sealed-1")) {
		t.Fatalf("secret not stored: %+v", c)
	}

	// Keep leaves the secret alone.
	c.Priority = 5
	kept, err := s.UpdateDownloadClient(ctx, c, SecretWrite{Op: SecretKeep}, c.Generation, "bob")
	if err != nil {
		t.Fatal(err)
	}
	if kept.Priority != 5 || !bytes.Equal(kept.SecretCT, []byte("sealed-1")) || kept.SecretKID != "k1" {
		t.Fatalf("keep changed the secret or lost the edit: %+v", kept)
	}

	// A write against the old generation is stale.
	if _, err := s.UpdateDownloadClient(ctx, c, SecretWrite{}, c.Generation, "bob"); !errors.Is(err, ErrStale) {
		t.Fatalf("stale update: %v, want ErrStale", err)
	}

	cleared, err := s.UpdateDownloadClient(ctx, kept, SecretWrite{Op: SecretClear}, kept.Generation, "bob")
	if err != nil {
		t.Fatal(err)
	}
	if cleared.SecretCT != nil || cleared.SecretKID != "" || cleared.SecretUpdatedAt != nil {
		t.Fatalf("clear left a secret behind: %+v", cleared)
	}

	entries, err := s.RecentAudit(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("audit has %d entries, want 3 (create, update, update): %+v", len(entries), entries)
	}
	if entries[0].Actor != "bob" || entries[0].Action != "update" || entries[2].Action != "create" {
		t.Fatalf("audit order or content wrong: %+v", entries)
	}
}

func TestClientWithDownloadsInFlightCannotBeDeleted(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	c, err := s.CreateDownloadClient(ctx, sampleClient("worker"), SecretWrite{}, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertDownload(ctx, Download{Adapter: "worker", ClientJobID: "7", State: "downloading"}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteDownloadClient(ctx, "worker", 0, "admin"); !errors.Is(err, ErrInFlight) {
		t.Fatalf("delete with a running download: %v, want ErrInFlight", err)
	}
	if _, err := s.UpsertDownload(ctx, Download{Adapter: "worker", ClientJobID: "7", State: "completed"}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteDownloadClient(ctx, "worker", c.Generation, "admin"); err != nil {
		t.Fatalf("delete after the download finished: %v", err)
	}
	if _, err := s.GetDownloadClient(ctx, "worker"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted client still readable: %v", err)
	}
}

// The id rule is the gateway's; the database enforces it too, so no code path
// can store an id the gateway would refuse.
func TestClientIDRuleIsEnforcedByTheDatabase(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	for _, id := range []string{"Upper", "-lead", "has space", "x2345678901234567890123456789012345678901"} {
		if _, err := s.CreateDownloadClient(ctx, sampleClient(id), SecretWrite{}, "admin"); err == nil {
			t.Errorf("id %q was stored", id)
		}
	}
}

func TestSettingsSeedOnceAndConditionalPut(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()

	if err := s.SeedSetting(ctx, "search", map[string]any{"maxConcurrentGrabs": 3}); err != nil {
		t.Fatal(err)
	}
	st, err := s.GetSetting(ctx, "search")
	if err != nil || st.Revision != 1 {
		t.Fatalf("seeded setting: %+v %v", st, err)
	}
	put, err := s.PutSetting(ctx, "search", map[string]any{"maxConcurrentGrabs": 5}, st.Revision, "alice")
	if err != nil || put.Revision != 2 {
		t.Fatalf("put: %+v %v", put, err)
	}
	// A second boot's seed must not overwrite the admin's edit.
	if err := s.SeedSetting(ctx, "search", map[string]any{"maxConcurrentGrabs": 3}); err != nil {
		t.Fatal(err)
	}
	again, _ := s.GetSetting(ctx, "search")
	if !bytes.Contains(again.Value, []byte("5")) {
		t.Fatalf("the seed overwrote an edit: %s", again.Value)
	}
	if _, err := s.PutSetting(ctx, "search", map[string]any{}, 1, "bob"); !errors.Is(err, ErrStale) {
		t.Fatalf("stale put: %v, want ErrStale", err)
	}
}

func TestGrabProvenanceIsStored(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	if err := s.CreateWanted(ctx, Wanted{ID: "w1", Title: "Example", MediaType: "movie"}); err != nil {
		t.Fatal(err)
	}
	id := int64(4)
	if err := s.RecordGrabRelease(ctx, Grab{
		WantedID: "w1", Adapter: "nzbget", ClientJobID: "9", Source: "https://idx.test/get?id=1",
		IndexerID: &id, ReleaseGUID: "guid-1", SourceCT: []byte("sealed"), SourceKID: "k1",
	}); err != nil {
		t.Fatal(err)
	}
	var ct []byte
	var gotID int64
	if err := s.pool.QueryRow(ctx, `SELECT source_ct, indexer_id FROM grabs WHERE wanted_id='w1'`).Scan(&ct, &gotID); err != nil {
		t.Fatal(err)
	}
	if string(ct) != "sealed" || gotID != 4 {
		t.Fatalf("provenance not stored: %q %d", ct, gotID)
	}
}
