// Package sse is a tiny server-sent-events broker: acquire's status changes fan
// out a payload-free "changed" PING, and the SPA / chino slot refetch the
// (authenticated) list on it — no polling, and the stream itself carries no data
// so it can be served unauthenticated (EventSource can't send a bearer).
package sse

import (
	"net/http"
	"sync"
	"time"
)

type Broker struct {
	mu   sync.Mutex
	subs map[chan struct{}]struct{}
}

func New() *Broker { return &Broker{subs: map[chan struct{}]struct{}{}} }

// Notify pings every subscriber (non-blocking; a busy subscriber coalesces).
func (b *Broker) Notify() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs {
		select {
		case ch <- struct{}{}:
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

	ch := make(chan struct{}, 4)
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
	w.Write([]byte(": connected\n\n"))
	fl.Flush()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ch:
			w.Write([]byte("event: changed\ndata: 1\n\n"))
			fl.Flush()
		case <-heartbeat.C:
			w.Write([]byte(": heartbeat\n\n"))
			fl.Flush()
		}
	}
}
