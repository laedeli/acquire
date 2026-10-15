package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/laedeli/acquire/internal/events"
	"github.com/laedeli/acquire/internal/gateway"
	"github.com/laedeli/acquire/internal/store"
)

// A completed event for a grab that failed — its download held no video, its
// download failed — leaves its targets failed, for their recovery: it does
// not move them back to downloaded, where nothing would take them on.
func TestACompletedEventLeavesFailedTargetsFailed(t *testing.T) {
	t.Run("repeated after its import failed", func(t *testing.T) {
		r := newTargetRig(t, "Show.Name.S01E01.1080p.WEB-DL.x265-G")
		ctx := context.Background()
		_, eps := track(t, r.svc, 4711, "Show Name", 1, true)
		g, err := r.svc.GrabTarget(ctx, eps[0].ID, "missing")
		if err != nil {
			t.Fatal(err)
		}
		files := map[string]string{"Show.Name.S01E01/Show.Name.S01E01.nfo": "info"}
		r.finish(t, g, files, "Show.Name.S01E01")
		r.svc.importNext(ctx)
		if st := target(t, r.svc, eps[0].ID); st.State != store.TargetFailed || !strings.Contains(st.Detail, "no video file") {
			t.Fatalf("after an import without a video: %+v", st)
		}
		r.finish(t, g, files, "Show.Name.S01E01")
		if st := target(t, r.svc, eps[0].ID); st.State != store.TargetFailed {
			t.Fatalf("a repeated completed event moved it back: %+v", st)
		}
	})
	t.Run("after a failed event", func(t *testing.T) {
		r := newTargetRig(t, "Show.Name.S01E01.1080p.WEB-DL.x265-G")
		ctx := context.Background()
		_, eps := track(t, r.svc, 4711, "Show Name", 1, true)
		g, err := r.svc.GrabTarget(ctx, eps[0].ID, "missing")
		if err != nil {
			t.Fatal(err)
		}
		_ = r.svc.OnFailed(ctx, events.DownloadEvent{Adapter: g.Adapter, ClientID: g.ClientJobID, WantedID: g.ID, Error: "par repair failed"})
		r.finish(t, g, map[string]string{"Show.Name.S01E01.mkv": "e"}, "Show.Name.S01E01.mkv")
		if st := target(t, r.svc, eps[0].ID); st.State != store.TargetFailed {
			t.Fatalf("a completed event after the failure moved it: %+v", st)
		}
		if r.svc.importNext(ctx) {
			t.Error("a failed grab's download was queued for import")
		}
	})
}

// A failed event moves only what is the grab's and in flight: an episode a
// reconcile held meanwhile stays held, and one the library held and lost
// meanwhile — wanted again, its grab still recorded on it — stays wanted.
func TestAFailedEventMovesOnlyWhatIsInFlight(t *testing.T) {
	r := newTargetRig(t, "Show.Name.S01E01E02E03.1080p.WEB-DL-G")
	ctx := context.Background()
	_, eps := track(t, r.svc, 4711, "Show Name", 3, true)
	g, err := r.svc.GrabTarget(ctx, eps[0].ID, "missing")
	if err != nil || len(g.TargetIDs) != 3 {
		t.Fatalf("grab: %+v %v", g, err)
	}
	_ = r.svc.st.HoldTarget(ctx, store.Hold{TargetID: eps[1].ID, ItemID: "found-meanwhile", Source: "derived"})
	_ = r.svc.st.HoldTarget(ctx, store.Hold{TargetID: eps[2].ID, ItemID: "lost-meanwhile", Source: "derived"})
	_ = r.svc.OnItemRemoved(ctx, "lost-meanwhile")
	if st := target(t, r.svc, eps[2].ID); st.State != store.TargetWanted || st.GrabID != g.ID {
		t.Fatalf("set-up: %+v", st)
	}
	_ = r.svc.OnFailed(ctx, events.DownloadEvent{Adapter: g.Adapter, ClientID: g.ClientJobID, WantedID: g.ID, Error: "x"})
	if st := target(t, r.svc, eps[1].ID); st.State != store.TargetHeld {
		t.Errorf("a held episode was failed: %+v", st)
	}
	if st := target(t, r.svc, eps[2].ID); st.State != store.TargetWanted {
		t.Errorf("an episode the grab no longer answers for was failed: %+v", st)
	}
	if st := target(t, r.svc, eps[0].ID); st.State != store.TargetFailed {
		t.Errorf("the grab's own episode: %+v", st)
	}
}

