import { useEffect, useState } from 'react'
import { api, V1 } from '../../lib/api'
import { useApp } from '../../lib/app-context'
import type { AddressBook } from '../../lib/types'
import { BookDialog, ROLES, ShareDialog } from '../../components/BookDialogs'
import { Icon } from '../../components/icons'
import { Avatar, Confirm, Empty, errMsg, useToast } from '../../components/ui'
import { Section } from './common'

const roleText = (r: string) => ROLES.find(x => x.value === r)?.label || (r === 'owner' ? 'Owner' : r)

export function SharingTab() {
  const { books, user, reloadBooks, reloadPeople, bump } = useApp()
  const { toast } = useToast()
  const [share, setShare] = useState<AddressBook | null>(null)
  const [newBook, setNewBook] = useState(false)
  const [leave, setLeave] = useState<AddressBook | null>(null)
  useEffect(() => { reloadBooks().catch(() => {}); reloadPeople() }, [reloadBooks, reloadPeople])
  const mine = books.filter(b => b.role === 'owner' || b.role === 'manage')
  const withMe = books.filter(b => b.role !== 'owner')

  return (
    <div className="stack loose">
      <Section title="Your address books" sub="Share a whole address book with someone on this box — they see it in their list and on their phone. Contacts aren’t copied; everyone works on the same ones."
        actions={<button type="button" className="btn sm" onClick={() => setNewBook(true)}><Icon name="plus" size={14} />New address book</button>}>
        {!mine.length ? <Empty title="No address books you manage" icon="book" /> : (
          <div className="share-books">
            {mine.map(b => {
              const shares = b.shares || []
              return (
                <div key={b.id} className="share-book">
                  <span className="set-cal-dot" style={{ background: b.color }} />
                  <div className="grow" style={{ minWidth: 0 }}>
                    <div className="set-cal-name">{b.name}{b.role === 'manage' && <span className="muted small">· {b.owner?.name || b.owner?.email}’s</span>}{b.kind !== 'local' && <span className="badge">synced</span>}</div>
                    <div className="share-who">
                      {shares.length === 0 ? <span className="muted small">Only {b.role === 'owner' ? 'you' : 'you and the owner'}</span> : shares.map(s => (
                        <span key={s.id} className="share-pill" title={`${s.everyone ? 'Everyone on this box' : s.person?.name || s.email} — ${roleText(s.role)}${s.pending ? ' (pending)' : ''}`}>
                          {s.everyone ? <span className="who-ico"><Icon name="users" size={11} /></span> : <Avatar name={s.person?.name} email={s.person?.email || s.email} size={18} />}
                          <span className="ellipsis">{s.everyone ? 'Everyone' : s.person?.name?.split(' ')[0] || s.email}</span>
                          <span className="muted">{s.role === 'read' ? 'sees' : s.role === 'write' ? 'edits' : 'manages'}</span>
                        </span>
                      ))}
                    </div>
                  </div>
                  <span className="muted small tnum">{b.contacts}</span>
                  <button type="button" className="btn sm" onClick={() => setShare(b)}><Icon name="users" size={14} />Share</button>
                </div>
              )
            })}
          </div>
        )}
      </Section>

      <Section title="Shared with you" sub="Address books other people on this box share with you.">
        {!withMe.length ? <p className="muted small" style={{ margin: 0 }}>Nobody has shared an address book with you yet.</p> : (
          <div className="share-books">
            {withMe.map(b => (
              <div key={b.id} className="share-book">
                <span className="set-cal-dot" style={{ background: b.color }} />
                <div className="grow" style={{ minWidth: 0 }}>
                  <div className="set-cal-name">{b.name}</div>
                  <div className="muted small">{b.owner?.name || b.owner?.email} · you {b.role === 'read' ? 'can see it' : b.role === 'write' ? 'can edit it' : 'can edit and share it'} · {b.contacts} contacts</div>
                </div>
                {b.owner?.id !== user?.id && <button type="button" className="btn ghost sm danger-ghost" onClick={() => setLeave(b)}>Leave</button>}
              </div>
            ))}
          </div>
        )}
      </Section>

      {share && <ShareDialog book={share} onClose={() => { setShare(null); reloadBooks().catch(() => {}) }} />}
      {newBook && <BookDialog onClose={() => setNewBook(false)} />}
      {leave && <Confirm title={`Leave “${leave.name}”?`} confirmLabel="Leave" message={`It disappears from your list and your phone. ${leave.owner?.name || 'The owner'} keeps it and can share it again.`}
        onCancel={() => setLeave(null)} onConfirm={async () => { const b = leave; setLeave(null); try { await api.post(`${V1}/addressbooks/${b.id}/leave`); await reloadBooks(); bump(); toast(`You left “${b.name}”`) } catch (e) { toast(errMsg(e), 'err') } }} />}
    </div>
  )
}
