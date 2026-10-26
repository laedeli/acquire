# Deploying the addon

acquire is **two containers**: `acquire` itself and `download-gateway`. The
sources that are searched are **not** part of it — they are endpoints you use,
and an admin tells acquire where they are in its console after the install.
The programs that download can be either: endpoints you already run, added in
the console the same way, or the two download clients the chart runs itself,
qBittorrent and NZBGet, each optional (see
[download clients and the VPN](#download-clients-and-the-vpn)). Those clients
are the only third-party pieces the addon deploys, and acquire needs no setup
for them.

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
        QB["Deployment/Service qbittorrent · nzbget<br/><i>optional: clients the chart runs</i>"]
        PAPI["portal-api<br/>(core)"]
    end
    DB[("acquire's Postgres<br/>requests · sources · clients · settings")]
    SRC["search sources<br/><i>newznab / torznab endpoints</i>"]
    DC["download clients<br/><i>external endpoints</i>"]
    VPN["platform VPN<br/><i>SOCKS5 proxy</i>"]
    DL[("acquire-downloads<br/><i>the clients' volume</i>")]
    MEDIA[("media storage")]

    PAPI -.->|"GET /.well-known/zaentrum-capability.json<br/>+ console proxy"| ACQ
    ACQ --> DB
    ACQ -->|"search, fetch release files"| SRC
    ACQ -->|"PUT /api/v1/config/clients<br/>POST /api/v1/downloads"| GW
    GW --> DC
    GW --> QB
    QB -.->|"vpn: true"| VPN
    DC --> MEDIA
    QB -->|"download, repair, unpack"| DL
    ACQ -.->|"reads finished downloads"| DL
    ACQ -->|"copies them into .work/incoming"| MEDIA
```

## Prerequisites

- A running **zaentrum platform** with the ingest seam (katalog-manager
  `POST /api/ingest`) and the portal's addon install. Installing the chart
  needs the operator's addon charts (the `ZaentrumAddon` resource).
- For **episodes**, the catalog's identity contracts (ADR-0012 of the
  platform): **ingest by identity** in katalog-manager, which takes an episode
  by its series' TMDB, TVDB or IMDb id, and **holdings by identity** in
  katalog-api (`GET /api/v1/holdings`). Without them acquire still runs:
  movies are handed over as before, by title; a finished episode download is
  copied and kept, its episodes **waiting for the catalog**, and handed over
  once the catalog takes it; and since nothing says what the library holds,
  the missing tab lists nothing of a title and nothing is grabbed on its own
  (see [what the library holds](./acquire.md#what-the-library-holds)).
- For **following what is requested**, a **TMDB API key** (`tmdb.apiKey`): a
  requested series' episodes and a requested movie's release dates are read
  from TMDB, and read again on the clock. Without it a request still follows
  its title, and says that nothing of it can be read; discovery is off too.
- For **subtitles**, the catalog's **subtitle API** in katalog-manager
  (`GET /api/library/subtitles/languages`, `GET`, `POST` and `DELETE
  /api/library/items/{id}/subtitles`, open to the addon role) and an
  **OpenSubtitles.com** account with the key of an API consumer. Without the
  account, subtitles are off and the setup checklist says what is missing;
  against a catalog without that API, the console says so (see
  [subtitles](./acquire.md#subtitles)).
- The platform's **event streaming**: brokers over mTLS with the platform's
  client certificate, or plaintext brokers such as a bundled one. The chart
  follows whichever the platform has.
- An **OIDC realm** you can add acquire's service client to (see
  [Identity](#identity)).
- A **Postgres database** for acquire.
- The **media storage** the download clients write to, mountable into acquire
  so it can find finished files. The chart mounts the platform's media claim.
- For the download clients the chart runs: a **volume for them to work on**
  (the claim `acquire-downloads`, see
  [download clients and the VPN](#download-clients-and-the-vpn)); the
  library's **v2 layout**, whose arrivals folder `.work/incoming` is where
  acquire copies their finished downloads (the catalog's ingest takes files
  from there only); and, for qBittorrent through the VPN, a platform **VPN**
  (`zaentrum.vpn.proxy`).

## The components

| Component | Role | Image | Port | Needs |
|---|---|---|---|---|
| `acquire` | primary | `ghcr.io/laedeli/acquire` | 8080 | Postgres, media storage (read-only, its arrivals writable when the chart runs clients), the downloads claim (read-only), the config key |
| `download-gateway` | required | `ghcr.io/laedeli/download-gateway` | 8080 | nothing stored — clients are pushed to it at runtime |
| `qbittorrent` | optional (`clients.qbittorrent.enabled`) | `ghcr.io/home-operations/qbittorrent` | 8080 | its configuration claim, its folder of the downloads claim, the platform VPN unless `vpn: false` |
| `nzbget` | optional (`clients.nzbget.enabled`) | `ghcr.io/home-operations/nzbget` | 6789 | its configuration claim, its folder of the downloads claim |

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

No Ingress or Route is needed, and none should be added. The console opens
in the portal, at `/portal/app/acquire`, on the portal's sign-in, and the
portal proxies its bundle and the API. acquire has no page or login of its
own: a browser that reaches it directly is sent to the portal (see
[where the console is](#where-the-console-is)).

The gateway workload acquire declares is the first label of the host in
`DOWNLOAD_GATEWAY_URL` (`http://download-gateway` → `download-gateway`); if
you rename the gateway, keep the two in step.

acquire's and the gateway's images run as non-root on a distroless base and
need no extra privileges; the client images run as non-root too (see
[the images](#the-images)).

## Install the addon chart

> **Install it under the name `acquire`.** The portal registers the addon from
> the manifest acquire serves, whose key is `acquire`, and only when the addon
> is installed under that name. The chart refuses any other name, and the plan
> reports it as
> `template error at acquire/templates/release-name-must-be-acquire.yaml:<line>`:
> the operator shows where a chart failed, never its message, so the file's name
> is the message.

Every chart version is published as a release archive:

```text
https://github.com/laedeli/acquire/releases/download/chart-acquire-0.4.2/acquire-0.4.2.tgz
```

The same archive is `oci://ghcr.io/laedeli/charts/acquire`, version `0.4.2`.
The operator pulls charts anonymously, so the OCI reference works once that
package is public; the https archive works everywhere. Each release names the
archive's digest: set it as the chart digest to pin exactly that archive.

Before installing, add the OIDC service client (see [Identity](#identity)) and
create acquire's database. The install asks for two secrets: the database URL
and the service client's secret.

### From the portal

> portal → settings → **addons** → **+** → chart reference → plan → inputs → **install**

Paste the https link as the chart reference and keep the name `acquire`. The
plan shows the chart, both workloads with their images, and every object the
chart renders. Fill in the inputs — secret ones are masked and never shown
back — and install. Progress follows both components; once they are ready, the
portal registers the addon from acquire's manifest (the app, its tiles, the
**Request this** row and the setup checklist) without an address to type. The
tiles open the console in place, in the portal.

### With zae

With `ZAE_TOKEN` holding an admin bearer for the instance:

```bash
zae addon add https://github.com/laedeli/acquire/releases/download/chart-acquire-0.4.2/acquire-0.4.2.tgz \
  --name acquire \
  --url https://<instance> \
  --set-secret database.url='postgres://acquire:…@db.example.org:5432/acquire?sslmode=require' \
  --set-secret oidc.serviceClientSecret='…' \
  --wait
```

`zae addon add` prints the plan and asks before it installs (`--yes` skips the
question). With the OCI reference, once the package is public:

```bash
zae addon add oci://ghcr.io/laedeli/charts/acquire --version 0.4.2 --name acquire \
  --url https://<instance> \
  --set-secret database.url=… --set-secret oidc.serviceClientSecret=…
```

### Values

The inputs, declared in the chart's `values.schema.json`:

| Value | Default | Sets |
|---|---|---|
| `database.url` | **required**, secret | `PG_URL` — acquire's Postgres, `postgres://user:password@host:5432/acquire?sslmode=require` |
| `oidc.serviceClientSecret` | **required**, secret | `ACQUIRE_SVC_CLIENT_SECRET` |
| `oidc.serviceClientId` | `laedeli-acquire-svc` | `ACQUIRE_SVC_CLIENT_ID`, and the gateway's `ALLOWED_CLIENTS` |
| `oidc.adminRole` | `zaentrum-admin` | `ACQUIRE_ADMIN_ROLE` |
| `oidc.userRole` | `zaentrum-user` | `ACQUIRE_USER_ROLE` |
| `tmdb.apiKey` | optional, secret | `TMDB_API_KEY` — discovery, and what a request follows (a series' episodes, a movie's release dates); both are off without it |
| `config.key` | generated, secret | `ACQUIRE_CONFIG_KEY` (see [the config key](#the-config-key)) |
| `config.previousKey` | optional, secret | `ACQUIRE_CONFIG_KEY_PREVIOUS`, while rotating the key |
| `subtitles.enabled` | `true` | `ACQUIRE_SUBTITLES` — [subtitles](./acquire.md#subtitles) in the configured languages; nothing is searched for without the account below |
| `subtitles.languages` | empty | `ACQUIRE_SUBTITLE_LANGUAGES`, language tags (`[en, de, zh-Hans]`); empty takes the platform's (its `subtitles.languages`, else the packager's language whitelist) |
| `subtitles.dailyBudget` | `18` | `ACQUIRE_SUBTITLE_DAILY_BUDGET` — the automatic downloads a UTC day; a free account allows 20, and the rest of the account's downloads are for versions you pick in the console |
| `subtitles.opensubtitles.username`, `.password`, `.apiKey` | optional, secret | `OPENSUBTITLES_USERNAME`, `OPENSUBTITLES_PASSWORD`, `OPENSUBTITLES_API_KEY` — the OpenSubtitles.com account and its API consumer's key; without all three, subtitles are off |
| `endpoints.deny` | empty | `ACQUIRE_ENDPOINT_DENY` (see [where configuration may point](#where-configuration-may-point)) |
| `endpoints.allowInternal` | `false` | `ACQUIRE_ENDPOINT_ALLOW_INTERNAL` |
| `downloadsRoot` | `/var/lib/katalog` | `ACQUIRE_DOWNLOADS_ROOT` |
| `clients.qbittorrent.*`, `clients.nzbget.*` | off | the download clients the chart runs (see [download clients and the VPN](#download-clients-and-the-vpn)); their passwords are secret and generated |

Secret inputs are rendered into the Secret `acquire-secrets` and reach the
containers through `secretKeyRef` only: no value appears in a Deployment.

Tuning:

| Value | Default |
|---|---|
| `acquire.image.repository`, `acquire.image.tag` | `ghcr.io/laedeli/acquire`, the chart's `appVersion` |
| `downloadGateway.image.repository`, `downloadGateway.image.tag` | `ghcr.io/laedeli/download-gateway`, the gateway commit pinned for this chart version |
| `acquire.image.pullPolicy`, `downloadGateway.image.pullPolicy` | the cluster default |
| `acquire.resources` | requests `20m` / `48Mi`, limits `500m` / `256Mi` |
| `downloadGateway.resources` | requests `20m` / `32Mi`, limits `200m` / `256Mi` (large adds hold up to about 140 MiB in flight) |
| `downloadGateway.pollInterval` | `5s` (`POLL_INTERVAL`) |
| `clients.<client>.image.repository`, `.tag`, `.pullPolicy` | the pinned client images (see [the images](#the-images)) |
| `clients.qbittorrent.resources` | requests `50m` / `256Mi`, limits `2` / `2Gi` |
| `clients.nzbget.resources` | requests `50m` / `128Mi`, limits `2` / `1Gi` (repair and unpacking are CPU-heavy) |
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
| `zaentrum.hostname` | `ACQUIRE_PLATFORM_URL`, the platform's address `https://<hostname>` (`http` when the issuer is plain http on that host): where acquire sends a browser that asks it for a page, to `/portal/app/acquire` there. Empty sends it to `/portal/` on the host it asked |
| `zaentrum.issuer` | `OIDC_ISSUER` on both; acquire's token endpoint `<issuer>/protocol/openid-connect/token` |
| `zaentrum.issuerHostAliasIP` | a host alias resolving the issuer's host to that address, on both |
| `zaentrum.events.brokers`, `zaentrum.events.topicPrefix` | `KAFKA_BROKERS` and `KAFKA_TOPIC_PREFIX` on both; acquire's consumer group is the prefix without its trailing dot, then `-acquire`, and a second group, that id followed by `-catalog`, reads the catalog's `catalog.item.discovered` and `catalog.item.removed` |
| `zaentrum.events.tlsSecret` | mounted at `/etc/kafka-cert`: acquire reads it as `KAFKA_CERT_DIR`, the gateway as `KAFKA_TLS_CERT`, `KAFKA_TLS_KEY` and `KAFKA_TLS_CA`. Empty means plaintext brokers: nothing is mounted, acquire gets an empty `KAFKA_CERT_DIR` and the gateway no TLS files |
| `zaentrum.media.claimName` | mounted read-only into acquire at `/var/lib/katalog`; when the chart runs download clients, its arrivals `.work/incoming` writable on top, for the copies. No client mounts it |
| `zaentrum.vpn.proxy` | the platform VPN's SOCKS5 proxy, `socks5h://host:port`, or empty without a VPN: qBittorrent goes through it with `vpn: true`, which needs it |
| `zaentrum.imagePullSecrets` | the pull secrets of both pods |
| `zaentrum.partOf` | the `app.kubernetes.io/part-of` label |

The chart renders two Deployments, two Services and one Secret, and for each
download client it runs a Deployment, a Service and a PersistentVolumeClaim,
with the downloads claim and one ConfigMap for their scripts — no Route,
Ingress or RBAC. Every pod runs
non-root with the runtime's default seccomp profile, no privilege escalation,
no capabilities and no service account token.

### GitOps

Committing the addon to a deploy repository is equally valid: a
`ZaentrumAddon` named `acquire` in the platform namespace, with the secret
inputs in a values Secret next to it. Create that Secret with your usual secret
tooling rather than committing the values in plain text.

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: zaentrum-addon-acquire-values   # must start with zaentrum-addon-acquire-
  namespace: zaentrum                   # the platform namespace
  labels:
    zaentrum.io/addon: acquire          # required, or the operator will not read it
type: Opaque
stringData:
  database.url: postgres://acquire:CHANGE-ME@db.example.org:5432/acquire?sslmode=require
  oidc.serviceClientSecret: CHANGE-ME
  # subtitles (optional): the OpenSubtitles.com account and its API key
  subtitles.opensubtitles.username: CHANGE-ME
  subtitles.opensubtitles.password: CHANGE-ME
  subtitles.opensubtitles.apiKey: CHANGE-ME
---
apiVersion: zaentrum.io/v1alpha1
kind: ZaentrumAddon
metadata:
  name: acquire                         # the only name the addon registers under
  namespace: zaentrum
spec:
  chart:
    ref: https://github.com/laedeli/acquire/releases/download/chart-acquire-0.6.2/acquire-0.6.2.tgz
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
    - kind: Secret
      name: zaentrum-addon-acquire-values
      valuesKey: subtitles.opensubtitles.username
      targetPath: subtitles.opensubtitles.username
    - kind: Secret
      name: zaentrum-addon-acquire-values
      valuesKey: subtitles.opensubtitles.password
      targetPath: subtitles.opensubtitles.password
    - kind: Secret
      name: zaentrum-addon-acquire-values
      valuesKey: subtitles.opensubtitles.apiKey
      targetPath: subtitles.opensubtitles.apiKey
```

The operator reads `valuesFrom` only from Secrets and ConfigMaps whose name
starts with `zaentrum-addon-acquire-` **and** that carry the label
`zaentrum.io/addon: acquire`; it refuses any other. The label also makes the
Secret part of the addon, removed with it — add `zaentrum.io/keep: "true"` to
keep it. `suspend: true` in the spec only plans: the status shows the plan and
nothing is applied.

### The config key

When no `config.key` is given, the operator generates one on the first install,
stores it in the Secret `zaentrum-addon-acquire-generated` and never generates
it again. Removing the addon with **keep values** (the portal's checkbox, or
`zae addon remove acquire --url https://<instance> --keep-values`) keeps that
Secret together with the values Secret, so a later install reuses the key and
the stored credentials keep opening. **Removing it without keep values deletes
the key**: an install after that generates a new one that cannot open the
credentials stored before.

A copy of the key survives any removal:

```bash
kubectl -n <platform namespace> get secret zaentrum-addon-acquire-generated \
  -o jsonpath='{.data.config\.key}' | base64 -d
```

Give it back as the secret input `config.key` when needed, or set `config.key`
yourself from the start. To rotate it, set the new key as `config.key` and the
old one as `config.previousKey`, enter the credentials again, then clear
`config.previousKey` (see [ACQUIRE_CONFIG_KEY](#acquire_config_key)).

### Upgrading the chart

A new chart version is a new release archive (and a new OCI version). Point the
addon at it — **upgrade** on the addon's row in the portal, `spec.chart.ref` in
a deploy repository, or zae:

```bash
zae addon upgrade acquire --url https://<instance> \
  --chart https://github.com/laedeli/acquire/releases/download/chart-acquire-<version>/acquire-<version>.tgz
zae addon upgrade acquire --url https://<instance> --version <version>   # the OCI reference
```

The plan lists what changes, images included, before anything is applied.

**To 0.3.0.** acquire has no standalone console any more, and no login of its
own: the console opens only in the portal, and `/` on acquire sends a browser
there (see [where the console is](#where-the-console-is)). The public client
`laedeli-acquire` is no longer used: remove it from the realm. An install that
still sets `oidc.clientId` renders, and the value is ignored. A Route or
Ingress you gave acquire for the old console now only leads to the portal:
remove it too.

**To 0.4.0.** acquire grabs the episodes and movies it tracks, hands episodes
to the catalog by identity and learns what the library holds from it: see
[upgrading to episode grabs](#upgrading-to-episode-grabs) for what that asks
of the platform. Nothing is grabbed on its own after the upgrade: the backlog
sweep only searches until an admin switches it on in settings.

**To 0.4.1.** A web release named Dolby Vision without an HDR10 base layer
ranks lower, as the platform cannot convert Dolby Vision profile 5 (see
[the release picker](./acquire.md#the-console)); nothing else changes.

**To 0.4.2.** The console's missing tab is a worklist on design system 0.3.0:
tabs with counts, one message for what cannot be checked, filters, episodes
grouped by series and season, and one emphasized action for what is
selected. Copies into the arrivals are synced every 64 MiB on their way, and
the temporary file of a copy killed mid-way is removed once it is two hours
old (see [from download to library](#from-download-to-library)). The portal
should run design system 0.3.0 too: the console's stylesheet carries the
design system's tokens, which a portal on an older version would otherwise
take on.

**To 0.5.0.** acquire downloads subtitles from OpenSubtitles.com in the
configured languages: new arrivals first, then the rest of the library, up to
`subtitles.dailyBudget` automatic downloads a day (see
[subtitles](./acquire.md#subtitles)). It needs the catalog's subtitle API
(katalog-manager's `/api/library/items/{id}/subtitles` and
`/api/library/subtitles/languages`) and an account: add `subtitles.opensubtitles.username`,
`.password` and `.apiKey` to the values Secret (see [GitOps](#gitops)) — without
them the chart renders, acquire starts, and subtitles are off, saying so in the
console and the setup checklist. Migration `080` (the titles, their wants and
the downloads) is applied on start, and the schedule `subtitles-sweep` (every
fifteen minutes) registered. The console has a **subtitles** tab, and its
settings a subtitles section. Every download counts against the account's daily
quota, which a free account has 20 of.

**To 0.5.1.** Nothing to set. The console reads every subtitle name, number
and setting it shows — the source's name, the languages and where they are
set, the limits — from the configuration and the account when it shows them.
The API's subtitle overview, its searches and every version found carry the
source's name (`source`).

**To 0.5.2.** Nothing to set. The subtitles store refused every title whose
file is over 2 GiB when it saved what the catalog said of it (`size_bytes`
was sent as a 32-bit number), so those titles were never searched; TMDB ids
over 2^31 had the same trap. No download was spent on them, and they are
searched on the next pass after the update, without a step of yours.

**To 0.6.0.** A request for a series or a movie follows its title (see
[a request that follows its title](./acquire.md#a-request-that-follows-its-title)):
a series request is no longer failed when a download of it completes — it
stays open while the series runs, its episodes tracked and grabbed one by one
—, and a movie request is its movie's target, searched from its release on.
TMDB is read again on the clock for what runs, so the episodes it announces
later become targets, and the sources' newest releases are read and matched
to what is wanted. Migration `042` (follows and their approval, what TMDB was
read for, the newest releases' cursors) is applied on start, and two
schedules are registered: `inventory-refresh` (every six hours) and
`rss-sync` (every fifteen minutes) — see [schedules](./acquire.md#schedules).

- **Nothing is grabbed on its own that was not before.** The new switch
  **new episodes and requests** (`search.autoGrabNew`) is off after the
  upgrade, and so is `search.autoGrab` unless you had switched it on: the
  sweep searches as before, and with both off no source's newest releases are
  read at all. To have new episodes and what is requested arrive by
  themselves, switch it on in settings → search and grab; the old backlog
  stays with **everything missing** (`search.autoGrab`), which keeps its
  meaning. Either way an automatic grab now waits, by TVmaze's air times, an
  hour after a broadcast ends — its start and runtime — or after an episode
  streams (`search.airDelayAfterAirstampMinutes`, 60), or
  `search.airDelayHours` (30) after its air date when TVmaze gives no hour —
  TMDB dates the day, not the hour —; it takes no release published before a
  broadcast ended, more than 30 minutes before an episode streams, or before
  the episode's day began where the series comes from (03:00 in New York of
  a series streamed from the United States) — skipped, never blocked: a person
  may still pick it —; it takes nothing of a series whose air times TVmaze has
  not given yet; and it leaves to the missing tab a movie TMDB dates in
  cinemas only, and a series whose releases name no season and episode
  acquire can tell: one the import says is anime or daily, one TMDB files as
  animation from Japan or as a talk or news show.
- **TVmaze.** acquire now asks TVmaze's public schedule (`api.tvmaze.com`,
  HTTPS, no key) how a series airs — broadcast or streamed — and when each
  episode airs and how long it runs, sending a series' TVDB or IMDb id and
  nothing else, at under 20 calls in 10 seconds. The first `inventory-refresh`
  runs within a minute of the upgrade: after its reads of TMDB, it
  looks up the air times of every tracked series, whatever TMDB's budget —
  the ones that run first, two calls each, at most 2,000 calls a run: every
  series of a library of up to 1,000 in that first run, about 15 minutes for
  800 —; after that a run reads the series that run again, the others
  monthly. Until a series' air
  times are read, nothing of it is grabbed on its own: settings → search and
  grab says how many series that run are not read yet, and switching
  **new episodes and requests** or **everything missing** on before then asks
  you to confirm it. A series TVmaze does not know is told by its air date.
  Without a way out to TVmaze, switch **exact air times** off in settings →
  search and grab — then every episode is told by its air date, and nothing
  waits for TVmaze —; with it on, nothing of a series would be grabbed on its
  own.
- **Requests now wait for an admin.** A request someone who is not an admin
  makes awaits an admin's approval (`requests.autoApprove`, off), and changes
  nothing until an admin approves it in the requests tab: a title acquire
  tracks stays as it is, one it does not is only read from TMDB, nothing of
  it searched or grabbed. An admin's own request is approved at once. Switch
  on **approve requests** in settings → requests to approve everyone's at
  once, as requests were before.
- **The sources' allowance.** With a switch on, every source is read every
  fifteen minutes — its TV feed and its movie feed, each only while
  something of its kind can be grabbed, or one search over both for a source
  that offers neither —: one query a feed, up to three when a feed moved
  further than a page since its last read. That is 96 queries a day per
  source and feed (192 with both), and at most three times that on a day
  busy enough that every read needs three pages. No feed is read while
  nothing could be grabbed: every download slot taken, no room for a
  download, the gateway out of reach. Set a source's queries per day if its
  allowance is tighter: the feeds stop at 70 % of it and leave the rest to
  the sweep and to your searches, and a source at its limit is not asked.
- **TMDB.** The first runs read every tracked series once — a few thousand
  calls for a large library, at most 300 a run, four runs a day, so it takes
  a few days —, the series with an episode aired in the last 14 days or
  airing in the next 7 first: until a series is read, its origin country is
  not known, and an episode dated to the day alone begins at midnight where
  TVmaze says its network or service is, else at midnight UTC. After that a
  run reads only what runs, the ended series weekly. It needs `tmdb.apiKey`.
  TVmaze's air times do not wait for it (above).
- **Requests from before.** A series or movie request made before 0.6.0
  follows nothing until it is asked for again (discover → request), which
  answers the request there is and makes it follow its title.
- **Rolling back to 0.5.x.** Switch **new episodes and requests** and
  **everything missing** off in settings → search and grab before you roll
  back. 0.5.x knows nothing of follows, approvals, air times or the air
  delay: with `search.autoGrab` on, its sweep would grab the episodes a
  follow added as soon as they air, and what 0.6.0 leaves to the missing tab
  — a movie in cinemas only, an anime, a daily show — too.
  Migration `042` only adds, so nothing is lost; 0.5.x leaves the new columns
  and the `rss_cursors` table alone.

**To 0.6.1.** Nothing to set; both grab switches can stay as they are.
Migration `043` only adds columns. Exact air times are read by new rules:

- **An episode moved.** When TVmaze dates an episode more than a day after
  TMDB — TMDB has not caught up with a move —, it is told by TVmaze's day and
  hour, for what is refused as published too early and for when it is
  grabbed; when TVmaze dates it more than a day earlier, by TMDB's, the later.
  An episode the two date days apart and cannot tell the same is held — not
  grabbed on its own, saying so — until they agree. TMDB moving an episode
  holds it until TVmaze is read again, at once.
- **An episode TVmaze dates nowhere.** One TMDB dates in the last 30 days or
  to come that TVmaze lists with no date (postponed), lists no more (pulled),
  or never listed is held until TVmaze dates it: TMDB's day may be the one it
  was moved from.
- **A broadcast without an hour.** An episode TVmaze dates to the day alone is
  told by the usual hour of its season's broadcasts (else the show's
  schedule); without one, a recent one is held until it has an air time.
- **TVmaze refusing.** A `403` — any 4xx but `404` — counts as TVmaze not
  answering: the series stays unread and is tried again, and grabbing on its
  own cannot be switched on past it without confirming. An answer that cannot
  be read leaves its series unread too. One show TVmaze fails for no longer
  keeps the others from being read: a run asks it no more only after three
  series in a row, and a series it failed for is read after the ones not
  tried yet. The retry every 15 minutes also takes a series with an episode
  held after TMDB moved it. Settings → search and grab counts the episodes
  held, the series that ended TVmaze does not answer for, and warns when more
  than a third of the series that run are not known to TVmaze.
- **Streamed from the United States.** An episode dated to the day alone is
  taken from 21:00 in New York the evening before, not from 03:00 on the day;
  a release from 03:00 on is taken before one from the evening.

Every series read by 0.6.0 counts as not read by these rules until it is read
again, and nothing of it is grabbed on its own until then. acquire makes a
run two minutes after it starts, whenever the schedule comes round next: it
reads what TMDB has due, then the air times of every series — the ones that
run first, one call each, their TVmaze ids known, at most 2,000 calls a run:
every series of a library of up to 2,000 in that run, about 8 minutes for
800 —. Settings → search and grab says how many series that run are not
read again yet, and the confirmation of grabbing on its own says so of
them. A schedule an
upgrade adds is due at once — the clock runs it on its next tick, within ten
seconds, and every interval after —, one already there keeps its time:
0.6.1 adds none, so its first run of `inventory-refresh` is the one two
minutes after the start, and the schedule's own runs come round as before.

**To 0.6.2.** Nothing to set, and no migration. The chart runs a newer
download gateway with it, one that describes a job its client has finished
(`GET /api/v1/downloads/{client}/{id}`, see
[the gateway's API](./download-gateway.md#http-api)).

NZBGet numbers its downloads from 1 again when it restarts with an empty
history — and acquire empties it, by releasing every download it imported.
acquire took a client's job id as one download's for ever, so a new download
that got the id of an old one was taken for the old one:

- its progress went to the old download's row, and the downloads tab showed
  the old release's title;
- it did not count against **downloads at once**, so one sweep could start
  more downloads than the cap allows — eight under a cap of three, in the case
  that showed it;
- its completed event found the old download's finished import and queued
  nothing, yet moved its episodes on to "downloaded; copying into the
  library", where they stayed: nothing copied them, the stale-grab sweep did
  not see them, and restarting the gateway does not bring the event back.
  Their files are still in the client's complete folder, and NZBGet still
  lists them in its history.

A download is now known by the grab it was handed over for, and a job id
names the newest grab handed to it. A new download on an id takes over the
old one's row and counts against the cap until a row of its own shows, so the
cap holds; the old download's finished import gives way to the new one's, and
the old download's late events change nothing. NZBGet's record of a download
is released only while the record under its id is that download, told by its
folder. qBittorrent's job ids — a new tag for every add — do not repeat.

The episodes left "downloaded; copying into the library" by a client restart
are imported once the new gateway is up: usually by the grab-recovery run
two minutes after acquire starts, at the latest by the next hourly
`grab-recovery` run — the run asks the client, through the gateway, so it
needs the gateway the chart pins. It asks about every grab that is downloaded
without an import of its own, or that was handed over and gave no word for a
quarter of an hour while the gateway no longer tracks its job, and settles
one that ended as its event would have: one that completed is imported from
the files the client reports, one that failed fails, its release blocked — at
most 25 a run, with one line in the log (`acquire grab-recovery: … grab(s)
whose download may have finished unseen: …`). A grab whose job the client no
longer knows is left to the stale-grab sweep, which now asks the client too
before it gives a grab up — at most 25 a run, a client that does not answer
not asked again in the run —: it imports or fails what ended instead, and
keeps a grab whose client works on its job still, its row refreshed. A grab
it gives up ends its download row, and a row still running for a grab that
ended counts against the cap no more. The downloads a sweep started past the
cap are not undone; they finish and are imported like any other.

An NZB is now handed to NZBGet under its release's name, whatever the source
calls the file, and NZBGet names the job after it. Of an NZBGet job, what the
client says is taken for a grab's only when the job carries a name acquire
could have handed that grab's NZB over under: its release's, or — for an NZB
handed over before 0.6.2 that the source named nothing — the episode's, as
`Show Name S01E03`; compared in lower case, letters and digits only, so
`DD+5.1` and `DD5.1` are the same, and cut at 200 bytes as an NZB's name is.
A grab with no release to tell it by — a link handed over by hand — is not
checked, nor is another client's job: only NZBGet gives a job id again. A
job under the id named otherwise is another download — one whose add's answer
was lost, or one added by hand —, and the grab's own download is gone: nothing
of the other is imported for it, the grab is given up, and its episodes are
wanted again — nothing blocked —, saying the client holds another download
under that job id; the sweep grabs them again on its own.

acquire 0.6.2 against an older gateway — one deployed on its own — imports
nothing of what finished unseen, and its log says so on every run: give the
gateway the image the chart pins.

### Moving an existing deployment to the chart

If acquire already runs from manifests you applied and was installed in the
portal by address:

1. Note the database URL and the `ACQUIRE_CONFIG_KEY` it runs with, and give
   both to the install (`database.url`, `config.key`). With a new key, the
   credentials acquire stored no longer open.
2. Delete the `acquire` and `download-gateway` Deployments and Services you
   applied. The operator never takes over objects it does not own: while they
   exist, the plan reports them as violations and nothing is installed.
3. Installing from settings or zae: first remove the address install
   (settings → addons → **remove**). While an addon installed by address holds
   the key `acquire`, a chart install is refused with `409`. Install the chart
   afterwards; the portal registers the addon again once it is ready.
   Committing a `ZaentrumAddon` instead (see [GitOps](#gitops)) takes over the
   address install directly, with nothing to remove first.
4. Delete the old deployment Secrets once acquire runs from the chart.

## Download clients and the VPN

The chart can run the two download clients acquire drives next to it:
**qBittorrent** for torrents and **NZBGet** for usenet. Each is optional and
off by default. An enabled one needs nothing in acquire's console: acquire
stores it as a **managed** client at start, and the gateway drives it like any
other.

```yaml
clients:
  qbittorrent:
    enabled: true      # through the platform VPN, unless vpn: false
  nzbget:
    enabled: true      # never through the VPN
```

| Value | Default | Sets |
|---|---|---|
| `clients.<client>.enabled` | `false` | runs the client |
| `clients.qbittorrent.vpn` | `true` | qBittorrent through the platform VPN ([below](#through-the-platform-vpn)) |
| `clients.nzbget.vpn` | `false` | must stay `false`: NZBGet cannot use the VPN |
| `clients.<client>.username` | `acquire` | the Web UI and API login acquire uses |
| `clients.<client>.password` | generated, secret | its password |
| `clients.qbittorrent.seeding.ratio`, `.minutes` | `1`, `4320` (3 days) | the seeding goal: a finished torrent seeds until it reaches either, then acquire removes it with its data; `-1` lifts a limit |
| `clients.<client>.config.size`, `.storageClassName` | `1Gi`, the cluster default | the client's configuration claim |
| `clients.downloads.size` | `300Gi` | the downloads claim both clients work on ([below](#the-downloads-volume)) |
| `clients.downloads.storageClassName` | the cluster default | its storage class (a class for local volumes suits the clients best) |
| `clients.downloads.tolerations` | none | the taints of the volume's node, which acquire and both clients tolerate (a node kept for GPU work, say) |
| `clients.downloads.volumeName` | empty | a PV to bind by name; leave it empty under the operator, which refuses it |
| `clients.downloads.reserve` | a tenth of the size | kept free on the volume ([room on it](#room-on-the-downloads-volume)) |

Each enabled client is a Deployment and a Service named after it (`qbittorrent`
on port 8080, `nzbget` on 6789), a configuration claim `<client>-config`, and
an init container `configure`; NZBGet's pod also runs a `release` container
([below](#from-download-to-library)). The Service is a ClusterIP inside the
namespace, with no Route: the gateway reaches the client there, and so does a
port-forward. The Deployment recreates its pod rather than rolling it, so two
instances never share one configuration. Objects of these names made outside
the addon in its namespace — a `qbittorrent` Deployment or a
`qbittorrent-config` claim from an earlier setup — stop the install: the
operator never takes over what it does not own, so remove them first.

### The images

| Client | Image | Version |
|---|---|---|
| qBittorrent | `ghcr.io/home-operations/qbittorrent` | 5.2.4 (libtorrent 2.0), pinned by digest |
| NZBGet | `ghcr.io/home-operations/nzbget` | 26.3, pinned by digest |

Both are rootless builds, maintained with each upstream release: they start
as their image's non-root user under a tiny init (`catatonit`), with no step
that needs root. That is what the addon guardrails leave a pod: images whose
init system starts as root to switch users later do not run there. Like the
gateway's image, they name their user instead of numbering it, which is fine
where the platform assigns pods a user, as OpenShift does; a cluster that
assigns none cannot verify a named user as non-root and refuses the container.

### What the chart sets on every start

The clients rewrite their configuration files while they run, so the init
container, in the client's own image, sets what the chart owns before each
start (`files/configure-client.py` in the chart) and nothing else:

| | qBittorrent (`qBittorrent/qBittorrent.conf`) | NZBGet (`nzbget.conf`) |
|---|---|---|
| downloads | `Session\DefaultSavePath=/downloads/complete`, `Session\TempPath=/downloads/incomplete`, `Session\TempPathEnabled=true` | `DestDir=/downloads/complete`, `InterDir=/downloads/incomplete` |
| room | `Session\Preallocation=true` | `DiskSpace=` the reserve, in MB |
| seeding goal | `Session\GlobalMaxRatio`, `Session\GlobalMaxSeedingMinutes`, `Session\ShareLimitAction=Stop` | — |
| login | `WebUI\Username`, `WebUI\Password_PBKDF2`, `WebUI\LocalHostAuth=true`, `WebUI\AuthSubnetWhitelistEnabled=false` | `ControlUsername`, `ControlPassword`, `AuthorizedIP=` (none) |
| port | `WebUI\Address=*`, `WebUI\Port=8080` | `ControlIP=0.0.0.0`, `ControlPort=6789` |
| proxy | the VPN ([below](#through-the-platform-vpn)), else `Network\Proxy\Type=None` | — |

There is no way in without the login: not from a whitelisted subnet (the
qBittorrent image's own default lets the private ranges in) and not from
localhost, where a port-forward arrives. Everything else is yours to set in the
client: NZBGet's `MainDir` and its news servers, qBittorrent's speed limits
and queueing, and the rest.

### The downloads volume

The clients do not work on the media storage: that share serves playback and
stalls under the small random writes downloading, repair and unpacking make.
They work on a claim of their own, `acquire-downloads` (ReadWriteOnce,
`clients.downloads.size`), each in its folder of it — `qbittorrent/` and
`nzbget/`, mounted at `/downloads` — and everything happens there: incomplete
and complete downloads, NZBGet's repair and unpacking. No client mounts the
media claim at all.

On a cluster without a provisioner, the claim binds to a local PV made for it
on a node's data disk (the storage class of that PV, a PV whose `claimRef` names
`<platform namespace>/acquire-downloads`). Leave `volumeName` empty under the operator: the
claimRef binds the two, and the operator refuses a claim that names its
volume. acquire mounts the claim too, read-only, at
`/var/lib/acquire/downloads`. A local ReadWriteOnce volume is on one node, so
**the two clients and acquire all run on that node**: if it is down, so is
acquire. Pods on the volume set `fsGroupChangePolicy: OnRootMismatch`, so a
platform that gives pods a group for their volumes does not re-own every file
on each start. If that node is tainted, list its taints in `clients.downloads.tolerations`:
those three pods tolerate them, and no other pod of the chart does.

### From download to library

The gateway follows a download until it completes; then acquire takes over:

1. **Copy.** acquire copies the video, and the subtitle files the catalog
   pairs with it (named as the video, in its folder), from the client's
   `complete/` folder into the library's arrivals on the media storage:
   `.work/incoming/acquire/<request>/<client>-<job>/`. One file at a time, in
   one sequential pass, into a temporary dot name beside the target (which the
   arrivals scan passes over), synced to disk, checked — the same size, and
   the same SHA-256 over the first and last MiB — then renamed into place. Of
   the media claim, acquire writes that folder alone: it mounts
   `.work/incoming` read-write and keeps the rest read-only.
2. **Hand-over.** acquire hands the copy to the catalog the way it always
   handed downloads over: `POST /api/ingest` with the file's path under the
   arrivals, its title and year. The request moves on to *packaging*.
3. **Release.** acquire never moves or deletes a client's files itself; it
   asks the client. An NZB download is released right after its verified copy:
   acquire marks its NZBGet history record `acquire:release` through NZBGet's
   API, and the `release` container in NZBGet's pod deletes the download's
   folder and then the record. That container exists because NZBGet's API
   cannot delete a successful download's files — its history delete removes
   the folder of a failed or deleted download only. A torrent keeps seeding
   until its goal (`clients.qbittorrent.seeding`); qBittorrent then stops it,
   and acquire, which looks every ten minutes, removes it with its data through
   qBittorrent's API.

A copy or a hand-over that fails leaves the download where it is and is tried
again, a minute after the first failure and up to an hour apart; the request
says what failed and when acquire tries next. A good copy is not written
twice. A refusal by the catalog fails the request, as before. The work is kept
in acquire's database (`imports`), so a restart picks it up where it stopped.

A copy is synced to disk every 64 MiB on its way, not only at its end. Written
to network storage, the pages a copy has not written yet count against the
memory of acquire's container, and a file of several gigabytes copied while the
storage is busy would otherwise pile them up until the container is killed for
memory. A copy killed outright (its pod lost, or killed for memory) leaves its
temporary dot file behind and is copied again once its claim runs out (an
hour); acquire removes such a temporary file — named `.<file>.<token>.copying`,
under `.work/incoming/acquire/` alone — once nothing has written it for two
hours, looking at start and every hour after, and says so in its log.

An **episode grab** — a release grabbed for episodes acquire tracks, one, a
file of several or a season pack — goes the same way, file by file: its copies
land in `.work/incoming/acquire/<grab>/<client>-<job>/`, each file is handed
over by identity for the episodes its name holds (the series' ids, the season,
the episode, `episodeEnd` for a file of several), and files that hold none of
the grab's episodes are left out, with a note on the grab. A download that
lies in the arrivals already is handed over in place, and its client keeps it:
its files are the library's. A copy the catalog did not take is deleted. The
download is released in its client only when the library has all the grab
holds; a refused file, or an episode the download holds no file for, keeps it
in the client for you to look at, and the grab's detail says why — so mind the
downloads volume. Against a catalog that does not take ingest by identity yet,
the episodes wait: the copy stays, the download is not released in its client,
and the hand-over is tried again half an hour after the first refusal and up to
six hours apart. See
[from download to catalog](./acquire.md#from-download-to-catalog).

### Room on the downloads volume

A local volume has no quota, and the disk is shared with other volumes, so
three things keep the downloads from filling it:

- acquire hands a release to one of these clients only when the volume has
  room for all of it: twice its size for an NZB (the archive and its
  unpacking), its size for a torrent, plus the reserve
  (`clients.downloads.reserve`, a tenth of the volume by default). Otherwise
  the release waits and the request says why — *waiting for room: this NZB
  needs 72.0 GB on the downloads volume (twice its 21.0 GB, for the archive
  and its unpacking, and the 30.0 GB reserve) and 31.0 GB is free* — and you
  grab it again once there is room. The media storage the copy lands on keeps
  the floor from the search and grab settings;
- qBittorrent preallocates a torrent whole when it is added, so the space it
  will take is taken at once;
- NZBGet pauses when the volume's free space drops below the reserve
  (`DiskSpace`), so downloads that started together cannot run it out.

### Through the platform VPN

The operator names the platform VPN's SOCKS5 proxy in `zaentrum.vpn.proxy`
(`socks5h://host:port`), or leaves it empty when the platform has no VPN. With
`clients.qbittorrent.vpn: true`:

- the pod carries the label `zaentrum.io/vpn: "true"`, and the operator
  confines its direct connections to the cluster: it reaches the internet
  through the proxy or not at all;
- qBittorrent sends everything through the proxy: `Network\Proxy\Type=SOCKS5`
  with `Proxy\IP` and `Proxy\Port` from `zaentrum.vpn.proxy`,
  `Proxy\HostnameLookupEnabled=true` (names are resolved by the proxy, never
  locally), `Proxy\Profiles\BitTorrent`, `RSS` and `Misc` (its own downloads,
  such as a `.torrent` by address), and `Session\ProxyPeerConnections=true`.
  Tracker connections go through the proxy too, and UDP — DHT, µTP, UDP
  trackers — through its UDP association, or is dropped while there is none;
- what would talk to the network directly is off: local peer discovery
  (`Session\LSDEnabled`), UPnP and NAT-PMP (`Network\PortForwardingEnabled`)
  and reverse lookups of peers (`Connection\ResolvePeerHostNames`).

With `vpn: false`, qBittorrent runs without a proxy and without the label. On
a platform without a VPN, `vpn: true` fails the render, in
`templates/qbittorrent-vpn-needs-the-platform-vpn.yaml` — the operator shows a
failed render by its location only, so the file's name is the message.

NZBGet cannot go through the VPN. It has no proxy setting: its news server
options are address, port, login, encryption, connections and the like, and it
opens its connections, over TLS, straight to the server; the proposal to add a
proxy to it is for downloads by address only and leaves news server
connections direct. A pod assigned to the VPN would reach no news server, so
`clients.nzbget.vpn: true` fails the render
(`templates/nzbget-cannot-use-the-vpn.yaml`), and NZBGet runs with direct
connections.

### Credentials and access

When no password is given, the operator generates one per client on the first
install (64 hexadecimal characters), keeps it in
`zaentrum-addon-acquire-generated` and never generates it again. The chart
renders it into `acquire-secrets` (`qbittorrent-password`, `nzbget-password`),
from where the client's init container sets it and acquire reads it. To use a
client's own interface — NZBGet's settings, to add your news server, say:

```bash
kubectl -n <platform namespace> get secret acquire-secrets \
  -o jsonpath='{.data.nzbget-password}' | base64 -d
kubectl -n <platform namespace> port-forward svc/nzbget 6789
```

and log in at `http://localhost:6789` as `acquire` (or your
`clients.nzbget.username`). qBittorrent is the same on port 8080. A password
changed in the client is set back on its next start: change it in the values.

### What acquire does with them

The chart tells acquire about the enabled clients in `ACQUIRE_MANAGED_CLIENTS`,
from `acquire-secrets`: each one's Service address, login, protocols and
folders (`/downloads/complete` as the client sees it,
`/var/lib/acquire/downloads/<client>/complete` as acquire does). At start,
before its first push to the gateway, acquire stores them:

- a client it does not know yet is created under the client's name, enabled,
  marked **managed**;
- one that differs from what the chart says is brought in line; an unchanged
  one is left as it is;
- a client of the same name and type stored before — an external `qbittorrent`
  from an earlier setup — is taken over once no download is in flight on it,
  so the downloads recorded under that name keep resolving;
- a managed client the chart no longer runs is removed, or only disabled while
  downloads are still in flight on it, and removed at a later start.

The console shows managed clients with a **managed** badge and lets you test
them, not edit or remove them; the API answers `409` to either. Turning a
client off in the values removes its Deployment, Service and configuration
claim; turning both off removes the downloads claim too, with whatever is
still on it.

## Identity

| Client | Type | Used for |
|---|---|---|
| `laedeli-acquire-svc` | confidential (client credentials) | acquire's own calls: the gateway (downloads and client configuration), ingest, and what the library holds (katalog-api's holdings) |

That is the only client acquire has. The console signs in through the portal
and calls acquire with the token of the portal's user, which acquire takes
from any client of the realm's issuer and judges by its realm roles:
`zaentrum-user` may request, `zaentrum-admin` may grab, remove and
**configure** — every configuration route is admin-only. With the chart, the
service client's id and the roles are inputs (`oidc.*`).

katalog-api takes the service client's token like any other bearer of the
realm, when it carries the **audience katalog-api accepts**: give the client
an audience mapper for the instance's audience if it has none. A token
without it is refused (`401`), and acquire then keeps what it knew of the
holdings and says on each title why it could not ask. katalog-api never serves
holdings to a request capped at an age (`403`); the service client carries no
cap.

The gateway validates acquire's service token. Set on the gateway:

| Env | Value |
|---|---|
| `OIDC_ISSUER` | the realm issuer |
| `ALLOWED_CLIENTS` | `laedeli-acquire-svc` |

Only with both set does the gateway accept calls from that client alone **and**
offer its client configuration API; without them acquire cannot push download
clients and setup says so. The chart always sets both.

## Where the console is

The console is acquire's app in the portal: `/portal/app/acquire` on the
platform's host, with a hash route for each tab
(`/portal/app/acquire#/downloads`), which is what the launchpad tiles open.
The portal loads the console's bundle from acquire through its app proxy
(`/api/portal/apps/acquire/embed/assets/remoteEntry.js`), renders it inside
its own page and signs the user in. acquire has no page and no login of its
own.

A browser that asks acquire itself for a page — `/`, or any other path the
standalone console of earlier versions answered — gets a temporary redirect
(`302`) to `ACQUIRE_PLATFORM_URL` followed by `/portal/app/acquire`:

- a search (`?q=`) is carried, since the console reads it; nothing else of
  the query is;
- the browser keeps the place in the console: `/#/downloads` lands on
  `/portal/app/acquire#/downloads`;
- without `ACQUIRE_PLATFORM_URL` (the chart leaves it empty only when the
  platform names no `zaentrum.hostname`) the redirect goes to the portal's
  root, `/portal/`, on the host the browser asked.

Nothing else acquire answers is a page: `/embed/…` serves the bundle's files
and nothing more, and an unknown `/api/` path, a missing file of the bundle,
the old page's `/assets/` and any method other than `GET` and `HEAD` are a
`404`.

## Secrets

The chart renders its own Secret, `acquire-secrets`, from the secret inputs and
mounts the platform's event streaming certificate when it has one. Deploying
the manifests yourself, create:

| Secret | Key | Read as |
|---|---|---|
| `acquire-db` | `url` | `PG_URL` |
| `acquire-svc-oidc` | `client-secret` | `ACQUIRE_SVC_CLIENT_SECRET` |
| `acquire-config` | `key` | `ACQUIRE_CONFIG_KEY` |
| `katalog-tmdb` | `api-key` | `TMDB_API_KEY` (optional mount) |
| `acquire-opensubtitles` | `username`, `password`, `api-key` | `OPENSUBTITLES_USERNAME`, `OPENSUBTITLES_PASSWORD`, `OPENSUBTITLES_API_KEY` (optional mounts; subtitles are off without them) |
| `kafka-mtls` | `user.crt`, `user.key`, `ca.crt` | created by the core deploy |

Nothing else: search source keys and download client passwords are **not**
deployment secrets. They are entered in acquire's console and stored in its
database, encrypted. The one exception is the download clients the chart runs:
their passwords are inputs of the chart (see
[credentials and access](#credentials-and-access)), and acquire stores them
from there.

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
| `OIDC_ISSUER`, `ACQUIRE_ADMIN_ROLE`, `ACQUIRE_USER_ROLE` | auth |
| `ACQUIRE_PLATFORM_URL` | the platform's address, `https://<host>`: where a browser that asks acquire for a page is sent (see [where the console is](#where-the-console-is)) |
| `PG_URL` | acquire's database |
| `DOWNLOAD_GATEWAY_URL` | e.g. `http://download-gateway` |
| `KATALOG_URL`, `KATALOG_MANAGER_URL` | catalog read and ingest |
| `OIDC_TOKEN_URL`, `ACQUIRE_SVC_CLIENT_ID`, `ACQUIRE_SVC_CLIENT_SECRET` | the service account |
| `KAFKA_BROKERS`, `KAFKA_CERT_DIR`, `KAFKA_TOPIC_PREFIX`, `KAFKA_GROUP_ID` | events |
| `ACQUIRE_DOWNLOADS_ROOT` | where finished downloads are visible to acquire when a client names no folder of its own |
| `ACQUIRE_MANAGED_CLIENTS` | the download clients the deployment runs, stored as managed clients at start (see [what acquire does with them](#what-acquire-does-with-them)) |
| `ACQUIRE_ARRIVALS_ROOT` | the library's arrivals, writable: finished downloads outside it are copied in before the hand-over (see [from download to library](#from-download-to-library)) |
| `ACQUIRE_DOWNLOADS_RESERVE` | bytes kept free on the downloads volume (see [room on it](#room-on-the-downloads-volume)) |
| `ACQUIRE_CONFIG_KEY`, `ACQUIRE_CONFIG_KEY_PREVIOUS` | credential encryption (above) |
| `ACQUIRE_ENDPOINT_DENY` | hosts acquire must never be pointed at (below) |
| `ACQUIRE_ENDPOINT_ALLOW_INTERNAL` | `true` to allow cluster services in other namespaces |
| `POD_NAMESPACE` | acquire's own namespace (downward API); read from the service account otherwise |
| `ACQUIRE_SUBTITLES`, `ACQUIRE_SUBTITLE_LANGUAGES`, `ACQUIRE_SUBTITLE_DAILY_BUDGET` | subtitles on, their languages (empty: the platform's) and the downloads a day |
| `OPENSUBTITLES_USERNAME`, `OPENSUBTITLES_PASSWORD`, `OPENSUBTITLES_API_KEY` | the OpenSubtitles.com account and its API key |
| `ACQUIRE_VERSION` | acquire's version, as it names itself to OpenSubtitles.com (the chart's version) |

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
`/var/lib/katalog`. For the clients the chart runs it sets both folders itself:
`/downloads/complete` as the client sees it,
`/var/lib/acquire/downloads/<client>/complete` as acquire does, on its
read-only mount of the downloads claim; acquire copies their finished
downloads into the arrivals before it hands them over.

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

1. **Download clients** — the ones the chart runs are there already, marked
   managed. Add each external download client: type, address, credentials, the
   protocols it handles, and the two folders. acquire pushes the set to the
   gateway immediately and whenever the gateway restarts. NZBGet run by the
   chart still needs your news server, entered in NZBGet itself (see
   [credentials and access](#credentials-and-access)).
2. **Search sources** — add each newznab or torznab endpoint with its API key
   and categories; acquire reads its caps on save. See
   [Search sources & NZB-first](./indexers-and-nzb.md).
3. **Search and grab** — which protocol is searched first, the free-space floor,
   how many downloads may run at once, and what is grabbed on its own: **new
   episodes and requests** (`search.autoGrabNew` — what aired in the last
   days, the episodes of a requested series, a requested movie once it is
   out) and **everything missing** (`search.autoGrab`, the old backlog too).
   Both are **off** by default: the sweep then only searches, and missing
   episodes are grabbed from the missing tab — per row, or for a selection.
   Turn the first on to have new episodes arrive by themselves, from the
   sources' newest releases and the sweep; turn the second on once the
   missing tab shows what you expect. Either grabs within the floor and the
   downloads at once, and never a title whose holdings are unknown.
4. **What the library holds** — the missing tab's header says how many titles'
   holdings are known. acquire asks the catalog on its own six hours apart, the
   first time right after its first start, and after an import of titles; **ask
   the catalog now** asks at once. A title of unknown holdings is missing
   nothing until then.

A manual search answers within 50 seconds. Whatever sits between the browser
and acquire must allow at least a minute per request.

acquire's schedules — the sweep, the sources' newest releases, the TMDB
refresh, the reconcile, the grab recovery (see
[schedules](./acquire.md#schedules)) — fire through the platform's event
streaming, as its download events arrive through it: without brokers, nothing
runs on a schedule, the reconcile runs only when it is asked for, a request's
title is read from TMDB only when it is made, and a failed episode waits for a
grab from the missing tab.

## Verifying

- For a chart install, `zae addon status acquire --url https://<instance>` (or
  the addon's row in the portal) reads *Ready* with both components ready.
- `GET /api/setup` on acquire (or the portal's checklist) reads `ready`.
- `GET /` on acquire answers `302` to `https://<host>/portal/app/acquire`, and
  the console opens there.
- `GET /api/v1/config/clients` on the gateway reports the same revision the
  clients tab shows.
- `GET /api/health/system` has no failing `sources` or `clients` check.
- A request → **find & grab** moves `pending → downloading` with a detail line
  naming the source and the client, then `packaging → fulfilled`, and plays.
- The missing tab's header reads no titles of unknown holdings
  (`GET /api/missing` answers `holdings.unknown: 0`), and its rows are
  episodes the library really lacks. One of them → **grab** moves
  `queued → grabbed → downloaded → held`, the row leaves the tab, and the
  episode plays.
- A series requested in discover reads *following* in the requests tab within
  a minute, with its episodes counted, and the series tab marks it
  *requested*. With **new episodes and requests** on, the log says what each
  `rss-sync` run read and grabbed (`acquire rss-sync: 1 feed(s) read …`), and
  an episode that turns up in a source's newest releases is grabbed without a
  step of yours.

## Upgrading to episode grabs

This version (chart 0.4.0) adds migrations `040` (target grabs, what the
library holds, each search's outcome) and `041` (grab reservations, hand-overs
the reconcile respects, failure recovery), applied on start like every other,
and a schedule, `grab-recovery` (hourly), registered on start like the others.
It asks two things of the platform: the catalog's ingest and holdings by
identity (zaentrum's ADR-0012; without them episodes wait and holdings stay
unknown, see [What the library holds](./acquire.md#what-the-library-holds)),
and a service token with the audience katalog-api accepts (see
[Identity](#identity)). These things read differently after the upgrade:

- The **missing** tab is empty until acquire has asked the catalog what the
  library holds — right after the first start, through the
  `holdings-reconcile` schedule, or on **ask the catalog now**. It used to
  count every aired episode, those on disk too. Against a catalog without
  holdings by identity it stays empty, and its header says why.
- The **sweep** searches only titles whose holdings are known, and every
  target it searched comes back after its next due time, so it works through
  the whole backlog. It grabs only once **the backlog sweep** is switched on
  in settings, and then — like **grab** in the missing tab — only a release of
  the episode alone or of one run of at most ten missing episodes in a row; a
  longer range or a season pack is grabbed when someone picks it.
- A **failed** episode is tried again on its own: only its release fails it
  (its download failed, the download had no file for it, the catalog refused
  it), and it is wanted again after six hours, doubling to two days, up to five
  times, the releases that failed it blocked; then it waits for someone to
  grab it from the missing tab. A grab that could not happen — the gateway or a
  source unavailable — fails nothing.
- A **managed client's download** is released only when the library has all
  of it. One the library did not get all of stays in its client; its grab
  says why.
- The catalog's `catalog.item.discovered` and `catalog.item.removed` events are
  read in a second consumer group, `<KAFKA_GROUP_ID>-catalog`. A broker with
  ACLs must let that group read; until those topics exist on the brokers that
  group reads nothing. The reconcile's schedule keeps the holdings right in
  the meantime, six hours apart.

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
   secrets from your overlay — acquire no longer needs them in the namespace;
   the chart can run the clients instead (`clients.*`, see
   [download clients and the VPN](#download-clients-and-the-vpn)), and takes
   over the clients stored under their names once they are idle. Remove the
   client variables from the gateway too: acquire routes grabs only to the
   clients it stores, and an environment client would block a stored client of
   the same id.
4. **Refresh** the addon in the portal so it learns the component group and the
   setup checklist.

## Uninstalling

Installed as a chart:

1. Settings → addons → **remove**, or
   `zae addon remove acquire --url https://<instance>`. Both Deployments, both
   Services and `acquire-secrets` go with the addon, and so do the download
   clients the chart runs, their configuration claims and the downloads claim
   included; what acquire copied into the library stays. With *keep values*
   (`--keep-values`) the values Secret with the inputs you gave and the Secret
   with a generated config key both stay for a later install; without it they
   are deleted too (see [the config key](#the-config-key)).
2. Drop acquire's database when you no longer need the data. All of its
   configuration lives there and nowhere else.

Deployed through your own channel:

1. Settings → addons → **remove**. The app, tiles, slot row and section go; the
   answer lists the addon's workloads that are still deployed.
2. Delete those workloads through the channel that deployed them — the
   `zaentrum.io/addon=acquire` label finds them.
3. Drop acquire's database when you no longer need the data.

The core is a neutral catalog and player again — exactly as before the addon.
