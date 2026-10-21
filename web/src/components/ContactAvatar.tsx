import { useEffect, useState } from 'react'
import { Icon } from './icons'

type Face = { id?: number; photo_url?: string; has_photo?: boolean; initials?: string; color?: string; fn?: string; kind?: string }

export function photoSrc(url: string | undefined, px: number): string {
  if (!url) return ''
  if (url.startsWith('blob:')) return url
  return url + (url.includes('?') ? '&' : '?') + 's=' + px
}

function initialsOf(name: string): string {
  return name.split(/\s+/).filter(Boolean).slice(0, 2).map(s => [...s][0]!.toUpperCase()).join('')
}

/** A contact's photo, else the initials on the contact's colour. */
export function ContactAvatar({ c, size = 36, className = '' }: { c: Face; size?: number; className?: string }) {
  const px = size <= 48 ? 96 : size <= 128 ? 256 : 512
  const src = c.has_photo !== false && c.photo_url ? photoSrc(c.photo_url, px) : ''
  const [broken, setBroken] = useState(false)
  useEffect(() => setBroken(false), [src])
  const style = { width: size, height: size, fontSize: Math.round(size * 0.38), background: c.color || 'var(--faint)' }
  if (src && !broken) {
    return <span className={`avatar has-photo ${className}`} style={style} aria-hidden="true"><img src={src} alt="" loading="lazy" decoding="async" onError={() => setBroken(true)} draggable={false} /></span>
  }
  const ini = c.initials || initialsOf(c.fn || '')
  return (
    <span className={`avatar ${className}`} style={style} aria-hidden="true">
      {c.kind === 'org' && !ini ? <Icon name="building" size={Math.round(size * 0.45)} /> : ini || <Icon name="user" size={Math.round(size * 0.45)} />}
    </span>
  )
}
