package indexer

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// A feed's identity attributes travel with each release: a series' TVDB id,
// an IMDb id in IMDb's own form, however the feed writes it, and a TMDB id.
func TestAFeedsIdentityAttributes(t *testing.T) {
	body := `<rss xmlns:newznab="http://www.newznab.com/DTD/2010/feeds/attributes/" xmlns:torznab="http://torznab.com/schemas/2015/feed"><channel>
	  <item><title>Example.Show.S01E03.1080p.WEB-G</title><guid>a</guid>
	    <enclosure url="https://source.test/1.nzb" type="application/x-nzb"/>
	    <newznab:attr name="tvdbid" value="81189"/><newznab:attr name="imdb" value="0903747"/></item>
	  <item><title>Example.Movie.2026.1080p.WEB-G</title><guid>b</guid>
	    <enclosure url="https://source.test/2.nzb" type="application/x-nzb"/>
	    <torznab:attr name="imdbid" value="tt12345678"/><torznab:attr name="tmdbid" value="9100"/></item>
	  <item><title>Example.Other.2026.1080p.WEB-G</title><guid>c</guid>
	    <enclosure url="https://source.test/3.nzb" type="application/x-nzb"/>
	    <newznab:attr name="imdb" value="0"/><newznab:attr name="tvdbid" value="none"/></item>
	</channel></rss>`
	items, err := parseFeed([]byte(body), testSource("https://source.test"))
	if err != nil || len(items) != 3 {
		t.Fatalf("items = %+v %v", items, err)
	}
	if items[0].TVDBID != 81189 || items[0].IMDBID != "tt0903747" || items[0].TMDBID != 0 {
		t.Errorf("newznab attributes: %+v", items[0])
	}
	if items[1].IMDBID != "tt12345678" || items[1].TMDBID != 9100 || items[1].TVDBID != 0 {
		t.Errorf("torznab attributes: %+v", items[1])
	}
	if items[2].IMDBID != "" || items[2].TVDBID != 0 {
		t.Errorf("attributes that name nothing: %+v", items[2])
	}
	for in, want := range map[string]string{"903747": "tt0903747", "tt0903747": "tt0903747", "TT0903747": "tt0903747", "": "", "x": ""} {
		if got := imdbAttr(in); got != want {
			t.Errorf("imdbAttr(%q) = %q, want %q", in, got, want)
		}
	}
}

// A read of a source's newest releases names its categories always — the
// source's own, else the protocol's parent ones — and pages back by offset.
func TestNewestReleasesAreScopedAndPaged(t *testing.T) {
	src := testSource("https://source.test")
	if got := fmt.Sprint(src.FeedCategories("tv")); got != "[5000]" {
		t.Errorf("tv = %s", got)
	}
	src.Categories = Categories{}
	if got := fmt.Sprint(src.FeedCategories("tv"), src.FeedCategories("movie"), src.FeedCategories("")); got != "[5000] [2000] [5000 2000]" {
		t.Errorf("without categories of its own = %s", got)
	}

	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		got = append(got, q.Get("t")+" q="+q.Get("q")+" cat="+q.Get("cat")+" offset="+q.Get("offset")+" limit="+q.Get("limit"))
		_, _ = w.Write([]byte(`<rss><channel></channel></rss>`))
	}))
	defer srv.Close()
	src = testSource(srv.URL)
	_, _ = testClient(srv).Search(context.Background(), src, Query{Kind: "tvsearch", Media: "tv", Cats: []int{5000}, Limit: 100})
	_, _ = testClient(srv).Search(context.Background(), src, Query{Kind: "search", Cats: []int{5000, 2000}, Limit: 100, Offset: 100})
	want := []string{"tvsearch q= cat=5000 offset= limit=100", "search q= cat=5000,2000 offset=100 limit=100"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("requests = %q, want %q", got, want)
	}
	if (*Caps)(nil).OffersTVSearch() || !(&Caps{TVSearch: SearchMode{Available: true}}).OffersTVSearch() ||
		(&Caps{}).OffersMovieSearch() {
		t.Error("what a source's caps say it offers")
	}
}
