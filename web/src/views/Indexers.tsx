// Search sources: the newznab and torznab endpoints acquire searches.
//
// acquire asks each source directly, with the source's own API key. The key is
// write-only here: the screen can say one is stored, never show it. Health
// comes from the searches themselves — a rejected key disables a source, a rate
// limit or an outage backs it off for a while.
import { useCallback, useEffect, useMemo, useState } from 'react'
import {
  Badge,
  Button,
  Checkbox,
  Field,
  Input,
  Modal,
  Select,
  Switch,
  Table,
  Text,
  type TableColumn,
} from '@nalet/design-system'
import { Pencil, Plug, Plus, Trash2 } from 'lucide-react'
import {
  ApiError,
  type Api,
  type Caps,
  type CapsCategory,
  type Source,
  type SourceInput,
  type SourceList,
  type SourceTestResult,
} from '../lib/api'
import { ago } from '../lib/format'

function errText(e: unknown) {
  return String((e as Error).message || e)
}

function time(iso: string | null) {
  return iso ? new Date(iso).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }) : ''
}

function HealthBadge({ s }: { s: Source }) {
  const h = s.health
  switch (h.state) {
    case 'disabled':
      return <Badge tone="neutral">disabled</Badge>
    case 'key':
      return (
        <Badge tone="amber" dot>
          key unreadable
        </Badge>
      )
    case 'backoff':
      return (
        <Badge tone="amber" dot>
          backing off until {time(h.backoffUntil)}
        </Badge>
      )
    case 'limit':
      return (
        <Badge tone="amber" dot>
          daily limit reached
        </Badge>
      )
    case 'failing':
      return (
        <Badge tone="amber" dot>
          {h.failures} failed
        </Badge>
      )
  }
  return (
    <Badge tone="green" dot>
      ok
    </Badge>
  )
}

function testLine(r: SourceTestResult): string {
  if (r.ok) return `answers · ${r.items} release${r.items === 1 ? '' : 's'} in a sample search`
  return `failed · ${r.error || r.capsError || 'unknown'}`
}

