import { useEffect, useState } from 'react'
import { api, V1 } from '../../lib/api'
import { useApp } from '../../lib/app-context'
import { ago } from '../../lib/contacts'
import type { APIToken, DeviceLink, Devices } from '../../lib/types'
import { Icon } from '../../components/icons'
import { QRCode } from '../../components/QRCode'
import { Confirm, CopyField, Empty, errMsg, Field, Loading, Modal, Spinner, useAsync, useIsMobile, useToast } from '../../components/ui'
import { Section } from './common'

export function DevicesTab({ goTokens, goProfile }: { goTokens: () => void; goProfile: () => void }) {
  const { toast } = useToast()
  const dev = useAsync(() => api.get<Devices>(`${V1}/devices`), [])
  const [adding, setAdding] = useState<string | null>(null)
  const [revoke, setRevoke] = useState<APIToken | null>(null)
  const d = dev.data

  if (dev.error) return <div className="callout danger"><Icon name="alert" size={15} /><span>{dev.error}</span></div>
  if (!d) return <Loading />
  const tokens = d.tokens || []
  const host = (() => { try { return new URL(d.carddav_url).host } catch { return d.ios_server.split('/')[0] } })()

  return (
    <div className="stack loose">
      <Section title="Add a phone in one step" sub="Scan a code with the iPhone camera, allow the download and install the profile. Your contacts show up in the Contacts app, under the default account.">
        <div className="dev-add">
          <button type="button" className="dev-tile" onClick={() => setAdding('iPhone')}><span className="dev-ico ios"><Icon name="smartphone" size={20} /></span><b>Add iPhone</b><span className="muted small">QR code · 15 minutes</span></button>
          <button type="button" className="dev-tile" onClick={() => setAdding('iPad')}><span className="dev-ico ios"><Icon name="smartphone" size={20} /></span><b>Add iPad</b><span className="muted small">Same profile, other device</span></button>
          <button type="button" className="dev-tile" onClick={() => setAdding('Mac')}><span className="dev-ico mac"><Icon name="laptop" size={20} /></span><b>Add Mac</b><span className="muted small">Open the link on the Mac</span></button>
        </div>
      </Section>

      <Section title="Set it up by hand" sub="For Android, Thunderbird, or if you prefer typing the details in yourself.">
        <div className="set-howto">
          <details>
            <summary><Icon name="smartphone" size={16} />iPhone or iPad</summary>
            <ol>
              <li>Settings → Apps → Contacts → Contacts Accounts → Add Account → Other → <b>Add CardDAV Account</b>.</li>
              <li>Server: <code>{d.ios_server}</code></li>
              <li>User name: <code>{d.username}</code></li>
              <li>Password: a new device password — <ManualPassword name="iPhone" onMade={dev.reload} />.</li>
              <li>Tap Next. This server path makes the contacts show up under <b>Default Account</b>, not as a separate “card” account.</li>
            </ol>
          </details>
          <details>
            <summary><Icon name="laptop" size={16} />Mac</summary>
            <ol>
              <li>System Settings → Internet Accounts → Add Account → Add Other Account → <b>CardDAV account</b>.</li>
              <li>Account type: Manual. User name <code>{d.username}</code>, password: <ManualPassword name="Mac" onMade={dev.reload} />.</li>
              <li>Server address: <code>{host}</code></li>
            </ol>
          </details>
          <details>
            <summary><Icon name="android" size={16} />Android (DAVx⁵)</summary>
            <ol>
              <li>Install <a href="https://www.davx5.com/" target="_blank" rel="noreferrer">DAVx⁵</a> (Play Store or F-Droid), then tap + → <b>Login with URL and user name</b>.</li>
              <li>Base URL: <code>{d.carddav_url}</code></li>
              <li>User name <code>{d.username}</code>, password: <ManualPassword name="Android" onMade={dev.reload} />.</li>
              <li>Pick “Groups are per-contact categories” so labels match, then tick the address books to sync. They appear in the phone’s Contacts app.</li>
            </ol>
          </details>
          <details>
            <summary><Icon name="mail" size={16} />Thunderbird</summary>
            <ol>
              <li>Address Book → New Address Book → <b>Add CardDAV Address Book</b>.</li>
              <li>User name <code>{d.username}</code>, location <code>{d.carddav_url}</code></li>
              <li>Password: <ManualPassword name="Thunderbird" onMade={dev.reload} />. Thunderbird finds every address book you have.</li>
            </ol>
          </details>
          <details>
            <summary><Icon name="server" size={16} />Anything else that speaks CardDAV</summary>
            <ol>
              <li>Server <code>{d.carddav_url}</code> (or just <code>{host}</code> — it is found automatically).</li>
              <li>Principal <code>{d.principal_url}</code></li>
              <li>User name <code>{d.username}</code> (<button type="button" className="link-btn" onClick={goProfile}>change it in Profile</button>), password: a device password or a <button type="button" className="link-btn" onClick={goTokens}>token</button>.</li>
            </ol>
          </details>
        </div>
      </Section>

      <Section title="Devices with access" sub="Each phone or app has its own password. Revoke one and only that device stops syncing.">
        {!tokens.length ? <Empty title="No devices yet" icon="smartphone">Add your iPhone above — it takes a minute.</Empty> : (
          <div className="set-people">
            {tokens.map(t => (
              <div key={t.id} className="set-person">
                <span className="set-token-ico"><Icon name={/mac/i.test(t.name) ? 'laptop' : /android/i.test(t.name) ? 'android' : /thunderbird/i.test(t.name) ? 'mail' : 'smartphone'} size={15} /></span>
                <div className="grow">
                  <div className="set-person-name">{t.name.replace(/^Device:\s*/, '')}</div>
                  <div className="muted small">added {ago(t.created_at)} · {t.last_used_at ? `last synced ${ago(t.last_used_at)}` : 'not used yet'}</div>
                </div>
                <button type="button" className="btn ghost sm danger-ghost" onClick={() => setRevoke(t)}>Revoke</button>
              </div>
            ))}
          </div>
        )}
      </Section>

      {adding && <AddDevice name={adding} onClose={() => { setAdding(null); dev.reload() }} />}
      {revoke && <Confirm title={`Revoke “${revoke.name.replace(/^Device:\s*/, '')}”?`} confirmLabel="Revoke" message="That device stops syncing right away. Contacts already on it stay there until you remove the account on the device."
        onCancel={() => setRevoke(null)} onConfirm={async () => { const t = revoke; setRevoke(null); try { await api.del(`${V1}/devices/${t.id}`); dev.reload(); toast('Device revoked') } catch (e) { toast(errMsg(e), 'err') } }} />}
    </div>
  )
}

