package katalog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// A catalog that is broken must NOT look like a catalog that is empty. This is
// the exact failure that hid a weeks-long HTTP 500 on beta: every error path
// returned "not in library", so discovery quietly flagged nothing as owned.
func TestBrokenCatalogIsUnknownNotAbsent(t *testing.T) {
	for _, tc := range []struct {
		name string
		h    http.HandlerFunc
	}{
		{"500", func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, `{"err":"count items: column i.search_vector does not exist"}`, 500)
		}},
		{"401", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(401) }},
		{"garbage body", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("<html>")) }},
	} {
		srv := httptest.NewServer(tc.h)
		c := &Client{KatalogURL: srv.URL, HTTP: srv.Client()}
		got, err := c.InLibrary(context.Background(), "Dune")
		if got != AvailabilityUnknown {
			t.Errorf("%s: got %v, want unknown — a broken catalog must not read as 'you do not own this'", tc.name, got)
		}
		if err == nil {
			t.Errorf("%s: no error returned, so nothing would ever be logged", tc.name)
		}
		srv.Close()
	}
}

func TestHealthyCatalogAnswersYesAndNo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("q") == "Dune" {
			_, _ = w.Write([]byte(`{"items":[{"title":"Dune"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	defer srv.Close()
	c := &Client{KatalogURL: srv.URL, HTTP: srv.Client()}
	if got, err := c.InLibrary(context.Background(), "Dune"); got != InLibraryYes || err != nil {
		t.Errorf("owned title: got %v, %v", got, err)
	}
	if got, err := c.InLibrary(context.Background(), "Nope"); got != NotInLibrary || err != nil {
		t.Errorf("unowned title: got %v, %v", got, err)
	}
}
