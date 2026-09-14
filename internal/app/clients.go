package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/laedeli/acquire/internal/configsync"
	"github.com/laedeli/acquire/internal/gateway"
	"github.com/laedeli/acquire/internal/secretbox"
	"github.com/laedeli/acquire/internal/store"
)

// Download clients are acquire's configuration: stored here, edited in the
// console, pushed to the gateway by configsync. The gateway only runs them.

// clientIDRule is the gateway's id rule; the table enforces it too.
var clientIDRule = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)

// builtinClientTypes is what acquire assumes when the gateway cannot be asked.
// It only keeps the console usable while the gateway is down; validation
// prefers the gateway's own answer whenever there is one. It must say exactly
// what the gateway's catalog says, or a write accepted while the gateway is up
// is refused while it is down (and the other way round).
var builtinClientTypes = []gateway.ClientType{
	{Type: "nzbget", Protocols: []string{"usenet"}, Auth: []string{"basic", "none"}, AcceptsPayload: true, SupportsSavePath: false, CanPause: true},
	{Type: "qbittorrent", Protocols: []string{"torrent"}, Auth: []string{"basic", "none"}, AcceptsPayload: true, SupportsSavePath: true, CanPause: true},
	{Type: "odownloader", Protocols: []string{"http"}, Auth: []string{"token", "none"}, AcceptsPayload: false, SupportsSavePath: false, CanPause: false},
}

// SecretInput is a secret field in a write. Omitted keeps the stored value, a
// string replaces it, {"clear":true} removes it.
type SecretInput struct {
	Present bool
	Value   string
	Clear   bool
}

func (s *SecretInput) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		*s = SecretInput{}
		return nil
	}
	var str string
	if err := json.Unmarshal(b, &str); err == nil {
		*s = SecretInput{Present: true, Value: str, Clear: str == ""}
		return nil
	}
	var obj struct {
		Clear bool `json:"clear"`
	}
	if err := json.Unmarshal(b, &obj); err == nil && obj.Clear {
		*s = SecretInput{Present: true, Clear: true}
		return nil
	}
	return errors.New(`secret must be a string or {"clear":true}`)
}

// replaces reports whether the write stores a new secret value.
func (s SecretInput) replaces() bool { return s.Present && !s.Clear }

// sameEndpoint reports whether two addresses reach the same place: scheme,
// host, port and path, ignoring letter case in the scheme and host and a
// trailing slash. A stored secret is only ever sent where it was stored for.
// Keeping it while the address changes would make a write-only secret readable
// to anyone who can edit the address: point a test or a save at a listener of
// their own and read it off the wire.
func sameEndpoint(a, b string) bool {
	ua, errA := url.Parse(strings.TrimSpace(a))
	ub, errB := url.Parse(strings.TrimSpace(b))
	if errA != nil || errB != nil {
		return false
	}
	host := func(u *url.URL) string { return strings.TrimSuffix(strings.ToLower(u.Hostname()), ".") }
	return strings.EqualFold(ua.Scheme, ub.Scheme) && host(ua) == host(ub) &&
		effectivePort(ua) == effectivePort(ub) &&
		strings.TrimRight(ua.EscapedPath(), "/") == strings.TrimRight(ub.EscapedPath(), "/")
}

func effectivePort(u *url.URL) string {
	if p := u.Port(); p != "" {
		return p
	}
	if strings.EqualFold(u.Scheme, "https") {
		return "443"
	}
	return "80"
}

// ClientInput is a download client as the console writes it.
type ClientInput struct {
	ID         string      `json:"id"`
	Type       string      `json:"type"`
	BaseURL    string      `json:"baseUrl"`
	Auth       string      `json:"auth"`
	Username   string      `json:"username"`
	Secret     SecretInput `json:"secret"`
	Protocols  []string    `json:"protocols"`
	Category   string      `json:"category"`
	RemotePath string      `json:"remotePath"`
	LocalPath  string      `json:"localPath"`
	Priority   *int        `json:"priority"`
	Enabled    *bool       `json:"enabled"`
}

// SecretView says whether a secret is stored, never what it is.
type SecretView struct {
	Set       bool       `json:"set"`
	UpdatedAt *time.Time `json:"updatedAt"`
}

