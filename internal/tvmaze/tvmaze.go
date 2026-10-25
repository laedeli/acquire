// Package tvmaze reads exact air times from TVmaze's public API, which needs
// no key: a show is looked up by its TVDB id or its IMDb id, then its episodes
// are read, each with its airstamp — the moment it airs, with its offset.
// Only those ids leave acquire, never anything of its users or its library.
//
// TVmaze allows at least 20 calls every 10 seconds from one address; the
// client spaces its calls under that, and waits out a 429 before it asks
// again.
package tvmaze

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultURL is TVmaze's API.
const DefaultURL = "https://api.tvmaze.com"

// DefaultEvery is the least time between two calls: under 19 every 10
// seconds.
const DefaultEvery = 550 * time.Millisecond

// maxBody bounds one answer: the episodes of the longest shows.
const maxBody = 16 << 20

// ErrNotFound: TVmaze does not know the show by the ids it was asked by.
var ErrNotFound = errors.New("TVmaze does not know the show")

// HTTPError is an answer that is neither what was asked for nor "not
// found".
type HTTPError struct {
	Status int
	Path   string
}

func (e *HTTPError) Error() string { return fmt.Sprintf("TVmaze %s: HTTP %d", e.Path, e.Status) }

// Unavailable reports whether err is TVmaze not answering as it should — out
// of reach, failing, limiting —, not the show's fault: no other show would
// fare better now.
func Unavailable(err error) bool {
	var he *HTTPError
	if errors.As(err, &he) {
		return he.Status == http.StatusTooManyRequests || he.Status >= 500
	}
	return err != nil && !errors.Is(err, ErrNotFound)
}

// Client asks TVmaze. Its zero value is off (Enabled).
type Client struct {
	BaseURL   string
	HTTP      *http.Client
	UserAgent string
	// Every is the least time between two calls (DefaultEvery when 0).
	Every time.Duration
	// RetryWait is how long a 429 without Retry-After is waited out (10 s
	// when 0).
	RetryWait time.Duration

	mu   sync.Mutex
	next time.Time
}

// New is a client of the API at base.
func New(base, userAgent string) *Client {
	return &Client{BaseURL: strings.TrimRight(base, "/"), HTTP: &http.Client{Timeout: 20 * time.Second}, UserAgent: userAgent}
}

// Enabled reports whether the client asks anything at all.
func (c *Client) Enabled() bool { return c != nil && strings.TrimSpace(c.BaseURL) != "" }

// Episode is one episode as TVmaze lists it: its id, its numbers, its local
// air date, and when it airs — only when TVmaze knows the hour, not just the
// day.
type Episode struct {
	ID       int64
	Season   int
	Number   int
	AirDate  *time.Time
	AirStamp *time.Time
}

// LookupShow finds a show's TVmaze id by its TVDB id, else by its IMDb id.
// ErrNotFound when TVmaze knows it by neither.
func (c *Client) LookupShow(ctx context.Context, tvdb int64, imdb string) (int64, error) {
	var tries []url.Values
	if tvdb > 0 {
		tries = append(tries, url.Values{"thetvdb": {strconv.FormatInt(tvdb, 10)}})
	}
	if imdb = strings.TrimSpace(imdb); imdb != "" {
		tries = append(tries, url.Values{"imdb": {imdb}})
	}
	for _, q := range tries {
		id, err := c.lookup(ctx, q)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		return id, err
	}
	return 0, ErrNotFound
}

// lookup asks /lookup/shows once. TVmaze answers a show it knows with a
// redirect to it: the id is read off the redirect, which is not followed —
// one call, not two.
func (c *Client) lookup(ctx context.Context, q url.Values) (int64, error) {
	resp, err := c.do(ctx, "/lookup/shows", q, false)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return 0, ErrNotFound
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		loc, err := url.Parse(resp.Header.Get("Location"))
		if err != nil {
			return 0, fmt.Errorf("TVmaze lookup: a redirect to %q", resp.Header.Get("Location"))
		}
		id, err := strconv.ParseInt(path.Base(loc.Path), 10, 64)
		if err != nil || id <= 0 || path.Base(path.Dir(loc.Path)) != "shows" {
			return 0, fmt.Errorf("TVmaze lookup: a redirect to %q", loc.Path)
		}
		return id, nil
	case resp.StatusCode == http.StatusOK:
		var show struct {
			ID int64 `json:"id"`
		}
		if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&show); err != nil || show.ID <= 0 {
			return 0, fmt.Errorf("TVmaze lookup: an answer that names no show")
		}
		return show.ID, nil
	}
	return 0, &HTTPError{Status: resp.StatusCode, Path: "/lookup/shows"}
}

