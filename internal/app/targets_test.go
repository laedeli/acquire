package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/laedeli/acquire/internal/events"
	"github.com/laedeli/acquire/internal/identity"
	"github.com/laedeli/acquire/internal/katalog"
	"github.com/laedeli/acquire/internal/store"
)

// ── fakes ──────────────────────────────────────────────────────────────────

// identityCatalog is katalog-manager's POST /api/ingest: with ingest by
// identity as it was built, or (mode) as the catalog from before the contract
// answers.
type identityCatalog struct {
	mu   sync.Mutex
	reqs []map[string]any
	// mode: "" takes ingest by identity; "old" is the catalog from before the
	// contract (unknown fields ignored, an episode needs a parentId);
	// "strict" refuses the new fields with 422; "conflict" answers 409 naming
	// holder; "down" answers 503.
	mode   string
	holder string
	// held are episodes (S01E06) the library holds already with a file of
	// their own: a file of several covering one leaves it out of covered.
	held map[string]bool
}

func (f *identityCatalog) set(mode string) {
	f.mu.Lock()
	f.mode = mode
	f.mu.Unlock()
}

func (f *identityCatalog) bodies() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]map[string]any(nil), f.reqs...)
}

func (f *identityCatalog) server(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		var raw map[string]any
		if r.URL.Path != "/api/ingest" || json.NewDecoder(r.Body).Decode(&raw) != nil {
			http.NotFound(w, r)
			return
		}
		f.reqs = append(f.reqs, raw)
		b, _ := json.Marshal(raw)
		var req katalog.IngestRequest
		_ = json.Unmarshal(b, &req)
		refuse := func(code int, body string) {
			w.WriteHeader(code)
			_, _ = w.Write([]byte(body))
		}
		switch f.mode {
		case "down":
			refuse(503, "busy")
			return
		case "conflict":
			refuse(409, `{"error":"the library holds it as another item","itemId":"`+f.holder+`"}`)
			return
		case "old":
			if req.Type == "episode" && req.ParentID == nil {
				refuse(400, `{"error":"an episode requires parentId, seasonNumber and episodeNumber; without them it would be ingested as an unlinked orphan"}`)
				return
			}
		case "strict":
			if req.ByIdentity() {
				refuse(422, `{"error":"unknown field"}`)
				return
			}
		default:
			// As katalog-manager's guard answers, in its words.
			for _, ids := range []*katalog.Identity{req.Series, req.Identity} {
				if ids != nil && (ids.IMDB != "" && katalog.IMDbID(ids.IMDB) != ids.IMDB) {
					refuse(400, `{"error":"series.imdb is tt and digits, not \"`+ids.IMDB+`\""}`)
					return
				}
			}
			switch {
			case req.Type == "episode" && req.Series == nil && req.ParentID == nil:
				refuse(400, `{"error":"an episode requires its series, by parentId or by its identity (series), and seasonNumber and episodeNumber; without them it would be ingested as an unlinked orphan"}`)
				return
			case req.EpisodeEnd != nil && (*req.EpisodeEnd < *req.EpisodeNumber || *req.EpisodeEnd-*req.EpisodeNumber >= katalog.MaxCovered):
				refuse(400, `{"error":"episodeEnd is the last episode the file covers, from episodeNumber on, as one file covers 10 episodes at most"}`)
				return
			}
		}
		out := katalog.IngestResult{ItemID: "movie-" + filepath.Base(req.Path), Created: true}
		if req.Type == "episode" {
			out.ItemID = fmt.Sprintf("item-S%02dE%02d", *req.SeasonNumber, *req.EpisodeNumber)
			out.SeriesID = "series-1"
			if req.EpisodeEnd != nil {
				out.Covered = []string{}
				for e := *req.EpisodeNumber + 1; e <= *req.EpisodeEnd; e++ {
					code := fmt.Sprintf("S%02dE%02d", *req.SeasonNumber, e)
					if !f.held[code] {
						out.Covered = append(out.Covered, "item-"+code)
					}
				}
			}
		}
		_ = json.NewEncoder(w).Encode(out)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// targetRig is a service with a search source offering releases, an NZBGet
// client downloading into a folder of the test's, and a catalog that takes
// ingest by identity — handed over in place (no arrivals).
type targetRig struct {
	svc       *Service
	gw        *fakeGateway
	cat       *identityCatalog
	api       *holdingsAPI
	src       *fakeSource
	downloads string
}

func newTargetRig(t *testing.T, releases ...string) *targetRig {
	t.Helper()
	r := &targetRig{gw: &fakeGateway{seq: true}, cat: &identityCatalog{}, api: newHoldingsAPI()}
	r.svc = e2eService(t, r.gw, testBox(t))
	r.svc.kc = katalog.New(r.cat.server(t).URL, r.api.server(t).URL, nil)
	r.downloads = t.TempDir()
	ctx := context.Background()
	if _, err := r.svc.CreateClient(ctx, ClientInput{Type: "nzbget", BaseURL: "http://worker:6789", Auth: "none",
		RemotePath: "/downloads", LocalPath: r.downloads}, "admin"); err != nil {
		t.Fatal(err)
	}
	r.src = newFakeSource(t, "usenet", "K", releases...)
	addSource(t, r.svc, "example", r.src, nil)
	return r
}

// track makes a monitored series with episodes 1..n of season 1, aired two
// days ago, and — known — records that the library holds none of them.
func track(t *testing.T, svc *Service, tmdb int64, name string, n int, known bool) (store.Title, []store.Target) {
	t.Helper()
	ctx := context.Background()
	if err := svc.st.UpsertTitle(ctx, store.Title{TMDBID: tmdb, Kind: "series", Title: name, Year: 2019,
		Monitored: true, MonitorNew: true, SeriesType: "standard"}); err != nil {
		t.Fatal(err)
	}
	id := store.TitleID("series", tmdb)
	aired := time.Now().Add(-48 * time.Hour)
	for e := 1; e <= n; e++ {
		season, ep := 1, e
		tid := store.TargetID(id, &season, &ep)
		if err := svc.st.UpsertTarget(ctx, store.Target{ID: tid, TitleID: id, Kind: "episode",
			SeasonNumber: &season, EpisodeNumber: &ep, Monitored: true, State: store.TargetWanted}); err != nil {
			t.Fatal(err)
		}
		if err := svc.st.SetAirWindow(ctx, tid, &aired, 0); err != nil {
			t.Fatal(err)
		}
	}
	if known {
		if _, err := svc.st.ApplySeriesHoldings(ctx, id, "", "default", nil); err != nil {
			t.Fatal(err)
		}
	}
	title, err := svc.st.TitleByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	targets, _ := svc.st.TitleTargets(ctx, id)
	return title, targets
}

func target(t *testing.T, svc *Service, id string) store.Target {
	t.Helper()
	tg, err := svc.st.GetTarget(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return tg
}

// finish writes a finished download into the client's folder (files relative
// to it) and announces it as the gateway would, reporting the path given.
func (r *targetRig) finish(t *testing.T, g store.TargetGrab, files map[string]string, reported ...string) {
	t.Helper()
	for name, body := range files {
		p := filepath.Join(r.downloads, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var paths []string
	for _, p := range reported {
		paths = append(paths, "/downloads/"+p)
	}
	if err := r.svc.OnCompleted(context.Background(), events.DownloadEvent{
		Adapter: g.Adapter, ClientID: g.ClientJobID, WantedID: g.ID, Files: paths,
	}); err != nil {
		t.Fatal(err)
	}
}

// ── the state machine ──────────────────────────────────────────────────────

// One episode, end to end: searched with its typed search, grabbed bound to
// its target (the gateway is given the grab's id, not a request's), the
// download handed over by identity, held with the catalog's item, playable
// once packaged, wanted again once removed.
func TestATargetGrabWalksWantedGrabbedDownloadedHeld(t *testing.T) {
	r := newTargetRig(t, "Show.Name.S01E02.1080p.WEB-DL.x265-G")
	ctx := context.Background()
	title, eps := track(t, r.svc, 4711, "Show Name", 3, true)

	g, err := r.svc.GrabTarget(ctx, eps[1].ID, "missing")
	if err != nil {
		t.Fatal(err)
	}
	if !store.IsTargetGrab(g.ID) || g.State != store.GrabGrabbed || g.Adapter != "nzbget" || g.ClientJobID != "101" ||
		!reflect.DeepEqual(g.TargetIDs, []string{eps[1].ID}) || g.Covers != "S01E02" || g.Via != "missing" {
		t.Fatalf("grab = %+v", g)
	}
	adds := r.gw.added()
	if len(adds) != 1 || adds[0]["wanted_item_id"] != g.ID || adds[0]["title"] != "Show Name S01E02" {
		t.Fatalf("gateway adds: %v", adds)
	}
	if strings.Contains(fmt.Sprint(adds[0]), "apikey") {
		t.Fatal("the source's key reached the gateway")
	}
	two := target(t, r.svc, eps[1].ID)
	if two.State != store.TargetGrabbed || two.GrabID != g.ID || two.Detail != "grabbed NZB S01E02 from example via nzbget" ||
		two.SearchOutcome != store.SearchFound {
		t.Fatalf("after the grab: %+v", two)
	}
	if _, err := r.svc.st.LatestGrab(ctx, g.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a target grab wrote a request's grab row: %v", err)
	}
	var ng *store.NotGrabbableError
	if _, err := r.svc.GrabTarget(ctx, eps[1].ID, "missing"); !errors.As(err, &ng) {
		t.Fatalf("a target in flight was grabbed again: %v", err)
	}

	r.finish(t, g, map[string]string{
		"Show.Name.S01E02.1080p.WEB-DL.x265-G/Show.Name.S01E02.1080p.WEB-DL.x265-G.mkv":        "the episode",
		"Show.Name.S01E02.1080p.WEB-DL.x265-G/Show.Name.S01E02.1080p.WEB-DL.x265-G.sample.mkv": "x",
	}, "Show.Name.S01E02.1080p.WEB-DL.x265-G")
	if st := target(t, r.svc, eps[1].ID); st.State != store.TargetDownloaded || !strings.Contains(st.Detail, "handing it over") {
		t.Fatalf("after completion: %+v", st)
	}
	if !r.svc.importNext(ctx) {
		t.Fatal("the finished download was not queued for import")
	}
	bodies := r.cat.bodies()
	if len(bodies) != 1 {
		t.Fatalf("hand-overs: %v", bodies)
	}
	want := map[string]any{
		"path": filepath.Join(r.downloads, "Show.Name.S01E02.1080p.WEB-DL.x265-G", "Show.Name.S01E02.1080p.WEB-DL.x265-G.mkv"),
		"type": "episode", "title": "Show Name", "year": float64(2019),
		"series": map[string]any{"tmdb": "4711"}, "seasonNumber": float64(1), "episodeNumber": float64(2),
	}
	if !reflect.DeepEqual(bodies[0], want) {
		t.Errorf("ingest body = %v\nwant          %v", bodies[0], want)
	}
	held := target(t, r.svc, eps[1].ID)
	if held.State != store.TargetHeld || held.HeldItemID != "item-S01E02" || held.HeldSource != "grab" || held.HeldPlayable ||
		!strings.Contains(held.Detail, "item-S01E02") {
		t.Fatalf("after the hand-over: %+v", held)
	}
	if got, _ := r.svc.st.GetTargetGrab(ctx, g.ID); got.State != store.GrabImported {
		t.Errorf("grab after the hand-over: %+v", got)
	}
	if im, _ := r.svc.st.GetImport(ctx, "nzbget", "101"); im.State != store.ImportDone {
		t.Errorf("import: %+v", im)
	}
	files, _ := r.svc.st.GrabFiles(ctx, g.ID)
	if len(files) != 1 || files[0].State != "imported" || files[0].ItemID != "item-S01E02" {
		t.Errorf("grab files: %+v", files)
	}

	_ = r.svc.OnPackaged(ctx, events.ItemEvent{ItemID: "item-S01E02"})
	if st := target(t, r.svc, eps[1].ID); !st.HeldPlayable || st.HeldSource != "grab" {
		t.Errorf("packaged: %+v", st)
	}
	_ = r.svc.OnRemoved(ctx, events.ItemEvent{ItemID: "item-S01E02"})
	if st := target(t, r.svc, eps[1].ID); st.State != store.TargetWanted || st.Detail != "no longer in the library; wanted again" {
		t.Errorf("removed: %+v", st)
	}
	if miss, _ := r.svc.st.Missing(ctx, 10); len(miss) != 3 || miss[0].TitleID != title.ID {
		t.Errorf("missing after the removal: %+v", miss)
	}
}

// A download that fails fails its targets with the client's reason and blocks
// the release for them: the next grab does not pick the identical release.
func TestAFailedDownloadFailsItsTargetsAndBlocksTheRelease(t *testing.T) {
	r := newTargetRig(t, "Show.Name.S01E01.1080p.WEB-DL.x265-G")
	ctx := context.Background()
	_, eps := track(t, r.svc, 4711, "Show Name", 1, true)
	g, err := r.svc.GrabTarget(ctx, eps[0].ID, "missing")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.svc.OnFailed(ctx, events.DownloadEvent{Adapter: g.Adapter, ClientID: g.ClientJobID, Error: "par repair failed"}); err != nil {
		t.Fatal(err)
	}
	st := target(t, r.svc, eps[0].ID)
	if st.State != store.TargetFailed || st.Detail != "download failed: par repair failed" {
		t.Fatalf("after the failure (found by its job, the event naming no grab): %+v", st)
	}
	if miss, _ := r.svc.st.Missing(ctx, 10); len(miss) != 1 || miss[0].State != store.TargetFailed {
		t.Errorf("a failed target is missing, saying why: %+v", miss)
	}
	cands, err := r.svc.SearchTarget(ctx, eps[0].ID)
	if err != nil || len(cands) != 1 || !cands[0].Rejected || !strings.Contains(cands[0].Reason, "blocked for this target: par repair failed") {
		t.Fatalf("the failed release is offered again: %+v %v", cands, err)
	}
	if _, err := r.svc.GrabTarget(ctx, eps[0].ID, "missing"); !errors.Is(err, errNoReleases) || len(r.gw.added()) != 1 {
		t.Fatalf("a grab after the failure: %v (adds %d)", err, len(r.gw.added()))
	}
}

// ── season packs and files of several episodes ─────────────────────────────

func TestPlanEpisodes(t *testing.T) {
	ep := func(e int) store.Target {
		s, n := 1, e
		return store.Target{ID: fmt.Sprintf("e%d", e), Kind: "episode", SeasonNumber: &s, EpisodeNumber: &n}
	}
	targets := []store.Target{ep(1), ep(2), ep(3), ep(5)}
	videos := []videoFile{
		{"/d/Show.S01E01.1080p.mkv", 900},
		{"/d/Show.S01E01.720p.mkv", 400},
		{"/d/Show.S01E02E03.1080p.mkv", 1800},
		{"/d/Show.S01E04.1080p.mkv", 900},
		{"/d/abc123.mkv", 900},
	}
	plan := planEpisodes(videos, targets, "Show.S01.1080p.BluRay-G")
	got := map[string][]string{}
	for _, f := range plan.Files {
		for _, tg := range f.Targets {
			got[filepath.Base(f.File.Path)] = append(got[filepath.Base(f.File.Path)], tg.ID)
		}
	}
	want := map[string][]string{"Show.S01E01.1080p.mkv": {"e1"}, "Show.S01E02E03.1080p.mkv": {"e2", "e3"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("files = %v, want %v", got, want)
	}
	sort.Strings(plan.Left)
	wantLeft := []string{
		"Show.S01E01.720p.mkv: a second file for S01E01",
		"Show.S01E04.1080p.mkv: S01E04 is not one this grab is for (held already, or not wanted)",
		"abc123.mkv: no episode in its name",
	}
	if !reflect.DeepEqual(plan.Left, wantLeft) {
		t.Errorf("left = %q\nwant  %q", plan.Left, wantLeft)
	}
	if len(plan.Missing) != 1 || plan.Missing[0].ID != "e5" {
		t.Errorf("missing = %+v", plan.Missing)
	}

	// One file named after nothing is the release itself; a file named after
	// nothing in a folder named after its episode is that episode.
	one := planEpisodes([]videoFile{{"/d/obfuscated.mkv", 1}}, []store.Target{ep(5)}, "Show.S01E05.1080p.WEB-DL-G")
	if len(one.Files) != 1 || one.Files[0].Coverage.String() != "S01E05" || len(one.Missing) != 0 {
		t.Errorf("one unnamed file: %+v", one)
	}
	folder := planEpisodes([]videoFile{{"/d/Show.S01E03.1080p/x1y2.mkv", 1}}, []store.Target{ep(3)}, "Show.S01.1080p")
	if len(folder.Files) != 1 || folder.Files[0].Targets[0].ID != "e3" {
		t.Errorf("a file named by its folder: %+v", folder)
	}
	if isExtra("/d/Trailer.Park.Show.S01E01.mkv") || !isExtra("/d/Show.S01E01.sample.mkv") || !isExtra("/d/Featurettes/making.of.mkv") {
		t.Error("samples and extras are told by their name and folder, not by a word in a show's title")
	}
}

// A season pack picked among a target's results is bound to every wanted,
// aired episode of its season — not a held one, not an unaired one — and its
// files go over one per episode, the rest left out with a note. An automatic
// grab never takes a pack for one episode.
func TestASeasonPackGoesOverFileByFile(t *testing.T) {
	r := newTargetRig(t, "Show.Name.S01.1080p.BluRay.x265-G")
	ctx := context.Background()
	_, eps := track(t, r.svc, 4711, "Show Name", 5, true)
	if err := r.svc.st.HoldTarget(ctx, store.Hold{TargetID: eps[2].ID, ItemID: "x3", Source: "derived"}); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(72 * time.Hour)
	_ = r.svc.st.SetAirWindow(ctx, eps[4].ID, &future, 0)

	if _, err := r.svc.GrabTarget(ctx, eps[0].ID, "missing"); !errors.Is(err, errNoReleases) {
		t.Fatalf("an automatic grab took a season pack: %v", err)
	}
	cands, err := r.svc.SearchTarget(ctx, eps[0].ID)
	if err != nil || len(cands) != 1 || !cands[0].Pack || cands[0].Covers != "S01" {
		t.Fatalf("the pack among the results: %+v %v", cands, err)
	}
	var c Candidate
	b, _ := json.Marshal(cands[0])
	_ = json.Unmarshal(b, &c) // as the console sends it back
	g, err := r.svc.GrabTargetCandidate(ctx, eps[0].ID, c)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(g.TargetIDs)
	if want := []string{eps[0].ID, eps[1].ID, eps[3].ID}; !reflect.DeepEqual(g.TargetIDs, want) || g.Via != "pick" {
		t.Fatalf("the pack is bound to %v, want %v", g.TargetIDs, want)
	}
	dir := "Show.Name.S01.1080p.BluRay.x265-G/"
	r.finish(t, g, map[string]string{
		dir + "Show.Name.S01E01.1080p.BluRay.x265-G.mkv": "one",
		dir + "Show.Name.S01E02.1080p.BluRay.x265-G.mkv": "two",
		dir + "Show.Name.S01E03.1080p.BluRay.x265-G.mkv": "three",
		dir + "Show.Name.S01E04.1080p.BluRay.x265-G.mkv": "four",
		dir + "Sample/sample.mkv":                        "s",
		dir + "Show.Name.S01.nfo":                        "info",
	}, strings.TrimSuffix(dir, "/"))
	r.svc.importNext(ctx)

	var handed []string
	for _, b := range r.cat.bodies() {
		handed = append(handed, fmt.Sprintf("S%02vE%02v", b["seasonNumber"], b["episodeNumber"]))
	}
	sort.Strings(handed)
	if !reflect.DeepEqual(handed, []string{"S01E01", "S01E02", "S01E04"}) {
		t.Errorf("handed over %v", handed)
	}
	for i, want := range []string{store.TargetHeld, store.TargetHeld, store.TargetHeld, store.TargetHeld, store.TargetWanted} {
		if st := target(t, r.svc, eps[i].ID); st.State != want {
			t.Errorf("E%02d: %s, want %s", i+1, st.State, want)
		}
	}
	if three := target(t, r.svc, eps[2].ID); three.HeldItemID != "x3" {
		t.Errorf("the episode held before was handed over again: %+v", three)
	}
	got, _ := r.svc.st.GetTargetGrab(ctx, g.ID)
	if got.State != store.GrabImported || !strings.Contains(got.Detail, "3 handed over") ||
		!strings.Contains(got.Detail, "Show.Name.S01E03.1080p.BluRay.x265-G.mkv: S01E03 is not one this grab is for") {
		t.Errorf("grab: %+v", got)
	}
}

// One file of three episodes goes over once, with its last episode. The first
// is held by the item the catalog answers; the others through that item's
// file, until the holdings name their own items — the answer's covered does
// not say which is which, since an episode the library held already is left
// out of it. The holder's packaged event, the only one the file gets, makes
// all of them playable, and removing the holder takes all of them.
func TestAFileOfSeveralEpisodesGoesOverOnce(t *testing.T) {
	r := newTargetRig(t, "Show.Name.S01E05E06E07.1080p.WEB-DL-G")
	ctx := context.Background()
	title, eps := track(t, r.svc, 4711, "Show Name", 7, true)
	r.cat.held = map[string]bool{"S01E06": true} // the library holds S01E06 already, though acquire did not know
	g, err := r.svc.GrabTarget(ctx, eps[4].ID, "sweep")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(g.TargetIDs)
	if !reflect.DeepEqual(g.TargetIDs, []string{eps[4].ID, eps[5].ID, eps[6].ID}) || g.Covers != "S01E05-E07" {
		t.Fatalf("grab: %+v", g)
	}
	r.finish(t, g, map[string]string{"Show.Name.S01E05E06E07.1080p.WEB-DL-G.mkv": "triple"}, "Show.Name.S01E05E06E07.1080p.WEB-DL-G.mkv")
	r.svc.importNext(ctx)
	bodies := r.cat.bodies()
	if len(bodies) != 1 || bodies[0]["episodeNumber"] != float64(5) || bodies[0]["episodeEnd"] != float64(7) {
		t.Fatalf("hand-overs: %v", bodies)
	}
	five, seven := target(t, r.svc, eps[4].ID), target(t, r.svc, eps[6].ID)
	if five.HeldItemID != "item-S01E05" || five.HeldVia != "" || seven.HeldItemID != "item-S01E05" || seven.HeldVia != "item-S01E05" {
		t.Fatalf("held: %+v / %+v", five, seven)
	}
	r.svc.holdingsMu.Lock()
	asked := r.svc.holdingsIDs[title.ID]
	r.svc.holdingsMu.Unlock()
	if !asked {
		t.Fatal("no reconcile was asked to name the covered episodes' items")
	}

	// The holder is packaged; the episodes it covers get no event of their own.
	_ = r.svc.OnPackaged(ctx, events.ItemEvent{ItemID: "item-S01E05"})
	if st := target(t, r.svc, eps[6].ID); !st.HeldPlayable {
		t.Errorf("a covered episode is not playable after its holder was packaged: %+v", st)
	}
	// The holdings name each episode's item: E06 its own file's, E07 its own,
	// through the holder's file.
	r.api.set(func(h *holdingsAPI) {
		h.series["tmdb:4711"] = season1(
			katalog.EpisodeHolding{Episode: 5, ItemID: "item-S01E05", HasFile: true, Playable: true},
			katalog.EpisodeHolding{Episode: 6, ItemID: "own-S01E06", HasFile: true, Playable: true},
			katalog.EpisodeHolding{Episode: 7, ItemID: "item-S01E07", HasFile: true, Playable: true, CoveredBy: "item-S01E05"},
		)
	})
	if _, err := r.svc.ReconcileHoldings(ctx, title.ID); err != nil {
		t.Fatal(err)
	}
	six, seven := target(t, r.svc, eps[5].ID), target(t, r.svc, eps[6].ID)
	if six.HeldItemID != "own-S01E06" || six.HeldVia != "" || seven.HeldItemID != "item-S01E07" || seven.HeldVia != "item-S01E05" ||
		seven.HeldSource != "grab" {
		t.Fatalf("after the reconcile: %+v / %+v", six, seven)
	}
	_ = r.svc.OnRemoved(ctx, events.ItemEvent{ItemID: "item-S01E05"})
	if a, b, c := target(t, r.svc, eps[4].ID), target(t, r.svc, eps[5].ID), target(t, r.svc, eps[6].ID); a.State != store.TargetWanted ||
		b.State != store.TargetHeld || c.State != store.TargetWanted {
		t.Errorf("the holder's removal left %s / %s / %s, want the holder and what it covers wanted, E06 held", a.State, b.State, c.State)
	}
}

// A file that covers more episodes in a row than the catalog takes in one is
// not handed over: its episodes fail, saying why.
func TestAFileOfTooManyEpisodesIsRefused(t *testing.T) {
	r := newTargetRig(t, "Show.Name.S01E01-E12.1080p.WEB-DL-G")
	ctx := context.Background()
	_, eps := track(t, r.svc, 4711, "Show Name", 12, true)
	g, err := r.svc.GrabTarget(ctx, eps[0].ID, "missing")
	if err != nil {
		t.Fatal(err)
	}
	if len(g.TargetIDs) != 12 {
		t.Fatalf("grab covers %d", len(g.TargetIDs))
	}
	r.finish(t, g, map[string]string{"Show.Name.S01E01-E12.1080p.WEB-DL-G.mkv": "x"}, "Show.Name.S01E01-E12.1080p.WEB-DL-G.mkv")
	r.svc.importNext(ctx)
	if n := len(r.cat.bodies()); n != 0 {
		t.Errorf("handed over %d", n)
	}
	if st := target(t, r.svc, eps[11].ID); st.State != store.TargetFailed ||
		!strings.Contains(st.Detail, "covers 12 episodes in a row (S01E01-E12); the catalog takes at most 10 in one file") {
		t.Errorf("E12: %+v", st)
	}
}

// ── the catalog's answers ──────────────────────────────────────────────────

// A 409 naming the item that holds the episode already is "held", not a
// failure, and a reconcile of the title is asked for.
func TestAConflictNamingTheHolderIsHeld(t *testing.T) {
	r := newTargetRig(t, "Show.Name.S01E01.1080p.WEB-DL.x265-G")
	ctx := context.Background()
	title, eps := track(t, r.svc, 4711, "Show Name", 1, true)
	r.cat.mode, r.cat.holder = "conflict", "x9"
	g, err := r.svc.GrabTarget(ctx, eps[0].ID, "missing")
	if err != nil {
		t.Fatal(err)
	}
	r.finish(t, g, map[string]string{"Show.Name.S01E01.mkv": "e"}, "Show.Name.S01E01.mkv")
	r.svc.importNext(ctx)
	st := target(t, r.svc, eps[0].ID)
	if st.State != store.TargetHeld || st.HeldItemID != "x9" || st.Detail != "the library holds it already, as item x9" {
		t.Fatalf("after a conflict: %+v", st)
	}
	if got, _ := r.svc.st.GetTargetGrab(ctx, g.ID); got.State != store.GrabImported || !strings.Contains(got.Detail, "1 held already") {
		t.Errorf("grab: %+v", got)
	}
	r.svc.holdingsMu.Lock()
	asked := r.svc.holdingsIDs[title.ID]
	r.svc.holdingsMu.Unlock()
	if !asked {
		t.Error("no reconcile of the title was asked for")
	}
}

// Against the catalog from before the contract an episode waits: the copy in
// the arrivals stays, the client keeps the download, and the hand-over is
// tried again later — and goes through once the catalog takes it, without a
// second copy.
func TestACatalogWithoutTheContractKeepsTheDownloadWaiting(t *testing.T) {
	rig := newImportRig(t)
	ctx := context.Background()
	cat := &identityCatalog{mode: "old"}
	rig.svc.kc = katalog.New(cat.server(t).URL, "", nil)
	src := newFakeSource(t, "usenet", "K", "Show.Name.S01E02.1080p.WEB-DL.x265-G")
	addSource(t, rig.svc, "example", src, nil)
	_, eps := track(t, rig.svc, 4711, "Show Name", 2, true)
	if err := os.MkdirAll(filepath.Join(rig.downloads, "nzbget", "complete"), 0o755); err != nil {
		t.Fatal(err)
	}

	g, err := rig.svc.GrabTarget(ctx, eps[1].ID, "missing")
	if err != nil {
		t.Fatal(err)
	}
	if g.Adapter != "nzbget" {
		t.Fatalf("grab went to %s", g.Adapter)
	}
	name := "Show.Name.S01E02.1080p.WEB-DL.x265-G.mkv"
	p := filepath.Join(rig.downloads, "nzbget", "complete", name)
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte("the episode"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := rig.svc.OnCompleted(ctx, events.DownloadEvent{Adapter: "nzbget", ClientID: g.ClientJobID, WantedID: g.ID,
		Files: []string{"/downloads/complete/" + name}}); err != nil {
		t.Fatal(err)
	}
	if st := target(t, rig.svc, eps[1].ID); !strings.Contains(st.Detail, "copying into the library") {
		t.Fatalf("after completion: %+v", st)
	}
	rig.svc.importNext(ctx)

	st := target(t, rig.svc, eps[1].ID)
	if st.State != store.TargetWaiting || !strings.Contains(st.Detail, "waiting for the catalog: it does not take episodes by identity yet") {
		t.Fatalf("against the old catalog: %+v", st)
	}
	im := rig.importOf(t, "nzbget", g.ClientJobID)
	if im.State != store.ImportPending || time.Until(im.NextAt) < 29*time.Minute || !strings.Contains(im.Detail, "waiting for the catalog") {
		t.Fatalf("import: %+v", im)
	}
	if len(rig.clients.editqueue) != 0 {
		t.Fatalf("the download was released while it waits: %v", rig.clients.editqueue)
	}
	copied := filepath.Join(rig.arrivals, "acquire", g.ID, "nzbget-"+g.ClientJobID, name)
	before, err := os.Stat(copied)
	if err != nil {
		t.Fatalf("the copy is gone: %v", err)
	}
	if miss, _ := rig.svc.st.Missing(ctx, 10); len(miss) != 2 {
		t.Errorf("a waiting episode is still missing: %+v", miss)
	}
	if rig.svc.importNext(ctx) {
		t.Fatal("tried again before its wait ran out")
	}

	cat.set("")
	rig.due(t, "nzbget", g.ClientJobID)
	rig.svc.importNext(ctx)
	if st := target(t, rig.svc, eps[1].ID); st.State != store.TargetHeld || st.HeldItemID != "item-S01E02" {
		t.Fatalf("once the catalog takes it: %+v", st)
	}
	if after, _ := os.Stat(copied); !os.SameFile(before, after) {
		t.Error("the good copy was written again")
	}
	if im := rig.importOf(t, "nzbget", g.ClientJobID); im.State != store.ImportDone || len(rig.clients.editqueue) != 1 {
		t.Errorf("not released once handed over: %+v %v", im, rig.clients.editqueue)
	}
	if bodies := cat.bodies(); len(bodies) != 2 || bodies[1]["path"] != copied {
		t.Errorf("hand-overs: %v", bodies)
	}
}

// A movie goes over with its identity; a catalog that refuses the new fields
// gets it as before, by title.
func TestAMovieGoesOverWithItsIdentity(t *testing.T) {
	r := newTargetRig(t)
	ctx := context.Background()
	w := store.Wanted{ID: "w_1", TMDBID: 603, Title: "The Matrix", Year: 1999}
	if _, err := r.svc.ingestMovie(ctx, movieIngest("/arrivals/m.mkv", w)); err != nil {
		t.Fatal(err)
	}
	r.cat.set("strict")
	if _, err := r.svc.ingestMovie(ctx, movieIngest("/arrivals/m.mkv", w)); err != nil {
		t.Fatal(err)
	}
	r.cat.set("old")
	if _, err := r.svc.ingestMovie(ctx, movieIngest("/arrivals/m.mkv", w)); err != nil {
		t.Fatal(err)
	}
	// With the contract: once, with the identity. A catalog that refuses the
	// new fields: once with them, once without. The catalog from before the
	// contract ignores what it does not know: once.
	bodies := r.cat.bodies()
	if len(bodies) != 4 {
		t.Fatalf("hand-overs: %v", bodies)
	}
	if !reflect.DeepEqual(bodies[0]["identity"], map[string]any{"tmdb": "603"}) || bodies[0]["type"] != "movie" {
		t.Errorf("with the contract: %v", bodies[0])
	}
	if _, ok := bodies[1]["identity"]; !ok {
		t.Errorf("first try against a strict catalog: %v", bodies[1])
	}
	if _, ok := bodies[2]["identity"]; ok || bodies[2]["title"] != "The Matrix" {
		t.Errorf("the retry still carries the identity: %v", bodies[2])
	}
	if _, ok := bodies[3]["identity"]; !ok {
		t.Errorf("the old catalog: %v", bodies[3])
	}
	tracked := store.Title{TMDBID: 603, IMDBID: "tt0133093", Title: "The Matrix", Year: 1999, Kind: "movie"}
	if req := movieTargetIngest(tracked, "/p.mkv"); req.Identity == nil || req.Identity.TMDB != "603" || req.Identity.IMDB != "tt0133093" {
		t.Errorf("a tracked movie: %+v", req)
	}
}

// ── the console's grabs ────────────────────────────────────────────────────

// Grabs asked for in the Missing tab are done one at a time: a target that is
// held is not queued, and while every download slot is taken a queued grab
// waits without spending a search.
func TestMissingTabGrabsQueue(t *testing.T) {
	r := newTargetRig(t, "Show.Name.S01E01.1080p.WEB-DL.x265-G", "Show.Name.S01E02.1080p.WEB-DL.x265-G")
	ctx := context.Background()
	if _, err := r.svc.SaveSearchSettings(ctx, SearchSettings{PreferProtocol: "usenet", MaxConcurrentGrabs: 1}, 0, "test"); err != nil {
		t.Fatal(err)
	}
	_, eps := track(t, r.svc, 4711, "Show Name", 3, true)
	_ = r.svc.st.HoldTarget(ctx, store.Hold{TargetID: eps[2].ID, ItemID: "x3"})
	queued, err := r.svc.RequestTargetGrabs(ctx, []string{eps[0].ID, eps[1].ID, eps[2].ID})
	if err != nil || len(queued) != 2 {
		t.Fatalf("queued %v %v", queued, err)
	}
	if miss, _ := r.svc.st.Missing(ctx, 10); len(miss) != 2 || !miss[0].GrabQueued {
		t.Errorf("the queue shows on the missing rows: %+v", miss)
	}
	if !r.svc.targetGrabNext(ctx) {
		t.Fatal("nothing was due")
	}
	searches := r.src.count("search")
	if !r.svc.targetGrabNext(ctx) {
		t.Fatal("the second grab was not looked at")
	}
	grabbed, waiting := target(t, r.svc, eps[0].ID), target(t, r.svc, eps[1].ID)
	if grabbed.State != store.TargetGrabbed {
		t.Fatalf("first: %+v", grabbed)
	}
	if waiting.State != store.TargetWanted || !strings.Contains(waiting.Detail, "queued: 1 download(s) already in flight (cap 1)") {
		t.Fatalf("second: %+v", waiting)
	}
	if r.src.count("search") != searches || len(r.gw.added()) != 1 {
		t.Errorf("a grab that cannot start was searched (%d→%d) or handed over (%d)", searches, r.src.count("search"), len(r.gw.added()))
	}
	if r.svc.targetGrabNext(ctx) {
		t.Error("a waiting grab came back before its wait")
	}
}

// A release from the manual search that names an episode of a tracked series
// is grabbed for that episode — not filed as a movie request — and one of a
// series acquire does not track is refused.
func TestAnEpisodeFromTheManualSearchIsNoMovie(t *testing.T) {
	r := newTargetRig(t, "Show.Name.S01E02.1080p.WEB-DL.x265-G", "Unknown.Show.S03E01.720p.HDTV-G")
	ctx := context.Background()
	_, eps := track(t, r.svc, 4711, "Show Name", 2, true)
	found, err := r.svc.Search(ctx, "Show Name", nil)
	if err != nil || len(found.Candidates) != 2 {
		t.Fatalf("search: %+v %v", found, err)
	}
	for _, c := range found.Candidates {
		var back Candidate
		b, _ := json.Marshal(c)
		_ = json.Unmarshal(b, &back)
		err := r.svc.GrabAdHoc(ctx, back, "", "admin")
		switch {
		case strings.HasPrefix(c.Title, "Show.Name"):
			if err != nil {
				t.Fatalf("an episode of a tracked series: %v", err)
			}
		default:
			if err == nil || !strings.Contains(err.Error(), "does not track") {
				t.Errorf("an episode of an untracked series: %v", err)
			}
		}
	}
	if st := target(t, r.svc, eps[1].ID); st.State != store.TargetGrabbed {
		t.Errorf("the episode: %+v", st)
	}
	if ws, _ := r.svc.st.ListWanted(ctx); len(ws) != 0 {
		t.Errorf("an episode was filed as a request: %+v", ws)
	}
}

func TestIdentityOfAnEvent(t *testing.T) {
	s, e := 4, 7
	h, ok := holdingOf(events.ItemEvent{ItemID: "e7", SeriesExternalIDs: identity.IDs{"tvdb": "362753"}, SeasonNumber: &s, EpisodeNumber: &e}, true)
	if !ok || h.Kind != "episode" || h.TVDBID != 362753 || !h.Playable {
		t.Errorf("episode: %+v %v", h, ok)
	}
	if _, ok := holdingOf(events.ItemEvent{ItemID: "x"}, false); ok {
		t.Error("an event without identities holds nothing by itself")
	}
	m, ok := holdingOf(events.ItemEvent{ItemID: "m", Type: "movie", ExternalIDs: identity.IDs{"imdb": "tt0133093"}}, false)
	if !ok || m.Kind != "movie" || m.IMDBID != "tt0133093" {
		t.Errorf("movie: %+v %v", m, ok)
	}
}
