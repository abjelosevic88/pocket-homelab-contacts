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

export type NotifyKind = 'apprise' | 'ntfy' | 'webhook'
export type Mode = 'core' | 'direct' | 'off'

export interface Settings {
  title: string
  sort_by: 'given' | 'family'
  display_order: 'given_first' | 'family_first'
  default_addressbook_id: number
  density: 'comfortable' | 'compact'
  phone_region: string
  notify_kind: NotifyKind
  notify_url: string
  birthday_notify: boolean
  birthday_ahead: number
  birthday_time: string
  anniversary_note: boolean
  integrations: Record<string, string> | null
  integration_urls: Record<string, string> | null
  integration_tokens: Record<string, boolean> | null
}

export interface CoreStatus { configured: boolean; url: string; registered: boolean; error: string }

export interface SettingsResponse {
  settings: Settings
  box_tz?: string
  notify_has_token: boolean
  notify_box_route: boolean
  core: CoreStatus
}

export interface Person { id: number; name: string; email: string }

export type Role = 'owner' | 'manage' | 'write' | 'read'
export type ShareRole = 'read' | 'write' | 'manage'

export interface Share {
  id: number
  addressbook_id: number
  user_id: number | null
  email?: string
  everyone: boolean
  role: ShareRole
  person?: Person | null
  pending: boolean
}

export interface AddressBook {
  id: number
  owner_id: number
  uid: string
  name: string
  color: string
  description: string
  kind: 'local' | 'carddav' | 'nextcloud' | string
  account_id: number | null
  read_only: boolean
  is_default: boolean
  sort_order: number
  role: Role
  visible: boolean
  owner?: Person
  contacts: number
  shares?: Share[] | null
  created_at: string
  updated_at: string
}

export interface Summary {
  id: number
  addressbook_id: number
  uid: string
  kind: 'individual' | 'org' | 'group'
  fn: string
  given: string
  family: string
  nickname: string
  org: string
  title: string
  emails: string[] | null
  phones: string[] | null
  birthday: string
  categories: string[] | null
  has_photo: boolean
  photo_url: string
  starred: boolean
  initials: string
  color: string
  updated_at: string
}

export interface CardField { label: string; value: string; pref?: boolean }
export interface CardAddress { label: string; street: string; ext?: string; pobox?: string; city: string; region: string; postcode: string; country: string; pref?: boolean }

export interface Card {
  kind: 'individual' | 'org' | 'group'
  fn: string
  prefix: string
  given: string
  middle: string
  family: string
  suffix: string
  nickname: string
  phonetic_given: string
  phonetic_family: string
  org: string
  department: string
  title: string
  emails: CardField[] | null
  phones: CardField[] | null
  addresses: CardAddress[] | null
  urls: CardField[] | null
  ims: CardField[] | null
  socials: CardField[] | null
  birthday: string
  anniversary: string
  dates: CardField[] | null
  related: CardField[] | null
  note: string
  categories: string[] | null
  members?: string[] | null
}

export interface Contact extends Summary {
  card: Card
  etag: string
  role: Role
  addressbook: { id: number; name: string; color: string; owner?: Person }
  source: string
  groups?: { uid: string; name: string }[] | null
  url: string
  created_at: string
}

export interface ContactList { contacts: Summary[] | null; total: number }

export interface RelatedEvent { id: string; title: string; start: string; end?: string; all_day: boolean; url: string; past: boolean }
export interface RelatedNote { id: string; title: string; snippet?: string; updated_at?: string; url: string }
export interface RelatedTask { id: string; title: string; due?: string; done: boolean; url: string }
export interface RelatedSection<T> { status: 'ok' | 'off' | 'error'; detail: string; app_url?: string; new_url?: string; items: T[] | null }
export interface Related {
  calendar?: RelatedSection<RelatedEvent>
  notes?: RelatedSection<RelatedNote>
  tasks?: RelatedSection<RelatedTask>
}

export interface Activity { id: number; addressbook_id: number | null; contact_id: number | null; action: string; summary: string; detail: string; created_at: string }

export interface Label { name: string; count: number }
export interface Group { uid: string; name: string; count: number; addressbook_id: number }
export interface Categories { labels: Label[] | null; groups: Group[] | null }

export interface Birthday { contact: Summary; kind: 'birthday' | 'anniversary'; date: string; original: string; age: number | null; days: number }

export interface DuplicateGroup { key: string; reason: string; contacts: Summary[] }

export interface Deleted { id: number; addressbook_id: number; uid: string; fn: string; deleted_at: string }

export interface ImportPreviewRow { fn: string; emails: string[] | null; phones: string[] | null; action: 'create' | 'merge' | 'skip'; match_id: number | null }
export interface ImportResult { total: number; created: number; merged: number; skipped: number; failed: number; errors: string[] | null; preview?: ImportPreviewRow[] | null }

export interface APIToken { id: number; name: string; prefix: string; last_used_at: string | null; created_at: string }

export interface Devices { carddav_url: string; username: string; ios_server: string; principal_url: string; tokens: APIToken[] | null }
export interface DeviceLink { token: string; profile_url: string; expires_in: number }

export interface Account {
  id: number
  kind: 'nextcloud' | 'carddav' | 'icloud' | string
  name: string
  url: string
  username: string
  via: 'core' | 'direct'
  mode: 'import' | 'two_way' | 'read_only' | 'off'
  enabled: boolean
  sync_minutes: number
  last_sync_at: string
  last_error: string
  has_secret: boolean
  addressbooks: number
}

export interface Integration {
  id: 'calendar' | 'notes' | 'tasks' | string
  name: string
  description: string
  scopes: string[] | null
  mode: Mode
  url: string
  has_token: boolean
  status: 'ok' | 'off' | 'error' | ''
  detail: string
  public_url: string
}

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
