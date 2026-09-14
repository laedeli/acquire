package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"reflect"
	"strings"
	"testing"

	"github.com/laedeli/acquire/internal/endpoint"
	"github.com/laedeli/acquire/internal/gateway"
	"github.com/laedeli/acquire/internal/store"
)

func TestSecretInputSemantics(t *testing.T) {
	var in struct {
		Secret SecretInput `json:"secret"`
	}
	cases := []struct {
		body              string
		present, clear    bool
		value             string
		wantErr, replaces bool
	}{
		{body: `{}`},
		{body: `{"secret":null}`},
		{body: `{"secret":"hunter2"}`, present: true, value: "hunter2", replaces: true},
		{body: `{"secret":{"clear":true}}`, present: true, clear: true},
		{body: `{"secret":""}`, present: true, clear: true},
		{body: `{"secret":{"clear":false}}`, wantErr: true},
		{body: `{"secret":42}`, wantErr: true},
	}
	for _, c := range cases {
		in.Secret = SecretInput{}
		err := json.Unmarshal([]byte(c.body), &in)
		if (err != nil) != c.wantErr {
			t.Errorf("%s: err = %v", c.body, err)
			continue
		}
		if c.wantErr {
			continue
		}
		s := in.Secret
		if s.Present != c.present || s.Clear != c.clear || s.Value != c.value || s.replaces() != c.replaces {
			t.Errorf("%s: got %+v", c.body, s)
		}
	}
}

func TestChooseClient(t *testing.T) {
	clients := []store.DownloadClient{
		{ID: "usenet-backup", Protocols: []string{"usenet"}, Priority: 10, Enabled: true},
		{ID: "nzbget", Protocols: []string{"usenet"}, Priority: 0, Enabled: true},
		{ID: "qbittorrent", Protocols: []string{"torrent"}, Priority: 0, Enabled: false},
		{ID: "qbt-b", Protocols: []string{"torrent"}, Priority: 5, Enabled: true},
		{ID: "qbt-a", Protocols: []string{"torrent"}, Priority: 5, Enabled: true},
		{ID: "hoster", Protocols: []string{"http"}, Priority: 0, Enabled: true},
	}
	cases := []struct {
		protocol, prefer, want string
	}{
		{"usenet", "", "nzbget"},     // lowest priority value wins
		{"torrent", "", "qbt-a"},     // disabled skipped; ties broken by id
		{"http", "usenet", "hoster"}, //
		{"", "usenet", "nzbget"},     // unknown kind: preference first
		{"", "torrent", "qbt-a"},     //
	}
	for _, c := range cases {
		got, err := chooseClient(clients, c.protocol, c.prefer)
		if err != nil || got.ID != c.want {
			t.Errorf("chooseClient(%q, prefer %q) = %q, %v; want %q", c.protocol, c.prefer, got.ID, err, c.want)
		}
	}
	_, err := chooseClient(clients[:1], "torrent", "")
	if err == nil || err.Error() != "no download client handles torrent — add one under download clients" {
		t.Errorf("no route: %v", err)
	}
}

func TestDefaultClientIDNamesTheFirstAfterItsType(t *testing.T) {
	if id := defaultClientID("nzbget", nil); id != "nzbget" {
		t.Errorf("first = %q", id)
	}
	existing := []store.DownloadClient{{ID: "nzbget"}, {ID: "nzbget-2"}}
	if id := defaultClientID("nzbget", existing); id != "nzbget-3" {
		t.Errorf("third = %q", id)
	}
}

func testService() *Service {
	return &Service{policy: endpoint.Policy{
		Deny: []string{"blocked.test"},
		Resolve: func(context.Context, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("10.0.0.9")}, nil
		},
	}}
}

func fieldsOf(fe []FieldError) string {
	var out []string
	for _, f := range fe {
		out = append(out, f.Field)
	}
	return strings.Join(out, ",")
}

