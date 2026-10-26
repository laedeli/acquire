package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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

// grabOnJob grabs a target from the missing tab; the client gives the grab
// job id job.
func (r *targetRig) grabOnJob(t *testing.T, target store.Target, job string) store.TargetGrab {
	t.Helper()
	r.gw.mu.Lock()
	r.gw.ids = []string{job}
	r.gw.mu.Unlock()
	g, err := r.svc.GrabTarget(context.Background(), target.ID, "missing")
	if err != nil || g.ClientJobID != job {
		t.Fatalf("grab of %s: %+v %v", target.ID, g, err)
	}
	return g
}

// What a client's restart leaves: grab A got nzbget's job 3, was imported and
// done ten days ago, and the client gives 3 to grab B. B counts against the
// cap; its events make the job's row its own, with its title; its completed
// event queues its import, pending, with its files, and a repeat of it queues
// nothing new; B is handed over, A left as it was.
func TestAJobIDTheClientGivesAgain(t *testing.T) {
	const one, three = "Show.Name.S01E01.1080p.WEB.h264-GRP", "Show.Name.S01E03.1080p.WEB.h264-GRP"
	r := newTargetRig(t, one, three)
	ctx := context.Background()
	_, eps := track(t, r.svc, 4711, "Show Name", 3, true)
	a := r.grabOnJob(t, eps[0], "3")
	r.finish(t, a, map[string]string{one + "/" + one + ".mkv": "one"}, one)
	r.svc.importNext(ctx)
	if im, _ := r.svc.st.GetImport(ctx, "nzbget", "3"); im.WantedID != a.ID || im.State != store.ImportDone {
		t.Fatalf("A's import: %+v", im)
	}
	appSQL(t, `UPDATE target_grabs SET created_at = now() - interval '10 days', updated_at = now() - interval '10 days' WHERE id = $1`, a.ID)
	appSQL(t, `UPDATE imports SET created_at = now() - interval '10 days', updated_at = now() - interval '10 days'`)

	b := r.grabOnJob(t, eps[2], "3")
	// (i) B counts against the cap, A's ended row under its job id or not.
	if n, err := r.svc.st.ActiveDownloads(ctx); err != nil || n != 1 {
		t.Fatalf("downloads at once with B handed over = %d %v", n, err)
	}
	// (ii) B's events make the row B's, with its title.
	_ = r.svc.OnStarted(ctx, events.DownloadEvent{Adapter: "nzbget", ClientID: "3", WantedID: b.ID, Title: "Show Name S01E03"})
	_ = r.svc.OnProgress(ctx, events.DownloadEvent{Adapter: "nzbget", ClientID: "3", WantedID: b.ID, State: "downloading",
		Title: three, ProgressPct: 60})
	if d, _ := r.svc.st.GetDownload(ctx, "nzbget", "3"); d.WantedID != b.ID || d.Title != three || d.State != "downloading" || d.ProgressPct != 60 {
		t.Fatalf("the job's row after B's events: %+v", d)
	}
	if n, _ := r.svc.st.ActiveDownloads(ctx); n != 1 {
		t.Errorf("downloads at once once B reported = %d, want 1", n)
	}
	// (iii) B's completed event queues B's import, pending, with B's files.
	r.finish(t, b, map[string]string{three + "/" + three + ".mkv": "three"}, three)
	im, err := r.svc.st.GetImport(ctx, "nzbget", "3")
	if err != nil || im.WantedID != b.ID || im.State != store.ImportPending || len(im.Files) != 1 || im.Files[0] != "/downloads/"+three ||
		im.Attempts != 0 || im.Detail != "" {
		t.Fatalf("B's import: %+v %v", im, err)
	}
	if st := target(t, r.svc, eps[2].ID); st.State != store.TargetDownloaded || !strings.Contains(st.Detail, "handing it over") {
		t.Fatalf("B's target: %+v", st)
	}
	if d, _ := r.svc.st.GetDownload(ctx, "nzbget", "3"); d.WantedID != b.ID || d.Title != three || d.State != "completed" {
		t.Fatalf("the job's row after B's completed event: %+v", d)
	}
	// (iv) A repeat of B's completed event queues nothing new.
	r.finish(t, b, nil, three)
	if again, _ := r.svc.st.GetImport(ctx, "nzbget", "3"); again.WantedID != b.ID || again.State != store.ImportPending ||
		!again.CreatedAt.Equal(im.CreatedAt) {
		t.Fatalf("B's import after a repeat: %+v", again)
	}
	if !r.svc.importNext(ctx) || r.svc.importNext(ctx) {
		t.Fatal("not exactly one import was due")
	}
	bodies := r.cat.bodies()
	if len(bodies) != 2 || bodies[1]["episodeNumber"] != float64(3) || !strings.HasSuffix(fmt.Sprint(bodies[1]["path"]), three+".mkv") {
		t.Fatalf("hand-overs: %v", bodies)
	}
	if st := target(t, r.svc, eps[2].ID); st.State != store.TargetHeld || st.HeldItemID != "item-S01E03" {
		t.Errorf("B's target after its import: %+v", st)
	}
	if st := target(t, r.svc, eps[0].ID); st.State != store.TargetHeld || st.HeldItemID != "item-S01E01" {
		t.Errorf("A's target: %+v", st)
	}
	if got, _ := r.svc.st.GetTargetGrab(ctx, a.ID); got.State != store.GrabImported {
		t.Errorf("A: %+v", got)
	}
}

