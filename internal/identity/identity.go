// Package identity is the identities a title has outside any one library — a
// TMDB, TVDB or IMDb id — as the catalog's holdings and events carry them
// (ADR-0012 of the platform: externalIds, seriesExternalIds).
package identity

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
)

// IDs maps an identity's kind (tmdb, tvdb, imdb, …) to its value. The catalog
// may write a number or a string; both read as the string.
type IDs map[string]string

// UnmarshalJSON reads {"tmdb": 97546} and {"tmdb": "97546"} alike, and skips
// what is neither (null, an object).
func (ids *IDs) UnmarshalJSON(b []byte) error {
	if bytes.Equal(bytes.TrimSpace(b), []byte("null")) {
		*ids = nil
		return nil
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	out := IDs{}
	for k, v := range raw {
		var s string
		if json.Unmarshal(v, &s) == nil {
			if s = strings.TrimSpace(s); s != "" {
				out[strings.ToLower(k)] = s
			}
			continue
		}
		var n json.Number
		if json.Unmarshal(v, &n) == nil && n.String() != "" {
			out[strings.ToLower(k)] = n.String()
		}
	}
	*ids = out
	return nil
}

// Int reads an id that is a number (tmdb, tvdb); 0 when it is not there.
func (ids IDs) Int(kind string) int64 {
	n, err := strconv.ParseInt(ids[kind], 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// IMDb reads the IMDb id, with its tt prefix.
func (ids IDs) IMDb() string {
	v := strings.TrimSpace(ids["imdb"])
	if v == "" {
		return ""
	}
	if !strings.HasPrefix(v, "tt") {
		v = "tt" + v
	}
	return v
}

// Empty reports whether no identity is known.
func (ids IDs) Empty() bool { return ids.Int("tmdb") == 0 && ids.Int("tvdb") == 0 && ids.IMDb() == "" }
