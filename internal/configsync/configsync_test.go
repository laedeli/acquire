package configsync

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"sync"
	"testing"

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
	puts     []gateway.ConfigPut
	getErr   error
	putErr   error
	errors   []gateway.ApplyError
}

func (g *fakeGateway) Enabled() bool { return true }
func (g *fakeGateway) ConfigClients(context.Context) (gateway.ConfigState, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return gateway.ConfigState{Revision: g.revision}, g.getErr
}
func (g *fakeGateway) PutConfigClients(_ context.Context, body gateway.ConfigPut) (gateway.ApplyResult, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.putErr != nil {
		return gateway.ApplyResult{}, g.putErr
	}
	g.puts = append(g.puts, body)
	g.revision = body.Revision
	applied := []string{}
	for _, c := range body.Clients {
		applied = append(applied, c.ID)
	}
	return gateway.ApplyResult{Revision: body.Revision, Applied: applied, Errors: g.errors}, nil
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
