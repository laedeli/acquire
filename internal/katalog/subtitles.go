package katalog

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// The catalog's attach seam (katalog-manager): the languages every title is
// to have a subtitle in, a title's subtitles and what its original is matched
// by, a subtitle attached to a title, and one removed. Players list and play
// an attached subtitle as any other; nothing is packaged again.

// CatalogSubtitle is a subtitle of a title as the seam says it: one of the
// title's own (Origin nil), or one attached to it, by whom.
type CatalogSubtitle struct {
	ID         string  `json:"id"`
	Language   string  `json:"language"`
	Label      *string `json:"label"`
	Format     string  `json:"format"`
	Forced     bool    `json:"forced"`
	SDH        bool    `json:"sdh"`
	Origin     *string `json:"origin"`
	Attached   bool    `json:"attached"`
	VersionID  *string `json:"versionId,omitempty"`
	AttachedBy *string `json:"attachedBy,omitempty"`
}

// LabelText is the subtitle's label, "" without one.
func (s CatalogSubtitle) LabelText() string {
	if s.Label == nil {
		return ""
	}
	return *s.Label
}

// Original is what subtitle sources match a title's original by: its size,
// its size-and-sum fingerprint and what its file's name claimed.
type Original struct {
	SizeBytes int64          `json:"sizeBytes"`
	Sum64     string         `json:"sum64"`
	Labels    map[string]any `json:"labels"`
}

// ItemSubtitles is a title's subtitles as the seam answers them: the item
// whose file they are (an episode's holder when another's file covers it),
// the version a subtitle is attached to now or why none, and, asked for, the
// original's fingerprint or why there is none.
type ItemSubtitles struct {
	ItemID       string            `json:"itemId"`
	Type         string            `json:"type"`
	VersionID    *string           `json:"versionId"`
	Attachable   bool              `json:"attachable"`
	Reason       string            `json:"reason"`
	Subtitles    []CatalogSubtitle `json:"subtitles"`
	Original     *Original         `json:"original"`
	OriginalNote string            `json:"originalNote"`
}

// SeamError is an answer of the seam other than success.
type SeamError struct {
	Code int
	Body string
}

func (e *SeamError) Error() string {
	if m := e.Message(); m != "" {
		return fmt.Sprintf("the catalog: %s (HTTP %d)", m, e.Code)
	}
	return fmt.Sprintf("the catalog answered HTTP %d", e.Code)
}

// Message is the catalog's own sentence.
func (e *SeamError) Message() string {
	var body struct {
		Error string `json:"error"`
	}
	if json.Unmarshal([]byte(e.Body), &body) == nil && body.Error != "" {
		return body.Error
	}
	b := strings.TrimSpace(e.Body)
	if len(b) > 200 {
		return ""
	}
	return b
}

// NoSeam reports whether the catalog has no attach seam (one from before it).
func (e *SeamError) NoSeam() bool {
	return e.Code == http.StatusMethodNotAllowed || (e.Code == http.StatusNotFound && !strings.Contains(e.Body, "no such title"))
}

// SubtitleLanguages are the languages every title is to have a subtitle in,
// as the catalog names them (its subtitles.languages, else its packager's
// language whitelist), and the setting they come from.
func (c *Client) SubtitleLanguages(ctx context.Context) ([]string, string, error) {
	var out struct {
		Languages []string `json:"languages"`
		From      *string  `json:"from"`
	}
	if err := c.seam(ctx, http.MethodGet, "/api/library/subtitles/languages", nil, &out); err != nil {
		return nil, "", err
	}
	from := ""
	if out.From != nil {
		from = *out.From
	}
	return out.Languages, from, nil
}

// ItemSubtitles asks for a title's subtitles, and with fingerprint for what
// its original is matched by (the catalog reads 128 KiB of it).
func (c *Client) ItemSubtitles(ctx context.Context, itemID string, fingerprint bool) (ItemSubtitles, error) {
	path := "/api/library/items/" + url.PathEscape(itemID) + "/subtitles"
	if fingerprint {
		path += "?fingerprint=true"
	}
	var out ItemSubtitles
	err := c.seam(ctx, http.MethodGet, path, nil, &out)
	return out, err
}

// AttachRequest is a subtitle to attach: its text (SubRip or WebVTT, UTF-8),
// its language tag, what it says of itself and how it came to be.
type AttachRequest struct {
	Language string  `json:"language"`
	Label    *string `json:"label,omitempty"`
	Forced   bool    `json:"forced"`
	SDH      bool    `json:"sdh"`
	Origin   string  `json:"origin"`
	Format   string  `json:"format,omitempty"`
	Content  string  `json:"content"`
}

// AttachResult is the catalog's answer to an attach: the item it is the
// file's of, whether it was new, and the subtitle.
type AttachResult struct {
	ItemID   string          `json:"itemId"`
	Created  bool            `json:"created"`
	Subtitle CatalogSubtitle `json:"subtitle"`
}

// AttachSubtitle attaches a subtitle to a title.
func (c *Client) AttachSubtitle(ctx context.Context, itemID string, r AttachRequest) (AttachResult, error) {
	body, err := json.Marshal(r)
	if err != nil {
		return AttachResult{}, err
	}
	var out AttachResult
	err = c.seam(ctx, http.MethodPost, "/api/library/items/"+url.PathEscape(itemID)+"/subtitles", body, &out)
	return out, err
}

// DetachSubtitle removes a subtitle acquire attached to a title; one not
// there is removed as well.
func (c *Client) DetachSubtitle(ctx context.Context, itemID, subtitleID string) error {
	return c.seam(ctx, http.MethodDelete, "/api/library/items/"+url.PathEscape(itemID)+"/subtitles/"+url.PathEscape(subtitleID), nil, nil)
}

// ItemName is what the read side names an item: its type, title and year, an
// episode's numbers and its parent (its series, or a season of it).
type ItemName struct {
	Type     string `json:"type"`
	Title    string `json:"title"`
	Year     *int   `json:"year"`
	Season   *int   `json:"season_number"`
	Episode  *int   `json:"episode_number"`
	ParentID string `json:"parent_id"`
}

// ItemName reads what katalog-api names the item id.
func (c *Client) ItemName(ctx context.Context, id string) (ItemName, error) {
	if c.KatalogURL == "" {
		return ItemName{}, fmt.Errorf("katalog url not configured")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.KatalogURL+"/api/v1/items/"+url.PathEscape(id), nil)
	if err != nil {
		return ItemName{}, err
	}
	req.Header.Set("Accept", "application/json")
	c.authHeader(ctx, req)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return ItemName{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return ItemName{}, &SeamError{Code: resp.StatusCode, Body: string(b)}
	}
	var out ItemName
	err = json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&out)
	return out, err
}

func (c *Client) seam(ctx context.Context, method, path string, body []byte, into any) error {
	if c.ManagerURL == "" {
		return fmt.Errorf("katalog-manager URL not configured")
	}
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.ManagerURL+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	c.authHeader(ctx, req)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("the catalog could not be reached: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return &SeamError{Code: resp.StatusCode, Body: string(b)}
	}
	if into == nil || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(into); err != nil {
		return fmt.Errorf("the catalog's answer could not be read: %w", err)
	}
	return nil
}
