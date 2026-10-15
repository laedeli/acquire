package app

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/laedeli/acquire/internal/gateway"
	"github.com/laedeli/acquire/internal/store"
)

// appSQL runs a statement against the app tests' schema: what a test needs
// to age a row, as time would.
func appSQL(t *testing.T, sql string, args ...any) {
	t.Helper()
	dsn := os.Getenv("TEST_DSN")
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	pool, err := pgxpool.New(context.Background(), dsn+sep+"search_path=acquire_app_test")
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatal(err)
	}
}

// A gateway that is down, busy or not there fails no target, whether the
// grab came from the Missing tab's queue or a pick: the reservation goes, the
// target stays wanted, saying why, and it is due again only after a while.
func TestATemporaryGatewayErrorDoesNotFailTheTarget(t *testing.T) {
	for name, set := range map[string]func(r *targetRig){
		"503":         func(r *targetRig) { r.gw.addStatus = 503 },
		"429":         func(r *targetRig) { r.gw.addStatus = 429 },
		"not there":   func(r *targetRig) { r.svc.gw = gateway.New("http://127.0.0.1:1", nil) },
		"client down": func(r *targetRig) { r.gw.addStatus = 502 },
	} {
		t.Run(name, func(t *testing.T) {
			r := newTargetRig(t, "Show.Name.S01E01.1080p.WEB-DL.x265-G", "Show.Name.S01E02.1080p.WEB-DL.x265-G")
			ctx := context.Background()
			set(r)
			_, eps := track(t, r.svc, 4711, "Show Name", 2, true)
			if _, err := r.svc.RequestTargetGrabs(ctx, []string{eps[0].ID}); err != nil {
				t.Fatal(err)
			}
			if !r.svc.targetGrabNext(ctx) {
				t.Fatal("nothing was due")
			}
			cands, err := r.svc.SearchTarget(ctx, eps[1].ID)
			if err != nil || len(cands) != 1 {
				t.Fatalf("search: %+v %v", cands, err)
			}
			if _, err := r.svc.GrabTargetCandidate(ctx, eps[1].ID, cands[0]); err == nil {
				t.Fatal("a pick the gateway did not take succeeded")
			}
			for _, ep := range eps {
				st := target(t, r.svc, ep.ID)
				if st.State != store.TargetWanted || st.ReservedBy != "" || st.NextSearchAt == nil ||
					time.Until(*st.NextSearchAt) < 10*time.Minute || !strings.Contains(st.Detail, "not grabbed for now") {
					t.Fatalf("after a %s: %+v", name, st)
				}
			}
			if blocked, _ := r.svc.st.BlockedFor(ctx, eps[0].ID); len(blocked) != 0 {
				t.Errorf("a release was blocked for a gateway's trouble: %v", blocked)
			}
		})
	}
}

