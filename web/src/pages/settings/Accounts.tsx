import React, { useEffect, useState } from 'react'
import { api, ApiError, V1 } from '../../lib/api'
import { useApp } from '../../lib/app-context'
import { ago } from '../../lib/contacts'
import type { Account } from '../../lib/types'
import { Icon } from '../../components/icons'
import { type Option, Select } from '../../components/Select'
import { Empty, errMsg, Field, Loading, MenuButton, Modal, Segmented, Spinner, Switch, useAsync, useToast } from '../../components/ui'
import { Section, StatusDot } from './common'

type Kind = 'nextcloud' | 'icloud' | 'carddav'
type Via = 'core' | 'direct'
type SyncMode = Account['mode']

const PROVIDERS: { id: Kind; name: string; desc: string; icon: string; color: string }[] = [
  { id: 'nextcloud', name: 'Nextcloud', desc: 'With an app password, straight to your Nextcloud', icon: 'server', color: '#0082c9' },
  { id: 'icloud', name: 'iCloud', desc: 'Apple contacts, with an app-specific password', icon: 'smartphone', color: '#0a84ff' },
  { id: 'carddav', name: 'Other CardDAV', desc: 'Fastmail, Google (CardDAV), Radicale, Baïkal, …', icon: 'globe', color: '#64748b' },
]
const KIND_NAME: Record<string, string> = { nextcloud: 'Nextcloud', icloud: 'iCloud', carddav: 'CardDAV' }
const MODES: { value: SyncMode; label: string; description: string }[] = [
  { value: 'two_way', label: 'Keep in sync', description: 'Changes on either side reach the other.' },
  { value: 'import', label: 'Import once', description: 'Copy them here once; afterwards they live here only.' },
  { value: 'read_only', label: 'Mirror, read-only', description: 'Follow the other side; no changes from here.' },
]
const modeOptions: Option<SyncMode>[] = [...MODES, { value: 'off', label: 'Off', description: 'No syncing; its books are ordinary ones here.' }]
const SYNC: Option<number>[] = [5, 15, 30, 60, 360, 1440].map(m => ({ value: m, label: m < 60 ? `Every ${m} minutes` : m === 60 ? 'Every hour' : m === 360 ? 'Every 6 hours' : 'Once a day' }))
const modeText = (m: string) => MODES.find(x => x.value === m)?.label || (m === 'off' ? 'Not syncing' : m)

interface Discovered { href: string; name: string; count: number; read_only?: boolean }
interface NextcloudCore { core: boolean; detail: string; url: string; username: string }

