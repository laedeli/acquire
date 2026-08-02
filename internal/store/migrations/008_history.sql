-- Append-only history.
--
-- DELIBERATELY NO FOREIGN KEY. `grabs` references wanted_items ON DELETE
-- CASCADE, which is why beta's grabs table holds zero rows despite five real
-- grab events having happened: removing the request erased the evidence that it
-- was ever acted on. History has to outlive its subject — it is the only record
-- of what we chose and why, and the shadow period compares against it.
CREATE TABLE IF NOT EXISTS history (
  id         bigserial PRIMARY KEY,
  at         timestamptz NOT NULL DEFAULT now(),
  kind       text        NOT NULL,   -- grabbed | failed | imported | blocked | upgraded | …
  subject    text        NOT NULL DEFAULT '',
  title      text        NOT NULL DEFAULT '',
  indexer    text        NOT NULL DEFAULT '',
  protocol   text        NOT NULL DEFAULT '',
  size_mb    bigint      NOT NULL DEFAULT 0,
  score      int         NOT NULL DEFAULT 0,
  reason     text        NOT NULL DEFAULT '',
  detail     jsonb       NOT NULL DEFAULT '{}'::jsonb
);

CREATE INDEX IF NOT EXISTS history_at_idx      ON history (at DESC);
CREATE INDEX IF NOT EXISTS history_subject_idx ON history (subject, at DESC);
CREATE INDEX IF NOT EXISTS history_kind_idx    ON history (kind, at DESC);
