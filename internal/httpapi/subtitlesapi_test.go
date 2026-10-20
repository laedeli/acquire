package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/laedeli/acquire/internal/app"
	"github.com/laedeli/acquire/internal/config"
)

// A search, a pick, a removal, asking for titles first and the settings are
// an admin's; a request to find is bounded and says what it lacks before
// anything is asked; a pick names its file; a tab is one of the five.
func TestSubtitleRoutesAreAdminOnlyAndBounded(t *testing.T) {
	cfg := config.Config{AdminRole: "zaentrum-admin", UserRole: "zaentrum-user"}
	s := &Server{cfg: cfg, svc: app.New(cfg, nil, nil, nil, nil, nil, nil)}
	as := func(role string, r *http.Request) *http.Request {
		return r.WithContext(context.WithValue(r.Context(), roleKey, []string{role}))
	}
	for name, h := range map[string]http.HandlerFunc{
		"search": s.searchSubtitle, "pick": s.pickSubtitle, "remove": s.removeSubtitle, "find": s.findSubtitles,
		"settings": s.getSubtitleSettings, "save settings": s.putSubtitleSettings,
	} {
		w := httptest.NewRecorder()
		h(w, as("zaentrum-user", httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`))))
		if w.Code != http.StatusForbidden {
			t.Errorf("%s as a user: %d", name, w.Code)
		}
	}
	keys := make([]map[string]string, maxSubtitleFinds+1)
	for i := range keys {
		keys[i] = map[string]string{"itemId": "i", "language": "de"}
	}
	big, _ := json.Marshal(map[string]any{"keys": keys})
	for body, want := range map[string]int{`{}`: 400, `{"keys":[]}`: 400, string(big): 400, `not json`: 400,
		`{"keys":[{"itemId":"i","language":"de"}]}`: http.StatusConflict} { // subtitles off: no account
		w := httptest.NewRecorder()
		s.findSubtitles(w, as("zaentrum-admin", httptest.NewRequest(http.MethodPost, "/api/subtitles/find", strings.NewReader(body))))
		if w.Code != want {
			t.Errorf("find %.40s: %d, want %d", body, w.Code, want)
		}
	}
	w := httptest.NewRecorder()
	s.pickSubtitle(w, as("zaentrum-admin", httptest.NewRequest(http.MethodPost, "/api/subtitles/i/de/pick", strings.NewReader(`{"release":"x"}`))))
	if w.Code != http.StatusBadRequest {
		t.Errorf("a pick without its file: %d", w.Code)
	}
	w = httptest.NewRecorder()
	s.subtitleList(w, as("zaentrum-user", httptest.NewRequest(http.MethodGet, "/api/subtitles?tab=everything", nil)))
	if w.Code != http.StatusBadRequest {
		t.Errorf("a tab that is none: %d", w.Code)
	}
}
