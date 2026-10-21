// The app shell: sidebar | list | detail on desktop, one column on phones; pages; shortcuts; palette.
import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Navigate, useLocation, useNavigate } from 'react-router-dom'
import { api, ApiError, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { displayName, haystack, matches, sortContacts } from '../lib/contacts'
import { isLocalScope, loadScope, type Scope, sameScope, scopeQuery, scopeTitle, storeScope } from '../lib/scope'
import type { Birthday, Contact, ContactList as ContactListT, Deleted, DuplicateGroup, Summary } from '../lib/types'
import { bookOptions, contactWritable } from '../components/BookDialogs'
import { CommandPalette, type PaletteCommand } from '../components/CommandPalette'
import { ContactDetail } from '../components/ContactDetail'
import { ContactEditor, type EditorMode } from '../components/ContactEditor'
import { ContactList } from '../components/ContactList'
import { ErrorBoundary } from '../components/ErrorBoundary'
import { Icon } from '../components/icons'
import { Select } from '../components/Select'
import { ShortcutsSheet } from '../components/ShortcutsSheet'
import { type Counts, Sidebar } from '../components/Sidebar'
import { Confirm, download, errMsg, Field, type MenuItem, MenuButton, Modal, modalOpen, useDebounce, useIsMobile, useMediaQuery, useToast } from '../components/ui'
import BirthdaysPage from './BirthdaysPage'
import DeletedPage from './DeletedPage'
import DuplicatesPage from './DuplicatesPage'
import ImportPage from './ImportPage'

type Pending = { kind: 'delete'; items: Summary[] } | { kind: 'move'; items: Summary[] } | { kind: 'newLabel'; items: Summary[] }

export default function ContactsApp() {
  const { settings, books, categories, version, bump, reloadBooks, reloadCategories, setThemeMode, theme } = useApp()
  const { toast } = useToast()
  const loc = useLocation()
  const navigate = useNavigate()
  const mobile = useIsMobile()
  const narrow = useMediaQuery('(max-width: 1100px)')
  const drawer = mobile || narrow
  const [drawerOpen, setDrawerOpen] = useState(false)
  const openedAt = useRef(0)
  useEffect(() => setDrawerOpen(false), [drawer])

  const seg = loc.pathname.split('/').filter(Boolean)
  const page = seg[0] || ''
  const routeId = page === 'contacts' && seg[1] && /^\d+$/.test(seg[1]) ? Number(seg[1]) : null
  const onList = page === '' || page === 'contacts'

  const [scope, setScopeRaw] = useState<Scope>(loadScope)
  const [query, setQuery] = useState('')
  const [checked, setChecked] = useState<Set<number>>(new Set())
  const anchor = useRef<number | null>(null)
  const setScope = useCallback((s: Scope) => {
    storeScope(s)
    setScopeRaw(prev => sameScope(prev, s) ? prev : s)
    setChecked(new Set()); setQuery('')
  }, [])

  // ---------- data ----------
  const [all, setAll] = useState<Summary[] | null>(null)
  const [scoped, setScoped] = useState<Summary[] | null>(null)
  const [remote, setRemote] = useState<{ q: string; key: string; list: Summary[] } | null>(null)
  const [searching, setSearching] = useState(false)
  const [counts, setCounts] = useState<Omit<Counts, 'all' | 'favorites'>>({ birthdays: 0, duplicates: 0 })
  const sortBy = settings?.sort_by || 'given'

  useEffect(() => {
    let live = true
    api.get<ContactListT>(`${V1}/contacts?limit=5000`).then(r => { if (live) setAll(sortContacts(r?.contacts || [], sortBy)) }).catch(e => { if (live) { setAll(a => a || []); toast(errMsg(e), 'err') } })
    return () => { live = false }
  }, [version, sortBy]) // eslint-disable-line react-hooks/exhaustive-deps

  const scopeKey = JSON.stringify(scope)
  useEffect(() => {
    if (isLocalScope(scope)) { setScoped(null); return }
    let live = true
    setScoped(s => (s && (s as Summary[] & { key?: string }).key === scopeKey ? s : null))
    api.get<ContactListT>(`${V1}/contacts${scopeQuery(scope)}`).then(r => {
      if (!live) return
      const list = r?.contacts || []
      const out = (scope.kind === 'recent' ? list : sortContacts(list, sortBy)) as Summary[] & { key?: string }
      out.key = scopeKey
      setScoped(out)
    }).catch(e => { if (live) { setScoped([]); toast(errMsg(e), 'err') } })
    return () => { live = false }
  }, [scopeKey, version, sortBy]) // eslint-disable-line react-hooks/exhaustive-deps

  const dq = useDebounce(query.trim(), 250)
  useEffect(() => {
    if (dq.length < 3) { setRemote(null); setSearching(false); return }
    let live = true
    setSearching(true)
    api.get<ContactListT>(`${V1}/contacts${scopeQuery(scope, { q: dq })}`).then(r => { if (live) setRemote({ q: dq, key: scopeKey, list: r?.contacts || [] }) })
      .catch(() => { if (live) setRemote(null) }).finally(() => { if (live) setSearching(false) })
    return () => { live = false }
  }, [dq, scopeKey, version]) // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    let live = true
    api.get<Birthday[]>(`${V1}/birthdays?days=30`).then(b => live && setCounts(c => ({ ...c, birthdays: (b || []).length }))).catch(() => {})
    api.get<DuplicateGroup[]>(`${V1}/duplicates`).then(d => live && setCounts(c => ({ ...c, duplicates: (d || []).length }))).catch(() => {})
    return () => { live = false }
  }, [version])

  const hay = useMemo(() => {
    const m = new Map<number, string>()
    for (const c of all || []) m.set(c.id, haystack(c))
    for (const c of scoped || []) if (!m.has(c.id)) m.set(c.id, haystack(c))
    return m
  }, [all, scoped])

  const base = useMemo<Summary[] | null>(() => {
    if (scope.kind === 'all') return all
    if (scope.kind === 'favorites') return all ? all.filter(c => c.starred) : null
    return scoped
  }, [scope.kind, all, scoped])

  const items = useMemo<Summary[] | null>(() => {
    const q = query.trim()
    if (!base || !q) return base
    const local = base.filter(c => matches(c, q, hay.get(c.id)))
    if (!remote || remote.key !== scopeKey || q.length < 3 || !q.startsWith(remote.q.slice(0, Math.min(remote.q.length, q.length)))) return local
    const seen = new Set(local.map(c => c.id))
    const extra = remote.list.filter(c => !seen.has(c.id) && (scope.kind !== 'favorites' || c.starred))
    return extra.length ? sortContacts([...local, ...extra], sortBy) : local
  }, [base, query, remote, hay, scopeKey, scope.kind, sortBy])

  const patchLocal = useCallback((id: number, patch: Partial<Summary> | null) => {
    const f = (xs: Summary[] | null) => xs && (patch ? xs.map(c => c.id === id ? { ...c, ...patch } : c) : xs.filter(c => c.id !== id))
    setAll(f)
    setScoped(s => { const n = f(s) as (Summary[] & { key?: string }) | null; if (n && s) n.key = (s as Summary[] & { key?: string }).key; return n })
    setRemote(r => r && { ...r, list: f(r.list) || [] })
  }, [])

  // ---------- the open contact ----------
  const [detail, setDetail] = useState<Contact | null>(null)
  const [detailErr, setDetailErr] = useState('')
  const [detailLoading, setDetailLoading] = useState(false)
  const [editor, setEditor] = useState<EditorMode | null>(null)
  const editorOpen = useRef(false)
  editorOpen.current = !!editor
  useEffect(() => {
    if (routeId == null) { setDetail(null); setDetailErr(''); return }
    let live = true
    setDetailErr('')
    setDetail(d => d && d.id === routeId ? d : null)
    setDetailLoading(true)
    api.get<Contact>(`${V1}/contacts/${routeId}`).then(c => live && setDetail(c)).catch(e => live && setDetailErr(e instanceof ApiError && e.status === 404 ? 'not found' : errMsg(e))).finally(() => live && setDetailLoading(false))
    return () => { live = false }
  }, [routeId])
  // a change elsewhere: refresh the open contact quietly (not while editing it)
  const firstVersion = useRef(version)
  useEffect(() => {
    if (version === firstVersion.current || routeId == null || editorOpen.current) return
    api.get<Contact>(`${V1}/contacts/${routeId}`).then(setDetail).catch(() => {})
  }, [version]) // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    const t = settings?.title || 'Pocket Contacts'
    document.title = detail && routeId === detail.id ? `${displayName(detail, settings?.display_order)} — ${t}` : t
  }, [detail, routeId, settings?.title, settings?.display_order])

  // ---------- actions ----------
  const open = useCallback((id: number, replace = false) => navigate(`/contacts/${id}`, { replace }), [navigate])
  const onOpen = useCallback((c: Summary) => { setChecked(new Set()); open(c.id, !mobile && page === 'contacts') }, [open, mobile, page])

  const onCheck = useCallback((c: Summary, mode: 'toggle' | 'range') => {
    setChecked(prev => {
      const next = new Set(prev)
      const list = items || []
      if (mode === 'range' && anchor.current != null) {
        const a = list.findIndex(x => x.id === anchor.current), b = list.findIndex(x => x.id === c.id)
        if (a >= 0 && b >= 0) { for (let i = Math.min(a, b); i <= Math.max(a, b); i++) next.add(list[i].id); return next }
      }
      if (next.has(c.id)) next.delete(c.id); else next.add(c.id)
      anchor.current = c.id
      return next
    })
  }, [items])

  const star = useCallback(async (c: Summary | Contact) => {
    const v = !c.starred
    patchLocal(c.id, { starred: v })
    setDetail(d => d && d.id === c.id ? { ...d, starred: v } : d)
    try { await api.patch(`${V1}/contacts/${c.id}`, { starred: v }) } catch (e) {
      patchLocal(c.id, { starred: !v }); setDetail(d => d && d.id === c.id ? { ...d, starred: !v } : d); toast(errMsg(e), 'err')
    }
  }, [patchLocal, toast])

  const restore = useCallback(async (uid: string) => {
    try {
      const del = (await api.get<Deleted[]>(`${V1}/deleted`)) || []
      const m = del.filter(d => d.uid === uid).sort((a, b) => b.deleted_at.localeCompare(a.deleted_at))[0]
      if (!m) { toast('It’s no longer in Recently deleted', 'err'); return }
      const c = await api.post<Contact>(`${V1}/deleted/${m.id}/restore`)
      bump(); reloadBooks().catch(() => {}); reloadCategories()
      toast(`${displayName(c)} is back`)
      if (c?.id) open(c.id)
    } catch (e) { toast(errMsg(e), 'err') }
  }, [bump, reloadBooks, reloadCategories, toast, open])

  const neighbour = (id: number) => {
    const list = items || []
    const i = list.findIndex(c => c.id === id)
    return i < 0 ? null : list[i + 1] || list[i - 1] || null
  }

  const doDelete = async (list: Summary[]) => {
    if (!list.length) return
    const ids = list.map(c => c.id)
    const next = routeId != null && ids.includes(routeId) ? neighbour(routeId) : null
    try {
      if (list.length === 1) await api.del(`${V1}/contacts/${ids[0]}`)
      else {
        const r = await api.post<{ done: number; failed: { id: number; error: string }[] | null }>(`${V1}/contacts/bulk`, { ids, action: 'delete' })
        if (r?.failed?.length) toast(`${r.failed.length} couldn’t be deleted: ${r.failed[0].error}`, 'err')
      }
      ids.forEach(id => patchLocal(id, null))
      setChecked(new Set())
      if (routeId != null && ids.includes(routeId)) { if (next && !mobile && !ids.includes(next.id)) open(next.id, true); else navigate('/', { replace: true }) }
      const one = list[0]
      if (list.length === 1) toast(`${displayName(one)} deleted`, 'ok', { label: 'Undo', run: () => restore(one.uid) })
      else toast(`${list.length} contacts deleted`, 'ok', { label: 'Recently deleted', run: () => navigate('/deleted') })
      bump(); reloadBooks().catch(() => {}); reloadCategories()
    } catch (e) { toast(errMsg(e), 'err') }
  }

  const bulk = async (action: string, extra: Record<string, unknown> = {}, ok?: string) => {
    const ids = [...checked]
    try {
      const r = await api.post<{ done: number; failed: { id: number; error: string }[] | null }>(`${V1}/contacts/bulk`, { ids, action, ...extra })
      if (r?.failed?.length) toast(`${r.done} done, ${r.failed.length} not: ${r.failed[0].error}`, 'err')
      else if (ok) toast(ok.replace('{n}', String(r?.done ?? ids.length)))
      bump(); reloadCategories(); if (action === 'move') reloadBooks().catch(() => {})
    } catch (e) { toast(errMsg(e), 'err') }
  }

  const [pending, setPending] = useState<Pending | null>(null)
  const [palette, setPalette] = useState(false)
  const [shortcuts, setShortcuts] = useState(!!(loc.state as { shortcuts?: boolean } | null)?.shortcuts)
  const searchRef = useRef<HTMLInputElement>(null)

  const newContact = useCallback((initial?: EditorMode) => {
    const bookId = scope.kind === 'book' ? scope.id : undefined
    const init = scope.kind === 'label' ? { categories: [scope.name] } : undefined
    setEditor(initial || { kind: 'new', bookId, initial: init })
  }, [scope])

  const onSaved = (c: Contact) => {
    setEditor(null)
    setDetail(c)
    if (routeId !== c.id) open(c.id)
    bump(); reloadCategories(); reloadBooks().catch(() => {})
  }

  const selectedSummaries = () => (items || []).filter(c => checked.has(c.id))

  // ---------- keyboard ----------
  const stateRef = useRef({ items, routeId, detail, checked, onList, editor, palette, books })
  stateRef.current = { items, routeId, detail, checked, onList, editor, palette, books }
  useEffect(() => {
    const h = (e: KeyboardEvent) => {
      const s = stateRef.current
      if (s.editor || s.palette) return
      const mod = e.metaKey || e.ctrlKey
      if (mod && e.key.toLowerCase() === 'k') { if (modalOpen()) return; e.preventDefault(); setPalette(true); return }
      if (modalOpen()) return
      const t = e.target as HTMLElement
      if (t.closest?.('input, textarea, select, [contenteditable="true"]')) return
      if (mod && e.key.toLowerCase() === 'a' && s.onList && s.items?.length) { e.preventDefault(); setChecked(new Set(s.items.map(c => c.id))); return }
      if (mod || e.altKey) return
      const list = s.items || []
      const move = (d: 1 | -1) => {
        if (!list.length) return
        const i = s.routeId != null ? list.findIndex(c => c.id === s.routeId) : -1
        const n = list[Math.max(0, Math.min(list.length - 1, i < 0 ? 0 : i + d))]
        if (n) { if (page !== '' && page !== 'contacts') return; open(n.id, s.routeId != null) }
      }
      switch (e.key) {
        case '/': e.preventDefault(); if (!s.onList) navigate('/'); setTimeout(() => searchRef.current?.focus(), 0); break
        case 'n': e.preventDefault(); newContact(); break
        case 'e': if (s.detail && contactWritable(s.detail, s.books)) { e.preventDefault(); setEditor({ kind: 'edit', contact: s.detail }) } break
        case 's': if (s.detail) { e.preventDefault(); star(s.detail) } break
        case 'j': case 'ArrowDown': if (s.onList) { e.preventDefault(); move(1) } break
        case 'k': case 'ArrowUp': if (s.onList) { e.preventDefault(); move(-1) } break
        case 'Backspace': case 'Delete':
          if (s.checked.size) { e.preventDefault(); setPending({ kind: 'delete', items: list.filter(c => s.checked.has(c.id)) }) }
          else if (s.detail && contactWritable(s.detail, s.books)) { e.preventDefault(); setPending({ kind: 'delete', items: [s.detail] }) }
          break
        case '?': e.preventDefault(); setShortcuts(true); break
        case 'Escape': if (s.checked.size) setChecked(new Set()); break
      }
    }
    window.addEventListener('keydown', h)
    return () => window.removeEventListener('keydown', h)
  }, [navigate, newContact, open, star, page])

  // ---------- palette commands ----------
  const commands = useMemo<PaletteCommand[]>(() => [
    { id: 'new', label: 'New contact', icon: 'user-plus', hint: 'N', run: () => newContact() },
    { id: 'import', label: 'Import contacts', icon: 'upload', keywords: 'vcf csv zip google outlook', run: () => navigate('/import') },
    { id: 'birthdays', label: 'Birthdays', icon: 'cake', keywords: 'anniversary', run: () => navigate('/birthdays') },
    { id: 'dupes', label: 'Find duplicates', icon: 'merge', keywords: 'merge', run: () => navigate('/duplicates') },
    { id: 'deleted', label: 'Recently deleted', icon: 'trash', keywords: 'restore undo', run: () => navigate('/deleted') },
    { id: 'all', label: 'All contacts', icon: 'contact', run: () => { setScope({ kind: 'all' }); navigate('/') } },
    { id: 'fav', label: 'Favorites', icon: 'star', run: () => { setScope({ kind: 'favorites' }); navigate('/') } },
    { id: 'phone', label: 'Set up a phone', icon: 'smartphone', keywords: 'iphone android carddav sync qr', run: () => navigate('/settings/devices') },
    { id: 'accounts', label: 'Sync with Nextcloud, iCloud…', icon: 'refresh', keywords: 'accounts carddav', run: () => navigate('/settings/accounts') },
    { id: 'export', label: 'Export all as .vcf', icon: 'download', run: () => download(`${V1}/export.vcf`) },
    { id: 'theme', label: theme === 'dark' ? 'Light theme' : 'Dark theme', icon: theme === 'dark' ? 'sun' : 'moon', keywords: 'dark light mode', run: () => setThemeMode(theme === 'dark' ? 'light' : 'dark') },
    { id: 'settings', label: 'Settings', icon: 'settings', run: () => navigate('/settings') },
    { id: 'keys', label: 'Keyboard shortcuts', icon: 'keyboard', hint: '?', run: () => setShortcuts(true) },
  ], [newContact, navigate, setScope, theme, setThemeMode]) // eslint-disable-line react-hooks/exhaustive-deps

  // ---------- layout ----------
  const toggleDrawer = () => { openedAt.current = performance.now(); setDrawerOpen(o => !o) }
  const title = scopeTitle(scope, books)
  const total = query.trim() ? null : base ? base.length : null
  const sidebarCounts: Counts = { all: all ? all.length : null, favorites: all ? all.filter(c => c.starred).length : 0, ...counts }

  const emptyList = query.trim() ? (
    <div className="empty"><div className="empty-ico"><Icon name="search" size={22} /></div><h3>Nobody called “{query.trim()}”</h3><div className="muted">Try part of a name, an email or a few digits of a number.</div>
      <button type="button" className="btn sm mt-s" onClick={() => setEditor(guessFromQuery(query))}><Icon name="user-plus" size={14} />Add “{query.trim()}”</button></div>
  ) : scope.kind === 'favorites' ? (
    <div className="empty"><div className="empty-ico"><Icon name="star" size={22} /></div><h3>No favorites yet</h3><div className="muted">Star the people you reach for most — they show up here and first in other apps.</div></div>
  ) : scope.kind === 'recent' ? (
    <div className="empty"><div className="empty-ico"><Icon name="history" size={22} /></div><h3>Nothing opened lately</h3><div className="muted">The people you look at show up here.</div></div>
  ) : scope.kind === 'all' ? (
    <div className="empty"><div className="empty-ico"><Icon name="contact" size={22} /></div><h3>Your address book is empty</h3><div className="muted">Add someone, bring in a .vcf or CSV, or sync from Nextcloud or iCloud.</div>
      <div className="row mt-s"><button type="button" className="btn primary sm" onClick={() => newContact()}><Icon name="user-plus" size={14} />New contact</button><button type="button" className="btn sm" onClick={() => navigate('/import')}><Icon name="upload" size={14} />Import</button></div></div>
  ) : (
    <div className="empty"><div className="empty-ico"><Icon name={scope.kind === 'book' ? 'book' : 'tag'} size={22} /></div><h3>Nobody here yet</h3>
      <button type="button" className="btn sm mt-s" onClick={() => newContact()}><Icon name="user-plus" size={14} />Add someone</button></div>
  )

  const selected = selectedSummaries()
  const writableBooksOpts = bookOptions(books)
  const labelItems = (): MenuItem[] => {
    const inSel = new Set(selected.flatMap(c => c.categories || []))
    return [
      { heading: 'Add a label' },
      ...(categories.labels || []).slice(0, 25).map(l => ({ label: l.name, icon: 'tag', onSelect: () => bulk('add_category', { category: l.name }, `Label “${l.name}” added to {n}`) })),
      { label: 'New label…', icon: 'plus', onSelect: () => setPending({ kind: 'newLabel', items: selected }) },
      ...(inSel.size ? ['sep' as const, { heading: 'Remove a label' }, ...[...inSel].map(l => ({ label: l, icon: 'minus', onSelect: () => bulk('remove_category', { category: l }, `Label “${l}” removed from {n}`) }))] : []),
    ]
  }
  const bulkBar = checked.size > 0 ? (
    <div className="bulk-bar" role="toolbar" aria-label="Selected contacts">
      <button type="button" className="btn ghost icon-only sm" aria-label="Clear selection" title="Clear selection (Esc)" onClick={() => setChecked(new Set())}><Icon name="x" size={15} /></button>
      <span className="bulk-n tnum">{checked.size} selected</span>
      <span className="spacer" />
      <button type="button" className="btn ghost icon-only sm" title="Add to favorites" aria-label="Add to favorites" onClick={() => bulk(selected.every(c => c.starred) ? 'unstar' : 'star', {}, selected.every(c => c.starred) ? 'Removed {n} from favorites' : 'Starred {n}')}><Icon name="star" size={15} /></button>
      <MenuButton label="Labels" className="btn ghost icon-only sm" items={labelItems} width={220}><Icon name="tag" size={15} /></MenuButton>
      <button type="button" className="btn ghost icon-only sm" title="Move to address book" aria-label="Move to address book" onClick={() => setPending({ kind: 'move', items: selected })}><Icon name="book" size={15} /></button>
      <button type="button" className="btn ghost icon-only sm" title="Export as .vcf" aria-label="Export as .vcf" onClick={() => download(`${V1}/export.vcf?ids=${[...checked].join(',')}`)}><Icon name="download" size={15} /></button>
      <button type="button" className="btn ghost icon-only sm danger-ghost" title="Delete" aria-label="Delete" onClick={() => setPending({ kind: 'delete', items: selected })}><Icon name="trash" size={15} /></button>
    </div>
  ) : null

  const list = settings && (
    <ErrorBoundary where="list" key="list">
      <ContactList title={title} items={items} total={total} selectedId={routeId} checked={checked} settings={settings} query={query} onQuery={q => { setQuery(q) }}
        searching={searching} searchRef={searchRef} onOpen={onOpen} onCheck={onCheck} onStar={star} onMenu={drawer ? toggleDrawer : undefined}
        bulkBar={bulkBar} empty={emptyList} grouped={scope.kind !== 'recent'}
        headerExtra={!mobile && checked.size === 0 && items && items.length > 0 ? <button type="button" className="btn ghost icon-only sm" title="Select" aria-label="Select contacts" onClick={() => setChecked(new Set([items[0].id]))}><Icon name="check-square" size={15} /></button> : undefined} />
    </ErrorBoundary>
  )
  const detailPane = (
    <ErrorBoundary where="detail" key="detail">
      {routeId == null ? (
        <section className="detail-pane"><div className="detail-placeholder">
          <div className="empty-ico big"><Icon name="contact" size={30} /></div>
          <h3>{all && all.length ? 'Pick someone from the list' : 'Nobody here yet'}</h3>
          <p className="muted">{all && all.length ? <>Press <kbd>/</kbd> to search, <kbd>j</kbd> <kbd>k</kbd> to move, <kbd>n</kbd> for a new contact.</> : 'Add your first contact or import the ones you already have.'}</p>
        </div></section>
      ) : (
        <ContactDetail contact={detail && detail.id === routeId ? detail : null} loading={detailLoading} error={detailErr} all={all} mobile={mobile}
          onBack={() => navigate('/')} onEdit={c => setEditor({ kind: 'edit', contact: c })} onDelete={c => setPending({ kind: 'delete', items: [c] })}
          onStar={star} onMove={c => setPending({ kind: 'move', items: [c] })} onChanged={c => { setDetail(c); patchLocal(c.id, { has_photo: c.has_photo, photo_url: c.photo_url }) }}
          onScope={s => { setScope(s); navigate('/') }} onOpen={id => open(id)} />
      )}
    </ErrorBoundary>
  )

  let main: React.ReactNode
  if (!['', 'contacts', 'birthdays', 'duplicates', 'deleted', 'import'].includes(page) || (page === 'contacts' && routeId == null)) {
    main = <Navigate to="/" replace />
  } else if (!onList) {
    const props = { onMenu: drawer ? toggleDrawer : undefined, onOpen: (id: number) => open(id) }
    main = (
      <ErrorBoundary where={page} key={page}>
        {page === 'birthdays' && <BirthdaysPage {...props} />}
        {page === 'duplicates' && <DuplicatesPage {...props} />}
        {page === 'deleted' && <DeletedPage {...props} />}
        {page === 'import' && <ImportPage {...props} />}
      </ErrorBoundary>
    )
  } else if (mobile) {
    main = routeId == null ? list : detailPane
  } else {
    main = <div className="panes">{list}{detailPane}</div>
  }

  return (
    <div className={`app ${drawer ? 'drawer-mode' : ''} ${mobile ? 'is-mobile' : ''}`}>
      {drawer && drawerOpen && <div className="drawer-backdrop" onClick={e => { if (e.timeStamp - openedAt.current > 350) setDrawerOpen(false) }} />}
      <div className={`sidebar-wrap ${drawerOpen ? 'open' : ''}`}>
        <ErrorBoundary where="sidebar" key="sidebar">
          <Sidebar scope={scope} onScope={setScope} counts={sidebarCounts} onNew={() => newContact()} onNavigate={() => drawer && setDrawerOpen(false)} />
        </ErrorBoundary>
      </div>
      <main className="main">{main}</main>
      {mobile && onList && routeId == null && checked.size === 0 && <button type="button" className="fab" aria-label="New contact" onClick={() => newContact()}><Icon name="plus" size={26} strokeWidth={2.2} /></button>}

      {editor && <ContactEditor key={editor.kind === 'edit' ? editor.contact.id : 'new'} mode={editor} onClose={() => setEditor(null)} onSaved={onSaved} />}
      {palette && <CommandPalette onClose={() => setPalette(false)} commands={commands} all={all} openContact={c => open(c.id)} />}
      {shortcuts && <ShortcutsSheet onClose={() => setShortcuts(false)} />}
      {pending?.kind === 'delete' && (
        <Confirm title={pending.items.length === 1 ? `Delete ${displayName(pending.items[0])}?` : `Delete ${pending.items.length} contacts?`}
          message={<>{pending.items.length === 1 ? 'It' : 'They'} also disappear from every phone that syncs {pending.items.length === 1 ? 'it' : 'them'}. You can bring {pending.items.length === 1 ? 'it' : 'them'} back from Recently deleted for 30 days.</>}
          onCancel={() => setPending(null)} onConfirm={() => { const p = pending; setPending(null); doDelete(p.items) }} />
      )}
      {pending?.kind === 'move' && <MoveDialog items={pending.items} options={writableBooksOpts} onClose={() => setPending(null)} onMove={async id => {
        const p = pending; setPending(null)
        if (p.items.length === 1 && checked.size === 0) {
          try { const c = await api.patch<Contact>(`${V1}/contacts/${p.items[0].id}`, { addressbook_id: id }); if (c?.id) setDetail(d => d && d.id === c.id ? c : d); toast(`Moved to ${books.find(b => b.id === id)?.name}`); bump(); reloadBooks().catch(() => {}) } catch (e) { toast(errMsg(e), 'err') }
        } else bulk('move', { addressbook_id: id }, `Moved {n} to ${books.find(b => b.id === id)?.name}`)
      }} />}
      {pending?.kind === 'newLabel' && <NewLabelDialog onClose={() => setPending(null)} onSave={name => { setPending(null); bulk('add_category', { category: name }, `Label “${name}” added to {n}`) }} />}
    </div>
  )
}

