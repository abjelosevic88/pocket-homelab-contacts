// URL state and the visible range of each view.
import { type Wall, addDays, addMonths, dayKey, MONTHS, monthKey, rangeTitle, startOfMonth, startOfWeek, startOfYear, weekday } from './dates'
import type { Settings, ViewName } from './types'

export const VIEWS: ViewName[] = ['day', '3day', 'week', 'month', 'year', 'agenda', 'people']
export const isView = (v: string | undefined): v is ViewName => !!v && (VIEWS as string[]).includes(v)

export function parseDateParam(s: string | undefined): Wall | null {
  if (!s) return null
  let m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(s)
  if (m) return Date.UTC(+m[1], +m[2] - 1, +m[3])
  m = /^(\d{4})-(\d{2})$/.exec(s)
  if (m) return Date.UTC(+m[1], +m[2] - 1, 1)
  m = /^(\d{4})$/.exec(s)
  if (m) return Date.UTC(+m[1], 0, 1)
  return null
}

export function urlFor(view: ViewName, d: Wall, today: Wall, search = ''): string {
  switch (view) {
    case 'month': return `/month/${monthKey(d)}${search}`
    case 'year': return `/year/${new Date(d).getUTCFullYear()}${search}`
    case 'agenda': return (d === today ? '/agenda' : `/agenda/${dayKey(d)}`) + search
    default: return `/${view}/${dayKey(d)}${search}`
  }
}

export interface Range { from: Wall; to: Wall; days: Wall[]; title: string; short: string }

export function rangeFor(view: ViewName, d: Wall, s: Settings | null, opts: { agendaDays: number; peopleSpan: 'day' | 'week' }): Range {
  const ws = s?.week_start ?? 1
  const span = (from: Wall, n: number) => Array.from({ length: n }, (_, i) => addDays(from, i))
  const month = (w: Wall) => `${MONTHS[new Date(w).getUTCMonth()]} ${new Date(w).getUTCFullYear()}`
  switch (view) {
    case 'day': return { from: d, to: addDays(d, 1), days: [d], title: rangeTitle(d, addDays(d, 1)), short: month(d) }
    case '3day': return { from: d, to: addDays(d, 3), days: span(d, 3), title: rangeTitle(d, addDays(d, 3)), short: month(d) }
    case 'week': {
      const from = startOfWeek(d, ws)
      let days = span(from, 7)
      if (s && !s.show_weekends) days = days.filter(x => weekday(x) !== 0 && weekday(x) !== 6)
      return { from, to: addDays(from, 7), days, title: rangeTitle(from, addDays(from, 7)), short: month(from) }
    }
    case 'month': {
      const first = startOfMonth(d)
      const from = startOfWeek(first, ws)
      return { from, to: addDays(from, 42), days: [], title: month(first), short: month(first) }
    }
    case 'year': {
      const from = startOfYear(d)
      return { from, to: addMonths(from, 12), days: [], title: String(new Date(from).getUTCFullYear()), short: String(new Date(from).getUTCFullYear()) }
    }
    case 'agenda': return { from: d, to: addDays(d, opts.agendaDays), days: [], title: rangeTitle(d, addDays(d, opts.agendaDays)), short: month(d) }
    case 'people': {
      if (opts.peopleSpan === 'day') return { from: d, to: addDays(d, 1), days: [d], title: rangeTitle(d, addDays(d, 1)), short: month(d) }
      const from = startOfWeek(d, ws)
      let days = span(from, 7)
      if (s && !s.show_weekends) days = days.filter(x => weekday(x) !== 0 && weekday(x) !== 6)
      return { from, to: addDays(from, 7), days, title: rangeTitle(from, addDays(from, 7)), short: month(from) }
    }
  }
}

export function step(view: ViewName, d: Wall, dir: 1 | -1, s: Settings | null, opts: { peopleSpan: 'day' | 'week' }): Wall {
  switch (view) {
    case 'day': return addDays(d, dir)
    case '3day': return addDays(d, 3 * dir)
    case 'week': return addDays(startOfWeek(d, s?.week_start ?? 1), 7 * dir)
    case 'month': return addMonths(startOfMonth(d), dir)
    case 'year': return addMonths(startOfYear(d), 12 * dir)
    case 'agenda': return addDays(d, 30 * dir)
    case 'people': return opts.peopleSpan === 'day' ? addDays(d, dir) : addDays(startOfWeek(d, s?.week_start ?? 1), 7 * dir)
  }
}