func TestValidateClient(t *testing.T) {
	s := testService()
	ctx := context.Background()
	intp := func(n int) *int { return &n }
	stored := store.DownloadClient{ID: "nzbget", Type: "nzbget", BaseURL: "http://worker", Auth: "basic", Username: "u",
		SecretCT: []byte("sealed"), Protocols: []string{"usenet"}, Priority: 3, Enabled: false}

	cases := []struct {
		name    string
		in      ClientInput
		current *store.DownloadClient
		fields  string
	}{
		{name: "minimal no-auth client", in: ClientInput{Type: "nzbget", BaseURL: "http://worker:6789", Auth: "none"}},
		{name: "unknown type", in: ClientInput{Type: "other", BaseURL: "http://worker", Auth: "none"}, fields: "type"},
		{name: "bad id", in: ClientInput{ID: "Bad_ID", Type: "nzbget", BaseURL: "http://worker", Auth: "none"}, fields: "id"},
		{name: "id taken", in: ClientInput{ID: "nzbget", Type: "nzbget", BaseURL: "http://worker", Auth: "none"}, fields: "id"},
		{name: "denied host", in: ClientInput{Type: "nzbget", BaseURL: "http://x.blocked.test", Auth: "none"}, fields: "baseUrl"},
		{name: "metadata address", in: ClientInput{Type: "nzbget", BaseURL: "http://169.254.169.254", Auth: "none"}, fields: "baseUrl"},
		{name: "auth the type lacks", in: ClientInput{Type: "nzbget", BaseURL: "http://worker", Auth: "token", Secret: SecretInput{Present: true, Value: "t"}}, fields: "auth"},
		{name: "basic needs username and secret", in: ClientInput{Type: "nzbget", BaseURL: "http://worker", Auth: "basic"}, fields: "username,secret"},
		{name: "protocol the type lacks", in: ClientInput{Type: "nzbget", BaseURL: "http://worker", Auth: "none", Protocols: []string{"torrent"}}, fields: "protocols"},
		{name: "relative remote path", in: ClientInput{Type: "nzbget", BaseURL: "http://worker", Auth: "none", RemotePath: "downloads"}, fields: "remotePath"},
		{name: "windows remote path", in: ClientInput{Type: "nzbget", BaseURL: "http://worker", Auth: "none", RemotePath: `D:\downloads`, LocalPath: "/media/downloads"}},
		{name: "local without remote", in: ClientInput{Type: "nzbget", BaseURL: "http://worker", Auth: "none", LocalPath: "/media/downloads"}, fields: "remotePath"},
		{name: "priority out of range", in: ClientInput{Type: "nzbget", BaseURL: "http://worker", Auth: "none", Priority: intp(5000)}, fields: "priority"},
		{name: "category with a slash", in: ClientInput{Type: "nzbget", BaseURL: "http://worker", Auth: "none", Category: "a/b"}, fields: "category"},
		// An update may omit the secret: the stored one is kept.
		{name: "update keeps stored secret", in: ClientInput{ID: "nzbget", Type: "nzbget", BaseURL: "http://worker", Auth: "basic", Username: "u"}, current: &stored},
		{name: "update clearing a required secret", in: ClientInput{ID: "nzbget", Type: "nzbget", BaseURL: "http://worker", Auth: "basic", Username: "u", Secret: SecretInput{Present: true, Clear: true}}, current: &stored, fields: "secret"},
		// The stored secret stays with the address it was saved for.
		{name: "update moving the address without the secret", in: ClientInput{ID: "nzbget", Type: "nzbget", BaseURL: "http://listener:6789", Auth: "basic", Username: "u"}, current: &stored, fields: "secret"},
		{name: "update moving the port without the secret", in: ClientInput{ID: "nzbget", Type: "nzbget", BaseURL: "http://worker:8080", Auth: "basic", Username: "u"}, current: &stored, fields: "secret"},
		{name: "update moving the address with a new secret", in: ClientInput{ID: "nzbget", Type: "nzbget", BaseURL: "http://listener:6789", Auth: "basic", Username: "u", Secret: SecretInput{Present: true, Value: "new"}}, current: &stored},
		{name: "update writing the same address differently", in: ClientInput{ID: "nzbget", Type: "nzbget", BaseURL: "HTTP://Worker:80/", Auth: "basic", Username: "u"}, current: &stored},
	}
	existing := []store.DownloadClient{{ID: "nzbget"}}
	for _, c := range cases {
		ex := existing
		if c.name == "minimal no-auth client" || c.name == "windows remote path" {
			ex = nil
		}
		_, fe := s.validateClient(ctx, c.in, builtinClientTypes, ex, c.current)
		if got := fieldsOf(fe); got != c.fields {
			t.Errorf("%s: invalid fields = %q, want %q (%+v)", c.name, got, c.fields, fe)
		}
	}
}

