// New and edit: every field of a card, with labels, own words, dates without a year, and a 412 guard.
import React, { useEffect, useMemo, useRef, useState } from 'react'
import { api, ApiError, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import {
  ADDRESS_LABELS, cardTitle, cleanCard, DATE_LABELS, EMAIL_LABELS, emptyCard, IM_LABELS, labelText, normalizeCard, PHONE_LABELS, RELATED_LABELS, SOCIAL_LABELS, URL_LABELS,
} from '../lib/contacts'
import type { Card, CardAddress, CardField, Contact } from '../lib/types'
import { bookOptions, writableBooks } from './BookDialogs'
import { ContactAvatar } from './ContactAvatar'
import { AddChip, ChipInput, DateField, LabelSelect, RowShell } from './fields'
import { Icon } from './icons'
import { Select } from './Select'
import { Confirm, errMsg, Field, Kbd, Modal, Segmented, Spinner, useToast } from './ui'

export type EditorMode = { kind: 'new'; initial?: Partial<Card>; bookId?: number } | { kind: 'edit'; contact: Contact }

type ListKey = 'phones' | 'emails' | 'urls' | 'ims' | 'socials' | 'related' | 'dates'
const LISTS: Record<ListKey, { labels: string[]; add: string; icon: string; placeholder: string; def: string; type?: string; inputMode?: React.HTMLAttributes<HTMLInputElement>['inputMode'] }> = {
  phones: { labels: PHONE_LABELS, add: 'Phone', icon: 'phone', placeholder: 'Phone', def: 'mobile', type: 'tel', inputMode: 'tel' },
  emails: { labels: EMAIL_LABELS, add: 'Email', icon: 'mail', placeholder: 'Email', def: 'home', type: 'email', inputMode: 'email' },
  urls: { labels: URL_LABELS, add: 'Website', icon: 'globe', placeholder: 'https://', def: 'homepage', inputMode: 'url' },
  ims: { labels: IM_LABELS, add: 'Messenger', icon: 'message', placeholder: 'Handle or number', def: 'telegram' },
  socials: { labels: SOCIAL_LABELS, add: 'Social profile', icon: 'at', placeholder: 'Profile address or @handle', def: 'linkedin' },
  related: { labels: RELATED_LABELS, add: 'Related person', icon: 'users', placeholder: 'Name', def: 'spouse' },
  dates: { labels: DATE_LABELS, add: 'Date', icon: 'calendar', placeholder: '', def: 'other' },
}

export function ContactEditor({ mode, onClose, onSaved }: { mode: EditorMode; onClose: () => void; onSaved: (c: Contact) => void }) {
  const { books, settings, categories } = useApp()
  const { toast } = useToast()
  const editing = mode.kind === 'edit' ? mode.contact : null
  const startCard = useMemo(() => {
    if (editing) return normalizeCard(editing.card)
    const c = normalizeCard({ ...emptyCard(), ...(mode.kind === 'new' ? mode.initial : {}) } as Card)
    if (!c.phones!.length) c.phones = [{ label: 'mobile', value: '' }]
    if (!c.emails!.length) c.emails = [{ label: 'home', value: '' }]
    return c
  }, []) // eslint-disable-line react-hooks/exhaustive-deps
  const writable = writableBooks(books)
  const defaultBook = (mode.kind === 'new' && mode.bookId && writable.some(b => b.id === mode.bookId) ? mode.bookId : 0)
    || (settings?.default_addressbook_id && writable.some(b => b.id === settings.default_addressbook_id) ? settings.default_addressbook_id : 0)
    || writable.find(b => b.is_default)?.id || writable[0]?.id || 0
  const startBook = editing ? editing.addressbook_id : defaultBook

  const [card, setCard] = useState<Card>(startCard)
  const [base, setBase] = useState<{ card: Card; etag: string }>({ card: startCard, etag: editing?.etag || '' })
  const [book, setBook] = useState(startBook)
  const [starred, setStarred] = useState(false)
  const [moreName, setMoreName] = useState(!!(startCard.prefix || startCard.middle || startCard.suffix || startCard.nickname || startCard.phonetic_given || startCard.phonetic_family))
  const [photo, setPhoto] = useState<{ file: File; url: string } | 'remove' | null>(null)
  const [busy, setBusy] = useState(false)
  const [conflict, setConflict] = useState(false)
  const [confirmClose, setConfirmClose] = useState(false)
  const [show, setShow] = useState({ address: !!startCard.addresses!.length, birthday: !!startCard.birthday, anniversary: !!startCard.anniversary, note: !!startCard.note, labels: !!startCard.categories!.length })
  const file = useRef<HTMLInputElement>(null)
  const firstField = useRef<HTMLInputElement>(null)

  const dirty = JSON.stringify(cleanCard(card)) !== JSON.stringify(cleanCard(base.card)) || book !== startBook || photo !== null || starred
  useEffect(() => {
    if (!dirty) return
    const h = (e: BeforeUnloadEvent) => { e.preventDefault() }
    window.addEventListener('beforeunload', h)
    return () => window.removeEventListener('beforeunload', h)
  }, [dirty])
  useEffect(() => () => { if (photo && photo !== 'remove') URL.revokeObjectURL(photo.url) }, [photo])

  const set = <K extends keyof Card>(k: K, v: Card[K]) => setCard(c => ({ ...c, [k]: v }))
  const setList = (k: ListKey, fn: (xs: CardField[]) => CardField[]) => setCard(c => ({ ...c, [k]: fn([...(c[k] || [])]) }))
  const setAddr = (fn: (xs: CardAddress[]) => CardAddress[]) => setCard(c => ({ ...c, addresses: fn([...(c.addresses || [])]) }))
  const isOrg = card.kind === 'org'
  const title = cardTitle({ ...card, fn: '' }) || (editing ? cardTitle(base.card) : '') || (isOrg ? 'New company' : 'New contact')
  const hasName = !!(card.given || card.family || card.org || card.nickname || card.middle || card.prefix || card.suffix || (card.emails || []).some(e => e.value.trim()) || (card.phones || []).some(p => p.value.trim()))

  const close = () => { if (dirty && !busy) setConfirmClose(true); else onClose() }

  const save = async (force = false) => {
    if (busy) return
    if (!hasName) { toast('Add a name, a company, a phone or an email first', 'err'); firstField.current?.focus(); return }
    if (!book) { toast('Pick an address book you may change', 'err'); return }
    setBusy(true)
    try {
      const body = cleanCard(card)
      const before = base.card
      const nameChanged = ['prefix', 'given', 'middle', 'family', 'suffix', 'org', 'kind'].some(k => (body as unknown as Record<string, string>)[k] !== (before as unknown as Record<string, string>)[k])
      if (nameChanged || !editing) body.fn = ''
      let c: Contact
      if (!editing) {
        c = await api.post<Contact>(`${V1}/contacts`, { addressbook_id: book, card: body, ...(starred ? { starred: true } : {}) })
      } else {
        try {
          c = await api.put<Contact>(`${V1}/contacts/${editing.id}`, force ? { card: body } : { card: body, etag: base.etag })
        } catch (e) {
          if (e instanceof ApiError && e.status === 412) { setConflict(true); setBusy(false); return }
          throw e
        }
        if (book !== editing.addressbook_id) c = await api.patch<Contact>(`${V1}/contacts/${editing.id}`, { addressbook_id: book })
      }
      if (photo === 'remove') c = await api.del<Contact>(`${V1}/contacts/${c.id}/photo`).catch(() => c)
      else if (photo) {
        const res = await fetch(`${V1}/contacts/${c.id}/photo`, { method: 'PUT', body: photo.file, credentials: 'same-origin', headers: { 'Content-Type': photo.file.type, 'X-Pocket-Source': 'web' } })
        if (res.ok) c = await res.json(); else toast('Saved, but the photo didn’t upload', 'err')
      }
      toast(editing ? 'Saved' : `${cardTitle({ ...body, fn: '' }) || 'Contact'} added`)
      onSaved(c)
    } catch (e) { toast(errMsg(e), 'err') } finally { setBusy(false) }
  }

  const reload = async () => {
    if (!editing) return
    try {
      const fresh = await api.get<Contact>(`${V1}/contacts/${editing.id}`)
      const c = normalizeCard(fresh.card)
      setCard(c); setBase({ card: c, etag: fresh.etag }); setConflict(false)
      toast('Loaded the latest version')
    } catch (e) { toast(errMsg(e), 'err') }
  }

  const listRows = (k: ListKey) => {
    const meta = LISTS[k]
    const xs = card[k] || []
    if (!xs.length) return null
    return (
      <div className="esec">
        {xs.map((f, i) => (
          <RowShell key={i} onRemove={() => setList(k, l => l.filter((_, j) => j !== i))} removeLabel={`Remove ${meta.add.toLowerCase()}`}>
            <LabelSelect value={f.label || meta.def} labels={meta.labels} onChange={v => setList(k, l => { l[i] = { ...l[i], label: v }; return l })} />
            {k === 'dates'
              ? <DateField label={labelText(f.label)} value={f.value} onChange={v => setList(k, l => { l[i] = { ...l[i], value: v }; return l })} />
              : <input className="evalue" type={meta.type || 'text'} inputMode={meta.inputMode} value={f.value} placeholder={meta.placeholder} aria-label={`${labelText(f.label)} ${meta.add.toLowerCase()}`}
                autoComplete="off" spellCheck={k === 'related'} onChange={e => { const v = e.target.value; setList(k, l => { l[i] = { ...l[i], value: v }; return l }) }} />}
            {(k === 'phones' || k === 'emails') && (
              <button type="button" className={`btn ghost icon-only sm pref ${f.pref ? 'on' : ''}`} aria-pressed={!!f.pref} title={f.pref ? 'Preferred' : 'Make preferred'} aria-label="Preferred"
                onClick={() => setList(k, l => l.map((x, j) => ({ ...x, pref: j === i ? !x.pref : false })))}><Icon name="star" size={14} fill={!!f.pref} /></button>
            )}
          </RowShell>
        ))}
      </div>
    )
  }
  const addRow = (k: ListKey) => setList(k, l => [...l, { label: LISTS[k].def, value: '' }])

  const avatarFace = { ...(editing || {}), fn: title, initials: '', color: editing?.color || 'var(--accent)', kind: card.kind,
    photo_url: photo && photo !== 'remove' ? photo.url : photo === 'remove' ? '' : editing?.photo_url, has_photo: photo && photo !== 'remove' ? true : photo === 'remove' ? false : editing?.has_photo }

  return (
    <Modal bare onClose={close} size="xl" className="editor full-mobile" labelledBy="editor-title">
      <div className="editor-wrap" onKeyDown={e => { if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') { e.preventDefault(); save() } }}>
        <div className="editor-bar">
          <button type="button" className="btn ghost" onClick={close}>Cancel</button>
          <h2 id="editor-title" className="editor-bar-title ellipsis">{editing ? `Edit ${title}` : title}</h2>
          <button type="button" className="btn primary" onClick={() => save()} disabled={busy} title="Save (⌘↵)">{busy && <Spinner size={13} />}{editing ? 'Save' : 'Add'}<Kbd>⌘↵</Kbd></button>
        </div>
        <div className="editor-body">
          {conflict && (
            <div className="callout warn conflict">
              <Icon name="alert" size={16} />
              <div className="grow"><b>Changed somewhere else.</b> Someone (or a phone) saved this contact while you were editing. Reload to see theirs and lose yours, or overwrite theirs with yours.</div>
              <div className="row"><button type="button" className="btn sm" onClick={reload}>Reload</button><button type="button" className="btn sm danger" onClick={() => { setConflict(false); save(true) }}>Overwrite</button></div>
            </div>
          )}
          <div className="ehead">
            <div className="ephoto">
              <button type="button" className="ephoto-btn" onClick={() => file.current?.click()} aria-label="Choose a photo"
                onDragOver={e => { if (e.dataTransfer.types.includes('Files')) e.preventDefault() }}
                onDrop={e => { e.preventDefault(); const f = e.dataTransfer.files[0]; if (f && /^image\//.test(f.type)) setPhoto({ file: f, url: URL.createObjectURL(f) }) }}>
                <ContactAvatar c={avatarFace} size={84} />
                <span className="photo-edit"><Icon name="camera" size={15} /></span>
              </button>
              {(avatarFace.has_photo) && <button type="button" className="link-btn small" onClick={() => setPhoto('remove')}>Remove photo</button>}
              <input ref={file} type="file" accept="image/*" hidden onChange={e => { const f = e.target.files?.[0]; if (f) setPhoto({ file: f, url: URL.createObjectURL(f) }); e.target.value = '' }} />
            </div>
            <div className="enames">
              <Segmented size="sm" label="Kind" value={isOrg ? 'org' : 'individual'} onChange={v => set('kind', v as Card['kind'])}
                options={[{ value: 'individual', label: <span className="row"><Icon name="user" size={13} />Person</span> }, { value: 'org', label: <span className="row"><Icon name="building" size={13} />Company</span> }]} />
              {isOrg ? (
                <input ref={firstField} className="ename-big" value={card.org} onChange={e => set('org', e.target.value)} placeholder="Company name" aria-label="Company name" autoFocus />
              ) : (
                <div className="two-col tight">
                  <input ref={firstField} className="ename-big" value={card.given} onChange={e => set('given', e.target.value)} placeholder="First name" aria-label="First name" autoFocus autoComplete="off" />
                  <input className="ename-big" value={card.family} onChange={e => set('family', e.target.value)} placeholder="Last name" aria-label="Last name" autoComplete="off" />
                </div>
              )}
              {!isOrg && moreName && (
                <div className="ename-more">
                  <input value={card.prefix} onChange={e => set('prefix', e.target.value)} placeholder="Prefix (Dr.)" aria-label="Prefix" />
                  <input value={card.middle} onChange={e => set('middle', e.target.value)} placeholder="Middle name" aria-label="Middle name" />
                  <input value={card.suffix} onChange={e => set('suffix', e.target.value)} placeholder="Suffix (Jr.)" aria-label="Suffix" />
                  <input value={card.nickname} onChange={e => set('nickname', e.target.value)} placeholder="Nickname" aria-label="Nickname" />
                  <input value={card.phonetic_given} onChange={e => set('phonetic_given', e.target.value)} placeholder="Phonetic first name" aria-label="Phonetic first name" />
                  <input value={card.phonetic_family} onChange={e => set('phonetic_family', e.target.value)} placeholder="Phonetic last name" aria-label="Phonetic last name" />
                </div>
              )}
              {!isOrg && !moreName && <button type="button" className="link-btn small more-name" onClick={() => setMoreName(true)}>More name fields</button>}
              <div className={isOrg ? 'two-col tight' : 'three-col tight'}>
                {!isOrg && <input value={card.org} onChange={e => set('org', e.target.value)} placeholder="Company" aria-label="Company" />}
                <input value={card.department} onChange={e => set('department', e.target.value)} placeholder="Department" aria-label="Department" />
                <input value={card.title} onChange={e => set('title', e.target.value)} placeholder="Job title" aria-label="Job title" />
              </div>
            </div>
          </div>

          <div className="efields">
            {listRows('phones')}
            {listRows('emails')}
            {show.address && (card.addresses || []).length > 0 && (
              <div className="esec">
                {(card.addresses || []).map((a, i) => {
                  const upd = (k: keyof CardAddress, v: string) => setAddr(l => { l[i] = { ...l[i], [k]: v }; return l })
                  return (
                    <RowShell key={i} onRemove={() => setAddr(l => l.filter((_, j) => j !== i))} removeLabel="Remove address">
                      <LabelSelect value={a.label || 'home'} labels={ADDRESS_LABELS} onChange={v => upd('label', v)} />
                      <div className="eaddr">
                        <input value={a.street} onChange={e => upd('street', e.target.value)} placeholder="Street and number" aria-label="Street" autoComplete="off" />
                        <input value={a.ext || ''} onChange={e => upd('ext', e.target.value)} placeholder="Apartment, floor (optional)" aria-label="Address line 2" autoComplete="off" />
                        <div className="eaddr-2"><input value={a.postcode} onChange={e => upd('postcode', e.target.value)} placeholder="Postcode" aria-label="Postcode" className="eaddr-pc" /><input value={a.city} onChange={e => upd('city', e.target.value)} placeholder="City" aria-label="City" /></div>
                        <div className="eaddr-2"><input value={a.region} onChange={e => upd('region', e.target.value)} placeholder="Region / state" aria-label="Region" /><input value={a.country} onChange={e => upd('country', e.target.value)} placeholder="Country" aria-label="Country" /></div>
                        {(a.pobox || '') !== '' && <input value={a.pobox || ''} onChange={e => upd('pobox', e.target.value)} placeholder="PO box" aria-label="PO box" />}
                      </div>
                    </RowShell>
                  )
                })}
              </div>
            )}
            {(show.birthday || show.anniversary) && (
              <div className="esec">
                {show.birthday && <RowShell onRemove={() => { set('birthday', ''); setShow(s => ({ ...s, birthday: false })) }} removeLabel="Remove birthday"><span className="elabel"><Icon name="cake" size={13} />Birthday</span><DateField label="Birthday" value={card.birthday} onChange={v => set('birthday', v)} /></RowShell>}
                {show.anniversary && <RowShell onRemove={() => { set('anniversary', ''); setShow(s => ({ ...s, anniversary: false })) }} removeLabel="Remove anniversary"><span className="elabel"><Icon name="heart" size={13} />Anniversary</span><DateField label="Anniversary" value={card.anniversary} onChange={v => set('anniversary', v)} /></RowShell>}
              </div>
            )}
            {listRows('dates')}
            {listRows('related')}
            {listRows('urls')}
            {listRows('socials')}
            {listRows('ims')}
            {show.note && (
              <div className="esec">
                <Field label="Notes"><textarea value={card.note} onChange={e => set('note', e.target.value)} rows={4} placeholder="Anything worth remembering — how you met, what they like…" /></Field>
              </div>
            )}
            {show.labels && (
              <div className="esec">
                <Field label="Labels" group help="Labels show up as groups on Android, in Nextcloud and in Google."><ChipInput value={card.categories || []} onChange={v => set('categories', v)} suggestions={(categories.labels || []).map(l => l.name)} /></Field>
              </div>
            )}

            <div className="add-chips">
              <AddChip icon="phone" label="Phone" onClick={() => addRow('phones')} />
              <AddChip icon="mail" label="Email" onClick={() => addRow('emails')} />
              <AddChip icon="map" label="Address" onClick={() => { setAddr(l => [...l, { label: (card.addresses || []).length ? 'work' : 'home', street: '', city: '', region: '', postcode: '', country: '' }]); setShow(s => ({ ...s, address: true })) }} />
              {!show.birthday && <AddChip icon="cake" label="Birthday" onClick={() => setShow(s => ({ ...s, birthday: true }))} />}
              {!show.anniversary && <AddChip icon="heart" label="Anniversary" onClick={() => setShow(s => ({ ...s, anniversary: true }))} />}
              <AddChip icon="calendar" label="Other date" onClick={() => addRow('dates')} />
              <AddChip icon="users" label="Related person" onClick={() => addRow('related')} />
              <AddChip icon="globe" label="Website" onClick={() => addRow('urls')} />
              <AddChip icon="at" label="Social profile" onClick={() => addRow('socials')} />
              <AddChip icon="message" label="Messenger" onClick={() => addRow('ims')} />
              {!show.note && <AddChip icon="note" label="Notes" onClick={() => setShow(s => ({ ...s, note: true }))} />}
              {!show.labels && <AddChip icon="tag" label="Labels" onClick={() => setShow(s => ({ ...s, labels: true }))} />}
            </div>

            <div className="esec ebook">
              <Field label="Address book" group>
                {writable.length ? <Select value={book} options={bookOptions(books)} onChange={setBook} label="Address book" />
                  : <div className="callout warn"><Icon name="lock" size={15} /><span>You can’t add contacts to any address book. Create one in the sidebar first.</span></div>}
              </Field>
              {!editing && <label className="inline-switch"><input type="checkbox" checked={starred} onChange={e => setStarred(e.target.checked)} />Add to favorites</label>}
            </div>
          </div>
        </div>
      </div>
      {confirmClose && <Confirm title="Discard your changes?" confirmLabel="Discard" message="What you typed here isn’t saved yet." onCancel={() => setConfirmClose(false)} onConfirm={() => { setConfirmClose(false); onClose() }} />}
    </Modal>
  )
}