export function AccountsTab() {
  const { reloadBooks, bump } = useApp()
  const { toast } = useToast()
  const list = useAsync(() => api.get<Account[]>(`${V1}/accounts`), [])
  const nc = useAsync(() => api.get<NextcloudCore>(`${V1}/accounts/nextcloud`), [])
  const [wizard, setWizard] = useState<{ kind?: Kind; via?: Via } | null>(null)
  const [edit, setEdit] = useState<Account | null>(null)
  const [del, setDel] = useState<Account | null>(null)
  const [syncing, setSyncing] = useState<number | null>(null)
  const accounts = list.data || []
  const after = () => { list.reload(); reloadBooks().catch(() => {}); bump() }
  const put = async (a: Account, body: Partial<Account> & { password?: string }, ok?: string) => {
    list.setData(xs => (xs || []).map(x => x.id === a.id ? { ...x, ...body } : x))
    try { await api.put(`${V1}/accounts/${a.id}`, body); if (ok) toast(ok); after() } catch (e) { toast(errMsg(e), 'err'); list.reload() }
  }
  const sync = async (a: Account) => {
    setSyncing(a.id)
    try { const r = await api.post<Account>(`${V1}/accounts/${a.id}/sync`); toast(r?.last_error ? `Synced with a problem: ${r.last_error}` : r?.syncing ? 'Still syncing in the background' : 'Synced', r?.last_error ? 'err' : 'ok'); after() } catch (e) { toast(errMsg(e), 'err') } finally { setSyncing(null) }
  }
  const hasCoreNC = accounts.some(a => a.kind === 'nextcloud' && a.via === 'core')
  const anySyncing = accounts.some(a => a.syncing)
  useEffect(() => {
    if (!anySyncing) return
    const t = setTimeout(async () => { const l = await api.get<Account[]>(`${V1}/accounts`).catch(() => null); if (l) { list.setData(l); if (!l.some(a => a.syncing)) { reloadBooks().catch(() => {}); bump() } } }, 3000)
    return () => clearTimeout(t)
  }, [anySyncing, list.data]) // eslint-disable-line react-hooks/exhaustive-deps

  return (
    <div className="stack loose">
      {nc.data?.core && !hasCoreNC && (
        <section className="nc-hero">
          <span className="nc-ico"><Icon name="server" size={22} /></span>
          <div className="grow">
            <h3>Your Nextcloud contacts are one click away</h3>
            <p className="muted small">Pocket Core already has your Nextcloud login{nc.data.username ? <> (<b>{nc.data.username}</b>{nc.data.url ? ` at ${hostOf(nc.data.url)}` : ''})</> : null}. Pick the address books, then import them once or keep them in sync both ways.</p>
          </div>
          <button type="button" className="btn primary" onClick={() => setWizard({ kind: 'nextcloud', via: 'core' })}><Icon name="download" size={15} />Import from Nextcloud</button>
        </section>
      )}

      <Section title="Connected accounts" sub="Address books from other servers, mirrored here and — if you want — kept in sync both ways."
        actions={<button type="button" className="btn primary sm" onClick={() => setWizard({})}><Icon name="plus" size={14} />Add account</button>}>
        {list.loading && !list.data ? <Loading /> : list.error ? (
          <div className="callout danger"><Icon name="alert" size={15} /><span>{list.error}</span></div>
        ) : !accounts.length ? (
          <Empty title="No accounts yet" icon="refresh">Connect Nextcloud, iCloud or any CardDAV server to bring those contacts here.</Empty>
        ) : (
          <div className="acct-list">
            {accounts.map(a => {
              const status: 'ok' | 'error' | 'off' = !a.enabled || a.mode === 'off' ? 'off' : a.last_error ? 'error' : 'ok'
              return (
                <div key={a.id} className="acct-row">
                  <span className="acct-ico"><Icon name={a.kind === 'icloud' ? 'smartphone' : a.kind === 'nextcloud' ? 'server' : 'globe'} size={17} /></span>
                  <div className="grow" style={{ minWidth: 0 }}>
                    <div className="acct-name"><span className="ellipsis">{a.name || a.url || KIND_NAME[a.kind]}</span><span className="badge">{KIND_NAME[a.kind] || a.kind}{a.via === 'core' ? ' · via Core' : ''}</span></div>
                    <div className="acct-meta ellipsis">{[a.username, a.via === 'core' ? '' : hostOf(a.url)].filter(Boolean).join(' · ')}</div>
                    <div className="acct-meta">
                      <StatusDot status={status} title={a.last_error || undefined} />
                      {syncing === a.id || a.syncing ? 'Syncing…' : a.mode === 'import' && a.last_sync_at ? `Imported ${ago(a.last_sync_at)}` : `${modeText(a.mode)} · synced ${ago(a.last_sync_at)}`} · {a.addressbooks} book{a.addressbooks === 1 ? '' : 's'}
                    </div>
                    {!!a.addressbook_list?.length && (
                      <div className="acct-books">
                        {a.addressbook_list.map(b => <span key={b.id} className="acct-book" title={b.read_only ? 'Read-only here' : undefined}><span className="dot" style={{ background: b.color }} />{b.name}<span className="faint tnum">{b.contacts}</span>{b.read_only && <Icon name="lock" size={10} />}</span>)}
                      </div>
                    )}
                    {a.last_error && <div className="acct-error">{a.last_error}</div>}
                  </div>
                  <div className="acct-actions">
                    <Select size="sm" value={a.mode} options={modeOptions} onChange={m => put(a, { mode: m }, `Now: ${modeText(m)}`)} label={`Sync mode for ${a.name}`} width={260} className="inline" />
                    <button type="button" className="btn ghost sm" disabled={syncing === a.id || !!a.syncing || a.mode === 'off' || !a.enabled} onClick={() => sync(a)}>
                      {syncing === a.id ? <Spinner /> : <Icon name="refresh" size={14} />}Sync now
                    </button>
                    <MenuButton label={`More for ${a.name}`} align="right" items={[
                      { label: 'Edit…', icon: 'edit', onSelect: () => setEdit(a) },
                      { label: a.enabled ? 'Pause' : 'Resume', icon: a.enabled ? 'bell-off' : 'refresh', onSelect: () => put(a, { enabled: !a.enabled }) },
                      'sep',
                      { label: 'Remove…', icon: 'trash', danger: true, onSelect: () => setDel(a) },
                    ]}><Icon name="more" size={16} /></MenuButton>
                  </div>
                </div>
              )
            })}
          </div>
        )}
        {nc.data && !nc.data.core && nc.data.detail && <p className="muted small mt">Nextcloud through Pocket Core: {nc.data.detail}</p>}
      </Section>

      {wizard && <AddAccount preset={wizard} nc={nc.data} onClose={() => setWizard(null)} onAdded={() => { setWizard(null); after() }} />}
      {edit && <EditAccount acct={edit} onClose={() => setEdit(null)} onSaved={() => { setEdit(null); after() }} />}
      {del && <RemoveAccount acct={del} onClose={() => setDel(null)} onDone={() => { setDel(null); after() }} />}
    </div>
  )
}

