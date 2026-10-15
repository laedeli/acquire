package katalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/laedeli/acquire/internal/identity"
)

// Holdings by identity: what the library holds of a title, asked of the read
// side (katalog-api GET /api/v1/holdings) by an identity the title has outside
// any one library (ADR-0012). The answer says what is held — never what
// anyone wants; deciding what is missing is acquire's.

var (
	// ErrNotHeld: the library holds nothing of the title the identity names.
	ErrNotHeld = errors.New("the library does not hold it")
	// ErrHoldingsUnsupported: the read side does not answer holdings by
	// identity (a catalog from before the contract).
	ErrHoldingsUnsupported = errors.New("the catalog does not answer holdings by identity")
)

// HoldingsError is any other answer: the read side down (503 too while it may
// not read the identities), refusing acquire's service account, or refusing
// the request (400).
type HoldingsError struct {
	Code int
	Body string
}

func (e *HoldingsError) Error() string {
	switch e.Code {
	case http.StatusUnauthorized:
		return "katalog-api refused acquire's service account (HTTP 401): its token must carry the audience katalog-api accepts"
	case http.StatusForbidden:
		return "katalog-api refused acquire's service account (HTTP 403): holdings are never served to a request capped at an age"
	}
	if b := strings.TrimSpace(e.Body); b != "" {
		return fmt.Sprintf("katalog-api holdings: HTTP %d: %s", e.Code, b)
	}
	return fmt.Sprintf("katalog-api holdings: HTTP %d", e.Code)
}

// SeriesHoldings is what the library holds of one series.
type SeriesHoldings struct {
	SeriesID    string           `json:"seriesId"`
	Title       string           `json:"title"`
	Year        int              `json:"year"`
	ExternalIDs identity.IDs     `json:"externalIds"`
	Seasons     []SeasonHoldings `json:"seasons"`
}

// SeasonHoldings is one season's held episodes.
type SeasonHoldings struct {
	Season   int              `json:"season"`
	Episodes []EpisodeHolding `json:"episodes"`
}

// EpisodeHolding is one held episode: its item, whether it has a file, whether
// it is playable (packaged), and the item whose file covers it when it is one
// of several episodes of one file — whose hasFile and playable it then
// carries. Season 0 is answered too.
type EpisodeHolding struct {
	Episode   int    `json:"episode"`
	ItemID    string `json:"itemId"`
	HasFile   bool   `json:"hasFile"`
	Playable  bool   `json:"playable"`
	CoveredBy string `json:"coveredBy"`
}

// Held reports whether the episode is in the library: with a file of its own,
// or covered by another episode's.
func (e EpisodeHolding) Held() bool { return e.ItemID != "" && (e.HasFile || e.CoveredBy != "") }

// MovieHolding is what the library holds of one movie.
type MovieHolding struct {
	ItemID      string       `json:"itemId"`
	Title       string       `json:"title"`
	Year        int          `json:"year"`
	ExternalIDs identity.IDs `json:"externalIds"`
	HasFile     bool         `json:"hasFile"`
	Playable    bool         `json:"playable"`
}

// Held reports whether the movie is in the library with a file.
func (m MovieHolding) Held() bool { return m.ItemID != "" && m.HasFile }

// ListedTitle is one title of the listing: its item, name, year (0 when it
// has none) and identities ({} when the catalog keeps none for it).
type ListedTitle struct {
	ItemID      string       `json:"itemId"`
	Title       string       `json:"title"`
	Year        int          `json:"year"`
	ExternalIDs identity.IDs `json:"externalIds"`
}

// HoldingsPage is one page of every title of a kind with its identities.
type HoldingsPage struct {
	Items    []ListedTitle `json:"items"`
	Page     int           `json:"page"`
	PageSize int           `json:"pageSize"`
	Total    int           `json:"total"`
}

// listingPage is the largest page the read side serves.
const listingPage = 1000

// ProbeHoldings asks whether the read side answers holdings by identity, with
// the cheapest request there is: one page of one series. nil means it does;
// ErrHoldingsUnsupported that it does not (404, 405 or 501 for the endpoint);
// anything else that it could not be told.
//
// The probe is what tells "this series is not held" (404 for a lookup) from
// "there is no such endpoint" (404 for every lookup) apart.
func (c *Client) ProbeHoldings(ctx context.Context) error {
	var page struct {
		Items json.RawMessage `json:"items"`
		Page  *int            `json:"page"`
		Total *int            `json:"total"`
	}
	err := c.holdings(ctx, url.Values{"type": {"series"}, "page": {"1"}, "pageSize": {"1"}}, &page)
	switch items := strings.TrimSpace(string(page.Items)); {
	case errors.Is(err, ErrNotHeld):
		// A 404 for a listing is no endpoint, not an empty library.
		return ErrHoldingsUnsupported
	case err != nil:
		return err
	case strings.HasPrefix(items, "["):
		return nil
	case (items == "" || items == "null") && page.Page != nil && page.Total != nil:
		// An empty page that says so with null is a page all the same.
		return nil
	}
	// 200 with something that is not a page (a catch-all answering every
	// path) is not the contract either.
	return ErrHoldingsUnsupported
}

// ListHoldings reads every title of a kind (series or movie) the library
// holds, page by page.
func (c *Client) ListHoldings(ctx context.Context, kind string) ([]ListedTitle, error) {
	var out []ListedTitle
	for page := 1; ; page++ {
		var p HoldingsPage
		q := url.Values{"type": {kind}, "page": {strconv.Itoa(page)}, "pageSize": {strconv.Itoa(listingPage)}}
		if err := c.holdings(ctx, q, &p); err != nil {
			return nil, fmt.Errorf("the %s listing, page %d: %w", kind, page, err)
		}
		out = append(out, p.Items...)
		if len(p.Items) == 0 || page*listingPage >= p.Total {
			return out, nil
		}
	}
}

// SeriesHoldings asks what the library holds of the series an identity names
// (kind tmdb, tvdb or imdb). ErrNotHeld when it holds nothing of it.
func (c *Client) SeriesHoldings(ctx context.Context, kind, id string) (SeriesHoldings, error) {
	var out SeriesHoldings
	err := c.holdings(ctx, url.Values{"type": {"series"}, kind: {id}}, &out)
	return out, err
}

// MovieHoldings asks what the library holds of the movie an identity names.
func (c *Client) MovieHoldings(ctx context.Context, kind, id string) (MovieHolding, error) {
	var out MovieHolding
	err := c.holdings(ctx, url.Values{"type": {"movie"}, kind: {id}}, &out)
	return out, err
}

func (c *Client) holdings(ctx context.Context, q url.Values, into any) error {
	if c.KatalogURL == "" {
		return errors.New("katalog url not configured")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.KatalogURL+"/api/v1/holdings?"+q.Encode(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	c.authHeader(ctx, req)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("katalog-api holdings: %w", err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return ErrNotHeld
	case http.StatusNotImplemented, http.StatusMethodNotAllowed:
		return ErrHoldingsUnsupported
	default:
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return &HoldingsError{Code: resp.StatusCode, Body: string(b)}
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(into); err != nil {
		return fmt.Errorf("katalog-api holdings: decode: %w", err)
	}
	return nil
}
