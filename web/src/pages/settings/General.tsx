import { useEffect, useState } from 'react'
import { useApp } from '../../lib/app-context'
import { bookOptions } from '../../components/BookDialogs'
import { Icon } from '../../components/icons'
import { type Option, Select } from '../../components/Select'
import { Loading, Segmented, SettingRow } from '../../components/ui'
import { Section, useSaveSettings } from './common'

const REGIONS: Option<string>[] = [
  ['', 'None — keep numbers as typed'], ['BA', 'Bosnia and Herzegovina (+387)'], ['RS', 'Serbia (+381)'], ['HR', 'Croatia (+385)'], ['ME', 'Montenegro (+382)'], ['SI', 'Slovenia (+386)'],
  ['MK', 'North Macedonia (+389)'], ['AT', 'Austria (+43)'], ['DE', 'Germany (+49)'], ['CH', 'Switzerland (+41)'], ['GB', 'United Kingdom (+44)'], ['US', 'United States (+1)'],
  ['NL', 'Netherlands (+31)'], ['SE', 'Sweden (+46)'], ['IT', 'Italy (+39)'], ['FR', 'France (+33)'], ['ES', 'Spain (+34)'],
].map(([value, label]) => ({ value, label, keywords: value }))

export function GeneralTab() {
  const { sr, settings, user, books, themeMode, setThemeMode } = useApp()
  const save = useSaveSettings()
  const [title, setTitle] = useState(settings?.title || '')
  useEffect(() => { setTitle(settings?.title || '') }, [settings?.title])
  if (!sr || !settings) return <Loading />
  const s = settings
  const opts: Option<number>[] = [{ value: 0, label: 'Your default address book', description: 'The one marked as default in the sidebar' }, ...bookOptions(books)]
  const regions = REGIONS.some(r => r.value === (s.phone_region || '')) ? REGIONS : [...REGIONS, { value: s.phone_region, label: s.phone_region }]

  return (
    <div className="stack loose">
      <Section title="Names">
        <SettingRow title="Sort by" help="The A–Z sections in the list follow this.">
          <Segmented label="Sort by" value={s.sort_by || 'given'} onChange={v => save({ sort_by: v })} options={[{ value: 'given', label: 'First name' }, { value: 'family', label: 'Last name' }]} />
        </SettingRow>
        <SettingRow title="Show names as">
          <Segmented label="Display order" value={s.display_order || 'given_first'} onChange={v => save({ display_order: v })} options={[{ value: 'given_first', label: 'Marko Petrović' }, { value: 'family_first', label: 'Petrović Marko' }]} />
        </SettingRow>
      </Section>

      <Section title="Appearance">
        <SettingRow title="Theme" help="System follows your device’s light or dark setting.">
          <Segmented label="Theme" value={themeMode} onChange={setThemeMode} options={[
            { value: 'system', label: <span className="row"><Icon name="monitor" size={14} />System</span> },
            { value: 'light', label: <span className="row"><Icon name="sun" size={14} />Light</span> },
            { value: 'dark', label: <span className="row"><Icon name="moon" size={14} />Dark</span> },
          ]} />
        </SettingRow>
        <SettingRow title="Density" help="Compact fits more people on the screen.">
          <Segmented label="Density" value={s.density || 'comfortable'} onChange={v => save({ density: v })} options={[{ value: 'comfortable', label: 'Comfortable' }, { value: 'compact', label: 'Compact' }]} />
        </SettingRow>
      </Section>

      <Section title="New contacts">
        <SettingRow title="Default address book" help="Where new contacts and imports go unless you pick another.">
          <Select value={s.default_addressbook_id || 0} options={opts} onChange={v => save({ default_addressbook_id: v })} label="Default address book" />
        </SettingRow>
        <SettingRow title="Phone numbers without a country code" help="Belong to this country — so 065 123 456 and +387 65 123 456 are found as the same number.">
          <Select value={s.phone_region || ''} options={regions} onChange={v => save({ phone_region: v })} label="Phone region" searchable />
        </SettingRow>
      </Section>

      {user?.role === 'admin' && (
        <Section title="This box" sub="Only admins see this.">
          <SettingRow title="App title" help="Shown in the browser tab and on the sign-in page.">
            <input value={title} onChange={e => setTitle(e.target.value)} onBlur={() => { if (title.trim() && title !== s.title) save({ title: title.trim() }, 'Title saved') }}
              onKeyDown={e => { if (e.key === 'Enter') (e.target as HTMLInputElement).blur() }} style={{ width: 240 }} aria-label="App title" />
          </SettingRow>
        </Section>
      )}
    </div>
  )
}
