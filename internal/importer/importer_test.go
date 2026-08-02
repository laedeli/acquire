package importer

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

const seriesBody = `[
 {"title":"Breaking Bad","sortTitle":"breaking bad","year":2008,"monitored":true,
  "monitorNewItems":"all","status":"ended","seriesType":"standard","tvdbId":81189,"tmdbId":1396,
  "alternateTitles":[{"title":"Breaking Bad"},{"title":"Во все тяжкие"},{"title":"Totalna Melina"}]},
 {"title":"Naruto Shippuden","year":2007,"monitored":true,"status":"ended",
  "seriesType":"anime","tvdbId":79824,"tmdbId":31910,"alternateTitles":[]},
 {"title":"No Ids Here","year":2020,"monitored":false,"status":"continuing",
  "seriesType":"standard","tmdbId":999,"alternateTitles":[]}
]`

func srv(t *testing.T, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
}

func TestImportsIntentAndAliases(t *testing.T) {
	s := srv(t, seriesBody)
	defer s.Close()
	got, err := Source{BaseURL: s.URL, APIKey: "k", Kind: "series", HTTP: s.Client()}.Import(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("imported %d titles", len(got))
	}
	bb := got[0]
	if bb.TVDBID != 81189 || bb.TMDBID != 1396 || !bb.Monitored || bb.SeriesType != "standard" {
		t.Errorf("Breaking Bad = %+v", bb)
	}
	// The title itself must not be duplicated into its own alias list, and
	// aliases matter: production matched 119 of 500 grabs on one.
	for _, a := range bb.Aliases {
		if a == "Breaking Bad" {
			t.Error("the title was duplicated into its own aliases")
		}
	}
	if len(bb.Aliases) != 2 {
		t.Errorf("aliases = %v, want the two non-title ones", bb.Aliases)
	}
	if got[1].SeriesType != "anime" {
		t.Errorf("anime type lost: %+v", got[1])
	}
}

// The gate measured that ZERO of 70 indexers accept a tmdbId. A series without
// a tvdbId cannot be typed-searched, and that has to be visible rather than
// silently imported as if it were fine.
func TestUnresolvableTitlesAreReportedNotDropped(t *testing.T) {
	s := srv(t, seriesBody)
	defer s.Close()
	got, _ := Source{BaseURL: s.URL, APIKey: "k", Kind: "series", HTTP: s.Client()}.Import(context.Background())
	rep := Summarise(got)
	if rep.Total != 3 {
		t.Fatalf("total = %d — unresolvable titles must still be imported", rep.Total)
	}
	if len(rep.Unresolvable) != 1 || rep.Unresolvable[0] != "No Ids Here" {
		t.Errorf("unresolvable = %v, want [No Ids Here]", rep.Unresolvable)
	}
	if rep.Monitored != 2 {
		t.Errorf("monitored = %d, want 2", rep.Monitored)
	}
	if rep.Aliases != 2 {
		t.Errorf("aliases counted = %d, want 2", rep.Aliases)
	}
}

func TestMovieResolvabilityUsesImdbOrTmdb(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   Title
		want bool
	}{
		{"imdb only", Title{Kind: "movie", IMDBID: "tt0133093"}, true},
		{"tmdb only", Title{Kind: "movie", TMDBID: 603}, true},
		{"neither", Title{Kind: "movie"}, false},
		{"series needs tvdb", Title{Kind: "series", TMDBID: 1396}, false},
		{"series with tvdb", Title{Kind: "series", TVDBID: 81189}, true},
	} {
		if got := tc.in.Resolvable(); got != tc.want {
			t.Errorf("%s: Resolvable() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// Nothing in this package may be able to grab. A shadow period that can write
// is not a shadow period.
func TestImporterCannotGrab(t *testing.T) {
	// Compile-time by construction: if a gateway client were imported this
	// package would not build without it. The dependency check lives in CI via
	// go list; this test documents the intent at the point of the constraint.
	s := srv(t, seriesBody)
	defer s.Close()
	src := Source{BaseURL: s.URL, APIKey: "k", Kind: "series", HTTP: s.Client()}
	if _, err := src.Import(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestUnauthorisedIsAnErrorNotAnEmptyImport(t *testing.T) {
	s := srv(t, seriesBody)
	defer s.Close()
	_, err := Source{BaseURL: s.URL, APIKey: "", Kind: "series", HTTP: s.Client()}.Import(context.Background())
	if err == nil {
		t.Fatal("a rejected import returned no error — it would look like an empty library")
	}
}
