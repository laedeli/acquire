package store

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Indexer is one configured search source. The API key is ciphertext: the
// store never sees it in the clear and cannot open it.
type Indexer struct {
	ID              int64
	Name            string
	Protocol        string // usenet | torrent
	BaseURL         string
	APIPath         string
	APIKeyCT        []byte
	APIKeyKID       string
	APIKeyUpdatedAt *time.Time
	Categories      IndexerCategories
	Priority        int
	Enabled         bool
	Caps            json.RawMessage // the parsed t=caps answer; nil until fetched
	CapsAt          *time.Time
	QueryLimitDay   *int // nil: no limit
	GrabLimitDay    *int
	Failures        int
	BackoffUntil    *time.Time
	LastError       string
	Revision        int64
	CreatedAt       time.Time
	UpdatedAt       time.Time

	// What the source has spent today (UTC), read with the row.
	QueriesToday int
	GrabsToday   int
}

// IndexerCategories are the newznab category ids a search is scoped to.
type IndexerCategories struct {
	Movie []int `json:"movie"`
	TV    []int `json:"tv"`
}

// IndexerKeyAAD is the secretbox binding for a source's API key.
func IndexerKeyAAD(id int64) (table, rowID, field string) {
	return "indexers", strconv.FormatInt(id, 10), "api_key"
}

// utcDay is the day usage is counted against. Daily allowances at the sources
// reset on a fixed clock, not per acquire time zone.
const utcDay = `(now() AT TIME ZONE 'UTC')::date`

const indexerCols = `i.id, i.name, i.protocol, i.base_url, i.api_path, i.api_key_ct, i.api_key_kid,
	i.api_key_updated_at, i.categories, i.priority, i.enabled, i.caps, i.caps_at, i.query_limit_day,
	i.grab_limit_day, i.failures, i.backoff_until, i.last_error, i.revision, i.created_at, i.updated_at,
	COALESCE(u.queries, 0), COALESCE(u.grabs, 0)`

const indexerFrom = ` FROM indexers i LEFT JOIN indexer_usage u ON u.indexer_id = i.id AND u.day = ` + utcDay

func scanIndexer(r interface{ Scan(...any) error }) (Indexer, error) {
	var ix Indexer
	var cats, caps []byte
	err := r.Scan(&ix.ID, &ix.Name, &ix.Protocol, &ix.BaseURL, &ix.APIPath, &ix.APIKeyCT, &ix.APIKeyKID,
		&ix.APIKeyUpdatedAt, &cats, &ix.Priority, &ix.Enabled, &caps, &ix.CapsAt, &ix.QueryLimitDay,
		&ix.GrabLimitDay, &ix.Failures, &ix.BackoffUntil, &ix.LastError, &ix.Revision, &ix.CreatedAt,
		&ix.UpdatedAt, &ix.QueriesToday, &ix.GrabsToday)
	if err != nil {
		return Indexer{}, err
	}
	if len(cats) > 0 {
		_ = json.Unmarshal(cats, &ix.Categories)
	}
	if ix.Categories.Movie == nil {
		ix.Categories.Movie = []int{}
	}
	if ix.Categories.TV == nil {
		ix.Categories.TV = []int{}
	}
	if len(caps) > 0 {
		ix.Caps = caps
	}
	return ix, nil
}

// ListIndexers returns every source in asking order: lowest priority first,
// then name.
func (s *Store) ListIndexers(ctx context.Context) ([]Indexer, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+indexerCols+indexerFrom+` ORDER BY i.priority, i.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Indexer{}
	for rows.Next() {
		ix, err := scanIndexer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ix)
	}
	return out, rows.Err()
}

// GetIndexer returns one source or ErrNotFound.
func (s *Store) GetIndexer(ctx context.Context, id int64) (Indexer, error) {
	ix, err := scanIndexer(s.pool.QueryRow(ctx, `SELECT `+indexerCols+indexerFrom+` WHERE i.id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Indexer{}, ErrNotFound
	}
	return ix, err
}

// NextIndexerID reserves an id for a source about to be created. The key is
// sealed bound to the id, so the id has to exist before the row does.
func (s *Store) NextIndexerID(ctx context.Context) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `SELECT nextval(pg_get_serial_sequence('indexers', 'id'))`).Scan(&id)
	return id, err
}

