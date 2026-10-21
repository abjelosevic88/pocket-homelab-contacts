import React, { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from 'react'
import { api, V1 } from './api'
import { browserZone } from './dates'
import { clearRangeCache } from './data'
import type { AuthStatus, Calendar, Occurrence, OverlaySource, PeopleEntry, Settings, SettingsResponse, User } from './types'

export type ThemeMode = 'system' | 'light' | 'dark'

interface AppState {
  status: AuthStatus | null
  user: User | null
  sr: SettingsResponse | null
  settings: Settings | null
  calendars: Calendar[]
  sources: OverlaySource[]
  people: PeopleEntry[]
  invitations: Occurrence[]
  /** Bumps whenever something changed on the server: views refetch. */
  version: number
  bump: () => void
  /** The person's time zone: their choice, else the browser's. */
  tz: string
  /** `tz` query value: the browser zone only when no zone was chosen. */
  tzParam: string
  h12: boolean
  themeMode: ThemeMode
  theme: 'light' | 'dark'
  setThemeMode: (m: ThemeMode) => void
  refresh: () => Promise<void>
  reloadCalendars: () => Promise<void>
  reloadSources: () => Promise<void>
  reloadPeople: () => Promise<void>
  reloadInvitations: () => Promise<void>
  saveSettings: (patch: Partial<Settings> & { notify_token?: string }) => Promise<SettingsResponse>
  patchCalendar: (id: number, patch: Partial<Calendar> & { my_color?: string }) => Promise<void>
  setUser: (u: User | null) => void
  setCalendars: React.Dispatch<React.SetStateAction<Calendar[]>>
  setSources: React.Dispatch<React.SetStateAction<OverlaySource[]>>
}

const Ctx = createContext<AppState>(null!)
export function useApp() { return useContext(Ctx) }

const THEME_KEY = 'pcon-theme'

export function AppProvider({ children }: { children: React.ReactNode }) {
  const [status, setStatus] = useState<AuthStatus | null>(null)
  const [user, setUser] = useState<User | null>(null)
  const [sr, setSr] = useState<SettingsResponse | null>(null)
  const [calendars, setCalendars] = useState<Calendar[]>([])
  const [sources, setSources] = useState<OverlaySource[]>([])
  const [people, setPeople] = useState<PeopleEntry[]>([])
  const [invitations, setInvitations] = useState<Occurrence[]>([])
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
    const meta = document.querySelectorAll('meta[name="theme-color"]')
    meta.forEach(m => m.setAttribute('content', theme === 'dark' ? '#121214' : '#ffffff'))
  }, [theme])
  const setThemeMode = useCallback((m: ThemeMode) => { localStorage.setItem(THEME_KEY, m); setThemeModeRaw(m) }, [])

  const settings = sr?.settings ?? null
  useEffect(() => { if (settings?.title) document.title = settings.title }, [settings?.title])
  useEffect(() => {
    document.documentElement.dataset.density = settings?.density || 'comfortable'
  }, [settings?.density])

  const tzParam = settings && !settings.timezone ? browserZone() : ''
  const tz = settings?.timezone || browserZone()
  const tzq = tzParam ? `?tz=${encodeURIComponent(tzParam)}` : ''

  const reloadCalendars = useCallback(async () => { setCalendars(await api.get<Calendar[]>(`${V1}/calendars`)) }, [])
  const reloadSources = useCallback(async () => { setSources(await api.get<OverlaySource[]>(`${V1}/overlays/sources`).catch(() => [])) }, [])
  const reloadPeople = useCallback(async () => { setPeople(await api.get<PeopleEntry[]>(`${V1}/people`).catch(() => [])) }, [])
  const reloadInvitations = useCallback(async () => {
    setInvitations(await api.get<Occurrence[]>(`${V1}/invitations${tzq}`).catch(() => []))
  }, [tzq])

  const refresh = useCallback(async () => {
    const st = await api.get<AuthStatus>(`${V1}/auth/status`)
    setStatus(st)
    setUser(st.user)
    if (st.authenticated) {
      const [s] = await Promise.all([
        api.get<SettingsResponse>(`${V1}/settings`),
        api.get<Calendar[]>(`${V1}/calendars`).then(setCalendars).catch(() => {}),
        api.get<OverlaySource[]>(`${V1}/overlays/sources`).then(setSources).catch(() => {}),
        api.get<PeopleEntry[]>(`${V1}/people`).then(setPeople).catch(() => {}),
      ])
      setSr(s)
    }
  }, [])

  useEffect(() => {
    refresh().catch(() => setStatus({ needs_setup: false, auth_mode: 'local', authenticated: false, user: null, version: '', title: 'Pocket Contacts' }))
  }, [refresh])
  useEffect(() => {
    const h = () => { setUser(null); setStatus(s => s ? { ...s, authenticated: false, user: null } : s) }
    window.addEventListener('pcal:unauthorized', h)
    return () => window.removeEventListener('pcal:unauthorized', h)
  }, [])
  useEffect(() => { if (user && settings) reloadInvitations() }, [user, settings, version, reloadInvitations])

  const bump = useCallback(() => { clearRangeCache(); setVersion(v => v + 1) }, [])

  const saveSettings = useCallback(async (patch: Partial<Settings> & { notify_token?: string }) => {
    setSr(s => s ? { ...s, settings: { ...s.settings, ...patch } } : s)
    try {
      const r = await api.put<SettingsResponse>(`${V1}/settings`, patch)
      setSr(r)
      if ('holiday_country' in patch || 'timezone' in patch || 'show_declined' in patch) bump()
      return r
    } catch (e) {
      setSr(await api.get<SettingsResponse>(`${V1}/settings`))
      throw e
    }
  }, [bump])

  const patchCalendar = useCallback(async (id: number, patch: Partial<Calendar> & { my_color?: string }) => {
    setCalendars(cs => cs.map(c => c.id === id ? { ...c, ...patch, ...(patch.my_color ? { color: patch.my_color } : {}) } : c))
    try {
      await api.put(`${V1}/calendars/${id}`, patch)
      if ('visible' in patch || 'my_color' in patch || 'color' in patch) bump()
      if ('is_default' in patch) await reloadCalendars()
    } catch (e) {
      await reloadCalendars()
      throw e
    }
  }, [bump, reloadCalendars])

  // Changes made elsewhere (a phone over CalDAV, another person, another
  // Pocket app) reach the open page: /changes is polled every 15 s while the
  // tab is visible, and on focus.
  const stamp = useRef<number | null>(null)
  useEffect(() => {
    if (!user) return
    const check = async () => {
      if (document.hidden) return
      try {
        const r = await api.get<{ stamp: number }>(`${V1}/changes`)
        if (stamp.current !== null && r.stamp !== stamp.current) {
          bump()
          reloadCalendars().catch(() => {})
        }
        stamp.current = r.stamp
      } catch { /* offline: try again later */ }
    }
    check()
    const t = setInterval(check, 15000)
    const onShow = () => { if (!document.hidden) check() }
    document.addEventListener('visibilitychange', onShow)
    window.addEventListener('focus', onShow)
    return () => { clearInterval(t); document.removeEventListener('visibilitychange', onShow); window.removeEventListener('focus', onShow) }
  }, [user, bump, reloadCalendars])

  const value = useMemo<AppState>(() => ({
    status, user, sr, settings, calendars, sources, people, invitations, version, bump, tz, tzParam,
    h12: settings?.time_format === '12h', themeMode, theme, setThemeMode, refresh, reloadCalendars, reloadSources,
    reloadPeople, reloadInvitations, saveSettings, patchCalendar, setUser, setCalendars, setSources,
  }), [status, user, sr, settings, calendars, sources, people, invitations, version, bump, tz, tzParam, themeMode, theme, setThemeMode, refresh, reloadCalendars, reloadSources, reloadPeople, reloadInvitations, saveSettings, patchCalendar])

  return <Ctx.Provider value={value}>{children}</Ctx.Provider>
}
