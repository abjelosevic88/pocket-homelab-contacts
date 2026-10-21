import { useEffect, useState } from 'react'
import { api, V1 } from '../../lib/api'
import { useApp } from '../../lib/app-context'
import type { Integration, Mode } from '../../lib/types'
import { Icon } from '../../components/icons'
import { errMsg, Field, Loading, Segmented, Spinner, useAsync, useToast } from '../../components/ui'
import { StatusDot } from './common'

const META: Record<string, { icon: string; color: string; what: string }> = {
  calendar: { icon: 'calendar', color: '#ea580c', what: 'Meetings with the person (they’re a guest by email), and “New event” with them invited.' },
  notes: { icon: 'note', color: '#ca8a04', what: 'Notes that link to the person, and “New note” about them.' },
  tasks: { icon: 'check-square', color: '#16a34a', what: 'Open to-dos about the person, and “Remind me”.' },
}

export function AppsTab() {
  const { sr } = useApp()
  const list = useAsync(() => api.get<Integration[]>(`${V1}/integrations`), [])
  const core = sr?.core
  return (
    <div className="stack loose">
      <div className="callout info">
        <Icon name="layers" size={16} />
        <div>
          <b>Pocket Core is the default and needs no setup.</b> Each app is read as you through a Pocket Core app link, so you only ever see your own meetings, notes and to-dos on a contact.
          <div className="small mt-s">
            Pocket Core: {!core?.configured ? <span className="danger-text">not configured on this box</span> : core.registered ? <span className="ok-text">connected</span> : <span className="danger-text">not reachable{core.error ? ` — ${core.error}` : ''}</span>}
          </div>
        </div>
      </div>
      {list.error ? <div className="callout danger"><Icon name="alert" size={15} /><span>{list.error}</span></div>
        : !list.data ? <Loading /> : (
          <div className="set-apps">
            {list.data.map(app => <AppCard key={app.id} app={app} onList={l => list.setData(l)} />)}
          </div>
        )}
    </div>
  )
}

function AppCard({ app, onList }: { app: Integration; onList: (l: Integration[]) => void }) {
  const { toast } = useToast()
  const [url, setUrl] = useState(app.url || '')
  const [token, setToken] = useState('')
  const [busy, setBusy] = useState(false)
  const [advanced, setAdvanced] = useState(app.mode === 'direct')
  useEffect(() => { setUrl(app.url || '') }, [app.url])
  const meta = META[app.id] || { icon: 'layers', color: '#64748b', what: '' }

  const put = async (body: Partial<{ mode: Mode; url: string; token: string }>, ok?: string) => {
    setBusy(true)
    try {
      const r = await api.put<Integration[] | Integration>(`${V1}/integrations/${app.id}`, body)
      if (Array.isArray(r)) onList(r)
      else onList((await api.get<Integration[]>(`${V1}/integrations`)) || [])
      if (ok) toast(ok)
      setToken('')
    } catch (e) { toast(errMsg(e), 'err') } finally { setBusy(false) }
  }
  const status: 'ok' | 'error' | 'off' = app.mode === 'off' ? 'off' : app.status === 'error' ? 'error' : 'ok'
  const modes = advanced || app.mode === 'direct'
    ? [{ value: 'core' as Mode, label: 'Pocket Core' }, { value: 'direct' as Mode, label: 'Direct' }, { value: 'off' as Mode, label: 'Off' }]
    : [{ value: 'core' as Mode, label: 'Pocket Core' }, { value: 'off' as Mode, label: 'Off' }]

  return (
    <section className={`set-app ${app.mode === 'off' ? 'off' : ''}`}>
      <div className="set-app-head">
        <span className="set-app-ico" style={{ background: meta.color }}><Icon name={meta.icon} size={16} /></span>
        <div className="grow">
          <div className="set-app-name">{app.name}{app.public_url && <a className="muted small app-link" href={app.public_url} target="_blank" rel="noreferrer"> · open <Icon name="external" size={11} /></a>}</div>
          <div className="muted small">{app.description || meta.what}</div>
        </div>
        <span className="set-app-status" title={app.detail || status}>
          {busy ? <Spinner size={10} /> : <StatusDot status={status} title={status === 'error' ? app.detail : status === 'ok' ? 'Working' : 'Off'} />}
          <span className="small">{status === 'ok' ? 'Working' : status === 'off' ? 'Off' : 'Problem'}</span>
        </span>
      </div>
      {status === 'error' && app.detail && <div className="callout danger small"><Icon name="alert" size={14} /><span>{app.detail.charAt(0).toUpperCase() + app.detail.slice(1)}</span></div>}
      <div className="set-app-controls">
        <Segmented size="sm" label={`${app.name} connection`} value={app.mode} onChange={m => put({ mode: m })} options={modes} />
        {!advanced && app.mode !== 'direct' && <button type="button" className="link-btn small" onClick={() => setAdvanced(true)}>Advanced</button>}
        {app.scopes?.length ? <span className="faint xsmall">{app.scopes.join(' · ')}</span> : null}
      </div>
      {app.mode === 'direct' && (
        <div className="set-app-direct">
          <div className="two-col">
            <Field label={`${app.name} address`}><input value={url} onChange={e => setUrl(e.target.value)} placeholder={`https://${app.id}.example.com`} inputMode="url" spellCheck={false} /></Field>
            <Field label="Token" help={`An API token made in ${app.name} → Settings → Tokens.`}><input type="password" value={token} onChange={e => setToken(e.target.value)} placeholder={app.has_token ? '•••• saved — type to replace' : 'Paste a token'} autoComplete="new-password" /></Field>
          </div>
          <div className="row"><button type="button" className="btn primary sm" disabled={busy || !url.trim()} onClick={() => put({ mode: 'direct', url: url.trim(), ...(token ? { token } : {}) }, 'Saved')}>Save connection</button></div>
        </div>
      )}
    </section>
  )
}