export function Indexers({
  api,
  admin,
  preferProtocol,
  onChanged,
}: {
  api: Api
  admin: boolean
  preferProtocol: 'usenet' | 'torrent'
  onChanged: () => void
}) {
  const [list, setList] = useState<SourceList | null>(null)
  const [error, setError] = useState('')
  const [note, setNote] = useState('')
  const [editing, setEditing] = useState<Source | 'new' | null>(null)
  const [busy, setBusy] = useState(0)
  const [tests, setTests] = useState<Record<number, string>>({})

  const load = useCallback(async () => {
    try {
      setList(await api.sources())
      setError('')
    } catch (e) {
      setError(errText(e))
    }
  }, [api])

  useEffect(() => {
    if (admin) void load()
  }, [admin, load])

  async function changed(msg = '') {
    setNote(msg)
    await load()
    onChanged()
  }

  async function toggle(s: Source) {
    setBusy(s.id)
    setNote('')
    try {
      await api.updateSource(s.id, toInput(s, { enabled: !s.enabled }), s.revision)
      await changed()
    } catch (e) {
      setError(errText(e))
    } finally {
      setBusy(0)
    }
  }

  async function remove(s: Source) {
    if (!window.confirm(`Remove the search source ${s.name}? Grabs made from it keep their history.`)) return
    setBusy(s.id)
    try {
      await api.deleteSource(s.id, s.revision)
      await changed()
    } catch (e) {
      setError(errText(e))
    } finally {
      setBusy(0)
    }
  }

  async function test(s: Source) {
    setTests((t) => ({ ...t, [s.id]: 'testing…' }))
    try {
      const r = await api.testStoredSource(s.id)
      setTests((t) => ({ ...t, [s.id]: testLine(r) }))
      await load()
    } catch (e) {
      setTests((t) => ({ ...t, [s.id]: errText(e) }))
    }
  }

  if (!admin) return <Text variant="muted">search sources are managed by an admin.</Text>
  if (error && !list) return <Text variant="muted">{error}</Text>
  if (!list) return <Text variant="muted">loading…</Text>

  const columns: TableColumn<Source>[] = [
    {
      key: 'name',
      header: 'source',
      render: (s) => (
        <div>
          <span className="acq__mono">{s.name}</span>{' '}
          <Badge tone={s.protocol === 'usenet' ? 'green' : 'blue'}>{s.protocol === 'usenet' ? 'NZB' : 'torrent'}</Badge>
          <div className="acq__sub">
            {s.baseUrl.replace(/\/+$/, '')}
            {s.apiPath}
          </div>
          {s.caps?.server.title && <div className="acq__sub">{s.caps.server.title}</div>}
        </div>
      ),
    },
    {
      key: 'categories',
      header: 'categories',
      render: (s) => (
        <div className="acq__sub">
          movies {s.categories.movie.join(', ') || 'all'}
          <br />
          tv {s.categories.tv.join(', ') || 'all'}
        </div>
      ),
    },
    {
      key: 'usage',
      header: 'today',
      render: (s) => (
        <div className="acq__sub">
          {s.usage.queries}
          {s.queryLimitDay ? ` / ${s.queryLimitDay}` : ''} queries
          <br />
          {s.usage.grabs}
          {s.grabLimitDay ? ` / ${s.grabLimitDay}` : ''} downloads
        </div>
      ),
    },
    { key: 'priority', header: 'priority', align: 'right', render: (s) => <span className="acq__sub">{s.priority}</span> },
    {
      key: 'apiKey',
      header: 'api key',
      render: (s) =>
        s.apiKey.set ? (
          <Badge tone="green">stored{s.apiKey.updatedAt ? ` · ${ago(s.apiKey.updatedAt)}` : ''}</Badge>
        ) : (
          <Text variant="muted">none</Text>
        ),
    },
    {
      key: 'health',
      header: 'health',
      render: (s) => (
        <div>
          <HealthBadge s={s} />
          {/* Errors are plain text from the API; React escapes them. */}
          {s.health.lastError && <div className="acq__sub">{s.health.lastError}</div>}
          {tests[s.id] && <div className="acq__sub">{tests[s.id]}</div>}
        </div>
      ),
    },
    {
      key: 'updatedAt',
      header: '',
      align: 'right',
      render: (s) => (
        <div className="acq__actions">
          <Switch
            size="sm"
            checked={s.enabled}
            disabled={busy === s.id}
            onChange={() => void toggle(s)}
            aria-label={s.enabled ? `disable ${s.name}` : `enable ${s.name}`}
          />
          <Button size="sm" variant="ghost" leading={<Plug size={14} />} onClick={() => void test(s)}>
            test
          </Button>
          <Button size="sm" variant="ghost" leading={<Pencil size={14} />} onClick={() => setEditing(s)}>
            edit
          </Button>
          <Button
            size="sm"
            variant="ghost"
            loading={busy === s.id}
            leading={<Trash2 size={14} />}
            onClick={() => void remove(s)}
          >
            remove
          </Button>
        </div>
      ),
    },
  ]

  const enabled = list.sources.filter((s) => s.enabled)
  const usenet = enabled.filter((s) => s.protocol === 'usenet').length
  const torrent = enabled.filter((s) => s.protocol === 'torrent').length

  return (
    <>
      <div className="acq__settings-head" style={{ maxWidth: 'none', marginBottom: 'var(--s-3)' }}>
        <div className="acq__chips" style={{ marginBottom: 0 }}>
          <Badge tone={enabled.length ? 'green' : 'amber'} dot>
            {usenet} usenet · {torrent} torrent enabled
          </Badge>
          {!list.keyConfigured && (
            <Badge tone="amber" dot>
              ACQUIRE_CONFIG_KEY is not set — API keys cannot be stored
            </Badge>
          )}
        </div>
        <Button variant="primary" leading={<Plus size={14} />} onClick={() => setEditing('new')}>
          add source
        </Button>
      </div>
      <Text variant="muted" as="p">
        {preferProtocol === 'usenet'
          ? 'auto-grab asks the usenet sources first, and the torrent sources only when they offer nothing usable.'
          : 'auto-grab asks the torrent sources first, and the usenet sources only when they offer nothing usable.'}{' '}
        within a protocol, lower priority is asked first. keys are used by acquire only — download clients receive
        the release file, never a source link.
      </Text>
      {error && <Text variant="muted">{error}</Text>}
      {note && <Text variant="muted">{note}</Text>}
      <Table
        columns={columns}
        rows={list.sources}
        rowKey={(s) => String(s.id)}
        empty={<Text variant="muted">no search source yet — add one so there is something to search.</Text>}
      />
      {editing && (
        <SourceDialog
          api={api}
          source={editing === 'new' ? null : editing}
          onClose={() => setEditing(null)}
          onSaved={(msg) => {
            setEditing(null)
            void changed(msg)
          }}
        />
      )}
    </>
  )
}

/** toInput turns a stored source back into a write, without its key. */
function toInput(s: Source, patch: Partial<SourceInput>): SourceInput {
  return {
    name: s.name,
    protocol: s.protocol,
    baseUrl: s.baseUrl,
    apiPath: s.apiPath,
    categories: s.categories,
    priority: s.priority,
    enabled: s.enabled,
    queryLimitDay: s.queryLimitDay,
    grabLimitDay: s.grabLimitDay,
    ...patch,
  }
}

