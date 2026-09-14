// Clients: the download programs acquire hands releases to.
//
// They run outside the addon; acquire only stores where they are and how to
// reach them, and pushes that to the gateway on every change. Credentials are
// write-only here: the screen can say a secret is stored, never show it.
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
import { Pencil, Plus, Plug, Trash2 } from 'lucide-react'
import {
  ApiError,
  type Api,
  type ClientInput,
  type ClientList,
  type ClientType,
  type DownloadClient,
} from '../lib/api'
import { ago } from '../lib/format'

function errText(e: unknown) {
  return String((e as Error).message || e)
}

/** The sync line: is the gateway running what is stored here? */
function SyncBadge({ list }: { list: ClientList }) {
  const s = list.sync
  if (!s.configured) return <Badge tone="amber" dot>no download gateway configured</Badge>
  if (!s.checked) return <Badge tone="neutral" dot>gateway not reached yet</Badge>
  if (!s.reachable) return <Badge tone="amber" dot>gateway unreachable</Badge>
  if (!s.configApi) return <Badge tone="amber" dot>gateway has no configuration API</Badge>
  if (s.gatewayRevision !== s.revision)
    return (
      <Badge tone="amber" dot>
        gateway runs revision {s.gatewayRevision}, stored {s.revision}
      </Badge>
    )
  if (s.errors && s.errors.length)
    return (
      <Badge tone="amber" dot>
        gateway refused {s.errors.map((e) => e.id).join(', ')}
      </Badge>
    )
  return <Badge tone="green" dot>gateway in sync · revision {s.revision}</Badge>
}

function LiveBadge({ c }: { c: DownloadClient }) {
  if (!c.enabled) return <Badge tone="neutral">disabled</Badge>
  if (c.applyError) return <Badge tone="amber" dot>refused · {c.applyError}</Badge>
  if (!c.status) return <Badge tone="neutral" dot>no status</Badge>
  return c.status.reachable ? (
    <Badge tone="green" dot>reachable</Badge>
  ) : (
    <Badge tone="amber" dot>
      unreachable{c.status.error ? ` · ${c.status.error}` : ''}
    </Badge>
  )
}

