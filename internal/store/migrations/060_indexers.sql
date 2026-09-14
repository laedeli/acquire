-- Search sources: the newznab and torznab endpoints acquire searches itself.
--
-- Until now acquire asked an external aggregator, which held the indexer list
-- and every API key. The list is acquire's data: an admin edits it in the
-- console, and acquire speaks each source's API directly with that source's
-- own key.
--
-- The API key is never stored in the clear. api_key_ct is AES-256-GCM
-- ciphertext (internal/secretbox) bound to "indexers|<id>|api_key";
-- api_key_kid names the key that sealed it. A source without a key (a public
-- feed) keeps api_key_ct NULL.
--
-- categories is {"movie":[...],"tv":[...]}: the newznab category ids a movie
-- or TV search is scoped to. caps is the source's parsed t=caps answer, cached
-- on save, on test and daily; caps_at is when it was fetched.
--
-- Health is kept here rather than in memory so a restart does not forget that
-- a source is rate-limiting us: failures counts consecutive failed requests,
-- backoff_until is when the source may be asked again, last_error is the last
-- failure as an operator reads it (never a credential). revision moves on every
-- admin write (and when acquire disables a source whose key was rejected), not
-- on health bookkeeping, so an open editor is not invalidated by a search.
--
-- query_limit_day and grab_limit_day are the source's daily allowance; NULL is
-- no limit. indexer_usage counts what was spent per UTC day.
--
-- No foreign keys, as everywhere in this directory: these files re-run on every
-- boot and ADD CONSTRAINT has no IF NOT EXISTS.
CREATE TABLE IF NOT EXISTS indexers (
  id                 bigserial   PRIMARY KEY,
  name               text        NOT NULL UNIQUE,
  protocol           text        NOT NULL CHECK (protocol IN ('usenet', 'torrent')),
  base_url           text        NOT NULL,
  api_path           text        NOT NULL DEFAULT '/api',
  api_key_ct         bytea,
  api_key_kid        text        NOT NULL DEFAULT '',
  api_key_updated_at timestamptz,
  categories         jsonb       NOT NULL DEFAULT '{"movie":[],"tv":[]}',
  priority           int         NOT NULL DEFAULT 0,    -- lower is asked first
  enabled            boolean     NOT NULL DEFAULT true,
  caps               jsonb,
  caps_at            timestamptz,
  query_limit_day    int,
  grab_limit_day     int,
  failures           int         NOT NULL DEFAULT 0,
  backoff_until      timestamptz,
  last_error         text        NOT NULL DEFAULT '',
  revision           bigint      NOT NULL DEFAULT 1,
  created_at         timestamptz NOT NULL DEFAULT now(),
  updated_at         timestamptz NOT NULL DEFAULT now()
);

-- What each source spent per UTC day. Rows of a deleted source are removed
-- with it by the service.
CREATE TABLE IF NOT EXISTS indexer_usage (
  indexer_id bigint NOT NULL,
  day        date   NOT NULL,
  queries    int    NOT NULL DEFAULT 0,
  grabs      int    NOT NULL DEFAULT 0,
  PRIMARY KEY (indexer_id, day)
);
