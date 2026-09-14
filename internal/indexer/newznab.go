// Package indexer speaks the newznab and torznab search APIs to each search
// source directly.
//
// Each source is one endpoint (base URL + API path) with its own API key,
// queried with the typed parameters the protocol defines: t=tvsearch with
// tvdbid, season and ep; t=movie with imdbid; t=search with free text; t=caps
// for what the source says it supports. An aggregator in front of the sources
// is no longer needed — and its unified search endpoint was the reason typed
// search did not work: it accepted season, ep and tvdbid, returned 200 and
// silently discarded them.
//
// The API key rides in the query string, as the protocol requires. It is kept
// out of everything this package returns: errors carry no URL, and a message a
// source sends back has the key cut out before it is used.
package indexer

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/laedeli/acquire/internal/endpoint"
	"github.com/laedeli/acquire/internal/release"
)

// Standard newznab parent categories, used when a source row names none.
const (
	CatTV    = 5000
	CatMovie = 2000
)

// Response size caps. A 100-item feed is well under a megabyte; the caps are
// about bounding a misbehaving endpoint, not about normal traffic.
const (
	maxFeedBytes = 16 << 20
	maxCapsBytes = 4 << 20
)

// Categories are the category ids a search is scoped to, per kind of title.
type Categories struct {
	Movie []int `json:"movie"`
	TV    []int `json:"tv"`
}

// For returns the categories for "movie" or "tv"; anything else is both.
func (c Categories) For(media string) []int {
	switch media {
	case "movie":
		return c.Movie
	case "tv":
		return c.TV
	}
	seen := map[int]bool{}
	var out []int
	for _, id := range append(append([]int(nil), c.Movie...), c.TV...) {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// Source is one search source ready to be queried: its API key is already
// opened. It must never be logged whole; String prints only its name.
type Source struct {
	ID         int64
	Name       string
	Protocol   string // usenet | torrent
	BaseURL    string
	APIPath    string
	APIKey     string
	Categories Categories
	Caps       *Caps
}

// String keeps the key out of any %v or %s formatting of a Source.
func (s Source) String() string { return fmt.Sprintf("source %d (%s)", s.ID, s.Name) }

// GoString does the same for %#v.
func (s Source) GoString() string { return s.String() }

func (s Source) endpoint(v url.Values) string {
	p := s.APIPath
	if p == "" {
		p = "/api"
	}
	return strings.TrimRight(s.BaseURL, "/") + p + "?" + v.Encode()
}

// Query is a typed search. Exactly one of the id fields is normally set;
// free-text Term is the fallback when a title has no id a source accepts.
type Query struct {
	Kind    string // tvsearch | movie | search
	Term    string
	TVDBID  int64
	IMDBID  string // without the "tt" prefix on the wire
	Season  *int
	Episode *int
	// Media picks the source's categories: movie, tv, or "" for both.
	Media string
	Limit int
}

// Values renders the query as newznab parameters.
func (q Query) Values(apiKey string, cats []int) url.Values {
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
	if len(cats) > 0 {
		parts := make([]string, len(cats))
		for i, c := range cats {
			parts[i] = strconv.Itoa(c)
		}
		v.Set("cat", strings.Join(parts, ","))
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 100
	}
	v.Set("limit", strconv.Itoa(limit))
	return v
}

// Typed reports whether the query carries something a source can match
// precisely, as opposed to degrading to text.
func (q Query) Typed() bool {
	return q.TVDBID > 0 || q.IMDBID != "" || q.Season != nil
}

// Client sends requests to search sources. HTTP should be the endpoint
// policy's client, so every connection is checked where it is dialled.
type Client struct {
	HTTP *http.Client
}

// Search runs a query against one source and returns its releases, each
// tagged with the source.
//
// An empty result is NOT an error and NOT proof of absence: an id-based
// tvsearch sent to a source that ignores ids returns HTTP 200 with zero items —
// indistinguishable from "no such release exists". Callers must treat a zero
// count from a typed query as inconclusive and fall back.
func (c *Client) Search(ctx context.Context, src Source, q Query) ([]release.Release, error) {
	limit := q.Limit
	if src.Caps != nil && src.Caps.Limits.Max > 0 && (limit <= 0 || limit > src.Caps.Limits.Max) {
		q.Limit = src.Caps.Limits.Max
	}
	body, err := c.get(ctx, src, q.Values(src.APIKey, src.Categories.For(q.Media)), maxFeedBytes)
	if err != nil {
		return nil, err
	}
	items, err := parseFeed(body, src)
	if err != nil {
		return nil, &Error{Kind: KindOther, Msg: "the answer is not a newznab or torznab feed"}
	}
	return items, nil
}

// FetchCaps asks a source what it supports (t=caps).
func (c *Client) FetchCaps(ctx context.Context, src Source) (*Caps, error) {
	v := url.Values{"t": {"caps"}}
	if src.APIKey != "" {
		v.Set("apikey", src.APIKey)
	}
	body, err := c.get(ctx, src, v, maxCapsBytes)
	if err != nil {
		return nil, err
	}
	caps, err := ParseCaps(body)
	if err != nil {
		return nil, &Error{Kind: KindOther, Msg: "the caps answer is not newznab caps XML"}
	}
	return caps, nil
}

func (c *Client) get(ctx context.Context, src Source, v url.Values, max int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src.endpoint(v), nil)
	if err != nil {
		return nil, &Error{Kind: KindOther, Msg: "the source address is not a valid URL"}
	}
	req.Header.Set("User-Agent", "acquire")
	req.Header.Set("Accept", "application/rss+xml, application/xml;q=0.9, */*;q=0.1")
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, redacted(transportError(err), src.APIKey)
	}
	defer resp.Body.Close()

	body, readErr := endpoint.ReadCapped(resp.Body, max)
	if resp.StatusCode != http.StatusOK {
		// A newznab error document says more than the status line does.
		if readErr == nil {
			if e := newznabError(body); e != nil {
				return nil, redacted(e, src.APIKey)
			}
		}
		return nil, statusError(resp.StatusCode)
	}
	if errors.Is(readErr, endpoint.ErrTooLarge) {
		return nil, &Error{Kind: KindOther, Msg: fmt.Sprintf("the answer is larger than %d MiB", max>>20)}
	}
	if readErr != nil {
		return nil, redacted(transportError(readErr), src.APIKey)
	}
	// Errors often come as a 200 carrying an <error> document.
	if e := newznabError(body); e != nil {
		return nil, redacted(e, src.APIKey)
	}
	return body, nil
}

