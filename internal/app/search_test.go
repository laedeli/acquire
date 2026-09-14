package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/laedeli/acquire/internal/release"
	"github.com/laedeli/acquire/internal/store"
)

func releaseFor(link string) release.Release {
	return release.Release{Protocol: "usenet", Title: "Example.Movie.2020.1080p.BluRay.x265-G", Indexer: "example",
		IndexerID: 7, GUID: "guid-1", Size: 8 << 30, Link: link}
}

func releaseProfile() release.Profile { return release.DefaultProfile() }

func encodeRef(ct []byte) string { return base64.RawURLEncoding.EncodeToString(ct) }

// fakeSource is a newznab or torznab endpoint. mode switches how it answers.
type fakeSource struct {
	mu       sync.Mutex
	key      string
	protocol string
	mode     string // "" | slow | 401 | 503
	delay    time.Duration
	titles   []string
	queries  []string
	srv      *httptest.Server
}

func newFakeSource(t *testing.T, protocol, key string, titles ...string) *fakeSource {
	f := &fakeSource{key: key, protocol: protocol, titles: titles}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeSource) set(mode string) {
	f.mu.Lock()
	f.mode = mode
	f.mu.Unlock()
}

func (f *fakeSource) slowBy(d time.Duration) {
	f.mu.Lock()
	f.mode, f.delay = "slow", d
	f.mu.Unlock()
}

func (f *fakeSource) offer(titles ...string) {
	f.mu.Lock()
	f.titles = titles
	f.mu.Unlock()
}

func (f *fakeSource) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	mode, delay, titles := f.mode, f.delay, f.titles
	q := r.URL.Query()
	f.queries = append(f.queries, q.Get("t"))
	f.mu.Unlock()
	switch mode {
	case "slow":
		select {
		case <-time.After(delay):
		case <-r.Context().Done():
			return
		}
	case "401":
		w.WriteHeader(http.StatusUnauthorized)
		return
	case "503":
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	if f.key != "" && q.Get("apikey") != f.key && q.Get("t") != "caps" {
		_, _ = w.Write([]byte(`<error code="100" description="Incorrect user credentials"/>`))
		return
	}
	switch q.Get("t") {
	case "caps":
		_, _ = w.Write([]byte(`<caps><server title="Example"/><limits max="100" default="50"/>
		  <searching><search available="yes" supportedParams="q"/>
		  <movie-search available="yes" supportedParams="q,imdbid"/></searching>
		  <categories><category id="2000" name="Movies"><subcat id="2040" name="Movies/HD"/></category></categories></caps>`))
	case "get":
		if f.protocol == "torrent" {
			_, _ = w.Write([]byte(sampleTorrent))
			return
		}
		_, _ = w.Write([]byte(sampleNZB))
	default:
		var b strings.Builder
		b.WriteString(`<rss xmlns:newznab="http://www.newznab.com/DTD/2010/feeds/attributes/"><channel>`)
		for i, title := range titles {
			if f.protocol == "torrent" {
				fmt.Fprintf(&b, `<item><title>%s</title><guid>t%d</guid><enclosure url="%s/api?t=get&amp;id=t%d&amp;apikey=%s" length="%d" type="application/x-bittorrent"/>
				  <newznab:attr name="seeders" value="20"/></item>`, title, i, f.srv.URL, i, f.key, int64(8<<30))
				continue
			}
			fmt.Fprintf(&b, `<item><title>%s</title><guid>n%d</guid><enclosure url="%s/api?t=get&amp;id=n%d&amp;apikey=%s" length="%d" type="application/x-nzb"/></item>`,
				title, i, f.srv.URL, i, f.key, int64(8<<30))
		}
		b.WriteString(`</channel></rss>`)
		_, _ = w.Write([]byte(b.String()))
	}
}

func (f *fakeSource) count(t string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, q := range f.queries {
		if q == t {
			n++
		}
	}
	return n
}

