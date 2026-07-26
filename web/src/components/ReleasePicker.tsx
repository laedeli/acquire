// Interactive search: show what the indexers actually offer for a request, in
// the order acquire would pick, and let an admin choose a different release.
import { useEffect, useState } from 'react'
import { Badge, Button, Modal, Table, Text, type TableColumn } from '@nalet/design-system'
import type { Api, Candidate, Wanted } from '../lib/api'
import { bytes } from '../lib/format'

export function ReleasePicker({
  api,
  wanted,
  onClose,
  onGrabbed,
}: {
  api: Api
  wanted: Wanted
  onClose: () => void
  onGrabbed: () => void
}) {
  const [rows, setRows] = useState<Candidate[] | null>(null)
  const [error, setError] = useState('')
  const [grabbing, setGrabbing] = useState('')

  useEffect(() => {
    let live = true
    api
      .releases(wanted.id)
      .then((r) => live && setRows(r))
      .catch((e) => live && setError(String(e.message || e)))
    return () => {
      live = false
    }
  }, [api, wanted.id])

  async function pick(c: Candidate) {
    setGrabbing(c.source)
    try {
      await api.pick(wanted.id, c)
      onGrabbed()
      onClose()
    } catch (e) {
      setError(String((e as Error).message || e))
    } finally {
      setGrabbing('')
    }
  }

  const columns: TableColumn<Candidate>[] = [
    {
      key: 'title',
      header: 'release',
      render: (c) => (
        <div>
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
      key: 'source',
      header: '',
      align: 'right',
      render: (c) => (
        <Button
          size="sm"
          variant={c.best ? 'primary' : 'default'}
          loading={grabbing === c.source}
          onClick={() => void pick(c)}
        >
          grab
        </Button>
      ),
    },
  ]

  return (
    <Modal
      open
      onClose={onClose}
      width={900}
      title={`releases · ${wanted.title}${wanted.year ? ` (${wanted.year})` : ''}`}
    >
      {error && <Text variant="muted">{error}</Text>}
      {!rows && !error && <Text variant="muted">searching the indexers…</Text>}
      {rows && (
        <Table
          columns={columns}
          rows={rows}
          rowKey={(c) => c.source}
          dense
          empty={<Text variant="muted">no releases found.</Text>}
        />
      )}
    </Modal>
  )
}
