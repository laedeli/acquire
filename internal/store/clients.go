package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	// ErrExists: a row with that id is already there.
	ErrExists = errors.New("already exists")
	// ErrStale: the row changed since the caller read it (If-Match mismatch).
	ErrStale = errors.New("changed since it was read")
	// ErrInFlight: the client still has downloads running.
	ErrInFlight = errors.New("downloads are still in flight on this client")
)

// DownloadClient is one configured download client. The secret is ciphertext:
// the store never sees a credential in the clear, and cannot open one.
type DownloadClient struct {
	ID              string
	Type            string
	BaseURL         string
	Auth            string // basic | token | none
	Username        string
	SecretCT        []byte
	SecretKID       string
	SecretUpdatedAt *time.Time
	Protocols       []string
	Category        string
	RemotePath      string // the save folder as the client sees it
	LocalPath       string // the same folder as acquire sees it
	Priority        int
	Enabled         bool
	Generation      int64
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// SecretOp is what a write does to a stored secret.
type SecretOp int

const (
	SecretKeep  SecretOp = iota // leave it as it is
	SecretSet                   // replace it with CT/KID
	SecretClear                 // remove it
)

// SecretWrite carries a sealed secret into a write.
type SecretWrite struct {
	Op  SecretOp
	CT  []byte
	KID string
}

const clientCols = `id, type, base_url, auth, username, secret_ct, secret_kid, secret_updated_at,
	protocols, category, remote_path, local_path, priority, enabled, generation, created_at, updated_at`

func scanClient(r interface{ Scan(...any) error }) (DownloadClient, error) {
	var c DownloadClient
	err := r.Scan(&c.ID, &c.Type, &c.BaseURL, &c.Auth, &c.Username, &c.SecretCT, &c.SecretKID,
		&c.SecretUpdatedAt, &c.Protocols, &c.Category, &c.RemotePath, &c.LocalPath,
		&c.Priority, &c.Enabled, &c.Generation, &c.CreatedAt, &c.UpdatedAt)
	if c.Protocols == nil {
		c.Protocols = []string{}
	}
	return c, err
}

// ListDownloadClients returns every client in routing order: lowest priority
// first, then id, so "the first client that handles usenet" is well defined.
func (s *Store) ListDownloadClients(ctx context.Context) ([]DownloadClient, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+clientCols+` FROM download_clients ORDER BY priority, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DownloadClient{}
	for rows.Next() {
		c, err := scanClient(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetDownloadClient returns one client or ErrNotFound.
func (s *Store) GetDownloadClient(ctx context.Context, id string) (DownloadClient, error) {
	c, err := scanClient(s.pool.QueryRow(ctx, `SELECT `+clientCols+` FROM download_clients WHERE id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return DownloadClient{}, ErrNotFound
	}
	return c, err
}

// CreateDownloadClient inserts a client and audits it in one transaction.
func (s *Store) CreateDownloadClient(ctx context.Context, c DownloadClient, secret SecretWrite, actor string) (DownloadClient, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return DownloadClient{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var ct []byte
	var kid string
	var secretAt *time.Time
	if secret.Op == SecretSet {
		now := time.Now()
		ct, kid, secretAt = secret.CT, secret.KID, &now
	}
	out, err := scanClient(tx.QueryRow(ctx, `
		INSERT INTO download_clients (id, type, base_url, auth, username, secret_ct, secret_kid,
		                              secret_updated_at, protocols, category, remote_path, local_path,
		                              priority, enabled)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		RETURNING `+clientCols,
		c.ID, c.Type, c.BaseURL, c.Auth, c.Username, ct, kid, secretAt, nonNil(c.Protocols),
		c.Category, c.RemotePath, c.LocalPath, c.Priority, c.Enabled))
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return DownloadClient{}, ErrExists
		}
		return DownloadClient{}, err
	}
	if err := audit(ctx, tx, actor, "client", c.ID, "create"); err != nil {
		return DownloadClient{}, err
	}
	return out, tx.Commit(ctx)
}

