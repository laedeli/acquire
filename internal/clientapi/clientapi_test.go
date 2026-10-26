package clientapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeQBittorrent is the slice of the WebUI API the client uses: a login that
// sets SID, torrents/info filtered by tag, torrents/delete. expireOnce makes
// the first authenticated call answer 403, as an expired session does.
type fakeQBittorrent struct {
	mu         sync.Mutex
	logins     int
	deleted    []string // "hash deleteFiles=…"
	torrents   map[string]Torrent
	expireOnce bool
}

func (f *fakeQBittorrent) server(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.URL.Path == "/api/v2/auth/login" {
			_ = r.ParseForm()
			if r.Form.Get("username") != "acquire" || r.Form.Get("password") != "pw" {
				_, _ = w.Write([]byte("Fails."))
				return
			}
			f.logins++
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "s", Path: "/"})
			_, _ = w.Write([]byte("Ok."))
			return
		}
		if c, err := r.Cookie("SID"); err != nil || c.Value == "" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if f.expireOnce {
			f.expireOnce = false
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if r.Header.Get("Referer") == "" {
			t.Errorf("%s without a Referer", r.URL.Path)
		}
		switch r.URL.Path {
		case "/api/v2/torrents/info":
			out := []Torrent{}
			if tor, ok := f.torrents[r.URL.Query().Get("tag")]; ok {
				out = append(out, tor)
			}
			_ = json.NewEncoder(w).Encode(out)
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

func TestQBittorrent(t *testing.T) {
	f := &fakeQBittorrent{torrents: map[string]Torrent{
		"dlg-seeding": {Hash: "aaa", State: "uploading"},
		"dlg-done":    {Hash: "bbb", State: "stoppedUP"},
	}, expireOnce: true}
	srv := f.server(t)
	q := NewQBittorrent(srv.URL+"/", "acquire", "pw", srv.Client())
	ctx := context.Background()

	tor, found, err := q.Torrent(ctx, "dlg-seeding")
	if err != nil || !found || tor.Hash != "aaa" || tor.SeedingDone() {
		t.Fatalf("seeding torrent: %+v %v %v", tor, found, err)
	}
	if f.logins != 2 {
		t.Errorf("logins = %d: want one, and one more after the session expired", f.logins)
	}
	if tor, found, err := q.Torrent(ctx, "dlg-done"); err != nil || !found || !tor.SeedingDone() {
		t.Fatalf("stopped torrent: %+v %v %v", tor, found, err)
	}
	if _, found, err := q.Torrent(ctx, "dlg-gone"); err != nil || found {
		t.Fatalf("missing torrent: %v %v", found, err)
	}
	if err := q.DeleteWithData(ctx, "bbb"); err != nil {
		t.Fatal(err)
	}
	if strings.Join(f.deleted, ",") != "bbb deleteFiles=true" {
		t.Errorf("deleted %v", f.deleted)
	}
	if (Torrent{State: "pausedUP"}).SeedingDone() != true || (Torrent{State: "stalledUP"}).SeedingDone() {
		t.Error("SeedingDone: pausedUP is done, stalledUP still seeds")
	}

	bad := NewQBittorrent(srv.URL, "acquire", "wrong", srv.Client())
	if _, _, err := bad.Torrent(ctx, "dlg-done"); err == nil || strings.Contains(err.Error(), "wrong") {
		t.Errorf("a refused login: %v", err)
	}
}

func TestNZBGetRelease(t *testing.T) {
	var got []any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "acquire" || pass != "pw" || r.URL.Path != "/jsonrpc" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var body struct {
			Method string `json:"method"`
			Params []any  `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Method == "history" {
			_ = json.NewEncoder(w).Encode(map[string]any{"result": []map[string]any{
				{"NZBID": 41, "Name": "Show.Name.S01E02.1080p.WEB.h264-GRP", "DestDir": "/downloads/complete/acquire/Show.Name.S01E02.1080p.WEB.h264-GRP", "Status": "SUCCESS/ALL"},
				{"NZBID": 42, "Name": "Show.Name.S01E03.1080p.WEB.h264-GRP", "DestDir": "/downloads/complete/acquire/Show.Name.S01E03.1080p.WEB.h264-GRP", "Status": "SUCCESS/ALL"},
			}})
			return
		}
		got = body.Params
		ids, _ := body.Params[3].([]any)
		ok = body.Method == "editqueue" && len(ids) == 1 && ids[0] == float64(42)
		_ = json.NewEncoder(w).Encode(map[string]any{"result": ok})
	}))
	defer srv.Close()
	n := NewNZBGet(srv.URL, "acquire", "pw", srv.Client())
	if err := n.Release(context.Background(), 42); err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 || got[0] != "HistorySetParameter" || got[2] != "acquire:release=yes" {
		t.Errorf("editqueue params %v", got)
	}
	if err := n.Release(context.Background(), 7); err == nil {
		t.Error("a record NZBGet does not keep was released")
	}
	if err := NewNZBGet(srv.URL, "acquire", "wrong", srv.Client()).Release(context.Background(), 42); err == nil || !strings.Contains(err.Error(), "401") {
		t.Errorf("wrong login: %v", err)
	}

	rec, found, err := n.Record(context.Background(), 42)
	if err != nil || !found || rec.Name != "Show.Name.S01E03.1080p.WEB.h264-GRP" ||
		rec.DestDir != "/downloads/complete/acquire/Show.Name.S01E03.1080p.WEB.h264-GRP" || rec.Status != "SUCCESS/ALL" {
		t.Errorf("record 42: %+v %v %v", rec, found, err)
	}
	if _, found, err := n.Record(context.Background(), 7); err != nil || found {
		t.Errorf("a record NZBGet does not keep: %v %v", found, err)
	}
	if _, _, err := NewNZBGet(srv.URL, "acquire", "wrong", srv.Client()).Record(context.Background(), 42); err == nil {
		t.Error("a refused login read a record")
	}
}
