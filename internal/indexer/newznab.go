// Package indexer speaks the indexer wire protocols directly.
//
// It exists because the aggregator's own /api/v1/search endpoint accepts
// type=tvsearch with season, ep and tvdbid, returns HTTP 200, and SILENTLY
// DISCARDS them: a request for Breaking Bad S02E05 comes back with S03E03,
// S03E04 and S03E06. It is a title-text endpoint wearing a typed interface,
// which is the worst possible failure shape — it looks like it worked.
//
// The per-indexer newznab/torznab proxy honours all of it exactly. That is what
// this package uses.
package indexer

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Categories. Production sends 5000-series to TV and 2000-series to movies, and
// scoping matters for more than precision: 12 of the enabled indexers are
// adult-only, so an unscoped family-movie search fans out to them.
const (
	CatTV    = "5000"
	CatMovie = "2000"
)

// Query is a typed search. Exactly one of the id fields is normally set;
// free-text Term is the fallback when a title has no id an indexer accepts.
type Query struct {
	Kind    string // tvsearch | movie | search
	Term    string
	TVDBID  int64
	IMDBID  string // without the "tt" prefix on the wire
	Season  *int
	Episode *int
	Cat     string
	Limit   int
}

// Values renders the query as newznab parameters.
func (q Query) Values(apiKey string) url.Values {
	v := url.Values{}
	kind := q.Kind
	if kind == "" {
		kind = "search"
	}
	v.Set("t", kind)
	if apiKey != "" {
		v.Set("apikey", apiKey)
	}
	if q.Term != "" {
		v.Set("q", q.Term)
	}
	if q.TVDBID > 0 {
		v.Set("tvdbid", strconv.FormatInt(q.TVDBID, 10))
	}
	if id := strings.TrimPrefix(strings.ToLower(q.IMDBID), "tt"); id != "" {
		v.Set("imdbid", id)
	}
	if q.Season != nil {
		v.Set("season", strconv.Itoa(*q.Season))
	}
	if q.Episode != nil {
		v.Set("ep", strconv.Itoa(*q.Episode))
	}
	if q.Cat != "" {
		v.Set("cat", q.Cat)
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 100
	}
	v.Set("limit", strconv.Itoa(limit))
	return v
}

// Typed reports whether the query carries something an indexer can match
// precisely, as opposed to degrading to text.
func (q Query) Typed() bool {
	return q.TVDBID > 0 || q.IMDBID != "" || q.Season != nil
}

// Item is one release from a newznab feed.
type Item struct {
	Title    string
	Size     int64
	Seeders  int
	Indexer  string
	Protocol string
	Link     string
	Magnet   string
	PubDate  time.Time
}

// Client talks to one aggregator's per-indexer newznab proxy.
type Client struct {
	BaseURL string
	APIKey  string
	HTTP    *http.Client
}

func New(baseURL, apiKey string) *Client {
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), APIKey: apiKey,
		HTTP: &http.Client{Timeout: 60 * time.Second}}
}

func (c *Client) Enabled() bool { return c != nil && c.BaseURL != "" }

// Search runs a typed query against ONE indexer.
//
// An empty result is NOT an error and NOT proof of absence: an id-based
// tvsearch sent to an indexer that does not support ids returns HTTP 200 with
// zero items and no error at all — indistinguishable from "no such release
// exists". Callers must treat a zero count from a typed query as inconclusive
// and fall back, never as a negative answer.
func (c *Client) Search(ctx context.Context, indexerID int, q Query) ([]Item, error) {
	if !c.Enabled() {
		return nil, nil
	}
	u := fmt.Sprintf("%s/api/v1/indexer/%d/newznab?%s", c.BaseURL, indexerID, q.Values(c.APIKey).Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Api-Key", c.APIKey)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("indexer %d: %w", indexerID, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("indexer %d: http %d", indexerID, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, err
	}
	return parseNewznab(body)
}

type nzbFeed struct {
	Channel struct {
		Items []struct {
			Title     string `xml:"title"`
			Link      string `xml:"link"`
			PubDate   string `xml:"pubDate"`
			Enclosure struct {
				URL    string `xml:"url,attr"`
				Length int64  `xml:"length,attr"`
			} `xml:"enclosure"`
			Attrs []struct {
				Name  string `xml:"name,attr"`
				Value string `xml:"value,attr"`
			} `xml:"attr"`
		} `xml:"item"`
	} `xml:"channel"`
}

func parseNewznab(body []byte) ([]Item, error) {
	var f nzbFeed
	if err := xml.Unmarshal(body, &f); err != nil {
		return nil, fmt.Errorf("newznab: %w", err)
	}
	out := make([]Item, 0, len(f.Channel.Items))
	for _, it := range f.Channel.Items {
		item := Item{Title: it.Title, Link: it.Link, Size: it.Enclosure.Length}
		if item.Link == "" {
			item.Link = it.Enclosure.URL
		}
		for _, a := range it.Attrs {
			switch strings.ToLower(a.Name) {
			case "size":
				if n, err := strconv.ParseInt(a.Value, 10, 64); err == nil && item.Size == 0 {
					item.Size = n
				}
			case "seeders":
				item.Seeders, _ = strconv.Atoi(a.Value)
			case "magneturl":
				item.Magnet = a.Value
			}
		}
		item.Protocol = "usenet"
		if item.Magnet != "" || item.Seeders > 0 {
			item.Protocol = "torrent"
		}
		if it.PubDate != "" {
			for _, layout := range []string{time.RFC1123Z, time.RFC1123, time.RFC822Z} {
				if d, err := time.Parse(layout, it.PubDate); err == nil {
					item.PubDate = d
					break
				}
			}
		}
		out = append(out, item)
	}
	return out, nil
}
