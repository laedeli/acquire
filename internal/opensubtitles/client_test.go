package opensubtitles

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeAPI is the API as far as the client uses it: a sign-in, searches,
// downloads, the account, and a 429 when asked to give one.
type fakeAPI struct {
	t        *testing.T
	mu       sync.Mutex
	srv      *httptest.Server
	logins   int
	searches []string
	tooMany  int // answer this many requests with 429 first
	quota    int
	tokenOK  string
	paths    []string
}

func newFake(t *testing.T) *fakeAPI {
	f := &fakeAPI{t: t, quota: 2, tokenOK: "token-1"}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeAPI) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.paths = append(f.paths, r.Method+" "+r.URL.Path)
	if r.Header.Get("Api-Key") != "key-1" || r.Header.Get("User-Agent") != "acquire v0.5.0" {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	if f.tooMany > 0 {
		f.tooMany--
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusTooManyRequests)
		return
	}
	authed := r.Header.Get("Authorization") == "Bearer "+f.tokenOK
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/login":
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["username"] != "someone" || body["password"] != "secret" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"message":"Error, invalid username/password"}`)
			return
		}
		f.logins++
		_, _ = io.WriteString(w, `{"user":{"allowed_downloads":20,"level":"Sub leecher","vip":false},"base_url":"`+
			strings.TrimPrefix(f.srv.URL, "http://")+`","token":"`+f.tokenOK+`","status":200}`)
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/subtitles":
		f.searches = append(f.searches, r.URL.RawQuery)
		_, _ = io.WriteString(w, `{"total_pages":1,"total_count":2,"page":1,"data":[
			{"id":"1","attributes":{"subtitle_id":"11","language":"de","download_count":5400,"hearing_impaired":false,
			 "votes":3,"ratings":8.5,"from_trusted":true,"ai_translated":false,"machine_translated":false,
			 "release":"Some.Film.2019.1080p.WEB-DL.DDP5.1.H.264-GRP","moviehash_match":true,"upload_date":"2020-01-02T03:04:05Z",
			 "feature_details":{"feature_type":"Movie","imdb_id":1234567},"files":[{"file_id":901,"file_name":"Some.Film.2019.de.srt"}]}},
			{"id":"2","attributes":{"subtitle_id":"12","language":"zh-CN","download_count":30,"ai_translated":true,
			 "release":"Some Film","files":[{"file_id":902,"file_name":"a.srt"},{"file_id":903,"file_name":"b.srt"}]}},
			{"id":"3","attributes":{"subtitle_id":"13","language":"en","files":[]}}]}`)
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/download":
		if !authed {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if f.quota == 0 {
			w.WriteHeader(http.StatusNotAcceptable)
			_, _ = io.WriteString(w, `{"requests":21,"remaining":-1,"message":"You have downloaded your allowed 20 subtitles for 24h.","reset_time_utc":"2026-10-21T00:00:00.000Z"}`)
			return
		}
		f.quota--
		_, _ = io.WriteString(w, `{"link":"https://files.example.org/x.srt","file_name":"x.srt","requests":19,"remaining":`+
			itoa(f.quota)+`,"reset_time_utc":"2026-10-21T00:00:00.000Z"}`)
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/infos/user":
		if !authed {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = io.WriteString(w, `{"data":{"allowed_downloads":20,"level":"Sub leecher","vip":false,"downloads_count":3,"remaining_downloads":17}}`)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

func (f *fakeAPI) client() *Client {
	return New(Config{BaseURL: f.srv.URL + "/api/v1", APIKey: "key-1", Username: "someone", Password: "secret",
		UserAgent: "acquire v0.5.0", MinInterval: time.Millisecond})
}

// Without an account and a key the client is off and says what is missing.
func TestAClientWithoutCredentialsIsOff(t *testing.T) {
	c := New(Config{APIKey: "k"})
	if c.Enabled() || strings.Join(c.Missing(), ",") != "username,password" {
		t.Errorf("enabled %v, missing %v", c.Enabled(), c.Missing())
	}
	if _, err := c.Search(context.Background(), Query{}); !errors.Is(err, ErrDisabled) {
		t.Errorf("a search: %v", err)
	}
}

// A search needs no sign-in, sends its parameters sorted and in lower case
// (ids without tt and leading zeros, languages sorted), always asking for full
// subtitles and showing the machine-translated ones flagged, and answers each
// subtitle's first file with what the API says of it; one without a file is
// left out.
func TestASearch(t *testing.T) {
	f := newFake(t)
	c := f.client()
	p, err := c.Search(context.Background(), Query{Languages: []string{"zh-CN", "de", "en", "de"}, MovieHash: "8E245D9679D31E12",
		IMDbID: "tt0123456", Type: "Movie"})
	if err != nil {
		t.Fatal(err)
	}
	want := "ai_translated=include&foreign_parts_only=exclude&imdb_id=123456&languages=de%2Cen%2Czh-cn&machine_translated=include" +
		"&moviehash=8e245d9679d31e12&type=movie"
	if len(f.searches) != 1 || f.searches[0] != want {
		t.Errorf("the query: %v, want %s", f.searches, want)
	}
	if f.logins != 0 {
		t.Errorf("a search signed in")
	}
	if len(p.Results) != 2 {
		t.Fatalf("results: %+v", p.Results)
	}
	de, zh := p.Results[0], p.Results[1]
	if de.FileID != 901 || de.Language != "de" || de.Downloads != 5400 || !de.HashMatch || !de.FromTrusted || de.Rating != 8.5 ||
		de.Release != "Some.Film.2019.1080p.WEB-DL.DDP5.1.H.264-GRP" || de.FileName != "Some.Film.2019.de.srt" ||
		de.FeatureType != "movie" || de.IMDbID != 1234567 || de.Uploaded.Year() != 2020 || de.Files != 1 {
		t.Errorf("the German one: %+v", de)
	}
	if zh.Language != "zh-cn" || !zh.AITranslated || zh.Files != 2 {
		t.Errorf("the Chinese one: %+v", zh)
	}
	ep := Query{Languages: []string{"en"}, ParentTMDBID: 1396, Season: 2, Episode: 5, Type: "episode"}
	if got := ep.Values().Encode(); !strings.Contains(got, "episode_number=5") || !strings.Contains(got, "parent_tmdb_id=1396") ||
		!strings.Contains(got, "season_number=2") || strings.Contains(got, "imdb_id") {
		t.Errorf("an episode's query: %s", got)
	}
}

// A download signs in first (once), and answers the link and the day's
// quota; with none left it is a QuotaError saying when the count starts
// over. The account says its downloads.
func TestADownloadAndTheQuota(t *testing.T) {
	f := newFake(t)
	c := f.client()
	ctx := context.Background()
	l, err := c.Download(ctx, 901)
	if err != nil {
		t.Fatal(err)
	}
	if l.URL != "https://files.example.org/x.srt" || !l.Quota.Known || l.Quota.Remaining != 1 || l.Quota.Allowed != 20 ||
		l.Quota.ResetAt.Format(time.RFC3339) != "2026-10-21T00:00:00Z" {
		t.Errorf("the link: %+v", l)
	}
	if _, err := c.Download(ctx, 902); err != nil {
		t.Fatal(err)
	}
	_, err = c.Download(ctx, 903)
	var qe *QuotaError
	if !errors.As(err, &qe) || qe.ResetAt.IsZero() || !strings.Contains(err.Error(), "2026-10-21 00:00 UTC") {
		t.Errorf("no downloads left: %v", err)
	}
	if q := c.LastQuota(); !q.Known || q.Remaining != 0 {
		t.Errorf("the quota after: %+v", q)
	}
	if f.logins != 1 {
		t.Errorf("%d sign-ins, want one kept", f.logins)
	}
	u, q, err := c.Account(ctx)
	if err != nil || u.AllowedDownloads != 20 || u.Level != "Sub leecher" || q.Remaining != 17 {
		t.Errorf("the account: %+v %+v %v", u, q, err)
	}
}

// A token the API no longer takes is asked for again, once; a sign-in it
// refuses says so without the credentials.
func TestATokenIsRenewedAndARefusedSignInSaysSo(t *testing.T) {
	f := newFake(t)
	c := f.client()
	if _, err := c.Login(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.tokenOK = "token-2" // the token the client holds is refused; a sign-in hands out this one
	f.mu.Unlock()
	if _, q, err := c.Account(context.Background()); err != nil || q.Remaining != 17 || f.logins != 2 {
		t.Errorf("the account with a refused token: %+v %v, %d sign-ins", q, err, f.logins)
	}
	bad := New(Config{BaseURL: f.srv.URL + "/api/v1", APIKey: "key-1", Username: "someone", Password: "wrong",
		UserAgent: "acquire v0.5.0", MinInterval: time.Millisecond})
	_, err := bad.Login(context.Background())
	if err == nil || strings.Contains(err.Error(), "wrong") || !strings.Contains(err.Error(), "refused the sign-in") {
		t.Errorf("a refused sign-in: %v", err)
	}
}

// A 429 is waited out as its Retry-After says, and the request sent again.
func TestTooManyRequestsIsWaitedOut(t *testing.T) {
	f := newFake(t)
	f.tooMany = 1
	c := f.client()
	start := time.Now()
	if _, err := c.Search(context.Background(), Query{Languages: []string{"de"}, IMDbID: "1"}); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) < 900*time.Millisecond {
		t.Errorf("the 429 was not waited out")
	}
	if retryAfter("120") != maxRetryAfter || retryAfter("x") != time.Second || retryAfter("3") != 3*time.Second {
		t.Error("Retry-After")
	}
}

// Requests are paced: the second waits the interval after the first.
func TestRequestsArePaced(t *testing.T) {
	f := newFake(t)
	c := New(Config{BaseURL: f.srv.URL + "/api/v1", APIKey: "key-1", Username: "someone", Password: "secret",
		UserAgent: "acquire v0.5.0", MinInterval: 300 * time.Millisecond})
	start := time.Now()
	for i := 0; i < 3; i++ {
		if _, err := c.Search(context.Background(), Query{Languages: []string{"de"}, IMDbID: "1"}); err != nil {
			t.Fatal(err)
		}
	}
	if d := time.Since(start); d < 600*time.Millisecond {
		t.Errorf("three requests in %v", d)
	}
}

// A link that is no https address is never fetched, and neither is a file
// too large: both are the file's (FileError); a link answered with no file
// says its status.
func TestFetchTakesHTTPSOnly(t *testing.T) {
	c := New(Config{UserAgent: "acquire v0.5.0"})
	var fe *FileError
	if _, err := c.Fetch(context.Background(), "http://files.example.org/x.srt"); !errors.As(err, &fe) {
		t.Errorf("an http link: %v", err)
	}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/gone.srt" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(make([]byte, MaxFileBytes+1))
	}))
	defer srv.Close()
	c = New(Config{UserAgent: "acquire v0.5.0", HTTPClient: srv.Client()})
	if _, err := c.Fetch(context.Background(), srv.URL+"/large.srt"); !errors.As(err, &fe) {
		t.Errorf("a file too large: %v", err)
	}
	var ae *Error
	if _, err := c.Fetch(context.Background(), srv.URL+"/gone.srt"); !errors.As(err, &ae) || ae.Status != http.StatusNotFound {
		t.Errorf("a link answered 404: %v", err)
	}
}
