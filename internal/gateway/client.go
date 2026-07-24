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
