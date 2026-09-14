-- Download clients: the external download programs acquire hands releases to.
--
-- Until now the gateway read its clients from its own environment and acquire
-- hard-coded two adapter names. The configuration is acquire's data: an admin
-- edits it in the console, it is stored here, and internal/configsync pushes it
-- to the gateway, which holds it in memory only.
--
-- id is the gateway's client id and follows the gateway's rule, so a row can
-- never hold an id the gateway would refuse. It is also what downloads.adapter
-- and grabs.adapter hold — which is why the first client of each type defaults
-- to the type's name: rows written before this table existed keep resolving.
--
-- The secret is never stored in the clear. secret_ct is AES-256-GCM ciphertext
-- (internal/secretbox) bound to "download_clients|<id>|secret"; secret_kid names
-- the key that sealed it.
--
-- generation comes from one sequence on every write, and a delete consumes a
-- value too, so the sequence position is a revision that only moves forward.
-- The gateway is told that revision and reports it back; any difference means
-- it is running something other than what is stored here.
--
-- No foreign keys, as everywhere in this directory: these files re-run on every
-- boot and ADD CONSTRAINT has no IF NOT EXISTS.
CREATE SEQUENCE IF NOT EXISTS download_clients_generation_seq;

CREATE TABLE IF NOT EXISTS download_clients (
  id                text        PRIMARY KEY CHECK (id ~ '^[a-z0-9][a-z0-9-]{0,39}$'),
  type              text        NOT NULL,
  base_url          text        NOT NULL,
  auth              text        NOT NULL DEFAULT 'none' CHECK (auth IN ('basic', 'token', 'none')),
  username          text        NOT NULL DEFAULT '',
  secret_ct         bytea,
  secret_kid        text        NOT NULL DEFAULT '',
  secret_updated_at timestamptz,
  protocols         text[]      NOT NULL DEFAULT '{}',
  category          text        NOT NULL DEFAULT 'acquire',
  remote_path       text        NOT NULL DEFAULT '',   -- the save folder as the client sees it
  local_path        text        NOT NULL DEFAULT '',   -- the same folder as acquire sees it
  priority          int         NOT NULL DEFAULT 0,    -- lower wins
  enabled           boolean     NOT NULL DEFAULT true,
  generation        bigint      NOT NULL DEFAULT nextval('download_clients_generation_seq'),
  created_at        timestamptz NOT NULL DEFAULT now(),
  updated_at        timestamptz NOT NULL DEFAULT now()
);

-- Routing reads the enabled clients for a protocol, lowest priority first.
CREATE INDEX IF NOT EXISTS download_clients_routing_idx
  ON download_clients (priority, id) WHERE enabled;
