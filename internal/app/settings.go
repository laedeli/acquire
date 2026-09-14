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

// SaveSearchSettings validates and stores the policy.
func (s *Service) SaveSearchSettings(ctx context.Context, in SearchSettings, ifRevision int64, actor string) (SearchSettingsDoc, error) {
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
	return fe
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
