# The acquire service

`acquire` (Go, module `github.com/laedeli/acquire`) is the addon's brain and face.
It records requests, drives grabs, reacts to download and pipeline events, and
serves its own single-page console. The service mounts the SPA + API at its
**root**; the reference deploy exposes it under `/acquire` on the portal host (see
[Deploying the addon](./deploying.md)).

## Request lifecycle

A request is a **WantedItem**. Its `status` walks a small state machine:

```mermaid
stateDiagram-v2
    [*] --> pending: POST /api/wanted
    pending --> downloading: grab / find & grab
    downloading --> packaging: download completed → ingested
    packaging --> fulfilled: pipeline packaged
    pending --> failed
    downloading --> failed
    packaging --> failed
    failed --> downloading: re-grab
    fulfilled --> [*]
```

- **pending** — the request exists (v1 is auto-approved: it simply waits for an
  admin to grab it).
- **downloading** — a source was handed to a download client. The detail line
  records how, e.g. `grabbed NZB from <indexer> via nzbget` or `grabbed via qbittorrent`.
- **packaging** — the download finished; acquire resolved the video file and
  ingested it (`ingested; pipeline running`).
- **fulfilled** — the pipeline packaged the item; it now plays in chino.
- **failed** — any of: grab error, indexer search error, no releases, download
  failed, no video file in the completed download, or ingest error. The detail
  line carries the reason. `pending` and `failed` requests are re-grabbable.

acquire reacts to three consumed events:

- `download.client.completed` → resolve the video file → `POST /api/ingest` →
  `packaging`.
- `download.client.failed` → `failed` (with the client's error).
- `catalog.item.packaged` → look up the request by item id → `fulfilled`.

**Resolving the video file** from a completed download: acquire walks the download
folder on the shared media NFS and picks the **largest** file with a video
extension (`.mkv .mp4 .m4v .avi .mov .ts .webm`). It ingests that file *in place*
— no staging copy.

## HTTP API

Router: `go-chi`. Auth is an OIDC bearer verifier (issuer-only; roles from the
JWT `realm_access.roles`). If `OIDC_ISSUER` is unset, auth is disabled for local
dev (every caller is treated as admin).

| Method | Path | Who | Purpose |
|---|---|---|---|
| `GET` | `/healthz` | public | liveness |
| `GET` | `/readyz` | public | DB ping (503 when down) |
| `GET` | `/api/config` | public | SPA bootstrap: issuer, client id, admin role, `autoGrab` flag |
| `GET` | `/api/events` | public¹ | SSE change-ping stream |
| `GET` | `/api/wanted` | any signed-in | list requests |
| `POST` | `/api/wanted` | **user** or admin | create a request |
| `GET` | `/api/discover?q=` | any signed-in | TMDB search, flags in-library hits |
| `GET` | `/api/status?tmdbId=` | any signed-in | status of the newest request for a TMDB id |
| `POST` | `/api/wanted/{id}/grab` | **admin** | hand a magnet/URL to a client |
| `POST` | `/api/wanted/{id}/autograb` | **admin** | search indexers + grab the best release |
| `DELETE` | `/api/wanted/{id}` | **admin** | remove a request |
| `GET` | `/*` | public | the embedded SPA |

¹ The SSE stream is public because `EventSource` can't send a bearer; it carries
**no data** — it's just a “something changed, refetch” ping, and clients refetch
the authenticated list.

### Roles

| Role (default) | Env | Can |
|---|---|---|
| `zaentrum-user` | `ACQUIRE_USER_ROLE` | request titles |
| `zaentrum-admin` | `ACQUIRE_ADMIN_ROLE` | request **+** grab, auto-grab, remove |

## The console (SPA)

The embedded SPA (OIDC Auth-Code + PKCE, no build step) offers:

- **Search & request** — search TMDB, request a hit (disabled when already in the
  library).
- **Requests table** — title · status pill · detail · actions.
- **Admin row actions** —
  - **find & grab** → `autograb` (shown only when `autoGrab` is configured and the
    request is `pending`/`failed`); see [Indexer search & NZB-first](./indexers-and-nzb.md).
  - **magnet** → prompts for a magnet/`.torrent` URL and grabs it via qBittorrent.
  - **remove** → deletes the request.
- **Live updates** — the SPA subscribes to `/api/events` and refetches on a ping.

## Storage

acquire owns a small Postgres schema (applied on boot, idempotent):

**`wanted_items`** — `id`, `tmdb_id`, `media_type` (`movie`/`series`), `title`,
`year`, `poster_url`, `requested_by` (Keycloak sub), `requested_at`, `status`,
`detail` (last status message), `item_id` (catalog id once created), `updated_at`.

**`grabs`** — `wanted_id` (FK, cascade), `adapter`, `client_job_id`, `source`,
`created_at`; PK `(wanted_id, adapter, client_job_id)`. An audit trail of what was
handed to which client. (Completed/failed events actually map back to a request
via the `wanted_item_id` echoed in the gateway's event payload — not via this
table; a `client_job_id → wanted` lookup exists but is currently unused.)

## Configuration

| Env | Default | Purpose |
|---|---|---|
| `ADDR` | `:8080` | listen address |
| `OIDC_ISSUER` | — | realm issuer (blank ⇒ auth disabled, dev only) |
| `ACQUIRE_OIDC_CLIENT_ID` | `laedeli-acquire` | public PKCE client id (for the SPA) |
| `ACQUIRE_ADMIN_ROLE` | `zaentrum-admin` | admin role |
| `ACQUIRE_USER_ROLE` | `zaentrum-user` | request role |
| `PG_URL` (`DATABASE_URL`) | — | Postgres URL |
| `DOWNLOAD_GATEWAY_URL` | — | the gateway base URL |
| `KATALOG_URL` | — | catalog read API (in-library check) |
| `KATALOG_MANAGER_URL` | — | ingest target |
| `OIDC_TOKEN_URL` | — | token endpoint for the service account |
| `ACQUIRE_SVC_CLIENT_ID` / `ACQUIRE_SVC_CLIENT_SECRET` | — | client-credentials for gateway + ingest calls |
| `TMDB_API_KEY` | — | discovery (optional) |
| `PROWLARR_URL` / `PROWLARR_API_KEY` | — | indexer search (blank ⇒ auto-grab off) |
| `ACQUIRE_PREFER` | `usenet` | ranking preference (`usenet` ⇒ NZB-first) |
| `KAFKA_BROKERS` | — | bootstrap (blank ⇒ consumer off, service stays up) |
| `KAFKA_CERT_DIR` | `/etc/kafka-cert` | mTLS cert dir (`user.crt`/`user.key`/`ca.crt`) |
| `KAFKA_TOPIC_PREFIX` | `zaentrum-beta.` | tenant prefix |
| `KAFKA_GROUP_ID` | `acquire` | consumer group |
| `ACQUIRE_DOWNLOADS_ROOT` | `/var/lib/katalog/packages/_downloads` | where the client saves (for path resolution) |

acquire calls the gateway and katalog-manager with a **service-account** token
(client-credentials), refreshed shortly before expiry, so those calls are
authenticated independently of any user.

## Next

- [Download gateway & clients](./download-gateway.md) — where the bytes come from.
- [Indexer search & NZB-first](./indexers-and-nzb.md) — how `find & grab` chooses.
