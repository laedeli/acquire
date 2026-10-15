package katalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func i32(v int32) *int32 { return &v }

// An episode goes to the catalog by its series' identity, its season and its
// episode, and a file of several episodes with the last one: the body ingest
// by identity takes, and no catalog id.
func TestIngestByIdentityBody(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = w.Write([]byte(`{"itemId":"e7","created":true,"seriesId":"s1","covered":["e8"]}`))
	}))
	defer srv.Close()
	c := New(srv.URL, "", nil)
	res, err := c.Ingest(context.Background(), IngestRequest{
		Path: "/arrivals/x/Show.S04E07E08.mkv", Type: "episode", Title: "Show", Year: i32(2019),
		Series: &Identity{TMDB: "97546"}, SeasonNumber: i32(4), EpisodeNumber: i32(7), EpisodeEnd: i32(8),
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.ItemID != "e7" || res.SeriesID != "s1" || len(res.Covered) != 1 || res.Covered[0] != "e8" {
		t.Errorf("answer = %+v", res)
	}
	want := map[string]any{
		"path": "/arrivals/x/Show.S04E07E08.mkv", "type": "episode", "title": "Show", "year": float64(2019),
		"series": map[string]any{"tmdb": "97546"}, "seasonNumber": float64(4), "episodeNumber": float64(7),
		"episodeEnd": float64(8),
	}
	gb, _ := json.Marshal(got)
	wb, _ := json.Marshal(want)
	if string(gb) != string(wb) {
		t.Errorf("body = %s\nwant   %s", gb, wb)
	}
}

// What a refusal says: a 409 names the item that holds the episode already;
// the catalog from before the contract asks for the parent, which is it not
// taking episodes by identity; a path outside the arrivals is a refusal, and
// so is everything the catalog with the contract refuses in the words of the
// new fields.
func TestIngestRefusals(t *testing.T) {
	cases := []struct {
		code                 int
		body                 string
		holder               string
		unsupported, refused bool
	}{
		{409, `{"error":"/arrivals/a.mkv is a copy of the original of item x1 at /arrivals/b.mkv","itemId":"x1"}`, "x1", false, true},
		{409, `{"error":"the library holds S04E07 as item x2","itemId":"x2"}`, "x2", false, true},
		// The catalog from before the contract.
		{400, `{"error":"an episode requires parentId, seasonNumber and episodeNumber; without them it would be ingested as an unlinked orphan"}`, "", true, true},
		{422, `{"error":"unknown field series"}`, "", true, true},
		// The catalog with it, refusing what it cannot read.
		{400, `{"error":"an episode requires its series, by parentId or by its identity (series), and seasonNumber and episodeNumber; without them it would be ingested as an unlinked orphan"}`, "", false, true},
		{400, `{"error":"an episode names its series one way: by its catalog id (parentId) or by its identity (series)"}`, "", false, true},
		{400, `{"error":"series.imdb is tt and digits, not \"0133093\""}`, "", false, true},
		{400, `{"error":"episodeEnd is the last episode the file covers, from episodeNumber on: 1 to 10, as one file covers 10 episodes at most"}`, "", false, true},
		{400, `{"error":"path must be under the arrivals' root (ARRIVALS_ROOT)"}`, "", false, true},
		{503, `busy`, "", false, false},
	}
	for _, c := range cases {
		e := &IngestError{Code: c.code, Body: c.body}
		if e.Holder() != c.holder || e.Unsupported() != c.unsupported || e.Refused() != c.refused {
			t.Errorf("%d %s: holder %q unsupported %v refused %v", c.code, c.body, e.Holder(), e.Unsupported(), e.Refused())
		}
	}
	if m := (&IngestError{Code: 400, Body: `{"error":"no file"}`}).Message(); m != "no file" {
		t.Errorf("message = %q", m)
	}
	if !(&IngestError{Code: 400, Body: `{"error":"identity.imdb is tt and digits, not \"x\""}`}).RefusesIdentity() {
		t.Error("a refused identity of a movie")
	}
	if !(IngestRequest{Identity: &Identity{TMDB: "1"}}).ByIdentity() || (IngestRequest{Type: "movie"}).ByIdentity() {
		t.Error("ByIdentity")
	}
}

// Ids go in their source's form or not at all: one the catalog would refuse
// would cost the whole hand-over.
func TestNewIdentity(t *testing.T) {
	if id := NewIdentity(1396, 81189, "tt0903747"); *id != (Identity{TMDB: "1396", TVDB: "81189", IMDB: "tt0903747"}) {
		t.Errorf("all three: %+v", id)
	}
	if id := NewIdentity(0, 0, "0903747"); id == nil || id.IMDB != "tt0903747" {
		t.Errorf("an IMDb id without its tt: %+v", id)
	}
	if id := NewIdentity(1396, 0, "TT0903747x"); *id != (Identity{TMDB: "1396"}) {
		t.Errorf("an IMDb id in another form is left out: %+v", id)
	}
	if NewIdentity(0, 0, "") != nil {
		t.Error("no identity is nil")
	}
	b, _ := json.Marshal(NewIdentity(1, 2, "tt3"))
	if string(b) != `{"tmdb":"1","tvdb":"2","imdb":"tt3"}` {
		t.Errorf("the strongest id goes first: %s", b)
	}
}

// holdingsServer answers holdings by identity for one series and one movie,
// 404 for anything else, and the paged listing.
func holdingsServer(t *testing.T, onRequest func(*http.Request)) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if onRequest != nil {
			onRequest(r)
		}
		q := r.URL.Query()
		switch {
		case r.URL.Path != "/api/v1/holdings":
			http.NotFound(w, r)
		case q.Get("page") != "":
			_, _ = w.Write([]byte(`{"items":[{"itemId":"s1","title":"Show","year":2019,"externalIds":{"tmdb":97546}}],"page":1,"pageSize":1,"total":1}`))
		case q.Get("type") == "series" && q.Get("tmdb") == "97546":
			_, _ = w.Write([]byte(`{"seriesId":"s1","title":"Show","year":2019,"externalIds":{"tmdb":"97546","tvdb":362753},
			  "seasons":[{"season":4,"episodes":[
			    {"episode":7,"itemId":"e7","hasFile":true,"playable":true},
			    {"episode":8,"itemId":"e8","hasFile":false,"playable":true,"coveredBy":"e7"},
			    {"episode":9,"itemId":"e9","hasFile":false,"playable":false}]}]}`))
		case q.Get("type") == "movie" && q.Get("imdb") == "tt0133093":
			_, _ = w.Write([]byte(`{"itemId":"m1","title":"The Matrix","year":1999,"externalIds":{"imdb":"tt0133093"},"hasFile":true,"playable":false}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not held"}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestHoldingsByIdentity(t *testing.T) {
	var auth string
	srv := holdingsServer(t, func(r *http.Request) { auth = r.Header.Get("Authorization") })
	c := New("", srv.URL, staticToken("svc-token"))
	ctx := context.Background()

	if err := c.ProbeHoldings(ctx); err != nil {
		t.Fatalf("probe: %v", err)
	}
	if auth != "Bearer svc-token" {
		t.Errorf("the read side is asked with acquire's service account, got %q", auth)
	}
	sh, err := c.SeriesHoldings(ctx, "tmdb", "97546")
	if err != nil {
		t.Fatal(err)
	}
	eps := sh.Seasons[0].Episodes
	if sh.SeriesID != "s1" || sh.ExternalIDs.Int("tvdb") != 362753 || len(eps) != 3 {
		t.Fatalf("series = %+v", sh)
	}
	if !eps[0].Held() || !eps[1].Held() || eps[1].CoveredBy != "e7" || eps[2].Held() {
		t.Errorf("held: %+v", eps)
	}
	if _, err := c.SeriesHoldings(ctx, "tmdb", "1"); !errors.Is(err, ErrNotHeld) {
		t.Errorf("a series not held: %v", err)
	}
	m, err := c.MovieHoldings(ctx, "imdb", "tt0133093")
	if err != nil || !m.Held() || m.Playable || m.ExternalIDs.IMDb() != "tt0133093" {
		t.Errorf("movie = %+v %v", m, err)
	}
}

// A catalog without the contract is told apart from one that holds nothing:
// the listing is answered 404, 405 or 501, or with something that is not a
// page. A read side that is down, or refuses acquire, is neither.
func TestHoldingsProbe(t *testing.T) {
	for _, c := range []struct {
		h    http.HandlerFunc
		want error
	}{
		{func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) }, ErrHoldingsUnsupported},
		{func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(501) }, ErrHoldingsUnsupported},
		{func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(405) }, ErrHoldingsUnsupported},
		{func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"title":"something else"}`)) }, ErrHoldingsUnsupported},
		{func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"items":[],"page":1,"pageSize":1,"total":0}`))
		}, nil},
		// An empty page that says so with null is a page all the same.
		{func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"items":null,"page":1,"pageSize":1,"total":0}`))
		}, nil},
		{func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"items":null}`)) }, ErrHoldingsUnsupported},
	} {
		srv := httptest.NewServer(c.h)
		err := New("", srv.URL, nil).ProbeHoldings(context.Background())
		if !errors.Is(err, c.want) || (c.want == nil && err != nil) {
			t.Errorf("probe = %v, want %v", err, c.want)
		}
		srv.Close()
	}
	for _, code := range []int{401, 403, 500, 503} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(code) }))
		err := New("", srv.URL, nil).ProbeHoldings(context.Background())
		var he *HoldingsError
		if !errors.As(err, &he) || errors.Is(err, ErrHoldingsUnsupported) {
			t.Errorf("HTTP %d: %v — a read side that is down or refusing is no answer either way", code, err)
		}
		if code == 401 && !strings.Contains(err.Error(), "audience") {
			t.Errorf("a refusal should say what the service account needs: %v", err)
		}
		srv.Close()
	}
}

// The listing is read to its last page, at the largest page there is.
func TestListHoldings(t *testing.T) {
	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		asked = append(asked, q.Get("page")+"/"+q.Get("pageSize"))
		page, _ := strconv.Atoi(q.Get("page"))
		items := `[]`
		if page <= 2 {
			items = fmt.Sprintf(`[{"itemId":"s%d","title":"Show %d","year":null,"externalIds":{}}]`, page, page)
		}
		_, _ = fmt.Fprintf(w, `{"items":%s,"page":%d,"pageSize":1000,"total":1500}`, items, page)
	}))
	defer srv.Close()
	got, err := New("", srv.URL, nil).ListHoldings(context.Background(), "series")
	if err != nil || len(got) != 2 || got[1].ItemID != "s2" || got[1].Year != 0 || len(got[1].ExternalIDs) != 0 {
		t.Fatalf("listing: %+v %v", got, err)
	}
	if strings.Join(asked, ",") != "1/1000,2/1000" {
		t.Errorf("asked %v", asked)
	}
}

type staticToken string

func (s staticToken) Token(context.Context) (string, error) { return string(s), nil }
func (s staticToken) Enabled() bool                         { return true }
