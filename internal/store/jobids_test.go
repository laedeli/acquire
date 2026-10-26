package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

// grabOn reserves and binds a target grab for one target to a client job, and
// dates it ago.
func grabOn(t *testing.T, s *Store, gid, titleID, target, adapter, job string, ago time.Duration) TargetGrab {
	t.Helper()
	ctx := context.Background()
	if _, err := s.ReserveTargetGrab(ctx, TargetGrab{ID: gid, TitleID: titleID, ReleaseTitle: gid}, target, []string{target}); err != nil {
		t.Fatalf("reserve %s: %v", gid, err)
	}
	g, err := s.BindTargetGrab(ctx, TargetGrab{ID: gid, Adapter: adapter, ClientJobID: job}, "grabbed")
	if err != nil {
		t.Fatalf("bind %s: %v", gid, err)
	}
	if ago > 0 {
		if _, err := s.pool.Exec(ctx, `UPDATE target_grabs SET created_at = now() - make_interval(secs => $2) WHERE id = $1`,
			gid, ago.Seconds()); err != nil {
			t.Fatal(err)
		}
	}
	return g
}

// A client gives a job id again once it forgot the download that had it: the
// id names the newest grab handed to it — a target grab or a request's — of
// that client alone, and an older grab on it is reused.
func TestAJobIDNamesTheNewestGrabHandedToIt(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	id, eps := series(t, s, 21, 3)
	grabOn(t, s, "tg_a", id, eps[0], "nzbget", "3", 10*24*time.Hour)
	grabOn(t, s, "tg_other", id, eps[2], "nzbget-2", "3", 0) // another client numbers its own jobs
	grabOn(t, s, "tg_b", id, eps[1], "nzbget", "3", 0)

	if owner, err := s.JobOwner(ctx, "nzbget", "3"); err != nil || owner.WantedID != "tg_b" {
		t.Fatalf("the job's grab: %+v %v", owner, err)
	}
	for _, c := range []struct {
		adapter, grab string
		reused        bool
	}{{"nzbget", "tg_a", true}, {"nzbget", "tg_b", false}, {"nzbget-2", "tg_other", false}, {"nzbget", "tg_unknown", false}} {
		if reused, err := s.JobReused(ctx, c.adapter, "3", c.grab); err != nil || reused != c.reused {
			t.Errorf("%s on %s/3 reused = %v %v, want %v", c.grab, c.adapter, reused, err, c.reused)
		}
	}
	if g, err := s.FindTargetGrabByJob(ctx, "nzbget", "3"); err != nil || g.ID != "tg_b" {
		t.Errorf("by job: %+v %v", g, err)
	}
	if g, err := s.FindTargetGrabByJob(ctx, "nzbget-2", "3"); err != nil || g.ID != "tg_other" {
		t.Errorf("by job of the other client: %+v %v", g, err)
	}
	if w, err := s.FindWantedByClientJob(ctx, "nzbget", "3"); err != nil || w != "tg_b" {
		t.Errorf("the wanted id of the job: %q %v", w, err)
	}
	if _, err := s.JobOwner(ctx, "nzbget", "4"); !errors.Is(err, ErrNotFound) {
		t.Errorf("a job nothing was handed to: %v", err)
	}

	// A request's grab handed to the same id after them is the job's now.
	if err := s.CreateWanted(ctx, Wanted{ID: "w_1", Title: "Film", MediaType: "movie"}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordGrab(ctx, "w_1", "nzbget", "3", "magnet:?xt=urn:btih:0"); err != nil {
		t.Fatal(err)
	}
	if w, err := s.FindWantedByClientJob(ctx, "nzbget", "3"); err != nil || w != "w_1" {
		t.Errorf("the wanted id of the job after a request's grab: %q %v", w, err)
	}
	if _, err := s.FindTargetGrabByJob(ctx, "nzbget", "3"); !errors.Is(err, ErrNotFound) {
		t.Errorf("a target grab was found for a request's job: %v", err)
	}
	if reused, _ := s.JobReused(ctx, "nzbget", "3", "tg_b"); !reused {
		t.Error("a grab older than the request's on its job id is not reused")
	}
	if w, err := s.FindWantedByClientJob(ctx, "nzbget-2", "3"); err != nil || w != "tg_other" {
		t.Errorf("the other client's job: %q %v", w, err)
	}
}

// An ended download's row is replaced by an event naming another wanted id —
// a new download on the job id —; one download's events merge as before: a
// late progress message does not resurrect it, unknown peers keep the known
// ones, a completed event pins 100 %.
func TestANewDownloadOnAJobIDGivenAgainReplacesTheEndedRow(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	up := func(d Download) Download {
		t.Helper()
		d.Adapter, d.ClientJobID = "nzbget", "3"
		got, err := s.UpsertDownload(ctx, d)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	health, seeders := int32(1000), int32(7)
	const one, three = "Show.Name.S01E01.1080p.WEB.h264-GRP", "Show.Name.S01E03.1080p.WEB.h264-GRP"

	up(Download{WantedID: "tg_a", Title: one, State: "downloading", ProgressPct: 40, BytesDone: 400, BytesTotal: 1000,
		SpeedBps: 50, Health: &health})
	a := up(Download{WantedID: "tg_a", State: "completed"})
	if a.State != "completed" || a.ProgressPct != 100 || a.BytesDone != 1000 || a.Title != one || a.Health == nil ||
		a.SpeedBps != 0 || a.FinishedAt == nil {
		t.Fatalf("A completed: %+v", a)
	}
	if late := up(Download{WantedID: "tg_a", State: "downloading", ProgressPct: 90}); late.State != "completed" || late.ProgressPct != 100 {
		t.Fatalf("a late progress message of A resurrected it: %+v", late)
	}
	if blank := up(Download{State: "downloading"}); blank.State != "completed" || blank.WantedID != "tg_a" {
		t.Fatalf("an event naming no wanted id is a new download: %+v", blank)
	}

	b := up(Download{WantedID: "tg_b", Title: three, State: "queued"})
	if b.WantedID != "tg_b" || b.Title != three || b.State != "queued" || b.ProgressPct != 0 || b.BytesDone != 0 ||
		b.BytesTotal != 0 || b.Health != nil || b.FinishedAt != nil || !b.StartedAt.After(a.StartedAt) {
		t.Fatalf("B's first event on A's ended row: %+v", b)
	}
	up(Download{WantedID: "tg_b", State: "downloading", ProgressPct: 10, BytesDone: 100, BytesTotal: 1000, Seeders: &seeders})
	b = up(Download{WantedID: "tg_b", State: "downloading", ProgressPct: 20, BytesDone: 200})
	if b.Seeders == nil || *b.Seeders != 7 || b.BytesTotal != 1000 || b.ProgressPct != 20 || b.Title != three {
		t.Fatalf("B's progress merged: %+v", b)
	}
	b = up(Download{WantedID: "tg_b", State: "completed"})
	if b.Title != three || b.ProgressPct != 100 || b.BytesDone != 1000 || b.FinishedAt == nil {
		t.Fatalf("B completed: %+v", b)
	}

	// A failed download's row is replaced too, by a completed event alike.
	up(Download{WantedID: "tg_c", Title: "x", State: "failed", Error: "par repair failed"})
	d := up(Download{WantedID: "tg_d", State: "completed", BytesTotal: 500})
	if d.WantedID != "tg_d" || d.Error != "" || d.Title != "" || d.ProgressPct != 100 || d.BytesDone != 500 || d.FinishedAt == nil {
		t.Fatalf("a completed download on a failed row: %+v", d)
	}
	// A new job's completed event alone makes a finished row.
	n, err := s.UpsertDownload(ctx, Download{Adapter: "nzbget", ClientJobID: "4", WantedID: "tg_e", State: "completed", BytesTotal: 800})
	if err != nil || n.ProgressPct != 100 || n.BytesDone != 800 || n.FinishedAt == nil {
		t.Fatalf("a new job's completed event: %+v %v", n, err)
	}
}

// A grab handed to a job id the client gave again counts against the cap
// until a download row of its own shows: the ended row of the grab before it
// under the same id is not its download.
func TestTheCapCountsAGrabOnAJobIDGivenAgain(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	id, eps := series(t, s, 22, 4)
	grabOn(t, s, "tg_a", id, eps[0], "nzbget", "3", 10*24*time.Hour)
	if _, err := s.UpsertDownload(ctx, Download{Adapter: "nzbget", ClientJobID: "3", WantedID: "tg_a", State: "completed"}); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.ActiveDownloads(ctx); n != 0 {
		t.Fatalf("an ended download counts: %d", n)
	}
	grabOn(t, s, "tg_b", id, eps[1], "nzbget", "3", 0)
	if n, _ := s.GrabsInFlight(ctx); n != 1 {
		t.Errorf("grabs in flight = %d, want B's", n)
	}
	if n, _ := s.ActiveDownloads(ctx); n != 1 {
		t.Errorf("active = %d, want B's", n)
	}
	var full *AtCapacityError
	if _, err := s.ReserveTargetGrabWithin(ctx, TargetGrab{ID: "tg_c", TitleID: id}, eps[2], eps[2:3], 1); !errors.As(err, &full) {
		t.Fatalf("a reservation past B, under a cap of 1: %v", err)
	}
	// B's own row shows it: counted once.
	if _, err := s.UpsertDownload(ctx, Download{Adapter: "nzbget", ClientJobID: "3", WantedID: "tg_b", State: "downloading"}); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.ActiveDownloads(ctx); n != 1 {
		t.Errorf("active once B reported = %d, want 1", n)
	}
	if _, err := s.ReserveTargetGrabWithin(ctx, TargetGrab{ID: "tg_c", TitleID: id}, eps[2], eps[2:3], 1); !errors.As(err, &full) {
		t.Fatalf("a reservation past B's download, under a cap of 1: %v", err)
	}
	if _, err := s.ReserveTargetGrabWithin(ctx, TargetGrab{ID: "tg_c", TitleID: id}, eps[2], eps[2:3], 2); err != nil {
		t.Fatalf("under a cap of 2: %v", err)
	}
}

// Another grab's download on the same job id is no word of a grab: a stale
// grab stays stale while the grab after it reports.
func TestAnotherGrabsDownloadIsNoWordOfAStaleGrab(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	id, eps := series(t, s, 24, 2)
	grabOn(t, s, "tg_a", id, eps[0], "nzbget", "3", 10*24*time.Hour)
	grabOn(t, s, "tg_b", id, eps[1], "nzbget", "3", 0)
	if _, err := s.pool.Exec(ctx, `UPDATE target_grabs SET updated_at = now() - interval '3 days'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertDownload(ctx, Download{Adapter: "nzbget", ClientJobID: "3", WantedID: "tg_b", State: "downloading"}); err != nil {
		t.Fatal(err)
	}
	stale, err := s.StaleTargetGrabs(ctx, 48*time.Hour)
	if err != nil || len(stale) != 1 || stale[0].ID != "tg_a" {
		t.Fatalf("stale = %+v %v, want A alone", stale, err)
	}
}

// A grab of a target on a job id the client gave again takes over the row an
// earlier grab of the same target left under it.
func TestAGrabOnAJobIDGivenAgainHoldsItsTargetsRows(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	id, eps := series(t, s, 25, 1)
	grabOn(t, s, "tg_old", id, eps[0], "nzbget", "5", 10*24*time.Hour)
	if err := s.RecordGrabFile(ctx, GrabFile{Adapter: "nzbget", ClientJobID: "5", GrabID: "tg_old", TargetID: eps[0],
		State: "failed", Path: "/downloads/old.mkv", Detail: "refused"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FreeTargetGrab(ctx, "tg_old", "given up"); err != nil {
		t.Fatal(err)
	}
	grabOn(t, s, "tg_new", id, eps[0], "nzbget", "5", 0)
	files, _ := s.GrabFiles(ctx, "tg_new")
	if len(files) != 1 || files[0].State != "expected" || files[0].Path != "" || files[0].Detail != "" {
		t.Fatalf("the new grab's rows: %+v", files)
	}
	if old, _ := s.GrabFiles(ctx, "tg_old"); len(old) != 0 {
		t.Errorf("the old grab still holds %+v", old)
	}
}

// The import of a job id the client gave again: another grab's import that
// is not finished keeps its row, saying whose it is; a finished one gives way
// to the new grab's, pending with its files, as new; the new grab's repeat
// records nothing; a save of the import taken over writes nothing.
func TestAnImportOfAJobIDGivenAgain(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	a := Import{Adapter: "nzbget", ClientJobID: "3", WantedID: "tg_a", Files: []string{"/downloads/complete/acquire/Show.Name.S01E01.1080p.WEB.h264-GRP"}}
	b := Import{Adapter: "nzbget", ClientJobID: "3", WantedID: "tg_b", Files: []string{"/downloads/complete/acquire/Show.Name.S01E03.1080p.WEB.h264-GRP"}}
	if created, err := s.EnqueueImport(ctx, a); err != nil || !created {
		t.Fatalf("A: %v %v", created, err)
	}
	var busy *ImportBusyError
	for _, state := range []string{ImportPending, ImportImported} {
		if _, err := s.pool.Exec(ctx, `UPDATE imports SET state = $1, attempts = 2, detail = 'working', arrival = '/arrivals/x' WHERE wanted_id = 'tg_a'`, state); err != nil {
			t.Fatal(err)
		}
		created, err := s.EnqueueImport(ctx, b)
		if created || !errors.As(err, &busy) || !errors.Is(err, ErrImportBusy) || busy.WantedID != "tg_a" || busy.State != state {
			t.Fatalf("B while A's import is %s: %v %v", state, created, err)
		}
		if got, _ := s.GetImport(ctx, "nzbget", "3"); got.WantedID != "tg_a" || got.State != state || got.Attempts != 2 {
			t.Fatalf("A's import while %s after B: %+v", state, got)
		}
	}

	if _, err := s.pool.Exec(ctx, `UPDATE imports SET state = 'done', next_at = now() - interval '9 days',
		created_at = now() - interval '9 days' WHERE wanted_id = 'tg_a'`); err != nil {
		t.Fatal(err)
	}
	if created, err := s.EnqueueImport(ctx, b); err != nil || !created {
		t.Fatalf("B once A's import is done: %v %v", created, err)
	}
	got, err := s.GetImport(ctx, "nzbget", "3")
	if err != nil || got.WantedID != "tg_b" || got.State != ImportPending || got.Attempts != 0 || got.Detail != "" ||
		got.Arrival != "" || len(got.Files) != 1 || got.Files[0] != b.Files[0] || time.Since(got.CreatedAt) > time.Minute ||
		time.Since(got.NextAt) > time.Minute {
		t.Fatalf("B's import: %+v %v", got, err)
	}
	if created, err := s.EnqueueImport(ctx, b); err != nil || created {
		t.Fatalf("B's repeated completed event: %v %v", created, err)
	}
	// The holder of A's import, late, writes nothing over B's.
	a.State, a.Detail = ImportDone, "released"
	if err := s.SaveImport(ctx, a); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a save of the import taken over: %v", err)
	}
	if got, _ := s.GetImport(ctx, "nzbget", "3"); got.WantedID != "tg_b" || got.State != ImportPending {
		t.Fatalf("B's import after A's late save: %+v", got)
	}
	// A failed import gives way too.
	if _, err := s.pool.Exec(ctx, `UPDATE imports SET state = 'failed' WHERE wanted_id = 'tg_b'`); err != nil {
		t.Fatal(err)
	}
	if created, err := s.EnqueueImport(ctx, Import{Adapter: "nzbget", ClientJobID: "3", WantedID: "w_c"}); err != nil || !created {
		t.Fatalf("after B's import failed: %v %v", created, err)
	}
}

// The grabs whose download may have finished unseen: downloaded without an
// import of their own, and handed over with no word of them for a while —
// the grab's own download row counts, another grab's on the same job id does
// not —; at most as many as asked.
func TestUnimportedTargetGrabs(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	id, eps := series(t, s, 26, 8)
	grabOn(t, s, "tg_swallowed", id, eps[0], "nzbget", "1", 0) // downloaded, no import of its own
	grabOn(t, s, "tg_importing", id, eps[1], "nzbget", "2", 0) // downloaded, its import queued
	grabOn(t, s, "tg_quiet", id, eps[2], "nzbget", "3", 0)     // grabbed, no word for an hour
	grabOn(t, s, "tg_fresh", id, eps[3], "nzbget", "4", 0)     // grabbed just now
	grabOn(t, s, "tg_reporting", id, eps[4], "nzbget", "5", 0) // grabbed long ago, its download reporting
	grabOn(t, s, "tg_other", id, eps[5], "nzbget", "6", 0)     // grabbed long ago, another grab's row fresh on its job
	grabOn(t, s, "tg_done", id, eps[6], "nzbget", "7", 0)      // imported
	if _, err := s.ReserveTargetGrab(ctx, TargetGrab{ID: "tg_pending", TitleID: id}, eps[7], eps[7:8]); err != nil {
		t.Fatal(err)
	}
	for gid, state := range map[string]string{"tg_swallowed": GrabDownloaded, "tg_importing": GrabDownloaded, "tg_done": GrabImported} {
		if err := s.SetTargetGrabState(ctx, gid, state, state); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.EnqueueImport(ctx, Import{Adapter: "nzbget", ClientJobID: "2", WantedID: "tg_importing"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE target_grabs SET updated_at = now() - interval '1 hour' WHERE id <> 'tg_fresh'`); err != nil {
		t.Fatal(err)
	}
	for job, wanted := range map[string]string{"5": "tg_reporting", "6": "tg_newer"} {
		if _, err := s.UpsertDownload(ctx, Download{Adapter: "nzbget", ClientJobID: job, WantedID: wanted, State: "downloading"}); err != nil {
			t.Fatal(err)
		}
	}

	got, err := s.UnimportedTargetGrabs(ctx, 15*time.Minute, 10)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, g := range got {
		ids[g.ID] = true
	}
	if len(got) != 3 || !ids["tg_swallowed"] || !ids["tg_quiet"] || !ids["tg_other"] {
		t.Fatalf("unimported = %v", ids)
	}
	if two, _ := s.UnimportedTargetGrabs(ctx, 15*time.Minute, 2); len(two) != 2 {
		t.Errorf("at most 2: %d", len(two))
	}
}
