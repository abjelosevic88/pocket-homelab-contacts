import React, { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from 'react'
import { api, V1 } from './api'
import type { AddressBook, AuthStatus, Categories, Person, Settings, SettingsResponse, User } from './types'

export type ThemeMode = 'system' | 'light' | 'dark'

interface AppState {
  status: AuthStatus | null
  user: User | null
  sr: SettingsResponse | null
  settings: Settings | null
  books: AddressBook[]
  categories: Categories
  people: Person[]
  /** Bumps whenever something changed on the server: lists refetch. */
  version: number
  bump: () => void
  themeMode: ThemeMode
  theme: 'light' | 'dark'
  setThemeMode: (m: ThemeMode) => void
  refresh: () => Promise<void>
  reloadBooks: () => Promise<void>
  reloadCategories: () => Promise<void>
  reloadPeople: () => Promise<void>
  saveSettings: (patch: Partial<Settings> & { notify_token?: string }) => Promise<SettingsResponse>
  patchBook: (id: number, patch: Partial<Pick<AddressBook, 'name' | 'color' | 'description' | 'visible' | 'sort_order' | 'is_default'>>) => Promise<void>
  setUser: (u: User | null) => void
  setBooks: React.Dispatch<React.SetStateAction<AddressBook[]>>
}

const Ctx = createContext<AppState>(null!)
export function useApp() { return useContext(Ctx) }

const THEME_KEY = 'pcon-theme'
const noCats: Categories = { labels: [], groups: [] }

export function AppProvider({ children }: { children: React.ReactNode }) {
  const [status, setStatus] = useState<AuthStatus | null>(null)
  const [user, setUser] = useState<User | null>(null)
  const [sr, setSr] = useState<SettingsResponse | null>(null)
  const [books, setBooks] = useState<AddressBook[]>([])
  const [categories, setCategories] = useState<Categories>(noCats)
  const [people, setPeople] = useState<Person[]>([])
  const [version, setVersion] = useState(0)
  const [themeMode, setThemeModeRaw] = useState<ThemeMode>(() => (localStorage.getItem(THEME_KEY) as ThemeMode) || 'system')
  const [systemDark, setSystemDark] = useState(() => window.matchMedia('(prefers-color-scheme: dark)').matches)

  useEffect(() => {
    const mq = window.matchMedia('(prefers-color-scheme: dark)')
    const h = () => setSystemDark(mq.matches)
    mq.addEventListener('change', h)
    return () => mq.removeEventListener('change', h)
  }, [])
  const theme: 'light' | 'dark' = themeMode === 'system' ? (systemDark ? 'dark' : 'light') : themeMode
  useEffect(() => {
    document.documentElement.dataset.theme = theme
    document.querySelectorAll('meta[name="theme-color"]').forEach(m => m.setAttribute('content', theme === 'dark' ? '#111113' : '#ffffff'))
  }, [theme])
  const setThemeMode = useCallback((m: ThemeMode) => { localStorage.setItem(THEME_KEY, m); setThemeModeRaw(m) }, [])

  const settings = sr?.settings ?? null
  useEffect(() => { if (settings?.title) document.title = settings.title }, [settings?.title])
  useEffect(() => { document.documentElement.dataset.density = settings?.density || 'comfortable' }, [settings?.density])

  const reloadBooks = useCallback(async () => { setBooks((await api.get<AddressBook[]>(`${V1}/addressbooks`)) || []) }, [])
  const reloadCategories = useCallback(async () => {
    const c = await api.get<Categories>(`${V1}/categories`).catch(() => noCats)
    setCategories({ labels: c?.labels || [], groups: c?.groups || [] })
  }, [])
  const reloadPeople = useCallback(async () => { setPeople((await api.get<Person[]>(`${V1}/people`).catch(() => [])) || []) }, [])

  const refresh = useCallback(async () => {
    const st = await api.get<AuthStatus>(`${V1}/auth/status`)
    setStatus(st)
    setUser(st.user)
    if (st.authenticated) {
      const [s] = await Promise.all([
        api.get<SettingsResponse>(`${V1}/settings`),
        reloadBooks().catch(() => {}),
        reloadCategories(),
        reloadPeople(),
      ])
      setSr(s)
    }
  }, [reloadBooks, reloadCategories, reloadPeople])

  useEffect(() => {
    refresh().catch(() => setStatus({ needs_setup: false, auth_mode: 'local', authenticated: false, user: null, version: '', title: 'Pocket Contacts' }))
  }, [refresh])
  useEffect(() => {
    const h = () => { setUser(null); setStatus(s => s ? { ...s, authenticated: false, user: null } : s) }
    window.addEventListener('pcon:unauthorized', h)
    return () => window.removeEventListener('pcon:unauthorized', h)
  }, [])

  const bump = useCallback(() => setVersion(v => v + 1), [])

  const saveSettings = useCallback(async (patch: Partial<Settings> & { notify_token?: string }) => {
    setSr(s => s ? { ...s, settings: { ...s.settings, ...patch } } : s)
    try {
      const r = await api.put<SettingsResponse>(`${V1}/settings`, patch)
      setSr(r)
      if ('sort_by' in patch || 'display_order' in patch) bump()
      return r
    } catch (e) {
      setSr(await api.get<SettingsResponse>(`${V1}/settings`))
      throw e
    }
  }, [bump])

  const patchBook = useCallback<AppState['patchBook']>(async (id, patch) => {
    setBooks(bs => bs.map(b => b.id === id ? { ...b, ...patch } : patch.is_default ? { ...b, is_default: false } : b))
    try {
      await api.put(`${V1}/addressbooks/${id}`, patch)
      if ('visible' in patch) bump()
      await reloadBooks()
    } catch (e) {
      await reloadBooks()
      throw e
    }
  }, [bump, reloadBooks])

  // Changes made elsewhere (a phone over CardDAV, another person, a sync, another
  // Pocket app) reach the open page: /changes is polled every 10 s while the tab
  // is visible, and on focus.
  const stamp = useRef<number | null>(null)
  useEffect(() => {
    if (!user) return
    const check = async () => {
      if (document.hidden) return
      try {
        const r = await api.get<{ stamp: number }>(`${V1}/changes`)
        if (stamp.current !== null && r.stamp !== stamp.current) {
          bump()
          reloadBooks().catch(() => {})
          reloadCategories()
        }
        stamp.current = r.stamp
      } catch { /* offline: try again later */ }
    }
    check()
    const t = setInterval(check, 10000)
    const onShow = () => { if (!document.hidden) check() }
    document.addEventListener('visibilitychange', onShow)
    window.addEventListener('focus', onShow)
    return () => { clearInterval(t); document.removeEventListener('visibilitychange', onShow); window.removeEventListener('focus', onShow) }
  }, [user, bump, reloadBooks, reloadCategories])

  const value = useMemo<AppState>(() => ({
    status, user, sr, settings, books, categories, people, version, bump, themeMode, theme, setThemeMode, refresh,
    reloadBooks, reloadCategories, reloadPeople, saveSettings, patchBook, setUser, setBooks,
  }), [status, user, sr, settings, books, categories, people, version, bump, themeMode, theme, setThemeMode, refresh, reloadBooks, reloadCategories, reloadPeople, saveSettings, patchBook])

  return <Ctx.Provider value={value}>{children}</Ctx.Provider>
}
