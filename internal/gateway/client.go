// Package gateway is acquire's client for the download-gateway command API and
// a shared service-account token source (client-credentials against the shared
// realm) used for gateway calls + catalog writes.
package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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

// AddRequest mirrors the gateway's POST /api/v1/downloads body.
type AddRequest struct {
	Adapter      string `json:"adapter"`
	Source       string `json:"source"`
	Title        string `json:"title"`
	SavePath     string `json:"save_path"`
	WantedItemID string `json:"wanted_item_id"`
}

// AddResult is the gateway's 202 response.
type AddResult struct {
	Adapter     string `json:"adapter"`
	ClientJobID string `json:"client_job_id"`
}

// Add hands a source to a download client and returns the client job id.
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
	resp, err := c.HTTP.Do(hreq)
	if err != nil {
		return AddResult{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return AddResult{}, fmt.Errorf("gateway add %d: %s", resp.StatusCode, string(b))
	}
	var out AddResult
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return AddResult{}, err
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