function hostOf(u: string): string { try { return new URL(u).host } catch { return u } }

function AddAccount({ preset, nc, onClose, onAdded }: { preset: { kind?: Kind; via?: Via }; nc: NextcloudCore | null; onClose: () => void; onAdded: () => void }) {
  const { toast } = useToast()
  const [kind, setKind] = useState<Kind | null>(preset.kind || null)
  const [via, setVia] = useState<Via>(preset.via || 'direct')
  const [f, setF] = useState({ url: '', username: '', password: '', name: '' })
  const [found, setFound] = useState<Discovered[] | null>(null)
  const [pick, setPick] = useState<Set<string>>(new Set())
  const [mode, setMode] = useState<SyncMode>('import')
  const [found_name, setFoundName] = useState('')
  const [busy, setBusy] = useState<'discover' | 'add' | null>(null)
  const [error, setError] = useState('')
  const set = (k: keyof typeof f) => (e: React.ChangeEvent<HTMLInputElement>) => { setF(x => ({ ...x, [k]: e.target.value })); setFound(null); setError('') }

  const choose = (k: Kind) => {
    setKind(k); setFound(null); setError('')
    setVia(k === 'nextcloud' && nc?.core ? 'core' : 'direct')
    setF({ url: '', username: '', password: '', name: '' })
  }
  const creds = () => {
    const b: Record<string, unknown> = { kind, via: kind === 'nextcloud' ? via : 'direct' }
    if (!(kind === 'nextcloud' && via === 'core')) { if (kind !== 'icloud') b.url = f.url.trim(); b.username = f.username.trim(); b.password = f.password }
    return b
  }
  const valid = kind === 'nextcloud' && via === 'core' ? !!nc?.core : !!((kind === 'icloud' || f.url.trim()) && f.username.trim() && f.password)
  const explain = (e: unknown) => e instanceof ApiError && e.status === 401 ? 'The user name or password was not accepted.' : errMsg(e)
  const discover = async () => {
    setBusy('discover'); setError('')
    try {
      const r = await api.post<{ addressbooks: Discovered[] | null; name?: string }>(`${V1}/accounts/discover`, creds())
      setFoundName(r?.name || '')
      const books = r?.addressbooks || []
      setFound(books); setPick(new Set(books.map(b => b.href)))
      if (!books.length) setError('Connected, but there are no address books there.')
    } catch (e) { setError(explain(e)) } finally { setBusy(null) }
  }
  const add = async () => {
    setBusy('add')
    try {
      await api.post(`${V1}/accounts`, { ...creds(), mode, hrefs: [...pick], ...(f.name.trim() ? { name: f.name.trim() } : { name: found_name || (kind === 'icloud' ? 'iCloud' : kind === 'nextcloud' ? 'Nextcloud' : hostOf(f.url)) }) })
      toast(mode === 'import' ? 'Importing now — it takes a moment' : 'Connected — syncing now')
      onAdded()
    } catch (e) { toast(explain(e), 'err') } finally { setBusy(null) }
  }
  const p = PROVIDERS.find(x => x.id === kind)
  const coreNC = kind === 'nextcloud' && via === 'core'

  return (
    <Modal size="lg" onClose={onClose}
      title={kind ? <span className="row">{!preset.kind && <button type="button" className="btn ghost icon-only sm" aria-label="Back" onClick={() => setKind(null)}><Icon name="chevron-left" size={16} /></button>}{coreNC ? 'Nextcloud through Pocket Core' : `Add ${p?.name}`}</span> : 'Add an account'}
      footer={kind ? <>
        <span className="spacer" />
        <button type="button" className="btn" onClick={onClose}>Cancel</button>
        {!found?.length
          ? <button type="button" className="btn primary" onClick={discover} disabled={!valid || !!busy}>{busy === 'discover' ? <Spinner /> : <Icon name="search" size={14} />}Find address books</button>
          : <button type="button" className="btn primary" onClick={add} disabled={!pick.size || !!busy}>{busy === 'add' && <Spinner />}{mode === 'import' ? `Import ${pick.size}` : `Connect ${pick.size}`} address book{pick.size === 1 ? '' : 's'}</button>}
      </> : undefined}>
      {!kind ? (
        <div className="acct-providers">
          {PROVIDERS.map(x => (
            <button key={x.id} type="button" className="acct-provider" onClick={() => choose(x.id)}>
              <span className="acct-provider-ico" style={{ background: x.color }}><Icon name={x.icon} size={18} /></span>
              <span className="acct-provider-name">{x.name}</span>
              <span className="acct-provider-desc">{x.id === 'nextcloud' && nc?.core ? 'Through Pocket Core — no password needed' : x.desc}</span>
            </button>
          ))}
        </div>
      ) : (
        <div className="stack">
          {kind === 'nextcloud' && (
            <Segmented value={via} onChange={v => { setVia(v); setFound(null); setError('') }} label="How to connect" options={[{ value: 'core', label: 'Through Pocket Core' }, { value: 'direct', label: 'Direct' }]} />
          )}
          {coreNC ? (
            nc?.core
              ? <div className="callout info"><Icon name="info" size={15} /><span>Pocket Core signs in to {nc.url ? hostOf(nc.url) : 'Nextcloud'} as {nc.username || 'you'} — no address or password needed here.</span></div>
              : <div className="callout warn"><Icon name="alert" size={15} /><span>{nc?.detail || 'Pocket Core has no Nextcloud login for you.'} Connect directly with an app password instead.</span></div>
          ) : (<>
            {kind !== 'icloud' && <Field label="Server address" help={kind === 'nextcloud' ? 'Your Nextcloud address, e.g. https://cloud.example.com' : 'The CardDAV address; most servers find the address books from the base address.'}>
              <input value={f.url} onChange={set('url')} inputMode="url" spellCheck={false} autoComplete="off" placeholder="https://" autoFocus />
            </Field>}
            <div className="two-col">
              <Field label={kind === 'icloud' ? 'Apple ID (email)' : 'User name'}><input value={f.username} onChange={set('username')} autoComplete="off" spellCheck={false} autoFocus={kind === 'icloud'} /></Field>
              <Field label={kind === 'icloud' ? 'App-specific password' : kind === 'nextcloud' ? 'App password' : 'Password'}><input type="password" value={f.password} onChange={set('password')} autoComplete="new-password" /></Field>
            </div>
            {kind === 'icloud' && <p className="muted small" style={{ margin: 0 }}>Make one at <a href="https://account.apple.com" target="_blank" rel="noreferrer">account.apple.com</a> → Sign-In and Security → App-Specific Passwords. Your normal Apple password won’t work.</p>}
            {kind === 'nextcloud' && <p className="muted small" style={{ margin: 0 }}>In Nextcloud: Personal settings → Security → Create new app password.</p>}
          </>)}
          {error && <div className="callout danger"><Icon name="alert" size={15} /><span>{error}</span></div>}
          {!!found?.length && (
            <div className="stack">
              <div className="found-list" role="group" aria-label="Address books">
                {found.map(b => (
                  <label key={b.href} className={`found-row ${pick.has(b.href) ? 'on' : ''}`}>
                    <input type="checkbox" checked={pick.has(b.href)} onChange={e => setPick(s => { const n = new Set(s); if (e.target.checked) n.add(b.href); else n.delete(b.href); return n })} />
                    <Icon name="book" size={15} /><span className="grow ellipsis">{b.name || b.href}</span>{b.read_only && <span className="badge" title="That server lets you read it only">Read-only</span>}<span className="muted small tnum">{b.count} contact{b.count === 1 ? '' : 's'}</span>
                  </label>
                ))}
              </div>
              <Field label="How" group>
                <div className="mode-cards">
                  {MODES.map(m => (
                    <button key={m.value} type="button" className={`mode-card ${mode === m.value ? 'on' : ''}`} aria-pressed={mode === m.value} onClick={() => setMode(m.value)}>
                      <b>{m.label}</b><span className="muted small">{m.description}</span>
                    </button>
                  ))}
                </div>
              </Field>
              <Field label="Name (optional)"><input value={f.name} onChange={e => setF(x => ({ ...x, name: e.target.value }))} placeholder={found_name || p?.name} /></Field>
            </div>
          )}
        </div>
      )}
    </Modal>
  )
}

