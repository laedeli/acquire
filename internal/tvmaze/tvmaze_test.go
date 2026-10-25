package tvmaze

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// A TVmaze of the tests' own: the show 77 known by its TVDB id (a redirect,
// as TVmaze answers), the show 78 by its IMDb id only (a show answered in
// full), the shows 81 and 82 on a streaming service, and every call recorded
// with its time; limited answers that many 429s first, always429 every one.
// The ids are invented.
type fake struct {
	mu        sync.Mutex
	calls     []string
	at        []time.Time
	limited   int
	always429 bool
}

func (f *fake) server(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.calls = append(f.calls, r.URL.RequestURI())
		f.at = append(f.at, time.Now())
		limited := f.limited > 0 || f.always429
		if f.limited > 0 {
			f.limited--
		}
		f.mu.Unlock()
		if r.Header.Get("User-Agent") != "acquire-test" {
			t.Errorf("User-Agent %q", r.Header.Get("User-Agent"))
		}
		embedded := r.URL.Query().Get("embed") == "episodes"
		switch {
		case limited:
			w.Header().Set("Retry-After", "0")
			http.Error(w, "slow down", http.StatusTooManyRequests)
		case r.URL.Path == "/lookup/shows" && r.URL.Query().Get("thetvdb") == "9900011":
			http.Redirect(w, r, "/shows/77", http.StatusMovedPermanently)
		case r.URL.Path == "/lookup/shows" && r.URL.Query().Get("imdb") == "tt99000012":
			_, _ = w.Write([]byte(`{"id":78,"name":"Example Show"}`))
		case r.URL.Path == "/shows/77" && embedded:
			_, _ = w.Write([]byte(`{"id":77,"name":"Example Night Show","runtime":60,
			  "network":{"id":1,"name":"Example Network","country":{"name":"United States","code":"US","timezone":"America/New_York"}},
			  "webChannel":null,
			  "_embedded":{"episodes":[
			  {"id":1,"season":1,"number":1,"airdate":"2026-10-23","airtime":"21:00","airstamp":"2026-10-24T01:00:00+00:00","runtime":90},
			  {"id":2,"season":1,"number":2,"airdate":"2026-10-30","airtime":"","airstamp":"2026-10-30T12:00:00+00:00","runtime":null},
			  {"id":3,"season":1,"number":null,"airdate":"2026-10-31","airtime":"20:00","airstamp":"2026-11-01T00:00:00+00:00"},
			  {"id":4,"season":2,"number":1,"airdate":"","airtime":"","airstamp":null}]}}`))
		case r.URL.Path == "/shows/81" && embedded:
			_, _ = w.Write([]byte(`{"id":81,"name":"Example Streamed Show","runtime":null,"averageRuntime":45,"network":null,
			  "webChannel":{"id":2,"name":"Example Streams","country":null},
			  "_embedded":{"episodes":[{"id":5,"season":1,"number":1,"airdate":"2026-10-23","airtime":"","airstamp":"2026-10-23T12:00:00+00:00"}]}}`))
		case r.URL.Path == "/shows/82" && embedded:
			_, _ = w.Write([]byte(`{"id":82,"name":"Example Other Stream","network":null,
			  "webChannel":{"id":3,"name":"Example Local Streams","country":{"name":"Germany","code":"de","timezone":"Europe/Berlin"}},
			  "_embedded":{"episodes":[]}}`))
		case r.URL.Path == "/shows/79":
			http.Error(w, "boom", http.StatusBadGateway)
		case r.URL.Path == "/shows/83":
			http.Error(w, "forbidden", http.StatusForbidden)
		case r.URL.Path == "/shows/84":
			_, _ = w.Write([]byte(`<html>a proxy's page</html>`))
		case r.URL.Path == "/shows/85":
			_, _ = w.Write([]byte(`{"id":86,"name":"Another Show","_embedded":{"episodes":[]}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func client(srv *httptest.Server) *Client {
	c := New(srv.URL, "acquire-test")
	c.Every = 20 * time.Millisecond
	return c
}

// A show is found by its TVDB id off TVmaze's redirect, which is not
// followed, else by its IMDb id; a show TVmaze knows by neither is not found.
func TestLookingAShowUp(t *testing.T) {
	f := &fake{}
	c := client(f.server(t))
	ctx := context.Background()
	if id, err := c.LookupShow(ctx, 9900011, "tt99000011"); err != nil || id != 77 {
		t.Errorf("by its TVDB id: %d %v", id, err)
	}
	if id, err := c.LookupShow(ctx, 9900012, "tt99000012"); err != nil || id != 78 {
		t.Errorf("by its IMDb id: %d %v", id, err)
	}
	if _, err := c.LookupShow(ctx, 9900013, ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown: %v", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if strings.Join(f.calls, " ") != "/lookup/shows?thetvdb=9900011 /lookup/shows?thetvdb=9900012 /lookup/shows?imdb=tt99000012 /lookup/shows?thetvdb=9900013" {
		t.Errorf("calls = %v, want one per id asked, and no redirect followed", f.calls)
	}
}

// A show's record says how it airs — on a network, a broadcast; on a web
// channel, streamed — and where, and how long it runs; its episodes come in
// the same call. An episode's airstamp is when it airs only when TVmaze knows
// the hour; its runtime is its own, else the show's; a special, numbered in
// no season, is left out.
func TestAShowAndItsEpisodes(t *testing.T) {
	f := &fake{}
	c := client(f.server(t))
	ctx := context.Background()
	show, err := c.Show(ctx, 77)
	if err != nil || show.Name != "Example Night Show" || show.Kind != Broadcast || show.Country != "US" || show.Runtime != 60 || len(show.Episodes) != 3 {
		t.Fatalf("the show = %+v %v", show, err)
	}
	eps := show.Episodes
	if eps[0].AirStamp == nil || !eps[0].AirStamp.Equal(time.Date(2026, 10, 24, 1, 0, 0, 0, time.UTC)) ||
		eps[0].AirDate == nil || eps[0].AirDate.Format("2006-01-02") != "2026-10-23" || eps[0].Runtime != 90 {
		t.Errorf("an episode at 21:00 ET, 90 minutes long: %+v", eps[0])
	}
	if eps[1].AirStamp != nil || eps[1].AirDate == nil || eps[1].Runtime != 60 {
		t.Errorf("an episode of a day without an hour, the show's runtime: %+v", eps[1])
	}
	if eps[2].Season != 2 || eps[2].AirDate != nil || eps[2].AirStamp != nil {
		t.Errorf("an episode without a date: %+v", eps[2])
	}
	if f.calls[0] != "/shows/77?embed=episodes" {
		t.Errorf("calls %v, want the record and its episodes in one", f.calls)
	}
	if s, err := c.Show(ctx, 81); err != nil || s.Kind != Streaming || s.Country != "" || s.Runtime != 45 || len(s.Episodes) != 1 ||
		s.Episodes[0].AirStamp != nil || s.Episodes[0].Runtime != 45 {
		t.Errorf("a show streamed everywhere: %+v %v", s, err)
	}
	if s, err := c.Show(ctx, 82); err != nil || s.Kind != Streaming || s.Country != "DE" || s.Runtime != 0 {
		t.Errorf("a show streamed from one country: %+v %v", s, err)
	}
	if _, err := c.Show(ctx, 80); !errors.Is(err, ErrNotFound) {
		t.Errorf("a show TVmaze does not know: %v", err)
	}
	if _, err := c.Show(ctx, 79); !Unavailable(err) {
		t.Errorf("TVmaze failing: %v", err)
	}
	// TVmaze refusing — a 403, any 4xx but 404 — is TVmaze not answering as
	// it should; an answer that cannot be read, or of another show, is
	// neither the show's absence nor TVmaze down.
	if _, err := c.Show(ctx, 83); !Unavailable(err) {
		t.Errorf("TVmaze refusing: %v", err)
	}
	for _, id := range []int64{84, 85} {
		if _, err := c.Show(ctx, id); !errors.Is(err, ErrUnreadable) || Unavailable(err) {
			t.Errorf("show %d, an answer that cannot be read: %v", id, err)
		}
	}
	if Unavailable(ErrNotFound) || !Unavailable(&HTTPError{Status: 429}) || !Unavailable(&HTTPError{Status: 401}) || Unavailable(&HTTPError{Status: 404}) {
		t.Error("what is TVmaze's fault")
	}
	if (&Client{}).Enabled() || (*Client)(nil).Enabled() {
		t.Error("a client without an address asks")
	}
}

// A 429 that lasts is given up after three answers — waited out between them
// — and is TVmaze's fault: no other show would fare better now.
func TestALimitThatLasts(t *testing.T) {
	f := &fake{always429: true}
	c := client(f.server(t))
	c.RetryWait = time.Millisecond
	_, err := c.LookupShow(context.Background(), 9900011, "tt99000011")
	var he *HTTPError
	if !errors.As(err, &he) || he.Status != http.StatusTooManyRequests || !Unavailable(err) {
		t.Errorf("a lasting 429: %v", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) != 3 {
		t.Errorf("calls %v, want three", f.calls)
	}
}

// Calls are spaced Every apart, and a 429 is waited out and asked again.
func TestCallsAreSpacedAndLimitsWaitedOut(t *testing.T) {
	f := &fake{limited: 1}
	c := client(f.server(t))
	c.RetryWait = time.Millisecond
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if _, err := c.Show(ctx, 77); err != nil {
			t.Fatal(err)
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.at) != 4 {
		t.Fatalf("calls = %v, want the limited one asked again", f.calls)
	}
	for i := 1; i < len(f.at); i++ {
		if gap := f.at[i].Sub(f.at[i-1]); gap < 15*time.Millisecond {
			t.Errorf("call %d came %v after the one before", i, gap)
		}
	}
}
