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
	// 0 grabs from the air date on. It holds for an episode whose air time is
	// not known (AirTimes).
	AirDelayHours int `json:"airDelayHours"`
	// AirTimes is where the moment an episode airs comes from: "tvmaze" —
	// the default — reads it from TVmaze's public schedule, asking by the
	// series' TVDB or IMDb id and nothing else; "off" tells an episode by its
	// air date alone.
	AirTimes string `json:"airTimes"`
	// AirDelayAfterAirstampMinutes is how long an automatic grab waits after
	// an episode's broadcast ends, or after it streams, when its air time is
	// known; 0 grabs from then on.
	AirDelayAfterAirstampMinutes int `json:"airDelayAfterAirstampMinutes"`

	// noSchedule is set on the policy in force when acquire has no schedule
	// source to ask: air times are off whatever AirTimes says.
	noSchedule bool
}

// Where exact air times come from (SearchSettings.AirTimes).
const (
	AirTimesTVmaze = "tvmaze"
	AirTimesOff    = "off"
)

// DefaultAirDelayAfterAirstampMinutes is how long an automatic grab waits
// after the moment an episode airs, by default, and
// MaxAirDelayAfterAirstampMinutes the longest it may be set to wait.
const (
	DefaultAirDelayAfterAirstampMinutes = 60
	MaxAirDelayAfterAirstampMinutes     = 1440
)

// airSlack is how long before the moment a streamed episode streams a
// release of it may be published: a schedule a little off, a service a
// little early.
const airSlack = 30 * time.Minute

// exactAirTimes reports whether the schedule source's air times are read and
// used.
func (p SearchSettings) exactAirTimes() bool { return p.AirTimes != AirTimesOff && !p.noSchedule }

// stampDelay is how long after an episode's broadcast ends, or after it
// streams, an automatic grab may take it.
func (p SearchSettings) stampDelay() time.Duration {
	return time.Duration(p.AirDelayAfterAirstampMinutes) * time.Minute
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
	o := store.DueOrder{AirDelay: p.airDelay(), StampDelay: p.stampDelay(), Stamps: p.exactAirTimes()}
	if p.AutoGrabNew {
		o.NewWithinDays = p.newWithin()
	}
	return o
}

// Grabs reports whether the policy lets an automatic grab — the sweep's, a
// newest release's — take a target: any with AutoGrab, a new one with
// AutoGrabNew; nothing not monitored, with its title; nothing of a title held
// for an admin's approval, nothing of a
// series the import says is anime or daily (handsOff), and no requested
// movie whose window is a guess (guessedWindow). Whether the target can be
// grabbed at all is the caller's.
func (p SearchSettings) Grabs(title store.Title, t store.Target, now time.Time) bool {
	if !title.Monitored || !t.Monitored || title.HeldForApproval() || handsOff(title) || guessedWindow(t) {
		return false
	}
	return p.AutoGrab || (p.AutoGrabNew && isNew(title, t, p.newWithin(), now))
}

// handsOff reports whether no automatic grab takes anything of a series
// (store.Title.HandsOff): its releases name no season and episode acquire
// can tell — an anime numbered by an absolute episode number, a daily show
// named by its date —, so a release found for one cannot be told to be it.
// They are searched, and grabbed from the missing tab.
func handsOff(title store.Title) bool { return title.HandsOff() }

// grabbedWhen says when an automatic grab takes an episode, as the policy
// has it: "an hour after its broadcast ends or it streams (TVmaze's air
// times), else 30 hours after its air date".
func (p SearchSettings) grabbedWhen() string {
	date := "from its air date on"
	if p.AirDelayHours > 0 {
		date = fmt.Sprintf("%d hours after its air date", p.AirDelayHours)
	}
	if !p.exactAirTimes() {
		return date
	}
	stamp := "once its broadcast ends or it streams"
	switch m := p.AirDelayAfterAirstampMinutes; {
	case m == 60:
		stamp = "an hour after its broadcast ends or it streams"
	case m > 0 && m%60 == 0:
		stamp = fmt.Sprintf("%d hours after its broadcast ends or it streams", m/60)
	case m > 0:
		stamp = fmt.Sprintf("%d minutes after its broadcast ends or it streams", m)
	}
	return stamp + " (TVmaze's air times), else " + date
}

