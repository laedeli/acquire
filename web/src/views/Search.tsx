// Manual search: query the indexers directly, scoped to whichever ones you
// choose, and grab a release without waiting for a request to exist. Results are
// scored by the active quality profile, so what would win automatically is
// marked — and what the profile refuses is shown, greyed, with the reason.
import { useState } from 'react'
import {
  Badge,
  Button,
  Checkbox,
  Input,
  Table,
  Text,
  type TableColumn,
} from '@nalet/design-system'
import { Search as SearchIcon } from 'lucide-react'
import type { Api, Candidate, Indexer, Wanted } from '../lib/api'
import { bytes } from '../lib/format'

export function Search({
  api,
  indexers,
  wanted,
  admin,
  refresh,
}: {
  api: Api
  indexers: Indexer[]
  wanted: Wanted[]
  admin: boolean
  refresh: () => void
}) {
  const [q, setQ] = useState('')
  const [scope, setScope] = useState<Set<number>>(new Set())
  const [rows, setRows] = useState<Candidate[] | null>(null)
  const [loading, setLoading] = useState(false)
  const [busy, setBusy] = useState('')
  const [error, setError] = useState('')
  const [note, setNote] = useState('')

  const enabled = indexers.filter((i) => i.enabled)

  async function run() {
    if (!q.trim()) return
    setLoading(true)
    setError('')
    setNote('')
    try {
      setRows(await api.search(q.trim(), [...scope]))
    } catch (e) {
      setError(String((e as Error).message || e))
    } finally {
      setLoading(false)
    }
  }

  async function grab(c: Candidate) {
    // Attach to a matching open request when there is one, so the download
    // fulfils it instead of creating a duplicate.
    const match = wanted.find(
      (w) =>
        (w.status === 'pending' || w.status === 'failed') &&
        c.title.toLowerCase().includes(w.title.toLowerCase().slice(0, 12)),
    )
    setBusy(c.source)
    setError('')
    try {
      await api.grabFound(c, { wantedId: match?.id, title: q.trim() })
      setNote(
        match
          ? `grabbing for the existing request “${match.title}”.`
          : 'grabbing — a request was created so it lands in the catalog.',
      )
      refresh()
    } catch (e) {
      setError(String((e as Error).message || e))
    } finally {
      setBusy('')
    }
  }

  function toggle(id: number) {
    setScope((prev) => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }

  const columns: TableColumn<Candidate>[] = [
    {
      key: 'title',
      header: 'release',
      render: (c) => (
        <div className={c.rejected ? 'acq__dim' : undefined}>
          <div className="acq__mono">{c.title}</div>
          <div className="acq__sub">{c.reason}</div>
        </div>
      ),
    },
    {
      key: 'protocol',
      header: 'protocol',
      render: (c) => (
        <Badge tone={c.protocol === 'usenet' ? 'green' : 'blue'}>
          {c.protocol === 'usenet' ? 'NZB' : 'torrent'}
        </Badge>
      ),
    },
    { key: 'indexer', header: 'indexer' },
    { key: 'size', header: 'size', align: 'right', render: (c) => bytes(c.size) },
    {
      key: 'seeders',
      header: 'seeds',
      align: 'right',
      render: (c) => (c.protocol === 'torrent' ? String(c.seeders) : '—'),
    },
    {
      key: 'score',
      header: 'score',
      align: 'right',
      render: (c) =>
        c.rejected ? <Badge tone="amber">rejected</Badge> : <span className="acq__sub">{c.score}</span>,
    },
    {
      key: 'source',
      header: '',
      align: 'right',
      render: (c) =>
        admin ? (
          <Button
            size="sm"
            variant={c.best ? 'primary' : 'default'}
            loading={busy === c.source}
            onClick={() => void grab(c)}
          >
            grab
          </Button>
        ) : null,
    },
  ]

  return (
    <>
      <div className="acq__searchbar">
        <Input
          value={q}
          placeholder="search all indexers — a title, a release name, anything…"
          onChange={(e) => setQ(e.currentTarget.value)}
          onKeyDown={(e) => e.key === 'Enter' && void run()}
        />
        <Button variant="primary" loading={loading} leading={<SearchIcon size={15} />} onClick={() => void run()}>
          search
        </Button>
      </div>

      <div className="acq__scope">
        <Text variant="muted" as="span">
          {scope.size ? `${scope.size} indexer(s) selected` : 'all enabled indexers'} —
          tick to narrow the search:
        </Text>
        <div className="acq__scope-list">
          {enabled.map((i) => (
            <Checkbox
              key={i.id}
              checked={scope.has(i.id)}
              onChange={() => toggle(i.id)}
              label={`${i.name}${i.protocol === 'usenet' ? ' (NZB)' : ''}`}
            />
          ))}
        </div>
      </div>

      {error && <Text variant="muted">{error}</Text>}
      {note && <Text variant="muted">{note}</Text>}
      {rows && (
        <Table
          columns={columns}
          rows={rows}
          rowKey={(c) => c.source}
          dense
          empty={<Text variant="muted">nothing found.</Text>}
        />
      )}
    </>
  )
}
