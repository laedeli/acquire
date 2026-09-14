package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/laedeli/acquire/internal/indexer"
	"github.com/laedeli/acquire/internal/store"
)

// Search sources are acquire's configuration: stored here, edited in the
// console, queried directly with their own keys. Nothing outside acquire holds
// the list or a key.

// capsMaxAge is how old cached caps may get before the daily refresh asks again.
const capsMaxAge = 24 * time.Hour

// defaultCategories scope a new source to the standard parent categories.
var defaultCategories = indexer.Categories{Movie: []int{indexer.CatMovie}, TV: []int{indexer.CatTV}}

// OptionalInt is a number field in a write where omitted keeps the stored
// value, null means "no limit", and a number sets it.
type OptionalInt struct {
	Set   bool
	Value *int
}

func (o *OptionalInt) UnmarshalJSON(b []byte) error {
	o.Set = true
	if string(b) == "null" {
		o.Value = nil
		return nil
	}
	var n int
	if err := json.Unmarshal(b, &n); err != nil {
		return errors.New("must be a whole number or null")
	}
	o.Value = &n
	return nil
}

// SourceInput is a search source as the console writes it.
type SourceInput struct {
	// ID is only read by a test: it names the stored source whose API key is
	// used when the body carries none.
	ID            int64               `json:"id,omitempty"`
	Name          string              `json:"name"`
	Protocol      string              `json:"protocol"`
	BaseURL       string              `json:"baseUrl"`
	APIPath       string              `json:"apiPath"`
	APIKey        SecretInput         `json:"apiKey"`
	Categories    *indexer.Categories `json:"categories"`
	Priority      *int                `json:"priority"`
	Enabled       *bool               `json:"enabled"`
	QueryLimitDay OptionalInt         `json:"queryLimitDay"`
	GrabLimitDay  OptionalInt         `json:"grabLimitDay"`
}

// SourceHealth is how a source is doing, as the console shows it.
type SourceHealth struct {
	// State: ok | backoff | limit | failing | disabled | key
	State        string     `json:"state"`
	Detail       string     `json:"detail,omitempty"`
	Failures     int        `json:"failures"`
	BackoffUntil *time.Time `json:"backoffUntil"`
	LastError    string     `json:"lastError"`
}

// SourceUsage is what a source spent today (UTC).
type SourceUsage struct {
	Queries int `json:"queries"`
	Grabs   int `json:"grabs"`
}

// SourceView is a search source as the API returns it. The key is write-only.
type SourceView struct {
	ID            int64              `json:"id"`
	Name          string             `json:"name"`
	Protocol      string             `json:"protocol"`
	BaseURL       string             `json:"baseUrl"`
	APIPath       string             `json:"apiPath"`
	APIKey        SecretView         `json:"apiKey"`
	Categories    indexer.Categories `json:"categories"`
	Priority      int                `json:"priority"`
	Enabled       bool               `json:"enabled"`
	QueryLimitDay *int               `json:"queryLimitDay"`
	GrabLimitDay  *int               `json:"grabLimitDay"`
	Usage         SourceUsage        `json:"usage"`
	Caps          *indexer.Caps      `json:"caps"`
	CapsAt        *time.Time         `json:"capsAt"`
	Health        SourceHealth       `json:"health"`
	Revision      int64              `json:"revision"`
	CreatedAt     time.Time          `json:"createdAt"`
	UpdatedAt     time.Time          `json:"updatedAt"`
}

// SourceList is GET /api/indexers.
type SourceList struct {
	KeyConfigured bool         `json:"keyConfigured"`
	Sources       []SourceView `json:"sources"`
}

// SourceWriteResult is a written source plus whether its caps could be read.
type SourceWriteResult struct {
	SourceView
	CapsError string `json:"capsError,omitempty"`
}

// SourceTestResult is what asking a source achieved. OK means a search
// succeeded — caps alone prove little, since many sources serve them without a
// key.
type SourceTestResult struct {
	OK        bool          `json:"ok"`
	Error     string        `json:"error,omitempty"`
	Items     int           `json:"items"`
	Caps      *indexer.Caps `json:"caps,omitempty"`
	CapsError string        `json:"capsError,omitempty"`
}

