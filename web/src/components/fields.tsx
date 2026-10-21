// Editor building blocks: a label picker with own words, a date with an optional year, a chip input.
import React, { useEffect, useMemo, useRef, useState } from 'react'
import { labelText, MONTHS, parseDate, toDateString } from '../lib/contacts'
import { Icon } from './icons'
import { createPortal } from 'react-dom'
import { type Option, Select } from './Select'

const CUSTOM = '\u0000custom'

/** Pick one of the known labels, or type your own ("Custom…"). */
export function LabelSelect({ value, labels, onChange, label = 'Label' }: { value: string; labels: string[]; onChange: (v: string) => void; label?: string }) {
  const [typing, setTyping] = useState(false)
  const [text, setText] = useState('')
  const known = labels.includes(value.toLowerCase())
  const options: Option<string>[] = [
    ...labels.map(l => ({ value: l, label: labelText(l) })),
    ...(value && !known ? [{ value, label: value, description: 'Your own label' }] : []),
    { value: CUSTOM, label: 'Custom…', icon: <Icon name="edit" size={13} /> },
  ]
  if (typing) {
    const commit = () => { setTyping(false); const t = text.trim(); if (t) onChange(labels.includes(t.toLowerCase()) ? t.toLowerCase() : t) }
    return <input className="label-input" value={text} autoFocus onChange={e => setText(e.target.value)} placeholder="Own label" aria-label={label}
      onBlur={commit} onKeyDown={e => { if (e.key === 'Enter') { e.preventDefault(); commit() } else if (e.key === 'Escape') { e.stopPropagation(); setTyping(false) } }} />
  }
  return <Select className="label-select" size="sm" value={known ? value.toLowerCase() : value} options={options} label={label} width={200}
    onChange={v => { if (v === CUSTOM) { setText(known ? '' : value); setTyping(true) } else onChange(v) }} />
}

/** Day, month and an optional year → "YYYY-MM-DD", "--MM-DD" or "" (cleared). */
export function DateField({ value, onChange, label }: { value: string; onChange: (v: string) => void; label: string }) {
  const p = parseDate(value)
  const [day, setDay] = useState(p ? String(p.d) : '')
  const [month, setMonth] = useState(p ? p.m : 0)
  const [year, setYear] = useState(p?.y ? String(p.y) : '')
  const last = useRef(value)
  useEffect(() => {
    if (value === last.current) return
    const q = parseDate(value)
    setDay(q ? String(q.d) : ''); setMonth(q ? q.m : 0); setYear(q?.y ? String(q.y) : '')
    last.current = value
  }, [value])
  const emit = (d: string, m: number, y: string) => {
    const dn = parseInt(d, 10), yn = parseInt(y, 10)
    let v = ''
    if (dn >= 1 && dn <= 31 && m >= 1) v = toDateString({ d: dn, m, y: y.length === 4 && yn > 0 ? yn : null })
    last.current = v
    onChange(v)
  }
  const months: Option<number>[] = MONTHS.map((n, i) => ({ value: i + 1, label: n }))
  return (
    <div className="date-field" role="group" aria-label={label}>
      <input className="date-day" inputMode="numeric" value={day} placeholder="Day" aria-label={`${label} day`} maxLength={2}
        onChange={e => { const d = e.target.value.replace(/\D/g, ''); setDay(d); emit(d, month, year) }} />
      <Select className="date-month" value={month} options={months} onChange={m => { setMonth(m); emit(day, m, year) }} placeholder="Month" label={`${label} month`} width={180} />
      <input className="date-year" inputMode="numeric" value={year} placeholder="Year (optional)" aria-label={`${label} year`} maxLength={4}
        onChange={e => { const y = e.target.value.replace(/\D/g, ''); setYear(y); emit(day, month, y) }} />
    </div>
  )
}

