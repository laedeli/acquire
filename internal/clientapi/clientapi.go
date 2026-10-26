// Package clientapi is what acquire asks a download client directly, past the
// gateway. The gateway follows a job until it completes and then forgets it;
// what becomes of a finished download is acquire's to decide once it has
// copied the files into the library: a torrent seeds to its goal and is then
// removed with its data, an NZB download is released right away.
//
// Only the few calls that needs: qBittorrent's WebUI API v2 (log in, look a
// torrent up by the gateway's job tag, delete it with its files) and NZBGet's
// JSON-RPC (look a history record up, mark it released). The HTTP client is
// the caller's, so the endpoint policy applies to every connection.
package clientapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
)

// maxResponse caps what is read of any answer; maxHistory of NZBGet's history,
// which holds every download it keeps.
const (
	maxResponse = 4 << 20
	maxHistory  = 32 << 20
)

// ── qBittorrent ─────────────────────────────────────────────────────────────

// QBittorrent talks to a qBittorrent WebUI.
type QBittorrent struct {
	base, user, pass string
	http             *http.Client
}

// NewQBittorrent returns a client for the WebUI at base. hc is copied and
// given a cookie jar of its own for the session cookie.
func NewQBittorrent(base, user, pass string, hc *http.Client) *QBittorrent {
	c := *hc
	c.Jar, _ = cookiejar.New(nil)
	return &QBittorrent{base: strings.TrimRight(base, "/"), user: user, pass: pass, http: &c}
}

// Torrent is the part of a torrent acquire reads.
type Torrent struct {
	Hash  string `json:"hash"`
	State string `json:"state"`
}

// SeedingDone reports whether the torrent stopped after it completed: what
// qBittorrent does when its share limit (ratio or seeding time) is reached,
// stoppedUP since 5.0 and pausedUP before.
func (t Torrent) SeedingDone() bool { return t.State == "stoppedUP" || t.State == "pausedUP" }

// Torrent finds the torrent carrying tag, the gateway's job id. found is false
// when qBittorrent holds no such torrent.
func (q *QBittorrent) Torrent(ctx context.Context, tag string) (t Torrent, found bool, err error) {
	resp, err := q.authed(ctx, func() (*http.Request, error) {
		return http.NewRequestWithContext(ctx, http.MethodGet, q.base+"/api/v2/torrents/info?"+url.Values{"tag": {tag}}.Encode(), nil)
	})
	if err != nil {
		return Torrent{}, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Torrent{}, false, statusError("qbittorrent torrents/info", resp)
	}
	var list []Torrent
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponse)).Decode(&list); err != nil {
		return Torrent{}, false, fmt.Errorf("qbittorrent torrents/info: %w", err)
	}
	if len(list) == 0 {
		return Torrent{}, false, nil
	}
	return list[0], true, nil
}

// DeleteWithData removes the torrent and lets qBittorrent delete its files.
func (q *QBittorrent) DeleteWithData(ctx context.Context, hash string) error {
	form := url.Values{"hashes": {hash}, "deleteFiles": {"true"}}.Encode()
	resp, err := q.authed(ctx, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, q.base+"/api/v2/torrents/delete", strings.NewReader(form))
		if err == nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		return req, err
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return statusError("qbittorrent torrents/delete", resp)
	}
	return nil
}

// authed sends the request built by build, logging in first when there is no
// session yet and once more when the session has expired (403).
func (q *QBittorrent) authed(ctx context.Context, build func() (*http.Request, error)) (*http.Response, error) {
	send := func() (*http.Response, error) {
		req, err := build()
		if err != nil {
			return nil, err
		}
		// qBittorrent's CSRF check compares the Referer with the host.
		req.Header.Set("Referer", q.base)
		return q.http.Do(req)
	}
	if !q.hasSession() {
		if err := q.login(ctx); err != nil {
			return nil, err
		}
	}
	resp, err := send()
	if err != nil || resp.StatusCode != http.StatusForbidden {
		return resp, err
	}
	resp.Body.Close()
	if err := q.login(ctx); err != nil {
		return nil, err
	}
	return send()
}

