// Package opensubtitles is acquire's client for the OpenSubtitles.com REST API
// (v1): a sign-in that is kept, searches by a title's identities and its
// file's hash, and downloads, each of which the account's daily quota counts.
//
// Every request carries the API key and a User-Agent naming acquire and its
// version, as the API asks, and the client keeps to its rate limits: one
// request at a time, at least a second apart, waiting out a 429 as its
// Retry-After says. A search's parameters are sent sorted and in lower case,
// as the API wants them so it does not redirect.
package opensubtitles

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultBaseURL is the API's address.
const DefaultBaseURL = "https://api.opensubtitles.com/api/v1"

// Config is what the client needs: the account and the API key (all three, or
// the client is off), and the User-Agent ("acquire v0.5.0").
type Config struct {
	BaseURL   string
	APIKey    string
	Username  string
	Password  string
	UserAgent string
	// MinInterval is the least time between two requests; 0 is a second.
	MinInterval time.Duration
	// HTTPClient sends the requests; nil is one with a 30 s timeout.
	HTTPClient *http.Client
}

// Client talks to the API. It is safe for concurrent use: requests go one at a
// time.
type Client struct {
	cfg  Config
	http *http.Client

	mu      sync.Mutex // one request at a time, and the pace between them
	last    time.Time
	token   string
	tokenAt time.Time
	base    string // the address the sign-in named for the account, else cfg.BaseURL
	user    User
	quota   Quota
}

// New is a client for cfg.
func New(cfg Config) *Client {
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultBaseURL
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	if cfg.MinInterval == 0 {
		cfg.MinInterval = time.Second
	}
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{cfg: cfg, http: hc, base: cfg.BaseURL}
}

// Enabled reports whether the client has an account and a key to work with.
func (c *Client) Enabled() bool {
	return c != nil && c.cfg.APIKey != "" && c.cfg.Username != "" && c.cfg.Password != ""
}

// Missing names what the configuration lacks, for a person: "" when nothing.
func (c *Client) Missing() []string {
	var out []string
	if c == nil {
		return []string{"apiKey", "username", "password"}
	}
	if c.cfg.APIKey == "" {
		out = append(out, "apiKey")
	}
	if c.cfg.Username == "" {
		out = append(out, "username")
	}
	if c.cfg.Password == "" {
		out = append(out, "password")
	}
	return out
}

// User is what the sign-in says of the account.
type User struct {
	AllowedDownloads int    `json:"allowed_downloads"`
	Level            string `json:"level"`
	VIP              bool   `json:"vip"`
}

// Quota is what the API last said of the day's downloads: how many are left,
// and when the count starts over. Known is false until it has said.
type Quota struct {
	Known     bool      `json:"known"`
	Allowed   int       `json:"allowed"`
	Remaining int       `json:"remaining"`
	ResetAt   time.Time `json:"resetAt"`
	At        time.Time `json:"at"`
}

// Error is an answer of the API other than success.
type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string {
	switch {
	case e.Status == http.StatusUnauthorized:
		return "OpenSubtitles refused the sign-in (HTTP 401): check the username and password"
	case e.Status == http.StatusForbidden:
		return "OpenSubtitles refused the API key (HTTP 403)"
	case e.Status == http.StatusTooManyRequests:
		return "OpenSubtitles asks to slow down (HTTP 429)"
	case e.Status >= 500:
		return fmt.Sprintf("OpenSubtitles did not answer (HTTP %d)", e.Status)
	case e.Message != "":
		return fmt.Sprintf("OpenSubtitles: %s (HTTP %d)", e.Message, e.Status)
	}
	return fmt.Sprintf("OpenSubtitles answered HTTP %d", e.Status)
}

// QuotaError is a download the account's quota refused: none left until
// ResetAt.
type QuotaError struct {
	ResetAt time.Time
	Message string
}

func (e *QuotaError) Error() string {
	if e.ResetAt.IsZero() {
		return "the account has no downloads left today"
	}
	return "the account has no downloads left until " + e.ResetAt.UTC().Format("2006-01-02 15:04 UTC")
}

// ErrDisabled is any call of a client without an account and a key.
var ErrDisabled = errors.New("OpenSubtitles is not configured: subtitles.opensubtitles.username, password and apiKey are needed")

