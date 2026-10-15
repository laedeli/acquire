// Interactive search: show what the search sources actually offer for a
// request or a missing episode, in the order acquire would pick, and let an
// admin choose a different release. For an episode the releases say what they
// hold — the episode alone, a file of several, a season pack — since a grab is
// bound to every wanted episode the release covers.
import { useEffect, useState } from 'react'
import { Badge, Button, Modal, Table, Text, type TableColumn } from '@nalet/design-system'
import type { Candidate, SearchResult } from '../lib/api'
import { bytes } from '../lib/format'
import { candidateKey } from '../views/Search'

export function ReleasePicker({
  title,
  load,
  pick,
  onClose,
  onGrabbed,
}: {
  title: string
  /** Runs the search: a request's free text, or a target's typed search. */
  load: () => Promise<SearchResult>
  /** Grabs the release picked. */
  pick: (c: Candidate) => Promise<unknown>
  onClose: () => void
  onGrabbed: () => void
}) {
  const [rows, setRows] = useState<Candidate[] | null>(null)
  const [incomplete, setIncomplete] = useState<string[]>([])
  const [error, setError] = useState('')
  const [grabbing, setGrabbing] = useState('')

  useEffect(() => {
    let live = true
    load()
      .then((r) => {
        if (!live) return
        setRows(r.candidates)
        setIncomplete(r.incomplete)
      })
      .catch((e) => live && setError(String(e.message || e)))
    return () => {
      live = false
    }
    // load is a fresh closure on every render of the caller: the search runs
    // once, when the picker opens.
  }, [])

  async function grab(c: Candidate) {
    setGrabbing(candidateKey(c))
    try {
      await pick(c)
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
          <div className="acq__mono">
            {c.title}{' '}
            {c.covers && <Badge tone={c.pack ? 'amber' : 'neutral'}>{c.pack ? `${c.covers} · season pack` : c.covers}</Badge>}
          </div>
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
    { key: 'indexer', header: 'source' },
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
          loading={grabbing === candidateKey(c)}
          onClick={() => void grab(c)}
        >
          grab
        </Button>
      ),
    },
  ]

  return (
    <Modal open onClose={onClose} width={900} title={`releases · ${title}`}>
      {error && <Text variant="muted">{error}</Text>}
      {!rows && !error && <Text variant="muted">searching the sources…</Text>}
      {rows && incomplete.length > 0 && (
        <Text variant="muted">incomplete — no answer from {incomplete.join(', ')}.</Text>
      )}
      {rows && (
        <Table
          columns={columns}
          rows={rows}
          rowKey={candidateKey}
          dense
          empty={<Text variant="muted">no releases found.</Text>}
        />
      )}
    </Modal>
  )
}
