// Package gateway is acquire's client for the download-gateway command API and
// a shared service-account token source (client-credentials against the shared
// realm) used for gateway calls + catalog writes.
package gateway

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// TokenSource mints + caches a client-credentials access token for the service
// account. Thread-safe; refreshes ~30s before expiry.
type TokenSource struct {
	TokenURL string
	ClientID string
	Secret   string
	HTTP     *http.Client

	mu     sync.Mutex
	token  string
	expiry time.Time
}

func NewTokenSource(tokenURL, clientID, secret string) *TokenSource {
	return &TokenSource{
		TokenURL: tokenURL, ClientID: clientID, Secret: secret,
		HTTP: &http.Client{Timeout: 10 * time.Second},
	}
}

// Enabled reports whether a token endpoint + client are configured.
func (t *TokenSource) Enabled() bool {
	return t != nil && t.TokenURL != "" && t.ClientID != ""
}

func (t *TokenSource) Token(ctx context.Context) (string, error) {
	if !t.Enabled() {
		return "", nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.token != "" && time.Now().Before(t.expiry) {
		return t.token, nil
	}
	form := url.Values{"grant_type": {"client_credentials"}, "client_id": {t.ClientID}, "client_secret": {t.Secret}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := t.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("token endpoint %d: %s", resp.StatusCode, string(b))
	}
	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	t.token = out.AccessToken
	ttl := out.ExpiresIn
	if ttl <= 0 {
		ttl = 60
	}
	t.expiry = time.Now().Add(time.Duration(ttl-30) * time.Second)
	return t.token, nil
}

// Client is the download-gateway command client.
type Client struct {
	BaseURL string
	Tokens  *TokenSource
	HTTP    *http.Client
}

func New(baseURL string, tokens *TokenSource) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Tokens:  tokens,
		HTTP:    &http.Client{Timeout: 15 * time.Second},
	}
}

func (c *Client) Enabled() bool { return c != nil && c.BaseURL != "" }

// addHTTP is the client for one add. A payload add carries a whole NZB or torrent
// file that the gateway forwards to the download client before it answers, so the
// usual short timeout would give up on a large file the client is still accepting —
// and a retry would then add it twice. Allow one extra second per MiB, at least
// two minutes, on top of the configured timeout.
func (c *Client) addHTTP(bodyBytes int) *http.Client {
	if bodyBytes < 1<<20 || c.HTTP.Timeout == 0 {
		return c.HTTP
	}
	longer := *c.HTTP
	longer.Timeout = c.HTTP.Timeout + max(2*time.Minute, time.Duration(bodyBytes>>20)*time.Second)
	return &longer
}

// AddRequest mirrors the gateway's POST /api/v1/downloads body.
//
// Adapter and Client both name the client id: a gateway with runtime client
// configuration reads either, an older one only adapter, so both are sent.
// With PayloadB64 the gateway uploads the NZB or .torrent content itself and
// Source stays empty — the link that carried a search source's credential never
// leaves acquire.
type AddRequest struct {
	Adapter      string `json:"adapter"`
	Client       string `json:"client,omitempty"`
	Source       string `json:"source,omitempty"`
	Title        string `json:"title"`
	SavePath     string `json:"save_path,omitempty"`
	WantedItemID string `json:"wanted_item_id"`
	PayloadB64   string `json:"payload_b64,omitempty"`
	PayloadName  string `json:"payload_name,omitempty"`
	Category     string `json:"category,omitempty"`
}

// ErrUnknownClient means the gateway does not know the client an add named —
// typically because it restarted and has not been sent the configuration yet.
var ErrUnknownClient = errors.New("the download gateway does not know this client")

// ErrGatewayBusy means the gateway refused a large add because too many large
// adds are in flight; nothing was added, so the grab can be tried again later.
var ErrGatewayBusy = errors.New("the download gateway is busy with other large adds")

