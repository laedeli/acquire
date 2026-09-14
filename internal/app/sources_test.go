package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/laedeli/acquire/internal/indexer"
	"github.com/laedeli/acquire/internal/store"
)

func TestOptionalIntSemantics(t *testing.T) {
	var in struct {
		Limit OptionalInt `json:"limit"`
	}
	cases := []struct {
		body    string
		set     bool
		value   *int
		wantErr bool
	}{
		{body: `{}`},
		{body: `{"limit":null}`, set: true},
		{body: `{"limit":250}`, set: true, value: intp(250)},
		{body: `{"limit":"lots"}`, wantErr: true},
	}
	for _, c := range cases {
		in.Limit = OptionalInt{}
		err := json.Unmarshal([]byte(c.body), &in)
		if (err != nil) != c.wantErr {
			t.Errorf("%s: err = %v", c.body, err)
			continue
		}
		if c.wantErr {
			continue
		}
		got := in.Limit
		if got.Set != c.set || (got.Value == nil) != (c.value == nil) || (got.Value != nil && *got.Value != *c.value) {
			t.Errorf("%s: got %+v", c.body, got)
		}
	}
}

func intp(n int) *int { return &n }

func TestValidateSource(t *testing.T) {
	s := testService()
	ctx := context.Background()
	stored := store.Indexer{ID: 4, Name: "example", Protocol: "usenet", BaseURL: "https://source.test", APIPath: "/api",
		Categories: store.IndexerCategories{Movie: []int{2040}, TV: []int{5040}}, Priority: 3, Enabled: false,
		QueryLimitDay: intp(100)}
	existing := []store.Indexer{stored}
	valid := SourceInput{Name: "another", Protocol: "torrent", BaseURL: "https://feed.test/torznab"}
	keyed := stored
	keyed.APIKeyCT = []byte("sealed")

	cases := []struct {
		name    string
		in      SourceInput
		current *store.Indexer
		fields  string
	}{
		{name: "minimal", in: valid},
		{name: "name required", in: SourceInput{Protocol: "usenet", BaseURL: "https://x.test"}, fields: "name"},
		{name: "name taken, any case", in: SourceInput{Name: "EXAMPLE", Protocol: "usenet", BaseURL: "https://x.test"}, fields: "name"},
		{name: "own name on update", in: SourceInput{Name: "example", BaseURL: "https://x.test"}, current: &stored},
		{name: "unknown protocol", in: SourceInput{Name: "n", Protocol: "http", BaseURL: "https://x.test"}, fields: "protocol"},
		{name: "denied host", in: SourceInput{Name: "n", Protocol: "usenet", BaseURL: "https://feed.blocked.test"}, fields: "baseUrl"},
		{name: "metadata address", in: SourceInput{Name: "n", Protocol: "usenet", BaseURL: "http://169.254.169.254"}, fields: "baseUrl"},
		{name: "key in the address", in: SourceInput{Name: "n", Protocol: "usenet", BaseURL: "https://x.test/?apikey=abc"}, fields: "baseUrl"},
		{name: "bad path", in: SourceInput{Name: "n", Protocol: "usenet", BaseURL: "https://x.test", APIPath: "api?t=caps"}, fields: "apiPath"},
		{name: "traversal path", in: SourceInput{Name: "n", Protocol: "usenet", BaseURL: "https://x.test", APIPath: "/a/../api"}, fields: "apiPath"},
		{name: "key with spaces", in: SourceInput{Name: "n", Protocol: "usenet", BaseURL: "https://x.test", APIKey: SecretInput{Present: true, Value: "a b"}}, fields: "apiKey"},
		{name: "bad category", in: SourceInput{Name: "n", Protocol: "usenet", BaseURL: "https://x.test", Categories: &indexer.Categories{Movie: []int{0}}}, fields: "categories"},
		{name: "zero limit", in: SourceInput{Name: "n", Protocol: "usenet", BaseURL: "https://x.test", QueryLimitDay: OptionalInt{Set: true, Value: intp(0)}}, fields: "queryLimitDay"},
		{name: "priority out of range", in: SourceInput{Name: "n", Protocol: "usenet", BaseURL: "https://x.test", Priority: intp(5000)}, fields: "priority"},
		// A stored key stays with the address and API path it was saved for.
		{name: "stored key at the same address", in: SourceInput{Name: "example", BaseURL: "https://SOURCE.test/"}, current: &keyed},
		{name: "stored key at another address", in: SourceInput{Name: "example", BaseURL: "https://listener.test"}, current: &keyed, fields: "apiKey"},
		{name: "stored key at another API path", in: SourceInput{Name: "example", BaseURL: "https://source.test", APIPath: "/other"}, current: &keyed, fields: "apiKey"},
		{name: "new key at another address", in: SourceInput{Name: "example", BaseURL: "https://listener.test", APIKey: SecretInput{Present: true, Value: "k"}}, current: &keyed},
		{name: "cleared key at another address", in: SourceInput{Name: "example", BaseURL: "https://listener.test", APIKey: SecretInput{Present: true, Clear: true}}, current: &keyed},
	}
	for _, c := range cases {
		_, fe := s.validateSource(ctx, c.in, existing, c.current)
		if got := fieldsOf(fe); got != c.fields {
			t.Errorf("%s: invalid fields = %q, want %q (%+v)", c.name, got, c.fields, fe)
		}
	}

	// Defaults on create.
	ix, fe := s.validateSource(ctx, valid, existing, nil)
	if len(fe) > 0 || ix.APIPath != "/api" || !ix.Enabled || ix.QueryLimitDay != nil ||
		len(ix.Categories.Movie) != 1 || ix.Categories.Movie[0] != 2000 || ix.Categories.TV[0] != 5000 {
		t.Fatalf("defaults: %+v %+v", ix, fe)
	}
	// On update, what is omitted keeps its stored value; null clears a limit.
	upd, fe := s.validateSource(ctx, SourceInput{Name: "example", BaseURL: "https://source.test"}, existing, &stored)
	if len(fe) > 0 || upd.Protocol != "usenet" || upd.Priority != 3 || upd.Enabled || *upd.QueryLimitDay != 100 || upd.Categories.Movie[0] != 2040 {
		t.Fatalf("update kept: %+v %+v", upd, fe)
	}
	cleared, _ := s.validateSource(ctx, SourceInput{Name: "example", BaseURL: "https://source.test",
		QueryLimitDay: OptionalInt{Set: true}}, existing, &stored)
	if cleared.QueryLimitDay != nil {
		t.Fatalf("null did not clear the limit: %v", *cleared.QueryLimitDay)
	}
}

