package configsync

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"log"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/laedeli/acquire/internal/gateway"
	"github.com/laedeli/acquire/internal/secretbox"
	"github.com/laedeli/acquire/internal/store"
)

type fakeStore struct {
	rev     int64
	clients []store.DownloadClient
}

func (f *fakeStore) ClientsConfig(context.Context) (int64, []store.DownloadClient, error) {
	return f.rev, f.clients, nil
}
func (f *fakeStore) ClientsRevision(context.Context) (int64, error) { return f.rev, nil }

type fakeGateway struct {
	mu       sync.Mutex
	revision int64
	running  []gateway.ConfigClientView
	puts     []gateway.ConfigPut
	getErr   error
	putErr   error
	errors   []gateway.ApplyError
}

func (g *fakeGateway) Enabled() bool { return true }
func (g *fakeGateway) ConfigClients(context.Context) (gateway.ConfigState, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return gateway.ConfigState{Revision: g.revision, Clients: g.running}, g.getErr
}

// PutConfigClients answers as the gateway does: a push with per-client errors
// applies its valid entries but keeps the previous revision in effect.
func (g *fakeGateway) PutConfigClients(_ context.Context, body gateway.ConfigPut) (gateway.ApplyResult, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.putErr != nil {
		return gateway.ApplyResult{}, g.putErr
	}
	g.puts = append(g.puts, body)
	if len(g.errors) == 0 {
		g.revision = body.Revision
	}
	applied := []string{}
	for _, c := range body.Clients {
		applied = append(applied, c.ID)
	}
	return gateway.ApplyResult{Revision: g.revision, Applied: applied, Errors: g.errors}, nil
}

func (g *fakeGateway) putCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.puts)
}

