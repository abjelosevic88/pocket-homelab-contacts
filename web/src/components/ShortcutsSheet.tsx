import { Modal } from './ui'

const GROUPS: { title: string; keys: [string[], string][] }[] = [
  { title: 'Find', keys: [[['/'], 'Search the list'], [['⌘', 'K'], 'Search & commands'], [['esc'], 'Clear search / selection']] },
  { title: 'Move', keys: [[['j'], 'Next contact'], [['k'], 'Previous contact'], [['↓'], 'Next contact'], [['↑'], 'Previous contact']] },
  { title: 'Contacts', keys: [[['n'], 'New contact'], [['e'], 'Edit'], [['s'], 'Star / unstar'], [['⌫'], 'Delete (asks first)'], [['⌘', '↵'], 'Save in the editor']] },
  { title: 'Select', keys: [[['⌘', 'A'], 'Select all in the list'], [['⇧', 'click'], 'Select a range'], [['⌘', 'click'], 'Add one to the selection']] },
  { title: 'Help', keys: [[['?'], 'This sheet']] },
]

export function ShortcutsSheet({ onClose }: { onClose: () => void }) {
  return (
    <Modal title="Keyboard shortcuts" onClose={onClose} size="lg">
      <div className="shortcuts">
        {GROUPS.map(g => (
          <section key={g.title}>
            <h4 className="sc-h">{g.title}</h4>
            {g.keys.map(([keys, label]) => (
              <div key={label + keys.join('')} className="sc-row"><span>{label}</span><span className="sc-keys">{keys.map(k => <kbd key={k}>{k}</kbd>)}</span></div>
            ))}
          </section>
        ))}
      </div>
    </Modal>
  )
}
