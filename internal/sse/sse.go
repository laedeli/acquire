// Package sse is a tiny server-sent-events broker.
//
// It carries two kinds of message. A payload-free "changed" ping tells clients
// to refetch the (authenticated) lists — that is enough for status transitions.
// Download telemetry instead streams as typed "download" events with a JSON
// body, because a progress bar that had to refetch the whole list every few
// seconds would be both laggy and wasteful.
//
// Because the stream can now carry data, subscribing requires a bearer: the SPA
// reads it with fetch-streaming rather than EventSource (which cannot send an
// Authorization header).
package sse

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

// Event is one message on the wire. Name is the SSE event name; Data is
// marshalled to JSON (nil for a bare ping).
type Event struct {
	Name string
	Data any
}

type Broker struct {
	mu   sync.Mutex
	subs map[chan Event]struct{}
}

func New() *Broker { return &Broker{subs: map[chan Event]struct{}{}} }

// Notify sends a payload-free "changed" ping: refetch the lists.
func (b *Broker) Notify() { b.publish(Event{Name: "changed"}) }

// Publish fans a typed event out to every subscriber.
func (b *Broker) Publish(name string, data any) { b.publish(Event{Name: name, Data: data}) }

// publish is non-blocking: a subscriber that cannot keep up drops this message
// rather than stalling the producer. Losing a progress tick is harmless — the
// next one (or the fallback poll) re-syncs the client.
func (b *Broker) publish(ev Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs {
		select {
		case ch <- ev:
		default:
		}
	}
}

// Handler streams events to one client until it disconnects. Sends a heartbeat
// every 20s (under the router's 30s idle timeout).
func (b *Broker) Handler(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	// Proxies must not buffer an event stream.
	w.Header().Set("X-Accel-Buffering", "no")

	ch := make(chan Event, 32)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		delete(b.subs, ch)
		b.mu.Unlock()
	}()

	heartbeat := time.NewTicker(20 * time.Second)
	defer heartbeat.Stop()

	// Initial comment so proxies flush headers.
	_, _ = w.Write([]byte(": connected\n\n"))
	fl.Flush()

	for {
		select {
		case <-r.Context().Done():
			return
		case ev := <-ch:
			payload := []byte("1")
			if ev.Data != nil {
				b, err := json.Marshal(ev.Data)
				if err != nil {
					continue
				}
				payload = b
			}
			_, _ = w.Write([]byte("event: " + ev.Name + "\ndata: "))
			_, _ = w.Write(payload)
			_, _ = w.Write([]byte("\n\n"))
			fl.Flush()
		case <-heartbeat.C:
			_, _ = w.Write([]byte(": heartbeat\n\n"))
			fl.Flush()
		}
	}
}
