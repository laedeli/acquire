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
