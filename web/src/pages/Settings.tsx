import { useEffect } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { useApp } from '../lib/app-context'
import { ErrorBoundary } from '../components/ErrorBoundary'
import { Icon } from '../components/icons'
import { Tabs, useIsMobile } from '../components/ui'
import { GeneralTab } from './settings/General'
import { DevicesTab } from './settings/Devices'
import { AccountsTab } from './settings/Accounts'
import { SharingTab } from './settings/Sharing'
import { AppsTab } from './settings/Apps'
import { NotificationsTab } from './settings/Notifications'
import { ProfileTab } from './settings/Profile'
import { TokensTab } from './settings/Tokens'
import { PeopleTab } from './settings/People'
import { AboutTab } from './settings/About'

type TabId = 'general' | 'devices' | 'accounts' | 'sharing' | 'apps' | 'notifications' | 'profile' | 'tokens' | 'users' | 'about'

const TABS: { id: TabId; label: string; icon: string; sub: string; admin?: boolean }[] = [
  { id: 'general', label: 'General', icon: 'settings', sub: 'How names are sorted and shown, and how the app looks.' },
  { id: 'devices', label: 'Phones & devices', icon: 'smartphone', sub: 'Your contacts on your iPhone, Android, Mac and in Thunderbird — kept in sync both ways.' },
  { id: 'accounts', label: 'Accounts & sync', icon: 'refresh', sub: 'Bring in contacts from Nextcloud, iCloud or any CardDAV server — once, or kept in sync.' },
  { id: 'sharing', label: 'Sharing', icon: 'users', sub: 'Who else on this box sees your address books, and what they may do.' },
  { id: 'apps', label: 'Apps', icon: 'layers', sub: 'Meetings, notes and to-dos about each person from your other Pocket apps.' },
  { id: 'notifications', label: 'Notifications', icon: 'bell', sub: 'Birthday reminders, and where they are sent.' },
  { id: 'profile', label: 'Profile', icon: 'user', sub: 'Your name, email, CardDAV user name and password.' },
  { id: 'tokens', label: 'Tokens', icon: 'key', sub: 'Personal tokens for the API and for CardDAV apps.' },
  { id: 'users', label: 'Users', icon: 'users', sub: 'Everyone who has an account on this box.', admin: true },
  { id: 'about', label: 'About', icon: 'info', sub: 'Version, numbers and the Pocket Core link.' },
]

export default function Settings() {
  const { tab: raw } = useParams()
  const navigate = useNavigate()
  const { user } = useApp()
  const mobile = useIsMobile()
  const isAdmin = user?.role === 'admin'
  const tabs = TABS.filter(t => !t.admin || isAdmin)
  const tab: TabId = (tabs.find(t => t.id === raw)?.id) || 'general'
  const current = tabs.find(t => t.id === tab)!
  const go = (id: TabId) => navigate(`/settings/${id}`, { replace: true })

  useEffect(() => { document.querySelector('.set-content')?.scrollTo({ top: 0 }) }, [tab])
  useEffect(() => { document.title = `Settings — ${current.label}` }, [current.label])

  return (
    <div className="set-page">
      <header className="set-header">
        <button type="button" className="btn ghost icon-only" aria-label="Back to contacts" title="Back to contacts" onClick={() => navigate('/')}><Icon name="chevron-left" size={18} /></button>
        <img src="/favicon.svg" alt="" width={24} height={24} className="set-logo" />
        <h1>Settings</h1>
      </header>
      {mobile && <div className="set-tabs-mobile"><Tabs tabs={tabs.map(t => ({ id: t.id, label: t.label, icon: t.icon }))} value={tab} onChange={go} /></div>}
      <div className="set-body">
        {!mobile && (
          <nav className="set-nav" aria-label="Settings sections">
            {tabs.map(t => (
              <button key={t.id} type="button" className={`set-nav-item ${t.id === tab ? 'active' : ''}`} aria-current={t.id === tab ? 'page' : undefined} onClick={() => go(t.id)}>
                <Icon name={t.icon} size={16} />{t.label}
              </button>
            ))}
          </nav>
        )}
        <main className="set-content">
          <div className="set-inner">
            <div className="set-title">
              <h2>{current.label}</h2>
              <p>{current.sub}</p>
            </div>
            <ErrorBoundary where={`settings-${tab}`} key={tab}>
              {tab === 'general' && <GeneralTab />}
              {tab === 'devices' && <DevicesTab goTokens={() => go('tokens')} goProfile={() => go('profile')} />}
              {tab === 'accounts' && <AccountsTab />}
              {tab === 'sharing' && <SharingTab />}
              {tab === 'apps' && <AppsTab />}
              {tab === 'notifications' && <NotificationsTab />}
              {tab === 'profile' && <ProfileTab />}
              {tab === 'tokens' && <TokensTab />}
              {tab === 'users' && <PeopleTab />}
              {tab === 'about' && <AboutTab />}
            </ErrorBoundary>
          </div>
        </main>
      </div>
    </div>
  )
}
