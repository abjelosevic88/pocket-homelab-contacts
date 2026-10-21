import { useState } from 'react'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { ago } from '../lib/contacts'
import type { Contact, Deleted } from '../lib/types'
import { Icon } from '../components/icons'
import { PageHead } from '../components/PageHead'
import { Confirm, errMsg, useAsync, useToast } from '../components/ui'

export default function DeletedPage({ onMenu, onOpen }: { onMenu?: () => void; onOpen: (id: number) => void }) {
  const { version, books, bump, reloadBooks, reloadCategories } = useApp()
  const { toast } = useToast()
  const list = useAsync(() => api.get<Deleted[]>(`${V1}/deleted`), [version])
  const [forget, setForget] = useState<Deleted | 'all' | null>(null)
  const items = (list.data || []).slice().sort((a, b) => b.deleted_at.localeCompare(a.deleted_at))
  const drop = (id: number) => list.setData(xs => (xs || []).filter(x => x.id !== id))

  const restore = async (d: Deleted) => {
    try {
      const c = await api.post<Contact>(`${V1}/deleted/${d.id}/restore`)
      drop(d.id); bump(); reloadBooks().catch(() => {}); reloadCategories()
      toast(`${d.fn || 'Contact'} is back`, 'ok', c?.id ? { label: 'Open', run: () => onOpen(c.id) } : undefined)
    } catch (e) { toast(errMsg(e), 'err') }
  }
  const remove = async (ds: Deleted[]) => {
    let failed = 0
    for (const d of ds) { try { await api.del(`${V1}/deleted/${d.id}`); drop(d.id) } catch { failed++ } }
    toast(failed ? `${failed} couldn’t be removed` : ds.length === 1 ? 'Gone for good' : `${ds.length} removed for good`, failed ? 'err' : 'ok')
  }

  return (
    <div className="page">
      <PageHead title="Recently deleted" icon="trash" onMenu={onMenu} sub="Deleted contacts stay here for 30 days, then they’re gone."
        actions={items.length > 1 ? <button type="button" className="btn ghost sm danger-ghost" onClick={() => setForget('all')}>Empty</button> : undefined} />
      <div className="page-scroll">
        <div className="page-inner narrow">
          {list.error ? <div className="callout danger"><Icon name="alert" size={15} /><span>{list.error}</span></div>
            : !list.data ? <div className="stack tight">{[0, 1, 2].map(i => <span key={i} className="skeleton" style={{ height: 52 }} />)}</div>
            : !items.length ? <div className="empty"><div className="empty-ico"><Icon name="trash" size={22} /></div><h3>Nothing deleted</h3><div className="muted">Contacts you delete here or on a phone wait here for 30 days in case you change your mind.</div></div>
            : (
              <div className="del-list">
                {items.map(d => {
                  const book = books.find(b => b.id === d.addressbook_id)
                  return (
                    <div key={d.id} className="del-row">
                      <span className="del-ico"><Icon name="user" size={16} /></span>
                      <div className="grow" style={{ minWidth: 0 }}>
                        <div className="del-name ellipsis">{d.fn || 'No name'}</div>
                        <div className="muted small row">{book && <><span className="dot" style={{ background: book.color, width: 8, height: 8 }} />{book.name} ·</>} deleted {ago(d.deleted_at)}</div>
                      </div>
                      <button type="button" className="btn sm" onClick={() => restore(d)}><Icon name="undo" size={14} />Restore</button>
                      <button type="button" className="btn ghost icon-only sm" title="Delete for good" aria-label="Delete for good" onClick={() => setForget(d)}><Icon name="x" size={15} /></button>
                    </div>
                  )
                })}
              </div>
            )}
        </div>
      </div>
      {forget && <Confirm title={forget === 'all' ? `Delete all ${items.length} for good?` : `Delete ${forget.fn || 'this contact'} for good?`} confirmLabel="Delete for good"
        message="This can’t be undone." onCancel={() => setForget(null)} onConfirm={() => { const f = forget; setForget(null); remove(f === 'all' ? items : [f]) }} />}
    </div>
  )
}
