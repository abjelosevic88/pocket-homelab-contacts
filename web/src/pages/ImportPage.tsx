import { useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { api, qs, upload, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import type { ImportResult } from '../lib/types'
import { bookOptions } from '../components/BookDialogs'
import { Icon } from '../components/icons'
import { PageHead } from '../components/PageHead'
import { Select } from '../components/Select'
import { errMsg, Field, Segmented, Spinner, useToast } from '../components/ui'

type Dupes = 'merge' | 'skip' | 'add'
const DUPES: { value: Dupes; label: string; help: string }[] = [
  { value: 'merge', label: 'Merge', help: 'Someone already here (same UID, email, phone or full name) gets the new details added. Nothing is lost.' },
  { value: 'skip', label: 'Skip', help: 'Leave the people you already have alone; only new people are added.' },
  { value: 'add', label: 'Add all', help: 'Add every card as a new contact, even if it looks like someone you have. Clean up later under Duplicates.' },
]

const fmtSize = (n: number) => n < 1024 ? `${n} B` : n < 1048576 ? `${(n / 1024).toFixed(0)} KB` : `${(n / 1048576).toFixed(1)} MB`

export default function ImportPage({ onMenu }: { onMenu?: () => void; onOpen: (id: number) => void }) {
  const { books, settings, bump, reloadBooks, reloadCategories } = useApp()
  const { toast } = useToast()
  const navigate = useNavigate()
  const opts = bookOptions(books)
  const def = (settings?.default_addressbook_id && opts.some(o => o.value === settings.default_addressbook_id) ? settings.default_addressbook_id : 0) || books.find(b => b.is_default && opts.some(o => o.value === b.id))?.id || opts[0]?.value || 0
  const [source, setSource] = useState<'files' | 'paste'>('files')
  const [files, setFiles] = useState<File[]>([])
  const [text, setText] = useState('')
  const [book, setBook] = useState<number>(def)
  const [dupes, setDupes] = useState<Dupes>('merge')
  const [over, setOver] = useState(false)
  const [busy, setBusy] = useState<'preview' | 'import' | null>(null)
  const [preview, setPreview] = useState<ImportResult | null>(null)
  const [done, setDone] = useState<ImportResult | null>(null)
  const input = useRef<HTMLInputElement>(null)

  const ready = !!book && (source === 'files' ? files.length > 0 : !!text.trim())
  const reset = () => { setPreview(null); setDone(null) }
  const addFiles = (fl: FileList | null) => {
    if (!fl) return
    const ok = [...fl].filter(f => /\.(vcf|vcard|csv|zip)$/i.test(f.name) || /vcard|csv|zip/.test(f.type))
    if (ok.length < fl.length) toast('Only .vcf, .csv and .zip files', 'err')
    setFiles(fs => [...fs, ...ok.filter(f => !fs.some(x => x.name === f.name && x.size === f.size))])
    reset()
  }

  const send = async (dry: boolean): Promise<ImportResult> => {
    const fields = { addressbook_id: String(book), duplicates: dupes, ...(dry ? { dry_run: '1' } : {}) }
    if (source === 'files') return upload<ImportResult>(`${V1}/import`, 'file', files, fields)
    return api.post<ImportResult>(`${V1}/import${qs(fields)}`, { text, addressbook_id: book, duplicates: dupes, ...(dry ? { dry_run: true } : {}) })
  }
  const run = async (dry: boolean) => {
    setBusy(dry ? 'preview' : 'import')
    try {
      const r = await send(dry)
      if (dry) setPreview(r)
      else { setDone(r); setPreview(null); bump(); reloadBooks().catch(() => {}); reloadCategories() }
    } catch (e) { toast(errMsg(e), 'err') } finally { setBusy(null) }
  }
  const bookName = books.find(b => b.id === book)?.name || 'the address book'

  if (done) {
    return (
      <div className="page">
        <PageHead title="Import" icon="upload" onMenu={onMenu} />
        <div className="page-scroll"><div className="page-inner narrow stack loose">
          <div className="import-done">
            <span className={`import-ok ${done.failed ? 'warn' : ''}`}><Icon name={done.failed ? 'alert' : 'check'} size={22} /></span>
            <div><h2>{done.created + done.merged > 0 ? `Imported into ${bookName}` : 'Nothing new to import'}</h2><p className="muted" style={{ margin: 0 }}>{done.total} card{done.total === 1 ? '' : 's'} read.</p></div>
          </div>
          <Counts r={done} />
          {!!done.errors?.length && <ErrorList errors={done.errors} />}
          <div className="row wrap">
            <button type="button" className="btn primary" onClick={() => navigate('/')}><Icon name="contact" size={15} />Show contacts</button>
            {done.merged + done.created > 1 && <button type="button" className="btn" onClick={() => navigate('/duplicates')}><Icon name="merge" size={15} />Check for duplicates</button>}
            <button type="button" className="btn ghost" onClick={() => { setDone(null); setFiles([]); setText('') }}>Import more</button>
          </div>
        </div></div>
      </div>
    )
  }

  return (
    <div className="page">
      <PageHead title="Import" icon="upload" onMenu={onMenu} sub="vCard (.vcf) from any phone or app, CSV from Google, Outlook or a spreadsheet, or a .zip of them." />
      <div className="page-scroll"><div className="page-inner narrow stack loose">
        <section className="stack">
          <Segmented value={source} onChange={v => { setSource(v); reset() }} label="Source" options={[{ value: 'files', label: <span className="row"><Icon name="folder" size={14} />Files</span> }, { value: 'paste', label: <span className="row"><Icon name="text" size={14} />Paste</span> }]} />
          {source === 'files' ? (
            <>
              <button type="button" className={`dropzone ${over ? 'over' : ''}`} onClick={() => input.current?.click()}
                onDragOver={e => { e.preventDefault(); setOver(true) }} onDragLeave={() => setOver(false)} onDrop={e => { e.preventDefault(); setOver(false); addFiles(e.dataTransfer.files) }}>
                <Icon name="upload" size={26} />
                <b>Drop files here or click to choose</b>
                <span className="muted small">.vcf · .csv · .zip — as many as you like</span>
              </button>
              <input ref={input} type="file" multiple accept=".vcf,.vcard,.csv,.zip,text/vcard,text/csv,application/zip" hidden onChange={e => { addFiles(e.target.files); e.target.value = '' }} />
              {files.length > 0 && (
                <ul className="file-list">
                  {files.map((f, i) => (
                    <li key={f.name + i}><Icon name={/\.zip$/i.test(f.name) ? 'folder' : /\.csv$/i.test(f.name) ? 'grid' : 'contact'} size={15} /><span className="grow ellipsis">{f.name}</span><span className="muted small tnum">{fmtSize(f.size)}</span>
                      <button type="button" className="btn ghost icon-only xs" aria-label={`Remove ${f.name}`} onClick={() => { setFiles(fs => fs.filter((_, j) => j !== i)); reset() }}><Icon name="x" size={13} /></button></li>
                  ))}
                </ul>
              )}
            </>
          ) : (
            <textarea className="paste" value={text} onChange={e => { setText(e.target.value); reset() }} rows={9} spellCheck={false} placeholder={'BEGIN:VCARD\nVERSION:3.0\nFN:Marko Petrović\nTEL;TYPE=CELL:+387 65 123 456\nEND:VCARD'} />
          )}
        </section>

        <section className="set-section">
          <div className="set-section-body stack">
            <Field label="Into the address book" group>
              {opts.length ? <Select value={book} options={opts} onChange={v => { setBook(v); reset() }} label="Address book" /> : <span className="danger-text small">There’s no address book you may add to.</span>}
            </Field>
            <Field label="People you already have" group help={DUPES.find(d => d.value === dupes)?.help}>
              <Segmented value={dupes} onChange={v => { setDupes(v); reset() }} label="Duplicates" options={DUPES.map(d => ({ value: d.value, label: d.label }))} />
            </Field>
          </div>
        </section>

        {preview && (
          <section className="stack">
            <h2 className="import-h">Preview</h2>
            <Counts r={preview} dry />
            {!!preview.errors?.length && <ErrorList errors={preview.errors} />}
            {!!preview.preview?.length && (
              <div className="preview-wrap">
                <table className="table preview">
                  <thead><tr><th>Name</th><th>Email / phone</th><th className="right">What happens</th></tr></thead>
                  <tbody>
                    {preview.preview.map((r, i) => (
                      <tr key={i}>
                        <td className="ellipsis" style={{ maxWidth: 220 }}>{r.fn || <span className="faint">No name</span>}</td>
                        <td className="muted ellipsis" style={{ maxWidth: 260 }}>{[...(r.emails || []), ...(r.phones || [])].slice(0, 2).join(' · ')}</td>
                        <td className="right"><span className={`badge ${r.action === 'create' ? 'ok' : r.action === 'merge' ? 'accent-soft' : ''}`}>{r.action === 'create' ? 'New' : r.action === 'merge' ? 'Merge' : 'Skip'}</span></td>
                      </tr>
                    ))}
                  </tbody>
                </table>
                {preview.total > preview.preview.length && <p className="muted small" style={{ margin: '8px 0 0' }}>Showing the first {preview.preview.length} of {preview.total}.</p>}
              </div>
            )}
          </section>
        )}

        <div className="row wrap import-actions">
          {!preview ? (
            <button type="button" className="btn primary" disabled={!ready || !!busy} onClick={() => run(true)}>{busy === 'preview' ? <Spinner /> : <Icon name="eye" size={15} />}Preview</button>
          ) : (
            <>
              <button type="button" className="btn primary" disabled={!ready || !!busy || preview.created + preview.merged === 0} onClick={() => run(false)}>
                {busy === 'import' ? <Spinner /> : <Icon name="upload" size={15} />}Import {preview.created + preview.merged} contact{preview.created + preview.merged === 1 ? '' : 's'}
              </button>
              <button type="button" className="btn ghost" onClick={reset}>Back</button>
            </>
          )}
          {!preview && <button type="button" className="btn ghost" disabled={!ready || !!busy} onClick={() => run(false)}>Import without preview</button>}
        </div>
        <p className="muted small" style={{ margin: 0 }}>Old vCard 2.1 files (Nokia, old Androids, Windows-1250 Cyrillic/Latin) are converted; photos come along. Importing the same file twice doesn’t create doubles unless you pick “Add all”.</p>
      </div></div>
    </div>
  )
}

function Counts({ r, dry }: { r: ImportResult; dry?: boolean }) {
  const cells: [string, number, string][] = [
    [dry ? 'New' : 'Added', r.created, 'ok'], [dry ? 'To merge' : 'Merged', r.merged, 'accent'], [dry ? 'To skip' : 'Skipped', r.skipped, ''], ['Failed', r.failed, r.failed ? 'danger' : ''],
  ]
  return (
    <div className="import-counts">
      {cells.map(([l, n, k]) => <div key={l} className={`import-count ${k}`}><span className="tnum">{n}</span><span>{l}</span></div>)}
    </div>
  )
}

function ErrorList({ errors }: { errors: string[] }) {
  return (
    <details className="import-errors" open={errors.length <= 5}>
      <summary><Icon name="alert" size={14} />{errors.length} problem{errors.length === 1 ? '' : 's'}</summary>
      <ul>{errors.slice(0, 100).map((e, i) => <li key={i}>{e}</li>)}</ul>
    </details>
  )
}