func (s *Service) sourceView(ix store.Indexer) SourceView {
	v := SourceView{
		ID: ix.ID, Name: ix.Name, Protocol: ix.Protocol, BaseURL: ix.BaseURL, APIPath: ix.APIPath,
		APIKey:     SecretView{Set: len(ix.APIKeyCT) > 0, UpdatedAt: ix.APIKeyUpdatedAt},
		Categories: indexer.Categories{Movie: ix.Categories.Movie, TV: ix.Categories.TV},
		Priority:   ix.Priority, Enabled: ix.Enabled,
		QueryLimitDay: ix.QueryLimitDay, GrabLimitDay: ix.GrabLimitDay,
		Usage:  SourceUsage{Queries: ix.QueriesToday, Grabs: ix.GrabsToday},
		CapsAt: ix.CapsAt, Revision: ix.Revision, CreatedAt: ix.CreatedAt, UpdatedAt: ix.UpdatedAt,
		Health: SourceHealth{State: "ok", Failures: ix.Failures, BackoffUntil: ix.BackoffUntil, LastError: ix.LastError},
	}
	if len(ix.Caps) > 0 {
		var caps indexer.Caps
		if json.Unmarshal(ix.Caps, &caps) == nil {
			v.Caps = &caps
		}
	}
	now := time.Now()
	switch {
	case !ix.Enabled:
		v.Health.State = "disabled"
	case len(ix.APIKeyCT) > 0 && !s.keyOpens(ix):
		v.Health.State, v.Health.Detail = "key", "the stored API key cannot be opened with the configured ACQUIRE_CONFIG_KEY"
	case ix.BackoffUntil != nil && ix.BackoffUntil.After(now):
		v.Health.State, v.Health.Detail = "backoff", sourceUnusable(ix, now)
	case ix.QueryLimitDay != nil && ix.QueriesToday >= *ix.QueryLimitDay:
		v.Health.State, v.Health.Detail = "limit", "daily query limit reached"
	case ix.Failures > 0:
		v.Health.State = "failing"
	}
	return v
}

func (s *Service) keyOpens(ix store.Indexer) bool {
	_, err := s.openSource(ix)
	return err == nil
}

// ── reads ──────────────────────────────────────────────────────────────────

// ListSources returns every search source with its health and today's usage.
func (s *Service) ListSources(ctx context.Context) (SourceList, error) {
	rows, err := s.st.ListIndexers(ctx)
	if err != nil {
		return SourceList{}, err
	}
	out := SourceList{KeyConfigured: s.box.Enabled(), Sources: make([]SourceView, 0, len(rows))}
	for _, ix := range rows {
		out.Sources = append(out.Sources, s.sourceView(ix))
	}
	return out, nil
}

// ── writes ─────────────────────────────────────────────────────────────────

// CreateSource validates, seals the key and stores a new source, then reads
// its caps.
func (s *Service) CreateSource(ctx context.Context, in SourceInput, actor string) (SourceWriteResult, error) {
	existing, err := s.st.ListIndexers(ctx)
	if err != nil {
		return SourceWriteResult{}, err
	}
	ix, fe := s.validateSource(ctx, in, existing, nil)
	if len(fe) > 0 {
		return SourceWriteResult{}, &ValidationError{Fields: fe}
	}
	if in.APIKey.replaces() && !s.box.Enabled() {
		// Refuse before an id is spent on a row that could not be written.
		return SourceWriteResult{}, ErrNoKey
	}
	id, err := s.st.NextIndexerID(ctx)
	if err != nil {
		return SourceWriteResult{}, err
	}
	ix.ID = id
	key, err := s.sealSourceKey(id, in.APIKey)
	if err != nil {
		return SourceWriteResult{}, err
	}
	stored, err := s.st.CreateIndexer(ctx, ix, key, actor)
	if err != nil {
		return SourceWriteResult{}, err
	}
	stored, capsErr := s.refreshCaps(ctx, stored, false)
	return SourceWriteResult{SourceView: s.sourceView(stored), CapsError: capsErr}, nil
}

