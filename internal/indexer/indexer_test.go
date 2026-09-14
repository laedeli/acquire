package indexer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/laedeli/acquire/internal/release"
)

func ip(v int) *int { return &v }

func testSource(url string) Source {
	return Source{
		ID: 7, Name: "example", Protocol: "usenet", BaseURL: url, APIPath: "/api", APIKey: "SECRETKEY",
		Categories: Categories{Movie: []int{2000, 2040}, TV: []int{5000}},
	}
}

func testClient(srv *httptest.Server) *Client { return &Client{HTTP: srv.Client()} }

// Typed parameters must reach the wire, at the source's own path, with its own
// key and its own categories.
func TestTypedParametersReachTheWire(t *testing.T) {
	var path, query string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, query = r.URL.Path, r.URL.RawQuery
		_, _ = w.Write([]byte(`<rss><channel></channel></rss>`))
	}))
	defer srv.Close()
	src := testSource(srv.URL + "/indexers/example/")
	src.APIPath = "/torznab/api"
	_, err := testClient(srv).Search(context.Background(), src, Query{
		Kind: "tvsearch", TVDBID: 81189, Season: ip(2), Episode: ip(5), Media: "tv"})
	if err != nil {
		t.Fatal(err)
	}
	if path != "/indexers/example/torznab/api" {
		t.Errorf("path = %q", path)
	}
	for _, want := range []string{"t=tvsearch", "tvdbid=81189", "season=2", "ep=5", "cat=5000", "apikey=SECRETKEY"} {
		if !strings.Contains(query, want) {
			t.Errorf("query %q is missing %q", query, want)
		}
	}
}

func TestCategoriesAndLimits(t *testing.T) {
	c := Categories{Movie: []int{2000, 2040}, TV: []int{5000, 2040}}
	if got := fmt.Sprint(c.For("movie")); got != "[2000 2040]" {
		t.Errorf("movie = %s", got)
	}
	if got := fmt.Sprint(c.For("")); got != "[2000 2040 5000]" {
		t.Errorf("both = %s", got)
	}
	v := Query{Kind: "movie", IMDBID: "tt0133093"}.Values("k", []int{2000, 2040})
	if v.Get("imdbid") != "0133093" || v.Get("cat") != "2000,2040" || v.Get("limit") != "100" {
		t.Errorf("values = %v", v)
	}
	if (Query{}).Values("", nil).Has("apikey") {
		t.Error("a source without a key must not send an empty apikey")
	}

	var limit string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		limit = r.URL.Query().Get("limit")
		_, _ = w.Write([]byte(`<rss><channel></channel></rss>`))
	}))
	defer srv.Close()
	src := testSource(srv.URL)
	src.Caps = &Caps{Limits: CapsLimits{Max: 50}}
	_, _ = testClient(srv).Search(context.Background(), src, Query{Kind: "search", Term: "x"})
	if limit != "50" {
		t.Errorf("limit = %s, want the source's maximum page size", limit)
	}
}

func TestTypedDetection(t *testing.T) {
	if !(Query{TVDBID: 1}).Typed() || !(Query{IMDBID: "tt1"}).Typed() || !(Query{Season: ip(1)}).Typed() {
		t.Error("a query carrying an id or season should be typed")
	}
	if (Query{Term: "Breaking Bad"}).Typed() {
		t.Error("a free-text query is not typed")
	}
}

const newznabFeed = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:atom="http://www.w3.org/2005/Atom" xmlns:newznab="http://www.newznab.com/DTD/2010/feeds/attributes/">
<channel>
  <item>
    <title>Example.Show.S02E05.1080p.WEB-DL.x265-G</title>
    <guid isPermaLink="true">https://source.test/details/abc123</guid>
    <link>https://source.test/api?t=get&amp;id=abc123&amp;apikey=SECRETKEY</link>
    <pubDate>Mon, 14 Sep 2026 10:00:00 +0000</pubDate>
    <enclosure url="https://source.test/api?t=get&amp;id=abc123&amp;apikey=SECRETKEY" length="1073741824" type="application/x-nzb"/>
    <newznab:attr name="category" value="5040"/>
    <newznab:attr name="size" value="1073741824"/>
  </item>
  <item>
    <title>Example.Show.S02E05.720p.HDTV.x264-H</title>
    <guid>def456</guid>
    <enclosure url="https://source.test/getnzb/def456.nzb" length="0" type="application/x-nzb"/>
    <newznab:attr name="size" value="524288000"/>
  </item>
  <item>
    <title>Wrong.Protocol.S02E05.1080p-X</title>
    <enclosure url="https://source.test/t/1.torrent" length="10" type="application/x-bittorrent"/>
  </item>
  <item><title></title><enclosure url="https://source.test/x" type="application/x-nzb"/></item>