func (q *QBittorrent) hasSession() bool {
	u, err := url.Parse(q.base)
	if err != nil {
		return false
	}
	for _, c := range q.http.Jar.Cookies(u) {
		if c.Name == "SID" && c.Value != "" {
			return true
		}
	}
	return false
}

func (q *QBittorrent) login(ctx context.Context) error {
	form := url.Values{"username": {q.user}, "password": {q.pass}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, q.base+"/api/v2/auth/login", strings.NewReader(form))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Referer", q.base)
	resp, err := q.http.Do(req)
	if err != nil {
		return fmt.Errorf("qbittorrent login: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
	// 200 "Ok." on success; 200 "Fails." for a wrong login; 403 when banned.
	if resp.StatusCode != http.StatusOK || strings.TrimSpace(string(body)) == "Fails." {
		return fmt.Errorf("qbittorrent login refused (HTTP %d)", resp.StatusCode)
	}
	return nil
}

// ── NZBGet ──────────────────────────────────────────────────────────────────

// ReleaseParameter marks an NZBGet history record acquire has released: the
// files it names are in the library now. NZBGet's API cannot delete a
// successful download's files (its history delete removes the folder of a
// failed or deleted one only), so the chart's NZBGet pod runs a loop that
// deletes the folder of every record carrying this mark, and the record.
const ReleaseParameter = "acquire:release"

// NZBGet talks to NZBGet's JSON-RPC API.
type NZBGet struct {
	base, user, pass string
	http             *http.Client
}

// NewNZBGet returns a client for the NZBGet at base.
func NewNZBGet(base, user, pass string, hc *http.Client) *NZBGet {
	return &NZBGet{base: strings.TrimRight(base, "/"), user: user, pass: pass, http: hc}
}

// Record is one download in NZBGet's history: its id, name, the folder it was
// unpacked to and how it ended.
type Record struct {
	ID      int    `json:"NZBID"`
	Name    string `json:"Name"`
	DestDir string `json:"DestDir"`
	Status  string `json:"Status"`
}

// Record finds history record id. found is false when NZBGet keeps no record
// of that id: released, deleted, or never there. NZBGet gives an id again
// once it forgot the download that had it, so the record is the download the
// id names now.
func (n *NZBGet) Record(ctx context.Context, id int) (rec Record, found bool, err error) {
	var history []Record
	if err := n.call(ctx, "history", []any{false}, &history); err != nil {
		return Record{}, false, err
	}
	for _, r := range history {
		if r.ID == id {
			return r, true, nil
		}
	}
	return Record{}, false, nil
}

// Release marks history record id released (ReleaseParameter=yes).
func (n *NZBGet) Release(ctx context.Context, id int) error {
	var ok bool
	if err := n.call(ctx, "editqueue", []any{"HistorySetParameter", 0, ReleaseParameter + "=yes", []int{id}}, &ok); err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("nzbget kept no record %d to release", id)
	}
	return nil
}

func (n *NZBGet) call(ctx context.Context, method string, params []any, out any) error {
	body, err := json.Marshal(map[string]any{"method": method, "params": params, "id": 1})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.base+"/jsonrpc", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if n.user != "" {
		req.SetBasicAuth(n.user, n.pass)
	}
	resp, err := n.http.Do(req)
	if err != nil {
		return fmt.Errorf("nzbget %s: %w", method, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return statusError("nzbget "+method, resp)
	}
	var rpc struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	limit := int64(maxResponse)
	if method == "history" {
		limit = maxHistory
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, limit)).Decode(&rpc); err != nil {
		return fmt.Errorf("nzbget %s: %w", method, err)
	}
	if rpc.Error != nil {
		return fmt.Errorf("nzbget %s: %s", method, truncate(rpc.Error.Message, 200))
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(rpc.Result, out)
}

// statusError names the status an endpoint answered with, never its body.
func statusError(op string, resp *http.Response) error {
	return &StatusError{Op: op, Code: resp.StatusCode}
}

// StatusError is an answer other than success.
type StatusError struct {
	Op   string
	Code int
}

func (e *StatusError) Error() string { return fmt.Sprintf("%s: HTTP %d", e.Op, e.Code) }

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
