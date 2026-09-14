package app

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/laedeli/acquire/internal/config"
	"github.com/laedeli/acquire/internal/endpoint"
	"github.com/laedeli/acquire/internal/gateway"
	"github.com/laedeli/acquire/internal/secretbox"
	"github.com/laedeli/acquire/internal/store"
)

const sampleNZB = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE nzb PUBLIC "-//newzBin//DTD NZB 1.1//EN" "http://www.newzbin.com/DTD/nzb/nzb-1.1.dtd">
<nzb xmlns="http://www.newzbin.com/DTD/2003/nzb"><file subject="example"></file></nzb>`

const sampleTorrent = "d8:announce22:http://tracker.test/a4:infod4:name7:examplee"

func TestSniffRelease(t *testing.T) {
	cases := map[string]string{
		sampleNZB:                          "usenet",
		"\xef\xbb\xbf  " + sampleNZB:       "usenet",
		sampleTorrent:                      "torrent",
		"<html><body>limit reached</body>": "",
		`{"error":"apikey invalid"}`:       "",
		"dear user":                        "",
	}
	for body, want := range cases {
		if got := sniffRelease([]byte(body)); got != want {
			t.Errorf("sniff(%.30q) = %q, want %q", body, got, want)
		}
	}
}

func TestInferProtocol(t *testing.T) {
	cases := map[string]string{
		"magnet:?xt=urn:btih:abc":                   "torrent",
		"https://idx.test/dl/Example.torrent?r=key": "torrent",
		"https://idx.test/getnzb/abc.nzb":           "usenet",
		"https://idx.test/api?t=get&id=1":           "",
		"https://hoster.test/file/abc":              "",
	}
	for link, want := range cases {
		if got := inferProtocol(link); got != want {
			t.Errorf("inferProtocol(%q) = %q, want %q", link, got, want)
		}
	}
}

func fetchService() *Service {
	p := endpoint.Policy{}
	return &Service{policy: p, fetch: p.HTTPClient(10 * time.Second)}
}

func TestFetchRelease(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/nzb":
			w.Header().Set("Content-Disposition", `attachment; filename="Example.2020.1080p.nzb"`)
			_, _ = w.Write([]byte(sampleNZB))
		case "/torrent":
			_, _ = w.Write([]byte(sampleTorrent))
		case "/limit":
			_, _ = w.Write([]byte("<html>daily API limit reached</html>"))
		case "/magnet":
			w.Header().Set("Location", "magnet:?xt=urn:btih:abc")
			w.WriteHeader(http.StatusFound)
		case "/huge-torrent":
			_, _ = w.Write([]byte("d8:announce"))
			_, _ = w.Write(make([]byte, maxTorrentBytes))
		case "/fail":
			http.Error(w, "nope", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	s := fetchService()
	ctx := context.Background()

	f, err := s.fetchRelease(ctx, srv.URL+"/nzb?apikey=SECRET", "usenet", "Example")
	if err != nil || f.Kind != "usenet" || string(f.Body) != sampleNZB || f.Name != "Example.2020.1080p.nzb" {
		t.Fatalf("nzb: %+v %v", f, err)
	}
	f, err = s.fetchRelease(ctx, srv.URL+"/torrent", "", "Some: Title/With Slashes")
	if err != nil || f.Kind != "torrent" || f.Name != "Some_ Title_With Slashes.torrent" {
		t.Fatalf("torrent sniffed from an unknown link: %+v %v", f, err)
	}
	if _, err := s.fetchRelease(ctx, srv.URL+"/limit?apikey=SECRET", "usenet", "x"); err == nil || !strings.Contains(err.Error(), "did not return an NZB") {
		t.Fatalf("an error page served as 200 must fail: %v", err)
	}
	if f, err := s.fetchRelease(ctx, srv.URL+"/limit", "", "x"); err != nil || f.Kind != "" {
		t.Fatalf("an unknown link that is not a release file is a hoster link: %+v %v", f, err)
	}
	if f, err := s.fetchRelease(ctx, srv.URL+"/magnet", "torrent", "x"); err != nil || f.Magnet != "magnet:?xt=urn:btih:abc" {
		t.Fatalf("magnet redirect: %+v %v", f, err)
	}
	if _, err := s.fetchRelease(ctx, srv.URL+"/huge-torrent", "torrent", "x"); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("oversize: %v", err)
	}
	_, err = s.fetchRelease(ctx, srv.URL+"/fail?apikey=SECRET", "usenet", "x")
	if err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("failure must not echo the key: %v", err)
	}
	_, err = s.fetchRelease(ctx, "http://127.0.0.1:1/x?apikey=SECRET", "usenet", "x")
	if err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("transport error must not echo the key: %v", err)
	}
	if _, err := s.fetchRelease(ctx, "http://169.254.169.254/latest", "usenet", "x"); err == nil {
		t.Fatal("a metadata address was fetched")
	}
}

// ── end to end over a real database ────────────────────────────────────────

// testStore opens a store in a schema of its own, so these tests can run at
// the same time as the store package's, which reset the public schema.
func testStore(t *testing.T) *store.Store {
	t.Helper()
	dsn := os.Getenv("TEST_DSN")
	if dsn == "" {
		t.Skip("TEST_DSN not set; skipping database tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx, `DROP SCHEMA IF EXISTS acquire_app_test CASCADE; CREATE SCHEMA acquire_app_test;`); err != nil {
		t.Fatal(err)
	}
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	st, err := store.New(ctx, dsn+sep+"search_path=acquire_app_test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	return st
}

func testBox(t *testing.T) *secretbox.Box {
	t.Helper()
	raw := make([]byte, 32)
	_, _ = rand.Read(raw)
	b, err := secretbox.New(base64.StdEncoding.EncodeToString(raw), "")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// fakeGateway records adds and configuration pushes. unknownOnce makes the
// first add fail as if the gateway had just restarted.
type fakeGateway struct {
	mu          sync.Mutex
	adds        []map[string]any
	puts        []gateway.ConfigPut
	unknownOnce bool
}

func (g *fakeGateway) server(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		defer g.mu.Unlock()
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/downloads":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			g.adds = append(g.adds, body)
			if g.unknownOnce && len(g.puts) == 0 {
				http.Error(w, `{"error":"no such client"}`, http.StatusNotFound)
				return
			}
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"adapter":"nzbget","client_job_id":"101"}`))
		case r.Method == http.MethodPut && r.URL.Path == "/api/v1/config/clients":
			var body gateway.ConfigPut
			_ = json.NewDecoder(r.Body).Decode(&body)
			g.puts = append(g.puts, body)
			ids := []string{}
			for _, c := range body.Clients {
				ids = append(ids, c.ID)
			}
			_ = json.NewEncoder(w).Encode(gateway.ApplyResult{Revision: body.Revision, Applied: ids})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/config/types":
			_ = json.NewEncoder(w).Encode(builtinClientTypes)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/config/clients":
			rev := int64(0)
			if n := len(g.puts); n > 0 {
				rev = g.puts[n-1].Revision
			}
			_ = json.NewEncoder(w).Encode(gateway.ConfigState{Revision: rev})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/clients/status":
			_, _ = w.Write([]byte(`[]`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func e2eService(t *testing.T, gw *fakeGateway, box *secretbox.Box) *Service {
	t.Helper()
	st := testStore(t)
	srv := gw.server(t)
	cfg := config.Config{GatewayURL: srv.URL, DownloadsRoot: t.TempDir(), PreferUsenet: true}
	svc := New(cfg, st, gateway.New(srv.URL, nil), nil, nil, nil, box)
	// Hostnames in these tests do not resolve; the policy must not care.
	svc.policy.Resolve = func(context.Context, string) ([]netip.Addr, error) { return nil, errors.New("offline") }
	ctx := context.Background()
	if err := svc.SeedSettings(ctx); err != nil {
		t.Fatal(err)
	}
	// A laptop disk is below the production floor; admission is not under test.
	if _, err := svc.SaveSearchSettings(ctx, SearchSettings{PreferProtocol: "usenet", StorageFloorGB: 0, MaxConcurrentGrabs: 3}, 0, "test"); err != nil {
		t.Fatal(err)
	}
	return svc
}

// The whole hand-off: route by protocol, fetch the NZB with the source's key,
// give the gateway the content (never the link), and keep the link sealed.
func TestHandOffSendsThePayloadAndKeepsTheKeySealed(t *testing.T) {
	gw := &fakeGateway{unknownOnce: true}
	box := testBox(t)
	svc := e2eService(t, gw, box)
	ctx := context.Background()

	indexer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("apikey") != "SECRET" {
			http.Error(w, "bad key", http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(sampleNZB))
	}))
	defer indexer.Close()

	local := t.TempDir()
	created, err := svc.CreateClient(ctx, ClientInput{
		Type: "nzbget", BaseURL: "http://worker:6789", Auth: "basic", Username: "control",
		Secret: SecretInput{Present: true, Value: "pw"}, RemotePath: "/downloads", LocalPath: local,
	}, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if created.ID != "nzbget" || !created.Secret.Set || !created.Sync.Applied {
		t.Fatalf("created: %+v", created)
	}
	if len(gw.puts) != 1 || gw.puts[0].Clients[0].Secret != "pw" || gw.puts[0].Clients[0].SavePath != "/downloads" {
		t.Fatalf("the write was not pushed with its secret: %+v", gw.puts)
	}
	// Simulate a gateway restart: the first add is refused as unknown.
	gw.puts = nil

	if err := svc.st.CreateWanted(ctx, store.Wanted{ID: "w_1", Title: "Example", MediaType: "movie"}); err != nil {
		t.Fatal(err)
	}
	link := indexer.URL + "/api?t=get&id=abc&apikey=SECRET"
	out, err := svc.handOff(ctx, handoff{WantedID: "w_1", Title: "Example", Link: link, Protocol: "usenet",
		ReleaseTitle: "Example.2020.1080p", Indexer: "example-source"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Client.ID != "nzbget" || out.JobID != "101" {
		t.Fatalf("handed off to %+v", out)
	}
	if len(gw.adds) != 2 || len(gw.puts) != 1 {
		t.Fatalf("unknown client was not pushed and retried once: adds=%d puts=%d", len(gw.adds), len(gw.puts))
	}
	add := gw.adds[1]
	payload, _ := base64.StdEncoding.DecodeString(add["payload_b64"].(string))
	if string(payload) != sampleNZB || add["client"] != "nzbget" || add["category"] != "acquire" || add["save_path"] != "/downloads" {
		t.Fatalf("add body: %v", add)
	}
	for _, a := range gw.adds {
		body, _ := json.Marshal(a)
		if strings.Contains(string(body), "SECRET") {
			t.Fatalf("the source's key reached the gateway: %s", body)
		}
	}

	g, err := svc.st.LatestGrab(ctx, "w_1")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(g.Source, "SECRET") || !strings.Contains(g.Source, "id=abc") {
		t.Fatalf("stored source not redacted: %q", g.Source)
	}
	ct, kid := sealedSource(t, svc, "w_1")
	table, id, field := store.GrabAAD("w_1", "nzbget", "101")
	full, err := box.Open(table, id, field, ct, kid)
	if err != nil || string(full) != link {
		t.Fatalf("the full link is not recoverable from source_ct: %q %v", full, err)
	}
}

func sealedSource(t *testing.T, svc *Service, wantedID string) ([]byte, string) {
	t.Helper()
	var ct []byte
	var kid string
	dsn := os.Getenv("TEST_DSN")
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := pool.QueryRow(context.Background(),
		`SELECT source_ct, source_kid FROM acquire_app_test.grabs WHERE wanted_id=$1`, wantedID).Scan(&ct, &kid); err != nil {
		t.Fatal(err)
	}
	return ct, kid
}

func TestNoRouteIsAnActionableError(t *testing.T) {
	svc := e2eService(t, &fakeGateway{}, testBox(t))
	_, err := svc.handOff(context.Background(), handoff{WantedID: "w", Title: "x", Link: "magnet:?xt=urn:btih:abc"})
	if err == nil || err.Error() != "no download client handles torrent — add one under download clients" {
		t.Fatalf("err = %v", err)
	}
}

func TestSecretsNeedAKey(t *testing.T) {
	svc := e2eService(t, &fakeGateway{}, &secretbox.Box{})
	ctx := context.Background()
	_, err := svc.CreateClient(ctx, ClientInput{Type: "nzbget", BaseURL: "http://worker", Auth: "basic",
		Username: "u", Secret: SecretInput{Present: true, Value: "pw"}}, "admin")
	if !errors.Is(err, ErrNoKey) {
		t.Fatalf("secret without a key: %v, want ErrNoKey", err)
	}
	// A client without credentials needs no key.
	if _, err := svc.CreateClient(ctx, ClientInput{Type: "qbittorrent", BaseURL: "http://worker", Auth: "none"}, "admin"); err != nil {
		t.Fatalf("no-auth client refused without a key: %v", err)
	}
	st := svc.Setup(ctx)
	for _, sec := range st.Sections {
		if sec.Key == "clients" && (sec.State != SetupNeedsSetup || !strings.Contains(sec.Summary, "ACQUIRE_CONFIG_KEY")) {
			t.Fatalf("clients section without a key: %+v", sec)
		}
	}
	if st.State != SetupNeedsSetup {
		t.Fatalf("setup state %q, want needs-setup", st.State)
	}
}

func TestClientIdentityIsFixedAfterCreation(t *testing.T) {
	svc := e2eService(t, &fakeGateway{}, testBox(t))
	ctx := context.Background()
	c, err := svc.CreateClient(ctx, ClientInput{Type: "qbittorrent", BaseURL: "http://worker", Auth: "none"}, "admin")
	if err != nil {
		t.Fatal(err)
	}
	var ve *ValidationError
	if _, err := svc.UpdateClient(ctx, c.ID, ClientInput{Type: "nzbget", BaseURL: "http://worker", Auth: "none"}, c.Revision, "admin"); !errors.As(err, &ve) {
		t.Fatalf("type change: %v", err)
	}
	if _, err := svc.UpdateClient(ctx, c.ID, ClientInput{ID: "renamed", BaseURL: "http://worker", Auth: "none"}, c.Revision, "admin"); !errors.As(err, &ve) {
		t.Fatalf("id change: %v", err)
	}
	upd, err := svc.UpdateClient(ctx, c.ID, ClientInput{BaseURL: "http://worker:9090", Auth: "none"}, c.Revision, "admin")
	if err != nil || upd.BaseURL != "http://worker:9090" {
		t.Fatalf("plain update: %+v %v", upd, err)
	}
	if _, err := svc.UpdateClient(ctx, c.ID, ClientInput{BaseURL: "http://worker", Auth: "none"}, c.Revision, "admin"); !errors.Is(err, store.ErrStale) {
		t.Fatalf("stale update: %v", err)
	}
}
