package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A payload add carries the content and the client, and no source link at all.
func TestAddSendsPayloadAndClient(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"adapter":"nzbget","client_job_id":"12"}`))
	}))
	defer srv.Close()

	res, err := New(srv.URL, nil).Add(context.Background(), AddRequest{
		Adapter: "nzbget", Client: "nzbget", Title: "Example", WantedItemID: "w_1",
		PayloadB64: "PG56Yj4=", PayloadName: "example.nzb", Category: "acquire", SavePath: "/downloads",
	})
	if err != nil || res.ClientJobID != "12" {
		t.Fatalf("add: %+v %v", res, err)
	}
	if got["client"] != "nzbget" || got["adapter"] != "nzbget" || got["payload_b64"] != "PG56Yj4=" ||
		got["payload_name"] != "example.nzb" || got["category"] != "acquire" || got["save_path"] != "/downloads" {
		t.Fatalf("body = %v", got)
	}
	if _, ok := got["source"]; ok {
		t.Fatalf("a payload add must not send a source: %v", got)
	}
}

func TestAddReportsUnknownClient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"no such client: nzbget"}`))
	}))
	defer srv.Close()
	_, err := New(srv.URL, nil).Add(context.Background(), AddRequest{Adapter: "nzbget", Source: "magnet:?xt=x"})
	if !errors.Is(err, ErrUnknownClient) {
		t.Fatalf("err = %v, want ErrUnknownClient", err)
	}
}

func TestConfigAPIAbsentIsDistinguishable(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	gw := New(srv.URL, nil)
	if _, err := gw.ConfigTypes(context.Background()); !errors.Is(err, ErrNoConfigAPI) {
		t.Errorf("types: %v", err)
	}
	if _, err := gw.ConfigClients(context.Background()); !errors.Is(err, ErrNoConfigAPI) {
		t.Errorf("clients: %v", err)
	}
	if _, err := gw.PutConfigClients(context.Background(), ConfigPut{}); !errors.Is(err, ErrNoConfigAPI) {
		t.Errorf("put: %v", err)
	}
}

func TestPutConfigClientsRoundTrip(t *testing.T) {
	var body ConfigPut
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/api/v1/config/clients" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = w.Write([]byte(`{"revision":7,"applied":["nzbget"],"errors":[{"id":"worker","message":"collides with env"}]}`))
	}))
	defer srv.Close()

	res, err := New(srv.URL, nil).PutConfigClients(context.Background(), ConfigPut{Revision: 7})
	if err != nil {
		t.Fatal(err)
	}
	if res.Revision != 7 || len(res.Applied) != 1 || len(res.Errors) != 1 || res.Errors[0].ID != "worker" {
		t.Fatalf("result = %+v", res)
	}
	// An empty set is sent as [] — "replace everything with nothing" — never null.
	if body.Clients == nil {
		t.Fatal("clients was sent as null")
	}
}

// The decrypted secret must not reach a log line through any formatting path.
func TestConfigClientNeverPrintsItsSecret(t *testing.T) {
	c := ConfigClient{ID: "nzbget", Type: "nzbget", Auth: "basic", Username: "u", Secret: "hunter2"}
	var logged strings.Builder
	slog.New(slog.NewTextHandler(&logged, nil)).Info("push", "client", c)
	for _, s := range []string{
		fmt.Sprint(c), fmt.Sprintf("%v", c), fmt.Sprintf("%+v", c), fmt.Sprintf("%#v", c), fmt.Sprintf("%s", c),
		fmt.Sprintf("%v", []ConfigClient{c}), logged.String(),
	} {
		if strings.Contains(s, "hunter2") {
			t.Errorf("secret leaked: %s", s)
		}
	}
}
