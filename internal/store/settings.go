package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Setting is one stored settings document.
type Setting struct {
	Key       string
	Value     json.RawMessage
	Revision  int64
	UpdatedAt time.Time
}

// GetSetting returns a settings document or ErrNotFound.
func (s *Store) GetSetting(ctx context.Context, key string) (Setting, error) {
	st := Setting{Key: key}
	err := s.pool.QueryRow(ctx,
		`SELECT value, revision, updated_at FROM acquire_settings WHERE key=$1`, key).
		Scan(&st.Value, &st.Revision, &st.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Setting{}, ErrNotFound
	}
	return st, err
}

// SeedSetting writes a document only when none exists. It runs on every boot,
// so an admin's edit is never overwritten by the environment's defaults.
func (s *Store) SeedSetting(ctx context.Context, key string, value any) error {
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx,
		`INSERT INTO acquire_settings (key, value) VALUES ($1, $2) ON CONFLICT (key) DO NOTHING`, key, body)
	return err
}

// PutSetting replaces a settings document. ifRevision > 0 makes it conditional
// (ErrStale on mismatch). The change is audited without its value.
func (s *Store) PutSetting(ctx context.Context, key string, value any, ifRevision int64, actor string) (Setting, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return Setting{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Setting{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var current int64
	err = tx.QueryRow(ctx, `SELECT revision FROM acquire_settings WHERE key=$1 FOR UPDATE`, key).Scan(&current)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		if ifRevision > 0 {
			return Setting{}, ErrStale
		}
	case err != nil:
		return Setting{}, err
	case ifRevision > 0 && current != ifRevision:
		return Setting{}, ErrStale
	}
	st := Setting{Key: key}
	if err := tx.QueryRow(ctx, `
		INSERT INTO acquire_settings (key, value) VALUES ($1, $2)
		ON CONFLICT (key) DO UPDATE SET
		  value = EXCLUDED.value,
		  revision = acquire_settings.revision + 1,
		  updated_at = now()
		RETURNING value, revision, updated_at`, key, body).
		Scan(&st.Value, &st.Revision, &st.UpdatedAt); err != nil {
		return Setting{}, err
	}
	if err := audit(ctx, tx, actor, "settings", key, "update"); err != nil {
		return Setting{}, err
	}
	return st, tx.Commit(ctx)
}

// AuditEntry is one recorded configuration change.
type AuditEntry struct {
	At       time.Time `json:"at"`
	Actor    string    `json:"actor"`
	Entity   string    `json:"entity"`
	EntityID string    `json:"entityId"`
	Action   string    `json:"action"`
}

// RecentAudit returns the newest configuration changes.
func (s *Store) RecentAudit(ctx context.Context, limit int) ([]AuditEntry, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT at, actor, entity, entity_id, action FROM settings_audit ORDER BY at DESC, id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AuditEntry{}
	for rows.Next() {
		var e AuditEntry
		if err := rows.Scan(&e.At, &e.Actor, &e.Entity, &e.EntityID, &e.Action); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
