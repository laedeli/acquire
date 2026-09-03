# acquire (laedeli addon) — requests + downloads for the zaentrum platform

`acquire` is the **acquisition addon**: it brings request + download capability
to a neutral [zaentrum](https://github.com/zaentrum/zaentrum) instance. The
core platform ships only extension *seams*; installing this addon surfaces a
"request" button in chino's search and drives download → ingest → package →
playback. Uninstalled, the core shows no acquisition UI and no trace.

It is deliberately event-driven and reuses neutral core capabilities — it adds
**no** download-specific code to the core. The platform-side contracts it
builds on are documented canonically in the platform docs:
**[Extending zaentrum](https://github.com/zaentrum/zaentrum/wiki/extending)**.

## What it is

- **The acquire service** — requests (WantedItems), discovery search, the
  grab decision (NZB-first ranking against your indexers), a transactional
  outbox + scheduler, and ingestion of finished downloads into the catalog via
  the platform's neutral ingest seam. Serves its own React console, hosted
  inside the portal shell.
- **[download-gateway](https://github.com/laedeli/download-gateway)** — the
  download plane: one API + Kafka events (`download.client.*`) in front of the
  actual download clients.

The loop: a request becomes a **WantedItem** → a grab (manual, or `find &
grab`, which searches indexers NZB-first) hands the source to the gateway →
the gateway drives a download client and emits progress events → on
`completed`, acquire ingests the finished file in place (`POST /api/ingest`,
which emits `catalog.item.discovered`) → the core pipeline enriches, analyzes,
transcodes, packages → on `catalog.item.packaged` the request flips to
**fulfilled** and plays in chino. Commands go over HTTP at the edges;
everything else is Kafka.

## Documentation

Full design docs live in [`docs/`](./docs) and are mirrored to this repo's
**[Wiki](https://github.com/laedeli/acquire/wiki)**:

- [Architecture](./docs/architecture.md) — how acquire uses the platform's
  extension seams; the event-driven loop.
- [The acquire service](./docs/acquire.md) — request lifecycle, API, console,
  schema.
- [Download gateway & clients](./docs/download-gateway.md) — the download plane.
- [Indexer search & NZB-first](./docs/indexers-and-nzb.md) — how auto-grab
  chooses a release.
- [Deploying the addon](./docs/deploying.md) — installing it on a platform,
  the addon-side view of the platform's
  [installing addons](https://github.com/zaentrum/zaentrum/wiki/extending-installing)
  guide.

## Status

Live end-to-end on the author's instance: request → grab → download → ingest →
package → playback, with the console (requests, downloads, search, indexers,
series, missing backlog, quality profiles) hosted in the portal. Expect sharp
edges: this is a young codebase serving one deployment so far, and
installation still involves the manual steps the platform's
[addon identity](https://github.com/zaentrum/zaentrum/wiki/extending-identity)
page describes.
