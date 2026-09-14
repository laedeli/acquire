package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/laedeli/acquire/internal/app"
	"github.com/laedeli/acquire/internal/config"
	"github.com/laedeli/acquire/internal/gateway"
	"github.com/laedeli/acquire/internal/store"
)

func TestConfigRoutesAreRegistered(t *testing.T) {
	s := &Server{}
	routes := map[string]bool{}
	_ = chi.Walk(s.Handler().(chi.Router), func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		routes[method+" "+route] = true
		return nil
	})
	for _, want := range []string{
		"GET /api/setup",
		"GET /api/download-clients",
		"POST /api/download-clients",
		"GET /api/download-clients/types",
		"POST /api/download-clients/test",
		"PUT /api/download-clients/{id}",
		"DELETE /api/download-clients/{id}",
		"GET /api/settings/search",
		"PUT /api/settings/search",
	} {
		if !routes[want] {
			t.Errorf("%s is not routed", want)
		}
	}
}

// Configuration is admin-only: a signed-in user without the admin role must
// not read client addresses, the checklist, or change anything.
func TestConfigRoutesRequireAdmin(t *testing.T) {
	s := &Server{cfg: config.Config{AdminRole: "zaentrum-admin"}}
	handlers := map[string]http.HandlerFunc{
		"setup":    s.setup,
		"list":     s.listDownloadClients,
		"types":    s.downloadClientTypes,
		"create":   s.createDownloadClient,
		"update":   s.updateDownloadClient,
		"delete":   s.deleteDownloadClient,
		"test":     s.testDownloadClient,
		"settings": s.getSearchSettings,
		"put":      s.putSearchSettings,
		"indexers": s.listIndexers,
	}
	for name, h := range handlers {
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`))
		r = r.WithContext(context.WithValue(r.Context(), roleKey, []string{"zaentrum-user"}))
		w := httptest.NewRecorder()
		h(w, r)
		if w.Code != http.StatusForbidden {
			t.Errorf("%s: %d for a non-admin, want 403", name, w.Code)
		}
	}
}

func TestIfMatch(t *testing.T) {
	cases := map[string]struct {
		rev int64
		ok  bool
	}{
		"":      {0, true},
		"*":     {0, true},
		"7":     {7, true},
		`"7"`:   {7, true},
		`W/"7"`: {7, true},
		"seven": {0, false},
		"-1":    {0, false},
	}
	for h, want := range cases {
		r := httptest.NewRequest(http.MethodPut, "/", nil)
		if h != "" {
			r.Header.Set("If-Match", h)
		}
		rev, err := ifMatch(r)
		if (err == nil) != want.ok || rev != want.rev {
			t.Errorf("If-Match %q = %d, %v", h, rev, err)
		}
	}
}

func TestConfigErrorStatuses(t *testing.T) {
	cases := []struct {
		err  error
		code int
	}{
		{&app.ValidationError{Fields: []app.FieldError{{Entity: "client", ID: "nzbget", Field: "baseUrl", Message: "bad"}}}, http.StatusUnprocessableEntity},
		{store.ErrNotFound, http.StatusNotFound},
		{store.ErrStale, http.StatusConflict},
		{store.ErrExists, http.StatusConflict},
		{store.ErrInFlight, http.StatusConflict},
		{fmt.Errorf("seal: %w", app.ErrNoKey), http.StatusConflict},
		{gateway.ErrNoConfigAPI, http.StatusBadGateway},
		{errors.New("boom"), http.StatusInternalServerError},
	}
	for _, c := range cases {
		w := httptest.NewRecorder()
		writeConfigError(w, c.err)
		if w.Code != c.code {
			t.Errorf("%v: %d, want %d", c.err, w.Code, c.code)
		}
	}

	// The 422 body is the field list the console maps onto its form.
	w := httptest.NewRecorder()
	writeConfigError(w, &app.ValidationError{Fields: []app.FieldError{{Entity: "client", ID: "nzbget", Field: "baseUrl", Message: "bad"}}})
	var body struct {
		FieldErrors []map[string]string `json:"fieldErrors"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || len(body.FieldErrors) != 1 {
		t.Fatalf("422 body: %s", w.Body.String())
	}
	fe := body.FieldErrors[0]
	if fe["entity"] != "client" || fe["id"] != "nzbget" || fe["field"] != "baseUrl" || fe["message"] != "bad" {
		t.Fatalf("field error shape: %v", fe)
	}
}
