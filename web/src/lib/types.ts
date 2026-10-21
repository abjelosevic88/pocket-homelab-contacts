// Shapes of the Pocket Contacts API (see docs/api.md).

export interface User {
  id: number
  email: string
  name: string
  username: string
  role: 'admin' | 'member'
  created_at: string
}

export interface AuthStatus {
  needs_setup: boolean
  core_configured?: boolean
  auth_mode: string
  authenticated: boolean
  user: User | null
  version: string
  title: string
  oidc?: boolean
  oidc_name?: string
}

export type ViewName = 'day' | '3day' | 'week' | 'month' | 'year' | 'agenda' | 'people'

export interface Settings {
  title: string
  timezone: string
  week_start: number
  time_format: '24h' | '12h'
  default_view: ViewName
  default_calendar_id: number
  default_duration_min: number
  default_alarms: string[]
  all_day_alarms: string[]
  work_start: number
  work_end: number
  work_days: number[]
  show_week_numbers: boolean
  show_declined: boolean
  show_weekends: boolean
  density: 'comfortable' | 'compact'
  holiday_country: string
  freebusy_shared: boolean
  invites_color: string
  notify_kind: 'apprise' | 'ntfy' | 'webhook'
  notify_url: string
  notify_alarms: boolean
  notify_invites: boolean
  digest_time: string
  overlays: Record<string, string>
  overlay_colors: Record<string, string>
  overlay_urls: Record<string, string>
  overlay_tokens: Record<string, boolean>
}

export interface CoreStatus { configured: boolean; url: string; registered: boolean; error: string }

export interface SettingsResponse {
  settings: Settings
  effective_tz: string
  box_tz: string
  box_holidays: string
  notify_has_token: boolean
  notify_box_route: boolean
  google_configured: boolean
  core: CoreStatus
}

export interface Person { id: number; name: string; email: string }

export interface Share {
  id: number
  calendar_id: number
  user_id: number | null
  email?: string
  everyone: boolean
  role: Role
  person?: Person
  pending: boolean
  created_at: string
}

export type Role = 'owner' | 'manage' | 'write' | 'read' | 'freebusy'

export interface Calendar {
  id: number
  uid: string
  name: string
  color: string
  calendar_color: string
  description: string
  timezone: string
  kind: 'local' | 'caldav' | 'google' | 'ics' | 'nextcloud'
  account_id: number | null
  read_only: boolean
  writable: boolean
  is_default: boolean
  default_alarms: string[]
  role: Role
  visible: boolean
  notify: boolean
  owner: Person
  events: number
  sort_order: number
  feed_url?: string
  caldav_url?: string
  shares?: Share[]
}

export interface Alarm { trigger: string; description?: string }

export type PartStat = 'ACCEPTED' | 'DECLINED' | 'TENTATIVE' | 'NEEDS-ACTION' | 'DELEGATED'

export interface Attendee {
  email: string
  name?: string
  role?: string
  status?: PartStat
  user_id?: number
  me?: boolean
}

export interface Occurrence {
  id: string
  event_id: number
  key: string
  uid: string
  calendar_id: number
  title: string
  description?: string
  location?: string
  url?: string
  conference?: string
  start: string
  end: string
  all_day: boolean
  tz?: string
  status: 'confirmed' | 'tentative' | 'cancelled'
  busy: boolean
  private: boolean
  color?: string
  categories: string[]
  recurring: boolean
  rrule?: string
  repeat_text?: string
  override: boolean
  alarms: Alarm[]
  organizer?: { email: string; name?: string }
  attendees: Attendee[]
  invite?: { status: PartStat }
  can_edit: boolean
  masked: boolean
  source?: string
  created_by?: Person
  updated_at?: string
  etag?: string
}

export interface EventsResponse { events: Occurrence[]; from: string; to: string; tz: string }

export interface EventDetail { event: Occurrence; role: Role | ''; calendar?: { id: number; name: string; color: string; owner: Person } }

export interface EventBody {
  calendar_id?: number
  title?: string
  description?: string
  location?: string
  url?: string
  conference?: string
  all_day?: boolean
  start?: string
  end?: string
  tz?: string
  rrule?: string
  status?: string
  busy?: boolean
  private?: boolean
  color?: string
  categories?: string[]
  alarms?: Alarm[]
  attendees?: Attendee[]
}

export type Scope = 'this' | 'following' | 'all'

export type OverlayKind = 'task' | 'due' | 'done' | 'trip' | 'service' | 'invoice' | 'habit' | 'review' | 'holiday'

export interface OverlayItem {
  id: string
  source: string
  ref: string
  kind: OverlayKind
  title: string
  subtitle?: string
  start: string
  end: string
  all_day: boolean
  color: string
  url?: string
  done: boolean
  overdue?: boolean
  actions: string[]
  meta?: Record<string, unknown>
}

export interface OverlaysResponse { items: OverlayItem[]; errors: Record<string, string> }

export interface OverlaySource {
  id: string
  name: string
  app: string
  description: string
  mode: 'core' | 'direct' | 'off'
  status: 'ok' | 'error' | 'off'
  detail: string
  color: string
  default_color: string
  url: string
  has_token: boolean
  pocket: boolean
  actions: string[] | null
}

export interface PeopleEntry { id: number; name: string; email: string; me: boolean; freebusy: boolean }

export interface BusyBlock { start: string; end: string; all_day: boolean }

export interface QuickParsed {
  title: string
  start: string
  end: string
  all_day: boolean
  rrule: string
  repeat_text: string
  location: string
  calendar_id: number
  calendar: string
  alarms: string[] | null
  attendees: Attendee[]
  recognized: string[] | null
  has_date?: boolean
  has_time?: boolean
  private?: boolean
}

export interface Account {
  id: number
  kind: 'caldav' | 'google' | 'ics' | 'nextcloud'
  name: string
  url: string
  username: string
  via: 'direct' | 'core' | ''
  sync_minutes: number
  enabled: boolean
  last_sync_at: string
  last_error: string
  has_secret: boolean
  calendars: number
}

export interface Feeds { all_url: string; all_webcal: string; caldav_url: string; caldav_user: string; principal: string }

export interface APIToken { id: number; name: string; prefix: string; last_used_at: string | null; created_at: string }

export interface SystemInfo {
  version: string
  go: string
  uptime: string
  counts: Record<string, number>
  core: CoreStatus
  auth_mode: string
  base_url: string
  timezone: string
  db_size?: number
  users?: number
}
