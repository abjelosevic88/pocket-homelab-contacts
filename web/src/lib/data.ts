// The visible range's events and overlay items, cached per range so going
// back to a week you just saw is instant (and refreshed in the background).
import { useEffect, useRef, useState } from 'react'
import { api, V1, qs } from './api'
import type { EventsResponse, Occurrence, OverlayItem, OverlaysResponse } from './types'

export interface RangeData {
  events: Occurrence[]
  overlays: OverlayItem[]
  errors: Record<string, string>
}

interface Entry { data: RangeData | null; at: number; fresh: boolean; promise?: Promise<RangeData> }
const cache = new Map<string, Entry>()

/** Mark everything stale (keep the data to show while refetching). */
export function clearRangeCache() { for (const e of cache.values()) e.fresh = false }

async function fetchRange(from: string, to: string, tz: string, overlays: boolean): Promise<RangeData> {
  const q = qs({ from, to, tz })
  const [ev, ov] = await Promise.all([
    api.get<EventsResponse>(`${V1}/events${q}`),
    overlays ? api.get<OverlaysResponse>(`${V1}/overlays${q}`).catch(e => ({ items: [], errors: { _: (e as Error).message } }) as OverlaysResponse) : Promise.resolve({ items: [], errors: {} } as OverlaysResponse),
  ])
  return { events: ev.events || [], overlays: ov.items || [], errors: ov.errors || {} }
}

function load(key: string, from: string, to: string, tz: string, overlays: boolean): Promise<RangeData> {
  const e = cache.get(key)
  if (e?.promise) return e.promise
  const p = fetchRange(from, to, tz, overlays).then(d => {
    cache.set(key, { data: d, at: Date.now(), fresh: true })
    if (cache.size > 40) { const first = cache.keys().next().value; if (first) cache.delete(first) }
    return d
  }).catch(err => { const c = cache.get(key); if (c) c.promise = undefined; throw err })
  cache.set(key, { data: e?.data ?? null, at: e?.at ?? 0, fresh: false, promise: p })
  return p
}

/** Warm the cache for a neighbouring range (prev/next) without rendering it. */
export function prefetchRange(from: string, to: string, tz: string, overlays = true) {
  const key = `${from}|${to}|${tz}|${overlays}`
  const e = cache.get(key)
  if (e && (e.fresh || e.promise)) return
  load(key, from, to, tz, overlays).catch(() => {})
}

export function useRange(from: string, to: string, tz: string, version: number, overlays = true) {
  const key = `${from}|${to}|${tz}|${overlays}`
  const [state, setState] = useState<{ key: string; data: RangeData | null; loading: boolean; error: string }>(() => {
    const e = cache.get(key)
    return { key, data: e?.data ?? null, loading: !e?.fresh, error: '' }
  })
  const [tick, setTick] = useState(0)
  const live = useRef(key)
  useEffect(() => {
    live.current = key
    const e = cache.get(key)
    if (e?.data) setState({ key, data: e.data, loading: !e.fresh, error: '' })
    else setState(s => ({ key, data: s.key === key ? s.data : null, loading: true, error: '' }))
    if (e?.fresh) return
    load(key, from, to, tz, overlays)
      .then(d => { if (live.current === key) setState({ key, data: d, loading: false, error: '' }) })
      .catch(err => { if (live.current === key) setState(s => ({ ...s, loading: false, error: (err as Error).message })) })
  }, [key, version, tick]) // eslint-disable-line react-hooks/exhaustive-deps
  const reload = () => { const e = cache.get(key); if (e) e.fresh = false; setTick(t => t + 1) }
  return { data: state.key === key ? state.data : null, loading: state.loading, error: state.error, reload }
}
