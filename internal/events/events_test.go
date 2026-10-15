package events

import (
	"encoding/json"
	"testing"
)

// The catalog's discovered and removed events are read in a group of their
// own, and an item event carries the identities ADR-0012 adds, optionally.
func TestCatalogTopicsAndIdentities(t *testing.T) {
	tp := TopicsFor("zaentrum-beta.")
	if tp.Discovered != "zaentrum-beta.catalog.item.discovered" || tp.Removed != "zaentrum-beta.catalog.item.removed" {
		t.Errorf("catalog topics: %+v", tp)
	}
	if CatalogGroup("zaentrum-beta-acquire") != "zaentrum-beta-acquire-catalog" {
		t.Error("catalog group")
	}
	var ev ItemEvent
	if err := json.Unmarshal([]byte(`{"eventId":"1","itemId":"e7","type":"episode",
	  "externalIds":{"tmdb":1234},"seriesExternalIds":{"tmdb":"97546","tvdb":362753},
	  "seasonNumber":4,"episodeNumber":7}`), &ev); err != nil {
		t.Fatal(err)
	}
	if ev.SeriesExternalIDs.Int("tmdb") != 97546 || ev.SeriesExternalIDs.Int("tvdb") != 362753 ||
		ev.ExternalIDs.Int("tmdb") != 1234 || *ev.SeasonNumber != 4 || *ev.EpisodeNumber != 7 {
		t.Errorf("event = %+v", ev)
	}
	var bare ItemEvent
	if err := json.Unmarshal([]byte(`{"eventId":"2","itemId":"x"}`), &bare); err != nil || bare.SeasonNumber != nil || !bare.ExternalIDs.Empty() {
		t.Errorf("an event without identities: %+v %v", bare, err)
	}
}

func TestTopicsFor(t *testing.T) {
	tp := TopicsFor("zaentrum-beta.")
	if tp.Completed != "zaentrum-beta.download.client.completed" {
		t.Errorf("completed = %s", tp.Completed)
	}
	if tp.Packaged != "zaentrum-beta.catalog.item.packaged" {
		t.Errorf("packaged = %s", tp.Packaged)
	}
	// blank falls back to stube. (prod default)
	if TopicsFor("").Failed != "stube.download.client.failed" {
		t.Errorf("blank fallback wrong: %s", TopicsFor("").Failed)
	}
}
