// The acquire REST surface. Every call carries the signed-in user's bearer;
// a 401 means the session lapsed, which the auth layer handles by re-login.

export interface Config {
  oidcIssuer: string
  oidcClientId: string
  adminRole: string
  autoGrab: boolean
}

export interface Wanted {
  id: string
  tmdbId: number
  mediaType: string
  title: string
  year: number
  posterUrl: string
  requestedBy: string
  requestedAt: string
  status: string
  detail: string
  itemId: string
  updatedAt: string
}

export interface Download {
  adapter: string
  clientJobId: string
  wantedId: string
  title: string
  state: string
  nativeState: string
  progressPct: number
  bytesDone: number
  bytesTotal: number
  speedBps: number
  etaSec: number | null
  seeders: number | null
  leechers: number | null
  health: number | null
  error: string
  startedAt: string
  updatedAt: string
  finishedAt: string | null
}

export interface ClientStatus {
  name: string
  reachable: boolean
  error?: string
  down_bps: number
  up_bps: number
  paused: boolean
  free_disk_bytes?: number
  detail?: Record<string, string>
}

export interface Candidate {
  title: string
  indexer: string
  protocol: string
  size: number
  seeders: number
  adapter: string
  source: string
  reason: string
  best: boolean
  score: number
  rejected: boolean
  resolution: string
  codec: string
  sourceType: string
}

export interface DiscoverHit {
  tmdbId: number
  mediaType: string
  title: string
  year: number
  posterUrl: string
  overview: string
  inLibrary: boolean
}

export interface Indexer {
  id: number
  name: string
  protocol: string
  enabled: boolean
}

export interface QualityProfile {
  id: string
  name: string
  isDefault: boolean
  updatedAt?: string
  config: {
    preferProtocol: string
    resolutions: string[]
    preferredCodecs: string[]
    rejectTerms: string[]
    minSizeMb: number
    maxSizeMb: number
    minSeeders: number
    preferHdr: boolean
  }
}

// Where the API lives depends on how the console is running: standalone it sits
// next to the SPA, embedded it is reached through the portal's proxy. The host
// tells us, so nothing here assumes a mount point.
export function defaultApiBase(): string {
  return window.location.pathname.replace(/\/[^/]*$/, '/')
}

export class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
  ) {
    super(message)
  }
}

export function makeApi(base: string, token: string | undefined, onUnauthorized: () => void) {
  async function call<T>(path: string, init: RequestInit = {}): Promise<T> {
    const res = await fetch(base + 'api/' + path, {
      ...init,
      headers: {
        ...(init.headers || {}),
        ...(token ? { Authorization: 'Bearer ' + token } : {}),
        ...(init.body ? { 'Content-Type': 'application/json' } : {}),
      },
    })
    if (res.status === 401) {
      onUnauthorized()
      throw new ApiError(401, 'session expired')
    }
    if (!res.ok) throw new ApiError(res.status, (await res.text()) || res.statusText)
    if (res.status === 204) return undefined as T
    return (await res.json()) as T
  }

  return {
    wanted: () => call<Wanted[]>('wanted'),
    request: (hit: DiscoverHit) =>
      call<Wanted>('wanted', {
        method: 'POST',
        body: JSON.stringify({
          tmdbId: hit.tmdbId,
          mediaType: hit.mediaType,
          title: hit.title,
          year: hit.year,
          posterUrl: hit.posterUrl,
        }),
      }),
    remove: (id: string) => call<void>('wanted/' + encodeURIComponent(id), { method: 'DELETE' }),
    autograb: (id: string) =>
      call<unknown>('wanted/' + encodeURIComponent(id) + '/autograb', { method: 'POST' }),
    grabMagnet: (id: string, source: string) =>
      call<unknown>('wanted/' + encodeURIComponent(id) + '/grab', {
        method: 'POST',
        body: JSON.stringify({ source, adapter: 'qbittorrent' }),
      }),
    releases: (id: string) => call<Candidate[]>('wanted/' + encodeURIComponent(id) + '/releases'),
    pick: (id: string, c: Candidate) =>
      call<unknown>('wanted/' + encodeURIComponent(id) + '/pick', {
        method: 'POST',
        body: JSON.stringify(c),
      }),
    discover: (q: string) => call<DiscoverHit[]>('discover?q=' + encodeURIComponent(q)),
    downloads: () => call<Download[]>('downloads'),
    clients: () => call<ClientStatus[]>('clients'),
    indexers: () => call<Indexer[]>('indexers'),
    search: (q: string, indexerIds: number[] = []) =>
      call<Candidate[]>(
        'search?q=' +
          encodeURIComponent(q) +
          (indexerIds.length ? '&indexers=' + indexerIds.join(',') : ''),
      ),
    grabFound: (c: Candidate, opts: { wantedId?: string; title?: string }) =>
      call<unknown>('search/grab', {
        method: 'POST',
        body: JSON.stringify({ ...c, wantedId: opts.wantedId || '', title2: opts.title || '' }),
      }),
    profiles: () => call<QualityProfile[]>('profiles'),
    saveProfile: (p: QualityProfile) =>
      call<QualityProfile>('profiles/' + encodeURIComponent(p.id), {
        method: 'PUT',
        body: JSON.stringify(p),
      }),
    deleteProfile: (id: string) =>
      call<void>('profiles/' + encodeURIComponent(id), { method: 'DELETE' }),
    control: (adapter: string, jobId: string, action: 'pause' | 'resume' | 'cancel') =>
      call<unknown>(
        `downloads/${encodeURIComponent(adapter)}/${encodeURIComponent(jobId)}/${action}`,
        { method: 'POST' },
      ),
    config: () => call<Config>('config'),
    // The WANT model's read side. `missing` is the backlog: monitored, aired,
    // still wanted — including rows in search backoff, flagged rather than
    // hidden, because a backlog view that omits everything failing is the least
    // useful version of itself.
    missing: (limit = 500) =>
      call<{ missing: MissingRow[] }>('missing?limit=' + limit).then((r) => r.missing ?? []),
    counts: () => call<Counts>('counts'),
    series: () => call<{ series: SeriesRow[] }>('series').then((r) => r.series ?? []),
  }
}

// MissingRow is one thing we want and do not have.
export type MissingRow = {
  targetId: string
  title: string
  kind: string
  season: number | null
  episode: number | null
  airDate: string | null
  searchFailures: number
  backoffUntil: string | null
  // False when the title carries no id an indexer will accept. Zero of 70
  // indexers accept a tmdbId, so such a row can only be searched as free text
  // and needs to say so rather than look like an ordinary miss.
  searchable: boolean
}

// SeriesRow is one tracked series with its acquisition progress.
export type SeriesRow = {
  titleId: string
  tmdbId: number
  tvdbId: number
  title: string
  year: number
  status: string
  type: string
  monitored: boolean
  episodes: number
  held: number
  missing: number
  unaired: number
}

export type Counts = {
  titles: number
  series: number
  movies: number
  targets: number
  held: number
  missing: number
  unaired: number
  inBackoff: number
}

export type Api = ReturnType<typeof makeApi>