// guessedWindow reports whether a requested movie is searched from a guess:
// TMDB dates it in cinemas only, and its window is 90 days after it came
// there. No automatic grab takes it — a release before one to watch at home
// is most often a recording made in a cinema —; it is searched, and an admin
// grabs it. Once TMDB dates a release to watch at home, it is grabbed on its
// own, and a release published before that day is skipped (preAired).
func guessedWindow(t store.Target) bool { return t.Kind == "movie" && t.ReleaseKind == "theatrical" }

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
		AirTimes:           AirTimesTVmaze,

		AirDelayAfterAirstampMinutes: DefaultAirDelayAfterAirstampMinutes,
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
	pol := s.envSearchSettings()
	if s.st != nil {
		if doc, err := s.GetSearchSettings(ctx); err == nil {
			pol = doc.SearchSettings
		}
	}
	pol.noSchedule = !s.sched.Enabled()
	return pol
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
	if in.AirTimes == "" {
		in.AirTimes = AirTimesTVmaze
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

// AirTimesStatus is how far the schedule source's air times are of the
// series that run whose air times matter (store.AirTimes): On is whether they
// are used — search.airTimes, and a source to ask.
type AirTimesStatus struct {
	On bool `json:"on"`
	store.AirTimes
}

// AirTimesStatus reads it.
func (s *Service) AirTimesStatus(ctx context.Context) (AirTimesStatus, error) {
	out := AirTimesStatus{On: s.grabPolicy(ctx).exactAirTimes()}
	if s.st == nil {
		return out, nil
	}
	a, err := s.st.AirTimesOfRunning(ctx)
	out.AirTimes = a
	return out, err
}

// AirTimesMissing refuses switching grabbing on its own on — search.autoGrab
// or search.autoGrabNew — while series that run have no air times from the
// schedule source yet (AirTimesStatus.Pending), until an admin confirms it:
// nothing of them is grabbed on its own until their air times are read.
type AirTimesMissing struct{ Status AirTimesStatus }

func (e *AirTimesMissing) Error() string {
	why := ""
	if e.Status.LastError != "" {
		why = " (" + e.Status.LastError + ")"
	}
	has, them := "have", "them"
	if e.Status.Pending == 1 {
		has, them = "has", "it"
	}
	return fmt.Sprintf("%d of the %d series that run %s no air times from TVmaze yet%s: nothing of %s is grabbed on its own until "+
		"they are read, on the next refreshes — confirm to switch grabbing on its own on now", e.Status.Pending, e.Status.Running, has, why, them)
}

// PutSearchSettings is SaveSearchSettings as an admin writes the policy:
// switching grabbing on its own on while series that run have no air times
// yet is refused (AirTimesMissing), unless confirmed.
func (s *Service) PutSearchSettings(ctx context.Context, in SearchSettings, ifRevision int64, actor string, confirmed bool) (SearchSettingsDoc, error) {
	if !confirmed {
		cur := s.grabPolicy(ctx)
		on := (in.AutoGrabNew && !cur.AutoGrabNew) || (in.AutoGrab && !cur.AutoGrab)
		next := in
		next.noSchedule = cur.noSchedule
		if on && next.exactAirTimes() {
			if st, err := s.AirTimesStatus(ctx); err == nil && st.Pending > 0 {
				return SearchSettingsDoc{}, &AirTimesMissing{Status: st}
			}
		}
	}
	return s.SaveSearchSettings(ctx, in, ifRevision, actor)
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
	// "" is a document that names none: the default.
	if in.AirTimes != "" && in.AirTimes != AirTimesTVmaze && in.AirTimes != AirTimesOff {
		add("airTimes", fmt.Sprintf("must be %q or %q", AirTimesTVmaze, AirTimesOff))
	}
	if in.AirDelayAfterAirstampMinutes < 0 || in.AirDelayAfterAirstampMinutes > MaxAirDelayAfterAirstampMinutes {
		add("airDelayAfterAirstampMinutes", fmt.Sprintf("must be between 0 and %d", MaxAirDelayAfterAirstampMinutes))
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