// ClientView is a download client as the API returns it.
type ClientView struct {
	ID         string     `json:"id"`
	Type       string     `json:"type"`
	BaseURL    string     `json:"baseUrl"`
	Auth       string     `json:"auth"`
	Username   string     `json:"username"`
	Secret     SecretView `json:"secret"`
	Protocols  []string   `json:"protocols"`
	Category   string     `json:"category"`
	RemotePath string     `json:"remotePath"`
	LocalPath  string     `json:"localPath"`
	Priority   int        `json:"priority"`
	Enabled    bool       `json:"enabled"`
	Revision   int64      `json:"revision"`
	CreatedAt  time.Time  `json:"createdAt"`
	UpdatedAt  time.Time  `json:"updatedAt"`
	// Live state, best effort: what the gateway reports for this client, and
	// its reason when the gateway refused to apply it.
	Status     *gateway.ClientStatus `json:"status,omitempty"`
	ApplyError string                `json:"applyError,omitempty"`
}

func clientView(c store.DownloadClient) ClientView {
	return ClientView{
		ID: c.ID, Type: c.Type, BaseURL: c.BaseURL, Auth: c.Auth, Username: c.Username,
		Secret:    SecretView{Set: len(c.SecretCT) > 0, UpdatedAt: c.SecretUpdatedAt},
		Protocols: c.Protocols, Category: c.Category, RemotePath: c.RemotePath, LocalPath: c.LocalPath,
		Priority: c.Priority, Enabled: c.Enabled, Revision: c.Generation,
		CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
	}
}

// SyncResult is what pushing a write to the gateway achieved. The write itself
// is already committed either way; this tells the editor whether it is live.
type SyncResult struct {
	Applied bool   `json:"applied"`
	Error   string `json:"error,omitempty"`
}

// ClientWriteResult is a written client plus whether the gateway took it.
type ClientWriteResult struct {
	ClientView
	Sync SyncResult `json:"sync"`
}

// ClientList is GET /api/download-clients.
type ClientList struct {
	Revision      int64             `json:"revision"`
	KeyConfigured bool              `json:"keyConfigured"`
	Sync          configsync.Status `json:"sync"`
	Clients       []ClientView      `json:"clients"`
}

// ── types ──────────────────────────────────────────────────────────────────

type typeCache struct {
	mu    sync.Mutex
	types []gateway.ClientType
	at    time.Time
}

// ClientTypes returns the client types the gateway runs, and whether the
// answer came from the gateway ("gateway") or acquire's fallback ("builtin").
func (s *Service) ClientTypes(ctx context.Context) ([]gateway.ClientType, string) {
	s.types.mu.Lock()
	defer s.types.mu.Unlock()
	if len(s.types.types) > 0 && time.Since(s.types.at) < 5*time.Minute {
		return s.types.types, "gateway"
	}
	if s.gw.Enabled() {
		cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if types, err := s.gw.ConfigTypes(cctx); err == nil && len(types) > 0 {
			s.types.types, s.types.at = types, time.Now()
			return types, "gateway"
		}
	}
	if len(s.types.types) > 0 {
		return s.types.types, "gateway"
	}
	return builtinClientTypes, "builtin"
}

func findType(types []gateway.ClientType, name string) (gateway.ClientType, bool) {
	for _, t := range types {
		if t.Type == name {
			return t, true
		}
	}
	return gateway.ClientType{}, false
}

// ── reads ──────────────────────────────────────────────────────────────────

// ListClients returns every client with live gateway state merged in.
func (s *Service) ListClients(ctx context.Context) (ClientList, error) {
	clients, err := s.st.ListDownloadClients(ctx)
	if err != nil {
		return ClientList{}, err
	}
	rev, err := s.st.ClientsRevision(ctx)
	if err != nil {
		return ClientList{}, err
	}
	out := ClientList{Revision: rev, KeyConfigured: s.box.Enabled(), Clients: make([]ClientView, 0, len(clients))}
	out.Sync = s.syncStatus(ctx)

	applyErr := map[string]string{}
	for _, e := range out.Sync.Errors {
		applyErr[e.ID] = e.Message
	}
	live := map[string]gateway.ClientStatus{}
	if s.gw.Enabled() && len(clients) > 0 {
		cctx, cancel := context.WithTimeout(ctx, 4*time.Second)
		if statuses, err := s.gw.ClientsStatus(cctx); err == nil {
			for _, st := range statuses {
				id := st.ID
				if id == "" {
					id = st.Name // a gateway without runtime configuration names clients by type
				}
				live[id] = st
			}
		}
		cancel()
	}
	for _, c := range clients {
		v := clientView(c)
		if st, ok := live[c.ID]; ok {
			st := st
			v.Status = &st
		}
		v.ApplyError = applyErr[c.ID]
		out.Clients = append(out.Clients, v)
	}
	return out, nil
}