// ErrNoConfigAPI means the gateway predates runtime client configuration, or
// runs without the settings that enable it.
var ErrNoConfigAPI = errors.New("the download gateway does not offer the configuration API")

// ErrUnreachable means an add never reached the gateway — it could not be
// connected to — so nothing was added and the same add may be tried later.
var ErrUnreachable = errors.New("the download gateway cannot be reached")

// ErrAnswerLost means an add was sent but no answer came back that says what
// became of it — the connection dropped, the wait ran out, the answer could
// not be read. The gateway may have handed the release to its client: an add
// whose answer was lost must not be undone, nor sent again.
var ErrAnswerLost = errors.New("the download gateway's answer to the add was lost")

// AddError is an add the gateway answered without taking it.
type AddError struct {
	Status int
	Body   string
}

func (e *AddError) Error() string {
	return fmt.Sprintf("gateway add %d: %s", e.Status, strings.TrimSpace(e.Body))
}

// Rejected reports whether the download client refused the release itself —
// a file it cannot read, a source it will not take, as the gateway's adapters
// say ("… rejected the source") — rather than being out of reach. Another
// release may do.
func (e *AddError) Rejected() bool {
	return e.Status == http.StatusBadGateway && strings.Contains(strings.ToLower(e.Body), "rejected")
}

// Temporary reports whether the same add may be taken later: the gateway, or
// the client behind it, down, failing or busy.
func (e *AddError) Temporary() bool {
	return !e.Rejected() && (e.Status >= 500 || e.Status == http.StatusTooManyRequests || e.Status == http.StatusRequestTimeout)
}

// notSent reports whether a request's error came before anything of it was
// sent: no connection could be made.
func notSent(err error) bool {
	var op *net.OpError
	if errors.As(err, &op) && (op.Op == "dial" || op.Op == "proxyconnect") {
		return true
	}
	var dns *net.DNSError
	if errors.As(err, &dns) {
		return true
	}
	var cert *tls.CertificateVerificationError
	return errors.As(err, &cert)
}

// AddResult is the gateway's 202 response.
type AddResult struct {
	Adapter     string `json:"adapter"`
	ClientJobID string `json:"client_job_id"`
}

// Add hands a source to a download client and returns the client job id.
//
// What an add that did not succeed says: ErrUnreachable (nothing was sent),
// ErrUnknownClient, ErrGatewayBusy and *AddError (the gateway answered and
// took nothing), or ErrAnswerLost (the gateway may have taken it).
func (c *Client) Add(ctx context.Context, req AddRequest) (AddResult, error) {
	if !c.Enabled() {
		return AddResult{}, fmt.Errorf("download gateway not configured")
	}
	body, _ := json.Marshal(req)
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/api/v1/downloads", bytes.NewReader(body))
	if err != nil {
		return AddResult{}, err
	}
	hreq.Header.Set("Content-Type", "application/json")
	if c.Tokens.Enabled() {
		if tok, err := c.Tokens.Token(ctx); err == nil && tok != "" {
			hreq.Header.Set("Authorization", "Bearer "+tok)
		}
	}
	resp, err := c.addHTTP(len(body)).Do(hreq)
	if err != nil {
		if notSent(err) {
			return AddResult{}, fmt.Errorf("%w: %v", ErrUnreachable, err)
		}
		return AddResult{}, fmt.Errorf("%w: %v", ErrAnswerLost, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return AddResult{}, fmt.Errorf("%w: %s", ErrUnknownClient, strings.TrimSpace(string(b)))
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return AddResult{}, fmt.Errorf("%w (retry after %ss)", ErrGatewayBusy, resp.Header.Get("Retry-After"))
	}
	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return AddResult{}, &AddError{Status: resp.StatusCode, Body: string(b)}
	}
	var out AddResult
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return AddResult{}, fmt.Errorf("%w: its answer could not be read: %v", ErrAnswerLost, err)
	}
	if out.ClientJobID == "" {
		return AddResult{}, fmt.Errorf("%w: it answered %d without the client's job", ErrAnswerLost, resp.StatusCode)
	}
	return out, nil
}