func TestSameEndpoint(t *testing.T) {
	cases := []struct {
		a, b string
		same bool
	}{
		{"http://worker:6789", "http://worker:6789/", true},
		{"http://Worker.Media.svc:6789", "http://worker.media.svc.:6789", true},
		{"http://worker", "http://worker:80", true},
		{"https://worker", "https://worker:443/", true},
		{"http://worker/nzb", "http://worker/nzb/", true},
		{"http://worker", "https://worker", false},
		{"http://worker:6789", "http://worker:6790", false},
		{"http://worker", "http://worker.evil.test", false},
		{"http://worker/nzb", "http://worker/other", false},
		{"http://worker", "http://[bad", false},
	}
	for _, c := range cases {
		if got := sameEndpoint(c.a, c.b); got != c.same {
			t.Errorf("sameEndpoint(%q, %q) = %v, want %v", c.a, c.b, got, c.same)
		}
	}
}

func TestValidateClientDefaults(t *testing.T) {
	s := testService()
	c, fe := s.validateClient(context.Background(),
		ClientInput{Type: "qbittorrent", BaseURL: " http://worker:8080 ", Auth: "none", Username: "ignored"},
		builtinClientTypes, []store.DownloadClient{{ID: "qbittorrent"}}, nil)
	if len(fe) > 0 {
		t.Fatal(fe)
	}
	if c.ID != "qbittorrent-2" || c.Category != "acquire" || !c.Enabled || c.BaseURL != "http://worker:8080" ||
		len(c.Protocols) != 1 || c.Protocols[0] != "torrent" || c.Username != "" {
		t.Fatalf("defaults not applied: %+v", c)
	}

	// On update, omitted priority/enabled/protocols keep what is stored.
	stored := store.DownloadClient{ID: "qbittorrent", Type: "qbittorrent", Protocols: []string{"torrent"}, Priority: 7, Enabled: false}
	u, fe := s.validateClient(context.Background(),
		ClientInput{ID: "qbittorrent", Type: "qbittorrent", BaseURL: "http://worker", Auth: "none"},
		builtinClientTypes, nil, &stored)
	if len(fe) > 0 || u.Priority != 7 || u.Enabled || u.Protocols[0] != "torrent" {
		t.Fatalf("update lost stored values: %+v %+v", u, fe)
	}
}

func TestMapClientPaths(t *testing.T) {
	files := []string{
		"/downloads/Example.2020/Example.2020.mkv",
		"/downloads2/other.mkv",
		"/elsewhere/x.mkv",
		`D:\downloads\Example\Example.mkv`,
	}
	got := mapClientPaths(files, "/downloads/", "/media/_downloads")
	want := []string{"/media/_downloads/Example.2020/Example.2020.mkv", "/downloads2/other.mkv", "/elsewhere/x.mkv", `D:\downloads\Example\Example.mkv`}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("posix %d: %q, want %q", i, got[i], want[i])
		}
	}
	win := mapClientPaths(files[3:], `D:\downloads`, "/media/_downloads")
	if win[0] != "/media/_downloads/Example/Example.mkv" {
		t.Errorf("windows: %q", win[0])
	}
	if same := mapClientPaths(files, "", "/media"); same[0] != files[0] {
		t.Error("no mapping configured must leave paths alone")
	}
}