// UpdateSource replaces a source's editable fields.
func (s *Service) UpdateSource(ctx context.Context, id int64, in SourceInput, ifRevision int64, actor string) (SourceWriteResult, error) {
	current, err := s.st.GetIndexer(ctx, id)
	if err != nil {
		return SourceWriteResult{}, err
	}
	existing, err := s.st.ListIndexers(ctx)
	if err != nil {
		return SourceWriteResult{}, err
	}
	ix, fe := s.validateSource(ctx, in, existing, &current)
	if len(fe) > 0 {
		return SourceWriteResult{}, &ValidationError{Fields: fe}
	}
	ix.ID = id
	key, err := s.sealSourceKey(id, in.APIKey)
	if err != nil {
		return SourceWriteResult{}, err
	}
	// A source reached differently, or turned back on, starts with a clean
	// slate: its old failures describe a configuration that no longer exists.
	reached := ix.BaseURL != current.BaseURL || ix.APIPath != current.APIPath ||
		ix.Protocol != current.Protocol || in.APIKey.Present
	reenabled := ix.Enabled && !current.Enabled
	stored, err := s.st.UpdateIndexer(ctx, ix, key, ifRevision, reached || reenabled, actor)
	if err != nil {
		return SourceWriteResult{}, err
	}
	var capsErr string
	if reached || reenabled || stored.CapsAt == nil {
		stored, capsErr = s.refreshCaps(ctx, stored, false)
	}
	return SourceWriteResult{SourceView: s.sourceView(stored), CapsError: capsErr}, nil
}

// DeleteSource removes a source and its usage counters.
func (s *Service) DeleteSource(ctx context.Context, id int64, ifRevision int64, actor string) error {
	return s.st.DeleteIndexer(ctx, id, ifRevision, actor)
}

func (s *Service) sealSourceKey(id int64, in SecretInput) (store.SecretWrite, error) {
	switch {
	case !in.Present:
		return store.SecretWrite{Op: store.SecretKeep}, nil
	case in.Clear:
		return store.SecretWrite{Op: store.SecretClear}, nil
	}
	table, rowID, field := store.IndexerKeyAAD(id)
	ct, kid, err := s.box.Seal(table, rowID, field, []byte(in.Value))
	if err != nil {
		return store.SecretWrite{}, err
	}
	return store.SecretWrite{Op: store.SecretSet, CT: ct, KID: kid}, nil
}