// A target its download failed is wanted again after a wait, six hours the
// first time, the release that failed it blocked: the next grab takes
// another.
func TestAFailedDownloadIsTriedAgainAfterABackoff(t *testing.T) {
	r := newTargetRig(t, "Show.Name.S01E01.1080p.WEB-DL.x265-A", "Show.Name.S01E01.720p.WEB-DL.x265-B")
	ctx := context.Background()
	_, eps := track(t, r.svc, 4711, "Show Name", 1, true)
	g, err := r.svc.GrabTarget(ctx, eps[0].ID, "missing")
	if err != nil {
		t.Fatal(err)
	}
	_ = r.svc.OnFailed(ctx, events.DownloadEvent{Adapter: g.Adapter, ClientID: g.ClientJobID, WantedID: g.ID, Error: "par repair failed"})
	st := target(t, r.svc, eps[0].ID)
	if st.State != store.TargetFailed || st.GrabFailures != 1 || st.RetryAt == nil || time.Until(*st.RetryAt) < 5*time.Hour {
		t.Fatalf("after the failure: %+v", st)
	}
	if err := r.svc.OnScheduleDue(ctx, "grab-recovery"); err != nil {
		t.Fatal(err)
	}
	if target(t, r.svc, eps[0].ID).State != store.TargetFailed {
		t.Fatal("tried again before its wait ran out")
	}
	appSQL(t, `UPDATE acquisition_targets SET retry_at = now() - interval '1 second' WHERE id = $1`, eps[0].ID)
	if err := r.svc.OnScheduleDue(ctx, "grab-recovery"); err != nil {
		t.Fatal(err)
	}
	if st := target(t, r.svc, eps[0].ID); st.State != store.TargetWanted || !strings.Contains(st.Detail, "wanted again") {
		t.Fatalf("after its wait: %+v", st)
	}
	if due, _ := r.svc.st.DueTargets(ctx, 10); len(due) != 1 {
		t.Errorf("not due for the sweep again: %+v", due)
	}
	again, err := r.svc.GrabTarget(ctx, eps[0].ID, "sweep")
	if err != nil || again.ReleaseTitle != "Show.Name.S01E01.720p.WEB-DL.x265-B" {
		t.Fatalf("the second grab took %q (%v), want the other release", again.ReleaseTitle, err)
	}
}

// Cancelling a target grab's download fails the grab and frees its targets
// at once, the release blocked for them; a failed event the gateway sends
// after it changes nothing.
func TestCancellingATargetGrabFreesItsTargets(t *testing.T) {
	r := newTargetRig(t, "Show.Name.S01E01.1080p.WEB-DL.x265-G")
	ctx := context.Background()
	_, eps := track(t, r.svc, 4711, "Show Name", 1, true)
	g, err := r.svc.GrabTarget(ctx, eps[0].ID, "missing")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.svc.ControlDownload(ctx, g.Adapter, g.ClientJobID, "cancel"); err != nil {
		t.Fatal(err)
	}
	st := target(t, r.svc, eps[0].ID)
	if st.State != store.TargetWanted || !strings.Contains(st.Detail, "cancelled") {
		t.Fatalf("after the cancel: %+v", st)
	}
	if got, _ := r.svc.st.GetTargetGrab(ctx, g.ID); got.State != store.GrabFailed {
		t.Errorf("grab: %+v", got)
	}
	if blocked, _ := r.svc.st.BlockedFor(ctx, eps[0].ID); blocked["Show.Name.S01E01.1080p.WEB-DL.x265-G"] == "" {
		t.Errorf("the cancelled release is not blocked: %v", blocked)
	}
	_ = r.svc.OnFailed(ctx, events.DownloadEvent{Adapter: g.Adapter, ClientID: g.ClientJobID, WantedID: g.ID, Error: "removed"})
	if st := target(t, r.svc, eps[0].ID); st.State != store.TargetWanted || st.GrabFailures != 0 {
		t.Fatalf("a failed event after the cancel: %+v", st)
	}
}