export function Clients({ api, admin }: { api: Api; admin: boolean }) {
  const [list, setList] = useState<ClientList | null>(null)
  const [types, setTypes] = useState<ClientType[]>([])
  const [error, setError] = useState('')
  const [note, setNote] = useState('')
  const [editing, setEditing] = useState<DownloadClient | 'new' | null>(null)
  const [busy, setBusy] = useState('')
  const [tests, setTests] = useState<Record<string, string>>({})

  const load = useCallback(async () => {
    try {
      const [l, t] = await Promise.all([api.downloadClients(), api.clientTypes()])
      setList(l)
      setTypes(t)
      setError('')
    } catch (e) {
      setError(errText(e))
    }
  }, [api])

  useEffect(() => {
    void load()
  }, [load])

  async function toggle(c: DownloadClient) {
    setBusy(c.id)
    setNote('')
    try {
      const res = await api.updateClient(c.id, toInput(c, { enabled: !c.enabled }), c.revision)
      if (!res.sync.applied) setNote(`saved, but the gateway has not applied it: ${res.sync.error || 'unknown'}`)
      await load()
    } catch (e) {
      setError(errText(e))
    } finally {
      setBusy('')
    }
  }

  async function remove(c: DownloadClient) {
    if (!window.confirm(`Remove the download client ${c.id}? Downloads it already finished are kept.`)) return
    setBusy(c.id)
    setNote('')
    try {
      await api.deleteClient(c.id, c.revision)
      await load()
    } catch (e) {
      setError(errText(e))
    } finally {
      setBusy('')
    }
  }

  async function test(c: DownloadClient) {
    setTests((t) => ({ ...t, [c.id]: 'testing…' }))
    try {
      // No secret in the body: the stored one is used.
      const r = await api.testClient({ ...toInput(c, {}), id: c.id })
      setTests((t) => ({
        ...t,
        [c.id]: r.reachable ? `reachable${r.version ? ` · version ${r.version}` : ''}` : `unreachable · ${r.error}`,
      }))
    } catch (e) {
      setTests((t) => ({ ...t, [c.id]: errText(e) }))
    }
  }

  if (!admin) return <Text variant="muted">download clients are managed by an admin.</Text>
  if (error && !list) return <Text variant="muted">{error}</Text>
  if (!list) return <Text variant="muted">loading…</Text>

  const columns: TableColumn<DownloadClient>[] = [
    {
      key: 'id',
      header: 'client',
      render: (c) => (
        <div>
          <span className="acq__mono">{c.id}</span>{' '}
          <Text variant="muted" as="span">
            {c.type}
          </Text>
          <div className="acq__sub">{c.baseUrl}</div>
        </div>
      ),
    },
    {
      key: 'protocols',
      header: 'handles',
      render: (c) => (
        <div className="acq__actions" style={{ justifyContent: 'flex-start' }}>
          {c.protocols.map((p) => (
            <Badge key={p} tone={p === 'usenet' ? 'green' : 'blue'}>
              {p === 'usenet' ? 'NZB' : p}
            </Badge>
          ))}
        </div>
      ),
    },
    {
      key: 'remotePath',
      header: 'folders',
      render: (c) =>
        c.remotePath ? (
          <div className="acq__sub">
            {c.remotePath}
            {c.localPath ? ` → ${c.localPath}` : ' (same path for acquire)'}
          </div>
        ) : (
          <div className="acq__sub">client default</div>
        ),
    },
    { key: 'priority', header: 'priority', align: 'right', render: (c) => <span className="acq__sub">{c.priority}</span> },
    {
      key: 'secret',
      header: 'credentials',
      render: (c) =>
        c.auth === 'none' ? (
          <Text variant="muted">none</Text>
        ) : c.secret.set ? (
          <Badge tone="green">stored{c.secret.updatedAt ? ` · ${ago(c.secret.updatedAt)}` : ''}</Badge>
        ) : (
          <Badge tone="amber">missing</Badge>
        ),
    },
    {
      key: 'enabled',
      header: 'state',
      render: (c) => (
        <div>
          <LiveBadge c={c} />
          {tests[c.id] && <div className="acq__sub">{tests[c.id]}</div>}
        </div>
      ),
    },
    {
      key: 'updatedAt',
      header: '',
      align: 'right',
      render: (c) => (
        <div className="acq__actions">
          <Switch
            size="sm"
            checked={c.enabled}
            disabled={busy === c.id}
            onChange={() => void toggle(c)}
            aria-label={c.enabled ? `disable ${c.id}` : `enable ${c.id}`}
          />
          <Button size="sm" variant="ghost" leading={<Plug size={14} />} onClick={() => void test(c)}>
            test
          </Button>
          <Button size="sm" variant="ghost" leading={<Pencil size={14} />} onClick={() => setEditing(c)}>
            edit
          </Button>
          <Button
            size="sm"
            variant="ghost"
            loading={busy === c.id}
            leading={<Trash2 size={14} />}
            onClick={() => void remove(c)}
          >
            remove
          </Button>
        </div>
      ),
    },
  ]

  return (
    <>
      <div className="acq__settings-head" style={{ maxWidth: 'none', marginBottom: 'var(--s-3)' }}>
        <div className="acq__chips" style={{ marginBottom: 0 }}>
          <SyncBadge list={list} />
          {!list.keyConfigured && (
            <Badge tone="amber" dot>
              ACQUIRE_CONFIG_KEY is not set — credentials cannot be stored
            </Badge>
          )}
        </div>
        <Button variant="primary" leading={<Plus size={14} />} onClick={() => setEditing('new')}>
          add client
        </Button>
      </div>
      <Text variant="muted" as="p">
        a grab goes to the enabled client that handles its protocol, lowest priority first. the
        clients themselves run outside acquire.
      </Text>
      {error && <Text variant="muted">{error}</Text>}
      {note && <Text variant="muted">{note}</Text>}
      <Table
        columns={columns}
        rows={list.clients}
        rowKey={(c) => c.id}
        empty={<Text variant="muted">no download client yet — add one so grabs have somewhere to go.</Text>}
      />
      {editing && (
        <ClientDialog
          api={api}
          types={types}
          client={editing === 'new' ? null : editing}
          existing={list.clients}
          onClose={() => setEditing(null)}
          onSaved={(msg) => {
            setEditing(null)
            setNote(msg)
            void load()
          }}
        />
      )}
    </>
  )
}