/** Labels as chips; type to add, with suggestions from the labels in use. */
export function ChipInput({ value, onChange, suggestions, placeholder = 'Add a label' }: { value: string[]; onChange: (v: string[]) => void; suggestions: string[]; placeholder?: string }) {
  const [text, setText] = useState('')
  const [open, setOpen] = useState(false)
  const [hi, setHi] = useState(0)
  const input = useRef<HTMLInputElement>(null)
  const box = useRef<HTMLDivElement>(null)
  const add = (t: string) => {
    const v = t.trim()
    if (v && !value.some(x => x.toLowerCase() === v.toLowerCase())) onChange([...value, v])
    setText('')
  }
  const options = useMemo(() => {
    const n = text.trim().toLowerCase()
    const list = suggestions.filter(s => !value.includes(s) && (!n || s.toLowerCase().includes(n))).slice(0, 12).map(s => ({ value: s, label: s, add: false }))
    if (n && !suggestions.some(s => s.toLowerCase() === n)) list.unshift({ value: text.trim(), label: `Add “${text.trim()}”`, add: true })
    return list
  }, [text, suggestions, value])
  useEffect(() => setHi(0), [text])
  const show = open && options.length > 0 && !!box.current
  const r = show ? box.current!.getBoundingClientRect() : null
  return (
    <div ref={box} className="chip-input" onClick={() => input.current?.focus()}>
      {value.map(v => (
        <span key={v} className="chip sm on">{v}<button type="button" className="chip-x" aria-label={`Remove ${v}`} onClick={e => { e.stopPropagation(); onChange(value.filter(x => x !== v)) }}><Icon name="x" size={11} /></button></span>
      ))}
      <input ref={input} value={text} placeholder={value.length ? '' : placeholder} aria-label={placeholder} role="combobox" aria-expanded={show} aria-autocomplete="list"
        onChange={e => { setText(e.target.value); setOpen(true) }} onFocus={() => setOpen(true)} onBlur={() => { setOpen(false); if (text.trim()) add(text) }}
        onKeyDown={e => {
          if (e.key === 'ArrowDown') { e.preventDefault(); setOpen(true); setHi(h => Math.min(options.length - 1, h + 1)) }
          else if (e.key === 'ArrowUp') { e.preventDefault(); setHi(h => Math.max(0, h - 1)) }
          else if (e.key === 'Enter' || e.key === ',') {
            const o = show ? options[hi] : null
            if (o || text.trim()) { e.preventDefault(); add(o ? o.value : text) }
          }
          else if (e.key === 'Escape' && show) { e.stopPropagation(); setOpen(false) }
          else if (e.key === 'Backspace' && !text && value.length) onChange(value.slice(0, -1))
        }} />
      {show && r && createPortal(
        <div className="select-pop chip-sugg" role="listbox" style={{ left: r.left, top: r.bottom + 4, width: Math.min(320, r.width) }} data-keep-open>
          <div className="select-list">
            {options.map((o, i) => (
              <div key={o.value + i} role="option" aria-selected={i === hi} className={`select-option ${i === hi ? 'hi' : ''}`} onMouseEnter={() => setHi(i)}
                onMouseDown={e => { e.preventDefault(); add(o.value) }}>
                <Icon name={o.add ? 'plus' : 'tag'} size={13} /><span className="grow"><span className="opt-label">{o.label}</span></span>
              </div>
            ))}
          </div>
        </div>, document.body)}
    </div>
  )
}

export function AddChip({ icon, label, onClick }: { icon: string; label: string; onClick: () => void }) {
  return <button type="button" className="add-chip" onClick={onClick}><Icon name="plus" size={13} />{label}<Icon name={icon} size={13} className="add-chip-ico" /></button>
}

export function RowShell({ children, onRemove, removeLabel }: { children: React.ReactNode; onRemove: () => void; removeLabel: string }) {
  return (
    <div className="erow">
      {children}
      <button type="button" className="btn ghost icon-only sm erow-x" aria-label={removeLabel} title={removeLabel} onClick={onRemove}><Icon name="minus" size={15} /></button>
    </div>
  )
}