func box(t *testing.T) *secretbox.Box {
	t.Helper()
	raw := make([]byte, 32)
	_, _ = rand.Read(raw)
	b, err := secretbox.New(base64.StdEncoding.EncodeToString(raw), "")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func sealed(t *testing.T, b *secretbox.Box, c store.DownloadClient, secret string) store.DownloadClient {
	t.Helper()
	table, id, field := store.ClientSecretAAD(c.ID)
	ct, kid, err := b.Seal(table, id, field, []byte(secret))
	if err != nil {
		t.Fatal(err)
	}
	c.SecretCT, c.SecretKID = ct, kid
	return c
}

func TestPushSendsEnabledClientsWithDecryptedSecrets(t *testing.T) {
	b := box(t)
	st := &fakeStore{rev: 9, clients: []store.DownloadClient{
		sealed(t, b, store.DownloadClient{ID: "qbittorrent", Type: "qbittorrent", Auth: "basic", Username: "u",
			Protocols: []string{"torrent"}, Category: "acquire", RemotePath: "/downloads", Enabled: true}, "pw"),
		{ID: "disabled", Type: "nzbget", Auth: "none", Enabled: false},
		{ID: "nzbget", Type: "nzbget", Auth: "none", Protocols: []string{"usenet"}, Enabled: true},
	}}
	gw := &fakeGateway{}
	r := New(st, gw, b, 0)

	res, err := r.Push(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(gw.puts) != 1 || gw.puts[0].Revision != 9 {
		t.Fatalf("puts = %+v", gw.puts)
	}
	got := gw.puts[0].Clients
	if len(got) != 2 || got[0].ID != "nzbget" || got[1].ID != "qbittorrent" {
		t.Fatalf("pushed %+v; want the two enabled clients sorted by id", got)
	}
	if got[1].Secret != "pw" || got[1].SavePath != "/downloads" || got[1].Category != "acquire" {
		t.Fatalf("client not mapped: %#v secret-ok=%v", got[1], got[1].Secret == "pw")
	}
	if len(res.Applied) != 2 || !r.Status().InSync() {
		t.Fatalf("status after push: %+v", r.Status())
	}
}

// A secret that cannot be opened must stop the whole push. A partial set would
// remove that client from a running gateway.
func TestUnopenableSecretPushesNothing(t *testing.T) {
	writer, reader := box(t), box(t) // a rotation without the previous key
	st := &fakeStore{rev: 3, clients: []store.DownloadClient{
		sealed(t, writer, store.DownloadClient{ID: "nzbget", Type: "nzbget", Auth: "basic", Enabled: true}, "pw"),
		{ID: "qbittorrent", Type: "qbittorrent", Auth: "none", Enabled: true},
	}}
	gw := &fakeGateway{}
	r := New(st, gw, reader, 0)
	if _, err := r.Push(context.Background()); err == nil {
		t.Fatal("push succeeded with an unopenable secret")
	}
	if len(gw.puts) != 0 {
		t.Fatalf("a partial set was pushed: %+v", gw.puts)
	}
	if r.Status().LastError == "" {
		t.Fatal("the failure is not reported")
	}
}

func TestCheckPushesOnlyWhenTheRevisionDiffers(t *testing.T) {
	st := &fakeStore{rev: 4, clients: []store.DownloadClient{{ID: "nzbget", Type: "nzbget", Auth: "none", Enabled: true}}}
	gw := &fakeGateway{revision: 4}
	r := New(st, gw, nil, 0)
	ctx := context.Background()

	if err := r.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if len(gw.puts) != 0 {
		t.Fatal("pushed although the gateway already runs this revision")
	}

	// The gateway restarted: it runs nothing.
	gw.revision = 0
	if err := r.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if len(gw.puts) != 1 || gw.puts[0].Revision != 4 {
		t.Fatalf("restart not repaired: %+v", gw.puts)
	}
}

func TestStatusDistinguishesNoConfigAPIFromUnreachable(t *testing.T) {
	st := &fakeStore{rev: 1}
	gw := &fakeGateway{getErr: gateway.ErrNoConfigAPI}
	r := New(st, gw, nil, 0)
	_ = r.Check(context.Background())
	s := r.Status()
	if !s.Reachable || s.ConfigAPI || s.InSync() {
		t.Fatalf("no config API: %+v", s)
	}

	gw.getErr = errors.New("connection refused")
	_ = r.Check(context.Background())
	s = r.Status()
	if s.Reachable || s.InSync() {
		t.Fatalf("unreachable: %+v", s)
	}
}

func TestApplyErrorsAreNotInSync(t *testing.T) {
	st := &fakeStore{rev: 2, clients: []store.DownloadClient{{ID: "nzbget", Type: "nzbget", Auth: "none", Enabled: true}}}
	gw := &fakeGateway{errors: []gateway.ApplyError{{ID: "nzbget", Message: "collides with an env client"}}}
	r := New(st, gw, nil, 0)
	if _, err := r.Push(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s := r.Status(); s.InSync() || len(s.Errors) != 1 {
		t.Fatalf("per-client apply error hidden: %+v", s)
	}
}

// A refused push leaves the gateway on its previous revision, so every check
// sees a difference. Sending the same refused set again every interval, and
// logging it each time, helps nobody: the repeat backs off and is logged once.
func TestRefusedPushBacksOffAndIsLoggedOnce(t *testing.T) {
	var logs bytes.Buffer
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	st := &fakeStore{rev: 5, clients: []store.DownloadClient{{ID: "nzbget", Type: "nzbget", Auth: "none", Enabled: true}}}
	envClient := []gateway.ConfigClientView{{ID: "nzbget", Type: "nzbget", Source: "env"}}
	gw := &fakeGateway{revision: 2, running: envClient,
		errors: []gateway.ApplyError{{ID: "nzbget", Message: "id is owned by an environment client"}}}
	r := New(st, gw, nil, 30*time.Second)
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	r.now = func() time.Time { return now }
	ctx := context.Background()

	if _, err := r.Push(ctx); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if err := r.Check(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if n := gw.putCount(); n != 1 {
		t.Fatalf("%d pushes within the first wait, want the one refused push", n)
	}
	if s := r.Status(); s.InSync() || len(s.Errors) != 1 {
		t.Fatalf("holding back must not hide the refusal: %+v", s)
	}

	// The wait runs out: offered again, and refused again — the same outcome is
	// not logged a second time, and the next wait is longer.
	now = now.Add(31 * time.Second)
	_ = r.Check(ctx)
	if n := gw.putCount(); n != 2 {
		t.Fatalf("%d pushes after the wait, want 2", n)
	}
	now = now.Add(31 * time.Second)
	_ = r.Check(ctx)
	if n := gw.putCount(); n != 2 {
		t.Fatalf("%d pushes, want the second wait to be longer than the first", n)
	}
	if n := strings.Count(logs.String(), "configsync pushed"); n != 1 {
		t.Fatalf("the same refusal was logged %d times:\n%s", n, logs.String())
	}

	// The gateway restarts with its environment changed: pushed straight away.
	gw.mu.Lock()
	gw.running, gw.errors = nil, nil
	gw.mu.Unlock()
	if err := r.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if n := gw.putCount(); n != 3 || !r.Status().InSync() {
		t.Fatalf("a changed gateway was not pushed to at once: %d pushes, %+v", gw.putCount(), r.Status())
	}
	if n := strings.Count(logs.String(), "configsync pushed"); n != 2 {
		t.Fatalf("the clean push was not logged:\n%s", logs.String())
	}
}

// A new revision is never held back by an earlier refusal.
func TestNewRevisionIsPushedDespiteAnEarlierRefusal(t *testing.T) {
	log.SetOutput(io.Discard)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	st := &fakeStore{rev: 5, clients: []store.DownloadClient{{ID: "nzbget", Type: "nzbget", Auth: "none", Enabled: true}}}
	gw := &fakeGateway{revision: 2, errors: []gateway.ApplyError{{ID: "nzbget", Message: "refused"}}}
	r := New(st, gw, nil, 30*time.Second)
	ctx := context.Background()
	if _, err := r.Push(ctx); err != nil {
		t.Fatal(err)
	}
	_ = r.Check(ctx)
	st.rev = 6 // an admin renamed the client
	_ = r.Check(ctx)
	if n := gw.putCount(); n != 2 || gw.puts[1].Revision != 6 {
		t.Fatalf("the new revision was held back: %+v", gw.puts)
	}
}
