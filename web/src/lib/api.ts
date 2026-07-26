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
  name: string
  protocol: string
  enabled: boolean
}

// The API lives next to the SPA, so paths are relative to wherever it is served
// from ('/acquire/' on the reference deployment).
const base = window.location.pathname.replace(/\/[^/]*$/, '/')

export class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
  ) {
    super(message)
  }
}

export function makeApi(token: string | undefined, onUnauthorized: () => void) {
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
    control: (adapter: string, jobId: string, action: 'pause' | 'resume' | 'cancel') =>
      call<unknown>(
        `downloads/${encodeURIComponent(adapter)}/${encodeURIComponent(jobId)}/${action}`,
        { method: 'POST' },
      ),
    config: () => call<Config>('config'),
  }
}

export type Api = ReturnType<typeof makeApi>
export { base as apiBase }