// Job is one in-flight download as the gateway currently sees it. Used to
// reconcile after a restart: acquire's Kafka consumer starts at the latest
// offset, so without this it would never learn about downloads that began
// while it was down.
type Job struct {
	Adapter      string  `json:"adapter"`
	ClientJobID  string  `json:"client_job_id"`
	WantedItemID string  `json:"wanted_item_id"`
	Title        string  `json:"title"`
	State        string  `json:"state"`
	NativeState  string  `json:"native_state"`
	ProgressPct  float64 `json:"progress_pct"`
	Downloaded   int64   `json:"downloaded_bytes"`
	SizeBytes    *int64  `json:"size_bytes"`
	SpeedBps     int64   `json:"speed_bps"`
	EtaSec       *int32  `json:"eta_sec"`
	Seeders      *int32  `json:"seeders"`
	Leechers     *int32  `json:"leechers"`
	Health       *int32  `json:"health"`
}

// ClientStatus is one download client's health + aggregate throughput.
type ClientStatus struct {
	ID        string            `json:"id,omitempty"`
	Type      string            `json:"type,omitempty"`
	Name      string            `json:"name"`
	Reachable bool              `json:"reachable"`
	Error     string            `json:"error,omitempty"`
	DownBps   int64             `json:"down_bps"`
	UpBps     int64             `json:"up_bps"`
	Paused    bool              `json:"paused"`
	FreeDisk  *int64            `json:"free_disk_bytes,omitempty"`
	Detail    map[string]string `json:"detail,omitempty"`
}

// List returns the gateway's in-flight jobs.
func (c *Client) List(ctx context.Context) ([]Job, error) {
	var out []Job
	if err := c.getJSON(ctx, "/api/v1/downloads", &out); err != nil {
		return nil, err
	}
	return out, nil
}

// JobView is one client job as its client describes it now: the list's
// fields, the files of a completed job (absolute paths, as the client reports
// them) and the client's message. WantedItemID is set only while the gateway
// tracks the job.
type JobView struct {
	Job
	Files   []string `json:"files"`
	Message string   `json:"message"`
}

// ErrJobNotFound means the gateway asked the client, and the client does not
// know the job: it never had it, or no longer does — removed, or gone from its
// history. A client that cannot be asked is another error.
var ErrJobNotFound = errors.New("the download client does not know this job")

// ErrNoDescribe means the gateway predates describing one job.
var ErrNoDescribe = errors.New("the download gateway cannot describe a job")

// Describe asks what a client job is now, as its client describes it — a
// finished one too, which the gateway no longer tracks. What a call that
// describes nothing says: ErrJobNotFound (the client does not know the job),
// ErrUnknownClient (the gateway does not know the client: it restarted and
// has not been told its clients yet), ErrNoDescribe (an older gateway), or
// any other error when the gateway or the client cannot be asked.
func (c *Client) Describe(ctx context.Context, adapter, clientJobID string) (JobView, error) {
	if !c.Enabled() {
		return JobView{}, fmt.Errorf("download gateway not configured")
	}
	path := "/api/v1/downloads/" + url.PathEscape(adapter) + "/" + url.PathEscape(clientJobID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return JobView{}, err
	}
	c.authorize(ctx, req)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return JobView{}, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		var out JobView
		if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&out); err != nil {
			return JobView{}, fmt.Errorf("gateway %s: %w", path, err)
		}
		return out, nil
	case http.StatusNotFound:
		// The gateway says which it does not know: a job of a client it runs
		// carries its native state, "not-found". A 404 that is no answer of
		// the call at all is a gateway without it.
		var body struct {
			Error       string `json:"error"`
			NativeState string `json:"native_state"`
		}
		if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<10)).Decode(&body); err != nil {
			return JobView{}, ErrNoDescribe
		}
		if body.NativeState == "not-found" {
			return JobView{}, fmt.Errorf("%w: %s", ErrJobNotFound, body.Error)
		}
		return JobView{}, fmt.Errorf("%w: %s", ErrUnknownClient, body.Error)
	case http.StatusMethodNotAllowed, http.StatusNotImplemented:
		// Before the call, the path took DELETE alone.
		return JobView{}, ErrNoDescribe
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	return JobView{}, fmt.Errorf("gateway %s %d: %s", path, resp.StatusCode, strings.TrimSpace(string(b)))
}

