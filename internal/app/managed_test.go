package app

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/laedeli/acquire/internal/store"
)

// declaration is the shape the addon chart renders into ACQUIRE_MANAGED_CLIENTS.
func declaration(t *testing.T, clients ...map[string]any) string {
	t.Helper()
	b, err := json.Marshal(clients)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func chartQB(password string) map[string]any {
	return map[string]any{
		"id": "qbittorrent", "type": "qbittorrent", "baseUrl": "http://qbittorrent:8080",
		"auth": "basic", "username": "acquire", "secret": password,
		"protocols": []string{"torrent"}, "category": "acquire",
		"remotePath": "/downloads/complete", "localPath": "/var/lib/acquire/downloads/qbittorrent/complete",
	}
}

func chartNZB(password string) map[string]any {
	return map[string]any{
		"id": "nzbget", "type": "nzbget", "baseUrl": "http://nzbget:6789",
		"auth": "basic", "username": "acquire", "secret": password,
		"protocols": []string{"usenet"}, "category": "acquire",
		"remotePath": "/downloads/complete", "localPath": "/var/lib/acquire/downloads/nzbget/complete",
	}
}

func TestParseManaged(t *testing.T) {
	if in, err := parseManaged("  "); err != nil || in != nil {
		t.Fatalf("empty declaration: %v %v", in, err)
	}
	in, err := parseManaged(declaration(t, chartQB("pw")))
	if err != nil || len(in) != 1 || in[0].ID != "qbittorrent" || !in[0].Secret.replaces() || in[0].Secret.Value != "pw" {
		t.Fatalf("one client: %+v %v", in, err)
	}
	for name, raw := range map[string]string{
		"not a list":    `{"id":"x"}`,
		"unknown field": `[{"id":"x","password":"hunter2"}]`,
		"two lists":     `[] []`,
		"broken":        `[{"id":`,
	} {
		_, err := parseManaged(raw)
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		if strings.Contains(err.Error(), "hunter2") {
			t.Errorf("%s: the error echoes the declaration: %v", name, err)
		}
	}
}

// The declaration is held to the console's own rules; an invalid client is
// left out with its reason, but its id still counts as declared.
func TestDeclaredClientsAreValidated(t *testing.T) {
	s := testService()
	bad := chartNZB("pw")
	bad["baseUrl"] = "http://169.254.169.254"
	noSecret := chartQB("")
	noSecret["id"] = "qbittorrent-2"
	delete(noSecret, "secret")
	unnamed := chartNZB("pw")
	delete(unnamed, "id")

	out, ids, skipped, err := s.declaredClients(context.Background(),
		declaration(t, chartQB("pw"), bad, noSecret, unnamed, chartQB("again")))
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].client.ID != "qbittorrent" {
		t.Fatalf("valid clients = %+v", out)
	}
	c := out[0].client
	if !c.Managed || !c.Enabled || c.Category != "acquire" || c.RemotePath != "/downloads/complete" ||
		c.LocalPath != "/var/lib/acquire/downloads/qbittorrent/complete" || out[0].secret.Value != "pw" {
		t.Errorf("stored row = %+v", c)
	}
	if !reflect.DeepEqual(ids, map[string]bool{"qbittorrent": true, "nzbget": true, "qbittorrent-2": true}) {
		t.Errorf("declared ids = %v", ids)
	}
	want := []string{"nzbget: baseUrl", "qbittorrent-2: secret", "(no id)", "qbittorrent: declared more than once"}
	if len(skipped) != len(want) {
		t.Fatalf("skipped = %q", skipped)
	}
	for i, w := range want {
		if !strings.HasPrefix(skipped[i], w) {
			t.Errorf("skipped[%d] = %q, want it to start with %q", i, skipped[i], w)
		}
	}
}

