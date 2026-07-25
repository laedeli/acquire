# acquire (laedeli addon) — requests + downloads for the zaentrum platform

`acquire` is the **acquisition addon**: it brings request + download capability to
a neutral zaentrum instance. The core platform ships only extension *seams*;
installing this addon (via the Lädeli) surfaces a "request" button in chino's
search and drives download → package → playback. Uninstalled, the core shows no
acquisition UI.

This is **v0.1** for zaentrum-beta. It is deliberately event-driven and reuses
neutral core capabilities — it adds **no** download-specific code to the core.

## Documentation

The full design docs live in [`docs/`](./docs) and are mirrored to this repo's
**[Wiki](https://github.com/laedeli/acquire/wiki)**:

- [Architecture](./docs/architecture.md) — the two extension seams, the
  event-driven loop, the neutral-core property.
- [The acquire service](./docs/acquire.md) — request lifecycle, API, SPA, schema.
- [Download gateway & clients](./docs/download-gateway.md) — the download plane.
- [Indexer search & NZB-first](./docs/indexers-and-nzb.md) — how auto-grab chooses.
- [Deploying the addon](./docs/deploying.md) — installing it on a platform.

## Architecture in one paragraph

A request becomes a **WantedItem**; an admin **grabs** it (manually, or via
`find & grab` which searches your indexers NZB-first); acquire hands the source to
the **download-gateway**, which drives your download clients and emits
`download.client.*` events. acquire is **consume-only**: on `completed` it ingests
the finished file *in place* via katalog-manager's neutral `POST /api/ingest`
(which emits `catalog.item.discovered`), the core pipeline enriches → analyzes →
transcodes → packages, and on `catalog.item.packaged` the request flips to
**fulfilled** and plays in chino. Commands go over HTTP at the edges (request,
grab, ingest); everything else is Kafka. See
[Architecture](./docs/architecture.md) for the diagrams.

## What's authored (this tree, compiling backend pieces)

- `internal/config` — env wiring (issuer, DB, gateway, katalog, TMDB, Kafka mTLS, inbox paths).
- `internal/store` — `acquire_beta` schema (wanted_items + grabs) + CRUD + status transitions + client-job↔wanted bridge.
- `internal/events` — Kafka mTLS: **producer** for `catalog.item.discovered`; **consumer** for `download.client.completed/failed` + `catalog.item.packaged`. Tenant-prefixed topics.
- `internal/gateway` — download-gateway command client + a client-credentials `TokenSource` (service account on the shared realm).
- `internal/tmdb` — multi-search discovery (movies + series → tmdb id + metadata).

## Remaining integration points (wire + verify against LIVE beta katalog-manager)

1. **Catalog write (the ingest step).** On `download.client.completed`, acquire
   must create the item and attach the staged source file, then emit
   `catalog.item.discovered`. katalog-manager exposes a GraphQL `CreateItem`
   mutation (`internal/graph/resolver.go:538`, input: type/title/year/
   description/…). Open question to verify live: how to attach the **source
   PlaybackAsset path** (inbox path) so the analyzer reads it — GraphQL asset
   mutation vs a REST seam. Model on `katalog-manager/internal/odownloader/
   poller.go` (which streams to inbox + writes the asset) + the manual-packaging
   recipe. Verify the pipeline picks up the discovered event and packages.
2. **In-library check** — `GET katalog-api /api/v1/items?...` by tmdb id (confirm
   a tmdb filter exists; else title+year match) to flag "already in library".
3. **httpapi** — REST (`/api/wanted`, `/grab`, `/discover`, `/status`) with OIDC
   bearer auth (user role requests, admin grabs) + the Kafka→SSE bridge.
4. **web** — embedded React SPA at `/acquire/` (@nalet/design-system): discovery
   search + request, my-requests, admin grab dialog, live status via SSE.
5. **main** — wire store + events + clients + http; lazy/fail-closed OIDC.

## Deploy (beta) — see zaentrum/deploy zaentrum-beta overlay + SSO clients
`laedeli-acquire` (public PKCE, redirect zaentrum.beta.nalet.cloud/acquire/*) +
`laedeli-acquire-svc` (confidential SA, role `zaentrum-addon`). DB `acquire_beta`
on the shared cluster. qbittorrent-nox saving into `packages/_downloads/`.
