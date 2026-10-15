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
	"time"
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

// What an add that did not succeed says: nothing sent, an answer that took
// nothing (the client refusing the release itself, or out of reach), or an
// answer lost after the gateway may have taken it.
func TestAddSaysWhatBecameOfIt(t *testing.T) {
	answer := func(code int, body string) *httptest.Server {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(code)
			_, _ = w.Write([]byte(body))
		}))
		t.Cleanup(srv.Close)
		return srv
	}
	add := func(url string) error {
		_, err := New(url, nil).Add(context.Background(), AddRequest{Adapter: "nzbget", Source: "magnet:?xt=x"})
		return err
	}
	var ae *AddError
	if err := add(answer(502, `{"error":"nzbget append rejected the source (id=0)"}`).URL); !errors.As(err, &ae) || !ae.Rejected() || ae.Temporary() {
		t.Errorf("the client refused the release: %v", err)
	}
	if err := add(answer(502, `{"error":"nzbget: dial tcp: connection refused"}`).URL); !errors.As(err, &ae) || ae.Rejected() || !ae.Temporary() {
		t.Errorf("the client out of reach: %v", err)
	}
	if err := add(answer(400, `{"error":"client (or adapter) and source or payload_b64 are required"}`).URL); !errors.As(err, &ae) || ae.Temporary() || ae.Rejected() {
		t.Errorf("a bad request: %v", err)
	}
	if err := add(answer(202, `{"adapter":"nzbget"`).URL); !errors.Is(err, ErrAnswerLost) {
		t.Errorf("an answer that cannot be read: %v", err)
	}
	if err := add(answer(202, `{"adapter":"nzbget"}`).URL); !errors.Is(err, ErrAnswerLost) {
		t.Errorf("an answer without the client's job: %v", err)
	}
	dropped := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		conn, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			_ = conn.Close()
		}
	}))
	t.Cleanup(dropped.Close)
	if err := add(dropped.URL); !errors.Is(err, ErrAnswerLost) {
		t.Errorf("a connection dropped after the add was sent: %v", err)
	}
	gone := httptest.NewServer(http.NotFoundHandler())
	gone.Close()
	if err := add(gone.URL); !errors.Is(err, ErrUnreachable) || errors.Is(err, ErrAnswerLost) {
		t.Errorf("a gateway that is not there: %v", err)
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

func TestAddAllowsLargePayloadsMoreTime(t *testing.T) {
	c := New("http://gw", nil)
	if got := c.addHTTP(1024); got != c.HTTP {
		t.Fatalf("small add should use the normal client")
	}
	if got := c.addHTTP(67 << 20); got.Timeout < 2*time.Minute+c.HTTP.Timeout {
		t.Fatalf("large add timeout = %v, want at least %v", got.Timeout, 2*time.Minute+c.HTTP.Timeout)
	}
	if c.HTTP.Timeout != 15*time.Second {
		t.Fatalf("addHTTP must not change the shared client, got %v", c.HTTP.Timeout)
	}
}

func TestAddReportsBusyGateway(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "5")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	_, err := New(srv.URL, nil).Add(context.Background(), AddRequest{Adapter: "nzbget", Title: "t"})
	if !errors.Is(err, ErrGatewayBusy) {
		t.Fatalf("err = %v, want ErrGatewayBusy", err)
	}
}