// syncStatus is the reconciler's view, refreshed when it is stale so setup and
// the clients screen do not report a restart from minutes ago.
func (s *Service) syncStatus(ctx context.Context) configsync.Status {
	if s.sync == nil {
		return configsync.Status{}
	}
	st := s.sync.Status()
	if st.Configured && time.Since(st.CheckedAt) > 15*time.Second {
		cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		_ = s.sync.Check(cctx)
		cancel()
		st = s.sync.Status()
	}
	return st
}

// ── writes ─────────────────────────────────────────────────────────────────

// CreateClient validates, seals the secret and stores a new client, then
// pushes the configuration to the gateway.
func (s *Service) CreateClient(ctx context.Context, in ClientInput, actor string) (ClientWriteResult, error) {
	types, _ := s.ClientTypes(ctx)
	existing, err := s.st.ListDownloadClients(ctx)
	if err != nil {
		return ClientWriteResult{}, err
	}
	c, fe := s.validateClient(ctx, in, types, existing, nil)
	if len(fe) > 0 {
		return ClientWriteResult{}, &ValidationError{Fields: fe}
	}
	if c.Auth == "none" {
		in.Secret = SecretInput{} // nothing to authenticate with, nothing to keep
	}
	secret, err := s.sealClientSecret(c.ID, in.Secret)
	if err != nil {
		return ClientWriteResult{}, err
	}
	stored, err := s.st.CreateDownloadClient(ctx, c, secret, actor)
	if err != nil {
		return ClientWriteResult{}, err
	}
	return ClientWriteResult{ClientView: clientView(stored), Sync: s.pushNow(ctx)}, nil
}

// UpdateClient replaces a client's editable fields. The id and type are fixed
// once created: downloads, grabs and events all refer to the id.
func (s *Service) UpdateClient(ctx context.Context, id string, in ClientInput, ifRevision int64, actor string) (ClientWriteResult, error) {
	current, err := s.st.GetDownloadClient(ctx, id)
	if err != nil {
		return ClientWriteResult{}, err
	}
	if in.ID != "" && in.ID != id {
		return ClientWriteResult{}, &ValidationError{Fields: []FieldError{{Entity: "client", ID: id, Field: "id", Message: "the id cannot change once a client exists"}}}
	}
	if in.Type != "" && in.Type != current.Type {
		return ClientWriteResult{}, &ValidationError{Fields: []FieldError{{Entity: "client", ID: id, Field: "type", Message: "the type cannot change; add a new client instead"}}}
	}
	in.ID, in.Type = id, current.Type
	types, _ := s.ClientTypes(ctx)
	c, fe := s.validateClient(ctx, in, types, nil, &current)
	if len(fe) > 0 {
		return ClientWriteResult{}, &ValidationError{Fields: fe}
	}
	if c.Auth == "none" {
		// A client that does not authenticate keeps no credential around.
		in.Secret = SecretInput{}
		if len(current.SecretCT) > 0 {
			in.Secret = SecretInput{Present: true, Clear: true}
		}
	}
	secret, err := s.sealClientSecret(id, in.Secret)
	if err != nil {
		return ClientWriteResult{}, err
	}
	stored, err := s.st.UpdateDownloadClient(ctx, c, secret, ifRevision, actor)
	if err != nil {
		return ClientWriteResult{}, err
	}
	return ClientWriteResult{ClientView: clientView(stored), Sync: s.pushNow(ctx)}, nil
}

// DeleteClient removes a client (refused while its downloads are in flight)
// and pushes the smaller set.
func (s *Service) DeleteClient(ctx context.Context, id string, ifRevision int64, actor string) (SyncResult, error) {
	if err := s.st.DeleteDownloadClient(ctx, id, ifRevision, actor); err != nil {
		return SyncResult{}, err
	}
	return s.pushNow(ctx), nil
}

