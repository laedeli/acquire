import { useCallback, useEffect, useMemo, useState } from 'react'
import { useAuth } from 'react-oidc-context'
import { Button, Heading, Tabs, Text } from '@nalet/design-system'
import { LogOut } from 'lucide-react'
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

const FALLBACK_POLL_MS = 30_000

export function App({ config }: { config: Config }) {
  const auth = useAuth()
  const token = auth.user?.access_token
  const [tab, setTab] = useState('requests')
  const [wanted, setWanted] = useState<Wanted[]>([])
  const [downloads, setDownloads] = useState<Download[]>([])
  const [clients, setClients] = useState<ClientStatus[]>([])
  const [indexers, setIndexers] = useState<Indexer[]>([])

  const api = useMemo(
    () => makeApi(token, () => void auth.signinRedirect()),
    [token, auth],
  )

  const admin = useMemo(
    () => realmRoles(auth.user?.access_token).includes(config.adminRole),
    [auth.user, config.adminRole],
  )

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

  useEventStream(token, {
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
      <header className="acq__header">
        <Heading level={1} chevron>
          acquire
        </Heading>
        <div className="acq__who">
          <Text variant="muted" as="span">
            {String(auth.user?.profile?.preferred_username || auth.user?.profile?.email || '')}
            {admin ? ' · admin' : ''}
          </Text>
          <Button
            size="sm"
            variant="ghost"
            leading={<LogOut size={14} />}
            onClick={() => void auth.removeUser()}
          >
            sign out
          </Button>
        </div>
      </header>

      <Tabs
        value={tab}
        onChange={setTab}
        items={[
          { value: 'requests', label: `requests${wanted.length ? ` (${wanted.length})` : ''}` },
          { value: 'downloads', label: `downloads${active ? ` (${active})` : ''}` },
          { value: 'discover', label: 'discover' },
          { value: 'indexers', label: 'indexers' },
        ]}
      />

      <main className="acq__main">
        {tab === 'requests' && (
          <Requests
            api={api}
            rows={wanted}
            downloads={downloads}
            admin={admin}
            autoGrab={config.autoGrab}
            refresh={() => void loadLists()}
          />
        )}
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
        {tab === 'indexers' && <Indexers rows={indexers} preferUsenet />}
      </main>
    </div>
  )
}
