-- downloads is a PROJECTION of the download clients' state, not data we own.
--
-- The wanted_id foreign key made that projection fragile: the gateway keeps
-- emitting telemetry with the request id it was given, so deleting a request
-- while its download is still running made every subsequent upsert fail with a
-- FK violation. The row then froze mid-progress and nothing could clear it.
-- Treat the link as a soft reference instead — a dangling id is simply a
-- download whose request is gone.
ALTER TABLE downloads DROP CONSTRAINT IF EXISTS downloads_wanted_id_fkey;