func addSource(t *testing.T, svc *Service, name string, f *fakeSource, patch func(*SourceInput)) SourceWriteResult {
	t.Helper()
	in := SourceInput{Name: name, Protocol: f.protocol, BaseURL: f.srv.URL, APIKey: SecretInput{Present: true, Value: f.key}}
	if patch != nil {
		patch(&in)
	}
	res, err := svc.CreateSource(context.Background(), in, "admin")
	if err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
	return res
}

func TestSourcesAreStoredSealedAndReadTheirCaps(t *testing.T) {
	svc := e2eService(t, &fakeGateway{}, testBox(t))
	ctx := context.Background()
	src := newFakeSource(t, "usenet", "SECRETKEY")

	res := addSource(t, svc, "example", src, nil)
	if res.CapsError != "" || res.Caps == nil || res.Caps.Server.Title != "Example" || res.CapsAt == nil {
		t.Fatalf("caps not read on save: %+v", res)
	}
	if !res.APIKey.Set || res.Usage.Queries != 1 || res.Health.State != "ok" {
		t.Fatalf("created: %+v", res.SourceView)
	}
	list, err := svc.ListSources(ctx)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(list)
	if strings.Contains(string(body), "SECRETKEY") {
		t.Fatalf("the key is readable through the API: %s", body)
	}
	ix, _ := svc.st.GetIndexer(ctx, res.ID)
	if strings.Contains(string(ix.APIKeyCT), "SECRETKEY") || ix.APIKeyKID != svc.box.KID() {
		t.Fatal("the key is stored in the clear or under the wrong key")
	}

	// Stale revisions are refused; an edit that changes how the source is
	// reached clears its failure state and reads caps again.
	if err := svc.st.IndexerFailed(ctx, res.ID, "old failure", true, time.Minute, time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateSource(ctx, res.ID, SourceInput{Name: "example", BaseURL: src.srv.URL + "/"}, res.Revision+5, "admin"); !errors.Is(err, store.ErrStale) {
		t.Fatalf("stale write: %v", err)
	}
	upd, err := svc.UpdateSource(ctx, res.ID, SourceInput{Name: "example", BaseURL: src.srv.URL + "/"}, res.Revision, "admin")
	if err != nil || upd.Health.Failures != 0 || upd.Health.LastError != "" || !upd.APIKey.Set || src.count("caps") != 2 {
		t.Fatalf("update: %+v %v (caps asked %d times)", upd, err, src.count("caps"))
	}

	// Without a key, a key cannot be stored; a public source still can be.
	noKey := e2eService(t, &fakeGateway{}, nil)
	if _, err := noKey.CreateSource(ctx, SourceInput{Name: "keyed", Protocol: "usenet", BaseURL: src.srv.URL,
		APIKey: SecretInput{Present: true, Value: "SECRETKEY"}}, "admin"); !errors.Is(err, ErrNoKey) {
		t.Fatalf("key without ACQUIRE_CONFIG_KEY: %v", err)
	}
	public := newFakeSource(t, "torrent", "")
	if _, err := noKey.CreateSource(ctx, SourceInput{Name: "public", Protocol: "torrent", BaseURL: public.srv.URL}, "admin"); err != nil {
		t.Fatalf("public source refused without a key: %v", err)
	}
}

func TestManualSearchKeepsKeysAndReportsWhatDidNotAnswer(t *testing.T) {
	svc := e2eService(t, &fakeGateway{}, testBox(t))
	svc.timing = searchTiming{manualDeadline: 3 * time.Second, manualPerSource: 400 * time.Millisecond, typedPerSource: time.Second}
	ctx := context.Background()

	good := newFakeSource(t, "usenet", "GOODKEY", "Example.Movie.2020.1080p.BluRay.x265-G")
	slow := newFakeSource(t, "torrent", "SLOWKEY", "Example.Movie.2020.2160p.WEB-DL.x265-S")
	goodRes := addSource(t, svc, "good", good, nil)
	slowRes := addSource(t, svc, "slow", slow, nil)
	slow.slowBy(2 * time.Second)

	started := time.Now()
	res, err := svc.Search(ctx, "Example Movie", nil)
	if err != nil {
		t.Fatal(err)
	}
	if took := time.Since(started); took > 2*time.Second {
		t.Fatalf("the slow source held the search for %v", took)
	}
	if len(res.Incomplete) != 1 || res.Incomplete[0] != "slow" {
		t.Fatalf("incomplete = %v, want [slow]", res.Incomplete)
	}
	if len(res.Candidates) != 1 || res.Candidates[0].Indexer != "good" || res.Candidates[0].IndexerID != goodRes.ID || res.Candidates[0].Release == "" {
		t.Fatalf("candidates = %+v", res.Candidates)
	}
	body, _ := json.Marshal(res)
	if strings.Contains(string(body), "GOODKEY") || strings.Contains(string(body), "SLOWKEY") {
		t.Fatalf("a source key reached the search result: %s", body)
	}

	// A per-source timeout is the source's failure: it backs off and the next
	// search does not ask it, but still names it.
	got, _ := svc.st.GetIndexer(ctx, slowRes.ID)
	if got.BackoffUntil == nil || !strings.Contains(got.LastError, "in time") {
		t.Fatalf("slow source not backed off: %+v", got)
	}
	before := slow.count("search")
	res, _ = svc.Search(ctx, "Example Movie", nil)
	if slow.count("search") != before || len(res.Incomplete) != 1 {
		t.Fatalf("a backed-off source was asked (%d→%d) or not named: %v", before, slow.count("search"), res.Incomplete)
	}

	// Scoping to one source asks only that one.
	res, _ = svc.Search(ctx, "Example Movie", []int64{goodRes.ID})
	if len(res.Incomplete) != 0 || len(res.Candidates) != 1 {
		t.Fatalf("scoped search: %+v", res)
	}

	// The overall deadline wins over generous per-source timeouts, and running
	// out of it is not held against the source.
	svc.timing = searchTiming{manualDeadline: 300 * time.Millisecond, manualPerSource: 5 * time.Second}
	if err := svc.st.IndexerSucceeded(ctx, slowRes.ID); err != nil {
		t.Fatal(err)
	}
	started = time.Now()
	res, _ = svc.Search(ctx, "Example Movie", nil)
	if took := time.Since(started); took > 1500*time.Millisecond {
		t.Fatalf("the deadline did not end the search: %v", took)
	}
	if len(res.Incomplete) != 1 || res.Incomplete[0] != "slow" {
		t.Fatalf("deadline incomplete = %v", res.Incomplete)
	}
	got, _ = svc.st.GetIndexer(ctx, slowRes.ID)
	if got.BackoffUntil != nil || got.Failures != 0 {
		t.Fatalf("the search's own deadline was recorded against the source: %+v", got)
	}
}

func TestSourceHealthRules(t *testing.T) {
	svc := e2eService(t, &fakeGateway{}, testBox(t))
	ctx := context.Background()
	rejected := newFakeSource(t, "usenet", "K1", "Example.Movie.2020.1080p.BluRay.x265-G")
	down := newFakeSource(t, "usenet", "K2", "Example.Movie.2020.1080p.BluRay.x265-G")
	limited := newFakeSource(t, "usenet", "K3", "Example.Movie.2020.1080p.BluRay.x265-G")
	r := addSource(t, svc, "rejected", rejected, nil)
	d := addSource(t, svc, "down", down, nil)
	// Caps on save spent one query; one more is left today.
	l := addSource(t, svc, "limited", limited, func(in *SourceInput) { in.QueryLimitDay = OptionalInt{Set: true, Value: intp(2)} })
	rejected.set("401")
	down.set("503")

	if _, err := svc.Search(ctx, "Example", nil); err != nil {
		t.Fatal(err)
	}
	gotR, _ := svc.st.GetIndexer(ctx, r.ID)
	if gotR.Enabled || !strings.Contains(gotR.LastError, "rejected the API key") || gotR.Revision != r.Revision+1 {
		t.Fatalf("a rejected key must disable the source: %+v", gotR)
	}
	gotD, _ := svc.st.GetIndexer(ctx, d.ID)
	if !gotD.Enabled || gotD.BackoffUntil == nil || time.Until(*gotD.BackoffUntil).Round(time.Minute) != sourceBackoffBase {
		t.Fatalf("a 503 must back off by the base: %+v", gotD)
	}
	gotL, _ := svc.st.GetIndexer(ctx, l.ID)
	if gotL.QueriesToday != 2 {
		t.Fatalf("limited queries today = %d", gotL.QueriesToday)
	}
	before := limited.count("search")
	res, _ := svc.Search(ctx, "Example", nil)
	if limited.count("search") != before {
		t.Fatal("a source over its daily allowance was asked")
	}
	if strings.Join(res.Incomplete, ",") != "down,limited" {
		t.Fatalf("incomplete = %v", res.Incomplete)
	}

	// Setup and health see it.
	setup := svc.Setup(ctx)
	if setup.Sections[0].Key != "sources" || setup.Sections[0].State != SetupDegraded ||
		!strings.Contains(setup.Sections[0].Summary, "no enabled source can be asked now") {
		t.Fatalf("sources section: %+v", setup.Sections[0])
	}
	// Once the key is fixed, enabling the source again clears its failure.
	rejected.set("")
	en := true
	back, err := svc.UpdateSource(ctx, r.ID, SourceInput{Name: "rejected", BaseURL: rejected.srv.URL, Enabled: &en}, gotR.Revision, "admin")
	if err != nil || !back.Enabled || back.Health.LastError != "" || back.Health.Failures != 0 || back.Health.State != "ok" {
		t.Fatalf("re-enable: %+v %v", back, err)
	}
}

func TestStoredSourceTestCountsAndRecords(t *testing.T) {
	svc := e2eService(t, &fakeGateway{}, testBox(t))
	ctx := context.Background()
	src := newFakeSource(t, "usenet", "SECRETKEY", "Example.Movie.2020.1080p.BluRay.x265-G")
	res := addSource(t, svc, "example", src, nil)

	out, err := svc.TestStoredSource(ctx, res.ID)
	if err != nil || !out.OK || out.Items != 1 || out.Caps == nil {
		t.Fatalf("stored test: %+v %v", out, err)
	}
	ix, _ := svc.st.GetIndexer(ctx, res.ID)
	if ix.QueriesToday != 3 {
		t.Fatalf("usage after save + test = %d, want 3", ix.QueriesToday)
	}

	// An unsaved edit with a wrong key fails without touching the stored source.
	bad, err := svc.TestSource(ctx, SourceInput{ID: res.ID, Protocol: "usenet", BaseURL: src.srv.URL, APIKey: SecretInput{Present: true, Value: "WRONG"}})
	if err != nil || bad.OK || !strings.Contains(bad.Error, "Incorrect user credentials") {
		t.Fatalf("wrong key test: %+v %v", bad, err)
	}
	ix2, _ := svc.st.GetIndexer(ctx, res.ID)
	if !ix2.Enabled || ix2.QueriesToday != ix.QueriesToday {
		t.Fatalf("an unsaved test changed the stored source: %+v", ix2)
	}
	// Naming the stored source without a key uses the stored key.
	good, err := svc.TestSource(ctx, SourceInput{ID: res.ID, Protocol: "usenet", BaseURL: src.srv.URL})
	if err != nil || !good.OK {
		t.Fatalf("stored-key test: %+v %v", good, err)
	}
}

// A grab from a search result: the reference resolves server-side, the NZB is
// fetched with the source's key and handed over as content, the download is
// counted against the source, and a spent allowance refuses without failing the
// request.
func TestGrabFromASearchResult(t *testing.T) {
	gw := &fakeGateway{}
	svc := e2eService(t, gw, testBox(t))
	ctx := context.Background()
	if _, err := svc.CreateClient(ctx, ClientInput{Type: "nzbget", BaseURL: "http://worker:6789", Auth: "none",
		RemotePath: "/downloads", LocalPath: t.TempDir()}, "admin"); err != nil {
		t.Fatal(err)
	}
	src := newFakeSource(t, "usenet", "SECRETKEY", "Example.Movie.2020.1080p.BluRay.x265-G")
	res := addSource(t, svc, "example", src, func(in *SourceInput) { in.GrabLimitDay = OptionalInt{Set: true, Value: intp(1)} })

	found, err := svc.Search(ctx, "Example Movie", nil)
	if err != nil || len(found.Candidates) != 1 {
		t.Fatalf("search: %+v %v", found, err)
	}
	var c Candidate
	b, _ := json.Marshal(found.Candidates[0])
	_ = json.Unmarshal(b, &c) // exactly what a browser would send back

	if err := svc.st.CreateWanted(ctx, store.Wanted{ID: "w_1", Title: "Example Movie", MediaType: "movie"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.GrabCandidate(ctx, "w_1", c); err != nil {
		t.Fatal(err)
	}
	if src.count("get") != 1 || len(gw.adds) != 1 {
		t.Fatalf("fetches %d, adds %d", src.count("get"), len(gw.adds))
	}
	add, _ := json.Marshal(gw.adds[0])
	if strings.Contains(string(add), "SECRETKEY") || !strings.Contains(string(add), "payload_b64") {
		t.Fatalf("add body: %s", add)
	}
	g, err := svc.st.LatestGrab(ctx, "w_1")
	if err != nil || strings.Contains(g.Source, "SECRETKEY") || g.Indexer != "example" {
		t.Fatalf("grab: %+v %v", g, err)
	}
	indexerID, guid := grabProvenance(t, "w_1")
	if indexerID == nil || *indexerID != res.ID || guid != "n0" {
		t.Fatalf("provenance: %v %q", indexerID, guid)
	}
	ix, _ := svc.st.GetIndexer(ctx, res.ID)
	if ix.GrabsToday != 1 {
		t.Fatalf("grabs today = %d", ix.GrabsToday)
	}

	// The allowance is spent: refused before anything is fetched, and the
	// request's state is left alone.
	if err := svc.st.CreateWanted(ctx, store.Wanted{ID: "w_2", Title: "Example Movie", MediaType: "movie"}); err != nil {
		t.Fatal(err)
	}
	err = svc.GrabCandidate(ctx, "w_2", c)
	if !errors.Is(err, errGrabLimit) || src.count("get") != 1 {
		t.Fatalf("over the grab limit: %v (fetches %d)", err, src.count("get"))
	}
	w2, _ := svc.st.GetWanted(ctx, "w_2")
	if w2.Status != "pending" {
		t.Fatalf("a refused grab changed the request: %s", w2.Status)
	}

	// A reference from an earlier process is refused.
	c.Release = (&Service{refs: processKey()}).sealRef(releaseRef{Link: "https://elsewhere.test/x.nzb"})
	if err := svc.GrabCandidate(ctx, "w_2", c); !errors.Is(err, errStaleRelease) {
		t.Fatalf("foreign reference: %v", err)
	}
}

// Auto-grab asks the preferred protocol's sources first and the others only
// when that finds nothing usable.
func TestAutoGrabSearchesThePreferredProtocolFirst(t *testing.T) {
	gw := &fakeGateway{}
	svc := e2eService(t, gw, testBox(t))
	ctx := context.Background()
	for _, c := range []ClientInput{
		{Type: "nzbget", BaseURL: "http://worker:6789", Auth: "none"},
		{Type: "qbittorrent", BaseURL: "http://worker:8080", Auth: "none"},
	} {
		if _, err := svc.CreateClient(ctx, c, "admin"); err != nil {
			t.Fatal(err)
		}
	}
	usenet := newFakeSource(t, "usenet", "K1") // nothing
	torrent := newFakeSource(t, "torrent", "K2", "Example.Movie.2020.1080p.BluRay.x265-T")
	addSource(t, svc, "nzb", usenet, nil)
	addSource(t, svc, "tor", torrent, nil)

	if err := svc.st.CreateWanted(ctx, store.Wanted{ID: "w_1", Title: "Example Movie", Year: 2020, MediaType: "movie"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.AutoGrab(ctx, "w_1"); err != nil {
		t.Fatal(err)
	}
	if usenet.count("search") != 1 || torrent.count("search") != 1 {
		t.Fatalf("searches: usenet %d, torrent %d", usenet.count("search"), torrent.count("search"))
	}
	w, _ := svc.st.GetWanted(ctx, "w_1")
	if w.Status != "downloading" || !strings.Contains(w.Detail, "from tor via qbittorrent") {
		t.Fatalf("request: %s %s", w.Status, w.Detail)
	}

	// With a usable NZB the torrent sources are not asked at all.
	usenet.offer("Example.Movie.2020.1080p.BluRay.x265-G")
	if err := svc.st.CreateWanted(ctx, store.Wanted{ID: "w_2", Title: "Example Movie", Year: 2020, MediaType: "movie"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.AutoGrab(ctx, "w_2"); err != nil {
		t.Fatal(err)
	}
	if torrent.count("search") != 1 {
		t.Fatalf("the torrent source was asked although an NZB was found: %d", torrent.count("search"))
	}
}

func grabProvenance(t *testing.T, wantedID string) (*int64, string) {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), os.Getenv("TEST_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var id *int64
	var guid string
	if err := pool.QueryRow(context.Background(),
		`SELECT indexer_id, release_guid FROM acquire_app_test.grabs WHERE wanted_id=$1`, wantedID).Scan(&id, &guid); err != nil {
		t.Fatal(err)
	}
	return id, guid
}

func TestSetupSourcesSection(t *testing.T) {
	svc := e2eService(t, &fakeGateway{}, testBox(t))
	ctx := context.Background()
	section := func() SetupSection {
		for _, s := range svc.Setup(ctx).Sections {
			if s.Key == "sources" {
				return s
			}
		}
		t.Fatal("no sources section")
		return SetupSection{}
	}
	if s := section(); s.State != SetupNeedsSetup || !strings.Contains(s.Summary, "no search source is configured") {
		t.Fatalf("empty: %+v", s)
	}
	src := newFakeSource(t, "usenet", "K")
	res := addSource(t, svc, "example", src, nil)
	if s := section(); s.State != SetupReady || s.Summary != "1 source enabled: 1 usenet" {
		t.Fatalf("one source: %+v", s)
	}
	if !svc.Coverage(ctx).SourcesKnown {
		t.Fatal("coverage does not know the source")
	}

	// The same database read with a different ACQUIRE_CONFIG_KEY: the key
	// cannot be opened, which the operator has to fix.
	svc.box = testBox(t)
	if s := section(); s.State != SetupDegraded || !strings.Contains(s.Summary, "API key of example cannot be opened") {
		t.Fatalf("locked key: %+v", s)
	}
	f, _ := svc.loadFleet(ctx, nil)
	if len(f.sources) != 0 || f.reasons["example"] != "its API key cannot be opened" {
		t.Fatalf("a source whose key cannot be opened is in the fleet: %+v", f)
	}

	off := false
	if _, err := svc.UpdateSource(ctx, res.ID, SourceInput{Name: "example", BaseURL: src.srv.URL, Enabled: &off}, 0, "admin"); err != nil {
		t.Fatal(err)
	}
	if s := section(); s.State != SetupNeedsSetup || !strings.Contains(s.Summary, "no search source is enabled") {
		t.Fatalf("disabled: %+v", s)
	}
}