/** "marko@x.com" → an email, "+387…" → a phone, else a name. */
function guessFromQuery(q: string): EditorMode {
  const t = q.trim()
  if (/^\S+@\S+$/.test(t)) return { kind: 'new', initial: { emails: [{ label: 'home', value: t }] } }
  if (/^[+\d][\d\s()/-]{4,}$/.test(t)) return { kind: 'new', initial: { phones: [{ label: 'mobile', value: t }] } }
  const parts = t.split(/\s+/)
  return { kind: 'new', initial: { given: parts[0] || '', family: parts.slice(1).join(' ') } }
}

function MoveDialog({ items, options, onClose, onMove }: { items: Summary[]; options: ReturnType<typeof bookOptions>; onClose: () => void; onMove: (id: number) => void }) {
  const current = items.length === 1 ? items[0].addressbook_id : 0
  const opts = options.filter(o => o.value !== current)
  const [id, setId] = useState<number>(opts[0]?.value ?? 0)
  return (
    <Modal title={items.length === 1 ? `Move ${displayName(items[0])}` : `Move ${items.length} contacts`} size="sm" onClose={onClose} footer={<>
      <button type="button" className="btn" onClick={onClose}>Cancel</button>
      <button type="button" className="btn primary" disabled={!id} onClick={() => onMove(id)}>Move</button>
    </>}>
      {opts.length ? <Field label="To the address book" group><Select value={id} options={opts} onChange={setId} label="Address book" /></Field>
        : <p className="muted">There’s no other address book you may add contacts to.</p>}
    </Modal>
  )
}

function NewLabelDialog({ onClose, onSave }: { onClose: () => void; onSave: (name: string) => void }) {
  const [name, setName] = useState('')
  return (
    <Modal title="New label" size="sm" onClose={onClose} footer={<>
      <button type="button" className="btn" onClick={onClose}>Cancel</button>
      <button type="button" className="btn primary" disabled={!name.trim()} onClick={() => onSave(name.trim())}>Add label</button>
    </>}>
      <form onSubmit={e => { e.preventDefault(); if (name.trim()) onSave(name.trim()) }}>
        <Field label="Label" help="Labels are groups that Android, Nextcloud and Google understand."><input value={name} onChange={e => setName(e.target.value)} autoFocus placeholder="e.g. Family, Work, Football" /></Field>
      </form>
    </Modal>
  )
}
