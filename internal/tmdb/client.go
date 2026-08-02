// Package tmdb is a minimal discovery client: search movies/series so a user can
// pick a precise title to request (carrying its tmdb id + metadata forward).
package tmdb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Client struct {
	APIKey   string
	Language string
	HTTP     *http.Client
	// BaseURL overrides the TMDB endpoint. Empty means the real one; tests set
	// it to a local server so the inventory logic is exercised without network.
	BaseURL string
}

// base returns the API root, defaulting to TMDB.
func (c *Client) base() string {
	if c.BaseURL != "" {
		return strings.TrimRight(c.BaseURL, "/")
	}
	return "https://api.themoviedb.org/3"
}

func New(apiKey, language string) *Client {
	if language == "" {
		language = "en-US"
	}
	return &Client{APIKey: apiKey, Language: language, HTTP: &http.Client{Timeout: 10 * time.Second}}
}

func (c *Client) Enabled() bool { return c != nil && c.APIKey != "" }

// Result is one discovery hit.
type Result struct {
	TMDBID    int64  `json:"tmdbId"`
	MediaType string `json:"mediaType"` // movie|series
	Title     string `json:"title"`
	Year      int    `json:"year"`
	PosterURL string `json:"posterUrl"`
	Overview  string `json:"overview"`
}

// Search runs a multi-search (movies + tv) and maps the hits.
func (c *Client) Search(ctx context.Context, q string) ([]Result, error) {
	if !c.Enabled() || strings.TrimSpace(q) == "" {
		return nil, nil
	}
	u := "https://api.themoviedb.org/3/search/multi?" + url.Values{
		"query":         {q},
		"language":      {c.Language},
		"include_adult": {"false"},
	}.Encode()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	// TMDB v4 read tokens are Bearer; v3 keys go in ?api_key=. Support both.
	if strings.HasPrefix(c.APIKey, "ey") || len(c.APIKey) > 40 {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	} else {
		req.URL.RawQuery += "&api_key=" + url.QueryEscape(c.APIKey)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tmdb search %d", resp.StatusCode)
	}
	var raw struct {
		Results []struct {
			ID           int64  `json:"id"`
			MediaType    string `json:"media_type"`
			Title        string `json:"title"`
			Name         string `json:"name"`
			ReleaseDate  string `json:"release_date"`
			FirstAirDate string `json:"first_air_date"`
			PosterPath   string `json:"poster_path"`
			Overview     string `json:"overview"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	var out []Result
	for _, r := range raw.Results {
		if r.MediaType != "movie" && r.MediaType != "tv" {
			continue
		}
		title := r.Title
		date := r.ReleaseDate
		mt := "movie"
		if r.MediaType == "tv" {
			title, date, mt = r.Name, r.FirstAirDate, "series"
		}
		poster := ""
		if r.PosterPath != "" {
			poster = "https://image.tmdb.org/t/p/w342" + r.PosterPath
		}
		out = append(out, Result{
			TMDBID: r.ID, MediaType: mt, Title: title, Year: yearOf(date),
			PosterURL: poster, Overview: r.Overview,
		})
	}
	return out, nil
}

func yearOf(date string) int {
	if len(date) >= 4 {
		y := 0
		fmt.Sscanf(date[:4], "%d", &y)
		return y
	}
	return 0
}
