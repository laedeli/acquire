package tmdb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// The kinds of release TMDB dates a movie's releases by
// (/movie/{id}/release_dates): a premiere, a limited and a wide theatrical
// release, and the releases to watch at home — digital, physical (disc) and
// TV.
const (
	ReleasePremiere          = 1
	ReleaseTheatricalLimited = 2
	ReleaseTheatrical        = 3
	ReleaseDigital           = 4
	ReleasePhysical          = 5
	ReleaseTV                = 6
)

// Movie is a movie as TMDB's /movie/{id} answers it in one call, with its
// external ids, its other titles and every release date appended.
type Movie struct {
	TMDBID int64
	IMDBID string
	Title  string
	// Status is TMDB's: Rumored, Planned, In Production, Post Production,
	// Released or Canceled.
	Status string
	// ReleaseDate is TMDB's primary release date, usually the first
	// theatrical one.
	ReleaseDate *time.Time
	// Aliases are its original title, and its titles in English-speaking
	// countries and in the countries it comes from (titlesOf); Countries
	// the countries it comes from.
	Aliases   []string
	Countries []string
	Releases  []Release
}

// Release is one dated release of a movie in one country.
type Release struct {
	Type    int
	Date    time.Time
	Country string
}

// Year is the year of the movie's primary release, or 0.
func (m Movie) Year() int {
	if m.ReleaseDate == nil {
		return 0
	}
	return m.ReleaseDate.Year()
}

// HomeRelease is the movie's first release to watch at home, in any country
// — digital, physical or TV, whichever comes first — and which kind it is
// (digital, physical, tv). ok is false when TMDB dates none: the movie may be
// in cinemas only, or TMDB may know no more of it.
func (m Movie) HomeRelease() (at time.Time, kind string, ok bool) {
	r, kind, ok := m.home()
	return r.Date, kind, ok
}

// HomeCountry is where the movie's first release to watch at home comes out
// (HomeRelease): the day it comes out begins at midnight there. "" when TMDB
// dates none.
func (m Movie) HomeCountry() string {
	r, _, _ := m.home()
	return r.Country
}

func (m Movie) home() (first Release, kind string, ok bool) {
	for _, r := range m.Releases {
		k := ""
		switch r.Type {
		case ReleaseDigital:
			k = "digital"
		case ReleasePhysical:
			k = "physical"
		case ReleaseTV:
			k = "tv"
		default:
			continue
		}
		if !ok || r.Date.Before(first.Date) {
			first, kind, ok = r, k, true
		}
	}
	return first, kind, ok
}

// TheatricalRelease is the movie's first release in cinemas, in any country:
// a limited or a wide theatrical release; else its primary release date;
// else, when TMDB dates nothing else, its premiere — a festival's premiere
// can come months before it reaches cinemas. ok is false when TMDB dates
// none.
func (m Movie) TheatricalRelease() (at time.Time, ok bool) {
	first := func(types ...int) (time.Time, bool) {
		var at time.Time
		ok := false
		for _, r := range m.Releases {
			for _, t := range types {
				if r.Type == t && (!ok || r.Date.Before(at)) {
					at, ok = r.Date, true
				}
			}
		}
		return at, ok
	}
	if at, ok := first(ReleaseTheatricalLimited, ReleaseTheatrical); ok {
		return at, true
	}
	if m.ReleaseDate != nil {
		return *m.ReleaseDate, true
	}
	return first(ReleasePremiere)
}

// Movie reads a movie's answer: one call, with its external ids, its other
// titles and its release dates appended.
func (c *Client) Movie(ctx context.Context, tmdbID int64) (Movie, error) {
	var out Movie
	if !c.Enabled() {
		return out, fmt.Errorf("tmdb not configured")
	}
	body, err := c.get(ctx, "/movie/"+strconv.FormatInt(tmdbID, 10),
		url.Values{"append_to_response": {"external_ids,alternative_titles,release_dates"}})
	if err != nil {
		return out, err
	}
	var raw struct {
		ID                  int64    `json:"id"`
		Title               string   `json:"title"`
		OriginalTitle       string   `json:"original_title"`
		OriginCountry       []string `json:"origin_country"`
		ProductionCountries []struct {
			Country string `json:"iso_3166_1"`
		} `json:"production_countries"`
		Status      string `json:"status"`
		ReleaseDate string `json:"release_date"`
		IMDBID      string `json:"imdb_id"`
		ExternalIDs struct {
			IMDBID string `json:"imdb_id"`
		} `json:"external_ids"`
		AlternativeTitles struct {
			Titles []altTitle `json:"titles"`
		} `json:"alternative_titles"`
		ReleaseDates struct {
			Results []struct {
				Country string `json:"iso_3166_1"`
				Dates   []struct {
					Type int    `json:"type"`
					Date string `json:"release_date"`
				} `json:"release_dates"`
			} `json:"results"`
		} `json:"release_dates"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return out, fmt.Errorf("tmdb movie/%d: decode: %w", tmdbID, err)
	}
	out = Movie{TMDBID: raw.ID, Title: strings.TrimSpace(raw.Title), Status: strings.TrimSpace(raw.Status),
		ReleaseDate: date(raw.ReleaseDate), IMDBID: strings.TrimSpace(raw.IMDBID)}
	if out.IMDBID == "" {
		out.IMDBID = strings.TrimSpace(raw.ExternalIDs.IMDBID)
	}
	out.Countries = countries(raw.OriginCountry)
	if len(out.Countries) == 0 {
		for _, c := range raw.ProductionCountries {
			out.Countries = append(out.Countries, c.Country)
		}
		out.Countries = countries(out.Countries)
	}
	out.Aliases = titlesOf(out.Title, raw.OriginalTitle, out.Countries, raw.AlternativeTitles.Titles)
	for _, country := range raw.ReleaseDates.Results {
		for _, d := range country.Dates {
			at, err := time.Parse(time.RFC3339, strings.TrimSpace(d.Date))
			if err != nil {
				continue
			}
			out.Releases = append(out.Releases, Release{Type: d.Type, Date: at.UTC(), Country: country.Country})
		}
	}
	return out, nil
}
