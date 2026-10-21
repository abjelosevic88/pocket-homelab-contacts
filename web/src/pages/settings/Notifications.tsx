import { useEffect, useState } from 'react'
import { api, V1 } from '../../lib/api'
import { useApp } from '../../lib/app-context'
import type { Settings } from '../../lib/types'
import { Icon } from '../../components/icons'
import { type Option, Select } from '../../components/Select'
import { errMsg, Field, Loading, Segmented, SettingRow, Spinner, Switch, useToast } from '../../components/ui'
import { Section, useSaveSettings } from './common'

const KINDS: { value: Settings['notify_kind']; label: string; help: string; placeholder: string; token: string }[] = [
  { value: 'apprise', label: 'Apprise', help: 'An Apprise API route — it fans out to ntfy, Pushover, Telegram, email and 100+ others.', placeholder: 'https://apprise.example.com/notify/contacts', token: 'Usually not needed' },
  { value: 'ntfy', label: 'ntfy', help: 'An ntfy topic URL. Install the ntfy app and subscribe to the same topic.', placeholder: 'https://ntfy.sh/my-birthdays', token: 'Access token for a protected topic (optional)' },
  { value: 'webhook', label: 'Webhook', help: 'A JSON POST with title, body and link to any URL you choose.', placeholder: 'https://example.com/hooks/contacts', token: 'Sent as a Bearer token (optional)' },
]

const TIMES: Option<string>[] = Array.from({ length: 36 }, (_, i) => {
  const m = 5 * 60 + i * 30
  const v = `${String(Math.floor(m / 60)).padStart(2, '0')}:${String(m % 60).padStart(2, '0')}`
  return { value: v, label: v }
})
const AHEAD: Option<number>[] = [0, 1, 2, 3, 7, 14].map(n => ({ value: n, label: n === 0 ? 'Only on the day' : n === 1 ? 'And the day before' : `And ${n} days before` }))

export function NotificationsTab() {
  const { sr, settings } = useApp()
  const save = useSaveSettings()
  const { toast } = useToast()
  const [url, setUrl] = useState(settings?.notify_url || '')
  const [token, setToken] = useState('')
  const [testing, setTesting] = useState(false)
  useEffect(() => { setUrl(settings?.notify_url || '') }, [settings?.notify_url])
  if (!sr || !settings) return <Loading />
  const s = settings
  const kind = KINDS.find(k => k.value === s.notify_kind) || KINDS[0]
  const dirty = url !== (s.notify_url || '') || token !== ''
  const time = s.birthday_time || '08:00'
  const times = TIMES.some(t => t.value === time) ? TIMES : [{ value: time, label: time }, ...TIMES]
  const ahead = AHEAD.some(a => a.value === s.birthday_ahead) ? AHEAD : [...AHEAD, { value: s.birthday_ahead, label: `And ${s.birthday_ahead} days before` }]

  const saveRoute = async () => {
    const patch: Partial<Settings> & { notify_token?: string } = { notify_url: url.trim() }
    if (token) patch.notify_token = token
    if (await save(patch, 'Notification address saved')) setToken('')
  }
  const test = async () => {
    setTesting(true)
    try { await api.post(`${V1}/notify/test`); toast('Test sent — check your phone') } catch (e) { toast(errMsg(e), 'err') } finally { setTesting(false) }
  }

  return (
    <div className="stack loose">
      <Section title="Birthday reminders" sub="A short message on the morning of a birthday, so nobody is forgotten.">
        <SettingRow title="Remind me of birthdays" help="Everyone with a birthday in the address books you show under All contacts.">
          <Switch label="Remind me of birthdays" checked={!!s.birthday_notify} onChange={v => save({ birthday_notify: v })} />
        </SettingRow>
        <SettingRow title="At" help={`The box’s time zone${sr.box_tz ? ` (${sr.box_tz})` : ''}.`}>
          <Select value={time} options={times} onChange={v => save({ birthday_time: v })} label="Reminder time" disabled={!s.birthday_notify} className="set-time-select" />
        </SettingRow>
        <SettingRow title="Days ahead" help="A heads-up in time to buy a present.">
          <Select value={s.birthday_ahead || 0} options={ahead} onChange={v => save({ birthday_ahead: v })} label="Days ahead" disabled={!s.birthday_notify} />
        </SettingRow>
        <SettingRow title="Anniversaries too" help="Wedding and other anniversaries saved on a contact.">
          <Switch label="Anniversaries too" checked={!!s.anniversary_note} onChange={v => save({ anniversary_note: v })} disabled={!s.birthday_notify} />
        </SettingRow>
      </Section>

      <Section title="Where to send" sub="Reminders are pushed from the server, so they arrive even when no app is open.">
        {sr.notify_box_route && <div className="callout info mb"><Icon name="info" size={15} /><span>This box already has a notification route set by the admin (the Apprise route <code>contacts</code>). It is used for you until you set your own address here.</span></div>}
        <div className="stack">
          <Segmented label="Service" value={s.notify_kind || 'apprise'} onChange={v => save({ notify_kind: v })} options={KINDS.map(k => ({ value: k.value, label: k.label }))} />
          <p className="muted small" style={{ margin: 0 }}>{kind.help}</p>
          <Field label="Address"><input value={url} onChange={e => setUrl(e.target.value)} placeholder={kind.placeholder} inputMode="url" autoComplete="off" spellCheck={false} /></Field>
          <Field label="Token" help={kind.token}><input type="password" value={token} onChange={e => setToken(e.target.value)} placeholder={sr.notify_has_token ? '•••• saved — type to replace' : 'Optional'} autoComplete="new-password" /></Field>
          <div className="row wrap">
            <button type="button" className="btn primary" disabled={!dirty} onClick={saveRoute}>Save</button>
            <button type="button" className="btn" disabled={testing || (!s.notify_url && !sr.notify_box_route)} onClick={test}>{testing ? <Spinner /> : <Icon name="bell" size={14} />}Send a test</button>
            {sr.notify_has_token && <button type="button" className="btn ghost sm" onClick={() => save({ notify_token: '' }, 'Token removed')}>Remove token</button>}
          </div>
        </div>
      </Section>
    </div>
  )
}