type feedItem struct {
	Title     string `xml:"title"`
	GUID      string `xml:"guid"`
	Link      string `xml:"link"`
	Size      int64  `xml:"size"`
	PubDate   string `xml:"pubDate"`
	Enclosure struct {
		URL    string `xml:"url,attr"`
		Length int64  `xml:"length,attr"`
		Type   string `xml:"type,attr"`
	} `xml:"enclosure"`
	// newznab:attr and torznab:attr: matched by local name in any namespace.
	Attrs []struct {
		Name  string `xml:"name,attr"`
		Value string `xml:"value,attr"`
	} `xml:"attr"`
}

type feed struct {
	Channel struct {
		Items []feedItem `xml:"item"`
	} `xml:"channel"`
}

// parseFeed reads a newznab or torznab RSS feed.
//
// The protocol comes from the source row, not from guessing: a usenet source
// hands out NZBs. Each item is still cross-checked against what it declares — an
// enclosure typed as the other protocol, or torrent attributes on a usenet
// source — and dropped when they disagree, because routing a torrent to a
// usenet client fails only after the grab has been spent.
func parseFeed(body []byte, src Source) ([]release.Release, error) {
	var f feed
	dec := newDecoder(body)
	root, err := rootElement(dec)
	if err != nil {
		return nil, err
	}
	if root.Name.Local != "rss" {
		// An HTML maintenance page parses as XML too, with zero items — which
		// would read as "nothing found" rather than as the failure it is.
		return nil, errors.New("not an RSS document")
	}
	if err := dec.DecodeElement(&f, &root); err != nil {
		return nil, err
	}
	out := make([]release.Release, 0, len(f.Channel.Items))
	for _, it := range f.Channel.Items {
		r := release.Release{
			Protocol: src.Protocol, Title: strings.TrimSpace(it.Title),
			Indexer: src.Name, IndexerID: src.ID,
			GUID: strings.TrimSpace(it.GUID), Link: strings.TrimSpace(it.Enclosure.URL),
			Size: it.Enclosure.Length,
		}
		if r.Link == "" {
			r.Link = strings.TrimSpace(it.Link)
		}
		var attrSize int64
		for _, a := range it.Attrs {
			v := strings.TrimSpace(a.Value)
			switch strings.ToLower(a.Name) {
			case "size":
				attrSize, _ = strconv.ParseInt(v, 10, 64)
			case "seeders":
				r.Seeders, _ = strconv.Atoi(v)
			case "peers":
				r.Peers, _ = strconv.Atoi(v)
			case "infohash":
				r.InfoHash = strings.ToLower(v)
			case "magneturl":
				r.Magnet = v
			case "guid":
				if r.GUID == "" {
					r.GUID = v
				}
			}
		}
		if r.Size <= 0 {
			r.Size = attrSize
		}
		if r.Size <= 0 {
			r.Size = it.Size
		}
		if strings.HasPrefix(strings.ToLower(r.Link), "magnet:") {
			if r.Magnet == "" {
				r.Magnet = r.Link
			}
			r.Link = ""
		}
		if r.Title == "" || (r.Link == "" && r.Magnet == "" && r.InfoHash == "") {
			continue
		}
		if !protocolAgrees(src.Protocol, strings.ToLower(it.Enclosure.Type), r) {
			continue
		}
		if it.PubDate != "" {
			for _, layout := range []string{time.RFC1123Z, time.RFC1123, time.RFC822Z, time.RFC3339} {
				if d, err := time.Parse(layout, strings.TrimSpace(it.PubDate)); err == nil {
					r.PubDate = d
					break
				}
			}
		}
		out = append(out, r)
	}
	return out, nil
}

func protocolAgrees(protocol, enclosureType string, r release.Release) bool {
	switch protocol {
	case "usenet":
		return enclosureType != "application/x-bittorrent" && r.Magnet == "" && r.InfoHash == ""
	case "torrent":
		return enclosureType != "application/x-nzb"
	}
	return false
}
