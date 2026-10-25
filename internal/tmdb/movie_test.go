package tmdb

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func movieServer(t *testing.T, body map[string]any) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/movie/9100":
			if got := r.URL.Query().Get("append_to_response"); got != "external_ids,alternative_titles,release_dates" {
				t.Errorf("append_to_response = %q", got)
			}
			_ = json.NewEncoder(w).Encode(body)
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"status_code":34,"status_message":"The resource you requested could not be found."}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// A movie's answer carries its identity, its other titles and every dated
// release: what decides when a requested movie is searched.
func TestMovieReadsItsReleases(t *testing.T) {
	srv := movieServer(t, map[string]any{
		"id": 9100, "title": "Example Clay Film", "status": "Released", "release_date": "2026-09-11",
		"imdb_id":            "tt0000001",
		"alternative_titles": map[string]any{"titles": []map[string]any{{"title": "Example Clay Film"}, {"title": "Clay Film"}}},
		"release_dates": map[string]any{"results": []map[string]any{
			{"iso_3166_1": "US", "release_dates": []map[string]any{
				{"type": 3, "release_date": "2026-09-11T00:00:00.000Z"},
				{"type": 4, "release_date": "2026-11-25T00:00:00.000Z"},
			}},
			{"iso_3166_1": "DE", "release_dates": []map[string]any{
				{"type": 1, "release_date": "2026-09-02T00:00:00.000Z"},
				{"type": 5, "release_date": "2027-01-15T00:00:00.000Z"},
				{"type": 6, "release_date": "not a date"},
			}},
		}},
	})
	c := testClient(srv)
	m, err := c.Movie(context.Background(), 9100)
	if err != nil {
		t.Fatal(err)
	}
	if m.TMDBID != 9100 || m.IMDBID != "tt0000001" || m.Title != "Example Clay Film" || m.Year() != 2026 || m.Status != "Released" {
		t.Errorf("movie = %+v", m)
	}
	if len(m.Aliases) != 1 || m.Aliases[0] != "Clay Film" {
		t.Errorf("aliases = %v, want the other title once, not its own name", m.Aliases)
	}
	if len(m.Releases) != 4 {
		t.Errorf("releases = %+v, want the four with a date", m.Releases)
	}
	at, kind, ok := m.HomeRelease()
	if !ok || kind != "digital" || !at.Equal(time.Date(2026, 11, 25, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("home release = %v %q %v, want the digital one, the first of any country", at, kind, ok)
	}
	if at, ok := m.TheatricalRelease(); !ok || !at.Equal(time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("theatrical release = %v %v, want the premiere", at, ok)
	}

	// A movie TMDB does not know is a 404 the caller can tell from an outage.
	_, err = c.Movie(context.Background(), 1)
	var he *HTTPError
	if !errors.As(err, &he) || !he.NotFound() || strings.Contains(err.Error(), "eyJ") {
		t.Errorf("unknown movie: %v", err)
	}
}

func TestReleasesOfAMovie(t *testing.T) {
	day := func(m time.Month, d int) time.Time { return time.Date(2026, m, d, 0, 0, 0, 0, time.UTC) }
	primary := day(time.March, 1)
	for _, tc := range []struct {
		name       string
		movie      Movie
		home       string
		homeAt     time.Time
		theatrical time.Time
		noTheatre  bool
	}{
		{"cinemas only", Movie{Releases: []Release{{Type: ReleaseTheatrical, Date: day(time.May, 1)}}},
			"", time.Time{}, day(time.May, 1), false},
		{"physical before digital", Movie{Releases: []Release{
			{Type: ReleaseDigital, Date: day(time.June, 10)}, {Type: ReleasePhysical, Date: day(time.June, 1)}}},
			"physical", day(time.June, 1), time.Time{}, true},
		{"tv", Movie{Releases: []Release{{Type: ReleaseTV, Date: day(time.July, 4)}}},
			"tv", day(time.July, 4), time.Time{}, true},
		{"only a primary date", Movie{ReleaseDate: &primary}, "", time.Time{}, primary, false},
		{"nothing dated", Movie{}, "", time.Time{}, time.Time{}, true},
	} {
		at, kind, ok := tc.movie.HomeRelease()
		if kind != tc.home || ok != (tc.home != "") || !at.Equal(tc.homeAt) {
			t.Errorf("%s: home release %v %q %v", tc.name, at, kind, ok)
		}
		th, ok := tc.movie.TheatricalRelease()
		if ok == tc.noTheatre || !th.Equal(tc.theatrical) {
			t.Errorf("%s: theatrical %v %v", tc.name, th, ok)
		}
	}
}

func TestRunningStatuses(t *testing.T) {
	for status, want := range map[string]bool{
		"Returning Series": true, "In Production": true, "Planned": true, "returning series": true,
		"Ended": false, "Canceled": false, "Pilot": false, "": false,
	} {
		if Running(status) != want {
			t.Errorf("Running(%q) = %v, want %v", status, !want, want)
		}
	}
}

// A series' answer is one call: its seasons with their episode counts, its
// identities and its first air date. A season's episodes are one call each.
func TestSeriesAndOneSeason(t *testing.T) {
	srv, _ := invServer(t, -1)
	defer srv.Close()
	c := testClient(srv)
	s, err := c.Series(context.Background(), 1396)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Seasons) != 1 || s.Seasons[0].Number != 1 || s.Seasons[0].EpisodeCount != 4 || s.TVDBID != 81189 || s.Status != "Ended" {
		t.Errorf("series = %+v", s)
	}
	eps, err := c.Season(context.Background(), 1396, 1)
	if err != nil || len(eps) != 4 || eps[0].Title != "Pilot" || eps[3].AirDate != nil {
		t.Errorf("season = %+v %v", eps, err)
	}
	if _, err := (&Client{}).Series(context.Background(), 1); err == nil {
		t.Error("a client without a key read a series")
	}
}
