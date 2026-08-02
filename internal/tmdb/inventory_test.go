package tmdb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func invServer(t *testing.T, seasonFails int) (*httptest.Server, *int) {
	t.Helper()
	var bearer int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			bearer++
		}
		switch {
		case strings.Contains(r.URL.Path, "/season/"):
			n := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
			if n == fmt.Sprint(seasonFails) {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"episodes": []map[string]any{
				{"season_number": 1, "episode_number": 1, "name": "Pilot", "air_date": "2008-01-20"},
				{"season_number": 1, "episode_number": 2, "name": "Two", "air_date": "2008-01-27"},
				{"season_number": 1, "episode_number": 3, "name": "Unaired", "air_date": "2999-01-01"},
				{"season_number": 1, "episode_number": 4, "name": "No date", "air_date": ""},
			}})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": 1396, "name": "Breaking Bad", "status": "Ended",
				"external_ids":       map[string]any{"tvdb_id": 81189, "imdb_id": "tt0903747"},
				"alternative_titles": map[string]any{"results": []map[string]any{{"title": "Breaking Bad"}, {"title": "Totalna Melina"}}},
				"seasons": []map[string]any{
					{"season_number": 0, "episode_count": 9}, // specials
					{"season_number": 1, "episode_count": 4},
				},
			})
		}
	}))
	return srv, &bearer
}

func testClient(srv *httptest.Server) *Client {
	c := New("eyJhbGciOiJIUzI1NiJ9.fake.v4token", "en-US")
	c.HTTP = srv.Client()
	c.BaseURL = srv.URL
	return c
}

// Season 0 must be excluded: specials agree with the incumbent only 45.9% of
// the time, so including them injects noise into every reconciliation.
func TestInventoryExcludesSpecials(t *testing.T) {
	srv, _ := invServer(t, -1)
	defer srv.Close()
	c := testClient(srv)
	in, err := c.SeriesInventory(context.Background(), 1396)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := in.SeasonInfo[0]; ok {
		t.Error("season 0 was included")
	}
	for _, e := range in.Episodes {
		if e.SeasonNumber == 0 {
			t.Errorf("a special leaked into the inventory: %+v", e)
		}
	}
	if len(in.Episodes) != 4 {
		t.Fatalf("episodes = %d, want 4", len(in.Episodes))
	}
	if in.TVDBID != 81189 || in.IMDBID != "tt0903747" {
		t.Errorf("external ids not carried: %+v", in)
	}
	// The series name must not become its own alias.
	for _, a := range in.Aliases {
		if strings.EqualFold(a, in.Name) {
			t.Error("the name was duplicated into aliases")
		}
	}
}

// An unreadable season must fail loudly. Silently returning a short list is how
// a fetch bug becomes a fake "these episodes are missing" result — the exact
// error the P1 metadata gate made when it reported 48.6% unresolvable.
func TestInventoryFailsLoudlyOnAPartialFetch(t *testing.T) {
	srv, _ := invServer(t, 1)
	defer srv.Close()
	c := testClient(srv)
	_, err := c.SeriesInventory(context.Background(), 1396)
	if err == nil {
		t.Fatal("a failed season fetch returned no error — the inventory would look short but complete")
	}
}

// A v4 JWT must go in the Authorization header. Passing it as ?api_key=
// returns 401 with status_code 7, which reads like a bad key.
func TestV4TokenUsesBearer(t *testing.T) {
	srv, bearer := invServer(t, -1)
	defer srv.Close()
	c := testClient(srv)
	if _, err := c.SeriesInventory(context.Background(), 1396); err != nil {
		t.Fatal(err)
	}
	if *bearer == 0 {
		t.Error("no request used a Bearer token")
	}
}

// An episode with no air date must not be treated as airable: searching for
// something that may not exist spends indexer quota, the scarce resource.
func TestAiredIsConservative(t *testing.T) {
	now := time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC)
	past := now.AddDate(0, 0, -1)
	future := now.AddDate(0, 0, 1)
	for _, tc := range []struct {
		name string
		ep   Episode
		want bool
	}{
		{"aired", Episode{AirDate: &past}, true},
		{"future", Episode{AirDate: &future}, false},
		{"no date", Episode{}, false},
	} {
		if got := tc.ep.Aired(now); got != tc.want {
			t.Errorf("%s: Aired = %v, want %v", tc.name, got, tc.want)
		}
	}
}
