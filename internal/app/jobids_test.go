package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/laedeli/acquire/internal/events"
	"github.com/laedeli/acquire/internal/gateway"
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
	appSQL(t, `UPDATE downloads SET started_at = now() - interval '10 days', updated_at = now() - interval '10 days',
	                   finished_at = now() - interval '10 days'
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
// B is downloaded, saying it waits. The repair leaves it waiting while A's
// import runs, and queues B's — with the files B's client reports — once
// A's is done.
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

	r.describes("3", completedView("3", three))
	if run, err := r.svc.RepairGrabs(ctx); err != nil || run.Found != 1 || run.Waiting != 1 || run.Queued != 0 {
		t.Fatalf("the repair while A's import runs: %+v %v", run, err)
	}
	if im, _ := r.svc.st.GetImport(ctx, "nzbget", "3"); im.WantedID != a.ID || im.State != store.ImportPending {
		t.Fatalf("A's import after the repair: %+v", im)
	}
	r.svc.importNext(ctx)
	if im, _ := r.svc.st.GetImport(ctx, "nzbget", "3"); im.WantedID != a.ID || im.State != store.ImportDone {
		t.Fatalf("A's import, handed over: %+v", im)
	}
	if run, err := r.svc.RepairGrabs(ctx); err != nil || run.Queued != 1 {
		t.Fatalf("the repair once A's import is done: %+v %v", run, err)
	}
	im, _ := r.svc.st.GetImport(ctx, "nzbget", "3")
	if im.WantedID != b.ID || im.State != store.ImportPending || len(im.Files) != 1 || im.Files[0] != "/downloads/"+three {
		t.Fatalf("B's import: %+v", im)
	}
	if st := target(t, r.svc, eps[2].ID); st.State != store.TargetDownloaded || !strings.Contains(st.Detail, "handing it over") {
		t.Fatalf("B's target once its import is queued: %+v", st)
	}
	r.svc.importNext(ctx)
	if st := target(t, r.svc, eps[2].ID); st.State != store.TargetHeld || st.HeldItemID != "item-S01E03" {
		t.Fatalf("B's target after its import: %+v", st)
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

// describes makes nzbget's job describe as v.
func (r *targetRig) describes(job string, v gateway.JobView) {
	r.gw.mu.Lock()
	defer r.gw.mu.Unlock()
	if r.gw.described == nil {
		r.gw.described = map[string]gateway.JobView{}
	}
	r.gw.described["nzbget/"+job] = v
}

// completedView is nzbget's job as the gateway describes it once its download
// completed into folder, as the client reports it.
func completedView(job, folder string) gateway.JobView {
	return gateway.JobView{
		Job:   gateway.Job{Adapter: "nzbget", ClientJobID: job, Title: folder, State: "completed", NativeState: "SUCCESS/ALL", ProgressPct: 100},
		Files: []string{"/downloads/" + folder},
	}
}

// writeDownload puts a finished download's file in the client's folder.
func (r *targetRig) writeDownload(t *testing.T, folder string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(r.downloads, folder), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.downloads, folder, folder+".mkv"), []byte(folder), 0o644); err != nil {
		t.Fatal(err)
	}
}

// (vi) What 0.6.1 left behind: B's completed event met A's done import on
// the job id the client gave B, queued nothing, and moved B to downloaded all
// the same — "copying into the library", for ever. The repair imports it with
// the files B's client reports, and the job's row becomes B's.
func TestTheRepairImportsADownloadedGrabWithoutAnImport(t *testing.T) {
	const one, three = "Show.Name.S01E01.1080p.WEB.h264-GRP", "Show.Name.S01E03.1080p.WEB.h264-GRP"
	r := newTargetRig(t, one, three)
	ctx := context.Background()
	_, eps := track(t, r.svc, 4711, "Show Name", 3, true)
	a := r.grabOnJob(t, eps[0], "3")
	r.finish(t, a, map[string]string{one + "/" + one + ".mkv": "one"}, one)
	r.svc.importNext(ctx)
	appSQL(t, `UPDATE target_grabs SET created_at = now() - interval '10 days' WHERE id = $1`, a.ID)
	b := r.grabOnJob(t, eps[2], "3")
	appSQL(t, `UPDATE target_grabs SET state = 'downloaded', detail = 'downloaded', updated_at = now() - interval '1 hour' WHERE id = $1`, b.ID)
	appSQL(t, `UPDATE acquisition_targets SET state = 'downloaded', detail = 'downloaded; copying into the library' WHERE grab_id = $1`, b.ID)
	r.writeDownload(t, three)
	r.describes("3", completedView("3", three))

	run, err := r.svc.RepairGrabs(ctx)
	if err != nil || run.Found != 1 || run.Queued != 1 {
		t.Fatalf("the repair: %+v %v", run, err)
	}
	im, _ := r.svc.st.GetImport(ctx, "nzbget", "3")
	if im.WantedID != b.ID || im.State != store.ImportPending || len(im.Files) != 1 || im.Files[0] != "/downloads/"+three {
		t.Fatalf("B's import: %+v", im)
	}
	if d, _ := r.svc.st.GetDownload(ctx, "nzbget", "3"); d.WantedID != b.ID || d.Title != three || d.State != "completed" {
		t.Errorf("the job's row: %+v", d)
	}
	if st := target(t, r.svc, eps[2].ID); st.State != store.TargetDownloaded || !strings.Contains(st.Detail, "handing it over") {
		t.Errorf("B's target: %+v", st)
	}
	if !r.svc.importNext(ctx) {
		t.Fatal("B's import was not due")
	}
	bodies := r.cat.bodies()
	if len(bodies) != 2 || bodies[1]["path"] != filepath.Join(r.downloads, three, three+".mkv") {
		t.Fatalf("hand-overs: %v", bodies)
	}
	if st := target(t, r.svc, eps[2].ID); st.State != store.TargetHeld {
		t.Errorf("B's target after its import: %+v", st)
	}
	if run, _ := r.svc.RepairGrabs(ctx); run.Found != 0 {
		t.Errorf("a second repair found %+v", run)
	}
}

// A grab whose completed event was lost — handed over, no word of it for a
// quarter of an hour, its job not tracked by the gateway — is imported as its
// event would have; one the gateway tracks, and one heard of just now, are
// not asked about.
func TestTheRepairImportsAGrabWhoseCompletedEventWasLost(t *testing.T) {
	const one, two, three = "Show.Name.S01E01.1080p.WEB.h264-GRP", "Show.Name.S01E02.1080p.WEB.h264-GRP", "Show.Name.S01E03.1080p.WEB.h264-GRP"
	r := newTargetRig(t, one, two, three)
	ctx := context.Background()
	_, eps := track(t, r.svc, 4711, "Show Name", 3, true)
	lost := r.grabOnJob(t, eps[0], "11")
	running := r.grabOnJob(t, eps[1], "12")
	r.grabOnJob(t, eps[2], "13")
	appSQL(t, `UPDATE target_grabs SET updated_at = now() - interval '20 minutes' WHERE id IN ($1, $2)`, lost.ID, running.ID)
	r.gw.mu.Lock()
	r.gw.jobs = []gateway.Job{{Adapter: "nzbget", ClientJobID: "12", WantedItemID: running.ID}}
	r.gw.mu.Unlock()
	for job, folder := range map[string]string{"11": one, "12": two, "13": three} {
		r.writeDownload(t, folder)
		r.describes(job, completedView(job, folder))
	}

	run, err := r.svc.RepairGrabs(ctx)
	if err != nil || run.Found != 2 || run.Queued != 1 || run.Tracked != 1 {
		t.Fatalf("the repair: %+v %v", run, err)
	}
	r.gw.mu.Lock()
	asked := append([]string(nil), r.gw.describes...)
	r.gw.mu.Unlock()
	if len(asked) != 1 || asked[0] != "nzbget/11" {
		t.Errorf("asked about %v", asked)
	}
	if im, err := r.svc.st.GetImport(ctx, "nzbget", "11"); err != nil || im.WantedID != lost.ID || im.Files[0] != "/downloads/"+one {
		t.Fatalf("the lost grab's import: %+v %v", im, err)
	}
	for i, want := range []string{store.TargetDownloaded, store.TargetGrabbed, store.TargetGrabbed} {
		if st := target(t, r.svc, eps[i].ID); st.State != want {
			t.Errorf("E%02d: %s, want %s", i+1, st.State, want)
		}
	}
}

// What the repair cannot tell it leaves as it is: a job the client does not
// know (for the stale-grab sweep), a gateway or client that cannot be asked,
// a gateway older than the question, a job not finished, one that ended with
// nothing to import.
func TestTheRepairLeavesWhatItCannotTell(t *testing.T) {
	r := newTargetRig(t, "Show.Name.S01E01.1080p.WEB.h264-GRP")
	ctx := context.Background()
	_, eps := track(t, r.svc, 4711, "Show Name", 1, true)
	g := r.grabOnJob(t, eps[0], "3")
	appSQL(t, `UPDATE target_grabs SET state = 'downloaded' WHERE id = $1`, g.ID)
	running := completedView("3", "Show.Name.S01E01.1080p.WEB.h264-GRP")
	running.State, running.Files = "downloading", nil
	failed := running
	failed.State, failed.Message = "failed", "nzbget: FAILURE/HEALTH"
	empty := completedView("3", "x")
	empty.Files = nil
	for name, c := range map[string]struct {
		status int
		view   *gateway.JobView
		want   func(RepairRun) bool
	}{
		"unknown to the client": {0, nil, func(r RepairRun) bool { return r.Unknown == 1 }},
		"a client out of reach": {502, nil, func(r RepairRun) bool { return r.Unasked == 1 }},
		"an older gateway":      {405, nil, func(r RepairRun) bool { return r.Unasked == 1 }},
		"not finished":          {0, &running, func(r RepairRun) bool { return r.Running == 1 }},
		"failed":                {0, &failed, func(r RepairRun) bool { return r.Ended == 1 }},
		"completed, no files":   {0, &empty, func(r RepairRun) bool { return r.Ended == 1 }},
	} {
		r.gw.mu.Lock()
		r.gw.describeStatus, r.gw.described = c.status, nil
		r.gw.mu.Unlock()
		if c.view != nil {
			r.describes("3", *c.view)
		}
		run, err := r.svc.RepairGrabs(ctx)
		if err != nil || run.Found != 1 || run.Queued != 0 || !c.want(run) {
			t.Errorf("%s: %+v %v", name, run, err)
		}
		if _, err := r.svc.st.GetImport(ctx, "nzbget", "3"); err == nil {
			t.Fatalf("%s: an import was queued", name)
		}
		if got, _ := r.svc.st.GetTargetGrab(ctx, g.ID); got.State != store.GrabDownloaded {
			t.Fatalf("%s: the grab is %s", name, got.State)
		}
	}
}

// The repair never gives one grab the files of another: an older grab whose
// job id the client gave to a newer one is not asked about — the job the id
// names is the newer grab's, and so are its files —, and the stale-grab sweep
// gives it up later without asking.
func TestTheRepairNeverTakesTheNewerJobsFiles(t *testing.T) {
	const one, three = "Show.Name.S01E01.1080p.WEB.h264-GRP", "Show.Name.S01E03.1080p.WEB.h264-GRP"
	r := newTargetRig(t, one, three)
	ctx := context.Background()
	_, eps := track(t, r.svc, 4711, "Show Name", 3, true)
	a := r.grabOnJob(t, eps[0], "3")
	b := r.grabOnJob(t, eps[2], "3")
	appSQL(t, `UPDATE target_grabs SET created_at = now() - interval '10 days' WHERE id = $1`, a.ID)
	appSQL(t, `UPDATE target_grabs SET state = 'downloaded' WHERE id IN ($1, $2)`, a.ID, b.ID)
	r.writeDownload(t, three)
	r.describes("3", completedView("3", three))

	run, err := r.svc.RepairGrabs(ctx)
	if err != nil || run.Found != 2 || run.Reused != 1 || run.Queued != 1 {
		t.Fatalf("the repair: %+v %v", run, err)
	}
	if im, _ := r.svc.st.GetImport(ctx, "nzbget", "3"); im.WantedID != b.ID || im.Files[0] != "/downloads/"+three {
		t.Fatalf("the job's import: %+v", im)
	}
	if st := target(t, r.svc, eps[0].ID); st.State != store.TargetGrabbed || st.GrabID != a.ID {
		t.Errorf("A's target: %+v", st)
	}

	appSQL(t, `UPDATE target_grabs SET updated_at = now() - interval '3 days' WHERE id = $1`, a.ID)
	r.gw.mu.Lock()
	asked := len(r.gw.describes)
	r.gw.mu.Unlock()
	if freed, err := r.svc.SweepStaleGrabs(ctx); err != nil || freed != 1 {
		t.Fatalf("the stale-grab sweep freed %d: %v", freed, err)
	}
	if got, _ := r.svc.st.GetTargetGrab(ctx, a.ID); got.State != store.GrabFailed {
		t.Errorf("A after the sweep: %+v", got)
	}
	if st := target(t, r.svc, eps[0].ID); st.State != store.TargetWanted {
		t.Errorf("A's target after the sweep: %+v", st)
	}
	r.gw.mu.Lock()
	defer r.gw.mu.Unlock()
	if len(r.gw.describes) != asked {
		t.Errorf("the sweep asked about A's job: %v", r.gw.describes[asked:])
	}
}

// (vii) A grab nothing was heard of for two days whose download completed
// unseen is imported by the stale-grab sweep, not given up; one whose client
// does not know its job is given up as before.
func TestAStaleGrabWhoseDownloadCompletedIsImported(t *testing.T) {
	const one, two = "Show.Name.S01E01.1080p.WEB.h264-GRP", "Show.Name.S01E02.1080p.WEB.h264-GRP"
	r := newTargetRig(t, one, two)
	ctx := context.Background()
	_, eps := track(t, r.svc, 4711, "Show Name", 2, true)
	done := r.grabOnJob(t, eps[0], "21")
	lost := r.grabOnJob(t, eps[1], "22")
	appSQL(t, `UPDATE target_grabs SET updated_at = now() - interval '3 days'`)
	r.writeDownload(t, one)
	r.describes("21", completedView("21", one))

	freed, err := r.svc.SweepStaleGrabs(ctx)
	if err != nil || freed != 1 {
		t.Fatalf("the sweep freed %d: %v", freed, err)
	}
	if got, _ := r.svc.st.GetTargetGrab(ctx, done.ID); got.State != store.GrabDownloaded {
		t.Fatalf("the grab whose download completed: %+v", got)
	}
	if im, err := r.svc.st.GetImport(ctx, "nzbget", "21"); err != nil || im.WantedID != done.ID || im.State != store.ImportPending ||
		im.Files[0] != "/downloads/"+one {
		t.Fatalf("its import: %+v %v", im, err)
	}
	if got, _ := r.svc.st.GetTargetGrab(ctx, lost.ID); got.State != store.GrabFailed {
		t.Errorf("the grab its client does not know: %+v", got)
	}
	r.svc.importNext(ctx)
	if st := target(t, r.svc, eps[0].ID); st.State != store.TargetHeld {
		t.Errorf("the target of the download that completed: %+v", st)
	}
	if st := target(t, r.svc, eps[1].ID); st.State != store.TargetWanted || !strings.Contains(st.Detail, "given up") {
		t.Errorf("the target of the one given up: %+v", st)
	}
}

// One repair asks about at most repairBatch grabs; shortly after acquire
// starts, it runs once by itself.
func TestTheRepairIsBoundedAndRunsAfterTheStart(t *testing.T) {
	r := newTargetRig(t, "Show.Name.S01E01.1080p.WEB.h264-GRP")
	ctx := context.Background()
	title, eps := track(t, r.svc, 4711, "Show Name", 1, true)
	appSQL(t, `INSERT INTO target_grabs (id, title_id, target_ids, adapter, client_job_id, state, updated_at, release_title)
		SELECT 'tg_stuck' || n, $1, ARRAY[$2], 'nzbget', (1000 + n)::text, 'downloaded', now() - make_interval(mins => n), $4
		  FROM generate_series(1, $3::int) AS n`, title.ID, eps[0].ID, repairBatch+5, "Show.Name.S01E01.1080p.WEB.h264-GRP")
	run, err := r.svc.RepairGrabs(ctx)
	if err != nil || run.Found != repairBatch || run.Unknown != repairBatch {
		t.Fatalf("one run: %+v %v", run, err)
	}

	appSQL(t, `DELETE FROM target_grabs WHERE id LIKE 'tg_stuck%' AND id <> 'tg_stuck1'`)
	r.svc.repairStart = 10 * time.Millisecond
	r.describes("1001", completedView("1001", "Show.Name.S01E01.1080p.WEB.h264-GRP"))
	r.writeDownload(t, "Show.Name.S01E01.1080p.WEB.h264-GRP")
	finished := make(chan struct{})
	go func() {
		r.svc.RepairAtStart(ctx)
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(10 * time.Second):
		t.Fatal("the repair after the start did not run")
	}
	if im, err := r.svc.st.GetImport(ctx, "nzbget", "1001"); err != nil || im.WantedID != "tg_stuck1" {
		t.Fatalf("the repair after the start: %+v %v", im, err)
	}
}

// A gateway that does not know a stale grab's client — restarted, and not
// told its clients yet — gives nothing up while acquire has that client; one
// acquire has disabled, and so does not tell the gateway of, is given up as
// before.
func TestAStaleGrabOfAClientTheGatewayDoesNotKnow(t *testing.T) {
	r := newTargetRig(t, "Show.Name.S01E01.1080p.WEB.h264-GRP")
	ctx := context.Background()
	_, eps := track(t, r.svc, 4711, "Show Name", 1, true)
	g := r.grabOnJob(t, eps[0], "31")
	appSQL(t, `UPDATE target_grabs SET updated_at = now() - interval '3 days'`)
	r.gw.mu.Lock()
	r.gw.describeStatus = http.StatusNotFound
	r.gw.mu.Unlock()
	if freed, err := r.svc.SweepStaleGrabs(ctx); err != nil || freed != 0 {
		t.Fatalf("with the client in acquire: freed %d %v", freed, err)
	}
	if got, _ := r.svc.st.GetTargetGrab(ctx, g.ID); got.State != store.GrabGrabbed {
		t.Fatalf("given up on a guess: %+v", got)
	}
	appSQL(t, `UPDATE download_clients SET enabled = false WHERE id = 'nzbget'`)
	if freed, err := r.svc.SweepStaleGrabs(ctx); err != nil || freed != 1 {
		t.Fatalf("with the client disabled: freed %d %v", freed, err)
	}
}

// The repair counts what it did, not what it tried: a grab that moved on
// while its client was asked — imported meanwhile, or its import queued by
// another run — is not counted as queued.
func TestTheRepairCountsWhatItDid(t *testing.T) {
	const one = "Show.Name.S01E01.1080p.WEB.h264-GRP"
	for name, meanwhile := range map[string]func(r *targetRig, g store.TargetGrab){
		"imported meanwhile": func(r *targetRig, g store.TargetGrab) {
			appSQL(t, `UPDATE target_grabs SET state = 'imported' WHERE id = $1`, g.ID)
		},
		"queued by another run": func(r *targetRig, g store.TargetGrab) {
			if _, err := r.svc.st.EnqueueImport(context.Background(), store.Import{Adapter: "nzbget", ClientJobID: "3",
				WantedID: g.ID, Files: []string{"/downloads/" + one}}); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := newTargetRig(t, one)
			ctx := context.Background()
			_, eps := track(t, r.svc, 4711, "Show Name", 1, true)
			g := r.grabOnJob(t, eps[0], "3")
			appSQL(t, `UPDATE target_grabs SET state = 'downloaded' WHERE id = $1`, g.ID)
			r.writeDownload(t, one)
			r.describes("3", completedView("3", one))
			r.gw.mu.Lock()
			r.gw.onDescribe = func(string) { meanwhile(r, g) }
			r.gw.mu.Unlock()
			run, err := r.svc.RepairGrabs(ctx)
			if err != nil || run.Found != 1 || run.Queued != 0 || run.Moved != 1 {
				t.Fatalf("the repair: %+v %v", run, err)
			}
		})
	}
}

// failedView is nzbget's job as the gateway describes it once its download
// failed.
func failedView(job, name string) gateway.JobView {
	return gateway.JobView{
		Job:     gateway.Job{Adapter: "nzbget", ClientJobID: job, Title: name, State: "failed", NativeState: "FAILURE/HEALTH"},
		Message: "nzbget: FAILURE/HEALTH",
	}
}

// A download that failed while nothing was watching — the gateway restarted
// as it failed and adopts no finished job, so no failed event comes —: the
// repair fails it as its event would have. Its row ends with the client's
// word, its target fails with its release blocked, and its slot of the
// downloads at once is free.
func TestADownloadThatFailedUnseenFreesItsSlot(t *testing.T) {
	const one = "Show.Name.S01E01.1080p.WEB.h264-GRP"
	r := newTargetRig(t, one)
	ctx := context.Background()
	_, eps := track(t, r.svc, 4711, "Show Name", 1, true)
	g := r.grabOnJob(t, eps[0], "3")
	_ = r.svc.OnProgress(ctx, events.DownloadEvent{Adapter: "nzbget", ClientID: "3", WantedID: g.ID, State: "downloading", Title: one, ProgressPct: 40})
	if n, _ := r.svc.st.ActiveDownloads(ctx); n != 1 {
		t.Fatalf("downloads at once while it runs = %d", n)
	}
	appSQL(t, `UPDATE target_grabs SET updated_at = now() - interval '20 minutes'`)
	appSQL(t, `UPDATE downloads SET updated_at = now() - interval '20 minutes'`)
	r.describes("3", failedView("3", one))

	run, err := r.svc.RepairGrabs(ctx)
	if err != nil || run.Found != 1 || run.Failed != 1 {
		t.Fatalf("the repair: %+v %v", run, err)
	}
	if d, _ := r.svc.st.GetDownload(ctx, "nzbget", "3"); d.State != "failed" || d.Error != "nzbget: FAILURE/HEALTH" || d.FinishedAt == nil {
		t.Errorf("the job's row: %+v", d)
	}
	st := target(t, r.svc, eps[0].ID)
	if st.State != store.TargetFailed || !strings.Contains(st.Detail, "download failed: nzbget: FAILURE/HEALTH") || st.RetryAt == nil {
		t.Errorf("the target: %+v", st)
	}
	if blocked, _ := r.svc.st.BlockedFor(ctx, eps[0].ID); blocked[one] == "" {
		t.Errorf("the failed release is not blocked: %v", blocked)
	}
	if n, _ := r.svc.st.ActiveDownloads(ctx); n != 0 {
		t.Errorf("downloads at once after the repair = %d, want 0", n)
	}
}

// The stale-grab sweep ends what it gives up: a grab whose client does not know
// its job is given up, and its download row, left running, ends with it; one
// whose job failed is failed as its event would have, its release blocked.
func TestTheStaleSweepEndsWhatItGivesUp(t *testing.T) {
	const one, two = "Show.Name.S01E01.1080p.WEB.h264-GRP", "Show.Name.S01E02.1080p.WEB.h264-GRP"
	r := newTargetRig(t, one, two)
	ctx := context.Background()
	_, eps := track(t, r.svc, 4711, "Show Name", 2, true)
	lost := r.grabOnJob(t, eps[0], "41")
	failed := r.grabOnJob(t, eps[1], "42")
	for job, g := range map[string]store.TargetGrab{"41": lost, "42": failed} {
		_ = r.svc.OnProgress(ctx, events.DownloadEvent{Adapter: "nzbget", ClientID: job, WantedID: g.ID, State: "downloading", ProgressPct: 10})
	}
	appSQL(t, `UPDATE target_grabs SET updated_at = now() - interval '3 days'`)
	appSQL(t, `UPDATE downloads SET updated_at = now() - interval '3 days'`)
	r.describes("42", failedView("42", two))

	freed, err := r.svc.SweepStaleGrabs(ctx)
	if err != nil || freed != 1 {
		t.Fatalf("the sweep freed %d: %v", freed, err)
	}
	if d, _ := r.svc.st.GetDownload(ctx, "nzbget", "41"); d.State != "failed" || !strings.Contains(d.Error, "given up") {
		t.Errorf("the given-up grab's row: %+v", d)
	}
	if st := target(t, r.svc, eps[0].ID); st.State != store.TargetWanted || !strings.Contains(st.Detail, "given up") {
		t.Errorf("the given-up grab's target: %+v", st)
	}
	if got, _ := r.svc.st.GetTargetGrab(ctx, failed.ID); got.State != store.GrabFailed || got.Detail != "nzbget: FAILURE/HEALTH" {
		t.Errorf("the failed grab: %+v", got)
	}
	if st := target(t, r.svc, eps[1].ID); st.State != store.TargetFailed || st.GrabFailures != 1 {
		t.Errorf("the failed grab's target: %+v", st)
	}
	if blocked, _ := r.svc.st.BlockedFor(ctx, eps[1].ID); blocked[two] == "" {
		t.Errorf("the failed release is not blocked: %v", blocked)
	}
	if n, _ := r.svc.st.ActiveDownloads(ctx); n != 0 {
		t.Errorf("downloads at once after the sweep = %d, want 0", n)
	}
}

// The name check takes a job for its grab's however the NZB's name spells
// the release's dots, dashes and plus signs, and in any case; another
// release, a name that says less, and no name at all are not the grab's. A
// qBittorrent job is the grab's whatever its name: its id is a tag made for
// that one add.
func TestAJobIsTheGrabsByItsName(t *testing.T) {
	r := newTargetRig(t)
	ctx := context.Background()
	for _, rel := range []string{
		"Show.Name.S02E01.1080p.WEB.DD+5.1.H.264-GRP",
		"Show.Name.S02E02.1080p.AMZN.WEB-DL.DDP5.1.H.264-GRP",
		"Show.Name.2024.S01E05.2160p.WEB-DL.DD+5.1.Atmos.H.265-GRP",
		"Show.Name.S02E03.720p.HDTV.x264-GRP",
		"Show.Name.S10E04.1080p.WEB.h264-GRP[ab]",
	} {
		g := store.TargetGrab{ID: "tg_x", Adapter: "nzbget", ClientJobID: "7", ReleaseTitle: rel}
		for _, name := range []string{
			rel, strings.ToLower(rel), strings.ReplaceAll(rel, ".", " "), strings.ReplaceAll(rel, "+", ""), strings.ReplaceAll(rel, ".", "_"),
			jobName("", []string{"/downloads/complete/acquire/" + rel}), jobName("", []string{"/downloads/complete/acquire/" + rel + "/"}),
		} {
			if !r.svc.jobIsGrabs(ctx, g, name) {
				t.Errorf("%q is not taken for %s", name, rel)
			}
		}
	}
	g := store.TargetGrab{ID: "tg_x", Adapter: "nzbget", ClientJobID: "7", ReleaseTitle: "Show.Name.S02E01.1080p.WEB.DD+5.1.H.264-GRP"}
	for _, name := range []string{
		"Show.Name.S02E02.1080p.WEB.DD+5.1.H.264-GRP", "Show.Name.S02E01.1080p.WEB.DD+5.1.H.264-OTHER", "Show Name S02E01", "",
	} {
		if r.svc.jobIsGrabs(ctx, g, name) {
			t.Errorf("%q is taken for %s", name, g.ReleaseTitle)
		}
	}
	if _, err := r.svc.CreateClient(ctx, ClientInput{Type: "qbittorrent", BaseURL: "http://torrents:8080", Auth: "none",
		RemotePath: "/downloads", LocalPath: r.downloads}, "admin"); err != nil {
		t.Fatal(err)
	}
	if qb := (store.TargetGrab{ID: "tg_q", Adapter: "qbittorrent", ClientJobID: "dlg-1", ReleaseTitle: "Show.Name.S02E01.1080p.WEB.h264-GRP"}); !r.svc.jobIsGrabs(ctx, qb, "[GRP] Show Name - 01 (1080p)") {
		t.Error("a qBittorrent job is not taken for its grab's")
	}
}

// What a client restart left: five downloads on nzbget's jobs 1 to 5,
// imported and released ten days ago, and five grabs since on the same ids,
// downloaded with no import of their own — their releases named with things
// like DD+5.1, their folders the releases' names. The repair queues the five
// imports.
func TestTheRepairQueuesTheFiveTheRestartLeftStuck(t *testing.T) {
	releases := []string{
		"Show.Name.S01E01.1080p.WEB.DD+5.1.H.264-GRP",
		"Show.Name.S01E02.1080p.AMZN.WEB-DL.DDP5.1.H.264-GRP",
		"Show.Name.S01E03.1080p.WEB-DL.DD+5.1.Atmos.H.264-GRP",
		"Show.Name.S01E04.1080p.WEB.DD+2.0.H.264-GRP",
		"Show.Name.S01E05.1080p.WEB.DD+5.1.H.264-GRP",
	}
	r := newTargetRig(t, releases...)
	ctx := context.Background()
	other, old := track(t, r.svc, 4712, "Other Show", 5, true)
	for i, ep := range old {
		endedGrab(t, r.svc, fmt.Sprintf("tg_old%d", i+1), other.ID, ep.ID, strconv.Itoa(i+1),
			fmt.Sprintf("Other.Show.S01E%02d.1080p.WEB.h264-GRP", i+1))
	}
	_, eps := track(t, r.svc, 4711, "Show Name", 5, true)
	var stuck []store.TargetGrab
	for i, ep := range eps {
		g := r.grabOnJob(t, ep, strconv.Itoa(i+1))
		if g.ReleaseTitle != releases[i] {
			t.Fatalf("grabbed %q for E%02d", g.ReleaseTitle, i+1)
		}
		appSQL(t, `UPDATE target_grabs SET state = 'downloaded', detail = 'downloaded' WHERE id = $1`, g.ID)
		appSQL(t, `UPDATE acquisition_targets SET state = 'downloaded', detail = 'downloaded; copying into the library' WHERE grab_id = $1`, g.ID)
		view := completedView(strconv.Itoa(i+1), releases[i])
		view.Files = []string{"/downloads/complete/acquire/" + releases[i]}
		r.describes(strconv.Itoa(i+1), view)
		stuck = append(stuck, g)
	}

	run, err := r.svc.RepairGrabs(ctx)
	if err != nil || run.Found != 5 || run.Queued != 5 || run.Foreign != 0 {
		t.Fatalf("the repair: %+v %v", run, err)
	}
	for i, g := range stuck {
		im, err := r.svc.st.GetImport(ctx, "nzbget", strconv.Itoa(i+1))
		if err != nil || im.WantedID != g.ID || im.State != store.ImportPending || im.Files[0] != "/downloads/complete/acquire/"+releases[i] {
			t.Errorf("job %d's import: %+v %v", i+1, im, err)
		}
	}
}

// Grab A is downloaded with no import on nzbget's job 7; A's record went and
// NZBGet restarted; B's add got id 7, but its answer was lost, so acquire
// recorded no job for B. Job 7 is B's, named after B's release. The repair,
// B's completed event — the gateway adopted B's job and names no grab — and
// the stale-grab sweep each take nothing of it for A: A's own download is
// gone, so A is given up, its row ended, and its target is wanted again —
// nothing blocked —, its detail saying why, for the sweep to grab again.
func TestAJobAnUnrecordedHandOverHoldsIsNotTheOlderGrabs(t *testing.T) {
	const one, three = "Show.Name.S01E01.1080p.WEB.h264-GRP", "Show.Name.S01E03.1080p.WEB.h264-GRP"
	setup := func(t *testing.T) (*targetRig, store.TargetGrab, []store.Target) {
		t.Helper()
		r := newTargetRig(t, one, three)
		_, eps := track(t, r.svc, 4711, "Show Name", 3, true)
		a := r.grabOnJob(t, eps[0], "7")
		appSQL(t, `UPDATE target_grabs SET state = 'downloaded', created_at = now() - interval '10 days' WHERE id = $1`, a.ID)
		appSQL(t, `UPDATE acquisition_targets SET state = 'downloaded' WHERE grab_id = $1`, a.ID)
		r.gw.mu.Lock()
		r.gw.addDrop = true
		r.gw.mu.Unlock()
		_, _ = r.svc.GrabTarget(context.Background(), eps[2].ID, "missing")
		if target(t, r.svc, eps[2].ID).ReservedBy == "" {
			t.Fatal("B is not pending")
		}
		r.writeDownload(t, three)
		r.describes("7", completedView("7", three))
		return r, a, eps
	}
	givenUp := func(t *testing.T, r *targetRig, a store.TargetGrab, eps []store.Target) {
		t.Helper()
		ctx := context.Background()
		if _, err := r.svc.st.GetImport(ctx, "nzbget", "7"); err == nil {
			t.Fatal("B's job was queued for A")
		}
		const why = "nzbget holds another download under its job id 7 (" + three + ")"
		got, _ := r.svc.st.GetTargetGrab(ctx, a.ID)
		if got.State != store.GrabFailed || !strings.Contains(got.Detail, why) {
			t.Fatalf("A: %+v", got)
		}
		st := target(t, r.svc, eps[0].ID)
		if st.State != store.TargetWanted || !strings.Contains(st.Detail, why) || !strings.Contains(st.Detail, "wanted again") ||
			st.RetryAt != nil || st.GrabFailures != 0 {
			t.Errorf("A's target: %+v", st)
		}
		if blocked, _ := r.svc.st.BlockedFor(ctx, eps[0].ID); len(blocked) != 0 {
			t.Errorf("A's release was blocked: %v", blocked)
		}
		if miss, _ := r.svc.st.Missing(ctx, 10); len(miss) == 0 {
			t.Error("A's target is not missing again")
		}
	}

	t.Run("the repair", func(t *testing.T) {
		r, a, eps := setup(t)
		run, err := r.svc.RepairGrabs(context.Background())
		if err != nil || run.Found != 1 || run.Foreign != 1 || run.Queued != 0 {
			t.Fatalf("the repair: %+v %v", run, err)
		}
		givenUp(t, r, a, eps)
		if run, _ := r.svc.RepairGrabs(context.Background()); run.Found != 0 {
			t.Errorf("A is looked at again: %+v", run)
		}
	})
	t.Run("the adopted job's completed event", func(t *testing.T) {
		r, a, eps := setup(t)
		if err := r.svc.OnCompleted(context.Background(), events.DownloadEvent{Adapter: "nzbget", ClientID: "7",
			Files: []string{"/downloads/" + three}}); err != nil {
			t.Fatal(err)
		}
		givenUp(t, r, a, eps)
		if d, _ := r.svc.st.GetDownload(context.Background(), "nzbget", "7"); d.WantedID == a.ID {
			t.Errorf("B's row was taken for A's: %+v", d)
		}
	})
	t.Run("the stale-grab sweep", func(t *testing.T) {
		r, a, eps := setup(t)
		appSQL(t, `UPDATE target_grabs SET updated_at = now() - interval '3 days' WHERE id = $1`, a.ID)
		if freed, err := r.svc.SweepStaleGrabs(context.Background()); err != nil || freed != 0 {
			t.Fatalf("the stale-grab sweep freed %d: %v", freed, err)
		}
		givenUp(t, r, a, eps)
	})
}

// An adopted job's completed event names no grab: it is taken for the grab
// its job id names when the job is named after that grab's release.
func TestAnAdoptedJobsCompletedEventIsItsGrabsByName(t *testing.T) {
	const one = "Show.Name.S01E01.1080p.WEB.h264-GRP"
	r := newTargetRig(t, one)
	ctx := context.Background()
	_, eps := track(t, r.svc, 4711, "Show Name", 1, true)
	g := r.grabOnJob(t, eps[0], "8")
	r.writeDownload(t, one)
	if err := r.svc.OnCompleted(ctx, events.DownloadEvent{Adapter: "nzbget", ClientID: "8", Files: []string{"/downloads/" + one}}); err != nil {
		t.Fatal(err)
	}
	if im, err := r.svc.st.GetImport(ctx, "nzbget", "8"); err != nil || im.WantedID != g.ID {
		t.Fatalf("the adopted job's import: %+v %v", im, err)
	}
	if d, _ := r.svc.st.GetDownload(ctx, "nzbget", "8"); d.WantedID != g.ID || d.State != "completed" {
		t.Errorf("the job's row: %+v", d)
	}
}

// stuckGrabs writes n grabs of title on client's jobs from first on, in state,
// with no word of them for three days.
func stuckGrabs(t *testing.T, prefix, client, state string, first, n int, title store.Title, target store.Target) {
	t.Helper()
	appSQL(t, `INSERT INTO target_grabs (id, title_id, target_ids, adapter, client_job_id, state, release_title, updated_at)
		SELECT $1 || k, $2, ARRAY[$3], $4, ($5::int + k)::text, $6, 'Show.Name.S01E01.1080p.WEB.h264-GRP', now() - interval '3 days'
		  FROM generate_series(0, $7::int - 1) AS k`, prefix, title.ID, target.ID, client, first, state, n)
}

// The stale-grab sweep asks at most repairBatch clients' answers a run — the
// grabs past that wait for the next run —, and asks a client that did not
// answer no more in the run, keeping its grabs.
func TestTheStaleSweepIsBounded(t *testing.T) {
	r := newTargetRig(t)
	ctx := context.Background()
	title, eps := track(t, r.svc, 4711, "Show Name", 1, true)
	stuckGrabs(t, "tg_lost", "nzbget", store.GrabGrabbed, 100, repairBatch+5, title, eps[0])
	if freed, err := r.svc.SweepStaleGrabs(ctx); err != nil || freed != repairBatch {
		t.Fatalf("the first sweep freed %d: %v", freed, err)
	}
	if freed, err := r.svc.SweepStaleGrabs(ctx); err != nil || freed != 5 {
		t.Fatalf("the second sweep freed %d: %v", freed, err)
	}

	stuckGrabs(t, "tg_down", "nzbget", store.GrabGrabbed, 200, 3, title, eps[0])
	stuckGrabs(t, "tg_up", "nzbget-2", store.GrabGrabbed, 300, 2, title, eps[0])
	r.gw.mu.Lock()
	r.gw.describeDown, r.gw.describes = map[string]bool{"nzbget": true}, nil
	r.gw.mu.Unlock()
	if freed, err := r.svc.SweepStaleGrabs(ctx); err != nil || freed != 2 {
		t.Fatalf("with one client down the sweep freed %d: %v", freed, err)
	}
	r.gw.mu.Lock()
	defer r.gw.mu.Unlock()
	down := 0
	for _, key := range r.gw.describes {
		if strings.HasPrefix(key, "nzbget/") {
			down++
		}
	}
	if down != 1 || len(r.gw.describes) != 3 {
		t.Errorf("asked %v, want the client that is down once", r.gw.describes)
	}
}

// The repair asks a client that did not answer no more in the run.
func TestTheRepairStopsAskingAClientThatDoesNotAnswer(t *testing.T) {
	r := newTargetRig(t)
	ctx := context.Background()
	title, eps := track(t, r.svc, 4711, "Show Name", 1, true)
	stuckGrabs(t, "tg_stuck", "nzbget", store.GrabDownloaded, 100, 4, title, eps[0])
	r.gw.mu.Lock()
	r.gw.describeDown = map[string]bool{"nzbget": true}
	r.gw.mu.Unlock()
	run, err := r.svc.RepairGrabs(ctx)
	if err != nil || run.Found != 4 || run.Unasked != 4 {
		t.Fatalf("the repair: %+v %v", run, err)
	}
	r.gw.mu.Lock()
	defer r.gw.mu.Unlock()
	if len(r.gw.describes) != 1 {
		t.Errorf("asked %v, want the client once", r.gw.describes)
	}
}

// An older grab's own late events while it is still in flight move nothing
// once a newer grab got its job id: its completed event queues no import for
// it, its failed event fails nothing, and the newer grab is left as it is.
func TestAnOlderGrabsLateEventsWhileInFlightMoveNothing(t *testing.T) {
	const one, three = "Show.Name.S01E01.1080p.WEB.h264-GRP", "Show.Name.S01E03.1080p.WEB.h264-GRP"
	r := newTargetRig(t, one, three)
	ctx := context.Background()
	_, eps := track(t, r.svc, 4711, "Show Name", 3, true)
	a := r.grabOnJob(t, eps[0], "3")
	appSQL(t, `UPDATE target_grabs SET created_at = now() - interval '10 days' WHERE id = $1`, a.ID)
	b := r.grabOnJob(t, eps[2], "3")
	r.writeDownload(t, one)

	if err := r.svc.OnCompleted(ctx, events.DownloadEvent{Adapter: "nzbget", ClientID: "3", WantedID: a.ID,
		Files: []string{"/downloads/" + one}}); err != nil {
		t.Fatal(err)
	}
	if im, err := r.svc.st.GetImport(ctx, "nzbget", "3"); err == nil {
		t.Fatalf("an import was queued for the older grab: %+v", im)
	}
	if got, _ := r.svc.st.GetTargetGrab(ctx, a.ID); got.State != store.GrabGrabbed {
		t.Errorf("the older grab after its late completed event: %+v", got)
	}
	if err := r.svc.OnFailed(ctx, events.DownloadEvent{Adapter: "nzbget", ClientID: "3", WantedID: a.ID, Error: "par repair failed"}); err != nil {
		t.Fatal(err)
	}
	if st := target(t, r.svc, eps[0].ID); st.State != store.TargetGrabbed || st.GrabFailures != 0 {
		t.Errorf("the older grab's target after its late failed event: %+v", st)
	}
	if blocked, _ := r.svc.st.BlockedFor(ctx, eps[0].ID); len(blocked) != 0 {
		t.Errorf("the older grab's release was blocked: %v", blocked)
	}
	if got, _ := r.svc.st.GetTargetGrab(ctx, b.ID); got.State != store.GrabGrabbed {
		t.Errorf("the newer grab: %+v", got)
	}
}

// A grab that is done is not failed by another download's event on its job
// id: the event is not its, and it stays as it was.
func TestAnotherDownloadsEventLeavesADoneGrabAlone(t *testing.T) {
	const one, two = "Show.Name.S01E01.1080p.WEB.h264-GRP", "Show.Name.S01E02.1080p.WEB.h264-GRP"
	r := newTargetRig(t, one)
	ctx := context.Background()
	_, eps := track(t, r.svc, 4711, "Show Name", 1, true)
	g := r.grabOnJob(t, eps[0], "9")
	r.finish(t, g, map[string]string{one + "/" + one + ".mkv": "one"}, one)
	r.svc.importNext(ctx)
	if got, _ := r.svc.st.GetTargetGrab(ctx, g.ID); got.State != store.GrabImported {
		t.Fatalf("the grab: %+v", got)
	}
	if err := r.svc.OnCompleted(ctx, events.DownloadEvent{Adapter: "nzbget", ClientID: "9", Files: []string{"/downloads/" + two}}); err != nil {
		t.Fatal(err)
	}
	if got, _ := r.svc.st.GetTargetGrab(ctx, g.ID); got.State != store.GrabImported || strings.Contains(got.Detail, "holds another download") {
		t.Errorf("the done grab after another download's event: %+v", got)
	}
	if st := target(t, r.svc, eps[0].ID); st.State != store.TargetHeld {
		t.Errorf("its target: %+v", st)
	}
}

// handedNZB is the name of the NZB the last add handed over, without its
// extension: the name NZBGet gives the job.
func (r *targetRig) handedNZB(t *testing.T) string {
	t.Helper()
	adds := r.gw.added()
	if len(adds) == 0 {
		t.Fatal("no add")
	}
	name, _ := adds[len(adds)-1]["payload_name"].(string)
	return strings.TrimSuffix(name, ".nzb")
}

// A target grab's NZB is handed over under its release's name whatever the
// source calls the file — nothing, or a name of its own —, cut at 200 bytes
// as any NZB's name is; NZBGet names the job after it. A request's NZB keeps
// the source's name.
func TestATargetGrabsNZBIsNamedAfterItsRelease(t *testing.T) {
	const rel = "Show.Name.S01E03.1080p.WEB.h264-GRP"
	long := "Show.Name.S01E04." + strings.Repeat("Extended.Cut.", 16) + "1080p.WEB.h264-GRP"
	r := newTargetRig(t, rel, long)
	ctx := context.Background()
	_, eps := track(t, r.svc, 4711, "Show Name", 4, true)
	r.grabOnJob(t, eps[2], "7")
	if got := r.handedNZB(t); got != rel {
		t.Errorf("from a source that names the file nothing, the NZB is %q", got)
	}
	r.src.mu.Lock()
	r.src.disposition = `attachment; filename="98765.nzb"`
	r.src.mu.Unlock()
	r.grabOnJob(t, eps[3], "8")
	if got := r.handedNZB(t); got != sanitizeFileName(long) || len(got) != 200 {
		t.Errorf("from a source that names the file its own way, a release of %d bytes: %q", len(long), got)
	}
	w, _, err := r.svc.Request(ctx, store.Wanted{Title: "Film", Year: 2020, MediaType: "movie"}, asAdmin("user"))
	if err != nil {
		t.Fatal(err)
	}
	if err := r.svc.Grab(ctx, w.ID, r.src.srv.URL+"/api?t=get&id=n0&apikey=K", ""); err != nil {
		t.Fatal(err)
	}
	if got := r.handedNZB(t); got != "98765" {
		t.Errorf("a request's NZB is %q, want the source's name", got)
	}
}

// A grab's own NZBGet job is its, named after the release — as acquire hands
// an NZB over now — or after the grab's label, "Show Name S01E03" — as an NZB
// the source named nothing was handed over before —: an adopted job's
// completed event imports it, the repair imports it, and an adopted job's
// failed event fails it by its release.
func TestAGrabsOwnJobIsItsByEitherName(t *testing.T) {
	const rel = "Show.Name.S01E03.1080p.WEB.h264-GRP"
	setup := func(t *testing.T) (*targetRig, store.TargetGrab, store.Target) {
		t.Helper()
		r := newTargetRig(t, rel)
		_, eps := track(t, r.svc, 4711, "Show Name", 3, true)
		return r, r.grabOnJob(t, eps[2], "7"), eps[2]
	}
	for _, named := range []string{rel, "Show Name S01E03"} {
		t.Run(named+", the adopted job's completed event", func(t *testing.T) {
			r, g, _ := setup(t)
			r.writeDownload(t, named)
			if err := r.svc.OnCompleted(context.Background(), events.DownloadEvent{Adapter: "nzbget", ClientType: "nzbget", ClientID: "7",
				Files: []string{"/downloads/" + named}}); err != nil {
				t.Fatal(err)
			}
			if im, err := r.svc.st.GetImport(context.Background(), "nzbget", "7"); err != nil || im.WantedID != g.ID {
				t.Fatalf("the import: %+v %v", im, err)
			}
		})
		t.Run(named+", the repair", func(t *testing.T) {
			r, g, _ := setup(t)
			r.writeDownload(t, named)
			appSQL(t, `UPDATE target_grabs SET updated_at = now() - interval '20 minutes'`)
			r.describes("7", completedView("7", named))
			if run, err := r.svc.RepairGrabs(context.Background()); err != nil || run.Queued != 1 {
				t.Fatalf("the repair: %+v %v", run, err)
			}
			if im, err := r.svc.st.GetImport(context.Background(), "nzbget", "7"); err != nil || im.WantedID != g.ID {
				t.Fatalf("the import: %+v %v", im, err)
			}
		})
		t.Run(named+", the adopted job's failed event", func(t *testing.T) {
			r, _, ep := setup(t)
			if err := r.svc.OnFailed(context.Background(), events.DownloadEvent{Adapter: "nzbget", ClientType: "nzbget", ClientID: "7",
				Title: named, Error: "nzbget: FAILURE/HEALTH"}); err != nil {
				t.Fatal(err)
			}
			st := target(t, r.svc, ep.ID)
			if st.State != store.TargetFailed || st.RetryAt == nil || st.GrabFailures != 1 {
				t.Fatalf("the target: %+v", st)
			}
			if blocked, _ := r.svc.st.BlockedFor(context.Background(), ep.ID); blocked[rel] == "" {
				t.Errorf("the release is not blocked: %v", blocked)
			}
		})
	}
}

// The check compares names as an NZB's name is cut, at 200 bytes, on both
// sides; it does not look at a grab with no release — a link handed over by
// hand —, nor at a job of a client that gives no id again — qBittorrent's,
// a tag made for one add —; a client acquire no longer has is told by the
// type the gateway's event names.
func TestTheNameCheck(t *testing.T) {
	r := newTargetRig(t)
	ctx := context.Background()
	long := "Show.Name.S01E03." + strings.Repeat("Extended.Cut.", 16) + "1080p.WEB.h264-GRP"
	g := store.TargetGrab{ID: "tg_x", Adapter: "nzbget", ClientJobID: "7", ReleaseTitle: long}
	cut := sanitizeFileName(long)
	for _, name := range []string{cut, jobName("", []string{"/downloads/complete/acquire/" + cut}), long} {
		if !r.svc.jobIsGrabs(ctx, g, name) {
			t.Errorf("%q (%d bytes) is not taken for the release of %d bytes", name, len(name), len(long))
		}
	}
	if r.svc.jobIsGrabs(ctx, g, "Show.Name.S01E03.1080p.WEB.h264-OTHER") {
		t.Error("another release is taken for the grab's")
	}
	if !r.svc.jobIsGrabs(ctx, store.TargetGrab{ID: "tg_m", Adapter: "nzbget", ClientJobID: "7"}, "Film.2020.1080p.WEB.h264-GRP") {
		t.Error("a grab with no release is checked")
	}
	if _, err := r.svc.CreateClient(ctx, ClientInput{Type: "qbittorrent", BaseURL: "http://torrents:8080", Auth: "none",
		RemotePath: "/downloads", LocalPath: r.downloads}, "admin"); err != nil {
		t.Fatal(err)
	}
	torrent := store.TargetGrab{ID: "tg_q", Adapter: "qbittorrent", ClientJobID: "dlg-1", ReleaseTitle: long}
	if !r.svc.jobIsGrabs(ctx, torrent, "[GRP] Show Name - 03 (1080p)") {
		t.Error("a qBittorrent job is checked")
	}
	gone := store.TargetGrab{ID: "tg_g", Adapter: "usenet-old", ClientJobID: "7", ReleaseTitle: long}
	if !r.svc.jobIsGrabsOf(ctx, gone, "Other.Release.1080p.WEB.h264-GRP", "") {
		t.Error("a job of a client acquire does not know, of no type, is checked")
	}
	if r.svc.jobIsGrabsOf(ctx, gone, "Other.Release.1080p.WEB.h264-GRP", "nzbget") {
		t.Error("an NZBGet job of a client acquire does not know any more is not checked")
	}
}

// A stale grab that is imported while its client is asked about it is not
// given up from the state it was read in: it stays imported.
func TestTheStaleSweepGivesNothingUpThatMovedOnMeanwhile(t *testing.T) {
	r := newTargetRig(t, "Show.Name.S01E01.1080p.WEB.h264-GRP")
	ctx := context.Background()
	_, eps := track(t, r.svc, 4711, "Show Name", 1, true)
	g := r.grabOnJob(t, eps[0], "51")
	appSQL(t, `UPDATE target_grabs SET updated_at = now() - interval '3 days'`)
	r.gw.mu.Lock()
	r.gw.onDescribe = func(string) {
		appSQL(t, `UPDATE target_grabs SET state = 'imported', detail = '1 handed over' WHERE id = $1`, g.ID)
	}
	r.gw.mu.Unlock()
	if freed, err := r.svc.SweepStaleGrabs(ctx); err != nil || freed != 0 {
		t.Fatalf("the sweep freed %d: %v", freed, err)
	}
	if got, _ := r.svc.st.GetTargetGrab(ctx, g.ID); got.State != store.GrabImported || got.Detail != "1 handed over" {
		t.Errorf("the grab imported meanwhile: %+v", got)
	}
}

// A grab imported while the repair asks its client is not given up for
// another download's job either: it is given up only from the state it is in.
func TestTheRepairGivesNothingUpThatMovedOnMeanwhile(t *testing.T) {
	const one, three = "Show.Name.S01E01.1080p.WEB.h264-GRP", "Show.Name.S01E03.1080p.WEB.h264-GRP"
	r := newTargetRig(t, one)
	ctx := context.Background()
	_, eps := track(t, r.svc, 4711, "Show Name", 1, true)
	g := r.grabOnJob(t, eps[0], "52")
	appSQL(t, `UPDATE target_grabs SET state = 'downloaded' WHERE id = $1`, g.ID)
	r.describes("52", completedView("52", three))
	r.gw.mu.Lock()
	r.gw.onDescribe = func(string) {
		appSQL(t, `UPDATE target_grabs SET state = 'imported', detail = '1 handed over' WHERE id = $1`, g.ID)
	}
	r.gw.mu.Unlock()
	if _, err := r.svc.RepairGrabs(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := r.svc.st.GetTargetGrab(ctx, g.ID); got.State != store.GrabImported || got.Detail != "1 handed over" {
		t.Errorf("the grab imported meanwhile: %+v", got)
	}
}

// A stale grab whose job its client works on still is not lost: the sweep
// keeps it and refreshes its row from what the client says, so it is no
// longer stale and its end is learned when it comes. A job under its id named
// otherwise is another download's: the grab is given up, wanted again.
func TestTheStaleSweepKeepsAGrabItsClientWorksOn(t *testing.T) {
	const one, two = "Show.Name.S01E01.1080p.WEB.h264-GRP", "Show.Name.S01E02.1080p.WEB.h264-GRP"
	r := newTargetRig(t, one, two)
	ctx := context.Background()
	_, eps := track(t, r.svc, 4711, "Show Name", 2, true)
	slow := r.grabOnJob(t, eps[0], "61")
	other := r.grabOnJob(t, eps[1], "62")
	appSQL(t, `UPDATE target_grabs SET updated_at = now() - interval '3 days'`)
	running := func(job, name string) gateway.JobView {
		v := completedView(job, name)
		v.State, v.NativeState, v.ProgressPct, v.Downloaded, v.Files = "downloading", "DOWNLOADING", 40, 400, nil
		return v
	}
	r.describes("61", running("61", one))
	r.describes("62", running("62", "Some.Other.Download.2023.1080p.WEB.h264-GRP"))

	if freed, err := r.svc.SweepStaleGrabs(ctx); err != nil || freed != 0 {
		t.Fatalf("the sweep freed %d: %v", freed, err)
	}
	if got, _ := r.svc.st.GetTargetGrab(ctx, slow.ID); got.State != store.GrabGrabbed {
		t.Errorf("the grab still downloading: %+v", got)
	}
	if d, _ := r.svc.st.GetDownload(ctx, "nzbget", "61"); d.WantedID != slow.ID || d.State != "downloading" || d.ProgressPct != 40 {
		t.Errorf("its row: %+v", d)
	}
	stale, _ := r.svc.st.StaleTargetGrabs(ctx, 48*time.Hour)
	for _, g := range stale {
		if g.ID == slow.ID {
			t.Error("the grab still downloading is stale after the sweep")
		}
	}
	if got, _ := r.svc.st.GetTargetGrab(ctx, other.ID); got.State != store.GrabFailed {
		t.Errorf("the grab whose id holds another running download: %+v", got)
	}
	if st := target(t, r.svc, eps[1].ID); st.State != store.TargetWanted {
		t.Errorf("its target: %+v", st)
	}
}

// A grab whose download completed, and whose job its client says failed since
// — removed in the client, say —, fails nothing by its release: the repair
// leaves it, and its row says what it did, completed, not failed.
func TestAFailureAfterTheDownloadCompletedLeavesItsRow(t *testing.T) {
	const one = "Show.Name.S01E01.1080p.WEB.h264-GRP"
	r := newTargetRig(t, one)
	ctx := context.Background()
	_, eps := track(t, r.svc, 4711, "Show Name", 1, true)
	g := r.grabOnJob(t, eps[0], "71")
	if _, err := r.svc.st.UpsertDownload(ctx, store.Download{Adapter: "nzbget", ClientJobID: "71", WantedID: g.ID, Title: one,
		State: "completed"}); err != nil {
		t.Fatal(err)
	}
	appSQL(t, `UPDATE target_grabs SET state = 'downloaded' WHERE id = $1`, g.ID)
	removed := failedView("71", one)
	removed.NativeState, removed.Message = "DELETED/MANUAL", "removed: DELETED/MANUAL"
	r.describes("71", removed)

	run, err := r.svc.RepairGrabs(ctx)
	if err != nil || run.Found != 1 || run.Ended != 1 || run.Failed != 0 {
		t.Fatalf("the repair: %+v %v", run, err)
	}
	if d, _ := r.svc.st.GetDownload(ctx, "nzbget", "71"); d.State != "completed" || d.Error != "" {
		t.Errorf("its row: %+v", d)
	}
	if blocked, _ := r.svc.st.BlockedFor(ctx, eps[0].ID); len(blocked) != 0 {
		t.Errorf("its release was blocked: %v", blocked)
	}
}