// Login signs in and keeps the token; it answers what the account is. A token
// is kept for a day and asked for again when the API refuses it.
func (c *Client) Login(ctx context.Context) (User, error) {
	if !c.Enabled() {
		return User{}, ErrDisabled
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.login(ctx)
}

func (c *Client) login(ctx context.Context) (User, error) {
	body, _ := json.Marshal(map[string]string{"username": c.cfg.Username, "password": c.cfg.Password})
	var out struct {
		User    User   `json:"user"`
		BaseURL string `json:"base_url"`
		Token   string `json:"token"`
	}
	if err := c.do(ctx, http.MethodPost, c.cfg.BaseURL+"/login", body, false, &out); err != nil {
		return User{}, err
	}
	if out.Token == "" {
		return User{}, errors.New("OpenSubtitles answered the sign-in without a token")
	}
	c.token, c.tokenAt, c.user = out.Token, time.Now(), out.User
	c.base = c.cfg.BaseURL
	if host := strings.TrimSpace(out.BaseURL); host != "" && !strings.ContainsAny(host, "/?#@") {
		// the address the account is served at (another one for some accounts)
		if u, err := url.Parse(c.cfg.BaseURL); err == nil && u.Host != host {
			c.base = u.Scheme + "://" + host + u.Path
		}
	}
	if !c.quota.Known && out.User.AllowedDownloads > 0 {
		c.quota = Quota{Known: false, Allowed: out.User.AllowedDownloads}
	}
	return out.User, nil
}

// signedIn signs in when the client holds no token, or one older than a day.
func (c *Client) signedIn(ctx context.Context) error {
	if c.token != "" && time.Since(c.tokenAt) < 23*time.Hour {
		return nil
	}
	_, err := c.login(ctx)
	return err
}

// Account is the account as the API says it now: what it is, and the day's
// downloads (GET /infos/user).
func (c *Client) Account(ctx context.Context) (User, Quota, error) {
	if !c.Enabled() {
		return User{}, Quota{}, ErrDisabled
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.signedIn(ctx); err != nil {
		return User{}, Quota{}, err
	}
	var out struct {
		Data struct {
			User
			DownloadsCount     int `json:"downloads_count"`
			RemainingDownloads int `json:"remaining_downloads"`
		} `json:"data"`
	}
	if err := c.authed(ctx, http.MethodGet, "/infos/user", nil, &out); err != nil {
		return User{}, Quota{}, err
	}
	c.user = out.Data.User
	reset := c.quota.ResetAt // a download named it; one past is another day's
	if !reset.After(time.Now()) {
		reset = time.Time{}
	}
	c.quota = Quota{Known: true, Allowed: out.Data.AllowedDownloads, Remaining: out.Data.RemainingDownloads,
		ResetAt: reset, At: time.Now()}
	return c.user, c.quota, nil
}

// LastQuota is what the API last said of the day's downloads, without asking.
func (c *Client) LastQuota() Quota {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.quota
}

// Query is a search for a title's subtitles in some languages: by its
// identities (a movie's own, an episode's series' with its numbers), its
// file's hash, else its name.
type Query struct {
	Languages []string // the API's codes: en, de, zh-cn, zh-tw, pt-br
	MovieHash string   // 16 hex digits
	IMDbID    string   // the movie's (tt and digits, or digits)
	TMDBID    int64
	// An episode: its series' identities and its numbers.
	ParentIMDbID string
	ParentTMDBID int64
	Season       int
	Episode      int
	// Text is the title's name, for a title without identities.
	Text string
	Year int
	Type string // movie or episode
	Page int
}

// Values are the query's parameters, as the API wants them: lower case, ids
// without their tt and leading zeros, sorted (url.Values encodes them so).
func (q Query) Values() url.Values {
	v := url.Values{}
	if len(q.Languages) > 0 {
		langs := make([]string, 0, len(q.Languages))
		for _, l := range q.Languages {
			if l = strings.ToLower(strings.TrimSpace(l)); l != "" && !slices.Contains(langs, l) {
				langs = append(langs, l)
			}
		}
		slices.Sort(langs)
		v.Set("languages", strings.Join(langs, ","))
	}
	if h := strings.ToLower(strings.TrimSpace(q.MovieHash)); h != "" {
		v.Set("moviehash", h)
	}
	if id := numericID(q.IMDbID); id != "" {
		v.Set("imdb_id", id)
	}
	if q.TMDBID > 0 {
		v.Set("tmdb_id", strconv.FormatInt(q.TMDBID, 10))
	}
	if id := numericID(q.ParentIMDbID); id != "" {
		v.Set("parent_imdb_id", id)
	}
	if q.ParentTMDBID > 0 {
		v.Set("parent_tmdb_id", strconv.FormatInt(q.ParentTMDBID, 10))
	}
	if q.Season > 0 || q.Episode > 0 {
		v.Set("season_number", strconv.Itoa(q.Season))
		v.Set("episode_number", strconv.Itoa(q.Episode))
	}
	if t := strings.ToLower(strings.TrimSpace(q.Text)); t != "" {
		v.Set("query", t)
		if q.Year > 0 {
			v.Set("year", strconv.Itoa(q.Year))
		}
	}
	if q.Type != "" {
		v.Set("type", strings.ToLower(q.Type))
	}
	// shown flagged, never taken on their own (the caller decides); a full
	// subtitle is asked for, not one of the foreign parts only
	v.Set("ai_translated", "include")
	v.Set("machine_translated", "include")
	v.Set("foreign_parts_only", "exclude")
	if q.Page > 1 {
		v.Set("page", strconv.Itoa(q.Page))
	}
	return v
}

// numericID is an id as the API takes it: its digits, without tt and without
// leading zeros; "" for none.
func numericID(id string) string {
	id = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(id)), "tt")
	id = strings.TrimLeft(id, "0")
	for _, r := range id {
		if r < '0' || r > '9' {
			return ""
		}
	}
	return id
}