function parseIds(text: string): number[] {
  const out: number[] = []
  for (const part of text.split(/[\s,]+/)) {
    const n = Number(part)
    if (part && Number.isInteger(n) && !out.includes(n)) out.push(n)
  }
  return out
}

function parseLimit(text: string): number | null {
  const t = text.trim()
  return t === '' ? null : Number(t)
}

/** The caps categories in a newznab range (2000s movies, 5000s tv), flattened. */
function categoriesIn(caps: Caps | null | undefined, lo: number, hi: number): CapsCategory[] {
  const out: CapsCategory[] = []
  for (const c of caps?.categories ?? []) {
    if (c.id >= lo && c.id <= hi) out.push(c)
    for (const sub of c.subcats ?? []) if (sub.id >= lo && sub.id <= hi) out.push(sub)
  }
  return out
}

function CategoryField({
  label,
  text,
  onText,
  options,
  error,
}: {
  label: string
  text: string
  onText: (t: string) => void
  options: CapsCategory[]
  error?: string
}) {
  const ids = parseIds(text)
  function toggle(id: number, on: boolean) {
    const next = on ? [...ids, id] : ids.filter((x) => x !== id)
    onText(next.join(', '))
  }
  return (
    <Field
      label={label}
      error={error}
      hint={options.length ? 'from the source’s caps; empty searches every category' : 'category ids, comma separated; test to list the source’s own'}
    >
      <div className="acq__form">
        <Input value={text} onChange={(e) => onText(e.currentTarget.value)} />
        {options.length > 0 && (
          <div className="acq__scope-list" style={{ marginTop: 0 }}>
            {options.map((o) => (
              <Checkbox
                key={o.id}
                checked={ids.includes(o.id)}
                label={`${o.name} (${o.id})`}
                onChange={(e) => toggle(o.id, e.currentTarget.checked)}
              />
            ))}
          </div>
        )}
      </div>
    </Field>
  )
}

