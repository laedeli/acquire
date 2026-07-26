// Indexers: what acquire searches, and in which order it prefers them.
//
// Read-only for now — the indexer definitions still live in the external search
// backend. When the engine moves in-process this view gains add/edit/test.
import { Badge, Table, Text, type TableColumn } from '@nalet/design-system'
import type { Indexer } from '../lib/api'

export function Indexers({ rows, preferUsenet }: { rows: Indexer[]; preferUsenet: boolean }) {
  const columns: TableColumn<Indexer>[] = [
    { key: 'name', header: 'indexer', render: (i) => <span className="acq__mono">{i.name}</span> },
    {
      key: 'protocol',
      header: 'protocol',
      render: (i) => (
        <Badge tone={i.protocol === 'usenet' ? 'green' : 'blue'}>
          {i.protocol === 'usenet' ? 'NZB' : 'torrent'}
        </Badge>
      ),
    },
    {
      key: 'enabled',
      header: 'state',
      render: (i) => (
        <Badge tone={i.enabled ? 'green' : 'neutral'} dot>
          {i.enabled ? 'enabled' : 'disabled'}
        </Badge>
      ),
    },
  ]

  const usenet = rows.filter((r) => r.protocol === 'usenet' && r.enabled).length
  const torrent = rows.filter((r) => r.protocol === 'torrent' && r.enabled).length

  return (
    <>
      <Text variant="muted" as="p">
        {usenet} usenet · {torrent} torrent enabled.{' '}
        {preferUsenet
          ? 'Search is NZB-first: the usenet indexers are queried first, and the torrent fan-out only runs when they come back empty.'
          : 'Search is torrent-first.'}
      </Text>
      <Table
        columns={columns}
        rows={rows}
        rowKey={(i) => i.name}
        dense
        empty={<Text variant="muted">no indexers configured.</Text>}
      />
    </>
  )
}
