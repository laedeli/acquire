-- Transactional outbox.
--
-- The platform is deliberately event-driven, but emitting to Kafka from inside
-- a request is a lie: the broker call can fail after the row is committed, or
-- succeed and then the transaction rolls back. Either way the database and the
-- event stream disagree and nothing notices.
--
-- Writing the domain row and the event row in ONE transaction makes the event
-- as durable as the fact it describes. A relay then publishes and marks
-- delivered, at-least-once. Consumers must be idempotent; they already are,
-- because the download consumer is.
CREATE TABLE IF NOT EXISTS outbox (
  id            bigserial PRIMARY KEY,
  topic         text        NOT NULL,
  key           text        NOT NULL DEFAULT '',
  payload       jsonb       NOT NULL,
  created_at    timestamptz NOT NULL DEFAULT now(),
  published_at  timestamptz,
  attempts      int         NOT NULL DEFAULT 0,
  last_error    text        NOT NULL DEFAULT ''
);

-- The relay's only query: oldest undelivered first. Partial, so the index stays
-- the size of the backlog rather than the size of history.
CREATE INDEX IF NOT EXISTS outbox_pending_idx
  ON outbox (id) WHERE published_at IS NULL;

-- Published rows are kept briefly as an audit trail, then swept. Indexed so the
-- sweep does not seq-scan a table that only ever grows.
CREATE INDEX IF NOT EXISTS outbox_published_idx
  ON outbox (published_at) WHERE published_at IS NOT NULL;