// validateSource checks a write and returns the row to store. existing is the
// full list (for name collisions); current is the stored row on update.
func (s *Service) validateSource(ctx context.Context, in SourceInput, existing []store.Indexer, current *store.Indexer) (store.Indexer, []FieldError) {
	var fe []FieldError
	idStr := ""
	if current != nil {
		idStr = strconv.FormatInt(current.ID, 10)
	}
	add := func(field, msg string) {
		fe = append(fe, FieldError{Entity: "source", ID: idStr, Field: field, Message: msg})
	}

	name := strings.TrimSpace(in.Name)
	switch {
	case name == "":
		add("name", "required")
	case len(name) > 64 || strings.ContainsFunc(name, unicode.IsControl):
		add("name", "at most 64 characters, no control characters")
	default:
		for _, e := range existing {
			if (current == nil || e.ID != current.ID) && strings.EqualFold(e.Name, name) {
				add("name", "a search source with this name already exists")
				break
			}
		}
	}

	protocol := strings.TrimSpace(in.Protocol)
	if protocol == "" && current != nil {
		protocol = current.Protocol
	}
	if protocol != "usenet" && protocol != "torrent" {
		add("protocol", `must be "usenet" (newznab) or "torrent" (torznab)`)
	}

	baseURL := strings.TrimSpace(in.BaseURL)
	if err := s.policy.CheckResolved(ctx, baseURL); err != nil {
		add("baseUrl", err.Error())
	} else if u, err := url.Parse(baseURL); err == nil && (u.RawQuery != "" || u.Fragment != "") {
		add("baseUrl", "the address takes no query — put the API key in its own field")
	}

	apiPath := strings.TrimSpace(in.APIPath)
	if apiPath == "" {
		apiPath = "/api"
	}
	if !strings.HasPrefix(apiPath, "/") || len(apiPath) > 200 || strings.ContainsAny(apiPath, "?#\\ \t") ||
		strings.Contains(apiPath, "..") || strings.Contains(apiPath, "//") || strings.ContainsFunc(apiPath, unicode.IsControl) {
		add("apiPath", "a path starting with /, e.g. /api")
	}

	switch {
	case in.APIKey.replaces() && (len(in.APIKey.Value) > 512 || strings.ContainsFunc(in.APIKey.Value, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	})):
		add("apiKey", "at most 512 characters, no spaces")
	case !in.APIKey.Present && current != nil && len(current.APIKeyCT) > 0 && !sameSourceEndpoint(baseURL, apiPath, *current):
		// The key goes into every request's query string: kept across an address
		// change it would go to the new address, whoever answers there.
		add("apiKey", "enter the API key again, or clear it: the stored one is only sent to the address it was saved for")
	}

	cats := defaultCategories
	switch {
	case in.Categories != nil:
		cats = indexer.Categories{Movie: cleanCategories(in.Categories.Movie), TV: cleanCategories(in.Categories.TV)}
		if cats.Movie == nil || cats.TV == nil {
			add("categories", "category ids are whole numbers between 1 and 999999, at most 100 per kind")
		}
	case current != nil:
		cats = indexer.Categories{Movie: current.Categories.Movie, TV: current.Categories.TV}
	}

	priority := 0
	if in.Priority != nil {
		priority = *in.Priority
	} else if current != nil {
		priority = current.Priority
	}
	if priority < -1000 || priority > 1000 {
		add("priority", "must be between -1000 and 1000")
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	} else if current != nil {
		enabled = current.Enabled
	}

	limit := func(field string, o OptionalInt, stored *int) *int {
		v := o.Value
		if !o.Set && current != nil {
			v = stored
		}
		if v != nil && (*v < 1 || *v > 1_000_000) {
			add(field, "leave empty for no limit, or between 1 and 1000000")
		}
		return v
	}
	var storedQ, storedG *int
	if current != nil {
		storedQ, storedG = current.QueryLimitDay, current.GrabLimitDay
	}
	queryLimit := limit("queryLimitDay", in.QueryLimitDay, storedQ)
	grabLimit := limit("grabLimitDay", in.GrabLimitDay, storedG)

	return store.Indexer{
		Name: name, Protocol: protocol, BaseURL: baseURL, APIPath: apiPath,
		Categories: store.IndexerCategories{Movie: cats.Movie, TV: cats.TV},
		Priority:   priority, Enabled: enabled, QueryLimitDay: queryLimit, GrabLimitDay: grabLimit,
	}, fe
}

// sameSourceEndpoint reports whether a source is still asked where its stored
// key was saved for: the same base address and the same API path.
func sameSourceEndpoint(baseURL, apiPath string, stored store.Indexer) bool {
	return sameEndpoint(baseURL, stored.BaseURL) && apiPath == stored.APIPath
}

