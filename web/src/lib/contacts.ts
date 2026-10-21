// Names, sorting, search folding, dates and links for contacts.
import type { Card, CardAddress, CardField, Settings, Summary } from './types'

// ---------- folding (search + sort) ----------

const CYR: Record<string, string> = {
  а: 'a', б: 'b', в: 'v', г: 'g', д: 'd', ђ: 'dj', е: 'e', ё: 'e', ж: 'z', з: 'z', и: 'i', й: 'j', ј: 'j', к: 'k', л: 'l', љ: 'lj', м: 'm',
  н: 'n', њ: 'nj', о: 'o', п: 'p', р: 'r', с: 's', т: 't', ћ: 'c', у: 'u', ф: 'f', х: 'h', ц: 'c', ч: 'c', џ: 'dz', ш: 's', щ: 'sc',
  ъ: '', ы: 'y', ь: '', э: 'e', ю: 'ju', я: 'ja', ґ: 'g', є: 'je', і: 'i', ї: 'ji', ѓ: 'g', ќ: 'k', ѕ: 'dz',
}

/** Lower case, no diacritics, Cyrillic in Latin letters; `đ` becomes `d` (or `dj` with dj=true). */
export function fold(s: string, dj = false): string {
  let out = ''
  for (const ch of (s || '').toLowerCase()) {
    if (ch === 'đ') out += dj ? 'dj' : 'd'
    else if (ch === 'ß') out += 'ss'
    else if (ch === 'æ') out += 'ae'
    else if (ch === 'ø') out += 'o'
    else if (ch === 'ł') out += 'l'
    else if (CYR[ch] !== undefined) out += ch === 'ђ' && !dj ? 'd' : CYR[ch]
    else out += ch
  }
  return out.normalize('NFD').replace(/[̀-ͯ]/g, '')
}

const digits = (s: string) => s.replace(/\D+/g, '')

/** Everything a quick local search looks at. */
export function haystack(c: Summary): string {
  const text = [c.fn, c.nickname, c.org, c.title, ...(c.emails || []), ...(c.categories || [])].join(' ')
  return `${fold(text)} ${fold(text, true)}`
}

/** A local match: every word of the query is in the contact, or the digits are in a phone number. */
export function matches(c: Summary, q: string, hay?: string): boolean {
  const words = fold(q).split(/\s+/).filter(Boolean)
  if (!words.length) return true
  const h = hay ?? haystack(c)
  if (words.every(w => h.includes(w))) return true
  const d = digits(q)
  return d.length >= 3 && (c.phones || []).some(p => digits(p).includes(d))
}

// ---------- names ----------

export function displayName(c: Pick<Summary, 'fn' | 'given' | 'family' | 'kind' | 'org' | 'emails' | 'phones'>, order: Settings['display_order'] = 'given_first'): string {
  if (c.kind !== 'org' && order === 'family_first' && c.given && c.family) return `${c.family} ${c.given}`
  return c.fn || c.org || c.emails?.[0] || c.phones?.[0] || 'No name'
}

/** The name with the sorted part in bold (macOS style). */
export function nameParts(c: Summary, s: Pick<Settings, 'display_order' | 'sort_by'>): { pre: string; bold: string; post: string } {
  const name = displayName(c, s.display_order)
  const key = c.kind === 'org' ? '' : s.sort_by === 'family' ? c.family : c.given
  if (!key) return { pre: '', bold: name, post: '' }
  const i = name.indexOf(key)
  if (i < 0) return { pre: '', bold: name, post: '' }
  return { pre: name.slice(0, i), bold: key, post: name.slice(i + key.length) }
}

export function sortKey(c: Summary, by: Settings['sort_by']): string {
  const primary = c.kind === 'org' ? c.org || c.fn : (by === 'family' ? c.family || c.given : c.given || c.family) || c.fn || c.org
  const secondary = by === 'family' ? c.given : c.family
  return `${fold(primary || c.emails?.[0] || c.phones?.[0] || '')}\u0001${fold(secondary || '')}\u0001${fold(c.fn)}`
}

/** The A–Z section a sort key belongs to ("#" for digits and symbols). */
export function letterOf(key: string): string {
  const ch = key.charAt(0).toUpperCase()
  return ch >= 'A' && ch <= 'Z' ? ch : '#'
}

export function sortContacts(list: Summary[], by: Settings['sort_by']): Summary[] {
  const keyed = list.map(c => ({ c, k: sortKey(c, by) }))
  keyed.sort((a, b) => {
    const la = letterOf(a.k) === '#', lb = letterOf(b.k) === '#'
    if (la !== lb) return la ? 1 : -1
    return a.k < b.k ? -1 : a.k > b.k ? 1 : a.c.id - b.c.id
  })
  return keyed.map(x => x.c)
}

