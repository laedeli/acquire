// Pick a version: what OpenSubtitles.com has for one title in one language,
// in the order an automatic choice ranks it — a subtitle made for this very
// file first, then one made for a release of its name, then by downloads and
// rating — and a person takes one. A search costs no download; taking one
// does, from the account's own downloads, at once.
//
// Each row is its release's name, with how it fits ("matches this file",
// "matches this release", "may be out of sync"), its downloads and rating,
// and the quiet flags that say more: AI for a machine translation, hearing
// impaired, Traditional. One that can't fit a single file — in parts, made
// for another title — says so and can't be taken; a machine translation can,
// by a person. How the search was made is under details.
import { useEffect, useState } from 'react'
import { Badge, Button, MessageStrip, Modal, Table, Text, formatCount, type TableColumn } from '@nalet/design-system'
import type { Api, SubtitleCandidate, SubtitleItem, SubtitleSearch } from '../lib/api'
import { KeepCase } from './Bits'
import { fitWords } from '../lib/format'

/** Why a person can't take a candidate, in words: what can't fit the one file; "" when they can. */
function unusable(c: SubtitleCandidate): string {
  const why = c.excluded ?? ''
  if (!why || why === 'machine-translated' || why === 'AI-translated') return ''
  const parts = /^in (\d+) files$/.exec(why)
  if (parts) return `comes in ${parts[1]} parts · can't be used for one file`
  if (why === 'foreign parts only') return "only the parts in another language · can't be used as a full subtitle"
  return `${why} · can't be used`
}

const LANGUAGE: Record<string, string> = {
  'zh-Hans': 'Chinese (Simplified)',
  'zh-Hant': 'Chinese (Traditional)',
  'pt-BR': 'Portuguese (Brazil)',
  'pt-PT': 'Portuguese',
}

function languageName(c: SubtitleCandidate, fallback: string): string {
  return LANGUAGE[c.tag] ?? fallback
}

const FIT_TONE = { file: 'green', release: 'blue', unknown: 'amber' } as const

export function SubtitlePicker({
  api,
  row,
  name,
  source: sourceName,
  onClose,
  onPicked,
}: {
  api: Api
  row: SubtitleItem
  /** The row as people name it: "The Quiet Orchard · S01E08 · German". */
  name: string
  /** The subtitle source's name, as the data gives it (the search's own once it answers). */
  source: string
  onClose: () => void
  onPicked: () => void
}) {
  const [result, setResult] = useState<SubtitleSearch | null>(null)
  const [error, setError] = useState('')
  const [taking, setTaking] = useState(0)

  useEffect(() => {
    let live = true
    api
      .subtitleSearch(row.itemId, row.language)
      .then((r) => live && setResult(r))
      .catch((e) => live && setError(String((e as Error).message || e)))
    return () => {
      live = false
    }
    // the search runs once, when the picker opens
  }, [])

  async function take(c: SubtitleCandidate) {
    setTaking(c.fileId)
    setError('')
    try {
      await api.pickSubtitle(row.itemId, row.language, c)
      onPicked()
      onClose()
    } catch (e) {
      setError(String((e as Error).message || e))
    } finally {
      setTaking(0)
    }
  }

  const quota = result?.quota
  const source = result?.source || sourceName
  // the one number the header shows too: what the account has left today, which a pick takes from
  const left = result?.left ?? null
  const noneLeft = left != null && left <= 0

  const columns: TableColumn<SubtitleCandidate>[] = [
    {
      key: 'release',
      header: 'version',
      render: (c) => (
        <div>
          <div className="acq__mono">
            {c.release || c.fileName}{' '}
            {(c.aiTranslated || c.machineTranslated) && (
              <Badge tone="neutral">
                <KeepCase>AI</KeepCase>
              </Badge>
            )}{' '}
            {c.hearingImpaired && <Badge tone="neutral">hearing impaired</Badge>}{' '}
            {c.traditional && <Badge tone="neutral">traditional</Badge>}
          </div>
          {unusable(c) && <div className="acq__sub">{unusable(c)}</div>}
        </div>
      ),
    },
    { key: 'language', header: 'language', render: (c) => languageName(c, row.languageName) },
    { key: 'fromTrusted', header: 'source', render: (c) => c.source || source },
    {
      key: 'match',
      header: 'fit',
      render: (c) => <Badge tone={FIT_TONE[c.match] ?? 'amber'}>{fitWords(c.match)}</Badge>,
    },
    { key: 'downloads', header: 'downloads', align: 'right', render: (c) => formatCount(c.downloads) },
    { key: 'rating', header: 'rating', align: 'right', render: (c) => (c.votes > 0 ? c.rating.toFixed(1) : '—') },
    {
      key: 'fileId',
      header: '',
      align: 'right',
      render: (c) => (
        <Button
          size="sm"
          variant={c.best ? 'primary' : 'default'}
          disabled={!!unusable(c) || noneLeft || (taking !== 0 && taking !== c.fileId)}
          loading={taking === c.fileId}
          onClick={() => void take(c)}
        >
          use this one
        </Button>
      ),
    },
  ]

  const how: string[] = []
  if (result) {
    how.push(
      result.hash
        ? "searched by the file's fingerprint, the title and the language"
        : `searched by the title and the language${result.original ? ` — no fingerprint: ${result.original}` : ''}`,
    )
    if (result.release) how.push(`the release: ${result.release}`)
    if (quota?.known) {
      how.push(`the ${source} account: ${formatCount(Math.max(quota.remaining, 0))} of ${formatCount(quota.allowed)} downloads left today`)
    }
  }
  const using =
    left == null
      ? ' Using one takes 1 of today’s downloads.'
      : noneLeft
        ? ' None of today’s downloads are left.'
        : ` Using one takes 1 of the ${formatCount(left)} ${left === 1 ? 'download' : 'downloads'} left today.`

  return (
    <Modal open onClose={onClose} width={980} title={<KeepCase>{name}</KeepCase>}>
      {error && (
        <MessageStrip state="error" details={error}>
          {result ? 'Couldn’t add it.' : 'Couldn’t search.'}
        </MessageStrip>
      )}
      {!result && !error && <Text variant="muted">{`Searching ${source}…`}</Text>}
      {result && (
        <>
          <Text variant="muted" as="p">
            {result.hash ? 'Versions found for this file.' : 'Versions found for this title.'}
            {using}
          </Text>
          <details className="acq__details">
            <summary>details</summary>
            <div className="acq__lines">
              {how.map((l) => (
                <span key={l} className="acq__sub">
                  {l}
                </span>
              ))}
            </div>
          </details>
          <Table
            columns={columns}
            rows={result.candidates}
            rowKey={(c) => String(c.fileId)}
            dense
            empty={<Text variant="muted">{`Nothing in ${row.languageName} for this title.`}</Text>}
          />
        </>
      )}
    </Modal>
  )
}
