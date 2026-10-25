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
// full), and every call recorded with its time. The ids are invented.
type fake struct {
	mu      sync.Mutex
	calls   []string
	at      []time.Time
	limited int
}

func (f *fake) server(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.calls = append(f.calls, r.URL.RequestURI())
		f.at = append(f.at, time.Now())
		limited := f.limited > 0
		if limited {
			f.limited--
		}
		f.mu.Unlock()
		if r.Header.Get("User-Agent") != "acquire-test" {
			t.Errorf("User-Agent %q", r.Header.Get("User-Agent"))
		}
		switch {
		case limited:
			w.Header().Set("Retry-After", "0")
			http.Error(w, "slow down", http.StatusTooManyRequests)
		case r.URL.Path == "/lookup/shows" && r.URL.Query().Get("thetvdb") == "9900011":
			http.Redirect(w, r, "/shows/77", http.StatusMovedPermanently)
		case r.URL.Path == "/lookup/shows" && r.URL.Query().Get("imdb") == "tt99000012":
			_, _ = w.Write([]byte(`{"id":78,"name":"Example Show"}`))
		case r.URL.Path == "/shows/77/episodes":
			_, _ = w.Write([]byte(`[
			  {"id":1,"season":1,"number":1,"airdate":"2026-10-23","airtime":"21:00","airstamp":"2026-10-24T01:00:00+00:00"},
			  {"id":2,"season":1,"number":2,"airdate":"2026-10-30","airtime":"","airstamp":"2026-10-30T12:00:00+00:00"},
			  {"id":3,"season":1,"number":null,"airdate":"2026-10-31","airtime":"20:00","airstamp":"2026-11-01T00:00:00+00:00"},
			  {"id":4,"season":2,"number":1,"airdate":"","airtime":"","airstamp":null}]`))
		case r.URL.Path == "/shows/79/episodes":
			http.Error(w, "boom", http.StatusBadGateway)
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

// An episode's airstamp is when it airs only when TVmaze knows the hour; a
// special, numbered in no season, is left out.
func TestEpisodesAndTheirAirTimes(t *testing.T) {
	c := client((&fake{}).server(t))
	eps, err := c.Episodes(context.Background(), 77)
	if err != nil || len(eps) != 3 {
		t.Fatalf("episodes = %+v %v", eps, err)
	}
	if eps[0].AirStamp == nil || !eps[0].AirStamp.Equal(time.Date(2026, 10, 24, 1, 0, 0, 0, time.UTC)) ||
		eps[0].AirDate == nil || eps[0].AirDate.Format("2006-01-02") != "2026-10-23" {
		t.Errorf("an episode at 21:00 ET: %+v", eps[0])
	}
	if eps[1].AirStamp != nil || eps[1].AirDate == nil {
		t.Errorf("an episode of a day without an hour: %+v", eps[1])
	}
	if eps[2].Season != 2 || eps[2].AirDate != nil || eps[2].AirStamp != nil {
		t.Errorf("an episode without a date: %+v", eps[2])
	}
	if _, err := c.Episodes(context.Background(), 80); !errors.Is(err, ErrNotFound) {
		t.Errorf("a show TVmaze does not know: %v", err)
	}
	if _, err := c.Episodes(context.Background(), 79); !Unavailable(err) {
		t.Errorf("TVmaze failing: %v", err)
	}
	if Unavailable(ErrNotFound) || !Unavailable(&HTTPError{Status: 429}) {
		t.Error("what is TVmaze's fault")
	}
	if (&Client{}).Enabled() || (*Client)(nil).Enabled() {
		t.Error("a client without an address asks")
	}
}

// Calls are spaced Every apart, and a 429 is waited out and asked again.
func TestCallsAreSpacedAndLimitsWaitedOut(t *testing.T) {
	f := &fake{limited: 1}
	c := client(f.server(t))
	c.RetryWait = time.Millisecond
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if _, err := c.Episodes(ctx, 77); err != nil {
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
