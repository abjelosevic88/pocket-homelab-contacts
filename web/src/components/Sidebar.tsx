import { useState } from 'react'
import { useLocation, useNavigate } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import type { Scope } from '../lib/scope'
import type { AddressBook } from '../lib/types'
import { BookDialog, ShareDialog } from './BookDialogs'
import { Icon } from './icons'
import { Confirm, download, errMsg, Field, Kbd, type MenuItem, MenuButton, Modal, useToast } from './ui'

export interface Counts { all: number | null; favorites: number; birthdays: number; duplicates: number }

interface Props {
  scope: Scope
  onScope: (s: Scope) => void
  counts: Counts
  onNew: () => void
  onNavigate: () => void
}

export function Sidebar({ scope, onScope, counts, onNew, onNavigate }: Props) {
  const { books, categories, settings, patchBook, reloadBooks, reloadCategories, bump, status } = useApp()
  const { toast } = useToast()
  const navigate = useNavigate()
  const loc = useLocation()
  const [bookDlg, setBookDlg] = useState<AddressBook | 'new' | null>(null)
  const [share, setShare] = useState<AddressBook | null>(null)
  const [delBook, setDelBook] = useState<AddressBook | null>(null)
  const [renameLabel, setRenameLabel] = useState<string | null>(null)
  const [delLabel, setDelLabel] = useState<string | null>(null)
  const page = loc.pathname.split('/')[1] || ''
  const onList = page === '' || page === 'contacts'
  const is = (k: Scope['kind'], extra?: (s: Scope) => boolean) => onList && scope.kind === k && (!extra || extra(scope))
  const go = (s: Scope) => { onScope(s); if (!onList || page === 'contacts') navigate('/'); onNavigate() }
  const goPage = (p: string) => { navigate(p); onNavigate() }
  const labels = categories.labels || []
  const groups = categories.groups || []
  const title = settings?.title || status?.title || 'Pocket Contacts'
  const [t1, ...rest] = title.split(' ')

  const bookMenu = (b: AddressBook): MenuItem[] => {
    const manage = b.role === 'owner' || b.role === 'manage'
    return [
      { label: 'Show only this book', icon: 'eye', onSelect: () => go({ kind: 'book', id: b.id }) },
      { label: 'Name & colour…', icon: 'palette', disabled: !manage, onSelect: () => setBookDlg(b) },
      { label: 'Share…', icon: 'users', disabled: !manage || b.read_only && b.kind !== 'local', onSelect: () => setShare(b) },
      { label: 'Export .vcf', icon: 'download', onSelect: () => download(`${V1}/addressbooks/${b.id}/export.vcf`) },
      ...(b.role === 'owner' && !b.is_default ? [{ label: 'Make default', icon: 'star', onSelect: () => patchBook(b.id, { is_default: true }).then(() => toast(`New contacts go to “${b.name}”`)).catch(e => toast(errMsg(e), 'err')) }] : []),
      'sep',
      b.role === 'owner'
        ? { label: 'Delete…', icon: 'trash', danger: true, onSelect: () => setDelBook(b) }
        : { label: 'Leave', icon: 'log-out', danger: true, onSelect: () => setDelBook(b) },
    ]
  }

  const removeBook = async (b: AddressBook) => {
    try {
      if (b.role === 'owner') await api.del(`${V1}/addressbooks/${b.id}`)
      else await api.post(`${V1}/addressbooks/${b.id}/leave`)
      if (scope.kind === 'book' && scope.id === b.id) onScope({ kind: 'all' })
      await reloadBooks(); bump()
      toast(b.role === 'owner' ? `“${b.name}” deleted` : `You left “${b.name}”`)
    } catch (e) { toast(errMsg(e), 'err') }
  }

  return (
    <aside className="sidebar" aria-label="Contacts navigation">
      <div className="sb-brand">
        <img src="/favicon.svg" alt="" className="sb-logo" />
        <span className="sb-title"><b>{t1}</b>{rest.length ? ' ' + rest.join(' ') : ''}</span>
      </div>
      <div className="sb-actions">
        <button type="button" className="btn primary create-btn" onClick={() => { onNew(); onNavigate() }}><Icon name="user-plus" size={17} />New contact<Kbd>N</Kbd></button>
        <button type="button" className="btn icon-only import-btn" title="Import" aria-label="Import" onClick={() => goPage('/import')}><Icon name="upload" size={16} /></button>
      </div>

      <nav className="sb-nav">
        <NavItem icon="contact" label="All contacts" count={counts.all} active={is('all')} onClick={() => go({ kind: 'all' })} />
        <NavItem icon="star" label="Favorites" count={counts.favorites || null} active={is('favorites')} onClick={() => go({ kind: 'favorites' })} />
        <NavItem icon="history" label="Recent" active={is('recent')} onClick={() => go({ kind: 'recent' })} />
        <NavItem icon="cake" label="Birthdays" count={counts.birthdays || null} hint="next 30 days" active={page === 'birthdays'} onClick={() => goPage('/birthdays')} />
        <NavItem icon="merge" label="Duplicates" count={counts.duplicates || null} badge active={page === 'duplicates'} onClick={() => goPage('/duplicates')} />
        <NavItem icon="trash" label="Recently deleted" active={page === 'deleted'} onClick={() => goPage('/deleted')} />
      </nav>

      <section className="sb-sec">
        <div className="sb-h"><span>Address books</span><button type="button" className="btn ghost xs icon-only" aria-label="New address book" title="New address book" onClick={() => setBookDlg('new')}><Icon name="plus" size={14} /></button></div>
        <ul className="sb-list">
          {books.map(b => {
            const shared = b.role !== 'owner'
            const active = is('book', s => s.kind === 'book' && s.id === b.id)
            return (
              <li key={b.id} className={`sb-row ${b.visible ? '' : 'off'} ${active ? 'active' : ''}`}>
                <button type="button" className="sb-box" style={{ ['--c' as string]: b.color }} role="checkbox" aria-checked={b.visible}
                  title={b.visible ? 'Shown in All contacts — click to hide' : 'Hidden from All contacts — click to show'}
                  onClick={() => patchBook(b.id, { visible: !b.visible }).catch(e => toast(errMsg(e), 'err'))}>
                  {b.visible && <Icon name="check" size={11} strokeWidth={3} />}
                </button>
                <button type="button" className="sb-name" onClick={() => go({ kind: 'book', id: b.id })} title={b.description || b.name}>
                  <span className="ellipsis">{b.name}</span>
                  {shared && <span className="sb-owner ellipsis">{b.owner?.name || b.owner?.email}</span>}
                  {!shared && !!b.shares?.length && <Icon name="users" size={12} className="sb-flag" title="Shared" />}
                  {b.kind !== 'local' && <Icon name="refresh" size={11} className="sb-flag" title={b.read_only ? 'Synced, read-only' : 'Synced with another server'} />}
                  {b.read_only && b.kind === 'local' && <Icon name="lock" size={11} className="sb-flag" title="Read-only" />}
                </button>
                <span className="sb-count tnum">{b.contacts || ''}</span>
                <MenuButton label={`More for ${b.name}`} className="btn ghost xs icon-only sb-more" align="right" items={() => bookMenu(b)}><Icon name="more" size={14} /></MenuButton>
              </li>
            )
          })}
        </ul>
      </section>

      {(labels.length > 0 || groups.length > 0) && (
        <section className="sb-sec">
          <div className="sb-h"><span>Labels</span></div>
          <ul className="sb-list">
            {labels.map(l => (
              <li key={l.name} className={`sb-row ${is('label', s => s.kind === 'label' && s.name === l.name) ? 'active' : ''}`}>
                <button type="button" className="sb-name with-ico" onClick={() => go({ kind: 'label', name: l.name })}><Icon name="tag" size={14} className="sb-ico" /><span className="ellipsis">{l.name}</span></button>
                <span className="sb-count tnum">{l.count}</span>
                <MenuButton label={`More for ${l.name}`} className="btn ghost xs icon-only sb-more" align="right" items={[
                  { label: 'Rename…', icon: 'edit', onSelect: () => setRenameLabel(l.name) },
                  { label: 'Remove label…', icon: 'trash', danger: true, onSelect: () => setDelLabel(l.name) },
                ]}><Icon name="more" size={14} /></MenuButton>
              </li>
            ))}
            {groups.map(g => (
              <li key={g.uid} className={`sb-row ${is('group', s => s.kind === 'group' && s.uid === g.uid) ? 'active' : ''}`}>
                <button type="button" className="sb-name with-ico" onClick={() => go({ kind: 'group', uid: g.uid, name: g.name })} title="A group from Apple Contacts"><Icon name="users" size={14} className="sb-ico" /><span className="ellipsis">{g.name}</span></button>
                <span className="sb-count tnum">{g.count}</span>
              </li>
            ))}
          </ul>
        </section>
      )}

      <div className="sb-foot">
        <button type="button" className={`sb-foot-link ${page === 'import' ? 'active' : ''}`} onClick={() => goPage('/import')}><Icon name="upload" size={15} />Import</button>
        <button type="button" className="sb-foot-link" onClick={() => goPage('/settings')}><Icon name="settings" size={15} />Settings</button>
      </div>

      {bookDlg && <BookDialog book={bookDlg === 'new' ? undefined : bookDlg} onClose={() => setBookDlg(null)} />}
      {share && <ShareDialog book={share} onClose={() => setShare(null)} />}
      {delBook && <Confirm title={delBook.role === 'owner' ? `Delete “${delBook.name}”?` : `Leave “${delBook.name}”?`} confirmLabel={delBook.role === 'owner' ? 'Delete' : 'Leave'}
        message={delBook.role === 'owner'
          ? <>Its {delBook.contacts} contact{delBook.contacts === 1 ? '' : 's'} are deleted too, also on every phone that syncs it. People it is shared with lose it.</>
          : <>It disappears from your list and your phone. {delBook.owner?.name || 'The owner'} keeps it and can share it again.</>}
        onCancel={() => setDelBook(null)} onConfirm={() => { const b = delBook; setDelBook(null); removeBook(b) }} />}
      {renameLabel && <RenameLabel from={renameLabel} onClose={() => setRenameLabel(null)} onDone={to => {
        if (scope.kind === 'label' && scope.name === renameLabel) onScope({ kind: 'label', name: to })
        setRenameLabel(null); reloadCategories(); bump()
      }} />}
      {delLabel && <Confirm title={`Remove the label “${delLabel}”?`} confirmLabel="Remove"
        message="The label comes off every contact you may change. The contacts themselves stay."
        onCancel={() => setDelLabel(null)} onConfirm={async () => {
          const name = delLabel; setDelLabel(null)
          try { await api.post(`${V1}/categories/delete`, { name }); if (scope.kind === 'label' && scope.name === name) onScope({ kind: 'all' }); reloadCategories(); bump(); toast(`Label “${name}” removed`) } catch (e) { toast(errMsg(e), 'err') }
        }} />}
    </aside>
  )
}

