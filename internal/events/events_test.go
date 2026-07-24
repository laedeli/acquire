package events

import "testing"

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