// ClientsStatus reports per-client health and speed.
func (c *Client) ClientsStatus(ctx context.Context) ([]ClientStatus, error) {
	var out []ClientStatus
	if err := c.getJSON(ctx, "/api/v1/clients/status", &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Cancel removes a job from its download client (non-destructive: the bytes
// already on disk stay).
func (c *Client) Cancel(ctx context.Context, adapter, clientJobID string) error {
	return c.do(ctx, http.MethodDelete, "/api/v1/downloads/"+url.PathEscape(adapter)+"/"+url.PathEscape(clientJobID))
}

// Pause pauses a job; Resume restarts it. Not every client supports this —
// the gateway answers 501 for those.
func (c *Client) Pause(ctx context.Context, adapter, clientJobID string) error {
	return c.do(ctx, http.MethodPost, "/api/v1/downloads/"+url.PathEscape(adapter)+"/"+url.PathEscape(clientJobID)+"/pause")
}

func (c *Client) Resume(ctx context.Context, adapter, clientJobID string) error {
	return c.do(ctx, http.MethodPost, "/api/v1/downloads/"+url.PathEscape(adapter)+"/"+url.PathEscape(clientJobID)+"/resume")
}

// authorize attaches the service-account bearer when one is configured.
func (c *Client) authorize(ctx context.Context, req *http.Request) {
	if c.Tokens.Enabled() {
		if tok, err := c.Tokens.Token(ctx); err == nil && tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
	}
}

func (c *Client) getJSON(ctx context.Context, path string, out any) error {
	if !c.Enabled() {
		return fmt.Errorf("download gateway not configured")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return err
	}
	c.authorize(ctx, req)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("gateway %s %d: %s", path, resp.StatusCode, string(b))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *Client) do(ctx context.Context, method, path string) error {
	if !c.Enabled() {
		return fmt.Errorf("download gateway not configured")
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, nil)
	if err != nil {
		return err
	}
	c.authorize(ctx, req)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	return fmt.Errorf("gateway %s %d: %s", path, resp.StatusCode, string(b))
}

// ── runtime client configuration (/api/v1/config/*) ─────────────────────────

// ClientType is one adapter type the gateway has compiled in.
type ClientType struct {
	Type             string   `json:"type"`
	Protocols        []string `json:"protocols"`
	Auth             []string `json:"auth"`
	AcceptsPayload   bool     `json:"acceptsPayload"`
	SupportsSavePath bool     `json:"supportsSavePath"`
	CanPause         bool     `json:"canPause"`
}

// ConfigClient is one client as acquire sends it: the only place a decrypted
// secret exists outside a single request. It redacts itself in every textual
// form, so a stray log line cannot print the credential.
type ConfigClient struct {
	ID        string   `json:"id"`
	Type      string   `json:"type"`
	BaseURL   string   `json:"baseUrl"`
	Auth      string   `json:"auth"`
	Username  string   `json:"username"`
	Secret    string   `json:"secret"`
	Category  string   `json:"category"`
	SavePath  string   `json:"savePath"`
	Protocols []string `json:"protocols"`
}

func (c ConfigClient) String() string {
	return fmt.Sprintf("{id:%s type:%s baseUrl:%s auth:%s secret:%s}", c.ID, c.Type, c.BaseURL, c.Auth, redacted(c.Secret))
}

// GoString keeps %#v from printing the secret field.
func (c ConfigClient) GoString() string { return "gateway.ConfigClient" + c.String() }

// LogValue keeps slog from printing the secret field.
func (c ConfigClient) LogValue() slog.Value {
	return slog.GroupValue(slog.String("id", c.ID), slog.String("type", c.Type), slog.String("secret", redacted(c.Secret)))
}

func redacted(s string) string {
	if s == "" {
		return ""
	}
	return "[redacted]"
}

// ConfigClientView is a client as the gateway reports it: never a secret.
type ConfigClientView struct {
	ID        string   `json:"id"`
	Type      string   `json:"type"`
	BaseURL   string   `json:"baseUrl"`
	Auth      string   `json:"auth"`
	Username  string   `json:"username"`
	Category  string   `json:"category"`
	SavePath  string   `json:"savePath"`
	Protocols []string `json:"protocols"`
	Source    string   `json:"source"` // api | env
	SecretSet bool     `json:"secretSet"`
}

// ConfigState is GET /api/v1/config/clients.
type ConfigState struct {
	Revision int64              `json:"revision"`
	Clients  []ConfigClientView `json:"clients"`
}

// ConfigPut is the PUT /api/v1/config/clients body: the complete set of
// api-sourced clients.
type ConfigPut struct {
	Revision int64          `json:"revision"`
	Clients  []ConfigClient `json:"clients"`
}

// ApplyError is one client the gateway could not apply.
type ApplyError struct {
	ID      string `json:"id"`
	Message string `json:"message"`
}

// ApplyResult is the PUT response.
type ApplyResult struct {
	Revision int64        `json:"revision"`
	Applied  []string     `json:"applied"`
	Errors   []ApplyError `json:"errors"`
}

// TestResult is POST /api/v1/config/clients/test.
type TestResult struct {
	Reachable bool          `json:"reachable"`
	Version   string        `json:"version"`
	Error     string        `json:"error"`
	Status    *ClientStatus `json:"status,omitempty"`
}

// ConfigTypes lists the client types the gateway can run.
func (c *Client) ConfigTypes(ctx context.Context) ([]ClientType, error) {
	var out []ClientType
	if err := c.configJSON(ctx, http.MethodGet, "/api/v1/config/types", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ConfigClients reports the revision and clients the gateway is running.
func (c *Client) ConfigClients(ctx context.Context) (ConfigState, error) {
	var out ConfigState
	err := c.configJSON(ctx, http.MethodGet, "/api/v1/config/clients", nil, &out)
	return out, err
}

// PutConfigClients replaces every api-sourced client on the gateway.
func (c *Client) PutConfigClients(ctx context.Context, body ConfigPut) (ApplyResult, error) {
	if body.Clients == nil {
		body.Clients = []ConfigClient{}
	}
	var out ApplyResult
	err := c.configJSON(ctx, http.MethodPut, "/api/v1/config/clients", body, &out)
	return out, err
}

// TestClient asks the gateway to reach a client without registering it.
func (c *Client) TestClient(ctx context.Context, cc ConfigClient) (TestResult, error) {
	var out TestResult
	err := c.configJSON(ctx, http.MethodPost, "/api/v1/config/clients/test", cc, &out)
	return out, err
}

// configJSON is one call to the configuration API. A 404 there means the API
// itself is absent — an older gateway, or one without ALLOWED_CLIENTS — and is
// reported as ErrNoConfigAPI so callers can say so instead of "not found".
func (c *Client) configJSON(ctx context.Context, method, path string, in, out any) error {
	if !c.Enabled() {
		return fmt.Errorf("download gateway not configured")
	}
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	c.authorize(ctx, req)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return ErrNoConfigAPI
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("gateway %s %d: %s", path, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(out)
}
