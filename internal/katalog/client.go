// Package katalog is acquire's client for the neutral catalog seams: create an
// item from a staged file via katalog-manager's POST /api/ingest (which emits
// discovered → the pipeline) — an episode by its series' identity, a movie with
// its own —, what the library holds of a title by identity via katalog-api's
// holdings, and a best-effort in-library check by title.
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
	"regexp"
	"strconv"
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

// Identity names a title by the identities it has outside any one library.
// One is enough. The catalog tries them in the order tmdb, tvdb, imdb, and
// the first that finds a title decides (of a series held twice, the one made
// first); a series none finds it makes from them and the title given (ingest
// by identity, ADR-0012). Every id is a string in its source's form: TMDB and
// TVDB ids digits, an IMDb id tt and digits.
type Identity struct {
	TMDB string `json:"tmdb,omitempty"`
	TVDB string `json:"tvdb,omitempty"`
	IMDB string `json:"imdb,omitempty"`
}

// MaxCovered is the most episodes one file may cover in a row: an ingest
// whose episodeEnd reaches further is refused.
const MaxCovered = 10

var (
	digitsRE = regexp.MustCompile(`^[0-9]{1,20}$`)
	imdbRE   = regexp.MustCompile(`^tt[0-9]{1,20}$`)
)

// NewIdentity is the identity of the ids given that are in their source's
// form — an IMDb id with or without its tt — leaving out the others, which
// the catalog would refuse the whole hand-over for. nil when none is.
func NewIdentity(tmdb, tvdb int64, imdb string) *Identity {
	var id Identity
	if tmdb > 0 {
		id.TMDB = strconv.FormatInt(tmdb, 10)
	}
	if tvdb > 0 {
		id.TVDB = strconv.FormatInt(tvdb, 10)
	}
	id.IMDB = IMDbID(imdb)
	if id == (Identity{}) {
		return nil
	}
	return &id
}

// IMDbID is an IMDb title id in the form the catalog takes it (tt and digits,
// lower case), or "" when it is none.
func IMDbID(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	if digitsRE.MatchString(v) {
		v = "tt" + v
	}
	if !imdbRE.MatchString(v) {
		return ""
	}
	return v
}

// IngestRequest mirrors katalog-manager's POST /api/ingest body.
type IngestRequest struct {
	Path        string  `json:"path"`
	Type        string  `json:"type"`
	Title       string  `json:"title"`
	Year        *int32  `json:"year,omitempty"`
	Description *string `json:"description,omitempty"`

	// Episode coordinates. katalog REJECTS type=episode without them, and
	// that rejection is deliberate: an episode with a NULL parent becomes a
	// playable, metadata-less orphan named after the show, with no error
	// anywhere in the pipeline. Series names the series by identity, which is
	// what acquire holds; ParentID is the catalog's own id, which acquire
	// never holds. EpisodeEnd is the last episode of one file that covers
	// several in a row.
	Series        *Identity `json:"series,omitempty"`
	ParentID      *string   `json:"parentId,omitempty"`
	SeasonNumber  *int32    `json:"seasonNumber,omitempty"`
	EpisodeNumber *int32    `json:"episodeNumber,omitempty"`
	EpisodeEnd    *int32    `json:"episodeEnd,omitempty"`

	// Identity names a movie, so it arrives matched rather than by title.
	Identity *Identity `json:"identity,omitempty"`
}

// ByIdentity reports whether the request carries a field of ingest by
// identity, which a catalog without that contract does not take.
func (r IngestRequest) ByIdentity() bool {
	return r.Series != nil || r.EpisodeEnd != nil || r.Identity != nil
}

// IngestResult is the catalog's ids for what it took in: the item, whether it
// made it, the series it linked an episode to, and the items of the episodes
// the file covers after its first (in episode order).
type IngestResult struct {
	ItemID   string   `json:"itemId"`
	Created  bool     `json:"created"`
	SeriesID string   `json:"seriesId,omitempty"`
	Covered  []string `json:"covered,omitempty"`
}

// IngestError is an answer of the ingest other than 200.
type IngestError struct {
	Code int
	Body string
}

func (e *IngestError) Error() string { return fmt.Sprintf("ingest %d: %s", e.Code, e.Body) }

// Refused reports whether the catalog turned the file down (a 4xx other than
// 429), which asking again does not change; anything else is worth a retry.
func (e *IngestError) Refused() bool {
	return e.Code >= 400 && e.Code < 500 && e.Code != http.StatusTooManyRequests
}

// Holder is the item a 409 names: the library holds that episode or movie
// already, as another item, or the path is that item's. "" when the answer
// names none.
func (e *IngestError) Holder() string {
	if e.Code != http.StatusConflict {
		return ""
	}
	var body struct {
		ItemID string `json:"itemId"`
	}
	if json.Unmarshal([]byte(e.Body), &body) != nil {
		return ""
	}
	return strings.TrimSpace(body.ItemID)
}

// Message is the error the catalog's answer gives, else its body.
func (e *IngestError) Message() string {
	var body struct {
		Error string `json:"error"`
	}
	if json.Unmarshal([]byte(e.Body), &body) == nil && body.Error != "" {
		return body.Error
	}
	return strings.TrimSpace(e.Body)
}

// Unsupported reports whether the refusal is the catalog not taking ingest by
// identity at all: a 422, or the 400 of the catalog from before the contract,
// which ignores series and asks for the catalog id of the parent instead ("an
// episode requires parentId, seasonNumber and episodeNumber"). Such a catalog
// takes the file once it has the contract, so the hand-over waits instead of
// failing.
//
// Not every 400 that names the new fields is one: the catalog with the
// contract refuses a body it cannot read — an id in the wrong form, an
// episodeEnd too far — in their words, and that is a refusal.
func (e *IngestError) Unsupported() bool {
	switch e.Code {
	case http.StatusUnprocessableEntity:
		return true
	case http.StatusBadRequest:
		return strings.Contains(strings.ToLower(e.Message()), "requires parentid")
	}
	return false
}

// RefusesIdentity reports whether a 400 is about a title's own identity
// (identity), which a movie can go without: it is handed over by title then,
// as before the contract.
func (e *IngestError) RefusesIdentity() bool {
	return e.Code == http.StatusBadRequest && strings.Contains(strings.ToLower(e.Message()), "identity")
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
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return IngestResult{}, &IngestError{Code: resp.StatusCode, Body: string(b)}
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

// IsVideo reports whether a path names a file the pipeline can ingest.
func IsVideo(path string) bool { return videoExts[strings.ToLower(filepath.Ext(path))] }

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