// pushNow sends the stored configuration to the gateway right after a write.
func (s *Service) pushNow(ctx context.Context) SyncResult {
	if s.sync == nil || !s.gw.Enabled() {
		return SyncResult{Error: "download gateway not configured"}
	}
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	res, err := s.sync.Push(cctx)
	if err != nil {
		return SyncResult{Error: err.Error()}
	}
	if len(res.Errors) > 0 {
		msgs := make([]string, 0, len(res.Errors))
		for _, e := range res.Errors {
			msgs = append(msgs, e.ID+": "+e.Message)
		}
		return SyncResult{Error: strings.Join(msgs, "; ")}
	}
	return SyncResult{Applied: true}
}

func (s *Service) sealClientSecret(id string, in SecretInput) (store.SecretWrite, error) {
	switch {
	case !in.Present:
		return store.SecretWrite{Op: store.SecretKeep}, nil
	case in.Clear:
		return store.SecretWrite{Op: store.SecretClear}, nil
	}
	table, rowID, field := store.ClientSecretAAD(id)
	ct, kid, err := s.box.Seal(table, rowID, field, []byte(in.Value))
	if err != nil {
		return store.SecretWrite{}, err
	}
	return store.SecretWrite{Op: store.SecretSet, CT: ct, KID: kid}, nil
}

// validateClient checks a write and returns the row to store. existing is the
// full list on create (for id defaults and collisions); current is the stored
// row on update.
func (s *Service) validateClient(ctx context.Context, in ClientInput, types []gateway.ClientType, existing []store.DownloadClient, current *store.DownloadClient) (store.DownloadClient, []FieldError) {
	var fe []FieldError
	id := strings.TrimSpace(in.ID)
	add := func(field, msg string) {
		fe = append(fe, FieldError{Entity: "client", ID: id, Field: field, Message: msg})
	}

	typ, known := findType(types, in.Type)
	if !known {
		names := make([]string, 0, len(types))
		for _, t := range types {
			names = append(names, t.Type)
		}
		add("type", "must be one of: "+strings.Join(names, ", "))
	}

	if current == nil {
		if id == "" && known {
			id = defaultClientID(in.Type, existing)
		}
		switch {
		case id == "" && !known:
			// Named after its type by default; the type error already says what
			// is wrong.
		case !clientIDRule.MatchString(id):
			add("id", "lowercase letters, digits and dashes, starting with a letter or digit, at most 40")
		default:
			for _, c := range existing {
				if c.ID == id {
					add("id", "a client with this id already exists")
					break
				}
			}
		}
	}

	if err := s.policy.CheckResolved(ctx, in.BaseURL); err != nil {
		add("baseUrl", err.Error())
	}

	auth := in.Auth
	if auth == "" {
		auth = "none"
		if known && len(typ.Auth) > 0 {
			auth = typ.Auth[0]
		}
	}
	switch {
	case auth != "basic" && auth != "token" && auth != "none":
		add("auth", `must be "basic", "token" or "none"`)
	case known && len(typ.Auth) > 0 && !contains(typ.Auth, auth):
		add("auth", fmt.Sprintf("%s supports: %s", in.Type, strings.Join(typ.Auth, ", ")))
	}
	username := strings.TrimSpace(in.Username)
	if auth == "basic" && username == "" {
		add("username", "required for basic authentication")
	}
	if auth != "basic" {
		username = ""
	}
	if auth != "none" {
		hasStored := current != nil && len(current.SecretCT) > 0
		switch {
		case in.Secret.Clear || (!in.Secret.replaces() && !hasStored):
			add("secret", "required for "+auth+" authentication")
		case !in.Secret.replaces() && !sameEndpoint(in.BaseURL, current.BaseURL):
			add("secret", "enter the secret again: the stored one is only sent to the address it was saved for")
		}
	}

	protocols := dedupe(in.Protocols)
	if len(protocols) == 0 {
		if current != nil && in.Protocols == nil {
			protocols = current.Protocols
		} else if known {
			protocols = typ.Protocols
		}
	}
	if len(protocols) == 0 && known {
		add("protocols", "at least one protocol is required")
	}
	for _, p := range protocols {
		if known && len(typ.Protocols) > 0 && !contains(typ.Protocols, p) {
			add("protocols", fmt.Sprintf("%s handles: %s", in.Type, strings.Join(typ.Protocols, ", ")))
			break
		}
	}

	category := strings.TrimSpace(in.Category)
	if category == "" {
		category = "acquire"
	}
	if len(category) > 64 || strings.ContainsAny(category, "/\\\x00\n\r\t") {
		add("category", "at most 64 characters, no slashes or control characters")
	}

	remote := strings.TrimSpace(in.RemotePath)
	local := strings.TrimSpace(in.LocalPath)
	if remote != "" && !absoluteAnywhere(remote) {
		add("remotePath", "must be an absolute path as the client sees it, e.g. /downloads")
	}
	if local != "" {
		if !strings.HasPrefix(local, "/") {
			add("localPath", "must be an absolute path inside acquire's container")
		} else {
			local = path.Clean(local)
		}
	}
	if local != "" && remote == "" {
		add("remotePath", "set the folder as the client sees it too, or finished files cannot be found")
	}

	priority := 0
	if in.Priority != nil {
		priority = *in.Priority
	} else if current != nil {
		priority = current.Priority
	}
	if priority < -1000 || priority > 1000 {
		add("priority", "must be between -1000 and 1000")
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	} else if current != nil {
		enabled = current.Enabled
	}

	return store.DownloadClient{
		ID: id, Type: in.Type, BaseURL: strings.TrimSpace(in.BaseURL), Auth: auth, Username: username,
		Protocols: protocols, Category: category, RemotePath: remote, LocalPath: local,
		Priority: priority, Enabled: enabled,
	}, fe
}