// Result is one subtitle the API found.
type Result struct {
	SubtitleID        string    `json:"subtitleId"`
	FileID            int64     `json:"fileId"`
	Files             int       `json:"files"`
	FileName          string    `json:"fileName"`
	Language          string    `json:"language"`
	Release           string    `json:"release"`
	Downloads         int       `json:"downloads"`
	Rating            float64   `json:"rating"`
	Votes             int       `json:"votes"`
	HearingImpaired   bool      `json:"hearingImpaired"`
	ForeignPartsOnly  bool      `json:"foreignPartsOnly"`
	AITranslated      bool      `json:"aiTranslated"`
	MachineTranslated bool      `json:"machineTranslated"`
	FromTrusted       bool      `json:"fromTrusted"`
	HashMatch         bool      `json:"hashMatch"`
	FPS               float64   `json:"fps"`
	Uploaded          time.Time `json:"uploaded"`
	// What the subtitle was made for, as the API says.
	FeatureType  string `json:"featureType"`
	IMDbID       int64  `json:"imdbId"`
	ParentIMDbID int64  `json:"parentImdbId"`
	Season       int    `json:"season"`
	Episode      int    `json:"episode"`
}

// Page is one page of a search.
type Page struct {
	Results    []Result
	Page       int
	TotalPages int
	Total      int
}

type apiSubtitle struct {
	Attributes struct {
		SubtitleID        string  `json:"subtitle_id"`
		Language          string  `json:"language"`
		DownloadCount     int     `json:"download_count"`
		HearingImpaired   bool    `json:"hearing_impaired"`
		FPS               float64 `json:"fps"`
		Votes             int     `json:"votes"`
		Ratings           float64 `json:"ratings"`
		FromTrusted       bool    `json:"from_trusted"`
		ForeignPartsOnly  bool    `json:"foreign_parts_only"`
		UploadDate        string  `json:"upload_date"`
		AITranslated      bool    `json:"ai_translated"`
		MachineTranslated bool    `json:"machine_translated"`
		Release           string  `json:"release"`
		MoviehashMatch    bool    `json:"moviehash_match"`
		FeatureDetails    struct {
			FeatureType   string `json:"feature_type"`
			IMDbID        int64  `json:"imdb_id"`
			ParentIMDbID  int64  `json:"parent_imdb_id"`
			SeasonNumber  int    `json:"season_number"`
			EpisodeNumber int    `json:"episode_number"`
		} `json:"feature_details"`
		Files []struct {
			FileID   int64  `json:"file_id"`
			FileName string `json:"file_name"`
		} `json:"files"`
	} `json:"attributes"`
}