</channel></rss>`

const torznabFeed = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:torznab="http://torznab.com/schemas/2015/feed">
<channel>
  <item>
    <title>Example.Movie.2020.2160p.BluRay.x265-T</title>
    <guid>https://tracker.test/t/9</guid>
    <link>https://proxy.test/dl/9?apikey=SECRETKEY</link>
    <size>7340032000</size>
    <enclosure url="https://proxy.test/dl/9?apikey=SECRETKEY" length="0" type="application/x-bittorrent"/>
    <torznab:attr name="seeders" value="42"/>
    <torznab:attr name="peers" value="50"/>
    <torznab:attr name="infohash" value="ABCDEF0123456789ABCDEF0123456789ABCDEF01"/>
    <torznab:attr name="magneturl" value="magnet:?xt=urn:btih:abcdef0123456789abcdef0123456789abcdef01"/>
  </item>
  <item>
    <title>Example.Movie.2020.1080p.WEB-DL-T</title>
    <link>magnet:?xt=urn:btih:0123</link>
    <torznab:attr name="seeders" value="3"/>
  </item>
  <item>
    <title>An.NZB.On.A.Torrent.Feed</title>
    <enclosure url="https://proxy.test/x.nzb" type="application/x-nzb"/>
  </item>
</channel></rss>`

func TestParsesNewznabFeed(t *testing.T) {
	src := testSource("https://source.test")
	items, err := parseFeed([]byte(newznabFeed), src)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("items = %d, want 2 (a torrent enclosure and an untitled item are dropped): %+v", len(items), items)
	}
	a := items[0]
	if a.Protocol != "usenet" || a.GUID != "https://source.test/details/abc123" || a.Size != 1073741824 ||
		a.Indexer != "example" || a.IndexerID != 7 || !strings.Contains(a.Link, "t=get") || a.PubDate.IsZero() {
		t.Errorf("first item mis-parsed: %+v", a)
	}
	if items[1].Size != 524288000 || items[1].GUID != "def456" {
		t.Errorf("size must fall back to the size attr: %+v", items[1])
	}
}

func TestParsesTorznabFeed(t *testing.T) {
	src := testSource("https://proxy.test")
	src.Protocol = "torrent"
	items, err := parseFeed([]byte(torznabFeed), src)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("items = %d, want 2 (the NZB enclosure is dropped)", len(items))
	}
	m := items[0]
	if m.Seeders != 42 || m.Peers != 50 || m.InfoHash != "abcdef0123456789abcdef0123456789abcdef01" ||
		!strings.HasPrefix(m.Magnet, "magnet:") || m.Size != 7340032000 || m.GUID != "https://tracker.test/t/9" {
		t.Errorf("torznab attrs mis-parsed: %+v", m)
	}
	// A magnet in <link> is a magnet, not a download link.
	if items[1].Magnet != "magnet:?xt=urn:btih:0123" || items[1].Link != "" {
		t.Errorf("magnet link: %+v", items[1])
	}
	if items[0].Source() != items[0].Magnet {
		t.Errorf("a torrent with a magnet starts from it: %s", items[0].Source())
	}
}

func TestParsesLatin1Feeds(t *testing.T) {
	body := []byte("<?xml version=\"1.0\" encoding=\"ISO-8859-1\"?><rss><channel><item><title>Caf\xe9.2020.1080p</title>" +
		"<enclosure url=\"https://source.test/1.nzb\" type=\"application/x-nzb\"/></item></channel></rss>")
	items, err := parseFeed(body, testSource("https://source.test"))
	if err != nil || len(items) != 1 || items[0].Title != "Café.2020.1080p" {
		t.Fatalf("latin-1: %+v %v", items, err)
	}
}

const capsDoc = `<?xml version="1.0" encoding="UTF-8"?>
<caps>
  <server version="1.3" title="Example Source"/>
  <limits max="200" default="75"/>
  <searching>
    <search available="yes" supportedParams="q"/>
    <tv-search available="yes" supportedParams="q, rid,tvdbid,season,ep"/>
    <movie-search available="yes" supportedParams="q,imdbid"/>
    <audio-search available="no"/>
  </searching>
  <categories>
    <category id="2000" name="Movies">
      <subcat id="2040" name="Movies/HD"/>
      <subcat id="2045" name="Movies/UHD"/>
    </category>
    <category id="5000" name="TV"><subcat id="5040" name="TV/HD"/></category>
    <category id="nope" name="broken"/>
  </categories>
</caps>`

