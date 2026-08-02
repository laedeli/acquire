package store

import (
	"context"
	"time"
)

// Block records a release we must not pick again for this target.
//
// Without one, retrying is pointless: ranking is deterministic, so the next
// attempt re-picks the identical release that just failed. The blocklist is the
// entire difference between "retry" and "retry with the next-best".
func (s *Store) Block(ctx context.Context, releaseTitle, targetID, indexer, reason string, ttl time.Duration) error {
	var expires *time.Time
	if ttl > 0 {
		t := time.Now().Add(ttl)
		expires = &t
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO blocklist (release_title, target_id, indexer, reason, expires_at)
		VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT (release_title, target_id) DO UPDATE
		   SET reason = EXCLUDED.reason, blocked_at = now(), expires_at = EXCLUDED.expires_at`,
		releaseTitle, targetID, indexer, reason, expires)
	return err
}

// BlockedFor returns the live blocks for a target. Expired ones are excluded
// rather than deleted: a release that failed because an indexer was down should
// become available again, and keeping the row preserves the history of why.
func (s *Store) BlockedFor(ctx context.Context, targetID string) (map[string]string, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT release_title, reason FROM blocklist
		 WHERE (target_id = $1 OR target_id = '')
		   AND (expires_at IS NULL OR expires_at > now())`, targetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var t, r string
		if err := rows.Scan(&t, &r); err != nil {
			return nil, err
		}
		out[t] = r
	}
	return out, rows.Err()
}

// BlocklistSize powers the metric; a blocklist that only grows is a signal in
// itself.
func (s *Store) BlocklistSize(ctx context.Context) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM blocklist WHERE expires_at IS NULL OR expires_at > now()`).Scan(&n)
	return n, err
}
