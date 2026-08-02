package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// systemHealth is a DIAGNOSTIC endpoint, deliberately separate from /readyz.
//
// Readiness must stay a cheap liveness signal. acquire runs at replicas: 1, so
// a readiness probe that fails on a degraded dependency removes the only pod
// from the Service — taking away the console you would use to diagnose it, and
// turning a partial outage into a total one. This endpoint reports "degraded"
// and stays 200 for exactly that reason.
//
//	GET /api/health/system
func (s *Server) systemHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	type check struct {
		Name   string `json:"name"`
		OK     bool   `json:"ok"`
		Detail string `json:"detail,omitempty"`
	}
	// A health endpoint must never panic: it is the thing you call WHEN
	// something is broken, so a missing dependency has to be reportable rather
	// than fatal.
	if s.st == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"status": "degraded",
			"checks": []map[string]any{{"name": "database", "ok": false, "detail": "no store configured"}},
		})
		return
	}

	var checks []check
	add := func(name string, err error, detail string) {
		c := check{Name: name, OK: err == nil, Detail: detail}
		if err != nil {
			c.Detail = err.Error()
		}
		checks = append(checks, c)
	}

	// The database is the only hard dependency; everything else degrades.
	counts, err := s.st.Counts(ctx)
	add("database", err, fmt.Sprintf("%d titles, %d targets", counts.Titles, counts.Targets))

	// A climbing outbox means the relay is failing while writes keep
	// succeeding — the failure mode that is otherwise completely invisible.
	depth, dErr := s.st.OutboxDepth(ctx)
	outboxOK := dErr
	if dErr == nil && depth > 1000 {
		outboxOK = fmt.Errorf("%d events pending — the relay is not draining", depth)
	}
	add("outbox", outboxOK, fmt.Sprintf("%d pending", depth))

	// Disk is the one dependency that destroys production rather than degrading
	// it, and acquire has no deletion code.
	if free := s.svc.FreeBytes(); free < 0 {
		add("storage", fmt.Errorf("cannot read free space at the downloads root"), "")
	} else {
		var derr error
		if free < s.cfg.StorageFloorBytes() {
			derr = fmt.Errorf("%d GB free is below the %d GB floor; grabs are refused",
				free>>30, s.cfg.StorageFloorBytes()>>30)
		}
		add("storage", derr, fmt.Sprintf("%d GB free", free>>30))
	}

	// A clock that has silently stopped looks identical to a quiet system.
	overdue, oErr := s.st.OverdueSchedules(ctx, 3)
	clockErr := oErr
	if oErr == nil && len(overdue) > 0 {
		clockErr = fmt.Errorf("schedules overdue: %s", strings.Join(overdue, ", "))
	}
	add("clock", clockErr, "")

	status := "ok"
	for _, c := range checks {
		if !c.OK {
			status = "degraded"
			break
		}
	}
	// Always 200. See the doc comment: this is a diagnostic, not a probe.
	writeJSON(w, http.StatusOK, map[string]any{
		"status": status, "checks": checks, "counts": counts,
	})
}

// metrics exposes Prometheus text format by hand.
//
// Written out rather than pulled in: the whole surface is a handful of gauges,
// and a metrics library is a dependency, a scrape registry and an init-order
// question in exchange for string formatting we can do in twenty lines.
//
//	GET /metrics
func (s *Server) metrics(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	var b strings.Builder
	gauge := func(name, help string, v int) {
		if name == "" {
			return
		}
		fmt.Fprintf(&b, "# HELP %s %s\n# TYPE %s gauge\n%s %d\n", name, help, name, name, v)
	}

	// Same rule as systemHealth: a scrape that panics loses the metrics that
	// would have explained the breakage.
	if s.st == nil {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		_, _ = w.Write([]byte("# acquire: no store configured\n"))
		return
	}
	if c, err := s.st.Counts(ctx); err == nil {
		gauge("acquire_titles", "Tracked titles.", c.Titles)
		gauge("acquire_titles_series", "Tracked series.", c.Series)
		gauge("acquire_titles_movies", "Tracked movies.", c.Movies)
		gauge("acquire_targets", "Acquisition targets.", c.Targets)
		gauge("acquire_targets_held", "Targets whose file we hold.", c.Held)
		gauge("acquire_targets_missing", "Monitored, aired, still wanted.", c.Missing)
		gauge("acquire_targets_unaired", "Not yet searchable.", c.Unaired)
		gauge("acquire_targets_backoff", "In search backoff after failures.", c.InBackoff)
	}
	if depth, err := s.st.OutboxDepth(ctx); err == nil {
		// The single most diagnostic number in the service: it climbs when the
		// relay fails, and nothing else surfaces that.
		gauge("acquire_outbox_pending", "Undelivered events.", int(depth))
	}
	if free := s.svc.FreeBytes(); free >= 0 {
		gauge("acquire_storage_free_gb",
			"Free space where downloads land. The export runs at 92%; beta shares it with production.",
			int(free>>30))
	}
	if n, err := s.st.ActiveDownloads(ctx); err == nil {
		gauge("acquire_downloads_active", "In-flight downloads.", n)
	}
	if n, err := s.st.BlocklistSize(ctx); err == nil {
		gauge("acquire_blocklist", "Releases blocked from being re-picked.", n)
	}
	if overdue, err := s.st.OverdueSchedules(ctx, 3); err == nil {
		gauge("acquire_schedules_overdue",
			"Enabled schedules more than 3 intervals late; >0 means the clock has stopped.",
			len(overdue))
	}

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	_, _ = w.Write([]byte(b.String()))
}