// UpdateDownloadClient replaces a client's editable fields. ifGeneration > 0
// makes the write conditional on the row still being at that generation;
// ErrStale when it is not. The id and type never change.
func (s *Store) UpdateDownloadClient(ctx context.Context, c DownloadClient, secret SecretWrite, ifGeneration int64, actor string) (DownloadClient, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return DownloadClient{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var current int64
	err = tx.QueryRow(ctx, `SELECT generation FROM download_clients WHERE id=$1 FOR UPDATE`, c.ID).Scan(&current)
	if errors.Is(err, pgx.ErrNoRows) {
		return DownloadClient{}, ErrNotFound
	}
	if err != nil {
		return DownloadClient{}, err
	}
	if ifGeneration > 0 && current != ifGeneration {
		return DownloadClient{}, ErrStale
	}
	out, err := scanClient(tx.QueryRow(ctx, `
		UPDATE download_clients SET
		  base_url = $2, auth = $3, username = $4, protocols = $5, category = $6,
		  remote_path = $7, local_path = $8, priority = $9, enabled = $10,
		  secret_ct         = CASE $11::int WHEN 1 THEN $12::bytea WHEN 2 THEN NULL ELSE secret_ct END,
		  secret_kid        = CASE $11::int WHEN 1 THEN $13 WHEN 2 THEN '' ELSE secret_kid END,
		  secret_updated_at = CASE $11::int WHEN 1 THEN now() WHEN 2 THEN NULL ELSE secret_updated_at END,
		  generation = nextval('download_clients_generation_seq'),
		  updated_at = now()
		WHERE id = $1
		RETURNING `+clientCols,
		c.ID, c.BaseURL, c.Auth, c.Username, nonNil(c.Protocols), c.Category,
		c.RemotePath, c.LocalPath, c.Priority, c.Enabled,
		int(secret.Op), secret.CT, secret.KID))
	if err != nil {
		return DownloadClient{}, err
	}
	if err := audit(ctx, tx, actor, "client", c.ID, "update"); err != nil {
		return DownloadClient{}, err
	}
	return out, tx.Commit(ctx)
}

// DeleteDownloadClient removes a client. It refuses with ErrInFlight while the
// client still has downloads running — removing it would orphan them, and
// their completion would then have no path mapping to resolve the files.
func (s *Store) DeleteDownloadClient(ctx context.Context, id string, ifGeneration int64, actor string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var current int64
	err = tx.QueryRow(ctx, `SELECT generation FROM download_clients WHERE id=$1 FOR UPDATE`, id).Scan(&current)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if ifGeneration > 0 && current != ifGeneration {
		return ErrStale
	}
	var active int
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM downloads WHERE adapter=$1 AND state NOT IN ('completed','failed','cancelled')`,
		id).Scan(&active); err != nil {
		return err
	}
	if active > 0 {
		return ErrInFlight
	}
	if _, err := tx.Exec(ctx, `DELETE FROM download_clients WHERE id=$1`, id); err != nil {
		return err
	}
	// A delete moves the revision forward too, so the gateway is told the set
	// shrank even when the deleted row did not hold the highest generation.
	if _, err := tx.Exec(ctx, `SELECT nextval('download_clients_generation_seq')`); err != nil {
		return err
	}
	if err := audit(ctx, tx, actor, "client", id, "delete"); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ClientsRevision is the configuration revision: the highest generation ever
// issued, deleted rows included. 0 means no client was ever written.
func (s *Store) ClientsRevision(ctx context.Context) (int64, error) {
	var rev int64
	err := s.pool.QueryRow(ctx,
		`SELECT CASE WHEN is_called THEN last_value ELSE 0 END FROM download_clients_generation_seq`).Scan(&rev)
	return rev, err
}

// ActiveDownloadsOn counts in-flight downloads on one client.
func (s *Store) ActiveDownloadsOn(ctx context.Context, clientID string) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM downloads WHERE adapter=$1 AND state NOT IN ('completed','failed','cancelled')`,
		clientID).Scan(&n)
	return n, err
}

func nonNil(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

// audit records who changed which configuration. Values are never recorded.
func audit(ctx context.Context, tx pgx.Tx, actor, entity, entityID, action string) error {
	_, err := tx.Exec(ctx,
		`INSERT INTO settings_audit (actor, entity, entity_id, action) VALUES ($1,$2,$3,$4)`,
		actor, entity, entityID, action)
	return err
}
