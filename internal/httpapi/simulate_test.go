package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/laedeli/acquire/internal/config"
	"github.com/laedeli/acquire/internal/release"
)

// adminReq builds a request already carrying the admin role, so the handler can
// be exercised without an OIDC issuer.
func adminReq(t *testing.T, body any) *http.Request {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/api/score/simulate", bytes.NewReader(raw))
	return r.WithContext(context.WithValue(r.Context(), roleKey, []string{"zaentrum-admin"}))
}

// The point of the endpoint: prove on a LIVE pod what the ranker will do,
// without grabbing anything to find out. An inline profile takes the store out
// of the path entirely.
func TestSimulateRanksAndExplains(t *testing.T) {
	s := &Server{cfg: config.Config{AdminRole: "zaentrum-admin"}}
	prof := release.DefaultProfile()
	req := adminReq(t, map[string]any{
		"profile": prof,
		"candidates": []release.Candidate{
			{Title: "Movie.2024.HDCAM.x264-GRP", Protocol: "usenet", SizeMb: 1500},
			{Title: "Movie.2024.1080p.WEB-DL.x265-FLUX", Protocol: "usenet", SizeMb: 7000},
			{Title: "Movie.2024.2160p.BluRay.REMUX.HEVC-FraMeSToR", Protocol: "usenet", SizeMb: 62000},
		},
	})
	w := httptest.NewRecorder()
	s.simulateScore(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var got struct {
		ProfileID string `json:"profileId"`
		Results   []struct {
			Title    string `json:"title"`
			Score    int    `json:"score"`
			Rejected bool   `json:"rejected"`
			Reason   string `json:"reason"`
			Info     struct {
				Source     string `json:"source"`
				Resolution string `json:"resolution"`
			} `json:"info"`
		} `json:"results"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.ProfileID != "(inline)" {
		t.Errorf("profileId = %q, want (inline)", got.ProfileID)
	}
	if len(got.Results) != 3 {
		t.Fatalf("got %d results", len(got.Results))
	}
	// Best first; the 4K remux wins, the cam is rejected and sorts last.
	if got.Results[0].Info.Resolution != "2160p" {
		t.Errorf("winner = %+v, want the 2160p remux", got.Results[0])
	}
	last := got.Results[len(got.Results)-1]
	if !last.Rejected || last.Info.Source != "cam" {
		t.Errorf("last = %+v, want the rejected cam", last)
	}
	// Every row must explain itself — that is the whole point.
	for _, r := range got.Results {
		if r.Reason == "" {
			t.Errorf("%s has no reason", r.Title)
		}
	}
}

func TestSimulateRequiresAdmin(t *testing.T) {
	s := &Server{cfg: config.Config{AdminRole: "zaentrum-admin"}}
	r := httptest.NewRequest(http.MethodPost, "/api/score/simulate", bytes.NewReader([]byte(`{}`)))
	r = r.WithContext(context.WithValue(r.Context(), roleKey, []string{"zaentrum-user"}))
	w := httptest.NewRecorder()
	s.simulateScore(w, r)
	if w.Code != http.StatusForbidden {
		t.Errorf("status %d, want 403 — the profile config is admin-only", w.Code)
	}
}

// Unbounded input on an admin endpoint is still a way to burn a 256Mi pod.
func TestSimulateRejectsEmptyAndOversizedInput(t *testing.T) {
	s := &Server{cfg: config.Config{AdminRole: "zaentrum-admin"}}
	for _, tc := range []struct {
		name string
		n    int
	}{{"empty", 0}, {"oversized", 201}} {
		cands := make([]release.Candidate, tc.n)
		for i := range cands {
			cands[i] = release.Candidate{Title: "M.2024.1080p.WEB-DL.x265-G", Protocol: "usenet", SizeMb: 4000}
		}
		w := httptest.NewRecorder()
		s.simulateScore(w, adminReq(t, map[string]any{
			"profile": release.DefaultProfile(), "candidates": cands}))
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", tc.name, w.Code)
		}
	}
}
