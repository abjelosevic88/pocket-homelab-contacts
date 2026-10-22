// "Connected": the meetings, notes and to-dos about this person in the other Pocket apps.
import React, { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { displayName } from '../lib/contacts'
import type { Contact, Related, RelatedEvent, RelatedProjectItem, RelatedSection, RelatedTask } from '../lib/types'
import { Icon } from './icons'
import { errMsg, Segmented, Spinner, useToast } from './ui'

export function RelatedPanel({ contact }: { contact: Contact }) {
  const [data, setData] = useState<Related | null>(null)
  const [err, setErr] = useState('')
  const id = contact.id
  useEffect(() => {
    let live = true
    setData(null); setErr('')
    api.get<Related>(`${V1}/contacts/${id}/related`).then(r => live && setData(r || {})).catch(e => live && setErr(errMsg(e)))
    return () => { live = false }
  }, [id])
  const name = displayName(contact).split(' ')[0]
  const upd = (patch: Partial<Related>) => setData(d => ({ ...(d || {}), ...patch }))

  return (
    <aside className="related" aria-label="Connected">
      <h2 className="related-h"><Icon name="link" size={14} />Connected</h2>
      {err ? <div className="rel-card"><p className="rel-msg danger-text">{err}</p></div> : <>
        <CalendarSection s={data ? data.calendar : undefined} loading={!data} contact={contact} name={name} onAdd={ev => upd({ calendar: { ...(data?.calendar || { status: 'ok', detail: '' }), items: [...(data?.calendar?.items || []), ev] } })} />
        <NotesSection s={data ? data.notes : undefined} loading={!data} name={name} />
        <ProjectsSection s={data ? data.projects : undefined} loading={!data} name={name} />
        <TasksSection s={data ? data.tasks : undefined} loading={!data} contact={contact} name={name} onAdd={t => upd({ tasks: { ...(data?.tasks || { status: 'ok', detail: '' }), items: [...(data?.tasks?.items || []), t] } })} />
      </>}
    </aside>
  )
}

const cap = (s: string) => s ? s.charAt(0).toUpperCase() + s.slice(1) : s

function Shell<T>({ icon, title, s, loading, children, action, empty }: { icon: string; title: string; s: RelatedSection<T> | undefined; loading: boolean; children: React.ReactNode; action?: React.ReactNode; empty: string }) {
  const off = !loading && (!s || s.status === 'off')
  return (
    <section className={`rel-card ${off ? 'off' : ''}`}>
      <div className="rel-head">
        <span className="rel-ico"><Icon name={icon} size={14} /></span>
        {s?.app_url ? <a className="rel-title" href={s.app_url} target="_blank" rel="noreferrer">{title}</a> : <span className="rel-title">{title}</span>}
        <span className="spacer" />
        {!loading && s?.status === 'ok' && action}
      </div>
      {loading ? <div className="rel-skel"><span className="skeleton" style={{ width: '70%', height: 11 }} /><span className="skeleton" style={{ width: '45%', height: 11 }} /></div>
        : off ? <p className="rel-msg">{cap(s?.detail || '') || `${title} is off.`} <Link to="/settings/apps">Turn on in Settings → Apps</Link></p>
        : s!.status === 'error' ? <p className="rel-msg warn"><Icon name="alert" size={13} />{cap(s!.detail) || `${title} didn’t answer.`}</p>
        : !(s!.items || []).length ? <p className="rel-msg">{empty}</p>
        : children}
    </section>
  )
}

function fmtWhen(e: RelatedEvent): string {
  const d = new Date(e.all_day ? e.start + 'T00:00:00' : e.start)
  if (isNaN(d.getTime())) return e.start
  const sameYear = d.getFullYear() === new Date().getFullYear()
  const day = d.toLocaleDateString('en-GB', { weekday: 'short', day: 'numeric', month: 'short', year: sameYear ? undefined : 'numeric' })
  return e.all_day ? day : `${day}, ${d.toLocaleTimeString('en-GB', { hour: '2-digit', minute: '2-digit' })}`
}

function CalendarSection({ s, loading, contact, name, onAdd }: { s?: RelatedSection<RelatedEvent>; loading: boolean; contact: Contact; name: string; onAdd: (e: RelatedEvent) => void }) {
  const [open, setOpen] = useState(false)
  const [showPast, setShowPast] = useState(false)
  const items = s?.items || []
  const upcoming = items.filter(e => !e.past).sort((a, b) => a.start.localeCompare(b.start))
  const past = items.filter(e => e.past).sort((a, b) => b.start.localeCompare(a.start))
  return (
    <Shell icon="calendar" title="Pocket Calendar" s={s} loading={loading} empty={`No meetings with ${name} yet.`}
      action={<button type="button" className="btn ghost xs rel-add" onClick={() => setOpen(o => !o)} aria-expanded={open}><Icon name="plus" size={13} />New event</button>}>
      <ul className="rel-list">
        {upcoming.map(e => <EventItem key={e.id} e={e} />)}
        {upcoming.length === 0 && <li className="rel-msg">Nothing planned.</li>}
      </ul>
      {past.length > 0 && <>
        <button type="button" className="rel-more" onClick={() => setShowPast(p => !p)}><Icon name={showPast ? 'chevron-up' : 'chevron-down'} size={12} />{past.length} past meeting{past.length === 1 ? '' : 's'}</button>
        {showPast && <ul className="rel-list past">{past.slice(0, 20).map(e => <EventItem key={e.id} e={e} />)}</ul>}
      </>}
      {open && <NewEvent contact={contact} name={name} onDone={e => { setOpen(false); if (e) onAdd(e) }} />}
    </Shell>
  )
}

function EventItem({ e }: { e: RelatedEvent }) {
  return <li><a href={e.url} target="_blank" rel="noreferrer" className="rel-item"><span className="rel-item-title">{e.title || 'Untitled'}</span><span className="rel-item-sub">{fmtWhen(e)}</span></a></li>
}

const pad = (n: number) => String(n).padStart(2, '0')

function NewEvent({ contact, name, onDone }: { contact: Contact; name: string; onDone: (e: RelatedEvent | null) => void }) {
  const { toast } = useToast()
  const t = new Date(Date.now() + 86400000)
  const [title, setTitle] = useState(`Meet ${name}`)
  const [date, setDate] = useState(`${t.getFullYear()}-${pad(t.getMonth() + 1)}-${pad(t.getDate())}`)
  const [time, setTime] = useState('10:00')
  const [dur, setDur] = useState(60)
  const [busy, setBusy] = useState(false)
  const save = async () => {
    if (!title.trim() || !date || !time) return
    setBusy(true)
    try {
      const r = await api.post<{ item: RelatedEvent }>(`${V1}/contacts/${contact.id}/event`, { title: title.trim(), start: `${date}T${time}`, duration_min: dur })
      toast(contact.emails?.length ? `Event created — ${name} is invited` : 'Event created')
      onDone(r?.item || null)
    } catch (e) { toast(errMsg(e), 'err') } finally { setBusy(false) }
  }
  return (
    <form className="rel-form" onSubmit={e => { e.preventDefault(); save() }}>
      <input value={title} onChange={e => setTitle(e.target.value)} aria-label="Title" autoFocus />
      <div className="row">
        <input type="date" value={date} onChange={e => setDate(e.target.value)} aria-label="Date" required />
        <input type="time" value={time} onChange={e => setTime(e.target.value)} aria-label="Time" required step={300} />
      </div>
      <Segmented size="sm" label="Duration" value={dur} onChange={setDur} options={[{ value: 30, label: '30 min' }, { value: 60, label: '1 h' }, { value: 90, label: '1½ h' }, { value: 120, label: '2 h' }]} />
      {!contact.emails?.length && <p className="muted xsmall" style={{ margin: 0 }}>{name} has no email, so nobody is invited.</p>}
      <div className="row"><button type="submit" className="btn primary sm" disabled={busy || !title.trim()}>{busy && <Spinner size={12} />}Create</button><button type="button" className="btn ghost sm" onClick={() => onDone(null)}>Cancel</button></div>
    </form>
  )
}

function NotesSection({ s, loading, name }: { s?: RelatedSection<{ id: string; title: string; snippet?: string; updated_at?: string; url: string }>; loading: boolean; name: string }) {
  return (
    <Shell icon="note" title="Pocket Notes" s={s} loading={loading} empty={`No notes mention ${name} yet.`}
      action={s?.new_url ? <a className="btn ghost xs rel-add" href={s.new_url} target="_blank" rel="noreferrer"><Icon name="plus" size={13} />New note</a> : undefined}>
      <ul className="rel-list">
        {(s?.items || []).map(n => (
          <li key={n.id}><a href={n.url} target="_blank" rel="noreferrer" className="rel-item"><span className="rel-item-title">{n.title || 'Untitled'}</span>{n.snippet && <span className="rel-item-sub clamp">{n.snippet}</span>}</a></li>
        ))}
      </ul>
    </Shell>
  )
}

const HEALTH: Record<string, string> = { on_track: 'On track', at_risk: 'At risk', off_track: 'Off track' }

function ProjectsSection({ s, loading, name }: { s?: RelatedSection<RelatedProjectItem>; loading: boolean; name: string }) {
  const items = (s?.items || []).filter(i => i.kind === 'project' || !i.done)
  return (
    <Shell icon="briefcase" title="Pocket Projects" s={s} loading={loading} empty={`${name} isn't on any project yet.`}>
      <ul className="rel-list">
        {items.map(it => (
          <li key={it.id}><a href={it.url} target="_blank" rel="noreferrer" className={`rel-item ${it.kind === 'issue' ? 'task' : ''}`}>
            {it.kind === 'issue' && <Icon name="circle" size={13} className="rel-check" />}
            <span className="rel-item-title">{it.kind === 'issue' ? <><span className="muted">{it.identifier}</span> {it.title}</> : it.title}</span>
            {it.kind === 'project' && <span className="rel-item-sub">{[it.done ? 'Done' : HEALTH[it.health || ''], typeof it.progress === 'number' ? `${Math.round(it.progress)}% done` : '', it.target_date ? `target ${fmtDue(it.target_date)}` : ''].filter(Boolean).join(' · ')}</span>}
            {it.kind === 'issue' && it.due && <span className={`rel-due ${it.due < today() ? 'late' : ''}`}>{fmtDue(it.due)}</span>}
          </a></li>
        ))}
      </ul>
    </Shell>
  )
}

function TasksSection({ s, loading, contact, name, onAdd }: { s?: RelatedSection<RelatedTask>; loading: boolean; contact: Contact; name: string; onAdd: (t: RelatedTask) => void }) {
  const [open, setOpen] = useState(false)
  const open_ = (s?.items || []).filter(t => !t.done)
  return (
    <Shell icon="check-square" title="Pocket Tasks" s={s} loading={loading} empty={`No open to-dos about ${name}.`}
      action={<button type="button" className="btn ghost xs rel-add" onClick={() => setOpen(o => !o)} aria-expanded={open}><Icon name="bell" size={13} />Remind me</button>}>
      <ul className="rel-list">
        {open_.map(t => (
          <li key={t.id}><a href={t.url} target="_blank" rel="noreferrer" className="rel-item task"><Icon name="circle" size={13} className="rel-check" /><span className="rel-item-title">{t.title}</span>{t.due && <span className={`rel-due ${t.due < today() ? 'late' : ''}`}>{fmtDue(t.due)}</span>}</a></li>
        ))}
        {open_.length === 0 && <li className="rel-msg">All done.</li>}
      </ul>
      {open && <NewTask contact={contact} name={name} onDone={t => { setOpen(false); if (t) onAdd(t) }} />}
    </Shell>
  )
}

const today = () => { const d = new Date(); return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}` }
function fmtDue(s: string): string {
  if (s === today()) return 'Today'
  const d = new Date(s + 'T00:00:00')
  return isNaN(d.getTime()) ? s : d.toLocaleDateString('en-GB', { day: 'numeric', month: 'short' })
}

function NewTask({ contact, name, onDone }: { contact: Contact; name: string; onDone: (t: RelatedTask | null) => void }) {
  const { toast } = useToast()
  const [title, setTitle] = useState(`Call ${name}`)
  const [due, setDue] = useState('')
  const [busy, setBusy] = useState(false)
  const save = async () => {
    if (!title.trim()) return
    setBusy(true)
    try {
      const r = await api.post<{ item: RelatedTask }>(`${V1}/contacts/${contact.id}/task`, { title: title.trim(), ...(due ? { due } : {}) })
      toast('Added to Pocket Tasks')
      onDone(r?.item || null)
    } catch (e) { toast(errMsg(e), 'err') } finally { setBusy(false) }
  }
  return (
    <form className="rel-form" onSubmit={e => { e.preventDefault(); save() }}>
      <input value={title} onChange={e => setTitle(e.target.value)} aria-label="What to do" autoFocus />
      <div className="row"><span className="muted small">Due</span><input type="date" value={due} onChange={e => setDue(e.target.value)} aria-label="Due date (optional)" /></div>
      <div className="row"><button type="submit" className="btn primary sm" disabled={busy || !title.trim()}>{busy && <Spinner size={12} />}Add</button><button type="button" className="btn ghost sm" onClick={() => onDone(null)}>Cancel</button></div>
    </form>
  )
}