export function secondaryLine(c: Summary): string {
  const work = [c.org, c.title].filter(Boolean).join(' · ')
  if (work && c.kind !== 'org') return work
  if (c.kind === 'org' && c.title) return c.title
  return c.emails?.[0] || c.phones?.[0] || ''
}

// ---------- labels ----------

export const EMAIL_LABELS = ['home', 'work', 'other']
export const PHONE_LABELS = ['mobile', 'iphone', 'home', 'work', 'main', 'home fax', 'work fax', 'other fax', 'pager', 'other']
export const ADDRESS_LABELS = ['home', 'work', 'other']
export const URL_LABELS = ['homepage', 'home', 'work', 'other']
export const IM_LABELS = ['skype', 'telegram', 'whatsapp', 'signal', 'viber', 'jabber', 'matrix', 'other']
export const SOCIAL_LABELS = ['twitter', 'linkedin', 'facebook', 'instagram', 'github', 'mastodon', 'other']
export const RELATED_LABELS = ['mother', 'father', 'parent', 'brother', 'sister', 'child', 'friend', 'spouse', 'partner', 'assistant', 'manager', 'other']
export const DATE_LABELS = ['anniversary', 'other']

const SPECIAL: Record<string, string> = { iphone: 'iPhone', linkedin: 'LinkedIn', github: 'GitHub', whatsapp: 'WhatsApp', twitter: 'X / Twitter', homepage: 'Homepage' }
export function labelText(l: string): string {
  if (!l) return 'Other'
  const k = l.toLowerCase()
  if (SPECIAL[k]) return SPECIAL[k]
  return l.charAt(0).toUpperCase() + l.slice(1)
}

// ---------- dates ----------

export const MONTHS = ['January', 'February', 'March', 'April', 'May', 'June', 'July', 'August', 'September', 'October', 'November', 'December']

export interface DateParts { y: number | null; m: number; d: number }

/** "1988-05-03", "--05-03", "19880503", "--0503". */
export function parseDate(s: string | undefined | null): DateParts | null {
  if (!s) return null
  let m = /^(\d{4})-?(\d{2})-?(\d{2})/.exec(s)
  if (m) return { y: +m[1], m: +m[2], d: +m[3] }
  m = /^--(\d{2})-?(\d{2})$/.exec(s)
  if (m) return { y: null, m: +m[1], d: +m[2] }
  return null
}

export function toDateString(p: DateParts): string {
  const mm = String(p.m).padStart(2, '0'), dd = String(p.d).padStart(2, '0')
  return p.y ? `${String(p.y).padStart(4, '0')}-${mm}-${dd}` : `--${mm}-${dd}`
}

export function fmtDate(s: string, withYear = true): string {
  const p = parseDate(s)
  if (!p) return s
  return `${p.d} ${MONTHS[p.m - 1] || '?'}${p.y && withYear ? ` ${p.y}` : ''}`
}

const dayMs = 86400000
const localMidnight = (d: Date) => new Date(d.getFullYear(), d.getMonth(), d.getDate())

/** Days until the next time this day and month comes around (0 = today) and the age then. */
export function nextOccurrence(s: string, now = new Date()): { days: number; age: number | null; date: Date } | null {
  const p = parseDate(s)
  if (!p) return null
  const today = localMidnight(now)
  const at = (y: number) => {
    // 29 February falls on 28 February in other years
    const leap = (y % 4 === 0 && y % 100 !== 0) || y % 400 === 0
    return new Date(y, p.m - 1, p.m === 2 && p.d === 29 && !leap ? 28 : p.d)
  }
  let y = today.getFullYear()
  let d = at(y)
  if (d < today) d = at(++y)
  return { days: Math.round((d.getTime() - today.getTime()) / dayMs), age: p.y ? y - p.y : null, date: d }
}

export function inDays(n: number): string {
  if (n === 0) return 'today'
  if (n === 1) return 'tomorrow'
  if (n < 7) return `in ${n} days`
  if (n < 14) return 'next week'
  if (n < 60) return `in ${Math.round(n / 7)} weeks`
  return `in ${Math.round(n / 30.4)} months`
}

/** "3 min ago", "yesterday", "Oct 12". */
export function ago(iso: string | null | undefined): string {
  if (!iso) return 'never'
  const t = Date.parse(iso)
  if (isNaN(t)) return iso
  const s = Math.round((Date.now() - t) / 1000)
  if (s < 45) return 'just now'
  if (s < 3600) return `${Math.max(1, Math.round(s / 60))} min ago`
  if (s < 86400) return `${Math.round(s / 3600)} h ago`
  if (s < 2 * 86400) return 'yesterday'
  if (s < 14 * 86400) return `${Math.round(s / 86400)} days ago`
  return new Date(t).toLocaleDateString('en-GB', { day: 'numeric', month: 'short', year: new Date(t).getFullYear() === new Date().getFullYear() ? undefined : 'numeric' })
}