function EditAccount({ acct, onClose, onSaved }: { acct: Account; onClose: () => void; onSaved: () => void }) {
  const { toast } = useToast()
  const [f, setF] = useState({ name: acct.name, password: '', sync_minutes: acct.sync_minutes || 30, mode: acct.mode })
  const [busy, setBusy] = useState(false)
  const save = async () => {
    setBusy(true)
    try {
      await api.put(`${V1}/accounts/${acct.id}`, { name: f.name.trim(), sync_minutes: f.sync_minutes, mode: f.mode, ...(f.password ? { password: f.password } : {}) })
      toast('Account saved'); onSaved()
    } catch (e) { toast(errMsg(e), 'err') } finally { setBusy(false) }
  }
  const sync = SYNC.some(o => o.value === f.sync_minutes) ? SYNC : [...SYNC, { value: f.sync_minutes, label: `Every ${f.sync_minutes} minutes` }]
  return (
    <Modal title={`Edit ${acct.name || 'account'}`} onClose={onClose} footer={<>
      <button type="button" className="btn" onClick={onClose}>Cancel</button>
      <button type="button" className="btn primary" onClick={save} disabled={busy}>Save</button>
    </>}>
      <div className="stack">
        <Field label="Name"><input value={f.name} onChange={e => setF({ ...f, name: e.target.value })} /></Field>
        <Field label="Sync" group><Select value={f.mode} options={modeOptions} onChange={v => setF({ ...f, mode: v })} label="Sync mode" /></Field>
        <Field label="How often" group><Select value={f.sync_minutes} options={sync} onChange={v => setF({ ...f, sync_minutes: v })} label="Sync interval" /></Field>
        {acct.via !== 'core' && <Field label="Password" help="Leave empty to keep the saved one."><input type="password" value={f.password} onChange={e => setF({ ...f, password: e.target.value })} placeholder={acct.has_secret ? '•••• saved' : ''} autoComplete="new-password" /></Field>}
      </div>
    </Modal>
  )
}

