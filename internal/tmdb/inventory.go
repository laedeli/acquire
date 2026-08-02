package tmdb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Episode is one derived WANT row: a coordinate and when it becomes searchable.
type Episode struct {
	SeasonNumber  int
	EpisodeNumber int
	Title         string
	AirDate       *time.Time
	// Absolute is the flat episode number anime releases are usually named
	// with. Zero when TMDB offers no usable ordering.
	Absolute int
}

// Inventory is everything the WANT model needs about one series.
type Inventory struct {
	TMDBID     int64
	TVDBID     int64
	IMDBID     string
	Name       string
	Status     string // Returning Series | Ended | Canceled
	Aliases    []string
	Episodes   []Episode
	SeasonInfo map[int]int // season -> episode count
	// GroupUsed records which TMDB episode group supplied the ordering, so a
	// reconciliation mismatch can be explained rather than guessed at.
	GroupUsed string
}

// Continuing reports whether new episodes should still be expected.
func (in Inventory) Continuing() bool {
	return strings.EqualFold(in.Status, "Returning Series")
}

func (c *Client) get(ctx context.Context, path string, q url.Values) ([]byte, error) {
	if q == nil {
		q = url.Values{}
	}
	if c.Language != "" {
		q.Set("language", c.Language)
	}
	u := c.base() + path + "?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	// TMDB v4 read tokens are Bearer; v3 keys go in ?api_key=. The key in use
	// on beta is a v4 JWT — passing it as ?api_key= returns status_code 7 with
	// an HTTP 401, which reads like a bad key rather than a wrong scheme.
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
	body := make([]byte, 0, 1<<16)
	buf := make([]byte, 32*1024)
	for {
		n, rerr := resp.Body.Read(buf)
		body = append(body, buf[:n]...)
		if rerr != nil {
			break
		}
		if len(body) > 8<<20 {
			return nil, fmt.Errorf("tmdb %s: response too large", path)
		}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tmdb %s: http %d: %s", path, resp.StatusCode,
			strings.TrimSpace(string(body[:min(len(body), 200)])))
	}
	return body, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// SeriesInventory derives the complete episode list for a series.
//
// Season 0 is EXCLUDED. Specials agree with the incumbent's inventory only
// 45.9% of the time, so including them would inject noise into every
// reconciliation and manufacture WANT rows for things nobody tracks. They are a
// later, separate decision.
func (c *Client) SeriesInventory(ctx context.Context, tmdbID int64) (Inventory, error) {
	var in Inventory
	if !c.Enabled() {
		return in, fmt.Errorf("tmdb not configured")
	}
	body, err := c.get(ctx, "/tv/"+strconv.FormatInt(tmdbID, 10),
		url.Values{"append_to_response": {"external_ids,alternative_titles"}})
	if err != nil {
		return in, err
	}
	var head struct {
		ID          int64  `json:"id"`
		Name        string `json:"name"`
		Status      string `json:"status"`
		ExternalIDs struct {
			TVDBID int64  `json:"tvdb_id"`
			IMDBID string `json:"imdb_id"`
		} `json:"external_ids"`
		AlternativeTitles struct {
			Results []struct {
				Title string `json:"title"`
			} `json:"results"`
		} `json:"alternative_titles"`
		Seasons []struct {
			SeasonNumber int `json:"season_number"`
			EpisodeCount int `json:"episode_count"`
		} `json:"seasons"`
	}
	if err := json.Unmarshal(body, &head); err != nil {
		return in, fmt.Errorf("tmdb tv/%d: decode: %w", tmdbID, err)
	}

	in = Inventory{
		TMDBID: head.ID, TVDBID: head.ExternalIDs.TVDBID, IMDBID: head.ExternalIDs.IMDBID,
		Name: head.Name, Status: head.Status, SeasonInfo: map[int]int{},
	}
	for _, a := range head.AlternativeTitles.Results {
		if t := strings.TrimSpace(a.Title); t != "" && !strings.EqualFold(t, head.Name) {
			in.Aliases = append(in.Aliases, t)
		}
	}

	for _, s := range head.Seasons {
		if s.SeasonNumber < 1 {
			continue // specials; see the doc comment
		}
		in.SeasonInfo[s.SeasonNumber] = s.EpisodeCount
		eps, err := c.seasonEpisodes(ctx, tmdbID, s.SeasonNumber)
		if err != nil {
			// One unreadable season must not silently shrink the inventory —
			// that is how a fetch bug becomes a fake "missing episodes" result.
			return in, fmt.Errorf("tmdb tv/%d season %d: %w", tmdbID, s.SeasonNumber, err)
		}
		in.Episodes = append(in.Episodes, eps...)
	}
	return in, nil
}

func (c *Client) seasonEpisodes(ctx context.Context, tmdbID int64, season int) ([]Episode, error) {
	body, err := c.get(ctx,
		fmt.Sprintf("/tv/%d/season/%d", tmdbID, season), nil)
	if err != nil {
		return nil, err
	}
	var raw struct {
		Episodes []struct {
			SeasonNumber  int    `json:"season_number"`
			EpisodeNumber int    `json:"episode_number"`
			Name          string `json:"name"`
			AirDate       string `json:"air_date"`
		} `json:"episodes"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	out := make([]Episode, 0, len(raw.Episodes))
	for _, e := range raw.Episodes {
		ep := Episode{SeasonNumber: e.SeasonNumber, EpisodeNumber: e.EpisodeNumber, Title: e.Name}
		if e.AirDate != "" {
			if d, err := time.Parse("2006-01-02", e.AirDate); err == nil {
				ep.AirDate = &d
			}
		}
		out = append(out, ep)
	}
	return out, nil
}

// Aired reports whether an episode can plausibly have a release yet. An episode
// with no air date is treated as NOT aired: searching for something that may not
// exist spends indexer quota, which the P1 gates showed is the scarce resource —
// three capability sweeps were enough to rate-limit the best indexer into
// failure.
func (e Episode) Aired(now time.Time) bool {
	return e.AirDate != nil && !e.AirDate.After(now)
}
