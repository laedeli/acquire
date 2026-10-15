package app

import (
	"context"
	"encoding/json"
	"errors"
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
	"github.com/laedeli/acquire/internal/katalog"
	"github.com/laedeli/acquire/internal/store"
)

// ── copy-on-import, end to end over a real database ────────────────────────

// fakeCatalog is katalog-manager's ingest: it records the paths it is handed
// and fails the first failFirst hand-overs with 503.
type fakeCatalog struct {
	mu        sync.Mutex
	paths     []string
	failFirst int
}

func (f *fakeCatalog) server(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		var req katalog.IngestRequest
		if r.URL.Path != "/api/ingest" || json.NewDecoder(r.Body).Decode(&req) != nil {
			http.NotFound(w, r)
			return
		}
		if f.failFirst > 0 {
			f.failFirst--
			http.Error(w, "busy", http.StatusServiceUnavailable)
			return
		}
		f.paths = append(f.paths, req.Path)
		_ = json.NewEncoder(w).Encode(katalog.IngestResult{ItemID: "item-1", Created: true})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// fakeClients answers NZBGet's JSON-RPC and qBittorrent's WebUI API, and
// records what acquire asked of them.
type fakeClients struct {
	mu        sync.Mutex
	editqueue [][]any
	state     string // the torrent's state
	deleted   []string
}

func (f *fakeClients) server(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch r.URL.Path {
		case "/jsonrpc":
			var body struct {
				Method string `json:"method"`
				Params []any  `json:"params"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.editqueue = append(f.editqueue, body.Params)
			_ = json.NewEncoder(w).Encode(map[string]any{"result": true})
		case "/api/v2/auth/login":
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "s", Path: "/"})
			_, _ = w.Write([]byte("Ok."))
		case "/api/v2/torrents/info":
			_ = json.NewEncoder(w).Encode([]map[string]string{{"hash": "abc", "state": f.state}})
		case "/api/v2/torrents/delete":
			_ = r.ParseForm()
			f.deleted = append(f.deleted, r.Form.Get("hashes")+" deleteFiles="+r.Form.Get("deleteFiles"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

type importRig struct {
	svc       *Service
	catalog   *fakeCatalog
	clients   *fakeClients
	downloads string // the downloads volume as acquire sees it
	arrivals  string
}

// newImportRig is a service whose imports are copies, with the deployment's
// two clients declared on a downloads volume in a temporary folder.
func newImportRig(t *testing.T) *importRig {
	t.Helper()
	rig := &importRig{catalog: &fakeCatalog{}, clients: &fakeClients{state: "uploading"}}
	rig.svc = e2eService(t, &fakeGateway{}, testBox(t))
	rig.svc.kc = katalog.New(rig.catalog.server(t).URL, "", nil)
	root := t.TempDir()
	rig.downloads, rig.arrivals = filepath.Join(root, "downloads"), filepath.Join(root, "arrivals")
	if err := os.MkdirAll(rig.arrivals, 0o755); err != nil {
		t.Fatal(err)
	}
	rig.svc.cfg.ArrivalsRoot = rig.arrivals
	url := rig.clients.server(t).URL
	declared := make([]map[string]any, 0, 2)
	for _, c := range []map[string]any{chartQB("pw"), chartNZB("pw")} {
		c["baseUrl"] = url
		c["localPath"] = filepath.Join(rig.downloads, c["id"].(string), "complete")
		c["remotePath"] = "/downloads/complete"
		declared = append(declared, c)
	}
	rig.svc.cfg.ManagedClients, rig.svc.cfg.ManagedClientsSet = declaration(t, declared...), true
	if res, err := rig.svc.ApplyManagedClients(context.Background()); err != nil || len(res.Created) != 2 {
		t.Fatalf("clients: %+v %v", res, err)
	}
	return rig
}

// finished puts a finished download on the downloads volume (files relative
// to the client's complete folder) and announces it as the gateway would.
func (rig *importRig) finished(t *testing.T, client, job string, files map[string]string, reported string) store.Wanted {
	t.Helper()
	ctx := context.Background()
	for name, body := range files {
		p := filepath.Join(rig.downloads, client, "complete", name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	w, err := rig.svc.Request(ctx, store.Wanted{Title: "Film", Year: 2020, MediaType: "movie"}, "user")
	if err != nil {
		t.Fatal(err)
	}
	if err := rig.svc.OnCompleted(ctx, events.DownloadEvent{
		Adapter: client, ClientID: job, WantedID: w.ID, Files: []string{"/downloads/complete/" + reported},
	}); err != nil {
		t.Fatal(err)
	}
	return w
}

func (rig *importRig) wanted(t *testing.T, id string) store.Wanted {
	t.Helper()
	w, err := rig.svc.st.GetWanted(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func (rig *importRig) importOf(t *testing.T, client, job string) store.Import {
	t.Helper()
	im, err := rig.svc.st.GetImport(context.Background(), client, job)
	if err != nil {
		t.Fatal(err)
	}
	return im
}

// due makes an import due now, as if its wait had run out.
func (rig *importRig) due(t *testing.T, client, job string) {
	t.Helper()
	im := rig.importOf(t, client, job)
	im.NextAt = time.Now().Add(-time.Second)
	if err := rig.svc.st.SaveImport(context.Background(), im); err != nil {
		t.Fatal(err)
	}
}

func treeOf(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(root, p)
			out = append(out, rel)
		}
		return nil
	})
	sort.Strings(out)
	return out
}

// An NZB download: copied with its subtitles (and nothing else), handed to the
// catalog at the copy's path, then released in NZBGet. Its own files stay.
func TestImportCopiesHandsOverAndReleasesAnNZB(t *testing.T) {
	rig := newImportRig(t)
	ctx := context.Background()
	files := map[string]string{
		"acquire/Film.2020/Film.2020.mkv":    "the film",
		"acquire/Film.2020/Film.2020.en.srt": "subtitles",
		"acquire/Film.2020/Film.2020.nfo":    "info",
		"acquire/Film.2020/sample.mkv":       "x",
	}
	w := rig.finished(t, "nzbget", "42", files, "acquire/Film.2020")
	if got := rig.wanted(t, w.ID); got.Status != "downloading" || !strings.Contains(got.Detail, "copying into the library") {
		t.Fatalf("after completion: %s %q", got.Status, got.Detail)
	}
	if len(rig.catalog.paths) != 0 {
		t.Fatal("handed over before it was copied")
	}

	if !rig.svc.importNext(ctx) {
		t.Fatal("no import was due")
	}
	dir := filepath.Join("acquire", w.ID, "nzbget-42")
	if got := treeOf(t, rig.arrivals); !reflect.DeepEqual(got, []string{filepath.Join(dir, "Film.2020.en.srt"), filepath.Join(dir, "Film.2020.mkv")}) {
		t.Fatalf("arrivals: %v", got)
	}
	if b, _ := os.ReadFile(filepath.Join(rig.arrivals, dir, "Film.2020.mkv")); string(b) != "the film" {
		t.Errorf("the copy holds %q", b)
	}
	if want := filepath.Join(rig.arrivals, dir, "Film.2020.mkv"); !reflect.DeepEqual(rig.catalog.paths, []string{want}) {
		t.Errorf("handed over %v, want %s", rig.catalog.paths, want)
	}
	if got := rig.wanted(t, w.ID); got.Status != "packaging" || got.ItemID != "item-1" {
		t.Errorf("after the hand-over: %s %q item %q", got.Status, got.Detail, got.ItemID)
	}
	if len(rig.clients.editqueue) != 1 || !reflect.DeepEqual(rig.clients.editqueue[0], []any{"HistorySetParameter", float64(0), "acquire:release=yes", []any{float64(42)}}) {
		t.Errorf("nzbget was asked %v", rig.clients.editqueue)
	}
	if im := rig.importOf(t, "nzbget", "42"); im.State != store.ImportDone {
		t.Errorf("import %s: %s", im.State, im.Detail)
	}
	if got := treeOf(t, filepath.Join(rig.downloads, "nzbget")); len(got) != len(files) {
		t.Errorf("acquire touched the client's files: %v", got)
	}
	if rig.svc.importNext(ctx) {
		t.Error("work was left after the release")
	}
}

// A copy that fails leaves the download where it is and is tried again; a
// hand-over the catalog cannot take yet is retried without copying again.
func TestImportRetriesAFailedCopyAndHandOver(t *testing.T) {
	rig := newImportRig(t)
	ctx := context.Background()
	if err := os.Chmod(rig.arrivals, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(rig.arrivals, 0o755) })
	w := rig.finished(t, "nzbget", "7", map[string]string{"Film.2020.mkv": "the film"}, "Film.2020.mkv")

	rig.svc.importNext(ctx)
	im := rig.importOf(t, "nzbget", "7")
	if im.State != store.ImportPending || im.Attempts != 1 || !strings.Contains(im.Detail, "copying into the library failed") ||
		time.Until(im.NextAt) < 50*time.Second {
		t.Fatalf("after a failed copy: %+v", im)
	}
	if got := rig.wanted(t, w.ID); got.Status != "downloading" || !strings.Contains(got.Detail, "trying again in 1m0s") {
		t.Errorf("the request says %s %q", got.Status, got.Detail)
	}
	if got := treeOf(t, rig.arrivals); len(got) != 0 {
		t.Errorf("a failed copy left %v", got)
	}
	if b, err := os.ReadFile(filepath.Join(rig.downloads, "nzbget", "complete", "Film.2020.mkv")); err != nil || string(b) != "the film" {
		t.Fatal("the download was touched")
	}
	if rig.svc.importNext(ctx) {
		t.Error("retried before its wait ran out")
	}

	if err := os.Chmod(rig.arrivals, 0o755); err != nil {
		t.Fatal(err)
	}
	rig.catalog.failFirst = 1
	rig.due(t, "nzbget", "7")
	rig.svc.importNext(ctx)
	im = rig.importOf(t, "nzbget", "7")
	if im.State != store.ImportPending || im.Attempts != 2 || !strings.Contains(im.Detail, "hand-over") || im.Arrival == "" {
		t.Fatalf("after a failed hand-over: %+v", im)
	}
	copied, _ := os.Stat(im.Arrival)
	rig.due(t, "nzbget", "7")
	rig.svc.importNext(ctx)
	if im := rig.importOf(t, "nzbget", "7"); im.State != store.ImportDone || len(rig.catalog.paths) != 1 {
		t.Fatalf("after the retry: %+v, handed over %v", im, rig.catalog.paths)
	}
	if again, _ := os.Stat(rig.catalog.paths[0]); !os.SameFile(copied, again) {
		t.Error("the good copy was written again")
	}
}

// A torrent seeds on after its import and is removed, with its data, through
// qBittorrent's API once it stopped at its goal.
func TestImportReleasesATorrentAtItsGoal(t *testing.T) {
	rig := newImportRig(t)
	ctx := context.Background()
	w := rig.finished(t, "qbittorrent", "dlg-1", map[string]string{"Film.2020.mkv": "the film"}, "Film.2020.mkv")

	rig.svc.importNext(ctx) // copied and handed over
	if got := rig.wanted(t, w.ID); got.Status != "packaging" {
		t.Fatalf("after the hand-over: %s %q", got.Status, got.Detail)
	}
	if im := rig.importOf(t, "qbittorrent", "dlg-1"); im.State != store.ImportImported {
		t.Fatalf("import %+v", im)
	}
	rig.svc.importNext(ctx) // still seeding
	im := rig.importOf(t, "qbittorrent", "dlg-1")
	if im.State != store.ImportImported || !strings.Contains(im.Detail, "seeding") || time.Until(im.NextAt) < 9*time.Minute || len(rig.clients.deleted) != 0 {
		t.Fatalf("while seeding: %+v, deleted %v", im, rig.clients.deleted)
	}

	rig.clients.state = "stoppedUP"
	rig.due(t, "qbittorrent", "dlg-1")
	rig.svc.importNext(ctx)
	if !reflect.DeepEqual(rig.clients.deleted, []string{"abc deleteFiles=true"}) {
		t.Errorf("qbittorrent was asked to delete %v", rig.clients.deleted)
	}
	if im := rig.importOf(t, "qbittorrent", "dlg-1"); im.State != store.ImportDone {
		t.Errorf("import %+v", im)
	}
	if _, err := os.Stat(filepath.Join(rig.downloads, "qbittorrent", "complete", "Film.2020.mkv")); err != nil {
		t.Error("acquire deleted the torrent's file itself")
	}
}

// ── the space guard ────────────────────────────────────────────────────────

func TestDownloadsVolumeGuard(t *testing.T) {
	const gb = int64(1) << 30
	s := &Service{freeSpace: func(string) (int64, error) { return 100 * gb, nil }}
	s.cfg.ArrivalsRoot, s.cfg.DownloadsReserveBytes = "/arrivals", 30*gb
	managed := store.DownloadClient{ID: "nzbget", Managed: true, LocalPath: "/downloads/nzbget/complete"}

	cases := []struct {
		protocol string
		size     int64
		admit    bool
		says     string
	}{
		{"usenet", 35 * gb, true, ""}, // 2 × 35 + 30 = 100
		{"usenet", 36 * gb, false, "this NZB needs 102.0 GB on the downloads volume (twice its 36.0 GB"},
		{"torrent", 70 * gb, true, ""}, // 70 + 30 = 100
		{"torrent", 71 * gb, false, "this release needs 101.0 GB on the downloads volume (its 71.0 GB and the 30.0 GB reserve) and 100.0 GB is free"},
		{"torrent", 0, true, ""},
	}
	for _, c := range cases {
		err := s.admitDownloadsVolume(managed, c.size, c.protocol)
		var room noRoom
		if (err == nil) != c.admit || (!c.admit && (!errors.As(err, &room) || !strings.Contains(err.Error(), c.says))) {
			t.Errorf("%s %d GB: %v", c.protocol, c.size/gb, err)
		}
	}
	s.freeSpace = func(string) (int64, error) { return 10 * gb, nil }
	if err := s.admitDownloadsVolume(managed, 0, "torrent"); err == nil || !strings.Contains(err.Error(), "keeps a 30.0 GB reserve and has 10.0 GB free") {
		t.Errorf("below the reserve: %v", err)
	}
	s.freeSpace = func(string) (int64, error) { return 0, errors.New("not mounted") }
	if err := s.admitDownloadsVolume(managed, gb, "torrent"); err == nil || errors.As(err, new(noRoom)) {
		t.Errorf("an unreadable volume must refuse outright, not wait: %v", err)
	}
	if !s.onDownloadsVolume(managed) || s.onDownloadsVolume(store.DownloadClient{ID: "external"}) {
		t.Error("only the deployment's clients work on the downloads volume")
	}
}

// A release the downloads volume cannot take waits: the request keeps its
// state and says why.
func TestARequestSaysWhyItsReleaseWaits(t *testing.T) {
	rig := newImportRig(t)
	ctx := context.Background()
	rig.svc.cfg.DownloadsReserveBytes = 1 << 50 // more than any test disk has free
	if err := os.MkdirAll(filepath.Join(rig.downloads, "qbittorrent", "complete"), 0o755); err != nil {
		t.Fatal(err)
	}
	w, err := rig.svc.Request(ctx, store.Wanted{Title: "Film", MediaType: "movie"}, "user")
	if err != nil {
		t.Fatal(err)
	}
	err = rig.svc.Grab(ctx, w.ID, "magnet:?xt=urn:btih:abc", "qbittorrent")
	if err == nil || !strings.Contains(err.Error(), "waiting for room") {
		t.Fatalf("grab: %v", err)
	}
	if got := rig.wanted(t, w.ID); got.Status != "pending" || !strings.Contains(got.Detail, "waiting for room") {
		t.Errorf("the request: %s %q", got.Status, got.Detail)
	}
}
