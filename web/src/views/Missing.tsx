// Missing: what we want and do not have.
//
// The backlog was previously invisible — it existed only as rows in a table
// nobody could see. This is the view that makes 158 wanted episodes a fact on a
// screen rather than a number in a database.
//
// Two deliberate choices, both about not lying by omission:
//   * rows in search backoff are SHOWN and flagged, not filtered out. A backlog
//     that hides everything currently failing looks healthy while being stuck.
//   * a title with no id an indexer accepts is marked unsearchable, so a row
//     that cannot progress says why instead of sitting there looking ordinary.
import { useEffect, useState } from 'react'
import { Badge, Table, Text, type TableColumn } from '@nalet/design-system'
import type { Api, Counts, MissingRow } from '../lib/api'
import { ago } from '../lib/format'

function coords(r: MissingRow) {
  if (r.season == null || r.episode == null) return 'movie'
  return `S${String(r.season).padStart(2, '0')}E${String(r.episode).padStart(2, '0')}`
}

export function Missing({ api }: { api: Api }) {
  const [rows, setRows] = useState<MissingRow[]>([])
  const [counts, setCounts] = useState<Counts | null>(null)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    let live = true
    const load = async () => {
      try {
        const [m, c] = await Promise.all([api.missing(500), api.counts()])
        if (!live) return
        setRows(m)
        setCounts(c)
        setError('')
      } catch (e) {
        if (live) setError(String((e as Error).message || e))
      } finally {
        if (live) setLoading(false)
      }
    }
    void load()
    const t = setInterval(load, 30000)
    return () => {
      live = false
      clearInterval(t)
    }
  }, [api])

  const cols: TableColumn<MissingRow>[] = [
    {
      key: 'title',
      header: 'title',
      render: (r) => (
        <div>
          <Text>{r.title}</Text>{' '}
          <Text variant="dim">{coords(r)}</Text>
        </div>
      ),
    },
    {
      key: 'airDate',
      header: 'aired',
      render: (r) => <Text variant="muted">{r.airDate ? ago(r.airDate) : '—'}</Text>,
    },
    {
      key: 'searchFailures' as keyof MissingRow,
      header: 'state',
      render: (r) => {
        if (!r.searchable)
          return (
            <Badge tone="amber">
              no indexer id — text search only
            </Badge>
          )
        if (r.backoffUntil && new Date(r.backoffUntil) > new Date())
          return (
            <Badge tone="neutral">
              retrying {ago(r.backoffUntil)} · {r.searchFailures} failed
            </Badge>
          )
        return <Badge tone="blue">searching</Badge>
      },
    },
  ]

  return (
    <div className="acq__view">
      {counts && (
        <div className="acq__counts">
          <Text variant="ui">
            {counts.missing} missing · {counts.held} held · {counts.unaired} not yet aired ·{' '}
            {counts.inBackoff} in backoff
          </Text>{' '}
          <Text variant="dim">
            across {counts.series} series and {counts.movies} movies
          </Text>
        </div>
      )}
      {error && <Text variant="muted">could not load the backlog: {error}</Text>}
      {loading && !rows.length && <Text variant="muted">loading…</Text>}
      {!loading && !rows.length && !error && (
        <Text variant="muted">
          nothing wanted and unheld — either everything is acquired, or nothing is monitored yet
        </Text>
      )}
      {rows.length > 0 && <Table columns={cols} rows={rows} />}
    </div>
  )
}
