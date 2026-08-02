package indexer

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func ip(v int) *int { return &v }

// The whole reason this package exists: the aggregator's own search endpoint
// accepts typed params, returns 200, and discards them. These must reach the
// wire.
func TestTypedParametersReachTheWire(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.RawQuery
		_, _ = w.Write([]byte(`<rss><channel></channel></rss>`))
	}))
	defer srv.Close()
	c := New(srv.URL, "k")
	c.HTTP = srv.Client()
	_, err := c.Search(context.Background(), 32, Query{
		Kind: "tvsearch", TVDBID: 81189, Season: ip(2), Episode: ip(5), Cat: CatTV})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"t=tvsearch", "tvdbid=81189", "season=2", "ep=5", "cat=5000"} {
		if !contains(got, want) {
			t.Errorf("query %q is missing %q", got, want)
		}
	}
}

func contains(s, sub string) bool { return len(s) >= len(sub) && (indexOf(s, sub) >= 0) }
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func TestParsesNewznabItems(t *testing.T) {
	const body = `<rss><channel>
	  <item><title>Breaking.Bad.S02E05.1080p.BluRay.x265-G</title>
	    <link>http://x/dl?id=1</link>
	    <enclosure url="http://x/dl?id=1" length="7340032000"/>
	    <attr name="seeders" value="42"/><attr name="magneturl" value="magnet:?xt=urn:btih:abc"/>
	  </item>
	  <item><title>Breaking.Bad.S02E05.720p.WEB-DL.x264-H</title>
	    <enclosure url="http://x/dl?id=2" length="1073741824"/>
	    <attr name="size" value="1073741824"/>
	  </item>
	</channel></rss>`
	items, err := parseNewznab([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("items = %d", len(items))
	}
	if items[0].Protocol != "torrent" || items[0].Seeders != 42 || items[0].Magnet == "" {
		t.Errorf("torrent item mis-parsed: %+v", items[0])
	}
	if items[1].Protocol != "usenet" || items[1].Size != 1073741824 {
		t.Errorf("usenet item mis-parsed: %+v", items[1])
	}
}

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

// Typed() drives the fallback decision: a zero-result typed query is
// inconclusive, not a negative answer.
func TestTypedDetection(t *testing.T) {
	if !(Query{TVDBID: 1}).Typed() || !(Query{IMDBID: "tt1"}).Typed() || !(Query{Season: ip(1)}).Typed() {
		t.Error("a query carrying an id or season should be typed")
	}
	if (Query{Term: "Breaking Bad"}).Typed() {
		t.Error("a free-text query is not typed")
	}
}

func TestImdbPrefixStrippedOnTheWire(t *testing.T) {
	v := Query{Kind: "movie", IMDBID: "tt0133093"}.Values("k")
	if v.Get("imdbid") != "0133093" {
		t.Errorf("imdbid = %q, want the tt prefix stripped", v.Get("imdbid"))
	}
}

func fleet() []Capability {
	return []Capability{
		{ID: 1, Name: "idCapable", AcceptsTVDBID: true, AcceptsSeasonEp: true},
		{ID: 2, Name: "coordsOnly", AcceptsSeasonEp: true},
		{ID: 3, Name: "textOnly"},
	}
}

// The escalation must go precise -> broad, and must STOP as soon as a stage
// produces verified results. Escalating past a good answer only spends quota.
func TestEscalationStopsAtTheFirstUsefulStage(t *testing.T) {
	var hits []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		hits = append(hits, r.URL.Path+"?"+q.Get("t")+"|tvdbid="+q.Get("tvdbid"))
		if q.Get("tvdbid") != "" {
			_, _ = w.Write([]byte(`<rss><channel><item>
			  <title>Breaking.Bad.S02E05.1080p.WEB-DL.x265-G</title>
			  <enclosure url="http://x/1" length="100"/></item></channel></rss>`))
			return
		}
		_, _ = w.Write([]byte(`<rss><channel></channel></rss>`))
	}))
	defer srv.Close()
	c := New(srv.URL, "k")
	c.HTTP = srv.Client()
	e := &Engine{Client: c, MaxConcurrent: 2}

	got, err := e.Search(context.Background(), Target{
		Title: "Breaking Bad", TVDBID: 81189, Season: ip(2), Episode: ip(5), Kind: "series",
	}, fleet())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Stage != "id" {
		t.Fatalf("results = %+v, want one id-stage hit", got)
	}
	// Only the id-capable indexer should have been asked.
	if len(hits) != 1 {
		t.Errorf("escalated past a successful stage: %v", hits)
	}
}

