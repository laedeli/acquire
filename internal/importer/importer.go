// Package importer reads acquisition INTENT out of the incumbent automation
// services so the WANT model starts from what is already being tracked rather
// than from nothing.
//
// It imports intent only: which titles are monitored, their identity across the
// id spaces indexers actually accept, their aliases, and their type. It
// deliberately does NOT import the episode inventory — that is derived from
// TMDB, because a derivation we can re-run is worth more than a one-time copy
// we cannot verify. If the derivation is wrong we lose a day; if a migration is
// wrong we lose the ability to tell.
//
// Everything here is read-only against the source. Nothing in this package can
// grab: there is no gateway client in scope, by construction, because a config
// flag is exactly how a shadow period starts double-grabbing.
package importer

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Title is one imported intent record, in our vocabulary rather than the
// source's.
type Title struct {
	TMDBID     int64
	TVDBID     int64
	IMDBID     string
	Kind       string // movie | series
	Title      string
	SortTitle  string
	Year       int
	Status     string // continuing | ended | released | …
	SeriesType string // standard | daily | anime
	Monitored  bool
	MonitorNew bool
	Aliases    []string
}

// Source is an incumbent instance to read from.
type Source struct {
	BaseURL string
	APIKey  string
	Kind    string // movie | series
	HTTP    *http.Client
}

// Import fetches every tracked title. The caller decides what to do with them;
// this package never writes.
func (s Source) Import(ctx context.Context) ([]Title, error) {
	path := "/api/v3/series"
	if s.Kind == "movie" {
		path = "/api/v3/movie"
	}
	u := strings.TrimRight(s.BaseURL, "/") + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Api-Key", s.APIKey)
	cl := s.HTTP
	if cl == nil {
		cl = &http.Client{Timeout: 120 * time.Second}
	}
	resp, err := cl.Do(req)
	if err != nil {
		return nil, fmt.Errorf("import %s: %w", s.Kind, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("import %s: http %d", s.Kind, resp.StatusCode)
	}

	var raw []struct {
		Title           string `json:"title"`
		SortTitle       string `json:"sortTitle"`
		Year            int    `json:"year"`
		Monitored       bool   `json:"monitored"`
		MonitorNewItems string `json:"monitorNewItems"`
		Status          string `json:"status"`
		SeriesType      string `json:"seriesType"`
		TvdbID          int64  `json:"tvdbId"`
		TmdbID          int64  `json:"tmdbId"`
		ImdbID          string `json:"imdbId"`
		AlternateTitles []struct {
			Title string `json:"title"`
		} `json:"alternateTitles"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("import %s: decode: %w", s.Kind, err)
	}

	out := make([]Title, 0, len(raw))
	for _, r := range raw {
		t := Title{
			TMDBID: r.TmdbID, TVDBID: r.TvdbID, IMDBID: strings.TrimSpace(r.ImdbID),
			Kind: s.Kind, Title: r.Title, SortTitle: r.SortTitle, Year: r.Year,
			Status: strings.ToLower(r.Status), Monitored: r.Monitored,
			// "all" is the incumbent's value for auto-monitoring new seasons.
			MonitorNew: r.MonitorNewItems == "" || strings.EqualFold(r.MonitorNewItems, "all"),
			SeriesType: normaliseType(r.SeriesType),
		}
		seen := map[string]bool{strings.ToLower(r.Title): true}
		for _, a := range r.AlternateTitles {
			k := strings.ToLower(strings.TrimSpace(a.Title))
			if k == "" || seen[k] {
				continue
			}
			seen[k] = true
			t.Aliases = append(t.Aliases, strings.TrimSpace(a.Title))
		}
		out = append(out, t)
	}
	return out, nil
}

func normaliseType(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "anime":
		return "anime"
	case "daily":
		return "daily"
	case "":
		return ""
	default:
		return "standard"
	}
}

// Resolvable reports whether a title can actually be searched for.
//
// This is the check the P1 gate made unavoidable: ZERO of 70 indexers accept a
// tmdbId. A series needs a tvdbId and a movie needs an imdbId, or every typed
// search for it degrades to free text — which is how you end up grabbing
// "The.Bad.Guys.Breaking.In.S02E05" when you asked for Breaking Bad.
//
// Titles that fail this are imported anyway and reported, rather than dropped:
// silently discarding them would make the inventory look complete while the
// gaps sat invisible.
func (t Title) Resolvable() bool {
	if t.Kind == "series" {
		return t.TVDBID > 0
	}
	return t.IMDBID != "" || t.TMDBID > 0
}

// Report summarises an import so the operator sees what did not come across.
type Report struct {
	Total         int
	Monitored     int
	Unresolvable  []string // titles that cannot be typed-searched
	MissingTMDBID []string // no TMDB id at all: cannot even be a titles row
	Aliases       int
}

func Summarise(titles []Title) Report {
	r := Report{Total: len(titles)}
	for _, t := range titles {
		if t.Monitored {
			r.Monitored++
		}
		r.Aliases += len(t.Aliases)
		if t.TMDBID == 0 {
			r.MissingTMDBID = append(r.MissingTMDBID, t.Title)
			continue
		}
		if !t.Resolvable() {
			r.Unresolvable = append(r.Unresolvable, t.Title)
		}
	}
	return r
}

// TargetURL is a helper for building the source URL from a service name, kept
// here so callers do not hardcode ports in three places.
func TargetURL(host string, port int) string {
	return (&url.URL{Scheme: "http", Host: fmt.Sprintf("%s:%d", host, port)}).String()
}