// The gateway being down stops the sweep's run: the rest of the slice is not
// searched for releases that could not be handed over.
func TestAGatewayOutageStopsTheSweep(t *testing.T) {
	r := newTargetRig(t, "Show.Name.S01E01.1080p.WEB-DL.x265-G", "Show.Name.S01E02.1080p.WEB-DL.x265-G")
	ctx := context.Background()
	if _, err := r.svc.SaveSearchSettings(ctx, SearchSettings{PreferProtocol: "usenet", MaxConcurrentGrabs: 3, AutoGrab: true}, 0, "test"); err != nil {
		t.Fatal(err)
	}
	r.gw.addStatus = 503
	_, eps := track(t, r.svc, 4711, "Show Name", 2, true)
	if err := r.svc.SweepBacklog(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if st := target(t, r.svc, eps[1].ID); st.LastSearchAt != nil {
		t.Errorf("the sweep searched on after the gateway failed: %+v", st)
	}
	if st := target(t, r.svc, eps[0].ID); st.State != store.TargetWanted || st.ReservedBy != "" {
		t.Errorf("the target the gateway failed: %+v", st)
	}
}

// A release whose file the source no longer has is blocked for the target,
// and the next release is grabbed instead.
func TestAReleaseThatCannotBeFetchedIsBlockedAndTheNextTaken(t *testing.T) {
	r := newTargetRig(t, "Show.Name.S01E01.1080p.WEB-DL.x265-A", "Show.Name.S01E01.720p.WEB-DL.x265-B")
	ctx := context.Background()
	r.src.gone = map[string]bool{"n0": true}
	_, eps := track(t, r.svc, 4711, "Show Name", 1, true)
	g, err := r.svc.GrabTarget(ctx, eps[0].ID, "missing")
	if err != nil {
		t.Fatal(err)
	}
	if g.ReleaseTitle != "Show.Name.S01E01.720p.WEB-DL.x265-B" {
		t.Fatalf("grabbed %q", g.ReleaseTitle)
	}
	blocked, _ := r.svc.st.BlockedFor(ctx, eps[0].ID)
	if why, ok := blocked["Show.Name.S01E01.1080p.WEB-DL.x265-A"]; !ok || !strings.Contains(why, "HTTP 404") {
		t.Errorf("the release whose file is gone is not blocked: %v", blocked)
	}
}

// A release the client refuses — not a client out of reach — is blocked too.
func TestAReleaseTheClientRefusesIsBlocked(t *testing.T) {
	r := newTargetRig(t, "Show.Name.S01E01.1080p.WEB-DL.x265-A")
	ctx := context.Background()
	_, eps := track(t, r.svc, 4711, "Show Name", 1, true)
	cands, _ := r.svc.SearchTarget(ctx, eps[0].ID)
	r.gw.mu.Lock()
	r.gw.addStatus, r.gw.addBody = 502, `{"error":"nzbget append rejected the source (id=0)"}`
	r.gw.mu.Unlock()
	if _, err := r.svc.GrabTargetCandidate(ctx, eps[0].ID, cands[0]); err == nil {
		t.Fatal("no error")
	}
	st := target(t, r.svc, eps[0].ID)
	blocked, _ := r.svc.st.BlockedFor(ctx, eps[0].ID)
	if _, ok := blocked["Show.Name.S01E01.1080p.WEB-DL.x265-A"]; !ok || st.State != store.TargetWanted ||
		!strings.Contains(st.Detail, "that release is blocked for it") {
		t.Errorf("a release the client refused: %+v, blocked %v", st, blocked)
	}
}

// The browser that asked for a pick goes away while the gateway takes the
// release: the hand-over and its bookkeeping finish anyway, so the job the
// gateway made is bound to its target.
func TestAHandOverOutlivesItsCaller(t *testing.T) {
	r := newTargetRig(t, "Show.Name.S01E01.1080p.WEB-DL.x265-G")
	_, eps := track(t, r.svc, 4711, "Show Name", 1, true)
	cands, _ := r.svc.SearchTarget(context.Background(), eps[0].ID)
	hold := make(chan struct{})
	r.gw.mu.Lock()
	r.gw.addHold = hold
	r.gw.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := r.svc.GrabTargetCandidate(ctx, eps[0].ID, cands[0])
		done <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for len(r.gw.added()) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the add never reached the gateway")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	close(hold)
	if err := <-done; err != nil {
		t.Fatalf("the pick: %v", err)
	}
	if st := target(t, r.svc, eps[0].ID); st.State != store.TargetGrabbed || st.GrabID == "" {
		t.Fatalf("the gateway took the release but the target is %+v", st)
	}
}

// The gateway's answer is lost after it may have taken the release: the
// reservation stays, the target says why, the sweep leaves it alone, and the
// download's completed event binds the grab.
func TestAHandOverWhoseAnswerIsLostWaitsForItsEvents(t *testing.T) {
	r := newTargetRig(t, "Show.Name.S01E01.1080p.WEB-DL.x265-G")
	ctx := context.Background()
	_, eps := track(t, r.svc, 4711, "Show Name", 1, true)
	r.gw.mu.Lock()
	r.gw.addDrop = true
	r.gw.mu.Unlock()
	if _, err := r.svc.RequestTargetGrabs(ctx, []string{eps[0].ID}); err != nil {
		t.Fatal(err)
	}
	r.svc.targetGrabNext(ctx)
	st := target(t, r.svc, eps[0].ID)
	if st.ReservedBy == "" || st.State != store.TargetWanted || !strings.Contains(st.Detail, "its answer was lost") {
		t.Fatalf("the reservation was given up: %+v", st)
	}
	g, err := r.svc.st.GetTargetGrab(ctx, st.ReservedBy)
	if err != nil || g.State != store.GrabPending {
		t.Fatalf("the pending grab is gone: %+v %v", g, err)
	}
	if due, _ := r.svc.st.DueTargets(ctx, 10); len(due) != 0 {
		t.Errorf("a reserved target is due for the sweep: %+v", due)
	}
	// The download's events carry the grab's id, its client and its job.
	g.Adapter, g.ClientJobID = "nzbget", "101"
	r.finish(t, g, map[string]string{"Show.Name.S01E01.mkv": "e"}, "Show.Name.S01E01.mkv")
	if st := target(t, r.svc, eps[0].ID); st.State != store.TargetDownloaded || st.GrabID != g.ID || st.ReservedBy != "" {
		t.Fatalf("the completed event did not bind it: %+v", st)
	}
}

// A manual-search result of a tracked series that names no episode is
// refused, not filed as a movie request; one naming another year is a movie.
func TestAManualResultOfATrackedSeriesWithoutAnEpisodeIsRefused(t *testing.T) {
	r := newTargetRig(t, "Show.Name.1080p.WEB-DL.x265-G", "Show.Name.1987.1080p.BluRay.x264-G")
	ctx := context.Background()
	track(t, r.svc, 4711, "Show Name", 1, true)
	found, err := r.svc.Search(ctx, "Show Name", nil)
	if err != nil || len(found.Candidates) != 2 {
		t.Fatalf("search: %+v %v", found, err)
	}
	for _, c := range found.Candidates {
		err := r.svc.GrabAdHoc(ctx, c, "", "admin")
		switch c.Title {
		case "Show.Name.1080p.WEB-DL.x265-G":
			if err == nil || !strings.Contains(err.Error(), "names no episode") {
				t.Errorf("a result of the tracked series: %v", err)
			}
		default:
			if err != nil {
				t.Errorf("a movie of the same name from another year: %v", err)
			}
		}
	}
	if ws, _ := r.svc.st.ListWanted(ctx); len(ws) != 1 || ws[0].MediaType != "movie" {
		t.Errorf("requests: %+v", ws)
	}
}
