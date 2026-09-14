package app

import (
	"context"
	"sync"
	"time"
)

// SourceSummary is what acquire can search, per protocol.
//
// This is the one seam between the search-source registry and everything that
// asks "is there anything to search?" — setup, health, auto-grab and routing
// coverage. Until the sources live in acquire's own table it is answered from
// the configured search backend's indexer list; the registry replaces only the
// body of loadSourceSummary.
type SourceSummary struct {
	// Configured: a source backend or registry exists at all.
	Configured bool
	// Enabled counts enabled sources per protocol (usenet, torrent).
	Enabled map[string]int
	// Err is a failure to find out, as opposed to an answer of zero.
	Err error
}

// Total is the number of enabled sources across protocols.
func (s SourceSummary) Total() int {
	n := 0
	for _, v := range s.Enabled {
		n += v
	}
	return n
}

type sourceCache struct {
	mu  sync.Mutex
	sum SourceSummary
	at  time.Time
}

// sourceSummary is cached briefly: /api/config is unauthenticated and loaded on
// every console start, and must not fan out to the search backend each time.
func (s *Service) sourceSummary(ctx context.Context) SourceSummary {
	s.sources.mu.Lock()
	defer s.sources.mu.Unlock()
	if !s.sources.at.IsZero() && time.Since(s.sources.at) < time.Minute {
		return s.sources.sum
	}
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	sum := s.loadSourceSummary(cctx)
	s.sources.sum, s.sources.at = sum, time.Now()
	return sum
}

func (s *Service) loadSourceSummary(ctx context.Context) SourceSummary {
	sum := SourceSummary{Enabled: map[string]int{}}
	if s.pr == nil || !s.pr.Enabled() {
		return sum
	}
	sum.Configured = true
	idx, err := s.pr.Indexers(ctx)
	if err != nil {
		sum.Err = err
		return sum
	}
	for _, i := range idx {
		if i.Enable {
			sum.Enabled[i.Protocol]++
		}
	}
	return sum
}