// Search asks for one page of subtitles. A search costs no download, and
// needs no sign-in: the key is enough (the token goes along once there is
// one).
func (c *Client) Search(ctx context.Context, q Query) (Page, error) {
	if !c.Enabled() {
		return Page{}, ErrDisabled
	}
	var out struct {
		TotalPages int           `json:"total_pages"`
		TotalCount int           `json:"total_count"`
		Page       int           `json:"page"`
		Data       []apiSubtitle `json:"data"`
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.do(ctx, http.MethodGet, c.base+"/subtitles?"+q.Values().Encode(), nil, c.token != "", &out); err != nil {
		var ae *Error
		if !errors.As(err, &ae) || ae.Status != http.StatusUnauthorized || c.token == "" {
			return Page{}, err
		}
		c.token = "" // a token the API no longer takes: the key alone
		if err := c.do(ctx, http.MethodGet, c.base+"/subtitles?"+q.Values().Encode(), nil, false, &out); err != nil {
			return Page{}, err
		}
	}
	p := Page{Page: out.Page, TotalPages: out.TotalPages, Total: out.TotalCount}
	for _, d := range out.Data {
		a := d.Attributes
		r := Result{SubtitleID: a.SubtitleID, Language: strings.ToLower(a.Language), Release: strings.TrimSpace(a.Release),
			Downloads: a.DownloadCount, Rating: a.Ratings, Votes: a.Votes, HearingImpaired: a.HearingImpaired,
			ForeignPartsOnly: a.ForeignPartsOnly, AITranslated: a.AITranslated, MachineTranslated: a.MachineTranslated,
			FromTrusted: a.FromTrusted, HashMatch: a.MoviehashMatch, FPS: a.FPS, Files: len(a.Files),
			FeatureType: strings.ToLower(a.FeatureDetails.FeatureType), IMDbID: a.FeatureDetails.IMDbID,
			ParentIMDbID: a.FeatureDetails.ParentIMDbID, Season: a.FeatureDetails.SeasonNumber, Episode: a.FeatureDetails.EpisodeNumber}
		if t, err := time.Parse(time.RFC3339, a.UploadDate); err == nil {
			r.Uploaded = t
		}
		if len(a.Files) > 0 {
			r.FileID, r.FileName = a.Files[0].FileID, a.Files[0].FileName
		}
		if r.FileID == 0 {
			continue
		}
		p.Results = append(p.Results, r)
	}
	return p, nil
}

// Link is a file a download made available: where to fetch it, and what the
// quota is now.
type Link struct {
	URL      string
	FileName string
	Quota    Quota
}

// MaxFileBytes is the most a subtitle file may hold that Fetch reads.
const MaxFileBytes = 8 << 20

// Download asks for the file fileID as SubRip and answers its link and the
// day's quota. It counts against the account's downloads: a refused one is a
// QuotaError.
func (c *Client) Download(ctx context.Context, fileID int64) (Link, error) {
	if !c.Enabled() {
		return Link{}, ErrDisabled
	}
	body, _ := json.Marshal(map[string]any{"file_id": fileID, "sub_format": "srt"})
	var out struct {
		Link         string `json:"link"`
		FileName     string `json:"file_name"`
		Requests     int    `json:"requests"`
		Remaining    int    `json:"remaining"`
		Message      string `json:"message"`
		ResetTimeUTC string `json:"reset_time_utc"`
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.signedIn(ctx); err != nil {
		return Link{}, err
	}
	err := c.authed(ctx, http.MethodPost, "/download", body, &out)
	reset, _ := time.Parse(time.RFC3339, out.ResetTimeUTC)
	var ae *Error
	if errors.As(err, &ae) && ae.Status == http.StatusNotAcceptable {
		c.quota = Quota{Known: true, Allowed: c.user.AllowedDownloads, Remaining: 0, ResetAt: reset, At: time.Now()}
		return Link{}, &QuotaError{ResetAt: reset, Message: ae.Message}
	}
	if err != nil {
		return Link{}, err
	}
	if out.Link == "" {
		return Link{}, errors.New("OpenSubtitles answered the download without a link")
	}
	remaining := out.Remaining
	if remaining < 0 {
		remaining = 0
	}
	c.quota = Quota{Known: true, Allowed: c.user.AllowedDownloads, Remaining: remaining, ResetAt: reset, At: time.Now()}
	return Link{URL: out.Link, FileName: out.FileName, Quota: c.quota}, nil
}

// FileError is a subtitle file that is not taken, however often it is asked
// for: its link is no https address, or it is too large.
type FileError struct {
	Reason string
}

func (e *FileError) Error() string { return e.Reason }

// Fetch reads the file a download's link names, at most MaxFileBytes. A
// FileError for a file that is never taken; an Error for a link answered
// other than with the file.
func (c *Client) Fetch(ctx context.Context, link string) ([]byte, error) {
	u, err := url.Parse(link)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return nil, &FileError{Reason: "the download link isn't an https address"}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.cfg.UserAgent)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("the subtitle file could not be fetched: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, &Error{Status: resp.StatusCode, Message: "the subtitle file could not be fetched"}
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, MaxFileBytes+1))
	if err != nil {
		return nil, fmt.Errorf("the subtitle file could not be read: %w", err)
	}
	if len(b) > MaxFileBytes {
		return nil, &FileError{Reason: "the subtitle file is larger than 8 MiB"}
	}
	return b, nil
}

