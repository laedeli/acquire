package prowlarr

import "testing"

// TestRankNZBFirst: with preferUsenet, every NZB ranks above torrents; dead
// (0-seed) torrents drop when a live alternative exists.
func TestRankNZBFirst(t *testing.T) {
	rs := []Release{
		{Protocol: "torrent", Title: "t-100", Seeders: 100, Size: 5},
		{Protocol: "usenet", Title: "nzb-small", Size: 1},
		{Protocol: "torrent", Title: "t-dead", Seeders: 0, Size: 9},
		{Protocol: "usenet", Title: "nzb-big", Size: 8},
		{Protocol: "torrent", Title: "t-5", Seeders: 5, Size: 2},
	}
	got := Rank(rs, true)
	if len(got) == 0 || !got[0].IsUsenet() {
		t.Fatalf("top should be usenet, got %+v", got)
	}
	// Both NZBs before any torrent; NZB ordered by size desc.
	if got[0].Title != "nzb-big" || got[1].Title != "nzb-small" {
		t.Errorf("nzb order wrong: %s,%s", got[0].Title, got[1].Title)
	}
	if !got[1].IsUsenet() || got[2].IsUsenet() {
		t.Errorf("all NZBs must precede torrents: %+v", got)
	}
	// The dead torrent is dropped (a live alternative exists).
	for _, r := range got {
		if r.Title == "t-dead" {
			t.Errorf("0-seed torrent should be dropped")
		}
	}
	// Seeded torrents ordered by seeders desc.
	if got[2].Title != "t-100" || got[3].Title != "t-5" {
		t.Errorf("torrent seeder order wrong: %s,%s", got[2].Title, got[3].Title)
	}
}

// TestRankTorrentFirst honors preferUsenet=false.
func TestRankTorrentFirst(t *testing.T) {
	rs := []Release{
		{Protocol: "usenet", Title: "nzb", Size: 1},
		{Protocol: "torrent", Title: "t", Seeders: 3, Size: 1},
	}
	got := Rank(rs, false)
	if got[0].IsUsenet() {
		t.Errorf("torrent-first: top should be torrent, got %s", got[0].Title)
	}
}

// TestRankOnlyDeadTorrents keeps them when there's no alternative.
func TestRankOnlyDeadTorrents(t *testing.T) {
	rs := []Release{{Protocol: "torrent", Title: "only", Seeders: 0, Size: 1}}
	got := Rank(rs, true)
	if len(got) != 1 || got[0].Title != "only" {
		t.Errorf("a lone dead torrent must survive: %+v", got)
	}
}

// TestAdapterAndSource maps protocol → adapter + source.
func TestAdapterAndSource(t *testing.T) {
	nzb := Release{Protocol: "usenet", DownloadURL: "http://p/dl?apikey=x"}
	if nzb.Adapter() != "nzbget" || nzb.Source() != "http://p/dl?apikey=x" {
		t.Errorf("usenet mapping wrong: %s %s", nzb.Adapter(), nzb.Source())
	}
	tor := Release{Protocol: "torrent", MagnetURL: "magnet:?x", DownloadURL: "http://p/t"}
	if tor.Adapter() != "qbittorrent" || tor.Source() != "magnet:?x" {
		t.Errorf("torrent mapping wrong: %s %s", tor.Adapter(), tor.Source())
	}
}