// defaultClientID names the first client of a type after the type, so rows
// written before clients were configurable (adapter = type) keep resolving.
// Later ones get a numbered suffix.
func defaultClientID(typ string, existing []store.DownloadClient) string {
	taken := map[string]bool{}
	for _, c := range existing {
		taken[c.ID] = true
	}
	if !taken[typ] {
		return typ
	}
	for n := 2; ; n++ {
		if id := fmt.Sprintf("%s-%d", typ, n); !taken[id] {
			return id
		}
	}
}

// absoluteAnywhere accepts a POSIX absolute path or a Windows drive path: the
// client may run on a different operating system than acquire.
func absoluteAnywhere(p string) bool {
	if strings.HasPrefix(p, "/") {
		return true
	}
	return len(p) >= 3 && p[1] == ':' && (p[2] == '\\' || p[2] == '/') &&
		((p[0] >= 'a' && p[0] <= 'z') || (p[0] >= 'A' && p[0] <= 'Z'))
}

// ── test ───────────────────────────────────────────────────────────────────

// TestClient asks the gateway to reach a client without registering it. When
// the body names an existing client at its stored address and carries no
// secret, the stored one is used, so an admin can re-test without retyping a
// password. At any other address the secret has to be typed.
func (s *Service) TestClient(ctx context.Context, in ClientInput) (gateway.TestResult, error) {
	types, _ := s.ClientTypes(ctx)
	var current *store.DownloadClient
	if in.ID != "" {
		if c, err := s.st.GetDownloadClient(ctx, in.ID); err == nil {
			current = &c
			if in.Type == "" {
				in.Type = c.Type
			}
		}
	}
	id := in.ID
	if id == "" {
		id = "test"
		in.ID = id
	}
	c, fe := s.validateClient(ctx, in, types, nil, current)
	// Only what the gateway needs to reach the client matters for a test; an
	// incomplete path mapping or a name collision is not a reason to refuse it.
	var blocking []FieldError
	for _, f := range fe {
		switch f.Field {
		case "type", "baseUrl", "auth", "username", "secret", "protocols":
			blocking = append(blocking, f)
		}
	}
	if len(blocking) > 0 {
		return gateway.TestResult{}, &ValidationError{Fields: blocking}
	}
	cc := gateway.ConfigClient{
		ID: id, Type: c.Type, BaseURL: c.BaseURL, Auth: c.Auth, Username: c.Username,
		Category: c.Category, SavePath: c.RemotePath, Protocols: c.Protocols,
	}
	switch {
	case in.Secret.replaces():
		cc.Secret = in.Secret.Value
	case c.Auth != "none" && current != nil && len(current.SecretCT) > 0 && sameEndpoint(c.BaseURL, current.BaseURL):
		table, rowID, field := store.ClientSecretAAD(current.ID)
		pt, err := s.box.Open(table, rowID, field, current.SecretCT, current.SecretKID)
		if err != nil {
			return gateway.TestResult{}, fmt.Errorf("the stored secret cannot be opened: %w", err)
		}
		cc.Secret = string(pt)
	}
	if !s.gw.Enabled() {
		return gateway.TestResult{}, errors.New("download gateway not configured")
	}
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return s.gw.TestClient(cctx, cc)
}

