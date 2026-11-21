import { useMemo, useRef, useState } from 'react'
import type { TelemetryDay } from '../lib/telemetry'
import { toLocalDay, type TimeRange } from '../lib/timeRange'

interface TimelineStripProps {
  days: TelemetryDay[]
  range: TimeRange
  onSelect: (range: TimeRange) => void
  onZoomOut: () => void
}

const HOUR = 3_600_000
const HEIGHT = 56
const BAR_AREA = 40

interface Bucket {
  start: number
  end: number
  minutes: number
}

/** Driving minutes per hour (ranges up to 4 days) or per day, from /api/telemetry/days. */
function buckets(days: TelemetryDay[], range: TimeRange): Bucket[] {
  const byDay = new Map(days.map(day => [day.day, day]))
  const hourly = range.to.getTime() - range.from.getTime() <= 96 * HOUR
  const result: Bucket[] = []
  const cursor = new Date(range.from)
  cursor.setMinutes(0, 0, 0)
  if (!hourly) cursor.setHours(0)
  while (cursor < range.to && result.length < 2000) {
    const start = cursor.getTime()
    const day = byDay.get(toLocalDay(cursor))
    if (hourly) {
      cursor.setHours(cursor.getHours() + 1)
      result.push({ start, end: cursor.getTime(), minutes: day?.hour_minutes[new Date(start).getHours()] ?? 0 })
    } else {
      cursor.setDate(cursor.getDate() + 1)
      result.push({ start, end: cursor.getTime(), minutes: (day?.driving_seconds ?? 0) / 60 })
    }
  }
  return result
}

function tickLabel(time: number, hourly: boolean, withDay = false): string {
  const date = new Date(time)
  const clock = date.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' })
  const day = date.toLocaleDateString(undefined, { day: 'numeric', month: 'short' })
  if (!hourly) return day
  return withDay ? `${day} ${clock}` : clock
}

/**
 * Driving time across the selected range. Drag to select a shorter range;
 * double-click to zoom out. Pointer events, so it works with touch.
 */
export function TimelineStrip({ days, range, onSelect, onZoomOut }: TimelineStripProps) {
  const svg = useRef<SVGSVGElement>(null)
  const [drag, setDrag] = useState<{ start: number, end: number } | null>(null)
  const from = range.from.getTime()
  const span = Math.max(range.to.getTime() - from, 1)
  const hourly = span <= 96 * HOUR
  const bars = useMemo(() => buckets(days, range), [days, range])
  const peak = Math.max(1, ...bars.map(bar => bar.minutes))
  const total = bars.reduce((sum, bar) => sum + bar.minutes, 0)
  const x = (time: number) => ((time - from) / span) * 1000

  const fraction = (clientX: number) => {
    const box = svg.current?.getBoundingClientRect()
    if (!box || box.width === 0) return 0
    return Math.min(1, Math.max(0, (clientX - box.left) / box.width))
  }

  const ticks = useMemo(() => {
    const count = 6
    return Array.from({ length: count + 1 }, (_, i) => from + (span * i) / count)
  }, [from, span])

  return (
    <div style={{ padding: '6px var(--sp-6) 4px', background: 'var(--bg-1)', borderBottom: '1px solid var(--border)' }}>
      <svg
        ref={svg}
        role="img"
        aria-label={`Driving time, ${Math.round(total)} minutes in this range. Drag to select a shorter range, double-click to zoom out.`}
        data-testid="timeline"
        viewBox={`0 0 1000 ${HEIGHT}`}
        preserveAspectRatio="none"
        style={{ width: '100%', height: HEIGHT, display: 'block', cursor: 'crosshair', touchAction: 'none', userSelect: 'none' }}
        onPointerDown={event => {
          event.currentTarget.setPointerCapture(event.pointerId)
          const f = fraction(event.clientX)
          setDrag({ start: f, end: f })
        }}
        onPointerMove={event => {
          if (drag) setDrag({ ...drag, end: fraction(event.clientX) })
        }}
        onPointerUp={() => {
          if (!drag) return
          const [a, b] = [Math.min(drag.start, drag.end), Math.max(drag.start, drag.end)]
          setDrag(null)
          if (b - a > 0.01) onSelect({ from: new Date(from + a * span), to: new Date(from + b * span) })
        }}
        onPointerCancel={() => setDrag(null)}
        onDoubleClick={onZoomOut}
      >
        <rect x="0" y="0" width="1000" height={BAR_AREA} fill="var(--bg-0)" />
        {bars.map(bar => {
          const height = bar.minutes > 0 ? Math.max(2, (bar.minutes / peak) * (BAR_AREA - 4)) : 0
          const left = x(Math.max(bar.start, from))
          const width = Math.max(1, x(Math.min(bar.end, range.to.getTime())) - left - 0.6)
          return height > 0 ? (
            <rect key={bar.start} x={left} y={BAR_AREA - height} width={width} height={height} fill="var(--accent)" opacity={0.8}>
              <title>{`${tickLabel(bar.start, hourly)}: ${Math.round(bar.minutes)} min driving`}</title>
            </rect>
          ) : null
        })}
        {ticks.map((time, i) => (
          <text
            key={time}
            x={i === 0 ? 2 : i === ticks.length - 1 ? 998 : x(time)}
            y={HEIGHT - 3}
            fontSize="10"
            textAnchor={i === 0 ? 'start' : i === ticks.length - 1 ? 'end' : 'middle'}
            fill="var(--fg-2)"
            style={{ pointerEvents: 'none' }}
          >
            {tickLabel(time, hourly, span > 24 * HOUR)}
          </text>
        ))}
        {drag && Math.abs(drag.end - drag.start) > 0 && (
          <rect
            x={Math.min(drag.start, drag.end) * 1000}
            y="0"
            width={Math.abs(drag.end - drag.start) * 1000}
            height={BAR_AREA}
            fill="var(--accent)"
            opacity={0.18}
            stroke="var(--accent)"
          />
        )}
      </svg>
    </div>
  )
}