// authed does a request with the sign-in's token, signing in again once when
// the API refuses it.
func (c *Client) authed(ctx context.Context, method, path string, body []byte, into any) error {
	err := c.do(ctx, method, c.base+path, body, true, into)
	var ae *Error
	if errors.As(err, &ae) && ae.Status == http.StatusUnauthorized {
		if _, lerr := c.login(ctx); lerr != nil {
			return lerr
		}
		err = c.do(ctx, method, c.base+path, body, true, into)
	}
	return err
}

// maxRetryAfter is the longest a 429's Retry-After is waited out.
const maxRetryAfter = 30 * time.Second

// do sends one request, at least MinInterval after the last, and waits out a
// 429 once. The caller holds c.mu.
func (c *Client) do(ctx context.Context, method, target string, body []byte, auth bool, into any) error {
	for attempt := 0; ; attempt++ {
		if wait := c.cfg.MinInterval - time.Since(c.last); wait > 0 {
			if err := sleep(ctx, wait); err != nil {
				return err
			}
		}
		req, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Api-Key", c.cfg.APIKey)
		req.Header.Set("User-Agent", c.cfg.UserAgent)
		req.Header.Set("Accept", "application/json")
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if auth && c.token != "" {
			req.Header.Set("Authorization", "Bearer "+c.token)
		}
		resp, err := c.http.Do(req)
		c.last = time.Now()
		if err != nil {
			return fmt.Errorf("OpenSubtitles could not be reached: %w", err)
		}
		raw, rerr := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		resp.Body.Close()
		if rerr != nil {
			return fmt.Errorf("OpenSubtitles' answer could not be read: %w", rerr)
		}
		if resp.StatusCode == http.StatusTooManyRequests && attempt == 0 {
			wait := retryAfter(resp.Header.Get("Retry-After"))
			if err := sleep(ctx, wait); err != nil {
				return err
			}
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			e := &Error{Status: resp.StatusCode, Message: messageOf(raw)}
			if into != nil && resp.StatusCode == http.StatusNotAcceptable {
				_ = json.Unmarshal(raw, into) // the quota's reset time
			}
			return e
		}
		if pause := pauseOf(resp.Header); pause > 0 {
			c.last = c.last.Add(pause)
		}
		if into == nil {
			return nil
		}
		if err := json.Unmarshal(raw, into); err != nil {
			return fmt.Errorf("OpenSubtitles' answer is no JSON it was asked for: %w", err)
		}
		return nil
	}
}

// retryAfter is how long a 429 asks to wait: its seconds, at least one and at
// most maxRetryAfter.
func retryAfter(v string) time.Duration {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	switch {
	case err != nil || n < 1:
		return time.Second
	case time.Duration(n)*time.Second > maxRetryAfter:
		return maxRetryAfter
	}
	return time.Duration(n) * time.Second
}

// pauseOf is how long the next request waits after an answer that says none
// are left in its window (ratelimit-remaining 0, ratelimit-reset seconds).
func pauseOf(h http.Header) time.Duration {
	remaining := h.Get("Ratelimit-Remaining")
	if remaining == "" {
		remaining = h.Get("X-Ratelimit-Remaining-Second")
	}
	if strings.TrimSpace(remaining) != "0" {
		return 0
	}
	if n, err := strconv.Atoi(strings.TrimSpace(h.Get("Ratelimit-Reset"))); err == nil && n > 0 && n <= 10 {
		return time.Duration(n) * time.Second
	}
	return time.Second
}

// messageOf is the message of an error answer: its message, or its errors.
func messageOf(raw []byte) string {
	var body struct {
		Message string   `json:"message"`
		Errors  []string `json:"errors"`
	}
	if json.Unmarshal(raw, &body) == nil {
		if body.Message != "" {
			return strings.TrimSpace(body.Message)
		}
		if len(body.Errors) > 0 {
			return strings.Join(body.Errors, "; ")
		}
	}
	s := strings.TrimSpace(string(raw))
	if len(s) > 200 || strings.HasPrefix(s, "<") {
		return ""
	}
	return s
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
