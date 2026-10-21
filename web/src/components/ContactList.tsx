// The contact list: search, A–Z sections with a letter rail, multi-select. Windowed so a few thousand rows stay smooth.
import React, { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import { letterOf, nameParts, secondaryLine, sortKey } from '../lib/contacts'
import type { Settings, Summary } from '../lib/types'
import { ContactAvatar } from './ContactAvatar'
import { Icon } from './icons'

type Row = { kind: 'h'; letter: string; top: number } | { kind: 'c'; c: Summary; idx: number; top: number }

interface Props {
  title: string
  items: Summary[] | null
  total: number | null
  selectedId: number | null
  checked: Set<number>
  settings: Settings
  query: string
  onQuery: (q: string) => void
  searching: boolean
  searchRef: React.RefObject<HTMLInputElement | null>
  onOpen: (c: Summary) => void
  onCheck: (c: Summary, mode: 'toggle' | 'range') => void
  onStar: (c: Summary) => void
  onMenu?: () => void
  bulkBar?: React.ReactNode
  empty: React.ReactNode
  headerExtra?: React.ReactNode
  grouped?: boolean
}

const HEADER_H = 28

export function ContactList({ title, items, total, selectedId, checked, settings, query, onQuery, searching, searchRef, onOpen, onCheck, onStar, onMenu, bulkBar, empty, headerExtra, grouped: groupedProp = true }: Props) {
  const scroller = useRef<HTMLDivElement>(null)
  const [scrollTop, setScrollTop] = useState(0)
  const [height, setHeight] = useState(600)
  const compact = settings.density === 'compact'
  const rowH = compact ? 48 : 58
  const grouped = groupedProp && !query.trim()

  const { rows, total: totalH, letters } = useMemo(() => {
    const rows: Row[] = []
    const letters: { letter: string; top: number }[] = []
    let top = 0
    let last = ''
    ;(items || []).forEach((c, idx) => {
      if (grouped) {
        const l = letterOf(sortKey(c, settings.sort_by))
        if (l !== last) {
          rows.push({ kind: 'h', letter: l, top })
          letters.push({ letter: l, top })
          top += HEADER_H
          last = l
        }
      }
      rows.push({ kind: 'c', c, idx, top })
      top += rowH
    })
    return { rows, total: top, letters }
  }, [items, grouped, settings.sort_by, rowH])

  useLayoutEffect(() => {
    const el = scroller.current
    if (!el) return
    const ro = new ResizeObserver(() => setHeight(el.clientHeight))
    ro.observe(el)
    setHeight(el.clientHeight)
    return () => ro.disconnect()
  }, [])
  const raf = useRef(0)
  const onScroll = () => {
    cancelAnimationFrame(raf.current)
    raf.current = requestAnimationFrame(() => setScrollTop(scroller.current?.scrollTop || 0))
  }
  // a new scope or query starts at the top
  useEffect(() => { scroller.current?.scrollTo({ top: 0 }); setScrollTop(0) }, [title, query])

  // keep the selected contact in view (j/k, opening from the palette)
  useEffect(() => {
    if (selectedId == null || !scroller.current) return
    const r = rows.find(x => x.kind === 'c' && x.c.id === selectedId)
    if (!r) return
    const el = scroller.current
    const top = r.top - (grouped ? HEADER_H : 0)
    if (top < el.scrollTop) el.scrollTop = top
    else if (r.top + rowH > el.scrollTop + el.clientHeight) el.scrollTop = r.top + rowH - el.clientHeight
  }, [selectedId]) // eslint-disable-line react-hooks/exhaustive-deps

  // binary search the first row on screen
  const first = useMemo(() => {
    let lo = 0, hi = rows.length - 1, ans = 0
    const target = scrollTop - 200
    while (lo <= hi) {
      const mid = (lo + hi) >> 1
      if (rows[mid].top <= target) { ans = mid; lo = mid + 1 } else hi = mid - 1
    }
    return ans
  }, [rows, scrollTop])
  const visible: Row[] = []
  for (let i = first; i < rows.length && rows[i].top < scrollTop + height + 200; i++) visible.push(rows[i])
  const current = grouped ? [...letters].reverse().find(l => l.top <= scrollTop) : undefined

  const jump = useCallback((letter: string) => {
    const el = scroller.current
    if (!el) return
    const exact = letters.find(l => l.letter === letter)
    const after = exact || letters.find(l => l.letter > letter && l.letter !== '#') || letters[letters.length - 1]
    if (after) el.scrollTop = after.top
  }, [letters])

  const anyChecked = checked.size > 0
  const count = items?.length ?? 0

  return (
    <section className={`list-pane ${compact ? 'compact' : ''}`} aria-label="Contacts">
      <header className="list-head">
        <div className="list-title-row">
          {onMenu && <button type="button" className="btn ghost icon-only list-menu" aria-label="Menu" onClick={onMenu}><Icon name="menu" size={19} /></button>}
          <h1 className="list-title ellipsis">{title}</h1>
          <span className="list-count tnum">{query.trim() ? `${count} found` : total != null ? total : ''}</span>
          <span className="spacer" />
          {headerExtra}
        </div>
        <div className="search">
          <Icon name="search" size={15} className="search-ico" />
          <input ref={searchRef} type="search" value={query} onChange={e => onQuery(e.target.value)} placeholder="Search names, emails, phones…" aria-label="Search contacts" autoComplete="off" spellCheck={false}
            onKeyDown={e => {
              if (e.key === 'Escape') { if (query) { e.stopPropagation(); onQuery('') } else (e.target as HTMLInputElement).blur() }
              else if (e.key === 'ArrowDown' || e.key === 'Enter') { const c = items?.[0]; if (c) { e.preventDefault(); onOpen(c); (e.target as HTMLInputElement).blur() } }
            }} />
          {searching ? <span className="spinner" /> : query ? <button type="button" className="search-x" aria-label="Clear search" onClick={() => { onQuery(''); searchRef.current?.focus() }}><Icon name="x" size={13} /></button> : <kbd className="search-kbd">/</kbd>}
        </div>
      </header>
      {bulkBar}
      <div className="list-body">
        <div className="list-scroll" ref={scroller} onScroll={onScroll} role="listbox" aria-label={title} aria-multiselectable="true" tabIndex={-1}>
          {items === null ? <ListSkeleton rowH={rowH} /> : count === 0 ? empty : (
            <div className="list-space" style={{ height: totalH }}>
              {visible.map(r => r.kind === 'h'
                ? <div key={'h' + r.letter} className="list-letter" style={{ top: r.top, height: HEADER_H }}>{r.letter}</div>
                : <ContactRow key={r.c.id} c={r.c} top={r.top} h={rowH} settings={settings} selected={r.c.id === selectedId} checked={checked.has(r.c.id)} showCheck={anyChecked}
                  onOpen={onOpen} onCheck={onCheck} onStar={onStar} />)}
            </div>
          )}
          {current && <div className="list-letter sticky" aria-hidden="true">{current.letter}</div>}
        </div>
        {grouped && letters.length > 4 && <LetterRail letters={letters.map(l => l.letter)} onJump={jump} />}
      </div>
    </section>
  )
}

const ContactRow = React.memo(function ContactRow({ c, top, h, settings, selected, checked, showCheck, onOpen, onCheck, onStar }: {
  c: Summary; top: number; h: number; settings: Settings; selected: boolean; checked: boolean; showCheck: boolean
  onOpen: (c: Summary) => void; onCheck: (c: Summary, mode: 'toggle' | 'range') => void; onStar: (c: Summary) => void
}) {
  const n = nameParts(c, settings)
  const sub = secondaryLine(c)
  return (
    <div role="option" aria-selected={selected} className={`crow ${selected ? 'sel' : ''} ${checked ? 'checked' : ''} ${showCheck ? 'show-check' : ''}`} style={{ top, height: h }}
      onClick={e => {
        if (e.shiftKey) { e.preventDefault(); onCheck(c, 'range') }
        else if (e.metaKey || e.ctrlKey) onCheck(c, 'toggle')
        else onOpen(c)
      }}
      onMouseDown={e => { if (e.shiftKey) e.preventDefault() }}>
      <button type="button" className="crow-check" role="checkbox" aria-checked={checked} aria-label={`Select ${c.fn}`} tabIndex={-1}
        onClick={e => { e.stopPropagation(); onCheck(c, e.shiftKey ? 'range' : 'toggle') }}>
        {checked && <Icon name="check" size={12} strokeWidth={3} />}
      </button>
      <ContactAvatar c={c} size={h > 50 ? 38 : 32} className="crow-avatar" />
      <div className="crow-text">
        <div className="crow-name ellipsis">{n.pre}<b>{n.bold}</b>{n.post}</div>
        {sub && <div className="crow-sub ellipsis">{sub}</div>}
      </div>
      <button type="button" className={`crow-star ${c.starred ? 'on' : ''}`} aria-label={c.starred ? 'Remove from favorites' : 'Add to favorites'} aria-pressed={c.starred} tabIndex={-1}
        onClick={e => { e.stopPropagation(); onStar(c) }}>
        <Icon name="star" size={15} fill={c.starred} />
      </button>
    </div>
  )
})

function LetterRail({ letters, onJump }: { letters: string[]; onJump: (l: string) => void }) {
  const all = [...'ABCDEFGHIJKLMNOPQRSTUVWXYZ', '#']
  const has = new Set(letters)
  const down = useRef(false)
  const at = (e: React.PointerEvent) => {
    const el = document.elementFromPoint(e.clientX, e.clientY) as HTMLElement | null
    const l = el?.dataset?.letter
    if (l) onJump(l)
  }
  return (
    <nav className="rail" aria-label="Jump to letter"
      onPointerDown={e => { down.current = true; (e.currentTarget as HTMLElement).setPointerCapture(e.pointerId); at(e) }}
      onPointerMove={e => { if (down.current) at(e) }}
      onPointerUp={() => { down.current = false }} onPointerCancel={() => { down.current = false }}>
      {all.map(l => <button type="button" key={l} data-letter={l} className={has.has(l) ? '' : 'dim'} tabIndex={-1} aria-label={`Jump to ${l}`} onClick={() => onJump(l)}>{l}</button>)}
    </nav>
  )
}

function ListSkeleton({ rowH }: { rowH: number }) {
  return (
    <div className="list-skel" aria-busy="true">
      {Array.from({ length: 12 }, (_, i) => (
        <div key={i} className="crow skel-row" style={{ height: rowH }}>
          <span className="skeleton" style={{ width: 36, height: 36, borderRadius: '50%' }} />
          <div className="crow-text"><span className="skeleton" style={{ width: `${45 + ((i * 37) % 40)}%`, height: 12 }} /><span className="skeleton" style={{ width: `${30 + ((i * 23) % 30)}%`, height: 10, marginTop: 7 }} /></div>
        </div>
      ))}
    </div>
  )
}
