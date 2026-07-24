// Package prowlarr queries our Prowlarr instance's unified search API and ranks
// releases NZB-first. Prowlarr is the indexer aggregator (it manages every
// indexer and returns one merged result set tagged by protocol); acquire is the
// decision layer that picks a release and routes it to the download-gateway.
package prowlarr

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Client struct {
	BaseURL string // in-cluster, e.g. http://prowlarr:9696
	APIKey  string
	HTTP    *http.Client
}

func New(baseURL, apiKey string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		APIKey:  apiKey,
		// The multi-indexer fan-out is slow (a single slow indexer gates the
		// whole aggregated search); give it generous headroom.
		HTTP: &http.Client{Timeout: 210 * time.Second},
	}
}

func (c *Client) Enabled() bool { return c != nil && c.BaseURL != "" && c.APIKey != "" }

// Release is one ranked search hit.
type Release struct {
	Protocol    string `json:"protocol"` // usenet | torrent
	Title       string `json:"title"`
	Indexer     string `json:"indexer"`
	Size        int64  `json:"size"`
	Seeders     int    `json:"seeders"`
	DownloadURL string `json:"downloadUrl"`
	MagnetURL   string `json:"magnetUrl"`
}

// IsUsenet reports whether the release is an NZB.
func (r Release) IsUsenet() bool { return r.Protocol == "usenet" }

// Source returns the URL to hand the download-gateway (magnet preferred for
// torrents; the Prowlarr download URL otherwise — for usenet it embeds the
// apikey so NZBGet fetches it directly).
func (r Release) Source() string {
	if r.Protocol == "torrent" && r.MagnetURL != "" {
		return r.MagnetURL
	}
	return r.DownloadURL
}

// Adapter maps the release protocol to the gateway adapter name.
func (r Release) Adapter() string {
	if r.IsUsenet() {
		return "nzbget"
	}
	return "qbittorrent"
}

// IndexerInfo is the subset of an indexer definition we need to scope searches.
type IndexerInfo struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
	Enable   bool   `json:"enable"`
}

// Indexers lists the configured indexers (to scope a search by protocol — a
// full 50-indexer fan-out is far too slow, so NZB-first means "search the few
// usenet indexers first").
func (c *Client) Indexers(ctx context.Context) ([]IndexerInfo, error) {
	if !c.Enabled() {
		return nil, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/api/v1/indexer", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Api-Key", c.APIKey)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, nil
	}
	var out []IndexerInfo
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

// EnabledIDs returns the enabled indexer IDs for a protocol ("usenet"|"torrent").
func EnabledIDs(idx []IndexerInfo, protocol string) []int {
	var ids []int
	for _, i := range idx {
		if i.Enable && i.Protocol == protocol {
			ids = append(ids, i.ID)
		}
	}
	return ids
}

// Search runs an aggregated search across all indexers.
func (c *Client) Search(ctx context.Context, query string) ([]Release, error) {
	return c.SearchIn(ctx, query, nil)
}

// SearchIn searches only the given indexer IDs (nil = all). Scoping to the usenet
// indexers keeps the NZB-first path fast; the torrent-wide fallback is rare.
func (c *Client) SearchIn(ctx context.Context, query string, indexerIDs []int) ([]Release, error) {
	if !c.Enabled() || strings.TrimSpace(query) == "" {
		return nil, nil
	}
	v := url.Values{"query": {query}, "type": {"search"}, "limit": {"100"}}
	for _, id := range indexerIDs {
		v.Add("indexerIds", strconv.Itoa(id))
	}
	u := c.BaseURL + "/api/v1/search?" + v.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Api-Key", c.APIKey)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, nil
	}
	var out []Release
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	// Prowlarr renders downloadUrl with its own view of the host — often
	// localhost:9696. Rewrite to the in-cluster base so NZBGet/qBittorrent can
	// fetch it.
	for i := range out {
		out[i].DownloadURL = c.rewriteHost(out[i].DownloadURL)
	}
	return out, nil
}

// rewriteHost swaps a localhost/127.0.0.1 origin in a Prowlarr download URL for
// the configured in-cluster base, preserving the path + query (the apikey).
func (c *Client) rewriteHost(raw string) string {
	if raw == "" {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	host := u.Hostname()
	if host == "localhost" || host == "127.0.0.1" {
		base, err := url.Parse(c.BaseURL)
		if err == nil {
			u.Scheme = base.Scheme
			u.Host = base.Host
			return u.String()
		}
	}
	return raw
}

// Rank orders releases best-first: when preferUsenet, all NZB releases come
// before torrents; within a protocol, torrents sort by seeders desc then size,
// usenet by size desc (bigger ≈ higher quality for a single title). Zero-seeder
// torrents are dropped when at least one usenet or seeded torrent exists.
func Rank(rs []Release, preferUsenet bool) []Release {
	usenet, torrent := splitByProtocol(rs)
	sort.SliceStable(usenet, func(i, j int) bool { return usenet[i].Size > usenet[j].Size })
	sort.SliceStable(torrent, func(i, j int) bool {
		if torrent[i].Seeders != torrent[j].Seeders {
			return torrent[i].Seeders > torrent[j].Seeders
		}
		return torrent[i].Size > torrent[j].Size
	})
	// Drop dead (0-seed) torrents if we have any live alternative.
	haveAlt := len(usenet) > 0 || (len(torrent) > 0 && torrent[0].Seeders > 0)
	if haveAlt {
		torrent = filterSeeded(torrent)
	}
	if preferUsenet {
		return append(usenet, torrent...)
	}
	return append(torrent, usenet...)
}

func splitByProtocol(rs []Release) (usenet, torrent []Release) {
	for _, r := range rs {
		if r.IsUsenet() {
			usenet = append(usenet, r)
		} else if r.Protocol == "torrent" {
			torrent = append(torrent, r)
		}
	}
	return
}

func filterSeeded(rs []Release) []Release {
	var out []Release
	for _, r := range rs {
		if r.Seeders > 0 {
			out = append(out, r)
		}
	}
	return out
}
