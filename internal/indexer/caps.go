package indexer

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Fleet reads the aggregator's indexer list and maps each entry to what it
// claims to answer.
//
// The claims are a HINT, not a contract. Measured against the live fleet:
// 23 of 54 enabled indexers advertise tv search params, only 2 advertise
// tvdbId, and one of those returns zero items for every id query it is given.
// The engine therefore uses caps to decide where to ask FIRST and always keeps
// a broader stage behind it.
func (c *Client) Fleet(ctx context.Context) ([]Capability, error) {
	if !c.Enabled() {
		return nil, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/api/v1/indexer", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Api-Key", c.APIKey)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fleet: http %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, err
	}
	var raw []struct {
		ID       int    `json:"id"`
		Name     string `json:"name"`
		Protocol string `json:"protocol"`
		Enable   bool   `json:"enable"`
		Caps     struct {
			TvSearchParams    []string `json:"tvSearchParams"`
			MovieSearchParams []string `json:"movieSearchParams"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("fleet: %w", err)
	}
	out := make([]Capability, 0, len(raw))
	for _, r := range raw {
		if !r.Enable {
			continue
		}
		out = append(out, Capability{
			ID: r.ID, Name: r.Name, Protocol: r.Protocol,
			AcceptsTVDBID:   has(r.Caps.TvSearchParams, "tvdbId"),
			AcceptsIMDBID:   has(r.Caps.TvSearchParams, "imdbId") || has(r.Caps.MovieSearchParams, "imdbId"),
			AcceptsSeasonEp: has(r.Caps.TvSearchParams, "season") && has(r.Caps.TvSearchParams, "ep"),
		})
	}
	return out, nil
}

func has(list []string, want string) bool {
	for _, v := range list {
		if strings.EqualFold(strings.TrimSpace(v), want) {
			return true
		}
	}
	return false
}