func TestPlanManaged(t *testing.T) {
	declared := func(id, typ string) managedWrite {
		return managedWrite{client: store.DownloadClient{ID: id, Type: typ, BaseURL: "http://" + id, Auth: "basic",
			Username: "acquire", Protocols: []string{"torrent"}, Category: "acquire", RemotePath: "/downloads",
			LocalPath: "/media/" + id, Enabled: true, Managed: true}, secret: SecretInput{Present: true, Value: "pw"}}
	}
	// The stored rows: each one as declared, then changed the way it differs.
	row := func(id string, change func(*store.DownloadClient)) store.DownloadClient {
		c := declared(id, "qbittorrent").client
		if change != nil {
			change(&c)
		}
		return c
	}
	admins := func(c *store.DownloadClient) { c.Managed = false }
	inLine := row("same", nil)
	admin := row("admin", admins)
	stored := []store.DownloadClient{
		inLine,
		row("moved", func(c *store.DownloadClient) { c.BaseURL = "http://old-address" }),
		admin,
		row("admin-busy", admins),
		row("other-type", func(c *store.DownloadClient) { c.Type, c.Managed = "nzbget", false }),
		row("gone", nil),
		row("gone-busy", nil),
		row("console", admins),
	}

	// "invalid" was declared but did not validate: no write, and no removal.
	ids := map[string]bool{"same": true, "moved": true, "admin": true, "admin-busy": true, "other-type": true, "new": true, "invalid": true}
	busy := func(id string) (int, error) {
		if id == "admin-busy" {
			return 2, nil
		}
		return 0, nil
	}
	same := func(store.DownloadClient, SecretInput) bool { return true }
	plan, err := planManaged([]managedWrite{
		declared("same", "qbittorrent"), declared("moved", "qbittorrent"), declared("admin", "qbittorrent"),
		declared("admin-busy", "qbittorrent"), declared("other-type", "qbittorrent"), declared("new", "qbittorrent"),
	}, ids, stored, busy, same)
	if err != nil {
		t.Fatal(err)
	}
	idsOf := func(ws []managedWrite) []string {
		var out []string
		for _, w := range ws {
			out = append(out, w.client.ID)
		}
		return out
	}
	if got := idsOf(plan.create); !reflect.DeepEqual(got, []string{"new"}) {
		t.Errorf("create = %v", got)
	}
	// Moved is out of line; an admin's client of the same type is taken over.
	if got := idsOf(plan.update); !reflect.DeepEqual(got, []string{"moved", "admin"}) {
		t.Errorf("update = %v", got)
	}
	for _, w := range plan.update {
		if w.current == nil || w.current.ID != w.client.ID {
			t.Errorf("update %s does not carry the row it replaces", w.client.ID)
		}
	}
	var removed []string
	for _, c := range plan.remove {
		removed = append(removed, c.ID)
	}
	// Only managed rows the deployment stopped declaring; never the console's.
	if !reflect.DeepEqual(removed, []string{"gone", "gone-busy"}) {
		t.Errorf("remove = %v", removed)
	}
	if len(plan.skip) != 2 || !strings.HasPrefix(plan.skip[0], "admin-busy: ") || !strings.Contains(plan.skip[0], "2 download(s) in flight") ||
		!strings.HasPrefix(plan.skip[1], "other-type: a nzbget client") {
		t.Errorf("skip = %q", plan.skip)
	}

	// A changed secret alone is an update too.
	differs := func(store.DownloadClient, SecretInput) bool { return false }
	plan, err = planManaged([]managedWrite{declared("same", "qbittorrent")}, map[string]bool{"same": true},
		[]store.DownloadClient{inLine}, busy, differs)
	if err != nil || len(plan.update) != 1 || len(plan.create)+len(plan.remove)+len(plan.skip) != 0 {
		t.Errorf("changed secret: %+v %v", plan, err)
	}

	// A store that cannot count downloads stops the plan: taking a client over
	// blind could orphan its downloads.
	failing := func(string) (int, error) { return 0, errors.New("down") }
	if _, err := planManaged([]managedWrite{declared("admin", "qbittorrent")}, map[string]bool{"admin": true},
		[]store.DownloadClient{admin}, failing, same); err == nil {
		t.Error("a failing count was ignored")
	}
}

