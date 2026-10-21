// Create/rename an address book, share it.
import { useEffect, useMemo, useState } from 'react'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import type { AddressBook, Share, ShareRole } from '../lib/types'
import { Icon } from './icons'
import { type Option, Select } from './Select'
import { Avatar, errMsg, Field, Loading, Modal, Switch, useToast } from './ui'

export const PALETTE = ['#e11d48', '#ec4899', '#d946ef', '#8b5cf6', '#6366f1', '#3b82f6', '#0ea5e9', '#06b6d4', '#14b8a6', '#10b981', '#84cc16', '#eab308', '#f59e0b', '#f97316', '#64748b']

export function ColorPalette({ value, onChange }: { value: string; onChange: (c: string) => void }) {
  const lower = (value || '').toLowerCase()
  const isCustom = !!value && !PALETTE.includes(lower)
  return (
    <div className="palette" role="radiogroup" aria-label="Colour">
      {PALETTE.map(c => <button key={c} type="button" role="radio" aria-checked={lower === c} aria-label={c} className={`swatch ${lower === c ? 'active' : ''}`} style={{ background: c }} onClick={() => onChange(c)} />)}
      <label className={`swatch custom ${isCustom ? 'active' : ''}`} title="Custom colour" style={isCustom ? { background: value } : undefined}>
        {!isCustom && <Icon name="plus" size={12} />}
        <input type="color" value={value || '#e11d48'} onChange={e => onChange(e.target.value)} aria-label="Custom colour" />
      </label>
    </div>
  )
}

export function BookDialog({ book, onClose }: { book?: AddressBook; onClose: () => void }) {
  const { reloadBooks, bump, patchBook } = useApp()
  const { toast } = useToast()
  const [name, setName] = useState(book?.name || '')
  const [color, setColor] = useState(book?.color || PALETTE[6])
  const [description, setDescription] = useState(book?.description || '')
  const [isDefault, setIsDefault] = useState(!!book?.is_default)
  const [busy, setBusy] = useState(false)
  const save = async () => {
    if (!name.trim()) return
    setBusy(true)
    try {
      if (book) {
        await patchBook(book.id, { name: name.trim(), color, description, ...(isDefault && !book.is_default ? { is_default: true } : {}) })
        toast('Address book saved')
      } else {
        await api.post(`${V1}/addressbooks`, { name: name.trim(), color, description })
        await reloadBooks()
        toast(`“${name.trim()}” created`)
      }
      bump()
      onClose()
    } catch (e) { toast(errMsg(e), 'err') } finally { setBusy(false) }
  }
  return (
    <Modal title={book ? 'Address book' : 'New address book'} onClose={onClose} size="sm" footer={<>
      <button type="button" className="btn" onClick={onClose}>Cancel</button>
      <button type="button" className="btn primary" disabled={busy || !name.trim()} onClick={save}>{book ? 'Save' : 'Create'}</button>
    </>}>
      <form className="stack" onSubmit={e => { e.preventDefault(); save() }}>
        <div className="book-name-row">
          <span className="book-name-dot" style={{ background: color }} />
          <input className="book-name-input" value={name} onChange={e => setName(e.target.value)} placeholder="Name, e.g. Family" autoFocus aria-label="Name" />
        </div>
        <Field label="Colour" group><ColorPalette value={color} onChange={setColor} /></Field>
        <Field label="Description"><input value={description} onChange={e => setDescription(e.target.value)} placeholder="Optional" /></Field>
        {book && book.role === 'owner' && (
          <label className="inline-switch"><Switch checked={isDefault} onChange={setIsDefault} disabled={book.is_default} label="Default address book" />New contacts go here by default</label>
        )}
        <button type="submit" hidden />
      </form>
    </Modal>
  )
}

export const ROLES: { value: ShareRole; label: string; description: string }[] = [
  { value: 'read', label: 'Can see', description: 'Sees every contact; changes nothing.' },
  { value: 'write', label: 'Can edit', description: 'Adds, changes and deletes contacts.' },
  { value: 'manage', label: 'Can edit and share', description: 'Also renames the book and shares it with others.' },
]
const roleOptions: Option<ShareRole>[] = ROLES.map(r => ({ value: r.value, label: r.label, description: r.description }))

