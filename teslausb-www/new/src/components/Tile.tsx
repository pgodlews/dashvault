import type { CSSProperties, ReactNode } from 'react'

interface TileProps {
  label: string
  children: ReactNode
  accent?: string     // left-border colour override
  style?: CSSProperties
}

export function Tile({ label, children, accent, style }: TileProps) {
  return (
    <div style={{
      background: 'var(--bg-2)',
      border: '1px solid var(--border)',
      borderRadius: 'var(--radius-md)',
      borderLeft: accent ? `3px solid ${accent}` : '1px solid var(--border)',
      padding: 'var(--sp-4) var(--sp-5)',
      display: 'flex',
      flexDirection: 'column',
      gap: 'var(--sp-2)',
      minWidth: 0,
      ...style,
    }}>
      <span style={{
        fontSize: 'var(--ts-xs)',
        letterSpacing: '.06em',
        textTransform: 'uppercase',
        color: 'var(--fg-2)',
        fontWeight: 600,
      }}>
        {label}
      </span>
      {children}
    </div>
  )
}

interface TileValueProps {
  value: string
  unit?: string
  color?: string
}

export function TileValue({ value, unit, color }: TileValueProps) {
  return (
    <span style={{
      fontSize: 'var(--ts-2xl)',
      fontWeight: 700,
      fontFamily: 'var(--font-mono)',
      fontVariantNumeric: 'tabular-nums',
      color: color ?? 'var(--fg-0)',
      lineHeight: 1,
    }}>
      {value}
      {unit && (
        <span style={{ fontSize: 'var(--ts-sm)', fontWeight: 400, color: 'var(--fg-2)', marginLeft: 4 }}>
          {unit}
        </span>
      )}
    </span>
  )
}

interface BarProps {
  pct: number      // 0–100
  color?: string
}

export function Bar({ pct, color }: BarProps) {
  const clamped = Math.max(0, Math.min(100, pct))
  return (
    <div style={{
      height: 6,
      borderRadius: 'var(--radius-pill)',
      background: 'var(--bg-4)',
      overflow: 'hidden',
    }}>
      <div style={{
        height: '100%',
        width: `${clamped}%`,
        borderRadius: 'var(--radius-pill)',
        background: color ?? 'var(--accent)',
        transition: 'width .5s ease',
      }} />
    </div>
  )
}