func TestManagedResultString(t *testing.T) {
	if got := (ManagedResult{}).String(); got != "in line" {
		t.Errorf("nothing changed: %q", got)
	}
	r := ManagedResult{Created: []string{"qbittorrent"}, Removed: []string{"nzbget", "old"}}
	if got := r.String(); got != "created qbittorrent; removed nzbget, old" {
		t.Errorf("changes: %q", got)
	}
}

// ── over a real database ───────────────────────────────────────────────────

func TestManagedClientsEndToEnd(t *testing.T) {
	gw := &fakeGateway{}
	svc := e2eService(t, gw, testBox(t))
	ctx := context.Background()
	declare := func(clients ...map[string]any) ManagedResult {
		t.Helper()
		svc.cfg.ManagedClients, svc.cfg.ManagedClientsSet = declaration(t, clients...), true
		res, err := svc.ApplyManagedClients(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	get := func(id string) store.DownloadClient {
		t.Helper()
		c, err := svc.st.GetDownloadClient(ctx, id)
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		return c
	}

	// Without the variable nothing is touched.
	svc.cfg.ManagedClientsSet = false
	if res, err := svc.ApplyManagedClients(ctx); err != nil || res.String() != "in line" {
		t.Fatalf("undeclared: %v %v", res, err)
	}

	res := declare(chartQB("first"), chartNZB("usenet-pw"))
	if !reflect.DeepEqual(res.Created, []string{"qbittorrent", "nzbget"}) || len(res.Skipped) > 0 {
		t.Fatalf("first start: %+v", res)
	}
	qb := get("qbittorrent")
	if !qb.Managed || !qb.Enabled || qb.Auth != "basic" || qb.Username != "acquire" || len(qb.SecretCT) == 0 ||
		qb.LocalPath != "/var/lib/acquire/downloads/qbittorrent/complete" {
		t.Fatalf("stored = %+v", qb)
	}
	if !svc.storedSecretIs(qb, SecretInput{Present: true, Value: "first"}) {
		t.Fatal("the declared password is not what is stored")
	}

	// The same declaration again writes nothing: the revision stays.
	rev, _ := svc.st.ClientsRevision(ctx)
	if res := declare(chartQB("first"), chartNZB("usenet-pw")); res.String() != "in line" {
		t.Fatalf("second start: %+v", res)
	}
	if again, _ := svc.st.ClientsRevision(ctx); again != rev {
		t.Fatalf("an unchanged declaration moved the revision %d → %d", rev, again)
	}

	// A new password is stored, and pushed with the client.
	if res := declare(chartQB("second"), chartNZB("usenet-pw")); !reflect.DeepEqual(res.Updated, []string{"qbittorrent"}) {
		t.Fatalf("changed password: %+v", res)
	}
	if !svc.storedSecretIs(get("qbittorrent"), SecretInput{Present: true, Value: "second"}) {
		t.Fatal("the new password was not stored")
	}
	if _, err := svc.sync.Push(ctx); err != nil {
		t.Fatal(err)
	}
	pushed := gw.puts[len(gw.puts)-1]
	var secret string
	for _, c := range pushed.Clients {
		if c.ID == "qbittorrent" {
			secret = c.Secret
		}
	}
	if secret != "second" {
		t.Fatalf("the gateway was pushed %+v", pushed.Clients)
	}

	// The API cannot change or remove a managed client.
	if _, err := svc.UpdateClient(ctx, "qbittorrent", ClientInput{BaseURL: "http://elsewhere:8080", Auth: "none"}, 0, "admin"); !errors.Is(err, ErrManaged) {
		t.Fatalf("update of a managed client: %v, want ErrManaged", err)
	}
	if _, err := svc.DeleteClient(ctx, "nzbget", 0, "admin"); !errors.Is(err, ErrManaged) {
		t.Fatalf("delete of a managed client: %v, want ErrManaged", err)
	}
	list, err := svc.ListClients(ctx)
	if err != nil || len(list.Clients) != 2 || !list.Clients[0].Managed || !list.Clients[1].Managed {
		t.Fatalf("list: %+v %v", list, err)
	}

	// No longer declared: removed, or only disabled while a download is in
	// flight on it.
	if _, err := svc.st.UpsertDownload(ctx, store.Download{Adapter: "nzbget", ClientJobID: "7", State: "downloading"}); err != nil {
		t.Fatal(err)
	}
	if res := declare(); !reflect.DeepEqual(res.Removed, []string{"qbittorrent"}) || !reflect.DeepEqual(res.Disabled, []string{"nzbget"}) {
		t.Fatalf("declared none: %+v", res)
	}
	if nz := get("nzbget"); nz.Enabled || !nz.Managed {
		t.Fatalf("nzbget with a download in flight: %+v", nz)
	}
	if _, err := svc.st.UpsertDownload(ctx, store.Download{Adapter: "nzbget", ClientJobID: "7", State: "completed"}); err != nil {
		t.Fatal(err)
	}
	if res := declare(); !reflect.DeepEqual(res.Removed, []string{"nzbget"}) {
		t.Fatalf("after the download finished: %+v", res)
	}
}

// An admin's client with the deployment's id and type is taken over once
// nothing is in flight on it; one of another type is never touched.
func TestManagedClientsTakeOver(t *testing.T) {
	svc := e2eService(t, &fakeGateway{}, testBox(t))
	ctx := context.Background()
	for _, in := range []ClientInput{
		{ID: "qbittorrent", Type: "qbittorrent", BaseURL: "http://old-host:8080", Auth: "none", Protocols: []string{"torrent"}},
		{ID: "nzbget", Type: "qbittorrent", BaseURL: "http://torrents:8080", Auth: "none"},
	} {
		if _, err := svc.CreateClient(ctx, in, "admin"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := svc.st.UpsertDownload(ctx, store.Download{Adapter: "qbittorrent", ClientJobID: "dlg-1", State: "downloading"}); err != nil {
		t.Fatal(err)
	}
	svc.cfg.ManagedClients, svc.cfg.ManagedClientsSet = declaration(t, chartQB("pw"), chartNZB("pw")), true

	res, err := svc.ApplyManagedClients(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Created)+len(res.Updated) != 0 || len(res.Skipped) != 2 {
		t.Fatalf("while busy: %+v", res)
	}
	if c, _ := svc.st.GetDownloadClient(ctx, "qbittorrent"); c.Managed || c.BaseURL != "http://old-host:8080" {
		t.Fatalf("taken over with a download in flight: %+v", c)
	}

	if _, err := svc.st.UpsertDownload(ctx, store.Download{Adapter: "qbittorrent", ClientJobID: "dlg-1", State: "completed"}); err != nil {
		t.Fatal(err)
	}
	res, err = svc.ApplyManagedClients(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(res.Updated, []string{"qbittorrent"}) || len(res.Skipped) != 1 || !strings.HasPrefix(res.Skipped[0], "nzbget: ") {
		t.Fatalf("after the download: %+v", res)
	}
	c, _ := svc.st.GetDownloadClient(ctx, "qbittorrent")
	if !c.Managed || c.BaseURL != "http://qbittorrent:8080" || c.Auth != "basic" {
		t.Fatalf("not taken over: %+v", c)
	}
	if other, _ := svc.st.GetDownloadClient(ctx, "nzbget"); other.Managed || other.Type != "qbittorrent" {
		t.Fatalf("a client of another type was touched: %+v", other)
	}
	entries, err := svc.st.RecentAudit(ctx, 1)
	if err != nil || len(entries) != 1 || entries[0].Actor != "deployment" || entries[0].EntityID != "qbittorrent" {
		t.Fatalf("audit: %+v %v", entries, err)
	}
}