// ── routing ────────────────────────────────────────────────────────────────

// errNoClient is the grab failure an operator can act on.
func errNoClient(protocol string) error {
	if protocol == "" {
		return errors.New("no download client is enabled — add one under download clients")
	}
	return fmt.Errorf("no download client handles %s — add one under download clients", protocol)
}

// protocolOrder is the order protocols are tried for a link of unknown kind.
func protocolOrder(prefer string) []string {
	if prefer == "torrent" {
		return []string{"torrent", "usenet", "http"}
	}
	return []string{"usenet", "torrent", "http"}
}

// chooseClient picks the enabled client for a protocol: lowest priority value,
// then id. clients must already be in that order (ListDownloadClients is). An
// empty protocol tries each in preference order.
func chooseClient(clients []store.DownloadClient, protocol, prefer string) (store.DownloadClient, error) {
	order := []string{protocol}
	if protocol == "" {
		order = protocolOrder(prefer)
	}
	sorted := append([]store.DownloadClient(nil), clients...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Priority != sorted[j].Priority {
			return sorted[i].Priority < sorted[j].Priority
		}
		return sorted[i].ID < sorted[j].ID
	})
	for _, p := range order {
		for _, c := range sorted {
			if c.Enabled && contains(c.Protocols, p) {
				return c, nil
			}
		}
	}
	return store.DownloadClient{}, errNoClient(protocol)
}

// pickClient routes a protocol to a client using the stored clients and the
// prefer-protocol setting.
func (s *Service) pickClient(ctx context.Context, protocol string) (store.DownloadClient, error) {
	clients, err := s.st.ListDownloadClients(ctx)
	if err != nil {
		return store.DownloadClient{}, fmt.Errorf("cannot read download clients: %w", err)
	}
	return chooseClient(clients, protocol, s.grabPolicy(ctx).PreferProtocol)
}

// clientRoutes maps each protocol to the client a grab would use right now,
// so search results can say where a release would go.
func (s *Service) clientRoutes(ctx context.Context) map[string]string {
	out := map[string]string{}
	if s.st == nil {
		return out
	}
	clients, err := s.st.ListDownloadClients(ctx)
	if err != nil {
		return out
	}
	for _, p := range []string{"usenet", "torrent", "http"} {
		if c, err := chooseClient(clients, p, ""); err == nil {
			out[p] = c.ID
		}
	}
	return out
}

// Coverage is what acquire can search and grab, by protocol.
type Coverage struct {
	Sources        map[string]int // enabled sources per protocol
	SourcesUsable  map[string]int // of those, the ones that can be asked now
	SourcesErr     error
	SourcesKnown   bool           // at least one source exists
	SourceSummary  SourceSummary  // the detail behind the counts
	Clients        map[string]int // enabled clients per protocol
	EnabledClients int
	ClientsErr     error
}

// CanGrab reports whether some protocol has both a source and a client.
func (c Coverage) CanGrab() bool {
	for p, n := range c.Sources {
		if n > 0 && c.Clients[p] > 0 {
			return true
		}
	}
	return false
}

// Gaps lists protocols with sources but no client to hand them to.
func (c Coverage) Gaps() []string {
	var out []string
	for p, n := range c.Sources {
		if n > 0 && c.Clients[p] == 0 {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

// Coverage reads the sources and enabled clients.
func (s *Service) Coverage(ctx context.Context) Coverage {
	cov := Coverage{Clients: map[string]int{}}
	src := s.sourceSummary(ctx)
	cov.Sources, cov.SourcesUsable, cov.SourcesErr, cov.SourcesKnown = src.Enabled, src.Usable, src.Err, src.Configured
	cov.SourceSummary = src
	if s.st == nil {
		cov.ClientsErr = errors.New("no store configured")
		return cov
	}
	clients, err := s.st.ListDownloadClients(ctx)
	if err != nil {
		cov.ClientsErr = err
		return cov
	}
	for _, c := range clients {
		if !c.Enabled {
			continue
		}
		cov.EnabledClients++
		for _, p := range c.Protocols {
			cov.Clients[p]++
		}
	}
	return cov
}

// ErrNoKey re-exports the secretbox refusal for the HTTP layer.
var ErrNoKey = secretbox.ErrNoKey

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}
