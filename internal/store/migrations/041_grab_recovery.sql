-- Reservations taken once, hand-overs the reconcile respects, and failures
-- that are tried again.
--
-- reserved_by is the pending grab a target is reserved for while its release
-- is handed to a client. A reservation is taken with one conditional UPDATE
-- (reserved_by = '' ... RETURNING): a second grab racing for the same target
-- waits on the row lock and then finds it taken. Reading the target and its
-- grab first and deciding in the client, as 040's code did, let both take it.
-- The grab is bound to the target (grab_id) once the client has the release.
ALTER TABLE acquisition_targets ADD COLUMN IF NOT EXISTS reserved_by    text NOT NULL DEFAULT '';
-- handed_over_at is when acquire last handed a file for the target to the
-- catalog and the catalog answered that it holds it. katalog-api may not list
-- it yet; a reconcile does not make such a target wanted again for a while.
ALTER TABLE acquisition_targets ADD COLUMN IF NOT EXISTS handed_over_at timestamptz;
-- A target failed by its release — its download failed, the download held no
-- file for it, the catalog refused the file — is wanted again at retry_at,
-- after a backoff, up to five times (grab_failures); then it stays failed for
-- someone to look at. The releases that failed stay blocked for it.
ALTER TABLE acquisition_targets ADD COLUMN IF NOT EXISTS grab_failures  int NOT NULL DEFAULT 0;
ALTER TABLE acquisition_targets ADD COLUMN IF NOT EXISTS retry_at       timestamptz;

CREATE INDEX IF NOT EXISTS targets_reserved_idx ON acquisition_targets (reserved_by) WHERE reserved_by <> '';
CREATE INDEX IF NOT EXISTS targets_retry_idx    ON acquisition_targets (retry_at) WHERE state = 'failed' AND retry_at IS NOT NULL;
