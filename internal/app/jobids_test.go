package app

import (
	"context"
	"fmt"
	"strconv"
	"testing"

	"github.com/laedeli/acquire/internal/events"
	"github.com/laedeli/acquire/internal/store"
)

// Job ids a client gives again.
//
// NZBGet numbers its jobs from 1 again when it restarts with an empty
// history, and acquire empties it by releasing every download it imported.
// The ids an earlier run of grabs got come back for new grabs, while acquire
// still holds the rows of the downloads that had them: their downloads row,
// their import, their grab.

// endedGrab is a target grab whose download on nzbget's job id job ended ten
// days ago: imported, its download row completed and its import done, its
// target held.
func endedGrab(t *testing.T, svc *Service, gid, titleID, targetID, job, release string) {
	t.Helper()
	ctx := context.Background()
	if _, err := svc.st.ReserveTargetGrab(ctx, store.TargetGrab{ID: gid, TitleID: titleID, ReleaseTitle: release},
		targetID, []string{targetID}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.st.BindTargetGrab(ctx, store.TargetGrab{ID: gid, Adapter: "nzbget", ClientJobID: job}, "grabbed"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.st.UpsertDownload(ctx, store.Download{Adapter: "nzbget", ClientJobID: job, WantedID: gid, Title: release,
		State: "completed"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.st.EnqueueImport(ctx, store.Import{Adapter: "nzbget", ClientJobID: job, WantedID: gid,
		Files: []string{"/downloads/complete/acquire/" + release}}); err != nil {
		t.Fatal(err)
	}
	if err := svc.st.HoldTarget(ctx, store.Hold{TargetID: targetID, ItemID: "item-" + gid, Source: "grab"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.st.SetTargetGrabState(ctx, gid, store.GrabImported, "1 handed over"); err != nil {
		t.Fatal(err)
	}
	appSQL(t, `UPDATE imports SET state = 'done', detail = 'released' WHERE wanted_id = $1`, gid)
	appSQL(t, `UPDATE target_grabs SET created_at = now() - interval '10 days', updated_at = now() - interval '10 days' WHERE id = $1`, gid)
	appSQL(t, `UPDATE downloads SET started_at = now() - interval '10 days', updated_at = now() - interval '10 days'
	            WHERE wanted_id = $1`, gid)
}

// A sweep of eight targets under a cap of three grabs three, though the first
// five job ids the client gives it are those of downloads that ended days
// ago: their ended rows say nothing of the new grabs.
func TestTheCapHoldsWhenTheClientNumbersItsJobsAnew(t *testing.T) {
	var releases []string
	for e := 1; e <= 8; e++ {
		releases = append(releases, fmt.Sprintf("Show.Name.S01E%02d.1080p.WEB.h264-GRP", e))
	}
	r := newTargetRig(t, releases...)
	ctx := context.Background()
	if _, err := r.svc.SaveSearchSettings(ctx, SearchSettings{PreferProtocol: "usenet", MaxConcurrentGrabs: 3, AutoGrab: true}, 0, "test"); err != nil {
		t.Fatal(err)
	}
	other, old := track(t, r.svc, 4712, "Other Show", 5, true)
	for i, ep := range old {
		endedGrab(t, r.svc, fmt.Sprintf("tg_old%d", i+1), other.ID, ep.ID, strconv.Itoa(i+1),
			fmt.Sprintf("Other.Show.S01E%02d.1080p.WEB.h264-GRP", i+1))
	}
	_, eps := track(t, r.svc, 4711, "Show Name", 8, true)
	r.gw.mu.Lock()
	r.gw.ids = []string{"1", "2", "3", "4", "5", "6", "7", "8"}
	r.gw.mu.Unlock()

	if err := r.svc.SweepBacklog(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if n := len(r.gw.added()); n != 3 {
		t.Fatalf("the sweep handed over %d grabs under a cap of 3", n)
	}
	grabbed := 0
	for _, ep := range eps {
		if target(t, r.svc, ep.ID).State == store.TargetGrabbed {
			grabbed++
		}
	}
	if grabbed != 3 {
		t.Errorf("%d targets grabbed, want 3", grabbed)
	}
	if n, err := r.svc.st.ActiveDownloads(ctx); err != nil || n != 3 {
		t.Errorf("downloads at once = %d %v, want 3", n, err)
	}
}

// The events of a job id given again: the new grab's events make the job's
// row its own, with its title; one of the old grab's, late, moves nothing
// and leaves the row alone; one naming no grab — an adopted job's — is the
// new grab's.
func TestTheEventsOfAJobIDGivenAgain(t *testing.T) {
	r := newTargetRig(t, "Show.Name.S01E03.1080p.WEB.h264-GRP")
	ctx := context.Background()
	other, old := track(t, r.svc, 4712, "Other Show", 1, true)
	endedGrab(t, r.svc, "tg_old", other.ID, old[0].ID, "3", "Other.Show.S01E01.1080p.WEB.h264-GRP")
	_, eps := track(t, r.svc, 4711, "Show Name", 3, true)
	r.gw.mu.Lock()
	r.gw.ids = []string{"3"}
	r.gw.mu.Unlock()
	b, err := r.svc.GrabTarget(ctx, eps[2].ID, "missing")
	if err != nil || b.ClientJobID != "3" {
		t.Fatalf("grab: %+v %v", b, err)
	}
	row := func() store.Download {
		t.Helper()
		d, err := r.svc.st.GetDownload(ctx, "nzbget", "3")
		if err != nil {
			t.Fatal(err)
		}
		return d
	}

	_ = r.svc.OnStarted(ctx, events.DownloadEvent{Adapter: "nzbget", ClientID: "3", WantedID: b.ID, Title: "Show Name S01E03"})
	if d := row(); d.WantedID != b.ID || d.State != "queued" || d.Title != "Show Name S01E03" || d.FinishedAt != nil {
		t.Fatalf("after B's started event: %+v", d)
	}
	size := int64(1000)
	_ = r.svc.OnProgress(ctx, events.DownloadEvent{Adapter: "nzbget", ClientID: "3", WantedID: b.ID, State: "downloading",
		Title: "Show.Name.S01E03.1080p.WEB.h264-GRP", ProgressPct: 50, Downloaded: 500, SizeBytes: &size})
	if d := row(); d.WantedID != b.ID || d.State != "downloading" || d.Title != "Show.Name.S01E03.1080p.WEB.h264-GRP" || d.ProgressPct != 50 {
		t.Fatalf("after B's progress: %+v", d)
	}

	// The old grab's events, late: its download is gone.
	_ = r.svc.OnProgress(ctx, events.DownloadEvent{Adapter: "nzbget", ClientID: "3", WantedID: "tg_old", State: "downloading", ProgressPct: 99})
	_ = r.svc.OnFailed(ctx, events.DownloadEvent{Adapter: "nzbget", ClientID: "3", WantedID: "tg_old", Error: "removed"})
	if d := row(); d.WantedID != b.ID || d.ProgressPct != 50 || d.State != "downloading" {
		t.Fatalf("the old grab's late events took the row: %+v", d)
	}
	if st := target(t, r.svc, old[0].ID); st.State != store.TargetHeld {
		t.Errorf("the old grab's target after its late failed event: %+v", st)
	}
	if st := target(t, r.svc, eps[2].ID); st.State != store.TargetGrabbed {
		t.Errorf("B's target after the old grab's late failed event: %+v", st)
	}

	// The gateway restarted and adopted the job: its events name no grab.
	_ = r.svc.OnProgress(ctx, events.DownloadEvent{Adapter: "nzbget", ClientID: "3", State: "downloading", ProgressPct: 75, Downloaded: 750})
	if d := row(); d.WantedID != b.ID || d.ProgressPct != 75 {
		t.Fatalf("an adopted job's progress: %+v", d)
	}
	if n, _ := r.svc.st.ActiveDownloads(ctx); n != 1 {
		t.Errorf("downloads at once = %d, want B's", n)
	}
}
