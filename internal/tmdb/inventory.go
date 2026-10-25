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

// Running reports whether TMDB's status of a series says episodes are still
// to come: a returning series, one in production, or one planned. Ended,
// Canceled and a lone Pilot are not.
func Running(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "returning series", "in production", "planned":
		return true
	}
	return false
}

// Series is a series as TMDB's /tv/{id} answers it in one call: its
// identities in the id spaces sources accept, its name and other titles, its
// status, and its seasons with their episode counts. Specials (season 0) are
// left out, as in the inventory.
type Series struct {
	TMDBID       int64
	TVDBID       int64
	IMDBID       string
	Name         string
	Status       string // Returning Series | In Production | Planned | Ended | Canceled | Pilot
	FirstAirDate *time.Time
	Aliases      []string
	Seasons      []Season
}

// Year is the year the series first aired, or 0.
func (s Series) Year() int {
	if s.FirstAirDate == nil {
		return 0
	}
	return s.FirstAirDate.Year()
}

// Season is one season as a series' answer lists it.
type Season struct {
	Number       int
	EpisodeCount int
	AirDate      *time.Time
}

// HTTPError is an answer of TMDB that is not a 200. NotFound tells a title
// TMDB does not know from TMDB not answering.
type HTTPError struct {
	Path   string
	Status int
	Body   string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("tmdb %s: http %d: %s", e.Path, e.Status, e.Body)
}

// NotFound reports whether TMDB answered that it does not know the title.
func (e *HTTPError) NotFound() bool { return e.Status == http.StatusNotFound }

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
		return nil, &HTTPError{Path: path, Status: resp.StatusCode,
			Body: strings.TrimSpace(string(body[:min(len(body), 200)]))}
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
	head, err := c.Series(ctx, tmdbID)
	if err != nil {
		return in, err
	}
	in = Inventory{
		TMDBID: head.TMDBID, TVDBID: head.TVDBID, IMDBID: head.IMDBID,
		Name: head.Name, Status: head.Status, Aliases: head.Aliases, SeasonInfo: map[int]int{},
	}
	for _, s := range head.Seasons {
		in.SeasonInfo[s.Number] = s.EpisodeCount
		eps, err := c.Season(ctx, tmdbID, s.Number)
		if err != nil {
			// One unreadable season must not silently shrink the inventory —
			// that is how a fetch bug becomes a fake "missing episodes" result.
			return in, fmt.Errorf("tmdb tv/%d season %d: %w", tmdbID, s.Number, err)
		}
		in.Episodes = append(in.Episodes, eps...)
	}
	return in, nil
}

// Series reads a series' answer: one call, with its external ids and other
// titles appended. Its seasons' episodes are Season's to read.
func (c *Client) Series(ctx context.Context, tmdbID int64) (Series, error) {
	var out Series
	if !c.Enabled() {
		return out, fmt.Errorf("tmdb not configured")
	}
	body, err := c.get(ctx, "/tv/"+strconv.FormatInt(tmdbID, 10),
		url.Values{"append_to_response": {"external_ids,alternative_titles"}})
	if err != nil {
		return out, err
	}
	var head struct {
		ID           int64  `json:"id"`
		Name         string `json:"name"`
		Status       string `json:"status"`
		FirstAirDate string `json:"first_air_date"`
		ExternalIDs  struct {
			TVDBID int64  `json:"tvdb_id"`
			IMDBID string `json:"imdb_id"`
		} `json:"external_ids"`
		AlternativeTitles struct {
			Results []struct {
				Title string `json:"title"`
			} `json:"results"`
		} `json:"alternative_titles"`
		Seasons []struct {
			SeasonNumber int    `json:"season_number"`
			EpisodeCount int    `json:"episode_count"`
			AirDate      string `json:"air_date"`
		} `json:"seasons"`
	}
	if err := json.Unmarshal(body, &head); err != nil {
		return out, fmt.Errorf("tmdb tv/%d: decode: %w", tmdbID, err)
	}
	out = Series{
		TMDBID: head.ID, TVDBID: head.ExternalIDs.TVDBID, IMDBID: strings.TrimSpace(head.ExternalIDs.IMDBID),
		Name: strings.TrimSpace(head.Name), Status: strings.TrimSpace(head.Status), FirstAirDate: date(head.FirstAirDate),
	}
	out.Aliases = otherTitles(out.Name, func(yield func(string)) {
		for _, a := range head.AlternativeTitles.Results {
			yield(a.Title)
		}
	})
	for _, s := range head.Seasons {
		if s.SeasonNumber < 1 {
			continue // specials; see SeriesInventory
		}
		out.Seasons = append(out.Seasons, Season{Number: s.SeasonNumber, EpisodeCount: s.EpisodeCount, AirDate: date(s.AirDate)})
	}
	return out, nil
}

// otherTitles are a title's other names, each once, without its own name.
func otherTitles(name string, each func(yield func(string))) []string {
	seen := map[string]bool{strings.ToLower(name): true}
	var out []string
	each(func(t string) {
		t = strings.TrimSpace(t)
		if k := strings.ToLower(t); t != "" && !seen[k] {
			seen[k] = true
			out = append(out, t)
		}
	})
	return out
}

// date reads one of TMDB's dates (2006-01-02), nil when it names none.
func date(s string) *time.Time {
	if s = strings.TrimSpace(s); s == "" {
		return nil
	}
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		return nil
	}
	return &d
}

// Season reads the episodes of one season of a series: one call.
func (c *Client) Season(ctx context.Context, tmdbID int64, season int) ([]Episode, error) {
	if !c.Enabled() {
		return nil, fmt.Errorf("tmdb not configured")
	}
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
