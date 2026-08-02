-- Failure recovery: the blocklist.
--
-- Roughly one grab in three fails in production (24% of movies, 43% of TV).
-- Today acquire writes 'failed' and stops, and because ranking is deterministic
-- a retry re-picks the identical bad release forever. The incumbent turns 196
-- failures into 168 automatic re-grabs at a median of 64 seconds, 149 of them
-- on a DIFFERENT release — that difference is the blocklist.
CREATE TABLE IF NOT EXISTS blocklist (
  release_title text        NOT NULL,
  target_id     text        NOT NULL DEFAULT '',
  indexer       text        NOT NULL DEFAULT '',
  reason        text        NOT NULL DEFAULT '',
  blocked_at    timestamptz NOT NULL DEFAULT now(),
  expires_at    timestamptz,
  PRIMARY KEY (release_title, target_id)
);

-- The ranker's lookup: everything blocked for one target.
CREATE INDEX IF NOT EXISTS blocklist_target_idx ON blocklist (target_id);

-- A block can expire: a release that failed because an indexer was down should
-- not be refused forever. A permanent block leaves expires_at NULL.
CREATE INDEX IF NOT EXISTS blocklist_expiry_idx ON blocklist (expires_at)
  WHERE expires_at IS NOT NULL;
