package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/laedeli/acquire/internal/store"
)

// searchSettingsKey is the acquire_settings row holding the search and grab
// policy.
const searchSettingsKey = "search"

// SearchSettings is the search and grab policy an admin edits in the console.
// It used to be environment only (ACQUIRE_PREFER, ACQUIRE_STORAGE_FLOOR_GB,
// ACQUIRE_MAX_CONCURRENT_GRABS); those values now only seed the first row.
type SearchSettings struct {
	// PreferProtocol decides which kind of source is searched first and, for a
	// link whose kind is unknown, which client is tried first.
	PreferProtocol string `json:"preferProtocol"` // usenet | torrent
	// StorageFloorGB: below this much free space grabs are refused.
	StorageFloorGB int64 `json:"storageFloorGb"`
	// MaxConcurrentGrabs bounds downloads in flight.
	MaxConcurrentGrabs int `json:"maxConcurrentGrabs"`
	// AutoGrab lets the backlog sweep — and the sources' newest releases —
	// grab the best acceptable release of whatever is missing, within the
	// floor and the cap above. Off, the sweep only searches. Never for a title
	// whose holdings are unknown.
	AutoGrab bool `json:"autoGrab"`
	// AutoGrabNew lets them grab the new things only, with AutoGrab off: an
	// episode aired in the last NewWithinDays days, an episode a follow or a
	// read of TMDB added after its series was requested, and a requested
	// movie once it is out (isNew). The old backlog waits for AutoGrab.
	AutoGrabNew bool `json:"autoGrabNew"`
	// NewWithinDays is how recently an episode aired to be one of the new
	// things.
	NewWithinDays int `json:"newWithinDays"`
	// AirDelayHours is how long an automatic grab waits after an episode's
	// air date — 00:00 UTC on the day TMDB dates it — and after the day a
	// requested movie comes out: TMDB dates the day, not the hour, and an
	// evening broadcast in the Americas is a day later in UTC. Releases
	// named for an episode before it was broadcast are most often fakes.
	// 0 grabs from the air date on.
	AirDelayHours int `json:"airDelayHours"`
}

// DefaultNewWithinDays is how recently an episode aired, by default, to be
// one of the new things search.autoGrabNew grabs.
const DefaultNewWithinDays = 14

// DefaultAirDelayHours is how long an automatic grab waits after an air date
// by default, and MaxAirDelayHours the longest it may be set to wait.
const (
	DefaultAirDelayHours = 30
	MaxAirDelayHours     = 168
)

// airDelay is how long after an air date an automatic grab may take a
// target.
func (p SearchSettings) airDelay() time.Duration { return time.Duration(p.AirDelayHours) * time.Hour }

// dueOrder is how the sweep picks what it searches under the policy: the new
// things first when they are grabbed on their own, and nothing before its air
// delay is over.
func (p SearchSettings) dueOrder() store.DueOrder {
	o := store.DueOrder{AirDelay: p.airDelay()}
	if p.AutoGrabNew {
		o.NewWithinDays = p.newWithin()
	}
	return o
}

// Grabs reports whether the policy lets an automatic grab — the sweep's, a
// newest release's — take a target: any with AutoGrab, a new one with
// AutoGrabNew; nothing of a title held for an admin's approval. Whether the
// target can be grabbed at all is the caller's.
func (p SearchSettings) Grabs(title store.Title, t store.Target, now time.Time) bool {
	if title.HeldForApproval() {
		return false
	}
	return p.AutoGrab || (p.AutoGrabNew && isNew(title, t, p.newWithin(), now))
}

// GrabsAnything reports whether the policy lets automatic grabs take
// anything at all.
func (p SearchSettings) GrabsAnything() bool { return p.AutoGrab || p.AutoGrabNew }

func (p SearchSettings) newWithin() int {
	if p.NewWithinDays <= 0 {
		return DefaultNewWithinDays
	}
	return p.NewWithinDays
}

