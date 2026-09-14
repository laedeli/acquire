-- Settings, their audit trail, and where a grab came from.
--
-- acquire_settings holds small admin-edited documents by key (the search and
-- grab policy first). Rows are seeded from the environment by the service, not
-- here: what the defaults are depends on the deployment, and data-dependent
-- work does not belong on the boot path (MIGRATIONS.md). revision is what an
-- editor sends back in If-Match, so two consoles cannot silently overwrite each
-- other.
CREATE TABLE IF NOT EXISTS acquire_settings (
  key        text        PRIMARY KEY,
  value      jsonb       NOT NULL,
  revision   bigint      NOT NULL DEFAULT 1,
  updated_at timestamptz NOT NULL DEFAULT now()
);

-- Who changed which configuration, and when. NEVER the values: a credential
-- written to an audit row is a credential stored in the clear, forever.
CREATE TABLE IF NOT EXISTS settings_audit (
  id        bigserial   PRIMARY KEY,
  at        timestamptz NOT NULL DEFAULT now(),
  actor     text        NOT NULL DEFAULT '',
  entity    text        NOT NULL,              -- client | source | settings
  entity_id text        NOT NULL DEFAULT '',
  action    text        NOT NULL               -- create | update | delete
);

CREATE INDEX IF NOT EXISTS settings_audit_at_idx ON settings_audit (at DESC);

-- A grab's provenance. source keeps only a redacted link (no apikey, r,
-- passkey or token); the full link, which carries the source's credential, is
-- kept sealed in source_ct under the key named by source_kid.
ALTER TABLE grabs ADD COLUMN IF NOT EXISTS indexer_id   bigint;
ALTER TABLE grabs ADD COLUMN IF NOT EXISTS release_guid text NOT NULL DEFAULT '';
ALTER TABLE grabs ADD COLUMN IF NOT EXISTS source_ct    bytea;
ALTER TABLE grabs ADD COLUMN IF NOT EXISTS source_kid   text NOT NULL DEFAULT '';