function ManualPassword({ name, onMade }: { name: string; onMade: () => void }) {
  const { toast } = useToast()
  const [token, setToken] = useState('')
  const [busy, setBusy] = useState(false)
  if (token) return <span className="dev-token"><CopyField value={token} label={`Password for ${name}`} /><span className="muted xsmall">Copy it now — you won’t see it again.</span></span>
  return <button type="button" className="btn sm" disabled={busy} onClick={async () => {
    setBusy(true)
    try { const r = await api.post<DeviceLink>(`${V1}/devices`, { name }); setToken(r.token); onMade() } catch (e) { toast(errMsg(e), 'err') } finally { setBusy(false) }
  }}>{busy ? <Spinner size={12} /> : <Icon name="key" size={13} />}Make a password</button>
}

function AddDevice({ name: initial, onClose }: { name: string; onClose: () => void }) {
  const { user } = useApp()
  const mobile = useIsMobile()
  const { toast } = useToast()
  const [name, setName] = useState(initial === 'iPhone' && user?.name ? `${user.name.split(' ')[0]}’s iPhone` : initial)
  const [link, setLink] = useState<DeviceLink | null>(null)
  const [busy, setBusy] = useState(false)
  const [until, setUntil] = useState(0)
  const [now, setNow] = useState(Date.now())
  useEffect(() => { if (!link) return; const t = setInterval(() => setNow(Date.now()), 1000); return () => clearInterval(t) }, [link])
  const left = Math.max(0, Math.round((until - now) / 1000))
  const create = async () => {
    setBusy(true)
    try { const r = await api.post<DeviceLink>(`${V1}/devices`, { name: name.trim() || initial }); setLink(r); setUntil(Date.now() + (r.expires_in || 900) * 1000); setNow(Date.now()) } catch (e) { toast(errMsg(e), 'err') } finally { setBusy(false) }
  }
  const mac = /mac/i.test(initial)
  return (
    <Modal title={link ? `Set up ${name}` : `Add ${initial}`} onClose={onClose} size={link ? 'lg' : 'sm'} footer={link ? <button type="button" className="btn primary" onClick={onClose}>Done</button> : <>
      <button type="button" className="btn" onClick={onClose}>Cancel</button>
      <button type="button" className="btn primary" onClick={create} disabled={busy}>{busy && <Spinner size={13} />}Continue</button>
    </>}>
      {!link ? (
        <form onSubmit={e => { e.preventDefault(); create() }} className="stack">
          <Field label="Name" help="So you can tell your devices apart — and revoke just this one later."><input value={name} onChange={e => setName(e.target.value)} autoFocus /></Field>
        </form>
      ) : (
        <div className="dev-setup">
          <div className="dev-qr">
            {left > 0 ? <QRCode value={link.profile_url} size={mobile ? 180 : 212} label="Scan with the iPhone camera" /> : <div className="qr expired"><Icon name="clock" size={22} /><span>Expired</span></div>}
            <div className={`dev-timer ${left < 60 ? 'late' : ''}`}><Icon name="clock" size={13} />{left > 0 ? <>Works for <b className="tnum">{Math.floor(left / 60)}:{String(left % 60).padStart(2, '0')}</b></> : <>The link expired. <button type="button" className="link-btn" onClick={create}>Make a new one</button></>}</div>
          </div>
          <div className="dev-steps">
            <ol>
              {mac ? <>
                <li>On the Mac, open the link below in Safari (or AirDrop it there).</li>
                <li>Open <b>System Settings → Privacy & Security → Profiles</b> (or “Profile Downloaded” at the top of System Settings).</li>
                <li>Double-click the profile and click <b>Install</b>.</li>
              </> : <>
                <li>Point the {/ipad/i.test(initial) ? 'iPad' : 'iPhone'} camera at the code and tap the link that pops up.</li>
                <li>Tap <b>Allow</b> to download the profile.</li>
                <li>Open <b>Settings → Profile Downloaded</b> (right under your name) and tap <b>Install</b>.</li>
                <li>Open Contacts — everyone is there, under the default account.</li>
              </>}
            </ol>
            <Field label={mobile ? 'Or open this link on the device' : 'Or open this link on the device'}><CopyField value={link.profile_url} label="Profile link" /></Field>
            <p className="muted xsmall" style={{ margin: 0 }}>The link works once and only for 15 minutes; the password inside it is saved as “{name}” under Devices with access.</p>
          </div>
        </div>
      )}
    </Modal>
  )
}
