-- Managed download clients: the ones the deployment runs itself.
--
-- The addon chart can deploy qBittorrent and NZBGet next to acquire and
-- declares them to it (ACQUIRE_MANAGED_CLIENTS). acquire writes those rows at
-- start with managed = true; their address, credentials and folders follow
-- the deployment, so the configuration API refuses to edit or delete them.
-- Every row written before this column existed is an admin's: false.
ALTER TABLE download_clients ADD COLUMN IF NOT EXISTS managed boolean NOT NULL DEFAULT false;