function NavItem({ icon, label, count, active, onClick, hint, badge }: { icon: string; label: string; count?: number | null; active: boolean; onClick: () => void; hint?: string; badge?: boolean }) {
  return (
    <button type="button" className={`sb-item ${active ? 'active' : ''}`} onClick={onClick} aria-current={active ? 'page' : undefined} title={hint ? `${label} (${hint})` : label}>
      <Icon name={icon} size={16} />
      <span className="grow ellipsis">{label}</span>
      {count != null && count > 0 && <span className={badge ? 'badge warn' : 'sb-count tnum'}>{count}</span>}
    </button>
  )
}

function RenameLabel({ from, onClose, onDone }: { from: string; onClose: () => void; onDone: (to: string) => void }) {
  const { toast } = useToast()
  const [to, setTo] = useState(from)
  const [busy, setBusy] = useState(false)
  const save = async () => {
    const t = to.trim()
    if (!t || t === from) { onClose(); return }
    setBusy(true)
    try { const r = await api.post<{ changed: number }>(`${V1}/categories/rename`, { from, to: t }); toast(`Renamed on ${r?.changed ?? 0} contact${r?.changed === 1 ? '' : 's'}`); onDone(t) } catch (e) { toast(errMsg(e), 'err') } finally { setBusy(false) }
  }
  return (
    <Modal title="Rename label" size="sm" onClose={onClose} footer={<>
      <button type="button" className="btn" onClick={onClose}>Cancel</button>
      <button type="button" className="btn primary" disabled={busy || !to.trim()} onClick={save}>Rename</button>
    </>}>
      <form onSubmit={e => { e.preventDefault(); save() }}>
        <Field label="New name" help="Renames it on every contact you may change — on your phone too."><input value={to} onChange={e => setTo(e.target.value)} autoFocus /></Field>
      </form>
    </Modal>
  )
}