function SourceDialog({
  api,
  source,
  onClose,
  onSaved,
}: {
  api: Api
  source: Source | null
  onClose: () => void
  onSaved: (note: string) => void
}) {
  const creating = source === null
  const [name, setName] = useState(source?.name || '')
  const [protocol, setProtocol] = useState<string>(source?.protocol || 'usenet')
  const [baseUrl, setBaseUrl] = useState(source?.baseUrl || '')
  const [apiPath, setApiPath] = useState(source?.apiPath || '/api')
  // Never prefilled: a stored key is only ever replaced or removed.
  const [apiKey, setApiKey] = useState('')
  const [clearKey, setClearKey] = useState(false)
  const [movie, setMovie] = useState((source?.categories.movie ?? [2000]).join(', '))
  const [tv, setTv] = useState((source?.categories.tv ?? [5000]).join(', '))
  const [priority, setPriority] = useState(String(source?.priority ?? 0))
  const [queryLimit, setQueryLimit] = useState(source?.queryLimitDay != null ? String(source.queryLimitDay) : '')
  const [grabLimit, setGrabLimit] = useState(source?.grabLimitDay != null ? String(source.grabLimitDay) : '')
  const [enabled, setEnabled] = useState(source?.enabled ?? true)
  const [caps, setCaps] = useState<Caps | null>(source?.caps ?? null)
  const [fields, setFields] = useState<Record<string, string>>({})
  const [error, setError] = useState('')
  const [saving, setSaving] = useState(false)
  const [testing, setTesting] = useState(false)
  const [testResult, setTestResult] = useState('')

  const movieOptions = useMemo(() => categoriesIn(caps, 2000, 2999), [caps])
  const tvOptions = useMemo(() => categoriesIn(caps, 5000, 5999), [caps])

  function body(): SourceInput {
    const b: SourceInput = {
      name: name.trim(),
      protocol,
      baseUrl: baseUrl.trim(),
      apiPath: apiPath.trim() || '/api',
      categories: { movie: parseIds(movie), tv: parseIds(tv) },
      priority: Number(priority) || 0,
      enabled,
      queryLimitDay: parseLimit(queryLimit),
      grabLimitDay: parseLimit(grabLimit),
    }
    if (apiKey) b.apiKey = apiKey
    else if (clearKey) b.apiKey = { clear: true }
    return b
  }

  async function save() {
    setSaving(true)
    setError('')
    setFields({})
    try {
      const res = creating ? await api.createSource(body()) : await api.updateSource(source!.id, body(), source!.revision)
      setApiKey('')
      setClearKey(false)
      onSaved(
        res.capsError
          ? `${res.name} saved, but its caps could not be read: ${res.capsError}`
          : `${res.name} saved.`,
      )
    } catch (e) {
      if (e instanceof ApiError && e.status === 422) {
        setFields(e.fields)
        setError('check the highlighted fields.')
      } else {
        setError(errText(e))
      }
    } finally {
      setSaving(false)
    }
  }

  async function test() {
    setTesting(true)
    setTestResult('')
    setFields({})
    try {
      // An edit without a new key is tested with the stored one.
      const r = await api.testSource({ ...body(), id: source?.id })
      if (r.caps) setCaps(r.caps)
      setTestResult(testLine(r))
    } catch (e) {
      if (e instanceof ApiError && e.status === 422) setFields(e.fields)
      setTestResult(errText(e))
    } finally {
      setTesting(false)
    }
  }

  const stored = !!source?.apiKey.set

  return (
    <Modal
      open
      onClose={onClose}
      width={680}
      title={creating ? 'add search source' : `edit ${source!.name}`}
      footer={
        <div className="acq__actions">
          {testResult && <Text variant="muted">{testResult}</Text>}
          <Button variant="ghost" loading={testing} leading={<Plug size={14} />} onClick={() => void test()}>
            test
          </Button>
          <Button variant="ghost" onClick={onClose}>
            cancel
          </Button>
          <Button variant="primary" loading={saving} onClick={() => void save()}>
            save
          </Button>
        </div>
      }
    >
      <div className="acq__form">
        {error && <Text variant="muted">{error}</Text>}
        <div className="acq__settings-row">
          <Field label="name" error={fields.name}>
            <Input value={name} onChange={(e) => setName(e.currentTarget.value)} />
          </Field>
          <Field label="protocol" error={fields.protocol}>
            <Select
              value={protocol}
              onChange={(e) => setProtocol(e.currentTarget.value)}
              options={[
                { label: 'usenet (newznab)', value: 'usenet' },
                { label: 'torrent (torznab)', value: 'torrent' },
              ]}
            />
          </Field>
        </div>

        <div className="acq__settings-row">
          <Field label="address" error={fields.baseUrl} hint="the source’s base address, without the API path or key">
            <Input value={baseUrl} placeholder="https://" onChange={(e) => setBaseUrl(e.currentTarget.value)} />
          </Field>
          <Field label="api path" error={fields.apiPath} hint="/api for most sources">
            <Input value={apiPath} onChange={(e) => setApiPath(e.currentTarget.value)} />
          </Field>
        </div>

        <Field
          label="api key"
          error={fields.apiKey}
          hint={stored ? 'a key is stored — type to replace it' : 'stored encrypted; never shown again; empty for a public source'}
        >
          <Input
            type="password"
            autoComplete="new-password"
            value={apiKey}
            placeholder={stored ? '••••••••' : ''}
            trailing={stored && !apiKey && !clearKey ? <Badge tone="green">stored</Badge> : undefined}
            onChange={(e) => {
              setApiKey(e.currentTarget.value)
              if (e.currentTarget.value) setClearKey(false)
            }}
          />
        </Field>
        {stored && !apiKey && (
          <Checkbox checked={clearKey} onChange={(e) => setClearKey(e.currentTarget.checked)} label="remove the stored key" />
        )}

        {caps && (
          <Text variant="muted" as="div">
            {caps.server.title || 'caps read'}
            {caps.tvSearch.available ? ` · tv search: ${caps.tvSearch.params.join(', ')}` : ''}
            {caps.movieSearch.available ? ` · movie search: ${caps.movieSearch.params.join(', ')}` : ''}
          </Text>
        )}
        <CategoryField label="movie categories" text={movie} onText={setMovie} options={movieOptions} error={fields.categories} />
        <CategoryField label="tv categories" text={tv} onText={setTv} options={tvOptions} />

        <div className="acq__settings-row">
          <Field label="priority" error={fields.priority} hint="lower is asked first">
            <Input type="number" value={priority} onChange={(e) => setPriority(e.currentTarget.value)} />
          </Field>
          <Field label="queries per day" error={fields.queryLimitDay} hint="empty for no limit">
            <Input type="number" value={queryLimit} onChange={(e) => setQueryLimit(e.currentTarget.value)} />
          </Field>
          <Field label="downloads per day" error={fields.grabLimitDay} hint="empty for no limit">
            <Input type="number" value={grabLimit} onChange={(e) => setGrabLimit(e.currentTarget.value)} />
          </Field>
        </div>
        <Switch checked={enabled} onChange={(e) => setEnabled(e.currentTarget.checked)} label="enabled" />
      </div>
    </Modal>
  )
}
