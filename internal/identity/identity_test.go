package identity

import (
	"encoding/json"
	"testing"
)

func TestIDsReadNumbersAndStrings(t *testing.T) {
	var v struct {
		IDs  IDs `json:"externalIds"`
		None IDs `json:"none"`
	}
	if err := json.Unmarshal([]byte(`{"externalIds":{"TMDB":97546,"tvdb":"362753","imdb":"0133093","other":{"x":1},"blank":""},"none":null}`), &v); err != nil {
		t.Fatal(err)
	}
	if v.IDs.Int("tmdb") != 97546 || v.IDs.Int("tvdb") != 362753 || v.IDs.IMDb() != "tt0133093" {
		t.Errorf("ids = %v", v.IDs)
	}
	if _, ok := v.IDs["other"]; ok {
		t.Error("an object is no identity")
	}
	if _, ok := v.IDs["blank"]; ok {
		t.Error("an empty value is no identity")
	}
	if v.None != nil || !v.None.Empty() || v.IDs.Empty() {
		t.Errorf("empty: %v %v", v.None, v.IDs)
	}
}