func categoriesJSON(c IndexerCategories) []byte {
	if c.Movie == nil {
		c.Movie = []int{}
	}
	if c.TV == nil {
		c.TV = []int{}
	}
	b, _ := json.Marshal(c)
	return b
}

// CreateIndexer inserts a source (with the id from NextIndexerID) and audits it
// in one transaction. ErrExists when the name is taken.
func (s *Store) CreateIndexer(ctx context.Context, ix Indexer, key SecretWrite, actor string) (Indexer, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Indexer{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var ct []byte
	var kid string
	var keyAt *time.Time
	if key.Op == SecretSet {
		now := time.Now()
		ct, kid, keyAt = key.CT, key.KID, &now
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO indexers (id, name, protocol, base_url, api_path, api_key_ct, api_key_kid, api_key_updated_at,
		                      categories, priority, enabled, query_limit_day, grab_limit_day)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9::jsonb,$10,$11,$12,$13)`,
		ix.ID, ix.Name, ix.Protocol, ix.BaseURL, ix.APIPath, ct, kid, keyAt,
		categoriesJSON(ix.Categories), ix.Priority, ix.Enabled, ix.QueryLimitDay, ix.GrabLimitDay)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return Indexer{}, ErrExists
		}
		return Indexer{}, err
	}
	if err := audit(ctx, tx, actor, "source", strconv.FormatInt(ix.ID, 10), "create"); err != nil {
		return Indexer{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Indexer{}, err
	}
	return s.GetIndexer(ctx, ix.ID)
}

// UpdateIndexer replaces a source's editable fields. ifRevision > 0 makes the
// write conditional (ErrStale on mismatch). resetHealth clears the failure
// count, the backoff and the last error — for when the admin has changed how
// the source is reached, or enabled it again.
func (s *Store) UpdateIndexer(ctx context.Context, ix Indexer, key SecretWrite, ifRevision int64, resetHealth bool, actor string) (Indexer, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Indexer{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var current int64
	err = tx.QueryRow(ctx, `SELECT revision FROM indexers WHERE id=$1 FOR UPDATE`, ix.ID).Scan(&current)
	if errors.Is(err, pgx.ErrNoRows) {
		return Indexer{}, ErrNotFound
	}
	if err != nil {
		return Indexer{}, err
	}
	if ifRevision > 0 && current != ifRevision {
		return Indexer{}, ErrStale
	}
	_, err = tx.Exec(ctx, `
		UPDATE indexers SET
		  name = $2, protocol = $3, base_url = $4, api_path = $5, categories = $6::jsonb,
		  priority = $7, enabled = $8, query_limit_day = $9, grab_limit_day = $10,
		  api_key_ct         = CASE $11::int WHEN 1 THEN $12::bytea WHEN 2 THEN NULL ELSE api_key_ct END,
		  api_key_kid        = CASE $11::int WHEN 1 THEN $13 WHEN 2 THEN '' ELSE api_key_kid END,
		  api_key_updated_at = CASE $11::int WHEN 1 THEN now() WHEN 2 THEN NULL ELSE api_key_updated_at END,
		  failures      = CASE WHEN $14 THEN 0 ELSE failures END,
		  backoff_until = CASE WHEN $14 THEN NULL ELSE backoff_until END,
		  last_error    = CASE WHEN $14 THEN '' ELSE last_error END,
		  revision = revision + 1,
		  updated_at = now()
		WHERE id = $1`,
		ix.ID, ix.Name, ix.Protocol, ix.BaseURL, ix.APIPath, categoriesJSON(ix.Categories),
		ix.Priority, ix.Enabled, ix.QueryLimitDay, ix.GrabLimitDay,
		int(key.Op), key.CT, key.KID, resetHealth)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return Indexer{}, ErrExists
		}
		return Indexer{}, err
	}
	if err := audit(ctx, tx, actor, "source", strconv.FormatInt(ix.ID, 10), "update"); err != nil {
		return Indexer{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Indexer{}, err
	}
	return s.GetIndexer(ctx, ix.ID)
}

// DeleteIndexer removes a source and its usage counters. Grabs keep the id they
// recorded: history outlives the configuration that produced it.
func (s *Store) DeleteIndexer(ctx context.Context, id int64, ifRevision int64, actor string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var current int64
	err = tx.QueryRow(ctx, `SELECT revision FROM indexers WHERE id=$1 FOR UPDATE`, id).Scan(&current)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if ifRevision > 0 && current != ifRevision {
		return ErrStale
	}
	if _, err := tx.Exec(ctx, `DELETE FROM indexers WHERE id=$1`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM indexer_usage WHERE indexer_id=$1`, id); err != nil {
		return err
	}
	if err := audit(ctx, tx, actor, "source", strconv.FormatInt(id, 10), "delete"); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// SetIndexerCaps caches a source's parsed capabilities.
func (s *Store) SetIndexerCaps(ctx context.Context, id int64, caps []byte) error {
	_, err := s.pool.Exec(ctx, `UPDATE indexers SET caps = $2::jsonb, caps_at = now() WHERE id = $1`, id, caps)
	return err
}

// IndexerSucceeded clears a source's failure state. It writes nothing when
// there is nothing to clear, so a healthy source costs no update per search.
func (s *Store) IndexerSucceeded(ctx context.Context, id int64) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE indexers SET failures = 0, backoff_until = NULL, last_error = ''
		 WHERE id = $1 AND (failures <> 0 OR backoff_until IS NOT NULL OR last_error <> '')`, id)
	return err
}

// IndexerFailed records a failed request. With backoff, the source is not asked
// again for base doubled per consecutive failure, capped at max: 5 minutes,
// 10, 20 … up to 6 hours with the service's values.
func (s *Store) IndexerFailed(ctx context.Context, id int64, lastError string, backoff bool, base, max time.Duration) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE indexers SET
		  failures = failures + 1,
		  last_error = $2,
		  backoff_until = CASE WHEN $3 THEN now() + LEAST(
		      ($4::double precision * POWER(2, LEAST(failures, 20))) * interval '1 second',
		      $5::double precision * interval '1 second')
		    ELSE backoff_until END
		WHERE id = $1`, id, lastError, backoff, base.Seconds(), max.Seconds())
	return err
}