// An empty typed stage is inconclusive, so the engine must fall back rather
// than concluding the release does not exist.
func TestEmptyTypedStageFallsBack(t *testing.T) {
	var stages []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		switch {
		case q.Get("tvdbid") != "":
			stages = append(stages, "id")
			_, _ = w.Write([]byte(`<rss><channel></channel></rss>`)) // silent zero
		case q.Get("season") != "":
			stages = append(stages, "coords")
			_, _ = w.Write([]byte(`<rss><channel><item>
			  <title>Breaking.Bad.S02E05.720p.WEB-DL.x264-H</title>
			  <enclosure url="http://x/2" length="100"/></item></channel></rss>`))
		default:
			stages = append(stages, "text")
			_, _ = w.Write([]byte(`<rss><channel></channel></rss>`))
		}
	}))
	defer srv.Close()
	c := New(srv.URL, "k")
	c.HTTP = srv.Client()
	e := &Engine{Client: c, MaxConcurrent: 2}
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
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<rss><channel>
		  <item><title>The.Bad.Guys.Breaking.In.S02E05.1080p-X</title>
		    <enclosure url="http://x/3" length="100"/></item>
		  <item><title>Breaking.Bad.S02E05.1080p.WEB-DL.x265-G</title>
		    <enclosure url="http://x/4" length="100"/></item>
		</channel></rss>`))
	}))
	defer srv.Close()
	c := New(srv.URL, "k")
	c.HTTP = srv.Client()
	e := &Engine{Client: c, MaxConcurrent: 1}
	got, _ := e.Search(context.Background(), Target{
		Title: "Breaking Bad", Season: ip(2), Episode: ip(5), Kind: "series"},
		[]Capability{{ID: 3, Name: "textOnly"}})
	if len(got) != 1 {
		t.Fatalf("results = %d, want only the real show", len(got))
	}
	if got[0].Title != "Breaking.Bad.S02E05.1080p.WEB-DL.x265-G" {
		t.Errorf("kept the wrong release: %s", got[0].Title)
	}
}

// One indexer failing must not fail the search — a fleet where any member can
// be down or banned is the normal case.
func TestOneFailingIndexerDoesNotFailTheSearch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/indexer/2/newznab" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(`<rss><channel><item>
		  <title>Breaking.Bad.S02E05.1080p-G</title>
		  <enclosure url="http://x/5" length="100"/></item></channel></rss>`))
	}))
	defer srv.Close()
	c := New(srv.URL, "k")
	c.HTTP = srv.Client()
	e := &Engine{Client: c, MaxConcurrent: 3}
	got, err := e.Search(context.Background(), Target{
		Title: "Breaking Bad", Season: ip(2), Episode: ip(5), Kind: "series"},
		[]Capability{{ID: 2, Name: "broken", AcceptsSeasonEp: true}, {ID: 3, Name: "ok", AcceptsSeasonEp: true}})
	if err != nil {
		t.Fatalf("a single indexer failure failed the whole search: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("results = %d, want the one healthy indexer's hit", len(got))
	}
}

// A movie target must be scoped to movie categories: 12 enabled indexers are
// adult-only and an unscoped family-movie search fans out to them.
func TestMovieTargetIsCategoryScoped(t *testing.T) {
	var cats []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cats = append(cats, r.URL.Query().Get("cat"))
		_, _ = w.Write([]byte(`<rss><channel></channel></rss>`))
	}))
	defer srv.Close()
	c := New(srv.URL, "k")
	c.HTTP = srv.Client()
	e := &Engine{Client: c, MaxConcurrent: 1}
	_, _ = e.Search(context.Background(), Target{Title: "The Matrix", IMDBID: "tt0133093", Kind: "movie"},
		[]Capability{{ID: 1, Name: "i", AcceptsIMDBID: true}})
	for _, c := range cats {
		if c != CatMovie {
			t.Errorf("movie search used cat=%q, want %q", c, CatMovie)
		}
	}
}