// cleanCategories dedupes a category list; nil when it is invalid.
func cleanCategories(in []int) []int {
	if len(in) > 100 {
		return nil
	}
	seen := map[int]bool{}
	out := []int{}
	for _, id := range in {
		if id < 1 || id > 999999 {
			return nil
		}
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// ── caps and tests ─────────────────────────────────────────────────────────

// refreshCaps asks a stored source for its caps and caches them, recording
// the request as usage and its outcome as health. With enforceLimit the
// source's daily allowance is respected (the daily refresh); an admin's save
// or test is always sent. It returns the row as it now stands and, when the
// caps could not be read, why.
func (s *Service) refreshCaps(ctx context.Context, ix store.Indexer, enforceLimit bool) (store.Indexer, string) {
	src, err := s.openSource(ix)
	if err != nil {
		return ix, "the stored API key cannot be opened: " + err.Error()
	}
	done, err := s.slot(ctx)
	if err != nil {
		return ix, err.Error()
	}
	defer done()
	var limit *int
	if enforceLimit {
		limit = ix.QueryLimitDay
	}
	if ok, err := s.st.ReserveIndexerQuery(ctx, ix.ID, limit); err != nil || !ok {
		return ix, "daily query limit reached"
	}
	cctx, cancel := context.WithTimeout(ctx, s.timing.manualPerSource)
	caps, err := s.ixc.FetchCaps(cctx, src)
	cancel()
	s.recordOutcome(ctx, src, err)
	var capsErr string
	if err != nil {
		capsErr = err.Error()
	} else if body, merr := json.Marshal(caps); merr == nil {
		if serr := s.st.SetIndexerCaps(ctx, ix.ID, body); serr != nil {
			capsErr = "caps read but not stored: " + serr.Error()
		}
	}
	if fresh, err := s.st.GetIndexer(ctx, ix.ID); err == nil {
		ix = fresh
	}
	return ix, capsErr
}

// probe asks a source for its caps and runs one small search, which is what
// proves the key. It reports how many requests it sent.
func (s *Service) probe(ctx context.Context, src indexer.Source) (res SourceTestResult, searchErr error, sent int) {
	done, err := s.slot(ctx)
	if err != nil {
		return SourceTestResult{Error: err.Error()}, nil, 0
	}
	defer done()

	cctx, cancel := context.WithTimeout(ctx, s.timing.manualPerSource)
	caps, capsErr := s.ixc.FetchCaps(cctx, src)
	cancel()
	sent++
	if capsErr != nil {
		res.CapsError = capsErr.Error()
		if indexer.KindOf(capsErr) == indexer.KindCredentials {
			res.Error = res.CapsError
			return res, capsErr, sent
		}
	} else {
		res.Caps, src.Caps = caps, caps
	}

	// The latest releases: no query term. A source that insists on one gets a
	// neutral term instead.
	cctx, cancel = context.WithTimeout(ctx, s.timing.manualPerSource)
	items, err := s.ixc.Search(cctx, src, indexer.Query{Kind: "search", Limit: 10})
	cancel()
	sent++
	if err != nil && indexer.KindOf(err) == indexer.KindOther {
		cctx, cancel = context.WithTimeout(ctx, s.timing.manualPerSource)
		items, err = s.ixc.Search(cctx, src, indexer.Query{Kind: "search", Term: "test", Limit: 10})
		cancel()
		sent++
	}
	if err != nil {
		res.Error = err.Error()
		return res, err, sent
	}
	res.OK, res.Items = true, len(items)
	return res, nil, sent
}

// TestSource asks a source that has not been saved. When the body names a
// stored source at its stored address and carries no key, the stored key is
// used, so an admin can test an edit without retyping it. At any other address
// the key has to be typed. Nothing is recorded.
func (s *Service) TestSource(ctx context.Context, in SourceInput) (SourceTestResult, error) {
	var current *store.Indexer
	if in.ID > 0 {
		if c, err := s.st.GetIndexer(ctx, in.ID); err == nil {
			current = &c
		}
	}
	if strings.TrimSpace(in.Name) == "" {
		in.Name = "test"
	}
	ix, fe := s.validateSource(ctx, in, nil, current)
	// Only what reaching the source needs matters for a test.
	var blocking []FieldError
	for _, f := range fe {
		switch f.Field {
		case "protocol", "baseUrl", "apiPath", "apiKey", "categories":
			blocking = append(blocking, f)
		}
	}
	if len(blocking) > 0 {
		return SourceTestResult{}, &ValidationError{Fields: blocking}
	}
	src := indexer.Source{
		Name: ix.Name, Protocol: ix.Protocol, BaseURL: ix.BaseURL, APIPath: ix.APIPath,
		Categories: indexer.Categories{Movie: ix.Categories.Movie, TV: ix.Categories.TV},
	}
	switch {
	case in.APIKey.replaces():
		src.APIKey = in.APIKey.Value
	case !in.APIKey.Clear && current != nil && len(current.APIKeyCT) > 0 && sameSourceEndpoint(ix.BaseURL, ix.APIPath, *current):
		stored, err := s.openSource(*current)
		if err != nil {
			return SourceTestResult{}, fmt.Errorf("the stored API key cannot be opened: %w", err)
		}
		src.APIKey = stored.APIKey
	}
	res, _, _ := s.probe(ctx, src)
	return res, nil
}

// TestStoredSource asks a saved source exactly as stored. Unlike an unsaved
// test it counts: the requests are usage, the caps are cached, and the outcome
// is the source's health — a rejected key disables it here too.
func (s *Service) TestStoredSource(ctx context.Context, id int64) (SourceTestResult, error) {
	ix, err := s.st.GetIndexer(ctx, id)
	if err != nil {
		return SourceTestResult{}, err
	}
	src, err := s.openSource(ix)
	if err != nil {
		return SourceTestResult{Error: "the stored API key cannot be opened with the configured ACQUIRE_CONFIG_KEY"}, nil
	}
	res, searchErr, sent := s.probe(ctx, src)
	for i := 0; i < sent; i++ {
		_, _ = s.st.ReserveIndexerQuery(ctx, id, nil)
	}
	if res.Caps != nil {
		if body, err := json.Marshal(res.Caps); err == nil {
			_ = s.st.SetIndexerCaps(ctx, id, body)
		}
	}
	if sent > 0 {
		s.recordOutcome(ctx, src, searchErr)
	}
	return res, nil
}

// RunSourceMaintenance refreshes cached caps once a day per enabled source
// until ctx ends. It checks hourly and at start, so a restart does not wait a
// day, and it respects each source's backoff and allowance.
func (s *Service) RunSourceMaintenance(ctx context.Context) {
	if s.st == nil {
		return
	}
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		s.refreshStaleCaps(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (s *Service) refreshStaleCaps(ctx context.Context) {
	rows, err := s.st.ListIndexers(ctx)
	if err != nil {
		return
	}
	now := time.Now()
	for _, ix := range rows {
		if !ix.Enabled || sourceUnusable(ix, now) != "" || (ix.CapsAt != nil && now.Sub(*ix.CapsAt) < capsMaxAge) {
			continue
		}
		if _, capsErr := s.refreshCaps(ctx, ix, true); capsErr != "" {
			log.Printf("acquire: caps for search source %s: %s", ix.Name, capsErr)
		}
		if ctx.Err() != nil {
			return
		}
	}
}

// ── summary ────────────────────────────────────────────────────────────────

// SourceSummary is what acquire can search, per protocol: the one seam between
// the source registry and everything that asks "is there anything to search?"
// — setup, health, auto-grab and routing coverage.
type SourceSummary struct {
	// Configured: at least one source exists.
	Configured bool
	// Enabled counts enabled sources per protocol (usenet, torrent).
	Enabled map[string]int
	// Usable counts the enabled sources that can be asked right now: key
	// readable, not backing off, allowance left.
	Usable map[string]int
	// Locked names enabled sources whose key cannot be opened; Waiting names
	// those backing off or out of allowance, with why; Disabled names disabled
	// sources that carry an error.
	Locked   []string
	Waiting  []string
	Disabled []string
	// Err is a failure to find out, as opposed to an answer of zero.
	Err error
}

// Total is the number of enabled sources across protocols.
func (s SourceSummary) Total() int { return total(s.Enabled) }

// TotalUsable is the number of sources that can be asked now.
func (s SourceSummary) TotalUsable() int { return total(s.Usable) }

func total(m map[string]int) int {
	n := 0
	for _, v := range m {
		n += v
	}
	return n
}

func (s *Service) sourceSummary(ctx context.Context) SourceSummary {
	sum := SourceSummary{Enabled: map[string]int{}, Usable: map[string]int{}}
	if s.st == nil {
		sum.Err = errors.New("no store configured")
		return sum
	}
	rows, err := s.st.ListIndexers(ctx)
	if err != nil {
		sum.Err = err
		return sum
	}
	sum.Configured = len(rows) > 0
	now := time.Now()
	for _, ix := range rows {
		if !ix.Enabled {
			if ix.LastError != "" {
				sum.Disabled = append(sum.Disabled, ix.Name+": "+ix.LastError)
			}
			continue
		}
		sum.Enabled[ix.Protocol]++
		if len(ix.APIKeyCT) > 0 && !s.keyOpens(ix) {
			sum.Locked = append(sum.Locked, ix.Name)
			continue
		}
		if reason := sourceUnusable(ix, now); reason != "" {
			sum.Waiting = append(sum.Waiting, ix.Name+" ("+reason+")")
			continue
		}
		sum.Usable[ix.Protocol]++
	}
	return sum
}
