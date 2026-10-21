import { useEffect, useState } from 'react'

/** A QR code as crisp SVG (the encoder is loaded only when a code is shown). */
export function QRCode({ value, size = 200, label }: { value: string; size?: number; label?: string }) {
  const [cells, setCells] = useState<{ n: number; d: string } | null>(null)
  useEffect(() => {
    let live = true
    import('qrcode-generator').then(({ default: qrcode }) => {
      const qr = qrcode(0, 'M')
      qr.addData(value)
      qr.make()
      const n = qr.getModuleCount()
      let d = ''
      for (let r = 0; r < n; r++) for (let c = 0; c < n; c++) if (qr.isDark(r, c)) d += `M${c} ${r}h1v1h-1z`
      if (live) setCells({ n, d })
    })
    return () => { live = false }
  }, [value])
  const q = 4
  return (
    <div className="qr" style={{ width: size, height: size }} role="img" aria-label={label || 'QR code'}>
      {cells ? (
        <svg viewBox={`${-q} ${-q} ${cells.n + 2 * q} ${cells.n + 2 * q}`} width={size} height={size} shapeRendering="crispEdges">
          <rect x={-q} y={-q} width={cells.n + 2 * q} height={cells.n + 2 * q} fill="#fff" />
          <path d={cells.d} fill="#111" />
        </svg>
      ) : <span className="spinner" />}
    </div>
  )
}