// Episodes reads a show's episodes. Specials, which TVmaze numbers in no
// season, are left out.
func (c *Client) Episodes(ctx context.Context, show int64) ([]Episode, error) {
	p := "/shows/" + strconv.FormatInt(show, 10) + "/episodes"
	resp, err := c.do(ctx, p, nil, true)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return nil, ErrNotFound
	default:
		return nil, &HTTPError{Status: resp.StatusCode, Path: p}
	}
	var raw []struct {
		ID       int64  `json:"id"`
		Season   int    `json:"season"`
		Number   *int   `json:"number"`
		AirDate  string `json:"airdate"`
		AirTime  string `json:"airtime"`
		AirStamp string `json:"airstamp"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&raw); err != nil {
		return nil, fmt.Errorf("TVmaze %s: decode: %w", p, err)
	}
	out := make([]Episode, 0, len(raw))
	for _, e := range raw {
		if e.Number == nil || *e.Number < 1 || e.Season < 1 {
			continue
		}
		ep := Episode{ID: e.ID, Season: e.Season, Number: *e.Number}
		if d, err := time.Parse("2006-01-02", strings.TrimSpace(e.AirDate)); err == nil {
			ep.AirDate = &d
		}
		// An airstamp without an hour is a day, not the moment it airs.
		if strings.TrimSpace(e.AirTime) != "" {
			if at, err := time.Parse(time.RFC3339, strings.TrimSpace(e.AirStamp)); err == nil {
				at = at.UTC()
				ep.AirStamp = &at
			}
		}
		out = append(out, ep)
	}
	return out, nil
}

// do sends one GET, spaced from the last (Every), and waits out a 429 — up
// to twice — before it gives up.
func (c *Client) do(ctx context.Context, p string, q url.Values, follow bool) (*http.Response, error) {
	if !c.Enabled() {
		return nil, errors.New("TVmaze is not configured")
	}
	u := strings.TrimRight(c.BaseURL, "/") + p
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	hc := http.DefaultClient
	if c.HTTP != nil {
		hc = c.HTTP
	}
	if !follow {
		cp := *hc
		cp.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		hc = &cp
	}
	for attempt := 0; ; attempt++ {
		if err := c.wait(ctx, 0); err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/json")
		if c.UserAgent != "" {
			req.Header.Set("User-Agent", c.UserAgent)
		}
		resp, err := hc.Do(req)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusTooManyRequests || attempt == 2 {
			return resp, nil
		}
		after := c.RetryWait
		if after <= 0 {
			after = 10 * time.Second
		}
		if s, err := strconv.Atoi(strings.TrimSpace(resp.Header.Get("Retry-After"))); err == nil && s >= 0 {
			after = min(time.Duration(s)*time.Second, 30*time.Second)
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		resp.Body.Close()
		if err := c.wait(ctx, after); err != nil {
			return nil, err
		}
	}
}

// wait holds a call until its turn: Every after the last, and — after a 429
// — at least extra from now, for every call that follows.
func (c *Client) wait(ctx context.Context, extra time.Duration) error {
	every := c.Every
	if every <= 0 {
		every = DefaultEvery
	}
	c.mu.Lock()
	now := time.Now()
	at := c.next
	if at.Before(now) {
		at = now
	}
	if extra > 0 && at.Before(now.Add(extra)) {
		at = now.Add(extra)
	}
	c.next = at
	if extra == 0 {
		c.next = at.Add(every)
	}
	c.mu.Unlock()
	d := time.Until(at)
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
