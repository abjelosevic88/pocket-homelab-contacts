// ⌘K — find a contact or run a command from the keyboard.
import React, { useEffect, useMemo, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { api, qs, V1 } from '../lib/api'
import { haystack, matches, secondaryLine } from '../lib/contacts'
import type { Summary } from '../lib/types'
import { ContactAvatar } from './ContactAvatar'
import { Icon } from './icons'
import { useDebounce } from './ui'

export interface PaletteCommand { id: string; label: string; icon: string; hint?: string; keywords?: string; run: () => void }

export function CommandPalette({ onClose, commands, all, openContact }: { onClose: () => void; commands: PaletteCommand[]; all: Summary[] | null; openContact: (c: Summary) => void }) {
  const [q, setQ] = useState('')
  const [hi, setHi] = useState(0)
  const [remote, setRemote] = useState<Summary[] | null>(null)
  const [searching, setSearching] = useState(false)
  const dq = useDebounce(q, 160)
  const list = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (dq.trim().length < 2) { setRemote(null); return }
    let live = true
    setSearching(true)
    api.get<{ contacts: Summary[] | null }>(`${V1}/search${qs({ q: dq.trim(), limit: 12 })}`)
      .then(r => live && setRemote(r?.contacts || [])).catch(() => live && setRemote(null)).finally(() => live && setSearching(false))
    return () => { live = false }
  }, [dq])

  type Row = { key: string; group: string; icon: React.ReactNode; label: React.ReactNode; sub?: React.ReactNode; hint?: string; run: () => void }
  const rows = useMemo<Row[]>(() => {
    const out: Row[] = []
    const n = q.trim()
    if (n) {
      // the server's ranking first (name prefix > other), then local matches it missed
      const seen = new Set<number>()
      const people: Summary[] = []
      for (const c of remote || []) { if (!seen.has(c.id)) { seen.add(c.id); people.push(c) } }
      if (all) for (const c of all) { if (people.length >= 12) break; if (!seen.has(c.id) && matches(c, n, haystack(c))) { seen.add(c.id); people.push(c) } }
      for (const c of people.slice(0, 12)) {
        out.push({ key: 'c' + c.id, group: 'Contacts', icon: <ContactAvatar c={c} size={26} />, label: c.fn || c.org || c.emails?.[0] || 'No name', sub: secondaryLine(c), run: () => openContact(c) })
      }
    }
    for (const c of commands) {
      if (n && !`${c.label} ${c.keywords || ''}`.toLowerCase().includes(n.toLowerCase())) continue
      out.push({ key: c.id, group: 'Commands', icon: <Icon name={c.icon} size={16} />, label: c.label, hint: c.hint, run: c.run })
    }
    return out
  }, [q, remote, all, commands, openContact])
  useEffect(() => setHi(0), [q, remote])
  useEffect(() => { list.current?.querySelector(`[data-i="${hi}"]`)?.scrollIntoView({ block: 'nearest' }) }, [hi])

  const run = (r: Row | undefined) => { if (!r) return; onClose(); r.run() }
  let last = ''
  return createPortal(
    <div className="palette-backdrop" onMouseDown={e => { if (e.target === e.currentTarget) onClose() }}>
      <div className="palette-box" role="dialog" aria-label="Search and commands">
        <div className="pal-input">
          <Icon name="search" size={18} />
          <input autoFocus value={q} onChange={e => setQ(e.target.value)} placeholder="Find a person, or run a command…" aria-label="Search"
            onKeyDown={e => {
              if (e.key === 'ArrowDown') { e.preventDefault(); setHi(h => Math.min(rows.length - 1, h + 1)) }
              else if (e.key === 'ArrowUp') { e.preventDefault(); setHi(h => Math.max(0, h - 1)) }
              else if (e.key === 'Enter') { e.preventDefault(); run(rows[hi]) }
              else if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); onClose() }
            }} />
          {searching && <span className="spinner" />}
          <kbd>esc</kbd>
        </div>
        <div className="pal-list" ref={list} role="listbox">
          {rows.length === 0 && <div className="pal-empty">{q.trim() && !searching ? 'Nobody matches.' : 'Type to search.'}</div>}
          {rows.map((r, i) => {
            const head = r.group !== last ? <div className="pal-group" key={'g' + r.group}>{r.group}</div> : null
            last = r.group
            return (
              <React.Fragment key={r.key}>
                {head}
                <div data-i={i} role="option" aria-selected={i === hi} className={`pal-row ${i === hi ? 'hi' : ''}`} onMouseEnter={() => setHi(i)} onClick={() => run(r)}>
                  <span className="pal-ico">{r.icon}</span>
                  <span className="grow"><span className="pal-label">{r.label}</span>{r.sub && <span className="pal-sub">{r.sub}</span>}</span>
                  {r.hint && <kbd>{r.hint}</kbd>}
                  {i === hi && <Icon name="chevron-right" size={14} className="muted" />}
                </div>
              </React.Fragment>
            )
          })}
        </div>
      </div>
    </div>, document.body)
}