export function ShareDialog({ book, onClose }: { book: AddressBook; onClose: () => void }) {
  const { people, user, reloadBooks } = useApp()
  const { toast } = useToast()
  const [shares, setShares] = useState<Share[] | null>(null)
  const [who, setWho] = useState('')
  const [email, setEmail] = useState('')
  const [role, setRole] = useState<ShareRole>('read')
  const [busy, setBusy] = useState(false)
  const load = async () => { try { setShares((await api.get<Share[]>(`${V1}/addressbooks/${book.id}/shares`)) || []) } catch (e) { toast(errMsg(e), 'err'); setShares([]) } }
  useEffect(() => { load() }, [book.id]) // eslint-disable-line react-hooks/exhaustive-deps

  const list = shares || []
  const taken = new Set(list.map(s => s.user_id ?? (s.everyone ? 'all' : s.email)))
  const whoOptions = useMemo<Option<string>[]>(() => [
    ...(!list.some(s => s.everyone) ? [{ value: 'everyone', label: 'Everyone on this box', description: 'Anyone who signs in here, now and later', icon: <span className="who-ico"><Icon name="users" size={13} /></span> }] : []),
    ...people.filter(p => p.id !== user?.id && p.id !== book.owner_id && !taken.has(p.id)).map(p => ({ value: String(p.id), label: p.name || p.email, description: p.email, keywords: p.email, icon: <Avatar name={p.name} email={p.email} size={20} /> })),
    { value: 'email', label: 'Someone not here yet, by email…', icon: <span className="who-ico"><Icon name="mail" size={13} /></span> },
  ], [people, shares]) // eslint-disable-line react-hooks/exhaustive-deps

  const after = async () => { await load(); reloadBooks().catch(() => {}) }
  const add = async () => {
    const body: Record<string, unknown> = { role }
    if (who === 'everyone') body.everyone = true
    else if (who === 'email') { if (!email.includes('@')) { toast('Type an email address', 'err'); return } body.email = email.trim() }
    else if (who) body.user_id = Number(who)
    else return
    setBusy(true)
    try { await api.post(`${V1}/addressbooks/${book.id}/shares`, body); setWho(''); setEmail(''); toast('Shared'); await after() } catch (e) { toast(errMsg(e), 'err') } finally { setBusy(false) }
  }
  const change = async (s: Share, r: ShareRole) => { try { await api.put(`${V1}/shares/${s.id}`, { role: r }); await after() } catch (e) { toast(errMsg(e), 'err') } }
  const remove = async (s: Share) => { try { await api.del(`${V1}/shares/${s.id}`); await after() } catch (e) { toast(errMsg(e), 'err') } }

  return (
    <Modal title={<span className="row"><span className="dot" style={{ background: book.color }} />Share “{book.name}”</span>} onClose={onClose} size="lg">
      <div className="stack loose">
        <section className="stack tight">
          <h3 className="share-h">Add people</h3>
          <div className="share-add">
            <div className="grow"><Select value={who} options={whoOptions} onChange={setWho} placeholder="Choose a person…" searchable label="Person" /></div>
            <div className="share-role"><Select value={role} options={roleOptions} onChange={setRole} label="Permission" width={300} /></div>
            <button type="button" className="btn primary" disabled={busy || !who || (who === 'email' && !email)} onClick={add}>Share</button>
          </div>
          {who === 'email' && <input type="email" value={email} onChange={e => setEmail(e.target.value)} placeholder="name@example.com — they see it once they sign in with this address" autoFocus />}
        </section>
        <section className="stack tight">
          <h3 className="share-h">Shared with</h3>
          {!shares ? <Loading /> : list.length === 0 ? <p className="muted small">Only you can see this address book.</p> : (
            <div className="share-list">
              {list.map(s => (
                <div key={s.id} className="share-row">
                  {s.everyone ? <span className="who-ico lg"><Icon name="users" size={16} /></span> : <Avatar name={s.person?.name} email={s.person?.email || s.email} size={30} />}
                  <div className="grow">
                    <div className="share-name">{s.everyone ? 'Everyone on this box' : s.person?.name || s.person?.email || s.email}{s.pending && <span className="badge warn" title="Shared by email with someone who has not signed in yet; it binds when they do.">Pending</span>}</div>
                    <div className="muted small">{s.everyone ? 'Anyone who signs in here' : s.person?.email || s.email}</div>
                  </div>
                  <div className="share-role"><Select value={s.role} options={roleOptions} onChange={r => change(s, r)} size="sm" label="Permission" width={300} /></div>
                  <button type="button" className="btn ghost icon-only sm" aria-label="Stop sharing" title="Stop sharing" onClick={() => remove(s)}><Icon name="x" size={15} /></button>
                </div>
              ))}
            </div>
          )}
        </section>
        <p className="muted small" style={{ margin: 0 }}>Shared address books show up on their phones too (CardDAV), next to their own.</p>
      </div>
    </Modal>
  )
}

/** Books the person may add contacts to. */
export const writableBooks = (books: AddressBook[]) => books.filter(b => !b.read_only && (b.role === 'owner' || b.role === 'manage' || b.role === 'write'))

export function bookOptions(books: AddressBook[]): Option<number>[] {
  return writableBooks(books).map(b => ({ value: b.id, label: b.name, color: b.color, description: b.role !== 'owner' ? `Shared by ${b.owner?.name || b.owner?.email || 'someone'}` : b.is_default ? 'Default' : undefined }))
}
