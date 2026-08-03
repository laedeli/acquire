// Series: every tracked show and how much of it we actually have.
//
// The progress bar is the point. "402 series" tells you nothing; "held 0 of 49"
// tells you where the work is. Counts come pre-aggregated from one query rather
// than a request per series — 399 rows makes an N+1 immediately visible.
import { useEffect, useState } from 'react'
import { Badge, Table, Text, type TableColumn } from '@nalet/design-system'
import type { Api, SeriesRow } from '../lib/api'
import { ProgressBar } from '../components/Bits'

export function Series({ api }: { api: Api }) {
  const [rows, setRows] = useState<SeriesRow[]>([])
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    let live = true
    const load = async () => {
      try {
        const s = await api.series()
        if (live) {
          setRows(s)
          setError('')
        }
      } catch (e) {
        if (live) setError(String((e as Error).message || e))
      } finally {
        if (live) setLoading(false)
      }
    }
    void load()
    const t = setInterval(load, 60000)
    return () => {
      live = false
      clearInterval(t)
    }
  }, [api])

  const cols: TableColumn<SeriesRow>[] = [
    {
      key: 'title',
      header: 'series',
      render: (r) => (
        <div>
          <Text>{r.title}</Text> {r.year > 0 && <Text variant="dim">{r.year}</Text>}
          {r.type === 'anime' && <Text variant="dim"> · anime</Text>}
        </div>
      ),
    },
    {
      key: 'episodes',
      header: 'held',
      render: (r) => (
        <div>
          <Text variant="ui">
            {r.held} / {r.episodes || '—'}
          </Text>
          {r.episodes > 0 && <ProgressBar value={Math.round((r.held / r.episodes) * 100)} />}
        </div>
      ),
    },
    {
      key: 'missing',
      header: 'state',
      render: (r) => {
        // No episodes derived yet is a real, distinct state: the series is
        // tracked but TMDB has not been asked for its inventory. Showing it as
        // "0 missing" would read as complete.
        if (r.episodes === 0) return <Badge tone="neutral">inventory not derived</Badge>
        if (r.missing > 0) return <Badge tone="amber">{r.missing} missing</Badge>
        if (r.unaired > 0) return <Badge tone="blue">{r.unaired} upcoming</Badge>
        return <Badge tone="green">complete</Badge>
      },
    },
  ]

  return (
    <div className="acq__view">
      {rows.length > 0 && (
        <Text variant="ui">
          {rows.length} series · {rows.filter((r) => r.episodes === 0).length} awaiting inventory
        </Text>
      )}
      {error && <Text variant="muted">could not load series: {error}</Text>}
      {loading && !rows.length && <Text variant="muted">loading…</Text>}
      {!loading && !rows.length && !error && (
        <Text variant="muted">no series tracked yet — import intent to populate this</Text>
      )}
      {rows.length > 0 && <Table columns={cols} rows={rows} />}
    </div>
  )
}
