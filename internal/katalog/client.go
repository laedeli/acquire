// Package katalog is acquire's client for the neutral catalog seams: create an
// item from a staged file via katalog-manager's POST /api/ingest (which emits
// discovered → the pipeline), and a best-effort in-library check via katalog-api.
package katalog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Tokener yields a bearer for service-to-service calls (client-credentials).
type Tokener interface {
	Token(ctx context.Context) (string, error)
	Enabled() bool
}

type Client struct {
	ManagerURL string // katalog-manager (write: /api/ingest)
	KatalogURL string // katalog-api (read: in-library)
	Tokens     Tokener
	HTTP       *http.Client
}

func New(managerURL, katalogURL string, tokens Tokener) *Client {
	return &Client{
		ManagerURL: strings.TrimRight(managerURL, "/"),
		KatalogURL: strings.TrimRight(katalogURL, "/"),
		Tokens:     tokens,
		HTTP:       &http.Client{Timeout: 20 * time.Second},
	}
}

// IngestRequest mirrors katalog-manager's POST /api/ingest body.
type IngestRequest struct {
	Path        string  `json:"path"`
	Type        string  `json:"type"`
	Title       string  `json:"title"`
	Year        *int32  `json:"year,omitempty"`
	Description *string `json:"description,omitempty"`
}

type IngestResult struct {
	ItemID  string `json:"itemId"`
	Created bool   `json:"created"`
}

func (c *Client) authHeader(ctx context.Context, req *http.Request) {
	if c.Tokens != nil && c.Tokens.Enabled() {
		if tok, err := c.Tokens.Token(ctx); err == nil && tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
	}
}

// Ingest registers a staged file as a catalog item (item + primary asset +
// discovered event → pipeline). Idempotent on the path server-side.
func (c *Client) Ingest(ctx context.Context, in IngestRequest) (IngestResult, error) {
	if c.ManagerURL == "" {
		return IngestResult{}, fmt.Errorf("katalog-manager URL not configured")
	}
	body, _ := json.Marshal(in)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.ManagerURL+"/api/ingest", bytes.NewReader(body))
	if err != nil {
		return IngestResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	c.authHeader(ctx, req)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return IngestResult{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return IngestResult{}, fmt.Errorf("ingest %d: %s", resp.StatusCode, string(b))
	}
	var out IngestResult
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return IngestResult{}, err
	}
	return out, nil
}

// Availability is what a library check can honestly report. "I could not tell"
// is a THIRD state, distinct from yes and no: collapsing it into "not in
// library" is why a katalog-api that had been returning HTTP 500 for weeks was
// invisible — the discovery UI simply never flagged anything as owned, which
// looks exactly like an empty library.
type Availability int

const (
	NotInLibrary Availability = iota
	InLibraryYes
	AvailabilityUnknown
)

func (a Availability) String() string {
	switch a {
	case InLibraryYes:
		return "in-library"
	case AvailabilityUnknown:
		return "unknown"
	}
	return "not-in-library"
}

// InLibrary reports whether katalog already holds a title. It answers Unknown
// (with a reason) rather than guessing when the catalog cannot be reached or
// refuses the query, so a broken dependency surfaces instead of silently
// degrading into "you own nothing".
func (c *Client) InLibrary(ctx context.Context, title string) (Availability, error) {
	if c.KatalogURL == "" {
		return AvailabilityUnknown, errors.New("katalog url not configured")
	}
	if strings.TrimSpace(title) == "" {
		return NotInLibrary, nil
	}
	u := c.KatalogURL + "/api/v1/items?" + url.Values{"q": {title}, "limit": {"5"}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return AvailabilityUnknown, err
	}
	c.authHeader(ctx, req)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return AvailabilityUnknown, fmt.Errorf("katalog search: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return AvailabilityUnknown, fmt.Errorf("katalog search: http %d: %s",
			resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var raw struct {
		Items []struct {
			Title string `json:"title"`
		} `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return AvailabilityUnknown, fmt.Errorf("katalog search: decode: %w", err)
	}
	for _, it := range raw.Items {
		if strings.EqualFold(strings.TrimSpace(it.Title), strings.TrimSpace(title)) {
			return InLibraryYes, nil
		}
	}
	return NotInLibrary, nil
}

// videoExts are the container extensions the pipeline can ingest.
var videoExts = map[string]bool{".mkv": true, ".mp4": true, ".m4v": true, ".avi": true, ".mov": true, ".ts": true, ".webm": true}

// ResolveVideo turns a download's reported paths into the single video file to
// ingest: if a path is a file with a video extension, use it; if it's a
// directory, pick the LARGEST video file within (skips samples/extras by size).
// Requires the media NFS mounted into acquire. Returns "" when none found.
func ResolveVideo(paths []string) string {
	var best string
	var bestSize int64 = -1
	consider := func(p string, size int64) {
		if videoExts[strings.ToLower(filepath.Ext(p))] && size > bestSize {
			best, bestSize = p, size
		}
	}
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			// Path not directly stat-able (e.g. reported by the client but not
			// mounted here) — fall back to extension-only match.
			if videoExts[strings.ToLower(filepath.Ext(p))] && bestSize < 0 {
				best = p
			}
			continue
		}
		if info.IsDir() {
			_ = filepath.WalkDir(p, func(fp string, d os.DirEntry, err error) error {
				if err != nil || d.IsDir() {
					return nil
				}
				if fi, err := d.Info(); err == nil {
					consider(fp, fi.Size())
				}
				return nil
			})
			continue
		}
		consider(p, info.Size())
	}
	return best
}