// isNew reports whether a target is one of the new things: an episode aired
// in the last days days (by its air date, as TMDB dates it), and what an
// approved request asked for — an episode a follow or a read of TMDB added
// after its series was first requested, or a requested movie —; never one
// the library held once: a deletion, or a reconcile that missed it, does not
// have it grabbed again on its own. Everything else is the old backlog.
func isNew(title store.Title, t store.Target, days int, now time.Time) bool {
	if t.EverHeld {
		return false
	}
	requested := title.Followed() && title.Approved
	if t.Kind == "movie" {
		return requested
	}
	today := now.UTC().Truncate(24 * time.Hour)
	if t.AirDate != nil && !t.AirDate.Before(today.AddDate(0, 0, -days)) {
		return true
	}
	return requested && t.AddedAt != nil && !t.AddedAt.Before(*title.FollowedAt)
}

// SearchSettingsDoc is the stored policy with the revision an editor sends
// back in If-Match.
type SearchSettingsDoc struct {
	SearchSettings
	Revision  int64      `json:"revision"`
	UpdatedAt *time.Time `json:"updatedAt,omitempty"`
}

// envSearchSettings is today's environment-derived policy: the seed, and the
// fallback whenever the stored row cannot be read.
func (s *Service) envSearchSettings() SearchSettings {
	prefer := "torrent"
	if s.cfg.PreferUsenet {
		prefer = "usenet"
	}
	return SearchSettings{
		PreferProtocol:     prefer,
		StorageFloorGB:     s.cfg.StorageFloorBytes() >> 30,
		MaxConcurrentGrabs: s.cfg.MaxConcurrentGrabs(),
		NewWithinDays:      DefaultNewWithinDays,
		AirDelayHours:      DefaultAirDelayHours,
	}
}

// SeedSettings writes the environment's policy once. It never overwrites a row
// an admin has edited, so it is safe on every boot.
func (s *Service) SeedSettings(ctx context.Context) error {
	return s.st.SeedSetting(ctx, searchSettingsKey, s.envSearchSettings())
}

// GetSearchSettings returns the stored policy. A missing row reads as the
// environment defaults at revision 0.
func (s *Service) GetSearchSettings(ctx context.Context) (SearchSettingsDoc, error) {
	doc := SearchSettingsDoc{SearchSettings: s.envSearchSettings()}
	row, err := s.st.GetSetting(ctx, searchSettingsKey)
	if errors.Is(err, store.ErrNotFound) {
		return doc, nil
	}
	if err != nil {
		return doc, err
	}
	// Decode over the defaults, so a field added later reads as its default in
	// a row written before it existed.
	if err := json.Unmarshal(row.Value, &doc.SearchSettings); err != nil {
		return SearchSettingsDoc{SearchSettings: s.envSearchSettings()}, err
	}
	doc.Revision, doc.UpdatedAt = row.Revision, &row.UpdatedAt
	return doc, nil
}

// grabPolicy is what grab decisions use. It never fails: an unreadable row
// falls back to the environment, and admission itself still fails closed.
func (s *Service) grabPolicy(ctx context.Context) SearchSettings {
	if s.st == nil {
		return s.envSearchSettings()
	}
	doc, err := s.GetSearchSettings(ctx)
	if err != nil {
		return s.envSearchSettings()
	}
	return doc.SearchSettings
}

// GrabPolicy is the search and grab policy in force: what the console says
// is grabbed on its own.
func (s *Service) GrabPolicy(ctx context.Context) SearchSettings { return s.grabPolicy(ctx) }