/** toInput turns a stored client back into a write, without its secret. */
function toInput(c: DownloadClient, patch: Partial<ClientInput>): ClientInput {
  return {
    baseUrl: c.baseUrl,
    auth: c.auth,
    username: c.username,
    protocols: c.protocols,
    category: c.category,
    remotePath: c.remotePath,
    localPath: c.localPath,
    priority: c.priority,
    enabled: c.enabled,
    ...patch,
  }
}

function defaultId(type: string, existing: DownloadClient[]): string {
  const taken = new Set(existing.map((c) => c.id))
  if (!taken.has(type)) return type
  for (let n = 2; ; n++) if (!taken.has(`${type}-${n}`)) return `${type}-${n}`
}

function ClientDialog({
  api,
  types,
  client,
  existing,
  onClose,
  onSaved,
}: {
  api: Api
  types: ClientType[]
  client: DownloadClient | null
  existing: DownloadClient[]
  onClose: () => void
  onSaved: (note: string) => void
}) {
  const creating = client === null
  const [type, setType] = useState(client?.type || types[0]?.type || '')
  const typeInfo = useMemo(() => types.find((t) => t.type === type), [types, type])
  const [id, setId] = useState(client?.id || '')
  const [baseUrl, setBaseUrl] = useState(client?.baseUrl || '')
  const [auth, setAuth] = useState<string>(client?.auth || typeInfo?.auth[0] || 'none')
  const [username, setUsername] = useState(client?.username || '')
  // Never prefilled: a stored secret is only ever replaced or removed.
  const [secret, setSecret] = useState('')
  const [clearSecret, setClearSecret] = useState(false)
  const [protocols, setProtocols] = useState<string[]>(client?.protocols || typeInfo?.protocols || [])
  const [category, setCategory] = useState(client?.category || 'acquire')
  const [remotePath, setRemotePath] = useState(client?.remotePath || '')
  const [localPath, setLocalPath] = useState(client?.localPath || '')
  const [priority, setPriority] = useState(String(client?.priority ?? 0))
  const [enabled, setEnabled] = useState(client?.enabled ?? true)
  const [fields, setFields] = useState<Record<string, string>>({})
  const [error, setError] = useState('')
  const [saving, setSaving] = useState(false)
  const [testing, setTesting] = useState(false)
  const [testResult, setTestResult] = useState('')

  // A new type brings its own auth modes and protocols.
  function chooseType(t: string) {
    setType(t)
    const info = types.find((x) => x.type === t)
    if (info) {
      setAuth(info.auth[0] || 'none')
      setProtocols(info.protocols)
    }
  }

  function body(): ClientInput {
    const b: ClientInput = {
      baseUrl: baseUrl.trim(),
      auth,
      username: auth === 'basic' ? username.trim() : '',
      protocols,
      category: category.trim(),
      remotePath: remotePath.trim(),
      localPath: localPath.trim(),
      priority: Number(priority) || 0,
      enabled,
    }
    if (creating) {
      b.type = type
      if (id.trim()) b.id = id.trim()
    }
    if (auth !== 'none') {
      if (secret) b.secret = secret
      else if (clearSecret) b.secret = { clear: true }
    }
    return b
  }

  async function save() {
    setSaving(true)
    setError('')
    setFields({})
    try {
      const res = creating
        ? await api.createClient(body())
        : await api.updateClient(client!.id, body(), client!.revision)
      setSecret('')
      setClearSecret(false)
      onSaved(
        res.sync.applied
          ? `${res.id} saved and live on the gateway.`
          : `${res.id} saved, but the gateway has not applied it yet: ${res.sync.error || 'unknown'}`,
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
      const r = await api.testClient({ ...body(), id: client?.id || id.trim() || undefined, type })
      setTestResult(
        r.reachable ? `reachable${r.version ? ` · version ${r.version}` : ''}` : `unreachable · ${r.error}`,
      )
    } catch (e) {
      if (e instanceof ApiError && e.status === 422) setFields(e.fields)
      setTestResult(errText(e))
    } finally {
      setTesting(false)
    }
  }

  const stored = !!client?.secret.set
  const suggested = creating && type ? defaultId(type, existing) : ''

  return (
    <Modal
      open
      onClose={onClose}
      width={640}
      title={creating ? 'add download client' : `edit ${client!.id}`}
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
          <Field label="type" error={fields.type}>
            <Select
              value={type}
              disabled={!creating}
              onChange={(e) => chooseType(e.currentTarget.value)}
              options={types.map((t) => ({ label: t.type, value: t.type }))}
            />
          </Field>
          <Field
            label="id"
            error={fields.id}
            hint={creating ? 'fixed once saved; the first client of a type is named after it' : 'fixed'}
          >
            <Input
              value={creating ? id : client!.id}
              disabled={!creating}
              placeholder={suggested}
              onChange={(e) => setId(e.currentTarget.value)}
            />
          </Field>
        </div>

        <Field label="address" error={fields.baseUrl} hint="where the download gateway reaches the client, e.g. http://worker:6789">
          <Input value={baseUrl} onChange={(e) => setBaseUrl(e.currentTarget.value)} />
        </Field>

        <div className="acq__settings-row">
          <Field label="authentication" error={fields.auth}>
            <Select
              value={auth}
              onChange={(e) => setAuth(e.currentTarget.value)}
              options={(typeInfo?.auth.length ? typeInfo.auth : ['basic', 'token', 'none']).map((a) => ({
                label: a,
                value: a,
              }))}
            />
          </Field>
          {auth === 'basic' && (
            <Field label="username" error={fields.username}>
              <Input value={username} autoComplete="off" onChange={(e) => setUsername(e.currentTarget.value)} />
            </Field>
          )}
        </div>

        {auth !== 'none' && (
          <Field
            label={auth === 'token' ? 'token' : 'password'}
            error={fields.secret}
            hint={stored ? 'a secret is stored — type to replace it' : 'stored encrypted; never shown again'}
          >
            <Input
              type="password"
              autoComplete="new-password"
              value={secret}
              placeholder={stored ? '••••••••' : ''}
              trailing={stored && !secret && !clearSecret ? <Badge tone="green">stored</Badge> : undefined}
              onChange={(e) => {
                setSecret(e.currentTarget.value)
                if (e.currentTarget.value) setClearSecret(false)
              }}
            />
          </Field>
        )}
        {auth !== 'none' && stored && !secret && (
          <Checkbox
            checked={clearSecret}
            onChange={(e) => setClearSecret(e.currentTarget.checked)}
            label="remove the stored secret"
          />
        )}

        <Field label="handles" error={fields.protocols}>
          <div className="acq__actions" style={{ justifyContent: 'flex-start' }}>
            {(typeInfo?.protocols || protocols).map((p) => (
              <Checkbox
                key={p}
                checked={protocols.includes(p)}
                label={p}
                onChange={(e) =>
                  setProtocols((prev) =>
                    e.currentTarget.checked ? [...prev, p] : prev.filter((x) => x !== p),
                  )
                }
              />
            ))}
          </div>
        </Field>

        <Field
          label="save folder as the client sees it"
          error={fields.remotePath}
          hint="where the client writes downloads, e.g. /downloads — empty uses the client's default"
        >
          <Input value={remotePath} onChange={(e) => setRemotePath(e.currentTarget.value)} />
        </Field>
        <Field
          label="same folder as acquire sees it"
          error={fields.localPath}
          hint="the path of that folder inside acquire, so finished files can be found — empty when it is the same path"
        >
          <Input value={localPath} onChange={(e) => setLocalPath(e.currentTarget.value)} />
        </Field>

        <div className="acq__settings-row">
          <Field label="category" error={fields.category}>
            <Input value={category} onChange={(e) => setCategory(e.currentTarget.value)} />
          </Field>
          <Field label="priority" error={fields.priority} hint="lower is tried first">
            <Input type="number" value={priority} onChange={(e) => setPriority(e.currentTarget.value)} />
          </Field>
        </div>
        <Switch checked={enabled} onChange={(e) => setEnabled(e.currentTarget.checked)} label="enabled" />
      </div>
    </Modal>
  )
}
