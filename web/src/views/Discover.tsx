// Discover: search TMDB and request what is missing. Titles already in the
// library are shown but not requestable.
import { useState } from 'react'
import { Button, Input, Text } from '@nalet/design-system'
import { Search } from 'lucide-react'
import type { Api, DiscoverHit } from '../lib/api'

export function Discover({
  api,
  initialQuery,
  refresh,
}: {
  api: Api
  initialQuery: string
  refresh: () => void
}) {
  const [q, setQ] = useState(initialQuery)
  const [hits, setHits] = useState<DiscoverHit[] | null>(null)
  const [loading, setLoading] = useState(false)
  const [requested, setRequested] = useState<Set<number>>(new Set())
  const [error, setError] = useState('')

  async function search() {
    if (!q.trim()) return
    setLoading(true)
    setError('')
    try {
      setHits(await api.discover(q.trim()))
    } catch (e) {
      setError(String((e as Error).message || e))
    } finally {
      setLoading(false)
    }
  }

  async function request(h: DiscoverHit) {
    try {
      await api.request(h)
      setRequested((s) => new Set(s).add(h.tmdbId))
      refresh()
    } catch (e) {
      setError(String((e as Error).message || e))
    }
  }

  return (
    <>
      <div className="acq__searchbar">
        <Input
          value={q}
          placeholder="search a movie or show to request…"
          onChange={(e) => setQ(e.currentTarget.value)}
          onKeyDown={(e) => e.key === 'Enter' && void search()}
        />
        <Button variant="primary" loading={loading} leading={<Search size={15} />} onClick={() => void search()}>
          search
        </Button>
      </div>
      {error && <Text variant="muted">{error}</Text>}
      {hits && !hits.length && <Text variant="muted">nothing found for “{q}”.</Text>}
      <div className="acq__grid">
        {(hits || []).map((h) => (
          <div className="acq__card" key={`${h.mediaType}-${h.tmdbId}`}>
            {h.posterUrl ? (
              <img src={h.posterUrl} alt="" loading="lazy" />
            ) : (
              <div className="acq__poster-blank" />
            )}
            <div className="acq__card-body">
              <div className="acq__mono acq__card-title">{h.title}</div>
              <Text variant="muted" as="div">
                {h.year || ''} · {h.mediaType}
              </Text>
              {h.inLibrary ? (
                <Button size="sm" disabled block>
                  in library
                </Button>
              ) : (
                <Button
                  size="sm"
                  variant="primary"
                  block
                  disabled={requested.has(h.tmdbId)}
                  onClick={() => void request(h)}
                >
                  {requested.has(h.tmdbId) ? 'requested' : 'request'}
                </Button>
              )}
            </div>
          </div>
        ))}
      </div>
    </>
  )
}