func TestCoverage(t *testing.T) {
	c := Coverage{
		Sources: map[string]int{"usenet": 2, "torrent": 3},
		Clients: map[string]int{"usenet": 1},
	}
	if !c.CanGrab() {
		t.Error("usenet has a source and a client")
	}
	if g := c.Gaps(); len(g) != 1 || g[0] != "torrent" {
		t.Errorf("gaps = %v", g)
	}
	if (Coverage{Sources: map[string]int{"torrent": 1}, Clients: map[string]int{"usenet": 1}}).CanGrab() {
		t.Error("no protocol is covered on both sides")
	}
}

func TestSetupStateIsTheWorstRequiredSection(t *testing.T) {
	cases := []struct {
		sections []SetupSection
		want     string
	}{
		{[]SetupSection{{Key: "sources", State: SetupReady}, {Key: "clients", State: SetupReady}, {Key: "grab-policy", State: SetupDegraded}}, SetupReady},
		{[]SetupSection{{Key: "sources", State: SetupDegraded}, {Key: "clients", State: SetupReady}}, SetupDegraded},
		{[]SetupSection{{Key: "sources", State: SetupDegraded}, {Key: "clients", State: SetupNeedsSetup}}, SetupNeedsSetup},
	}
	for i, c := range cases {
		if got := worstRequired(c.sections); got != c.want {
			t.Errorf("%d: %s, want %s", i, got, c.want)
		}
	}
	long := strings.Repeat("é", 150)
	if got := clip(long, 200); len(got) > 200 || !strings.HasSuffix(got, "…") {
		t.Errorf("clip: %d bytes", len(got))
	}
}

func TestSearchSettingsValidation(t *testing.T) {
	if fe := validateSearchSettings(SearchSettings{PreferProtocol: "usenet", StorageFloorGB: 500, MaxConcurrentGrabs: 3}); len(fe) != 0 {
		t.Fatal(fe)
	}
	fe := validateSearchSettings(SearchSettings{PreferProtocol: "any", StorageFloorGB: -1, MaxConcurrentGrabs: 0})
	if fieldsOf(fe) != "preferProtocol,storageFloorGb,maxConcurrentGrabs" {
		t.Fatalf("fields = %s", fieldsOf(fe))
	}
	var ve *ValidationError
	if !errors.As(error(&ValidationError{Fields: fe}), &ve) {
		t.Fatal("ValidationError must be matchable")
	}
}

// The builtin types must say what the gateway's catalog says
// (GET /api/v1/config/types, documented in the gateway's README), field for
// field: validation runs against them whenever the gateway cannot be asked.
func TestBuiltinTypesMatchTheGatewayCatalog(t *testing.T) {
	want := []gateway.ClientType{
		{Type: "nzbget", Protocols: []string{"usenet"}, Auth: []string{"basic", "none"}, AcceptsPayload: true, SupportsSavePath: false, CanPause: true},
		{Type: "odownloader", Protocols: []string{"http"}, Auth: []string{"token", "none"}, AcceptsPayload: false, SupportsSavePath: false, CanPause: false},
		{Type: "qbittorrent", Protocols: []string{"torrent"}, Auth: []string{"basic", "none"}, AcceptsPayload: true, SupportsSavePath: true, CanPause: true},
	}
	if len(builtinClientTypes) != len(want) {
		t.Fatalf("builtin types = %d, the gateway has %d", len(builtinClientTypes), len(want))
	}
	for _, w := range want {
		got, ok := findType(builtinClientTypes, w.Type)
		if !ok || !reflect.DeepEqual(got, w) {
			t.Errorf("%s: builtin %+v, gateway %+v", w.Type, got, w)
		}
	}

	// A body the gateway accepts must not be refused just because it is down.
	s := testService()
	_, fe := s.validateClient(context.Background(),
		ClientInput{Type: "odownloader", BaseURL: "http://worker:9666", Auth: "none"},
		builtinClientTypes, nil, nil)
	if len(fe) > 0 {
		t.Errorf("odownloader without authentication refused by the fallback: %+v", fe)
	}
}
