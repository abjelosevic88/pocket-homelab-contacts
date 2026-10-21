// What the contact list shows: everything, favorites, one book, one label, …
import { qs } from './api'
import type { AddressBook } from './types'

export type Scope =
  | { kind: 'all' }
  | { kind: 'favorites' }
  | { kind: 'recent' }
  | { kind: 'book'; id: number }
  | { kind: 'label'; name: string }
  | { kind: 'nolabel' }
  | { kind: 'group'; uid: string; name: string }

const KEY = 'pcon-scope'

export function loadScope(): Scope {
  try {
    const s = JSON.parse(sessionStorage.getItem(KEY) || '') as Scope
    if (s && typeof s.kind === 'string') return s
  } catch { /* first visit */ }
  return { kind: 'all' }
}
export function storeScope(s: Scope) { sessionStorage.setItem(KEY, JSON.stringify(s)) }

export function sameScope(a: Scope, b: Scope): boolean { return JSON.stringify(a) === JSON.stringify(b) }

/** Scopes the browser can filter from the full list without asking the server. */
export const isLocalScope = (s: Scope) => s.kind === 'all' || s.kind === 'favorites'

export function scopeQuery(s: Scope, extra: Record<string, string | number | undefined> = {}): string {
  const p: Record<string, string | number | undefined> = { limit: 5000, ...extra }
  switch (s.kind) {
    case 'favorites': p.starred = 1; break
    case 'recent': p.recent = 1; break
    case 'book': p.book = s.id; break
    case 'label': p.category = s.name; break
    case 'nolabel': p.category = '-'; break
    case 'group': p.category = `group:${s.uid}`; break
  }
  return qs(p)
}

export function scopeTitle(s: Scope, books: AddressBook[]): string {
  switch (s.kind) {
    case 'all': return 'All contacts'
    case 'favorites': return 'Favorites'
    case 'recent': return 'Recent'
    case 'book': return books.find(b => b.id === s.id)?.name || 'Address book'
    case 'label': return s.name
    case 'nolabel': return 'No label'
    case 'group': return s.name
  }
}
