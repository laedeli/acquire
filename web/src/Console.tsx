import { useCallback, useEffect, useMemo, useState } from 'react'
import { Tabs } from '@nalet/design-system'
import {
  makeApi,
  type ClientStatus,
  type Config,
  type Download,
  type Indexer,
  type Wanted,
} from './lib/api'
import { debounce, useEventStream } from './lib/stream'
import { realmRoles } from './lib/format'
import { Requests } from './views/Requests'
import { Downloads } from './views/Downloads'
import { Discover } from './views/Discover'
import { Indexers } from './views/Indexers'
import { Missing } from './views/Missing'
import { Series } from './views/Series'
import { Search } from './views/Search'
import { Settings } from './views/Settings'

const FALLBACK_POLL_MS = 30_000

// Each surface is its own launchpad tile, so it needs its own address. Hash
// routing keeps that working behind the ingress that strips the /acquire prefix,
// without the server having to know where it is mounted.
const TABS = ['requests', 'series', 'missing', 'downloads', 'search', 'discover', 'indexers', 'settings'] as const
type Tab = (typeof TABS)[number]

function tabFromHash(): Tab {
  const h = window.location.hash.replace(/^#\/?/, '').split('?')[0]
  return (TABS as readonly string[]).includes(h) ? (h as Tab) : 'requests'
}

/**
 * Console is the whole acquire UI, with no opinion about how it is hosted: the
 * portal shell mounts it in-page and supplies the proxied API base and the
 * signed-in user's token, and the standalone entry supplies its own. It never
 * runs an OIDC flow itself — whoever hosts it already did.
 */
export function Console({
  apiBase,
  token,
  isAdmin,
  onUnauthorized,
}: {
  apiBase: string
  token: string | undefined
  isAdmin?: boolean
  onUnauthorized?: () => void
}) {
  const [config, setConfig] = useState<Config | null>(null)
  const [tab, setTabState] = useState<Tab>(tabFromHash)

  // Keep the address bar and the view in step, so a tile deep-link lands on the
  // right surface and back/forward behave.
  const setTab = useCallback((t: string) => {
    setTabState(t as Tab)
    if (tabFromHash() !== t) window.location.hash = '#/' + t
  }, [])
  useEffect(() => {
    const onHash = () => setTabState(tabFromHash())
    window.addEventListener('hashchange', onHash)
    return () => window.removeEventListener('hashchange', onHash)
  }, [])
  const [wanted, setWanted] = useState<Wanted[]>([])
  const [downloads, setDownloads] = useState<Download[]>([])
  const [clients, setClients] = useState<ClientStatus[]>([])
  const [indexers, setIndexers] = useState<Indexer[]>([])

  const api = useMemo(
    () => makeApi(apiBase, token, () => onUnauthorized?.()),
    [apiBase, token, onUnauthorized],
  )

  // The host may already know; otherwise read the realm roles out of the token
  // it handed us (they live in the access token, not the ID-token profile).
  const admin = useMemo(() => {
    if (typeof isAdmin === 'boolean') return isAdmin
    if (!config) return false
    return realmRoles(token).includes(config.adminRole)
  }, [isAdmin, token, config])

  useEffect(() => {
    let live = true
    api
      .config()
      .then((c) => live && setConfig(c))
      .catch(() => undefined)
    return () => {
      live = false
    }
  }, [api])

  const loadLists = useCallback(async () => {
    const [w, d] = await Promise.allSettled([api.wanted(), api.downloads()])
    if (w.status === 'fulfilled') setWanted(w.value)
    if (d.status === 'fulfilled') setDownloads(d.value)
  }, [api])

  const loadSide = useCallback(async () => {
    const [c, i] = await Promise.allSettled([api.clients(), api.indexers()])
    if (c.status === 'fulfilled') setClients(c.value)
    if (i.status === 'fulfilled') setIndexers(i.value)
  }, [api])

  useEffect(() => {
    if (!token) return
    void loadLists()
    void loadSide()
    // The live stream is the primary trigger; this only covers a dropped one.
    const t = setInterval(() => {
      void loadLists()
      void loadSide()
    }, FALLBACK_POLL_MS)
    return () => clearInterval(t)
  }, [token, loadLists, loadSide])

  const debouncedLists = useMemo(() => debounce(() => void loadLists(), 400), [loadLists])

  useEventStream(apiBase, token, {
    // Telemetry arrives every few seconds — apply it in place rather than
    // refetching the whole list for each tick.
    onDownload: (d) =>
      setDownloads((prev) => {
        const i = prev.findIndex(
          (x) => x.adapter === d.adapter && x.clientJobId === d.clientJobId,
        )
        if (i < 0) return [d, ...prev]
        const next = prev.slice()
        next[i] = d
        return next
      }),
    onChanged: debouncedLists,
    onReconnect: () => {
      void loadLists()
      void loadSide()
    },
  })

  const active = downloads.filter((d) => d.state === 'downloading' || d.state === 'queued').length

  return (
    <div className="acq">
      <Tabs
        value={tab}
        onChange={setTab}
        items={[
          { value: 'requests', label: `requests${wanted.length ? ` (${wanted.length})` : ''}` },
          // The backlog sits next to requests on purpose: "what was asked for"
          // and "what is still owed" are the same question from two ends.
          { value: 'series', label: 'series' },
          { value: 'missing', label: 'missing' },
          { value: 'downloads', label: `downloads${active ? ` (${active})` : ''}` },
          { value: 'search', label: 'search' },
          { value: 'discover', label: 'discover' },
          { value: 'indexers', label: 'indexers' },
          { value: 'settings', label: 'settings' },
        ]}
      />

      <main className="acq__main">
        {tab === 'requests' && (
          <Requests
            api={api}
            rows={wanted}
            downloads={downloads}
            admin={admin}
            autoGrab={!!config?.autoGrab}
            refresh={() => void loadLists()}
          />
        )}
        {tab === 'series' && <Series api={api} />}
        {tab === 'missing' && <Missing api={api} />}
        {tab === 'downloads' && (
          <Downloads
            api={api}
            rows={downloads}
            clients={clients}
            admin={admin}
            refresh={() => void loadLists()}
          />
        )}
        {tab === 'discover' && (
          <Discover
            api={api}
            initialQuery={new URLSearchParams(window.location.search).get('q') || ''}
            refresh={() => void loadLists()}
          />
        )}
        {tab === 'search' && (
          <Search
            api={api}
            indexers={indexers}
            wanted={wanted}
            admin={admin}
            refresh={() => void loadLists()}
          />
        )}
        {tab === 'indexers' && <Indexers rows={indexers} preferUsenet />}
        {tab === 'settings' && <Settings api={api} admin={admin} />}
      </main>
    </div>
  )
}
