package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/laedeli/acquire/internal/events"
	"github.com/laedeli/acquire/internal/katalog"
	"github.com/laedeli/acquire/internal/store"
)

// managedRig is the deployment's NZBGet — managed, so acquire releases its
// downloads through its API — on a downloads volume of its own, its finished
// downloads copied into the arrivals, a catalog taking ingest by identity,
// and a source offering releases.
type managedRig struct {
	*importRig
	gw  *fakeGateway
	cat *identityCatalog
	api *holdingsAPI
	src *fakeSource
}

func newManagedRig(t *testing.T, releases ...string) *managedRig {
	t.Helper()
	r := &managedRig{importRig: &importRig{catalog: &fakeCatalog{}, clients: &fakeClients{state: "uploading"}},
		gw: &fakeGateway{seq: true}, cat: &identityCatalog{}, api: newHoldingsAPI()}
	r.svc = e2eService(t, r.gw, testBox(t))
	r.svc.kc = katalog.New(r.cat.server(t).URL, r.api.server(t).URL, nil)
	root := t.TempDir()
	r.downloads, r.arrivals = filepath.Join(root, "downloads"), filepath.Join(root, "arrivals")
	for _, d := range []string{r.arrivals, filepath.Join(r.downloads, "nzbget", "complete")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	r.svc.cfg.ArrivalsRoot = r.arrivals
	c := chartNZB("pw")
	c["baseUrl"] = r.clients.server(t).URL
	c["localPath"] = filepath.Join(r.downloads, "nzbget", "complete")
	r.svc.cfg.ManagedClients, r.svc.cfg.ManagedClientsSet = declaration(t, c), true
	if res, err := r.svc.ApplyManagedClients(context.Background()); err != nil || len(res.Created) != 1 {
		t.Fatalf("clients: %+v %v", res, err)
	}
	r.src = newFakeSource(t, "usenet", "K", releases...)
	addSource(t, r.svc, "example", r.src, nil)
	return r
}

// done writes a grab's finished download into NZBGet's complete folder and
// announces it as the gateway would.
func (r *managedRig) done(t *testing.T, g store.TargetGrab, files map[string]string, reported string) {
	t.Helper()
	for name, body := range files {
		p := filepath.Join(r.downloads, "nzbget", "complete", name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	r.clients.keeps(g.ClientJobID, "/downloads/complete/"+reported)
	if err := r.svc.OnCompleted(context.Background(), events.DownloadEvent{
		Adapter: g.Adapter, ClientID: g.ClientJobID, WantedID: g.ID, Files: []string{"/downloads/complete/" + reported},
	}); err != nil {
		t.Fatal(err)
	}
}

// released counts the downloads acquire told NZBGet to release.
func (r *managedRig) released() int {
	r.clients.mu.Lock()
	defer r.clients.mu.Unlock()
	return len(r.clients.editqueue)
}

// A managed client's download is released only when every target its grab
// holds was handed over as a copy the catalog took, or is held. A target the
// download holds no file for, a file the catalog refuses, and a hand-over in
// place — where the client's file is the library's — keep it.
func TestAManagedDownloadIsReleasedOnlyWhenTheLibraryHasAllOfIt(t *testing.T) {
	const rel = "Show.Name.S01E02.1080p.WEB-DL.x265-G"
	file := rel + "/" + rel + ".mkv"
	grab := func(t *testing.T, r *managedRig) store.TargetGrab {
		t.Helper()
		_, eps := track(t, r.svc, 4711, "Show Name", 2, true)
		g, err := r.svc.GrabTarget(context.Background(), eps[1].ID, "missing")
		if err != nil {
			t.Fatal(err)
		}
		return g
	}
	t.Run("all of it, copied: released", func(t *testing.T) {
		r := newManagedRig(t, rel)
		g := grab(t, r)
		r.done(t, g, map[string]string{file: "the episode"}, rel)
		r.svc.importNext(context.Background())
		if r.released() != 1 {
			t.Fatalf("released %d times", r.released())
		}
		if left := treeOf(t, r.arrivals); len(left) != 1 {
			t.Errorf("the copy the catalog took: %v", left)
		}
	})
	t.Run("a target without a file: kept", func(t *testing.T) {
		r := newManagedRig(t, rel)
		g := grab(t, r)
		// Two files that name nothing, in a folder that names nothing either.
		r.done(t, g, map[string]string{"x9y8z7/a1b2c3.mkv": "x", "x9y8z7/d4e5f6.mkv": "y"}, "x9y8z7")
		r.svc.importNext(context.Background())
		st := target(t, r.svc, g.TargetIDs[0])
		if r.released() != 0 || st.State != store.TargetFailed || !strings.Contains(st.Detail, "no file for S01E02") ||
			!strings.Contains(st.Detail, "stays in its client") || st.GrabFailures != 1 || st.RetryAt == nil {
			t.Fatalf("released %d, target %+v", r.released(), st)
		}
		got, _ := r.svc.st.GetTargetGrab(context.Background(), g.ID)
		if got.State != store.GrabFailed || !strings.Contains(got.Detail, "no file for S01E02") {
			t.Errorf("grab: %+v", got)
		}
		if im := r.importOf(t, "nzbget", g.ClientJobID); im.State != store.ImportFailed || !strings.HasPrefix(im.Detail, "kept in the client: ") {
			t.Errorf("import: %+v", im)
		}
	})
	t.Run("refused by the catalog: kept, the copy gone", func(t *testing.T) {
		r := newManagedRig(t, rel)
		r.cat.set("refuse")
		g := grab(t, r)
		r.done(t, g, map[string]string{file: "the episode", rel + "/" + rel + ".en.srt": "subs"}, rel)
		r.svc.importNext(context.Background())
		st := target(t, r.svc, g.TargetIDs[0])
		if r.released() != 0 || st.State != store.TargetFailed || !strings.Contains(st.Detail, "the catalog refused") {
			t.Fatalf("released %d, target %+v", r.released(), st)
		}
		if left := treeOf(t, r.arrivals); len(left) != 0 {
			t.Errorf("the refused copy stayed in the arrivals: %v", left)
		}
		if _, err := os.Stat(filepath.Join(r.downloads, "nzbget", "complete", file)); err != nil {
			t.Errorf("the client's own file: %v", err)
		}
	})
	t.Run("handed over in place: kept", func(t *testing.T) {
		r := newManagedRig(t, rel)
		r.svc.cfg.ArrivalsRoot = "" // no arrivals: every file goes over where it lies
		g := grab(t, r)
		r.done(t, g, map[string]string{file: "the episode"}, rel)
		r.svc.importNext(context.Background())
		if st := target(t, r.svc, g.TargetIDs[0]); st.State != store.TargetHeld {
			t.Fatalf("target %+v", st)
		}
		if r.released() != 0 {
			t.Fatal("the client was told to delete the library's own file")
		}
		if im := r.importOf(t, "nzbget", g.ClientJobID); im.State != store.ImportDone || !strings.Contains(im.Detail, "in place") {
			t.Errorf("import: %+v", im)
		}
	})
	t.Run("a download already in the arrivals: kept", func(t *testing.T) {
		r := newManagedRig(t, rel)
		r.svc.cfg.ArrivalsRoot = r.downloads // the client downloads into the arrivals: nothing is copied
		g := grab(t, r)
		r.done(t, g, map[string]string{file: "the episode"}, rel)
		r.svc.importNext(context.Background())
		if st := target(t, r.svc, g.TargetIDs[0]); st.State != store.TargetHeld || r.released() != 0 {
			t.Fatalf("released %d, target %+v", r.released(), st)
		}
	})
}

// A season pack that holds no file for an episode acquire counts as aired
// does not fail that episode: it is let go — wanted again, the pack blocked
// for it — and the rest goes over; the download is released, since the
// library has all the grab still holds.
func TestAPackWithoutAnEpisodeDoesNotFailIt(t *testing.T) {
	const rel = "Show.Name.S01.1080p.BluRay.x265-G"
	r := newManagedRig(t, rel)
	ctx := context.Background()
	_, eps := track(t, r.svc, 4711, "Show Name", 3, true)
	cands, _ := r.svc.SearchTarget(ctx, eps[0].ID)
	var c Candidate
	b, _ := json.Marshal(cands[0])
	_ = json.Unmarshal(b, &c)
	g, err := r.svc.GrabTargetCandidate(ctx, eps[0].ID, c)
	if err != nil || len(g.TargetIDs) != 3 {
		t.Fatalf("grab: %+v %v", g, err)
	}
	r.done(t, g, map[string]string{
		rel + "/Show.Name.S01E01.1080p.mkv": "one", rel + "/Show.Name.S01E02.1080p.mkv": "two",
	}, rel)
	r.svc.importNext(ctx)
	three := target(t, r.svc, eps[2].ID)
	if three.State != store.TargetWanted || three.GrabFailures != 0 || !strings.Contains(three.Detail, "held no file for it") {
		t.Fatalf("the episode the pack lacked: %+v", three)
	}
	got, _ := r.svc.st.GetTargetGrab(ctx, g.ID)
	if got.State != store.GrabImported || len(got.TargetIDs) != 2 {
		t.Errorf("grab: %+v", got)
	}
	blocked, _ := r.svc.st.BlockedFor(ctx, eps[2].ID)
	if _, ok := blocked[rel]; !ok {
		t.Errorf("the pack is not blocked for the episode it lacked: %v", blocked)
	}
	if r.released() != 1 {
		t.Errorf("released %d times", r.released())
	}
}

// A season pack without a file for an episode, and with a file that names no
// episode, may hold it all the same, in that file: the episode is let go, and
// the client keeps the download for someone to look at.
func TestAPackWithAFileThatNamesNothingIsKept(t *testing.T) {
	const rel = "Show.Name.S01.1080p.BluRay.x265-G"
	r := newManagedRig(t, rel)
	ctx := context.Background()
	_, eps := track(t, r.svc, 4711, "Show Name", 3, true)
	cands, _ := r.svc.SearchTarget(ctx, eps[0].ID)
	g, err := r.svc.GrabTargetCandidate(ctx, eps[0].ID, cands[0])
	if err != nil || len(g.TargetIDs) != 3 {
		t.Fatalf("grab: %+v %v", g, err)
	}
	r.done(t, g, map[string]string{
		rel + "/Show.Name.S01E01.1080p.mkv": "one", rel + "/Show.Name.S01E02.1080p.mkv": "two", rel + "/x7k2q9.mkv": "three?",
	}, rel)
	r.svc.importNext(ctx)
	if st := target(t, r.svc, eps[2].ID); st.State != store.TargetWanted || st.GrabFailures != 0 {
		t.Fatalf("the episode the pack seemed to lack: %+v", st)
	}
	if r.released() != 0 {
		t.Fatal("released a download whose unnamed file may be the missing episode")
	}
	if got, _ := r.svc.st.GetTargetGrab(ctx, g.ID); got.State != store.GrabFailed || !strings.Contains(got.Detail, "x7k2q9.mkv") {
		t.Errorf("grab: %+v", got)
	}
}

// The catalog holds S01E07 already; a release S01E07E08 is refused with a 409
// naming S01E07's item. That file was not taken in: S01E07 is held by its own
// item, with nothing recorded of a file acquire did not bring; S01E08 is
// wanted again with the release blocked for it; the copy is gone, the
// download kept — and the release is not grabbed a second time, however
// often the episode is walked.
func TestAConflictOnAFileOfSeveralEpisodesDoesNotLoop(t *testing.T) {
	const rel = "Show.Name.S01E07E08.1080p.WEB-DL-G"
	walk := func(t *testing.T, r *managedRig, eight store.Target) {
		t.Helper()
		adds := len(r.gw.added())
		for i := 0; i < 2; i++ {
			if _, err := r.svc.GrabTarget(context.Background(), eight.ID, "sweep"); !errors.Is(err, errNoReleases) {
				t.Fatalf("walk %d: %v", i+1, err)
			}
			// What the library holds says nothing new about S01E08.
			if _, err := r.svc.ReconcileHoldings(context.Background(), eight.TitleID); err != nil {
				t.Fatal(err)
			}
		}
		if len(r.gw.added()) != adds {
			t.Fatal("the refused release was grabbed again")
		}
		blocked, _ := r.svc.st.BlockedFor(context.Background(), eight.ID)
		if _, ok := blocked[rel]; !ok {
			t.Errorf("the release is not blocked for S01E08: %v", blocked)
		}
	}
	held := func(r *managedRig) {
		r.cat.mode, r.cat.holder = "conflict", "own-S01E07"
		r.api.set(func(h *holdingsAPI) {
			h.series["tmdb:4711"] = season1(katalog.EpisodeHolding{Episode: 7, ItemID: "own-S01E07", HasFile: true})
		})
	}

	t.Run("acquire did not know", func(t *testing.T) {
		r := newManagedRig(t, rel)
		ctx := context.Background()
		held(r)
		_, eps := track(t, r.svc, 4711, "Show Name", 8, true)
		seven, eight := eps[6], eps[7]
		g, err := r.svc.GrabTarget(ctx, seven.ID, "sweep")
		if err != nil || len(g.TargetIDs) != 2 {
			t.Fatalf("grab: %+v %v", g, err)
		}
		r.done(t, g, map[string]string{rel + ".mkv": "both"}, rel+".mkv")
		r.svc.importNext(ctx)

		s7, s8 := target(t, r.svc, seven.ID), target(t, r.svc, eight.ID)
		if s7.State != store.TargetHeld || s7.HeldItemID != "own-S01E07" || s7.HeldVia != "" || s7.HeldSource == "grab" ||
			s7.HeldRelease != "" || s7.HeldScore != 0 {
			t.Fatalf("S01E07: %+v", s7)
		}
		if s8.State != store.TargetWanted || s8.HeldItemID != "" || !strings.Contains(s8.Detail, "was not taken in") {
			t.Fatalf("S01E08: %+v", s8)
		}
		if left := treeOf(t, r.arrivals); len(left) != 0 {
			t.Errorf("the refused copy stayed: %v", left)
		}
		if r.released() != 0 {
			t.Error("the download was released though the library does not have S01E08")
		}
		if got, _ := r.svc.st.GetTargetGrab(ctx, g.ID); got.State != store.GrabFailed {
			t.Errorf("grab: %+v", got)
		}
		walk(t, r, eight)
	})
	t.Run("acquire knew, and S01E08 was picked", func(t *testing.T) {
		r := newManagedRig(t, rel)
		ctx := context.Background()
		held(r)
		title, eps := track(t, r.svc, 4711, "Show Name", 8, true)
		if _, err := r.svc.ReconcileHoldings(ctx, title.ID); err != nil {
			t.Fatal(err)
		}
		eight := eps[7]
		cands, _ := r.svc.SearchTarget(ctx, eight.ID)
		g, err := r.svc.GrabTargetCandidate(ctx, eight.ID, cands[0])
		if err != nil || len(g.TargetIDs) != 1 {
			t.Fatalf("pick: %+v %v", g, err)
		}
		r.done(t, g, map[string]string{rel + ".mkv": "both"}, rel+".mkv")
		r.svc.importNext(ctx)
		if s8 := target(t, r.svc, eight.ID); s8.State != store.TargetWanted || s8.HeldItemID != "" {
			t.Fatalf("S01E08: %+v", s8)
		}
		walk(t, r, eight)
	})
}

// A reconcile holds the episode while the import is handing it over, and the
// catalog then refuses the file: the import leaves the episode held.
func TestTheImportLeavesAStateThatMovedMeanwhile(t *testing.T) {
	r := newTargetRig(t, "Show.Name.S01E01.1080p.WEB-DL.x265-G")
	ctx := context.Background()
	_, eps := track(t, r.svc, 4711, "Show Name", 1, true)
	g, err := r.svc.GrabTarget(ctx, eps[0].ID, "missing")
	if err != nil {
		t.Fatal(err)
	}
	r.cat.mode = "refuse"
	r.cat.onIngest = func(katalog.IngestRequest) {
		_ = r.svc.st.HoldTarget(ctx, store.Hold{TargetID: eps[0].ID, ItemID: "found-meanwhile", Source: "derived"})
	}
	r.finish(t, g, map[string]string{"Show.Name.S01E01.mkv": "e"}, "Show.Name.S01E01.mkv")
	r.svc.importNext(ctx)
	if st := target(t, r.svc, eps[0].ID); st.State != store.TargetHeld || st.HeldItemID != "found-meanwhile" || st.GrabFailures != 0 {
		t.Fatalf("the import overwrote a held target: %+v", st)
	}
}

// An episode just handed over is not made wanted again by a reconcile whose
// read side does not list it yet.
func TestAReconcileDoesNotTakeBackAFreshHandOver(t *testing.T) {
	r := newTargetRig(t, "Show.Name.S01E01.1080p.WEB-DL.x265-G")
	ctx := context.Background()
	title, eps := track(t, r.svc, 4711, "Show Name", 1, true)
	g, err := r.svc.GrabTarget(ctx, eps[0].ID, "missing")
	if err != nil {
		t.Fatal(err)
	}
	r.finish(t, g, map[string]string{"Show.Name.S01E01.mkv": "e"}, "Show.Name.S01E01.mkv")
	r.svc.importNext(ctx)
	r.api.set(func(h *holdingsAPI) { h.series["tmdb:4711"] = season1() })
	if _, err := r.svc.ReconcileHoldings(ctx, title.ID); err != nil {
		t.Fatal(err)
	}
	if st := target(t, r.svc, eps[0].ID); st.State != store.TargetHeld || st.HandedOverAt == nil {
		t.Fatalf("a fresh hand-over was taken back: %+v", st)
	}
	// Half an hour later, the read side's word counts again.
	appSQL(t, `UPDATE acquisition_targets SET handed_over_at = now() - interval '31 minutes' WHERE id = $1`, eps[0].ID)
	if _, err := r.svc.ReconcileHoldings(ctx, title.ID); err != nil {
		t.Fatal(err)
	}
	if st := target(t, r.svc, eps[0].ID); st.State != store.TargetWanted {
		t.Fatalf("an old hand-over the library does not list: %+v", st)
	}
}