// DisableIndexer turns a source off because it cannot work as configured (its
// key was rejected). It is a configuration change, so it moves the revision
// and is audited under actor.
func (s *Store) DisableIndexer(ctx context.Context, id int64, lastError, actor string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `
		UPDATE indexers SET enabled = false, failures = failures + 1, last_error = $2,
		       revision = revision + 1, updated_at = now()
		 WHERE id = $1 AND enabled`, id, lastError)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		// Already disabled: keep the newest reason, change nothing else.
		_, err := tx.Exec(ctx, `UPDATE indexers SET last_error = $2 WHERE id = $1`, id, lastError)
		if err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	if err := audit(ctx, tx, actor, "source", strconv.FormatInt(id, 10), "disable"); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ReserveIndexerQuery counts one request to a source against today's allowance
// and reports whether it may be sent. The check and the count are one
// statement, so concurrent searches cannot overspend. A nil limit always
// allows and still counts.
func (s *Store) ReserveIndexerQuery(ctx context.Context, id int64, limit *int) (bool, error) {
	return s.reserveUsage(ctx, id, "queries", limit)
}

// ReserveIndexerGrab is ReserveIndexerQuery for release file downloads.
func (s *Store) ReserveIndexerGrab(ctx context.Context, id int64, limit *int) (bool, error) {
	return s.reserveUsage(ctx, id, "grabs", limit)
}

func (s *Store) reserveUsage(ctx context.Context, id int64, column string, limit *int) (bool, error) {
	if column != "queries" && column != "grabs" {
		return false, errors.New("unknown usage column")
	}
	if limit != nil && *limit <= 0 {
		return false, nil
	}
	var n int
	err := s.pool.QueryRow(ctx, `
		INSERT INTO indexer_usage AS u (indexer_id, day, `+column+`)
		VALUES ($1, `+utcDay+`, 1)
		ON CONFLICT (indexer_id, day) DO UPDATE SET `+column+` = u.`+column+` + 1
		WHERE $2::int IS NULL OR u.`+column+` < $2::int
		RETURNING u.`+column, id, limit).Scan(&n)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}
