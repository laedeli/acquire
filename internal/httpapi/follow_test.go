package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/laedeli/acquire/internal/app"
	"github.com/laedeli/acquire/internal/config"
	"github.com/laedeli/acquire/internal/store"
)

// testServer is the API over a store in a schema of its own, with auth off
// (every caller an admin), as a developer runs it.
func testServer(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	dsn := os.Getenv("TEST_DSN")
	if dsn == "" {
		t.Skip("TEST_DSN not set; skipping database tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx, `DROP SCHEMA IF EXISTS acquire_httpapi_test CASCADE; CREATE SCHEMA acquire_httpapi_test;`); err != nil {
		t.Fatal(err)
	}
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	st, err := store.New(ctx, dsn+sep+"search_path=acquire_httpapi_test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	cfg := config.Config{AdminRole: "zaentrum-admin", UserRole: "zaentrum-user"}
	svc := app.New(cfg, st, nil, nil, nil, nil, nil)
	if err := svc.SeedSettings(ctx); err != nil {
		t.Fatal(err)
	}
	return NewServer(cfg, svc, st, nil), st
}

func call(t *testing.T, h http.Handler, method, target, body string, header ...string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	for i := 0; i+1 < len(header); i += 2 {
		r.Header.Set(header[i], header[i+1])
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w, out
}

// A series request follows its series and reads as following, with its
// progress; asking again answers the same request; the missing tab and the
// settings say what grabs on its own.
func TestRequestsFollowAndSayWhatGrabsOnItsOwn(t *testing.T) {
	s, _ := testServer(t)
	h := s.Handler()
	w, created := call(t, h, http.MethodPost, "/api/wanted",
		`{"tmdbId":9001,"mediaType":"series","title":"Example Saga","year":2026,"status":"fulfilled"}`)
	if w.Code != http.StatusCreated || created["titleId"] != "series:9001" || created["status"] != "following" {
		t.Fatalf("POST /api/wanted -> %d %s", w.Code, w.Body.String())
	}
	if p, _ := created["progress"].(map[string]any); p == nil || p["kind"] != "series" || created["autoGrab"] != false {
		t.Errorf("the created request's progress: %s", w.Body.String())
	}
	if w, again := call(t, h, http.MethodPost, "/api/wanted", `{"tmdbId":9001,"mediaType":"tv","title":"Example Saga"}`); w.Code != http.StatusCreated || again["id"] != created["id"] {
		t.Errorf("asked again: %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/wanted", nil))
	var list []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil || len(list) != 1 || list[0]["detail"] != "reading its episodes from TMDB" {
		t.Errorf("GET /api/wanted -> %s", w.Body.String())
	}
	if w, st := call(t, h, http.MethodGet, "/api/status?tmdbId=9001", ""); w.Code != 200 || st["status"] != "following" {
		t.Errorf("GET /api/status -> %s", w.Body.String())
	}

	_, missing := call(t, h, http.MethodGet, "/api/missing", "")
	if missing["autoGrab"] != false || missing["autoGrabNew"] != false || missing["newWithinDays"] != float64(14) {
		t.Errorf("GET /api/missing -> %v", missing)
	}
	_, doc := call(t, h, http.MethodGet, "/api/settings/search", "")
	if doc["autoGrabNew"] != false || doc["newWithinDays"] != float64(14) || doc["airDelayHours"] != float64(30) {
		t.Fatalf("GET /api/settings/search -> %v", doc)
	}
	rev := doc["revision"].(float64)
	body := `{"preferProtocol":"usenet","storageFloorGb":500,"maxConcurrentGrabs":3,"autoGrab":false,"autoGrabNew":true,"newWithinDays":7}`
	if w, saved := call(t, h, http.MethodPut, "/api/settings/search", body, "If-Match", strconv.Itoa(int(rev))); w.Code != 200 ||
		saved["autoGrabNew"] != true || saved["newWithinDays"] != float64(7) {
		t.Fatalf("PUT /api/settings/search -> %d %s", w.Code, w.Body.String())
	}
	_, missing = call(t, h, http.MethodGet, "/api/missing", "")
	if missing["autoGrabNew"] != true || missing["newWithinDays"] != float64(7) {
		t.Errorf("GET /api/missing after the save -> %v", missing)
	}
	if w, _ := call(t, h, http.MethodPut, "/api/settings/search",
		`{"preferProtocol":"usenet","storageFloorGb":500,"maxConcurrentGrabs":3,"newWithinDays":400}`); w.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(w.Body.String(), "newWithinDays") {
		t.Errorf("400 days -> %d %s", w.Code, w.Body.String())
	}
	if w, _ := call(t, h, http.MethodPut, "/api/settings/search",
		`{"preferProtocol":"usenet","storageFloorGb":500,"maxConcurrentGrabs":3,"airDelayHours":200}`); w.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(w.Body.String(), "airDelayHours") {
		t.Errorf("a delay of 200 hours -> %d %s", w.Code, w.Body.String())
	}
	if w, saved := call(t, h, http.MethodPut, "/api/settings/search", `{"airDelayHours":0}`); w.Code != 200 || saved["airDelayHours"] != float64(0) {
		t.Errorf("no delay -> %d %s", w.Code, w.Body.String())
	}
	// A console from before names neither newWithinDays nor airDelayHours:
	// they keep their values.
	if w, saved := call(t, h, http.MethodPut, "/api/settings/search",
		`{"preferProtocol":"usenet","storageFloorGb":500,"maxConcurrentGrabs":3,"autoGrabNew":true}`); w.Code != 200 ||
		saved["newWithinDays"] != float64(7) || saved["airDelayHours"] != float64(0) {
		t.Errorf("without newWithinDays and airDelayHours -> %d %s", w.Code, w.Body.String())
	}

	// Removing the request ends the follow.
	if w, _ := call(t, h, http.MethodDelete, "/api/wanted/"+created["id"].(string), ""); w.Code != http.StatusNoContent {
		t.Errorf("DELETE -> %d %s", w.Code, w.Body.String())
	}
	if w, _ := call(t, h, http.MethodDelete, "/api/wanted/"+created["id"].(string), ""); w.Code != http.StatusNotFound {
		t.Errorf("DELETE again -> %d", w.Code)
	}
	if ti, err := s.st.TitleByID(context.Background(), "series:9001"); err != nil || ti.Monitored || ti.Followed() {
		t.Errorf("the title after its request: %+v %v", ti, err)
	}
}
