package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/laedeli/acquire/internal/config"
)

// Grabbing targets and reconciling the holdings are an admin's, and a bulk
// grab is bounded before anything is queued.
func TestTargetRoutesAreAdminOnlyAndBounded(t *testing.T) {
	s := &Server{cfg: config.Config{AdminRole: "zaentrum-admin", UserRole: "zaentrum-user"}}
	asUser := func(r *http.Request) *http.Request {
		return r.WithContext(context.WithValue(r.Context(), roleKey, []string{"zaentrum-user"}))
	}
	asAdmin := func(r *http.Request) *http.Request {
		return r.WithContext(context.WithValue(r.Context(), roleKey, []string{"zaentrum-admin"}))
	}
	for name, h := range map[string]http.HandlerFunc{
		"grab": s.grabTargets, "pick": s.pickTarget, "reconcile": s.reconcileHoldings,
	} {
		w := httptest.NewRecorder()
		h(w, asUser(httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`))))
		if w.Code != http.StatusForbidden {
			t.Errorf("%s as a user: %d", name, w.Code)
		}
	}
	ids := make([]string, maxTargetGrabs+1)
	for i := range ids {
		ids[i] = "t"
	}
	big, _ := json.Marshal(map[string]any{"targetIds": ids})
	for body, want := range map[string]int{`{}`: 400, `{"targetIds":[]}`: 400, string(big): 400, `not json`: 400} {
		w := httptest.NewRecorder()
		s.grabTargets(w, asAdmin(httptest.NewRequest(http.MethodPost, "/api/targets/grab", strings.NewReader(body))))
		if w.Code != want {
			t.Errorf("grab %.40s: %d, want %d", body, w.Code, want)
		}
	}
	w := httptest.NewRecorder()
	s.pickTarget(w, asAdmin(httptest.NewRequest(http.MethodPost, "/api/targets/x/pick", strings.NewReader(`{"title":"x"}`))))
	if w.Code != http.StatusBadRequest {
		t.Errorf("a pick without its release: %d", w.Code)
	}
}
