package release

import (
	"net/url"
	"sort"
	"strings"
	"time"
)

// Release is one search hit as a search source returned it.
//
// Link is the source's own download link and usually carries the source's API
// key. It never leaves acquire: it is not serialised (no JSON tags on purpose),
// grabs store it redacted, and the console only ever receives an opaque
// reference to it.
type Release struct {
	Protocol  string // usenet | torrent
	Title     string
	Indexer   string // the search source's name
	IndexerID int64  // the search source's id
	GUID      string
	Size      int64
	Seeders   int
	Peers     int
	InfoHash  string
	Link      string
	Magnet    string
	PubDate   time.Time
}

// IsUsenet reports whether the release is an NZB.
func (r Release) IsUsenet() bool { return r.Protocol == "usenet" }

// Source is the link a grab starts from: a magnet for a torrent when there is
// one (it carries no source credential and needs no fetch), otherwise the
// source's download link. A torrent known only by its info hash becomes a
// magnet built from it.
func (r Release) Source() string {
	if r.Protocol == "torrent" {
		if r.Magnet != "" {
			return r.Magnet
		}
		if r.Link == "" && r.InfoHash != "" {
			v := url.Values{"xt": {"urn:btih:" + strings.ToLower(r.InfoHash)}}
			if r.Title != "" {
				v.Set("dn", r.Title)
			}
			return "magnet:?" + v.Encode()
		}
	}
	return r.Link
}

// Rank orders releases best-first: when preferUsenet, all NZB releases come
// before torrents; within a protocol, torrents sort by seeders desc then size,
// usenet by size desc (bigger ≈ higher quality for a single title). Zero-seeder
// torrents are dropped when at least one usenet or seeded torrent exists.
//
// This is the protocol-only order. Grab decisions score releases against the
// quality profile (Score); Rank remains for callers that have no profile.
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
