package prowlarr

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// Prowlarr renders BOTH downloadUrl and magnetUrl against its own view of the
// host — in beta that is http://localhost:9696. Source() prefers magnetUrl for
// torrents, so a magnet left unrewritten hands qBittorrent a URL pointing at its
// own pod: every torrent grab fails. This locks both fields.
func TestSearchRewritesBothDownloadAndMagnetHosts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `[
		 {"protocol":"torrent","title":"A","magnetUrl":"http://localhost:9696/download?apikey=k&id=1",
		  "downloadUrl":"http://localhost:9696/download?apikey=k&id=1"},
		 {"protocol":"usenet","title":"B","downloadUrl":"http://127.0.0.1:9696/download?apikey=k&id=2"},
		 {"protocol":"torrent","title":"C","magnetUrl":"magnet:?xt=urn:btih:deadbeef&dn=C"}
		]`)
	}))
	defer srv.Close()

	c := &Client{BaseURL: srv.URL, APIKey: "k", HTTP: srv.Client()}
	base, _ := url.Parse(srv.URL)
	out, err := c.SearchIn(context.Background(), "q", []int{1})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 3 {
		t.Fatalf("got %d releases", len(out))
	}
	for _, r := range out {
		src := r.Source()
		if src == "" {
			t.Fatalf("%s: empty source", r.Title)
		}
		// A real magnet: URI has no host and must survive untouched.
		if strings.HasPrefix(src, "magnet:") {
			if src != "magnet:?xt=urn:btih:deadbeef&dn=C" {
				t.Errorf("%s: magnet URI was mangled: %s", r.Title, src)
			}
			continue
		}
		u, err := url.Parse(src)
		if err != nil {
			t.Fatalf("%s: %v", r.Title, err)
		}
		// Must have been rewritten to the configured base. Asserting "not
		// loopback" would be wrong here: httptest binds 127.0.0.1, so the
		// correct target IS loopback — what matters is that the indexer's own
		// :9696 view was replaced by ours.
		if u.Host != base.Host {
			t.Errorf("%s: Source() host %q, want the configured base %q (raw: %s)",
				r.Title, u.Host, base.Host, src)
		}
		if !strings.Contains(src, "apikey=k") {
			t.Errorf("%s: rewrite dropped the query: %s", r.Title, src)
		}
	}
}
