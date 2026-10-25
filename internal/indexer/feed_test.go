package indexer

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// A feed's identity attributes travel with each release: a series' TVDB id,
// an IMDb id in IMDb's own form, however the feed writes it, and a TMDB id;
// so do the categories it files the release under. The ids are invented.
func TestAFeedsIdentityAttributes(t *testing.T) {
	body := `<rss xmlns:newznab="http://www.newznab.com/DTD/2010/feeds/attributes/" xmlns:torznab="http://torznab.com/schemas/2015/feed"><channel>
	  <item><title>Example.Show.S01E03.1080p.WEB-G</title><guid>a</guid>
	    <enclosure url="https://source.test/1.nzb" type="application/x-nzb"/>
	    <newznab:attr name="category" value="5000"/><newznab:attr name="category" value="5040"/>
	    <newznab:attr name="tvdbid" value="9900001"/><newznab:attr name="imdb" value="99000001"/></item>
	  <item><title>Example.Movie.2026.1080p.WEB-G</title><guid>b</guid>
	    <enclosure url="https://source.test/2.nzb" type="application/x-nzb"/>
	    <torznab:attr name="category" value="2040"/>
	    <torznab:attr name="imdbid" value="tt99000002"/><torznab:attr name="tmdbid" value="9900002"/></item>
	  <item><title>Example.Other.2026.1080p.WEB-G</title><guid>c</guid>
	    <enclosure url="https://source.test/3.nzb" type="application/x-nzb"/>
	    <newznab:attr name="imdb" value="0"/><newznab:attr name="tvdbid" value="none"/><newznab:attr name="category" value="x"/></item>
	</channel></rss>`
	items, err := parseFeed([]byte(body), testSource("https://source.test"))
	if err != nil || len(items) != 3 {
		t.Fatalf("items = %+v %v", items, err)
	}
	if items[0].TVDBID != 9900001 || items[0].IMDBID != "tt99000001" || items[0].TMDBID != 0 ||
		fmt.Sprint(items[0].Categories) != "[5000 5040]" || items[0].Media() != "tv" {
		t.Errorf("newznab attributes: %+v", items[0])
	}
	if items[1].IMDBID != "tt99000002" || items[1].TMDBID != 9900002 || items[1].TVDBID != 0 || items[1].Media() != "movie" {
		t.Errorf("torznab attributes: %+v", items[1])
	}
	if items[2].IMDBID != "" || items[2].TVDBID != 0 || len(items[2].Categories) != 0 || items[2].Media() != "" {
		t.Errorf("attributes that name nothing: %+v", items[2])
	}
	for in, want := range map[string]string{"99000001": "tt99000001", "tt99000001": "tt99000001", "TT99000001": "tt99000001",
		"42": "tt0000042", "": "", "x": ""} {
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
		got = append(got, q.Get("t")+" q="+q.Get("q")+" cat="+q.Get("cat")+" offset="+q.Get("offset")+" limit="+q.Get("limit")+" extended="+q.Get("extended"))
		_, _ = w.Write([]byte(`<rss><channel></channel></rss>`))
	}))
	defer srv.Close()
	src = testSource(srv.URL)
	_, _ = testClient(srv).Search(context.Background(), src, Query{Kind: "tvsearch", Media: "tv", Cats: []int{5000}, Limit: 100})
	_, _ = testClient(srv).Search(context.Background(), src, Query{Kind: "search", Cats: []int{5000, 2000}, Limit: 100, Offset: 100, Extended: true})
	want := []string{"tvsearch q= cat=5000 offset= limit=100 extended=", "search q= cat=5000,2000 offset=100 limit=100 extended=1"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("requests = %q, want %q", got, want)
	}
	if (*Caps)(nil).OffersTVSearch() || !(&Caps{TVSearch: SearchMode{Available: true}}).OffersTVSearch() ||
		(&Caps{}).OffersMovieSearch() {
		t.Error("what a source's caps say it offers")
	}
}