func TestParseCaps(t *testing.T) {
	c, err := ParseCaps([]byte(capsDoc))
	if err != nil {
		t.Fatal(err)
	}
	if c.Server.Title != "Example Source" || c.Limits.Max != 200 || c.Limits.Default != 75 {
		t.Errorf("server/limits: %+v", c)
	}
	if !c.AcceptsTVDBID() || !c.AcceptsSeasonEp() || !c.AcceptsMovieIMDBID() {
		t.Errorf("modes: %+v", c)
	}
	if len(c.Categories) != 2 || c.Categories[0].ID != 2000 || len(c.Categories[0].Subcats) != 2 || c.Categories[1].Subcats[0].ID != 5040 {
		t.Errorf("categories: %+v", c.Categories)
	}
	// It round-trips through the JSON the store keeps.
	b, _ := json.Marshal(c)
	var back Caps
	if err := json.Unmarshal(b, &back); err != nil || !back.AcceptsTVDBID() {
		t.Fatalf("json round trip: %s %v", b, err)
	}

	half, _ := ParseCaps([]byte(`<caps><searching><tv-search available="yes" supportedParams="q,season"/></searching></caps>`))
	if half.AcceptsSeasonEp() {
		t.Error("season without ep should not count as coordinate-capable")
	}
	off, _ := ParseCaps([]byte(`<caps><searching><tv-search available="no" supportedParams="q,tvdbid"/></searching></caps>`))
	if off.AcceptsTVDBID() {
		t.Error("an unavailable mode accepts nothing")
	}
	var none *Caps
	if none.AcceptsTVDBID() || none.AcceptsSeasonEp() || none.AcceptsMovieIMDBID() {
		t.Error("unknown caps accept nothing")
	}
	if _, err := ParseCaps([]byte(`<rss/>`)); err == nil {
		t.Error("an RSS document is not caps")
	}
}

// Every way a source can fail maps to what it means for the source's health —
// and no error ever carries the key.
func TestErrorsAreClassifiedAndCarryNoKey(t *testing.T) {
	cases := []struct {
		name string
		h    http.HandlerFunc
		kind Kind
		msg  string
	}{
		{"401", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401) }, KindCredentials, "rejected the API key"},
		{"403", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(403) }, KindCredentials, "HTTP 403"},
		{"429", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(429) }, KindRateLimited, "HTTP 429"},
		{"503", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }, KindUnavailable, "HTTP 503"},
		{"404", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(404) }, KindOther, "HTTP 404"},
		{"credentials in a 200", func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`<?xml version="1.0"?><error code="100" description="Incorrect user credentials: SECRETKEY"/>`))
		}, KindCredentials, "Incorrect user credentials: … (code 100)"},
		{"request limit", func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`<error code="500" description="Request limit reached"/>`))
		}, KindRateLimited, "Request limit reached"},
		{"error document with a status", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`<error code="201" description="Incorrect parameter"/>`))
		}, KindOther, "Incorrect parameter (code 201)"},
		{"not a feed", func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`<html>maintenance</html>`))
		}, KindOther, "not a newznab or torznab feed"},
	}
	for _, c := range cases {
		srv := httptest.NewServer(c.h)
		_, err := testClient(srv).Search(context.Background(), testSource(srv.URL), Query{Kind: "search", Term: "x"})
		srv.Close()
		if err == nil {
			t.Errorf("%s: no error", c.name)
			continue
		}
		if KindOf(err) != c.kind || !strings.Contains(err.Error(), c.msg) {
			t.Errorf("%s: kind %d %q, want kind %d containing %q", c.name, KindOf(err), err, c.kind, c.msg)
		}
		if strings.Contains(err.Error(), "SECRETKEY") {
			t.Errorf("%s: the key leaked into the error: %q", c.name, err)
		}
	}

	// A connection failure keeps the cause, not the URL.
	_, err := (&Client{}).Search(context.Background(), testSource("http://127.0.0.1:1"), Query{Kind: "search", Term: "x"})
	if err == nil || KindOf(err) != KindUnavailable || strings.Contains(err.Error(), "SECRETKEY") || strings.Contains(err.Error(), "apikey") {
		t.Errorf("transport error: kind %d %q", KindOf(err), err)
	}
	var e *Error
	if !errors.As(err, &e) {
		t.Errorf("transport errors are *Error: %T", err)
	}

	if s := fmt.Sprintf("%v %+v %#v %s", testSource("x"), testSource("x"), testSource("x"), testSource("x")); strings.Contains(s, "SECRETKEY") {
		t.Errorf("formatting a Source printed its key: %s", s)
	}
}

