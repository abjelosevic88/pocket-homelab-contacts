import { useState } from 'react'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { displayName, MONTHS, ordinalAge } from '../lib/contacts'
import type { Birthday } from '../lib/types'
import { ContactAvatar } from '../components/ContactAvatar'
import { Icon } from '../components/icons'
import { PageHead } from '../components/PageHead'
import { Confirm, CopyField, errMsg, useAsync, useToast } from '../components/ui'

const WD = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat']

export default function BirthdaysPage({ onMenu, onOpen }: { onMenu?: () => void; onOpen: (id: number) => void }) {
  const { version, settings } = useApp()
  const list = useAsync(() => api.get<Birthday[]>(`${V1}/birthdays?days=366`), [version])
  const items = list.data || []
  const groups: { key: string; label: string; items: Birthday[] }[] = []
  const thisYear = new Date().getFullYear()
  for (const b of items) {
    const [y, m] = b.date.split('-').map(Number)
    const key = `${y}-${m}`
    let g = groups[groups.length - 1]
    if (!g || g.key !== key) { g = { key, label: `${MONTHS[m - 1]}${y !== thisYear ? ` ${y}` : ''}`, items: [] }; groups.push(g) }
    g.items.push(b)
  }
  const soon = items.filter(b => b.days <= 7).length

  return (
    <div className="page">
      <PageHead title="Birthdays" icon="cake" onMenu={onMenu} sub={list.data ? (items.length ? `${items.length} in the next 12 months${soon ? ` · ${soon} this week` : ''}` : 'Nobody’s birthday is saved yet') : undefined} />
      <div className="page-scroll">
        <div className="page-inner bday-layout">
          <div className="bday-main">
            {list.error ? <div className="callout danger"><Icon name="alert" size={15} /><span>{list.error}</span></div>
              : !list.data ? <BdaySkeleton />
              : !items.length ? (
                <div className="empty"><div className="empty-ico"><Icon name="cake" size={22} /></div><h3>No birthdays yet</h3><div className="muted">Add a birthday to a contact — with or without the year — and it shows up here, on your phone and in Pocket Calendar.</div></div>
              ) : groups.map(g => (
                <section key={g.key} className="bday-month">
                  <h2 className="bday-month-h">{g.label}</h2>
                  <ul className="bday-list">
                    {g.items.map(b => {
                      const d = new Date(b.date + 'T00:00:00')
                      const hot = b.days <= 1
                      const what = b.kind === 'anniversary'
                        ? (b.age != null && b.age > 0 ? `${ordinalAge(b.age)} anniversary` : 'Anniversary')
                        : (b.age != null ? `Turns ${b.age}` : 'Birthday')
                      return (
                        <li key={`${b.contact.id}-${b.kind}`}>
                          <button type="button" className={`bday-row ${hot ? 'hot' : ''} ${b.days === 0 ? 'today' : ''}`} onClick={() => onOpen(b.contact.id)}>
                            <span className="bday-date"><span className="bday-day tnum">{d.getDate()}</span><span className="bday-wd">{WD[d.getDay()]}</span></span>
                            <span className="bday-face">
                              <ContactAvatar c={b.contact} size={38} />
                              <span className={`bday-badge ${b.kind}`}><Icon name={b.kind === 'anniversary' ? 'heart' : 'cake'} size={10} strokeWidth={2.2} /></span>
                            </span>
                            <span className="grow bday-text">
                              <span className="bday-name ellipsis">{displayName(b.contact, settings?.display_order)}</span>
                              <span className="bday-what">{what}</span>
                            </span>
                            <span className={`bday-when ${hot ? 'hot' : ''}`}>{b.days === 0 ? 'Today' : b.days === 1 ? 'Tomorrow' : `in ${b.days} days`}</span>
                          </button>
                        </li>
                      )
                    })}
                  </ul>
                </section>
              ))}
          </div>
          <aside className="bday-side"><FeedCard /></aside>
        </div>
      </div>
    </div>
  )
}

function FeedCard() {
  const { toast } = useToast()
  const feeds = useAsync(() => api.get<{ birthdays_url: string; birthdays_webcal: string }>(`${V1}/feeds`), [])
  const [rotate, setRotate] = useState(false)
  return (
    <section className="set-section feed-card">
      <div className="set-section-head"><div className="grow"><h3><Icon name="calendar" size={15} /> Subscribe in your calendar</h3><p>Every birthday and anniversary as a yearly all-day event — in Apple Calendar, Google Calendar, Outlook or Pocket Calendar.</p></div></div>
      <div className="set-section-body stack tight">
        {feeds.error ? <p className="danger-text small">{feeds.error}</p> : !feeds.data ? <span className="skeleton" style={{ height: 34 }} /> : <>
          <CopyField value={feeds.data.birthdays_url} label="Birthday calendar address" />
          <div className="row wrap">
            <a className="btn primary sm" href={feeds.data.birthdays_webcal}><Icon name="calendar-plus" size={14} />Subscribe</a>
            <button type="button" className="btn ghost sm" onClick={() => setRotate(true)}><Icon name="refresh" size={13} />New address</button>
          </div>
          <p className="muted xsmall" style={{ margin: 0 }}>Anyone with the address sees the names and dates — keep it to yourself.</p>
        </>}
      </div>
      {rotate && <Confirm title="Make a new address?" confirmLabel="Make new address" danger message="The current address stops working; calendars subscribed to it stop updating until you subscribe again."
        onCancel={() => setRotate(false)} onConfirm={async () => { setRotate(false); try { await api.post(`${V1}/feeds/rotate`); await feeds.reload(); toast('New address made — the old one stopped working') } catch (e) { toast(errMsg(e), 'err') } }} />}
    </section>
  )
}

function BdaySkeleton() {
  return <div className="stack">{[0, 1, 2, 3, 4].map(i => <div key={i} className="bday-row skel"><span className="skeleton" style={{ width: 36, height: 36, borderRadius: 10 }} /><span className="skeleton" style={{ width: 38, height: 38, borderRadius: '50%' }} /><span className="skeleton" style={{ width: `${30 + i * 9}%`, height: 13 }} /></div>)}</div>
}