func TestSourceUnusable(t *testing.T) {
	now := time.Now()
	later, earlier := now.Add(time.Hour), now.Add(-time.Hour)
	cases := []struct {
		ix   store.Indexer
		want string
	}{
		{store.Indexer{}, ""},
		{store.Indexer{BackoffUntil: &earlier}, ""},
		{store.Indexer{BackoffUntil: &later}, "backing off until"},
		{store.Indexer{QueryLimitDay: intp(10), QueriesToday: 9}, ""},
		{store.Indexer{QueryLimitDay: intp(10), QueriesToday: 10}, "daily query limit reached"},
	}
	for i, c := range cases {
		got := sourceUnusable(c.ix, now)
		if (c.want == "") != (got == "") || !strings.HasPrefix(got, c.want) {
			t.Errorf("%d: %q, want %q", i, got, c.want)
		}
	}
}

// A release reference is the only way a console grab reaches a source's link:
// it opens only in the process that sealed it, only unmodified, only until it
// expires, and what it says beats whatever else the body claims.
func TestReleaseReferences(t *testing.T) {
	s := &Service{refs: processKey()}
	link := "https://source.test/api?t=get&id=abc&apikey=SECRETKEY"
	c := s.candidate(releaseFor(link), releaseProfile(), nil)
	if c.Release == "" || strings.Contains(c.Release, "SECRETKEY") {
		t.Fatalf("reference: %q", c.Release)
	}
	if strings.Contains(c.Source, "SECRETKEY") || !strings.Contains(c.Source, "id=abc") {
		t.Fatalf("shown source not redacted: %q", c.Source)
	}
	body, _ := json.Marshal(c)
	if strings.Contains(string(body), "SECRETKEY") {
		t.Fatalf("the key reached the JSON a browser gets: %s", body)
	}

	// The console sends back what it got, perhaps edited.
	var back Candidate
	_ = json.Unmarshal(body, &back)
	back.IndexerID, back.Protocol, back.Indexer = 999, "torrent", "someone else"
	if err := s.resolveCandidate(&back); err != nil {
		t.Fatal(err)
	}
	if back.link != link || back.IndexerID != 7 || back.Protocol != "usenet" || back.Indexer != "example" || back.GUID != "guid-1" {
		t.Fatalf("resolved from the body instead of the reference: %+v", back)
	}

	tampered := back
	mid := len(c.Release) / 2
	flip := byte('A')
	if c.Release[mid] == 'A' {
		flip = 'B'
	}
	tampered.Release = c.Release[:mid] + string(flip) + c.Release[mid+1:]
	if err := s.resolveCandidate(&tampered); !errors.Is(err, errStaleRelease) {
		t.Fatalf("tampered reference: %v", err)
	}
	other := &Service{refs: processKey()}
	if err := other.resolveCandidate(&Candidate{Release: c.Release}); !errors.Is(err, errStaleRelease) {
		t.Fatalf("a reference from another process opened: %v", err)
	}
	expired := s.sealRef(releaseRef{Link: link})
	ref, _ := s.openRef(expired)
	ref.Expires = time.Now().Add(-time.Minute).Unix()
	b, _ := json.Marshal(ref)
	ct, _, _ := s.refs.Seal("release", "ref", "v1", b)
	if _, err := s.openRef(encodeRef(ct)); !errors.Is(err, errStaleRelease) {
		t.Fatalf("an expired reference opened: %v", err)
	}

	// Without a reference the source is an admin-supplied link, accounted to no
	// source whatever the body says.
	manual := Candidate{Source: "magnet:?xt=urn:btih:abc", IndexerID: 7, GUID: "x"}
	if err := s.resolveCandidate(&manual); err != nil || manual.link != manual.Source || manual.IndexerID != 0 || manual.GUID != "" {
		t.Fatalf("manual link: %+v %v", manual, err)
	}
}