// (v) B's download finishes while A's import on the same job id still runs:
// A's import is not replaced, nor B's targets moved on as if B's were queued;
// B is downloaded, saying it waits.
func TestAnImportStillRunningOnTheJobIDIsNotReplaced(t *testing.T) {
	const one, three = "Show.Name.S01E01.1080p.WEB.h264-GRP", "Show.Name.S01E03.1080p.WEB.h264-GRP"
	r := newTargetRig(t, one, three)
	ctx := context.Background()
	_, eps := track(t, r.svc, 4711, "Show Name", 3, true)
	a := r.grabOnJob(t, eps[0], "3")
	r.finish(t, a, map[string]string{one + "/" + one + ".mkv": "one"}, one)
	b := r.grabOnJob(t, eps[2], "3")

	if err := os.MkdirAll(filepath.Join(r.downloads, three), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.downloads, three, three+".mkv"), []byte("three"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := r.svc.OnCompleted(ctx, events.DownloadEvent{Adapter: "nzbget", ClientID: "3", WantedID: b.ID, Files: []string{"/downloads/" + three}})
	if !errors.Is(err, errImportWaits) {
		t.Fatalf("B's completed event while A's import runs: %v", err)
	}
	if im, _ := r.svc.st.GetImport(ctx, "nzbget", "3"); im.WantedID != a.ID || im.State != store.ImportPending || im.Files[0] != "/downloads/"+one {
		t.Fatalf("A's import after B's completed event: %+v", im)
	}
	g, _ := r.svc.st.GetTargetGrab(ctx, b.ID)
	st := target(t, r.svc, eps[2].ID)
	if g.State != store.GrabDownloaded || st.State != store.TargetDownloaded || !strings.Contains(st.Detail, "not copied into the library yet") ||
		strings.Contains(st.Detail, "copying into the library") {
		t.Fatalf("B after its completed event: %+v / %+v", g, st)
	}
}

// Cancelling a grab whose job id holds another grab's import stops nothing of
// that import.
func TestCancellingAGrabLeavesAnotherGrabsImportAlone(t *testing.T) {
	const one = "Show.Name.S01E01.1080p.WEB.h264-GRP"
	r := newTargetRig(t, one, "Show.Name.S01E03.1080p.WEB.h264-GRP")
	ctx := context.Background()
	_, eps := track(t, r.svc, 4711, "Show Name", 3, true)
	a := r.grabOnJob(t, eps[0], "3")
	r.finish(t, a, map[string]string{one + "/" + one + ".mkv": "one"}, one)
	b := r.grabOnJob(t, eps[2], "3")
	if err := r.svc.ControlDownload(ctx, "nzbget", "3", "cancel"); err != nil {
		t.Fatal(err)
	}
	if got, _ := r.svc.st.GetTargetGrab(ctx, b.ID); got.State != store.GrabFailed {
		t.Errorf("the cancelled grab: %+v", got)
	}
	if im, _ := r.svc.st.GetImport(ctx, "nzbget", "3"); im.WantedID != a.ID || im.State != store.ImportPending {
		t.Fatalf("A's import after B was cancelled: %+v", im)
	}
	if got, _ := r.svc.st.GetTargetGrab(ctx, a.ID); got.State != store.GrabDownloaded {
		t.Errorf("A after B was cancelled: %+v", got)
	}
}

// The release step lets alone what NZBGet keeps under the job id for another
// download — the id given again once this one's record went —, and a record
// it keeps no more: nothing is released, and the import is done, saying so.
func TestTheReleaseLeavesAnotherDownloadOnTheJobIDAlone(t *testing.T) {
	const rel = "Show.Name.S01E02.1080p.WEB.h264-GRP"
	for name, then := range map[string]func(r *managedRig, g store.TargetGrab){
		"another download": func(r *managedRig, g store.TargetGrab) {
			r.clients.keeps(g.ClientJobID, "/downloads/complete/Show.Name.S01E05.1080p.WEB.h264-GRP")
		},
		"no record": func(r *managedRig, g store.TargetGrab) {
			r.clients.mu.Lock()
			r.clients.history = nil
			r.clients.mu.Unlock()
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := newManagedRig(t, rel)
			ctx := context.Background()
			_, eps := track(t, r.svc, 4711, "Show Name", 2, true)
			g, err := r.svc.GrabTarget(ctx, eps[1].ID, "missing")
			if err != nil {
				t.Fatal(err)
			}
			r.done(t, g, map[string]string{rel + "/" + rel + ".mkv": "the episode"}, rel)
			then(r, g)
			r.svc.importNext(ctx)
			if r.released() != 0 {
				t.Fatalf("released %d times", r.released())
			}
			if im := r.importOf(t, "nzbget", g.ClientJobID); im.State != store.ImportDone || !strings.Contains(im.Detail, "nothing was released") {
				t.Errorf("import: %+v", im)
			}
			if st := target(t, r.svc, eps[1].ID); st.State != store.TargetHeld {
				t.Errorf("target: %+v", st)
			}
		})
	}
}

// A request's download on a job id that still holds another download's
// import, not done, fails saying why — nothing looks at it again —, and
// leaves that import alone.
func TestARequestsDownloadOnABusyJobIDFails(t *testing.T) {
	rig := newImportRig(t)
	ctx := context.Background()
	earlier := store.Import{Adapter: "nzbget", ClientJobID: "42", WantedID: "tg_earlier",
		Files: []string{"/downloads/complete/acquire/Show.Name.S01E01.1080p.WEB.h264-GRP"}}
	if _, err := rig.svc.st.EnqueueImport(ctx, earlier); err != nil {
		t.Fatal(err)
	}
	w := rig.finished(t, "nzbget", "42", map[string]string{"acquire/Film.2020/Film.2020.mkv": "the film"}, "acquire/Film.2020")
	if got := rig.wanted(t, w.ID); got.Status != "failed" || !strings.Contains(got.Detail, "gave its job id 42 to an earlier download") {
		t.Fatalf("the request: %s %q", got.Status, got.Detail)
	}
	if im := rig.importOf(t, "nzbget", "42"); im.WantedID != "tg_earlier" || im.State != store.ImportPending {
		t.Errorf("the earlier import: %+v", im)
	}
}