export function ordinalAge(n: number): string {
  const s = n % 100 >= 11 && n % 100 <= 13 ? 'th' : ['th', 'st', 'nd', 'rd'][n % 10] || 'th'
  return `${n}${s}`
}

// ---------- links ----------

export const telHref = (v: string) => `tel:${v.replace(/[^\d+*#,;]/g, '')}`
export const smsHref = (v: string) => `sms:${v.replace(/[^\d+]/g, '')}`

export function addressLines(a: CardAddress): string[] {
  const line2 = [a.postcode, a.city].filter(Boolean).join(' ')
  return [a.pobox ? `PO Box ${a.pobox}` : '', a.street, a.ext || '', [line2, a.region].filter(Boolean).join(', '), a.country].filter(Boolean)
}

export function mapsHref(a: CardAddress): string {
  const q = encodeURIComponent(addressLines(a).join(', '))
  return /Mac|iPhone|iPad|iPod/.test(navigator.userAgent) ? `https://maps.apple.com/?q=${q}` : `https://www.google.com/maps/search/?api=1&query=${q}`
}

export function webHref(v: string): string {
  if (/^[a-z][a-z0-9+.-]*:/i.test(v)) return v
  return `https://${v}`
}

export function socialHref(f: CardField): string | null {
  const v = f.value.trim()
  if (/^https?:\/\//i.test(v)) return v
  const h = v.replace(/^@/, '')
  switch (f.label.toLowerCase()) {
    case 'twitter': case 'x': return `https://x.com/${h}`
    case 'linkedin': return `https://www.linkedin.com/in/${h}`
    case 'facebook': return `https://www.facebook.com/${h}`
    case 'instagram': return `https://www.instagram.com/${h}`
    case 'github': return `https://github.com/${h}`
    case 'mastodon': { const m = /^([^@]+)@(.+)$/.exec(h); return m ? `https://${m[2]}/@${m[1]}` : null }
  }
  return /\./.test(v) ? webHref(v) : null
}

export function imHref(f: CardField): string | null {
  const v = f.value.trim()
  if (/^[a-z][a-z0-9+.-]*:/i.test(v)) return v
  const h = v.replace(/^@/, '')
  switch (f.label.toLowerCase()) {
    case 'telegram': return `https://t.me/${h}`
    case 'whatsapp': return `https://wa.me/${digits(v)}`
    case 'skype': return `skype:${h}?chat`
    case 'viber': return `viber://chat?number=${encodeURIComponent(v.replace(/[^\d+]/g, ''))}`
    case 'signal': return digits(v).length >= 6 ? `https://signal.me/#p/${v.replace(/[^\d+]/g, '')}` : null
    case 'matrix': return `https://matrix.to/#/${v.startsWith('@') ? v : '@' + v}`
    case 'jabber': return `xmpp:${h}`
  }
  return null
}

// ---------- cards ----------

export function emptyCard(): Card {
  return {
    kind: 'individual', fn: '', prefix: '', given: '', middle: '', family: '', suffix: '', nickname: '', phonetic_given: '', phonetic_family: '',
    org: '', department: '', title: '', emails: [], phones: [], addresses: [], urls: [], ims: [], socials: [], birthday: '', anniversary: '',
    dates: [], related: [], note: '', categories: [],
  }
}

/** Arrays never null, so the editor can map over them. */
export function normalizeCard(c: Card): Card {
  return {
    ...emptyCard(), ...c,
    emails: c.emails || [], phones: c.phones || [], addresses: c.addresses || [], urls: c.urls || [], ims: c.ims || [], socials: c.socials || [],
    dates: c.dates || [], related: c.related || [], categories: c.categories || [],
  }
}

/** Drop rows the person left empty. */
export function cleanCard(c: Card): Card {
  const f = (xs: CardField[] | null) => (xs || []).map(x => ({ ...x, value: x.value.trim() })).filter(x => x.value)
  return {
    ...c,
    emails: f(c.emails), phones: f(c.phones), urls: f(c.urls), ims: f(c.ims), socials: f(c.socials), dates: f(c.dates), related: f(c.related),
    addresses: (c.addresses || []).filter(a => [a.street, a.ext, a.pobox, a.city, a.region, a.postcode, a.country].some(v => (v || '').trim())),
    categories: (c.categories || []).map(s => s.trim()).filter(Boolean),
  }
}

export function cardTitle(c: Card): string {
  if (c.fn) return c.fn
  const n = [c.prefix, c.given, c.middle, c.family, c.suffix].filter(Boolean).join(' ')
  return n || c.org || ''
}
