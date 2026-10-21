import { useState } from 'react'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { displayName, fmtDate } from '../lib/contacts'
import type { DuplicateGroup, Summary } from '../lib/types'
import { ContactAvatar } from '../components/ContactAvatar'
import { Icon } from '../components/icons'
import { PageHead } from '../components/PageHead'
import { Confirm, errMsg, Spinner, useAsync, useToast } from '../components/ui'

export default function DuplicatesPage({ onMenu, onOpen }: { onMenu?: () => void; onOpen: (id: number) => void }) {
  const { version, bump, reloadBooks, reloadCategories } = useApp()
  const { toast } = useToast()
  const list = useAsync(() => api.get<DuplicateGroup[]>(`${V1}/duplicates`), [version])
  const [primary, setPrimary] = useState<Record<string, number>>({})
  const [busy, setBusy] = useState<string | null>(null)
  const [mergeAll, setMergeAll] = useState(false)
  const [progress, setProgress] = useState<{ done: number; of: number } | null>(null)
  const groups = list.data || []
  const drop = (key: string) => list.setData(g => (g || []).filter(x => x.key !== key))
  const after = () => { bump(); reloadBooks().catch(() => {}); reloadCategories() }

  const merge = async (g: DuplicateGroup, quiet = false) => {
    const p = primary[g.key] ?? g.contacts[0].id
    await api.post(`${V1}/duplicates/merge`, { ids: g.contacts.map(c => c.id), primary_id: p })
    drop(g.key)
    if (!quiet) { toast(`Merged into ${displayName(g.contacts.find(c => c.id === p) || g.contacts[0])}`, 'ok', { label: 'Open', run: () => onOpen(p) }); after() }
  }
  const run = async (g: DuplicateGroup, what: 'merge' | 'ignore') => {
    setBusy(g.key)
    try {
      if (what === 'merge') await merge(g)
      else { await api.post(`${V1}/duplicates/ignore`, { ids: g.contacts.map(c => c.id) }); drop(g.key); toast('Got it — they stay apart') ; after() }
    } catch (e) { toast(errMsg(e), 'err') } finally { setBusy(null) }
  }
  const runAll = async () => {
    setMergeAll(false)
    const todo = [...groups]
    setProgress({ done: 0, of: todo.length })
    let failed = 0
    for (let i = 0; i < todo.length; i++) {
      try { await merge(todo[i], true) } catch { failed++ }
      setProgress({ done: i + 1, of: todo.length })
    }
    setProgress(null)
    after()
    toast(failed ? `Merged ${todo.length - failed}, ${failed} failed` : `Merged ${todo.length} groups`, failed ? 'err' : 'ok')
  }

  return (
    <div className="page">
      <PageHead title="Duplicates" icon="merge" onMenu={onMenu}
        sub={list.data ? (groups.length ? `${groups.length} group${groups.length === 1 ? '' : 's'} that look like the same person` : 'No duplicates — everything looks tidy') : undefined}
        actions={groups.length > 1 ? <button type="button" className="btn sm" disabled={!!progress} onClick={() => setMergeAll(true)}>{progress ? <><Spinner size={12} />{progress.done}/{progress.of}</> : <><Icon name="merge" size={14} />Merge all</>}</button> : undefined} />
      <div className="page-scroll">
        <div className="page-inner">
          {list.error ? <div className="callout danger"><Icon name="alert" size={15} /><span>{list.error}</span></div>
            : !list.data ? <div className="stack">{[0, 1].map(i => <div key={i} className="dupe skel"><span className="skeleton" style={{ height: 120 }} /><span className="skeleton" style={{ height: 120 }} /></div>)}</div>
            : !groups.length ? (
              <div className="empty"><div className="empty-ico"><Icon name="check-circle" size={22} /></div><h3>No duplicates</h3><div className="muted">Contacts with the same full name, email or phone number would show up here. Nothing is ever merged without you.</div></div>
            ) : (
              <div className="stack loose">
                <p className="muted small" style={{ margin: 0 }}>Merging keeps the name of the one you pick, adds every phone, email, address and date from the others, joins the notes, and moves the others to Recently deleted.</p>
                {groups.map(g => {
                  const p = primary[g.key] ?? g.contacts[0].id
                  return (
                    <section key={g.key} className="dupe">
                      <div className="dupe-head"><span className="badge warn">{reasonText(g.reason)}</span><span className="spacer" />
                        <button type="button" className="btn ghost sm" disabled={busy === g.key} onClick={() => run(g, 'ignore')}>Not duplicates</button>
                        <button type="button" className="btn primary sm" disabled={busy === g.key} onClick={() => run(g, 'merge')}>{busy === g.key ? <Spinner size={12} /> : <Icon name="merge" size={14} />}Merge</button>
                      </div>
                      <div className="dupe-cards">
                        {g.contacts.map(c => <DupeCard key={c.id} c={c} primary={c.id === p} onPick={() => setPrimary(m => ({ ...m, [g.key]: c.id }))} onOpen={() => onOpen(c.id)} />)}
                      </div>
                    </section>
                  )
                })}
              </div>
            )}
        </div>
      </div>
      {mergeAll && <Confirm title={`Merge all ${groups.length} groups?`} confirmLabel="Merge all" danger={false}
        message="Each group is merged into the contact picked in it (the first one unless you chose another). The others go to Recently deleted, where you can get them back for 30 days."
        onCancel={() => setMergeAll(false)} onConfirm={runAll} />}
    </div>
  )
}

function reasonText(r: string): string {
  if (/name/.test(r)) return 'Same name'
  if (/email/.test(r)) return 'Same email'
  if (/phone/.test(r)) return 'Same phone'
  return r
}

function DupeCard({ c, primary, onPick, onOpen }: { c: Summary; primary: boolean; onPick: () => void; onOpen: () => void }) {
  return (
    <div className={`dupe-card ${primary ? 'primary' : ''}`} onClick={onPick} role="radio" aria-checked={primary} tabIndex={0} onKeyDown={e => { if (e.key === ' ' || e.key === 'Enter') { e.preventDefault(); onPick() } }}>
      <div className="dupe-top">
        <ContactAvatar c={c} size={40} />
        <div className="grow" style={{ minWidth: 0 }}>
          <div className="dupe-name ellipsis">{c.fn || c.org || 'No name'}</div>
          {(c.org || c.title) && <div className="muted small ellipsis">{[c.org, c.title].filter(Boolean).join(' · ')}</div>}
        </div>
        <span className="dupe-radio">{primary && <Icon name="check" size={12} strokeWidth={3} />}</span>
      </div>
      <ul className="dupe-vals">
        {(c.phones || []).map(p => <li key={'p' + p}><Icon name="phone" size={12} />{p}</li>)}
        {(c.emails || []).map(e => <li key={'e' + e}><Icon name="mail" size={12} />{e}</li>)}
        {c.birthday && <li><Icon name="cake" size={12} />{fmtDate(c.birthday)}</li>}
        {!c.phones?.length && !c.emails?.length && !c.birthday && <li className="faint">No phone or email</li>}
      </ul>
      <div className="dupe-foot">
        {primary ? <span className="dupe-keep">Keep this one</span> : <span className="faint small">Click to keep this one</span>}
        <button type="button" className="link-btn small" onClick={e => { e.stopPropagation(); onOpen() }}>Open</button>
      </div>
    </div>
  )
}