function RemoveAccount({ acct, onClose, onDone }: { acct: Account; onClose: () => void; onDone: () => void }) {
  const { toast } = useToast()
  const [keep, setKeep] = useState(true)
  const [busy, setBusy] = useState(false)
  const go = async () => {
    setBusy(true)
    try { await api.del(`${V1}/accounts/${acct.id}${keep ? '?keep=1' : ''}`); toast(keep ? 'Account removed — its contacts stay here' : 'Account and its address books removed'); onDone() } catch (e) { toast(errMsg(e), 'err') } finally { setBusy(false) }
  }
  return (
    <Modal title={`Remove ${acct.name || 'this account'}?`} size="sm" onClose={onClose} footer={<>
      <button type="button" className="btn" onClick={onClose}>Cancel</button>
      <button type="button" className="btn danger" onClick={go} disabled={busy}>Remove</button>
    </>}>
      <div className="stack">
        <p className="confirm-msg" style={{ margin: 0 }}>Syncing stops. Nothing is deleted on {KIND_NAME[acct.kind] || 'the other server'}.</p>
        <label className="inline-switch"><Switch checked={keep} onChange={setKeep} label="Keep the contacts here" />Keep its {acct.addressbooks} address book{acct.addressbooks === 1 ? '' : 's'} here as normal ones</label>
      </div>
    </Modal>
  )
}