func TestFetchCaps(t *testing.T) {
	var query string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		_, _ = w.Write([]byte(capsDoc))
	}))
	defer srv.Close()
	c, err := testClient(srv).FetchCaps(context.Background(), testSource(srv.URL))
	if err != nil || c.Limits.Max != 200 {
		t.Fatalf("caps: %+v %v", c, err)
	}
	if !strings.Contains(query, "t=caps") || !strings.Contains(query, "apikey=SECRETKEY") {
		t.Errorf("caps query: %s", query)
	}
}

// ── the escalation engine ───────────────────────────────────────────────────

// The identity check the ranker has no term for. This exact release was the TOP
// free-text hit for "Breaking Bad" in a live probe.
func TestRejectsTheWrongShow(t *testing.T) {
	m := Verify("Breaking Bad", nil, "The.Bad.Guys.Breaking.In.S02E05.1080p-X", ip(2), ip(5), false)
	if m.OK {
		t.Errorf("accepted a different show: %+v", m)
	}
}

func TestAcceptsViaAlias(t *testing.T) {
	m := Verify("Breaking Bad", []string{"Totalna Melina"},
		"Totalna.Melina.S02E05.1080p.WEB-DL.x265-G", ip(2), ip(5), false)
	if !m.OK || m.Via != "alias" {
		t.Errorf("alias match failed: %+v", m)
	}
}

// An id-resolved search settles the series, but still returns a whole season —
// so the coordinates are the likeliest error and must still be checked.
func TestIdResolvedStillChecksCoordinates(t *testing.T) {
	ok := Verify("Breaking Bad", nil, "Breaking.Bad.S02E05.1080p-G", ip(2), ip(5), true)
	if !ok.OK || ok.Via != "id" {
		t.Errorf("id-resolved correct episode rejected: %+v", ok)
	}
	bad := Verify("Breaking Bad", nil, "Breaking.Bad.S03E03.1080p-G", ip(2), ip(5), true)
	if bad.OK {
		t.Errorf("id-resolved WRONG episode accepted: %+v", bad)
	}
}

// A release with no coordinates at all cannot satisfy an episode target.
func TestReleaseWithoutCoordinatesIsRejectedForAnEpisode(t *testing.T) {
	m := Verify("Breaking Bad", nil, "Breaking.Bad.Complete.Series.1080p-G", ip(2), ip(5), true)
	if m.OK {
		t.Errorf("a release with no episode number satisfied an episode target: %+v", m)
	}
}

var (
	idCaps     = &Caps{TVSearch: SearchMode{Available: true, Params: []string{"q", "tvdbid", "season", "ep"}}}
	coordsCaps = &Caps{TVSearch: SearchMode{Available: true, Params: []string{"q", "season", "ep"}}}
)

func fleet() []Source {
	return []Source{
		{ID: 1, Name: "idCapable", Caps: idCaps},
		{ID: 2, Name: "coordsOnly", Caps: coordsCaps},
		{ID: 3, Name: "textOnly"},
	}
}

// recorder is an Ask that answers from a function and records what was asked.
type recorder struct {
	mu     sync.Mutex
	asked  []string
	answer func(src Source, q Query) ([]release.Release, error)
}

func (r *recorder) ask(_ context.Context, src Source, q Query) ([]release.Release, error) {
	r.mu.Lock()
	r.asked = append(r.asked, fmt.Sprintf("%s:%s", src.Name, q.Kind))
	r.mu.Unlock()
	return r.answer(src, q)
}

func rel(title string) []release.Release {
	return []release.Release{{Title: title, Protocol: "usenet"}}
}

// The escalation must go precise -> broad, and must STOP as soon as a stage
// produces verified results. Escalating past a good answer only spends quota.
func TestEscalationStopsAtTheFirstUsefulStage(t *testing.T) {
	r := &recorder{answer: func(src Source, q Query) ([]release.Release, error) {
		if q.TVDBID != 0 {
			return rel("Breaking.Bad.S02E05.1080p.WEB-DL.x265-G"), nil
		}
		return nil, nil
	}}
	e := &Engine{Ask: r.ask}
	got, err := e.Search(context.Background(), Target{
		Title: "Breaking Bad", TVDBID: 81189, Season: ip(2), Episode: ip(5), Kind: "series",
	}, fleet())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Stage != "id" {
		t.Fatalf("results = %+v, want one id-stage hit", got)
	}
	if len(r.asked) != 1 || r.asked[0] != "idCapable:tvsearch" {
		t.Errorf("escalated past a successful stage: %v", r.asked)
	}
}

