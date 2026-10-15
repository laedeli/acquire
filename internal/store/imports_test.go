package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

// An import is recorded once, claimed by one holder at a time, due again when
// its holder saves it so, and never once it is done.
func TestImportsQueue(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	im := Import{Adapter: "nzbget", ClientJobID: "42", WantedID: "w_1", Files: []string{"/downloads/complete/Film"}}
	if created, err := s.EnqueueImport(ctx, im); err != nil || !created {
		t.Fatalf("enqueue: %v %v", created, err)
	}
	if created, err := s.EnqueueImport(ctx, im); err != nil || created {
		t.Fatalf("a second completed event recorded it again: %v %v", created, err)
	}

	got, ok, err := s.ClaimImport(ctx, time.Hour)
	if err != nil || !ok || got.State != ImportPending || got.WantedID != "w_1" || len(got.Files) != 1 {
		t.Fatalf("claim: %+v %v %v", got, ok, err)
	}
	if _, ok, err := s.ClaimImport(ctx, time.Hour); err != nil || ok {
		t.Fatalf("claimed twice within its lease: %v %v", ok, err)
	}

	got.State, got.Attempts, got.Detail, got.NextAt = ImportImported, 2, "seeding", time.Now().Add(-time.Second)
	if err := s.SaveImport(ctx, got); err != nil {
		t.Fatal(err)
	}
	again, ok, err := s.ClaimImport(ctx, time.Minute)
	if err != nil || !ok || again.State != ImportImported || again.Attempts != 2 || again.Detail != "seeding" {
		t.Fatalf("an imported row due again: %+v %v %v", again, ok, err)
	}

	again.State, again.NextAt = ImportDone, time.Now().Add(-time.Hour)
	if err := s.SaveImport(ctx, again); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.ClaimImport(ctx, time.Minute); err != nil || ok {
		t.Fatalf("a done import was claimed: %v %v", ok, err)
	}
	if err := s.SaveImport(ctx, Import{Adapter: "nzbget", ClientJobID: "missing", State: ImportDone}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("saving a missing import: %v", err)
	}
	if _, err := s.GetImport(ctx, "nzbget", "42"); err != nil {
		t.Fatal(err)
	}
}
