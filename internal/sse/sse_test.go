package sse

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The stream must actually carry the payload — the previous broker used a
// chan struct{} and could only ever send a contentless ping.
func TestHandlerStreamsTypedEvent(t *testing.T) {
	b := New()
	rec := httptest.NewRecorder()
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/api/events", nil).WithContext(ctx)

	done := make(chan struct{})
	go func() { b.Handler(rec, req); close(done) }()

	waitForSubscriber(t, b)
	b.Publish("download", map[string]any{"clientJobId": "dlg-1", "speedBps": 1024})
	// Give the handler a moment to write, then close the stream.
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done

	body := rec.Body.String()
	if !strings.Contains(body, "event: download") {
		t.Fatalf("missing event name in %q", body)
	}
	if !strings.Contains(body, `"clientJobId":"dlg-1"`) || !strings.Contains(body, `"speedBps":1024`) {
		t.Fatalf("payload not streamed: %q", body)
	}
}

func TestHandlerSendsBarePing(t *testing.T) {
	b := New()
	rec := httptest.NewRecorder()
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/api/events", nil).WithContext(ctx)

	done := make(chan struct{})
	go func() { b.Handler(rec, req); close(done) }()

	waitForSubscriber(t, b)
	b.Notify()
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done

	if body := rec.Body.String(); !strings.Contains(body, "event: changed") {
		t.Fatalf("missing changed ping in %q", body)
	}
}

// A subscriber that cannot keep up must be dropped, never block the producer.
func TestPublishDoesNotBlockOnSlowSubscriber(t *testing.T) {
	b := New()
	ch := make(chan Event) // unbuffered, nobody reading
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()

	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			b.Publish("download", i)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Publish blocked on a subscriber that is not reading")
	}
}

func waitForSubscriber(t *testing.T, b *Broker) {
	t.Helper()
	for i := 0; i < 100; i++ {
		b.mu.Lock()
		n := len(b.subs)
		b.mu.Unlock()
		if n > 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("handler never subscribed")
}