// An empty typed stage is inconclusive, so the engine must fall back rather
// than concluding the release does not exist.
func TestEmptyTypedStageFallsBack(t *testing.T) {
	r := &recorder{answer: func(src Source, q Query) ([]release.Release, error) {
		if q.TVDBID == 0 && q.Season != nil {
			return rel("Breaking.Bad.S02E05.720p.WEB-DL.x264-H"), nil
		}
		return nil, nil // the id stage is a silent zero
	}}
	e := &Engine{Ask: r.ask}
	got, _ := e.Search(context.Background(), Target{
		Title: "Breaking Bad", TVDBID: 81189, Season: ip(2), Episode: ip(5), Kind: "series"}, fleet())
	if len(got) == 0 {
		t.Fatal("a silent-zero id stage was treated as proof of absence")
	}
	if got[0].Stage != "coords" {
		t.Errorf("fell through to %q, want coords", got[0].Stage)
	}
}

// The free-text stage is where the wrong show arrives. It must be filtered out
// rather than handed to a ranker that has no title term.
func TestFreeTextStageRejectsTheWrongShow(t *testing.T) {
	r := &recorder{answer: func(Source, Query) ([]release.Release, error) {
		return []release.Release{
			{Title: "The.Bad.Guys.Breaking.In.S02E05.1080p-X"},
			{Title: "Breaking.Bad.S02E05.1080p.WEB-DL.x265-G"},
		}, nil
	}}
	e := &Engine{Ask: r.ask}
	got, _ := e.Search(context.Background(), Target{
		Title: "Breaking Bad", Season: ip(2), Episode: ip(5), Kind: "series"},
		[]Source{{ID: 3, Name: "textOnly"}})
	if len(got) != 1 || got[0].Title != "Breaking.Bad.S02E05.1080p.WEB-DL.x265-G" {
		t.Fatalf("results = %+v, want only the real show", got)
	}
}

// One source failing must not fail the search — a fleet where any member can
// be down or banned is the normal case.
func TestOneFailingSourceDoesNotFailTheSearch(t *testing.T) {
	r := &recorder{answer: func(src Source, q Query) ([]release.Release, error) {
		if src.Name == "broken" {
			return nil, &Error{Kind: KindUnavailable, Msg: "HTTP 500"}
		}
		return rel("Breaking.Bad.S02E05.1080p-G"), nil
	}}
	e := &Engine{Ask: r.ask}
	got, err := e.Search(context.Background(), Target{
		Title: "Breaking Bad", Season: ip(2), Episode: ip(5), Kind: "series"},
		[]Source{{ID: 2, Name: "broken", Caps: coordsCaps}, {ID: 3, Name: "ok", Caps: coordsCaps}})
	if err != nil {
		t.Fatalf("a single source failure failed the whole search: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("results = %d, want the one healthy source's hit", len(got))
	}
}

// A movie target asks movie-capable sources by imdbid, scoped to the movie
// categories.
func TestMovieTargetUsesMovieSearch(t *testing.T) {
	var queries []Query
	var mu sync.Mutex
	e := &Engine{Ask: func(_ context.Context, src Source, q Query) ([]release.Release, error) {
		mu.Lock()
		queries = append(queries, q)
		mu.Unlock()
		return nil, nil
	}}
	movieCaps := &Caps{MovieSearch: SearchMode{Available: true, Params: []string{"q", "imdbid"}}}
	_, _ = e.Search(context.Background(), Target{Title: "The Matrix", IMDBID: "tt0133093", Kind: "movie"},
		[]Source{{ID: 1, Name: "i", Caps: movieCaps}, {ID: 2, Name: "tv only", Caps: idCaps}})
	if len(queries) != 3 {
		t.Fatalf("queries = %+v, want the id stage on one source and text on both", queries)
	}
	if queries[0].Kind != "movie" || queries[0].IMDBID != "tt0133093" {
		t.Errorf("id stage: %+v", queries[0])
	}
	for _, q := range queries {
		if q.Media != "movie" {
			t.Errorf("movie search scoped to %q", q.Media)
		}
	}
}
