package events

import (
	"encoding/json"
	"testing"
)

// The gateway's progress payload must decode into DownloadEvent with the
// correlation id and telemetry intact — this is what drives the console.
func TestDecodeProgressEvent(t *testing.T) {
	raw := `{
	  "client_id":"dlg-abc","adapter":"qbittorrent","wanted_item_id":"w_1","title":"Some Title",
	  "state":"downloading","native_state":"stalledDL","progress_pct":42.5,
	  "downloaded_bytes":1048576,"size_bytes":2097152,"speed_bps":0,"eta_sec":120,
	  "seeders":7,"leechers":3
	}`
	var ev DownloadEvent
	if err := json.Unmarshal([]byte(raw), &ev); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if ev.WantedID != "w_1" || ev.Adapter != "qbittorrent" || ev.ClientID != "dlg-abc" {
		t.Fatalf("correlation fields lost: %+v", ev)
	}
	if ev.NativeState != "stalledDL" {
		t.Errorf("native state = %q, want stalledDL", ev.NativeState)
	}
	// 0 must survive as a real "idle" value, not be mistaken for unknown.
	if ev.SpeedBps != 0 {
		t.Errorf("speed = %d, want 0", ev.SpeedBps)
	}
	if ev.SizeBytes == nil || *ev.SizeBytes != 2097152 {
		t.Errorf("size = %v, want 2097152", ev.SizeBytes)
	}
	if ev.EtaSec == nil || *ev.EtaSec != 120 {
		t.Errorf("eta = %v, want 120", ev.EtaSec)
	}
	if ev.Seeders == nil || *ev.Seeders != 7 {
		t.Errorf("seeders = %v, want 7", ev.Seeders)
	}
}

// An unknown size/eta must stay nil rather than decoding to a misleading zero.
func TestDecodeProgressUnknowns(t *testing.T) {
	var ev DownloadEvent
	if err := json.Unmarshal([]byte(`{"client_id":"x","adapter":"nzbget","size_bytes":null,"eta_sec":null}`), &ev); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if ev.SizeBytes != nil || ev.EtaSec != nil || ev.Seeders != nil {
		t.Fatalf("unknowns should stay nil: %+v", ev)
	}
}

// The consumer must subscribe to the progress + started topics, or the console
// gets no telemetry at all (the original bug: only 3 topics were consumed).
func TestTopicsIncludeProgressAndStarted(t *testing.T) {
	tp := TopicsFor("zaentrum-beta.")
	if tp.Progress != "zaentrum-beta.download.client.progress" {
		t.Errorf("progress topic = %q", tp.Progress)
	}
	if tp.Started != "zaentrum-beta.download.client.started" {
		t.Errorf("started topic = %q", tp.Started)
	}
}