// SaveSearchSettings validates and stores the policy. A document that names
// no newWithinDays — one written by a console from before it — takes the
// default; the API reads a document over the policy stored, so a field it
// does not name keeps its value (airDelayHours, whose 0 means no wait).
func (s *Service) SaveSearchSettings(ctx context.Context, in SearchSettings, ifRevision int64, actor string) (SearchSettingsDoc, error) {
	if in.NewWithinDays == 0 {
		in.NewWithinDays = DefaultNewWithinDays
	}
	if fe := validateSearchSettings(in); len(fe) > 0 {
		return SearchSettingsDoc{}, &ValidationError{Fields: fe}
	}
	row, err := s.st.PutSetting(ctx, searchSettingsKey, in, ifRevision, actor)
	if err != nil {
		return SearchSettingsDoc{}, err
	}
	return SearchSettingsDoc{SearchSettings: in, Revision: row.Revision, UpdatedAt: &row.UpdatedAt}, nil
}

func validateSearchSettings(in SearchSettings) []FieldError {
	var fe []FieldError
	add := func(field, msg string) {
		fe = append(fe, FieldError{Entity: "settings", ID: searchSettingsKey, Field: field, Message: msg})
	}
	if in.PreferProtocol != "usenet" && in.PreferProtocol != "torrent" {
		add("preferProtocol", `must be "usenet" or "torrent"`)
	}
	if in.StorageFloorGB < 0 || in.StorageFloorGB > 1_000_000 {
		add("storageFloorGb", "must be between 0 and 1000000")
	}
	if in.MaxConcurrentGrabs < 1 || in.MaxConcurrentGrabs > 100 {
		add("maxConcurrentGrabs", "must be between 1 and 100")
	}
	// 0 is a document that names none: the default.
	if in.NewWithinDays < 0 || in.NewWithinDays > 365 {
		add("newWithinDays", "must be between 1 and 365")
	}
	if in.AirDelayHours < 0 || in.AirDelayHours > MaxAirDelayHours {
		add("airDelayHours", fmt.Sprintf("must be between 0 and %d", MaxAirDelayHours))
	}
	return fe
}

// requestSettingsKey is the acquire_settings row holding how requests are
// taken.
const requestSettingsKey = "requests"

// RequestSettings say how requests are taken: AutoApprove approves a request
// at once whoever makes it; off — the default — a request from someone who
// is not an admin awaits an admin's approval, its title followed but nothing
// of it grabbed on its own until then.
type RequestSettings struct {
	AutoApprove bool  `json:"autoApprove"`
	Revision    int64 `json:"revision"`
}

// RequestSettings reads the stored settings; nothing approved on its own when
// none are stored.
func (s *Service) RequestSettings(ctx context.Context) RequestSettings {
	var out RequestSettings
	if s.st == nil {
		return out
	}
	row, err := s.st.GetSetting(ctx, requestSettingsKey)
	if err != nil {
		return out
	}
	_ = json.Unmarshal(row.Value, &out)
	out.Revision = row.Revision
	return out
}

// PutRequestSettings replaces them (ifRevision > 0: only over that revision,
// store.ErrStale otherwise). A request awaiting approval stays so: turning
// autoApprove on approves the requests made from then on.
func (s *Service) PutRequestSettings(ctx context.Context, in RequestSettings, ifRevision int64, actor string) (RequestSettings, error) {
	row, err := s.st.PutSetting(ctx, requestSettingsKey, map[string]bool{"autoApprove": in.AutoApprove}, ifRevision, actor)
	if err != nil {
		return RequestSettings{}, err
	}
	out := RequestSettings{Revision: row.Revision}
	_ = json.Unmarshal(row.Value, &out)
	return out, nil
}

// FieldError is one invalid field in a configuration write.
type FieldError struct {
	Entity  string `json:"entity"`
	ID      string `json:"id"`
	Field   string `json:"field"`
	Message string `json:"message"`
}

// ValidationError is a write refused because of its content (HTTP 422).
type ValidationError struct {
	Fields []FieldError
}

func (e *ValidationError) Error() string {
	if len(e.Fields) == 0 {
		return "invalid"
	}
	return fmt.Sprintf("invalid %s: %s", e.Fields[0].Field, e.Fields[0].Message)
}
