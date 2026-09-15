# Deploying the addon

acquire is **two containers**: `acquire` itself and `download-gateway`. That
is the whole deployment. The programs that download and the sources that are
searched are **not** part of it — they are endpoints you already run or use,
and an admin tells acquire where they are in its console after the install.
Nothing third-party is deployed, bundled or configured through manifests.

The usual way to install it is the **addon chart**: a Helm chart the platform's
operator installs from a chart reference. It deploys both containers, and the
portal registers the addon as soon as they are ready. You can also deploy the
two containers through your own channel and install the addon by address, as
the platform's
[installing addons](https://github.com/zaentrum/zaentrum/wiki/extending-installing)
guide describes; the chart renders exactly the shapes described below.

```mermaid
flowchart TB
    subgraph ns["platform namespace"]
        ACQ["Deployment/Service acquire<br/><i>primary: console, API, manifest</i>"]
        GW["Deployment/Service download-gateway<br/><i>required: runs the download clients</i>"]
        PAPI["portal-api<br/>(core)"]
    end
    DB[("acquire's Postgres<br/>requests · sources · clients · settings")]
    SRC["search sources<br/><i>newznab / torznab endpoints</i>"]
    DC["download clients<br/><i>external endpoints</i>"]
    MEDIA[("media storage")]

    PAPI -.->|"GET /.well-known/zaentrum-capability.json<br/>+ console proxy"| ACQ
    ACQ --> DB
    ACQ -->|"search, fetch release files"| SRC
    ACQ -->|"PUT /api/v1/config/clients<br/>POST /api/v1/downloads"| GW
    GW --> DC
    DC --> MEDIA
    ACQ -.->|"reads finished files"| MEDIA
```

## Prerequisites

- A running **zaentrum platform** with the ingest seam (katalog-manager
  `POST /api/ingest`) and the portal's addon install. Installing the chart
  needs the operator's addon charts (the `ZaentrumAddon` resource).
- The platform's **event streaming over mTLS**. Both components talk to the
  brokers with the platform's client certificate: on a plaintext broker the
  gateway emits no events and acquire's consumer does not start, so finished
  downloads are never picked up.
- An **OIDC realm** you can add two clients to (see [Identity](#identity)).
- A **Postgres database** for acquire.
- The **media storage** the download clients write to, mountable into acquire
  so it can find finished files. The chart mounts the platform's media claim.

## The components

| Component | Role | Image | Port | Needs |
|---|---|---|---|---|
| `acquire` | primary | `ghcr.io/laedeli/acquire` | 8080 | Postgres, media storage (read-only), the config key |
| `download-gateway` | required | `ghcr.io/laedeli/download-gateway` | 8080 | nothing stored — clients are pushed to it at runtime |

Each Deployment and its Service are named after the component's workload
(`acquire` and `download-gateway`), with the grouping labels in **metadata**
only, never in a selector. The chart does this; deploying the manifests
yourself, do the same:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: download-gateway
  labels:
    zaentrum.io/addon: acquire
    zaentrum.io/component: download-gateway
spec:
  selector:
    matchLabels: { app: download-gateway }   # selectors stay your own
```

No Ingress or Route is needed: the portal proxies acquire's console and API.
The gateway workload acquire declares is the first label of the host in
`DOWNLOAD_GATEWAY_URL` (`http://download-gateway` → `download-gateway`); if
you rename the gateway, keep the two in step.

Both images run as non-root on a distroless base and need no extra privileges.

## Install the addon chart

Every chart version is published as a release archive:

```text
https://github.com/laedeli/acquire/releases/download/chart-acquire-0.1.0/acquire-0.1.0.tgz
```

The same archive is `oci://ghcr.io/laedeli/charts/acquire`, version `0.1.0`.
The operator pulls charts anonymously, so the OCI reference works once that
package is public; the https archive works everywhere. Each release names the
archive's digest: set it as the chart digest to pin exactly that archive.

Before installing, add the two OIDC clients (see [Identity](#identity)) and
create acquire's database. The install asks for two secrets: the database URL
and the service client's secret.

### From the portal

> portal → settings → **addons** → **+** → chart reference → plan → inputs → **install**

Paste the https link as the chart reference. The plan shows the chart, both
workloads with their images, and every object the chart renders. Fill in the
inputs — secret ones are masked and never shown back — and install. Progress
follows both components; once they are ready, the portal registers the addon
from acquire's manifest (the app, its tiles, the **Request this** row and the
setup checklist) without an address to type.

### With zae

```bash
zae addon add https://github.com/laedeli/acquire/releases/download/chart-acquire-0.1.0/acquire-0.1.0.tgz \
  --name acquire \
  --set-secret database.url='postgres://acquire:…@db.example.org:5432/acquire?sslmode=require' \
  --set-secret oidc.serviceClientSecret='…' \
  --wait
```

`zae addon add` prints the plan and asks before it installs (`--yes` skips the
question). With the OCI reference, once the package is public:

```bash
zae addon add oci://ghcr.io/laedeli/charts/acquire --version 0.1.0 \
  --set-secret database.url=… --set-secret oidc.serviceClientSecret=…
```

### Values

The inputs, declared in the chart's `values.schema.json`:

| Value | Default | Sets |
|---|---|---|
| `database.url` | **required**, secret | `PG_URL` — acquire's Postgres, `postgres://user:password@host:5432/acquire?sslmode=require` |
| `oidc.serviceClientSecret` | **required**, secret | `ACQUIRE_SVC_CLIENT_SECRET` |
| `oidc.serviceClientId` | `laedeli-acquire-svc` | `ACQUIRE_SVC_CLIENT_ID`, and the gateway's `ALLOWED_CLIENTS` |
| `oidc.clientId` | `laedeli-acquire` | `ACQUIRE_OIDC_CLIENT_ID` |
| `oidc.adminRole` | `zaentrum-admin` | `ACQUIRE_ADMIN_ROLE` |
| `oidc.userRole` | `zaentrum-user` | `ACQUIRE_USER_ROLE` |
| `tmdb.apiKey` | optional, secret | `TMDB_API_KEY` — discovery is off without it |
| `config.key` | generated, secret | `ACQUIRE_CONFIG_KEY` (see [the config key](#the-config-key)) |
| `config.previousKey` | optional, secret | `ACQUIRE_CONFIG_KEY_PREVIOUS`, while rotating the key |
| `endpoints.deny` | empty | `ACQUIRE_ENDPOINT_DENY` (see [where configuration may point](#where-configuration-may-point)) |
| `endpoints.allowInternal` | `false` | `ACQUIRE_ENDPOINT_ALLOW_INTERNAL` |
| `downloadsRoot` | `/var/lib/katalog` | `ACQUIRE_DOWNLOADS_ROOT` |

Secret inputs are rendered into the Secret `acquire-secrets` and reach the
containers through `secretKeyRef` only: no value appears in a Deployment.

Tuning:

| Value | Default |
|---|---|
| `acquire.image.repository`, `acquire.image.tag` | `ghcr.io/laedeli/acquire`, the chart's `appVersion` |
| `downloadGateway.image.repository`, `downloadGateway.image.tag` | `ghcr.io/laedeli/download-gateway`, the gateway commit pinned for this chart version |
| `acquire.image.pullPolicy`, `downloadGateway.image.pullPolicy` | the cluster default |
| `acquire.resources` | requests `20m` / `48Mi`, limits `500m` / `256Mi` |
| `downloadGateway.resources` | requests `20m` / `32Mi`, limits `200m` / `128Mi` |
| `downloadGateway.pollInterval` | `5s` (`POLL_INTERVAL`) |
| `urls.downloadGateway` | `http://download-gateway` |
| `urls.katalog` | `http://katalog-api` |
| `urls.katalogManager` | `http://katalog-manager-api` |

A published chart's `appVersion` is the git commit of this repository it was
published from, and acquire's image is tagged with that same commit, so a
chart version always runs the same code. The gateway is built in its own
repository, so the chart pins the gateway's tag instead.

### What comes from the platform

The operator sets the chart's `zaentrum` values, and the chart reads the
platform from them and nowhere else:

| Platform value | In the chart |
|---|---|
| `zaentrum.issuer` | `OIDC_ISSUER` on both; acquire's token endpoint `<issuer>/protocol/openid-connect/token` |
| `zaentrum.issuerHostAliasIP` | a host alias resolving the issuer's host to that address, on both |
| `zaentrum.events.brokers`, `zaentrum.events.topicPrefix` | `KAFKA_BROKERS` and `KAFKA_TOPIC_PREFIX` on both; acquire's consumer group is the prefix without its trailing dot, then `-acquire` |
| `zaentrum.events.tlsSecret` | mounted at `/etc/kafka-cert`: acquire reads it as `KAFKA_CERT_DIR`, the gateway as `KAFKA_TLS_CERT`, `KAFKA_TLS_KEY` and `KAFKA_TLS_CA` |
| `zaentrum.media.claimName` | mounted read-only into acquire at `/var/lib/katalog` |
| `zaentrum.imagePullSecrets` | the pull secrets of both pods |
| `zaentrum.partOf` | the `app.kubernetes.io/part-of` label |

The chart renders two Deployments, two Services and one Secret — no Route,
Ingress or RBAC. Both pods run non-root with the runtime's default seccomp
profile, no privilege escalation, no capabilities and no service account token.

### GitOps

Committing the addon to a deploy repository is equally valid: a
`ZaentrumAddon` in the platform namespace, with the secret inputs in a values
Secret next to it. Create that Secret with your usual secret tooling rather
than committing the values in plain text.

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: zaentrum-addon-acquire-values
  namespace: zaentrum            # the platform namespace
type: Opaque
stringData:
  database.url: postgres://acquire:CHANGE-ME@db.example.org:5432/acquire?sslmode=require
  oidc.serviceClientSecret: CHANGE-ME
---
apiVersion: zaentrum.io/v1alpha1
kind: ZaentrumAddon
metadata:
  name: acquire
  namespace: zaentrum
spec:
  chart:
    ref: https://github.com/laedeli/acquire/releases/download/chart-acquire-0.1.0/acquire-0.1.0.tgz
    # digest: sha256:…           # optional: the archive digest the release names
  values:
    endpoints:
      deny: .other-namespace.svc,files.example.org
  valuesFrom:
    - kind: Secret
      name: zaentrum-addon-acquire-values
      valuesKey: database.url
      targetPath: database.url
    - kind: Secret
      name: zaentrum-addon-acquire-values
      valuesKey: oidc.serviceClientSecret
      targetPath: oidc.serviceClientSecret
```

`suspend: true` in the spec only plans: the status shows the plan and nothing
is applied. The values Secret above stays yours. Labelled
`zaentrum.io/addon: acquire`, it would belong to the addon and be removed with
it, unless it also carries `zaentrum.io/keep: "true"`.

### The config key

When no `config.key` is given, the operator generates one on the first install,
stores it in the Secret `zaentrum-addon-acquire-generated` and never generates
it again. That Secret belongs to the addon: **removing the addon deletes the
key**, and an install after that generates a new one that cannot open the
credentials stored before. To keep them across a remove and a new install, save
the key:

```bash
kubectl -n <platform namespace> get secret zaentrum-addon-acquire-generated \
  -o jsonpath='{.data.config\.key}' | base64 -d
```

and give it back as the secret input `config.key` — or set `config.key`
yourself from the start. To rotate it, set the new key as `config.key` and the
old one as `config.previousKey`, enter the credentials again, then clear
`config.previousKey` (see [ACQUIRE_CONFIG_KEY](#acquire_config_key)).

### Upgrading the chart

A new chart version is a new release archive (and a new OCI version). Point the
addon at it — **upgrade** on the addon's row in the portal, `spec.chart.ref` in
a deploy repository, or `zae addon upgrade acquire --version <version>` for the
OCI reference. The plan lists what changes, images included, before anything is
applied.

### Moving an existing deployment to the chart

If acquire already runs from manifests you applied:

1. Note the database URL and the `ACQUIRE_CONFIG_KEY` it runs with, and give
   both to the install (`database.url`, `config.key`). With a new key, the
   credentials acquire stored no longer open.
2. Delete the `acquire` and `download-gateway` Deployments and Services you
   applied. The operator never takes over objects it does not own: while they
   exist, the plan reports them as violations and nothing is installed.
3. Install the chart. The address stays `http://acquire`, so the portal keeps
   the addon's registration. Remove the old deployment Secrets once acquire runs.

## Identity

| Client | Type | Used for |
|---|---|---|
| `laedeli-acquire` | public (PKCE) | the standalone console login (`ACQUIRE_OIDC_CLIENT_ID`) |
| `laedeli-acquire-svc` | confidential (client credentials) | acquire's own calls: the gateway (downloads and client configuration) and ingest |

Realm roles: `zaentrum-user` may request, `zaentrum-admin` may grab, remove and
**configure** — every configuration route is admin-only. With the chart, the
client ids and the roles are inputs (`oidc.*`).

The gateway validates acquire's service token. Set on the gateway:

| Env | Value |
|---|---|
| `OIDC_ISSUER` | the realm issuer |
| `ALLOWED_CLIENTS` | `laedeli-acquire-svc` |

Only with both set does the gateway accept calls from that client alone **and**
offer its client configuration API; without them acquire cannot push download
clients and setup says so. The chart always sets both.

## Secrets

The chart renders its own Secret, `acquire-secrets`, from the secret inputs and
mounts the platform's event streaming certificate. Deploying the manifests
yourself, create:

| Secret | Key | Read as |
|---|---|---|
| `acquire-db` | `url` | `PG_URL` |
| `acquire-svc-oidc` | `client-secret` | `ACQUIRE_SVC_CLIENT_SECRET` |
| `acquire-config` | `key` | `ACQUIRE_CONFIG_KEY` |
| `katalog-tmdb` | `api-key` | `TMDB_API_KEY` (optional mount) |
| `kafka-mtls` | `user.crt`, `user.key`, `ca.crt` | created by the core deploy |

Nothing else: search source keys and download client passwords are **not**
deployment secrets. They are entered in acquire's console and stored in its
database, encrypted.

### ACQUIRE_CONFIG_KEY

The key that encrypts every credential acquire stores — search source API keys,
download client passwords and tokens, and the full release links grabs keep.
It is 32 random bytes, base64:

```bash
openssl rand -base64 32
```

- **Without it** acquire runs, but refuses to store a credential (`409`) and
  its setup checklist reports it. There is never a plaintext fallback.
- **Rotating:** set the new key as `ACQUIRE_CONFIG_KEY` and the old one as
  `ACQUIRE_CONFIG_KEY_PREVIOUS`. Stored values keep opening; everything written
  from then on is sealed with the new key. Re-enter the credentials, then drop
  the previous key.
- **Losing it** makes the stored credentials unreadable: setup shows which
  sources and clients are affected, and they have to be entered again. acquire
  pushes nothing to the gateway while a client secret cannot be opened, so a
  wrong key never empties a running gateway.

Keep it out of logs and backups of the database it protects.

## acquire's environment

The deployment-level settings. Everything an admin edits later — sources,
clients, the search and grab policy — is **not** here. The chart sets all of
these from its values and the platform's.

| Env | Purpose |
|---|---|
| `OIDC_ISSUER`, `ACQUIRE_OIDC_CLIENT_ID`, `ACQUIRE_ADMIN_ROLE`, `ACQUIRE_USER_ROLE` | auth |
| `PG_URL` | acquire's database |
| `DOWNLOAD_GATEWAY_URL` | e.g. `http://download-gateway` |
| `KATALOG_URL`, `KATALOG_MANAGER_URL` | catalog read and ingest |
| `OIDC_TOKEN_URL`, `ACQUIRE_SVC_CLIENT_ID`, `ACQUIRE_SVC_CLIENT_SECRET` | the service account |
| `KAFKA_BROKERS`, `KAFKA_CERT_DIR`, `KAFKA_TOPIC_PREFIX`, `KAFKA_GROUP_ID` | events |
| `ACQUIRE_DOWNLOADS_ROOT` | where finished downloads are visible to acquire when a client names no folder of its own |
| `ACQUIRE_CONFIG_KEY`, `ACQUIRE_CONFIG_KEY_PREVIOUS` | credential encryption (above) |
| `ACQUIRE_ENDPOINT_DENY` | hosts acquire must never be pointed at (below) |
| `ACQUIRE_ENDPOINT_ALLOW_INTERNAL` | `true` to allow cluster services in other namespaces |
| `POD_NAMESPACE` | acquire's own namespace (downward API); read from the service account otherwise |

`ACQUIRE_PREFER`, `ACQUIRE_STORAGE_FLOOR_GB` and `ACQUIRE_MAX_CONCURRENT_GRABS`
only seed the search and grab settings on the first boot; the console owns them
from then on. See [the acquire service](./acquire.md#configuration) for the
full list.

### Where configuration may point

Configuration makes acquire connect to whatever address an admin types, from
inside the cluster. Every search source and download client address — and
every link acquire fetches for them — goes through one policy:

- `http` and `https` only, with no credentials in the address;
- never a link-local or cloud metadata address (`169.254.0.0/16`, `fe80::/10`,
  `100.100.100.200`, `fd00:ec2::254`), checked on save and again on the
  resolved address at connect time;
- nothing listed in `ACQUIRE_ENDPOINT_DENY` (the chart's `endpoints.deny`) — a
  comma list of hostnames and domain suffixes
  (`.other-namespace.svc,files.example.org`). Use it to keep a test instance
  from ever reaching production endpoints;
- no `*.svc` / `*.svc.cluster.local` service in another namespace unless
  `ACQUIRE_ENDPOINT_ALLOW_INTERNAL=true` (`endpoints.allowInternal`). That
  includes the short form the pod's DNS search list completes
  (`gateway.other-namespace`), which acquire looks up on save and again before
  it connects. acquire's own namespace and private (RFC 1918) addresses stay
  allowed — so this is a rule about names, not a network boundary: a service's
  cluster IP typed as a number is reachable like any private address. Fence
  acquire's egress with a NetworkPolicy when that matters;
- responses are size-capped, and a redirect may not switch scheme.

## Storage paths

A download client reports finished files as **it** sees them. For each client
acquire keeps two folders: the save folder *as the client sees it* (sent to the
client with every add) and the same folder *as acquire sees it*. Mount the
storage into acquire read-only so the second path exists; free-space checks run
against it too. The chart mounts the platform's media claim at
`/var/lib/katalog`.

## Install manifests you deployed yourself

Without the chart, deploy both components through your own channel, then:

> portal → settings → **addons** → address `http://acquire` → **check** → **install**

portal-api reads acquire's manifest and records:

| Declared | Result |
|---|---|
| `components` | `acquire` (primary) and `download-gateway` (required), matched by workload name — **check** shows a gateway you forgot to deploy as *not deployed* |
| `setup` | the checklist from `GET /api/setup`: **search sources** and **download clients** (required), **search and grab** (optional), each with a **configure** link into acquire's console |
| `ui` | the `acquire` app and launchpad section, its tiles (requests, downloads, search, search sources, clients, quality profiles) and the **Request this** row in the `search.empty` slot |

acquire writes nothing to the portal and needs no credential for this. The
portal never sees a configuration value — it shows the checklist states and
links to where they are edited. A chart install records the same, on its own.

## Configure it

Right after install the checklist reads *needs setup*. Follow its
**configure** links:

1. **Download clients** — add each external download client: type, address,
   credentials, the protocols it handles, and the two folders. acquire pushes
   the set to the gateway immediately and whenever the gateway restarts.
2. **Search sources** — add each newznab or torznab endpoint with its API key
   and categories; acquire reads its caps on save. See
   [Search sources & NZB-first](./indexers-and-nzb.md).
3. **Search and grab** — which protocol is searched first, the free-space floor
   and how many downloads may run at once.

A manual search answers within 50 seconds. Whatever sits between the browser
and acquire must allow at least a minute per request.

## Verifying

- For a chart install, `zae addon status acquire` (or the addon's row in the
  portal) reads *Ready* with both components ready.
- `GET /api/setup` on acquire (or the portal's checklist) reads `ready`.
- `GET /api/v1/config/clients` on the gateway reports the same revision the
  clients tab shows.
- `GET /api/health/system` has no failing `sources` or `clients` check.
- A request → **find & grab** moves `pending → downloading` with a detail line
  naming the source and the client, then `packaging → fulfilled`, and plays.

## Upgrading from an earlier deployment

Earlier deployments ran the download clients and an indexer aggregator next to
acquire and configured them through environment variables and secrets.

1. Deploy the new images. Remove `INDEXER_URL` / `INDEXER_API_KEY` from acquire
   (acquire logs a line while they are still set) and add
   `ACQUIRE_CONFIG_KEY`. Add `OIDC_ISSUER` and `ALLOWED_CLIENTS` to the gateway.
2. Add the download clients and search sources in the console. The first client
   of each type is named after the type (`nzbget`, `qbittorrent`), so downloads
   and grabs recorded before keep resolving.
3. Remove the third-party Deployments, Services, service accounts and their
   secrets from your overlay — acquire no longer needs them in the namespace.
   Remove the client variables from the gateway too: acquire routes grabs only
   to the clients it stores, and an environment client would block a stored
   client of the same id.
4. **Refresh** the addon in the portal so it learns the component group and the
   setup checklist.

## Uninstalling

Installed as a chart:

1. Settings → addons → **remove**, or `zae addon remove acquire`. Both
   Deployments, both Services and `acquire-secrets` go with the addon, and so
   does a generated config key (see [the config key](#the-config-key)). Tick
   *keep values* (`--keep-values`) to keep the values Secret with the inputs you
   gave.
2. Drop acquire's database when you no longer need the data. All of its
   configuration lives there and nowhere else.

Deployed through your own channel:

1. Settings → addons → **remove**. The app, tiles, slot row and section go; the
   answer lists the addon's workloads that are still deployed.
2. Delete those workloads through the channel that deployed them — the
   `zaentrum.io/addon=acquire` label finds them.
3. Drop acquire's database when you no longer need the data.

The core is a neutral catalog and player again — exactly as before the addon.
