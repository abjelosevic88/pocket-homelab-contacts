// One contact: photo, name, quick actions, every field, the other Pocket apps, history.
import React, { useRef, useState } from 'react'
import { api, ApiError, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { addressLines, ago, displayName, fmtDate, fold, imHref, labelText, mapsHref, nextOccurrence, inDays, ordinalAge, smsHref, socialHref, telHref, webHref } from '../lib/contacts'
import type { Scope } from '../lib/scope'
import type { Activity, CardField, Contact, Summary } from '../lib/types'
import { ContactAvatar } from './ContactAvatar'
import { Icon } from './icons'
import { RelatedPanel } from './RelatedPanel'
import { copyText, download, errMsg, type MenuItem, MenuButton, Spinner, useToast } from './ui'

interface Props {
  contact: Contact | null
  loading: boolean
  error: string
  all: Summary[] | null
  mobile: boolean
  onBack: () => void
  onEdit: (c: Contact) => void
  onDelete: (c: Contact) => void
  onStar: (c: Contact) => void
  onMove: (c: Contact) => void
  onChanged: (c: Contact) => void
  onScope: (s: Scope) => void
  onOpen: (id: number) => void
}

export function ContactDetail({ contact: c, loading, error, all, mobile, onBack, onEdit, onDelete, onStar, onMove, onChanged, onScope, onOpen }: Props) {
  const { settings } = useApp()
  const { toast } = useToast()
  if (error) {
    return (
      <section className="detail-pane">
        {mobile && <DetailBar onBack={onBack} />}
        <div className="empty"><div className="empty-ico"><Icon name="user" size={22} /></div><h3>{/not found|404/i.test(error) ? 'This contact isn’t here' : 'Couldn’t open this contact'}</h3><div className="muted">{/not found|404/i.test(error) ? 'It was deleted, moved to a book you can’t see, or the link is wrong.' : error}</div></div>
      </section>
    )
  }
  if (!c) return <section className="detail-pane">{mobile && <DetailBar onBack={onBack} />}{loading ? <DetailSkeleton /> : null}</section>
  const card = c.card
  const writable = c.role !== 'read'
  const name = displayName(c, settings?.display_order)
  const phones = card.phones || []
  const emails = card.emails || []
  const addresses = card.addresses || []
  const copy = async (text: string, what: string) => toast(await copyText(text) ? `Copied ${what}` : 'Could not copy', 'ok')

  const moreItems: MenuItem[] = [
    { label: c.starred ? 'Remove from favorites' : 'Add to favorites', icon: 'star', hint: 'S', onSelect: () => onStar(c) },
    { label: 'Copy vCard', icon: 'copy', onSelect: async () => { try { copy(await api.getText(`${V1}/contacts/${c.id}/vcf`), 'vCard') } catch (e) { toast(errMsg(e), 'err') } } },
    { label: 'Download .vcf', icon: 'download', onSelect: () => download(`${V1}/contacts/${c.id}/vcf?download=1`) },
    { label: 'Copy link', icon: 'link', onSelect: () => copy(c.url || `${location.origin}/contacts/${c.id}`, 'link') },
    ...(writable ? ['sep' as const, { label: 'Move to address book…', icon: 'book', onSelect: () => onMove(c) }, { label: 'Delete', icon: 'trash', danger: true, hint: '⌫', onSelect: () => onDelete(c) }] : []),
  ]

  const work = [card.title, card.department, c.kind !== 'org' ? card.org : ''].filter(Boolean)
  const bd = card.birthday ? nextOccurrence(card.birthday) : null
  const an = card.anniversary ? nextOccurrence(card.anniversary) : null
  const findByName = (n: string) => { const f = fold(n); return all?.find(x => fold(x.fn) === f) }

  return (
    <section className="detail-pane" aria-label={name}>
      {mobile && <DetailBar onBack={onBack} right={<>
        {writable && <button type="button" className="btn ghost sm" onClick={() => onEdit(c)}>Edit</button>}
        <MenuButton label="More" align="right" items={moreItems}><Icon name="more" size={18} /></MenuButton>
      </>} />}
      <div className="detail-scroll">
        <div className="detail-inner">
          <header className="detail-hero">
            <PhotoButton c={c} writable={writable} onChanged={onChanged} />
            <div className="detail-names">
              <h1 className="detail-name">{name}</h1>
              {(card.nickname || card.phonetic_given || card.phonetic_family) && <div className="detail-aka">{[card.nickname && `“${card.nickname}”`, [card.phonetic_given, card.phonetic_family].filter(Boolean).join(' ')].filter(Boolean).join(' · ')}</div>}
              {work.length > 0 && <div className="detail-work">{work.join(' · ')}</div>}
              {c.kind === 'org' && <div className="detail-work"><Icon name="building" size={13} /> Company</div>}
            </div>
            {!mobile && (
              <div className="detail-tools">
                <button type="button" className={`btn ghost icon-only star-btn ${c.starred ? 'on' : ''}`} aria-pressed={c.starred} title={c.starred ? 'Favorite (S)' : 'Add to favorites (S)'} aria-label="Favorite" onClick={() => onStar(c)}><Icon name="star" size={18} fill={c.starred} /></button>
                {writable && <button type="button" className="btn sm" onClick={() => onEdit(c)} title="Edit (E)"><Icon name="edit" size={14} />Edit</button>}
                <MenuButton label="More" align="right" items={moreItems}><Icon name="more" size={18} /></MenuButton>
              </div>
            )}
          </header>

          <div className="quick">
            <QuickAction icon="phone" label="Call" options={phones.map(p => ({ label: `${labelText(p.label)} · ${p.value}`, href: telHref(p.value) }))} />
            <QuickAction icon="message" label="Message" options={phones.map(p => ({ label: `${labelText(p.label)} · ${p.value}`, href: smsHref(p.value) }))} />
            <QuickAction icon="mail" label="Email" options={emails.map(e => ({ label: `${labelText(e.label)} · ${e.value}`, href: `mailto:${e.value}` }))} />
            <QuickAction icon="map" label="Map" options={addresses.map(a => ({ label: `${labelText(a.label)} · ${addressLines(a).join(', ')}`, href: mapsHref(a), blank: true }))} />
          </div>

          <div className="detail-grid">
            <div className="detail-fields">
              {phones.length > 0 && <Group>{phones.map((p, i) => <ValueRow key={i} label={labelText(p.label)} pref={p.pref} value={p.value} onCopy={() => copy(p.value, 'number')}
                actions={<><a className="btn ghost icon-only xs" href={telHref(p.value)} title="Call" aria-label="Call"><Icon name="phone" size={13} /></a><a className="btn ghost icon-only xs" href={smsHref(p.value)} title="Message" aria-label="Message"><Icon name="message" size={13} /></a></>} />)}</Group>}
              {emails.length > 0 && <Group>{emails.map((e, i) => <ValueRow key={i} label={labelText(e.label)} pref={e.pref} value={e.value} onCopy={() => copy(e.value, 'email')}
                actions={<a className="btn ghost icon-only xs" href={`mailto:${e.value}`} title="Write an email" aria-label="Write an email"><Icon name="mail" size={13} /></a>} />)}</Group>}
              {addresses.length > 0 && <Group>{addresses.map((a, i) => {
                const lines = addressLines(a)
                return <ValueRow key={i} label={labelText(a.label)} value={<span className="addr">{lines.map((l, j) => <span key={j}>{l}</span>)}</span>} onCopy={() => copy(lines.join('\n'), 'address')}
                  actions={<a className="btn ghost icon-only xs" href={mapsHref(a)} target="_blank" rel="noreferrer" title="Open in Maps" aria-label="Open in Maps"><Icon name="map" size={13} /></a>} />
              })}</Group>}
              {(card.birthday || card.anniversary || (card.dates || []).length > 0) && (
                <Group>
                  {card.birthday && <ValueRow label="Birthday" icon="cake" value={<span>{fmtDate(card.birthday)}{bd && <span className={`when ${bd.days <= 1 ? 'soon' : ''}`}>{bd.age != null ? `turns ${bd.age} ` : ''}{inDays(bd.days)}</span>}</span>} onCopy={() => copy(fmtDate(card.birthday), 'birthday')} />}
                  {card.anniversary && <ValueRow label="Anniversary" icon="heart" value={<span>{fmtDate(card.anniversary)}{an && <span className={`when ${an.days <= 1 ? 'soon' : ''}`}>{an.age != null && an.age > 0 ? `${ordinalAge(an.age)} ` : ''}{inDays(an.days)}</span>}</span>} onCopy={() => copy(fmtDate(card.anniversary), 'date')} />}
                  {(card.dates || []).map((d, i) => <ValueRow key={i} label={labelText(d.label)} value={fmtDate(d.value)} onCopy={() => copy(fmtDate(d.value), 'date')} />)}
                </Group>
              )}
              {(card.related || []).length > 0 && <Group>{(card.related || []).map((r, i) => {
                const m = findByName(r.value)
                return <ValueRow key={i} label={labelText(r.label)} value={m ? <button type="button" className="link-btn" onClick={() => onOpen(m.id)}>{r.value}</button> : r.value} onCopy={m ? undefined : () => copy(r.value, 'name')} />
              })}</Group>}
              {((card.urls || []).length > 0 || (card.socials || []).length > 0 || (card.ims || []).length > 0) && (
                <Group>
                  {(card.urls || []).map((u, i) => <LinkRow key={'u' + i} f={u} href={webHref(u.value)} onCopy={() => copy(u.value, 'address')} />)}
                  {(card.socials || []).map((s, i) => <LinkRow key={'s' + i} f={s} href={socialHref(s)} onCopy={() => copy(s.value, 'profile')} />)}
                  {(card.ims || []).map((s, i) => <LinkRow key={'i' + i} f={s} href={imHref(s)} onCopy={() => copy(s.value, 'handle')} />)}
                </Group>
              )}
              {card.note && <Group><ValueRow label="Notes" value={<span className="note-text">{card.note}</span>} /></Group>}
              {((card.categories || []).length > 0 || (c.groups || []).length > 0) && (
                <Group>
                  <ValueRow label="Labels" value={<span className="chips">
                    {(card.categories || []).map(l => <button type="button" key={l} className="chip sm" onClick={() => onScope({ kind: 'label', name: l })}><Icon name="tag" size={12} />{l}</button>)}
                    {(c.groups || []).map(g => <button type="button" key={g.uid} className="chip sm" onClick={() => onScope({ kind: 'group', uid: g.uid, name: g.name })}><Icon name="users" size={12} />{g.name}</button>)}
                  </span>} />
                </Group>
              )}
              {isBare(c) && <p className="muted small bare-hint">Nothing else saved yet.{writable && <> <button type="button" className="link-btn" onClick={() => onEdit(c)}>Add a phone, email or birthday</button>.</>}</p>}
              <footer className="detail-foot">
                <span className="row"><span className="dot" style={{ background: c.addressbook?.color }} />{c.addressbook?.name}{c.role !== 'owner' && c.addressbook?.owner && <span className="muted">· {c.addressbook.owner.name || c.addressbook.owner.email}</span>}{c.role === 'read' && <span className="badge">Read-only</span>}</span>
                <span className="muted">Edited {ago(c.updated_at)}{sourceText(c.source)}</span>
              </footer>
              <History id={c.id} />
            </div>
            <RelatedPanel contact={c} />
          </div>
        </div>
      </div>
    </section>
  )
}

function isBare(c: Contact): boolean {
  const k = c.card
  return !(k.phones?.length || k.emails?.length || k.addresses?.length || k.birthday || k.note || k.urls?.length || k.socials?.length || k.ims?.length || k.related?.length || k.dates?.length || k.anniversary)
}

function sourceText(s: string): string {
  switch (s) {
    case '': case 'web': return ''
    case 'carddav': return ' · from a phone or computer'
    case 'import': return ' · imported'
    case 'remote': return ' · synced from another server'
    default: return ` · from Pocket ${s.charAt(0).toUpperCase() + s.slice(1)}`
  }
}

function DetailBar({ onBack, right }: { onBack: () => void; right?: React.ReactNode }) {
  return (
    <div className="detail-bar">
      <button type="button" className="btn ghost back-btn" onClick={onBack}><Icon name="chevron-left" size={20} />Contacts</button>
      <span className="spacer" />
      {right}
    </div>
  )
}

function Group({ children }: { children: React.ReactNode }) { return <div className="fgroup">{children}</div> }

function ValueRow({ label, value, onCopy, actions, pref, icon }: { label: string; value: React.ReactNode; onCopy?: () => void; actions?: React.ReactNode; pref?: boolean; icon?: string }) {
  return (
    <div className="frow">
      <div className="flabel">{icon && <Icon name={icon} size={12} />}{label}{pref && <Icon name="star" size={10} fill className="pref-star" title="Preferred" />}</div>
      <div className="fvalue">
        {onCopy ? <button type="button" className="fcopy" onClick={onCopy} title="Click to copy">{value}</button> : <div className="fstatic">{value}</div>}
        {actions && <span className="factions">{actions}</span>}
      </div>
    </div>
  )
}

function LinkRow({ f, href, onCopy }: { f: CardField; href: string | null; onCopy: () => void }) {
  return (
    <div className="frow">
      <div className="flabel">{labelText(f.label)}</div>
      <div className="fvalue">
        {href ? <a className="flink ellipsis" href={href} target="_blank" rel="noreferrer">{f.value}</a> : <button type="button" className="fcopy" onClick={onCopy}>{f.value}</button>}
        <span className="factions"><button type="button" className="btn ghost icon-only xs" title="Copy" aria-label="Copy" onClick={onCopy}><Icon name="copy" size={13} /></button></span>
      </div>
    </div>
  )
}

function QuickAction({ icon, label, options }: { icon: string; label: string; options: { label: string; href: string; blank?: boolean }[] }) {
  const inner = <><span className="qa-ico"><Icon name={icon} size={18} /></span><span className="qa-label">{label}</span></>
  if (options.length === 0) return <span className="qa disabled" aria-disabled="true" title={`No ${label === 'Map' ? 'address' : label === 'Email' ? 'email' : 'phone number'}`}>{inner}</span>
  if (options.length === 1) return <a className="qa" href={options[0].href} target={options[0].blank ? '_blank' : undefined} rel="noreferrer" title={options[0].label}>{inner}</a>
  return (
    <MenuButton className="qa" label={label} width={260} items={options.map(o => ({ label: o.label, onSelect: () => { if (o.blank) window.open(o.href, '_blank', 'noopener'); else location.href = o.href } }))}>{inner}</MenuButton>
  )
}

function PhotoButton({ c, writable, onChanged }: { c: Contact; writable: boolean; onChanged: (c: Contact) => void }) {
  const { toast } = useToast()
  const file = useRef<HTMLInputElement>(null)
  const [over, setOver] = useState(false)
  const [busy, setBusy] = useState(false)
  const upload = async (f: File | undefined) => {
    if (!f) return
    if (!/^image\//.test(f.type)) { toast('That isn’t an image', 'err'); return }
    setBusy(true)
    try {
      const res = await fetch(`${V1}/contacts/${c.id}/photo`, { method: 'PUT', body: f, credentials: 'same-origin', headers: { 'Content-Type': f.type, 'X-Pocket-Source': 'web' } })
      const j = await res.json().catch(() => ({}))
      if (!res.ok) throw new ApiError(res.status, j.error || res.statusText)
      onChanged(j as Contact)
      toast('Photo saved')
    } catch (e) { toast(errMsg(e), 'err') } finally { setBusy(false) }
  }
  const remove = async () => {
    setBusy(true)
    try { onChanged(await api.del<Contact>(`${V1}/contacts/${c.id}/photo`)); toast('Photo removed') } catch (e) { toast(errMsg(e), 'err') } finally { setBusy(false) }
  }
  const avatar = <ContactAvatar c={c} size={88} className="detail-avatar" />
  if (!writable) return <div className="photo-btn static">{avatar}</div>
  return (
    <div className={`photo-btn ${over ? 'over' : ''}`}
      onDragOver={e => { if (e.dataTransfer.types.includes('Files')) { e.preventDefault(); setOver(true) } }}
      onDragLeave={() => setOver(false)}
      onDrop={e => { e.preventDefault(); setOver(false); upload(e.dataTransfer.files[0]) }}>
      <MenuButton className="photo-trigger" label="Change photo" items={[
        { label: c.has_photo ? 'Choose another photo…' : 'Add a photo…', icon: 'image', onSelect: () => file.current?.click() },
        ...(c.has_photo ? [{ label: 'Open full size', icon: 'external', onSelect: () => window.open(`${c.photo_url}${c.photo_url.includes('?') ? '&' : '?'}s=0`, '_blank') }, { label: 'Remove photo', icon: 'trash', danger: true, onSelect: remove }] : []),
      ]}>
        {avatar}
        <span className="photo-edit">{busy ? <Spinner size={16} /> : <Icon name="camera" size={16} />}</span>
      </MenuButton>
      <input ref={file} type="file" accept="image/*" hidden onChange={e => { upload(e.target.files?.[0]); e.target.value = '' }} />
    </div>
  )
}

function History({ id }: { id: number }) {
  const [items, setItems] = useState<Activity[] | null>(null)
  const [err, setErr] = useState('')
  const load = () => { if (items) return; api.get<Activity[]>(`${V1}/contacts/${id}/activity`).then(a => setItems(a || [])).catch(e => setErr(errMsg(e))) }
  return (
    <details className="history" key={id} onToggle={e => { if ((e.target as HTMLDetailsElement).open) load() }}>
      <summary><Icon name="history" size={14} />History</summary>
      {err ? <p className="danger-text small">{err}</p> : !items ? <div className="history-load"><Spinner /></div> : items.length === 0 ? <p className="muted small">No changes recorded yet.</p> : (
        <ol className="history-list">
          {items.map(a => (
            <li key={a.id}><span className="history-what">{a.summary || a.action}</span>{a.detail && <span className="muted"> — {a.detail}</span>}<span className="history-when" title={new Date(a.created_at).toLocaleString()}>{ago(a.created_at)}</span></li>
          ))}
        </ol>
      )}
    </details>
  )
}

function DetailSkeleton() {
  return (
    <div className="detail-scroll"><div className="detail-inner" aria-busy="true">
      <div className="detail-hero">
        <span className="skeleton" style={{ width: 88, height: 88, borderRadius: '50%' }} />
        <div className="detail-names"><span className="skeleton" style={{ width: 220, height: 24 }} /><span className="skeleton" style={{ width: 140, height: 14, marginTop: 10 }} /></div>
      </div>
      <div className="quick">{[0, 1, 2, 3].map(i => <span key={i} className="qa disabled"><span className="skeleton" style={{ width: 40, height: 40, borderRadius: 12 }} /></span>)}</div>
      {[0, 1, 2].map(i => <div key={i} className="fgroup"><span className="skeleton" style={{ width: '60%', height: 14, margin: '14px 0' }} /></div>)}
    </div></div>
  )
}