// A grab nothing was heard of for two days that the gateway does not know is
// given up and its targets freed — a pending one whose hand-over died too;
// one the gateway still has is left alone, and a gateway that cannot be asked
// gives nothing up.
func TestAStaleGrabTheGatewayDoesNotKnowIsFreed(t *testing.T) {
	r := newTargetRig(t, "Show.Name.S01E01.1080p.WEB-DL.x265-G", "Show.Name.S01E02.1080p.WEB-DL.x265-G",
		"Show.Name.S01E03.1080p.WEB-DL.x265-G")
	ctx := context.Background()
	_, eps := track(t, r.svc, 4711, "Show Name", 3, true)
	lost, err := r.svc.GrabTarget(ctx, eps[0].ID, "missing")
	if err != nil {
		t.Fatal(err)
	}
	running, err := r.svc.GrabTarget(ctx, eps[1].ID, "missing")
	if err != nil {
		t.Fatal(err)
	}
	// A hand-over that died with its reservation taken.
	if _, err := r.svc.st.ReserveTargetGrab(ctx, store.TargetGrab{ID: "tg_crashed", TitleID: eps[2].TitleID}, eps[2].ID, []string{eps[2].ID}); err != nil {
		t.Fatal(err)
	}
	r.gw.mu.Lock()
	r.gw.jobs = []gateway.Job{{Adapter: running.Adapter, ClientJobID: running.ClientJobID}}
	r.gw.mu.Unlock()
	appSQL(t, `UPDATE target_grabs SET updated_at = now() - interval '3 days'`)

	gw := r.svc.gw
	r.svc.gw = gateway.New("http://127.0.0.1:1", nil)
	if err := r.svc.OnScheduleDue(ctx, "grab-recovery"); err == nil {
		t.Error("a gateway that cannot be asked was taken for one that knows nothing")
	}
	if st := target(t, r.svc, eps[0].ID); st.State != store.TargetGrabbed {
		t.Fatalf("given up on a guess: %+v", st)
	}
	r.svc.gw = gw
	if err := r.svc.OnScheduleDue(ctx, "grab-recovery"); err != nil {
		t.Fatal(err)
	}
	if st := target(t, r.svc, eps[0].ID); st.State != store.TargetWanted || !strings.Contains(st.Detail, "given up") {
		t.Errorf("the lost grab's target: %+v", st)
	}
	if got, _ := r.svc.st.GetTargetGrab(ctx, lost.ID); got.State != store.GrabFailed {
		t.Errorf("the lost grab: %+v", got)
	}
	if st := target(t, r.svc, eps[2].ID); st.State != store.TargetWanted || st.ReservedBy != "" {
		t.Errorf("the crashed hand-over's target: %+v", st)
	}
	if st := target(t, r.svc, eps[1].ID); st.State != store.TargetGrabbed {
		t.Errorf("the running grab's target: %+v", st)
	}
}

// A failed event for a grab whose bind never landed — its add's answer was
// lost — binds it and fails it like any other.
func TestAFailedEventForAGrabWhoseAnswerWasLost(t *testing.T) {
	r := newTargetRig(t, "Show.Name.S01E01.1080p.WEB-DL.x265-G")
	ctx := context.Background()
	_, eps := track(t, r.svc, 4711, "Show Name", 1, true)
	r.gw.mu.Lock()
	r.gw.addDrop = true
	r.gw.mu.Unlock()
	_, _ = r.svc.GrabTarget(ctx, eps[0].ID, "missing")
	gid := target(t, r.svc, eps[0].ID).ReservedBy
	if gid == "" {
		t.Fatal("no reservation")
	}
	_ = r.svc.OnFailed(ctx, events.DownloadEvent{Adapter: "nzbget", ClientID: "101", WantedID: gid, Error: "par repair failed"})
	if st := target(t, r.svc, eps[0].ID); st.State != store.TargetFailed || st.ReservedBy != "" || st.GrabID != gid || st.RetryAt == nil {
		t.Fatalf("after the failed event: %+v", st)
	}
}
