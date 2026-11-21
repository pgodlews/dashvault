/**
 * Map time ranges, Grafana style: a relative preset ("last 7 days", kept
 * relative across reloads) or an absolute from/to. Times are local wall-clock
 * time, which is how the car names its clips (the Pi and the browser are
 * normally in the car's time zone).
 */

export interface TimeRange {
  from: Date
  to: Date
}

export interface MapSearch {
  /** A preset id; wins over from/to. */
  range?: string
  /** Absolute bounds, YYYY-MM-DDTHH:MM local time. */
  from?: string
  to?: string
  /** A selected trip: its first clip's local start, YYYY-MM-DDTHH:MM:SS. */
  trip?: string
  /** Look around at "lat,lon" with this radius in metres. */
  look?: string
  lr?: number
}

export const RANGE_PRESETS = [
  { id: '24h', label: 'Last 24 hours', hours: 24 },
  { id: '7d', label: 'Last 7 days', hours: 24 * 7 },
  { id: '30d', label: 'Last 30 days', hours: 24 * 30 },
  { id: '90d', label: 'Last 90 days', hours: 24 * 90 },
  { id: 'all', label: 'All retained', hours: null },
] as const

export const DEFAULT_PRESET = '7d'
const HOUR = 3_600_000

function pad(value: number): string {
  return String(value).padStart(2, '0')
}

/** Local "YYYY-MM-DDTHH:MM", the format of <input type="datetime-local">. */
export function toLocalInput(date: Date): string {
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`
}

/** Local "YYYY-MM-DD". */
export function toLocalDay(date: Date): string {
  return toLocalInput(date).slice(0, 10)
}

/** Parses local "YYYY-MM-DD[THH:MM[:SS]]"; null when missing or invalid. */
export function parseLocal(value: string | undefined): Date | null {
  if (!value) return null
  const match = /^(\d{4})-(\d{2})-(\d{2})(?:T(\d{2}):(\d{2})(?::(\d{2}))?)?$/.exec(value.trim())
  if (!match) return null
  const [, y, mo, d, h = '0', mi = '0', s = '0'] = match
  const date = new Date(Number(y), Number(mo) - 1, Number(d), Number(h), Number(mi), Number(s))
  return Number.isNaN(date.getTime()) ? null : date
}

export function validateMapSearch(search: Record<string, unknown>): MapSearch {
  const text = (value: unknown) => (typeof value === 'string' && value ? value : undefined)
  const range = text(search.range)
  return {
    range: RANGE_PRESETS.some(preset => preset.id === range) ? range : undefined,
    from: parseLocal(text(search.from)) ? text(search.from) : undefined,
    to: parseLocal(text(search.to)) ? text(search.to) : undefined,
    trip: parseLocal(text(search.trip)) ? text(search.trip) : undefined,
    look: /^-?\d+(\.\d+)?,-?\d+(\.\d+)?$/.test(text(search.look) ?? '') ? text(search.look) : undefined,
    lr: Number(search.lr) >= 10 && Number(search.lr) <= 2000 ? Math.round(Number(search.lr)) : undefined,
  }
}

/** The range a search stands for; a preset or nothing at all means relative to now. */
export function resolveRange(search: MapSearch, now: Date, retentionDays: number): TimeRange {
  const from = parseLocal(search.from)
  if (!search.range && from) {
    const to = parseLocal(search.to) ?? now
    return to > from ? { from, to } : { from: to, to: from }
  }
  const preset = RANGE_PRESETS.find(item => item.id === (search.range ?? DEFAULT_PRESET)) ?? RANGE_PRESETS[1]
  const hours = preset.hours ?? retentionDays * 24
  return { from: new Date(now.getTime() - hours * HOUR), to: now }
}

export function presetLabel(search: MapSearch): string | null {
  if (search.range || !search.from) {
    return RANGE_PRESETS.find(item => item.id === (search.range ?? DEFAULT_PRESET))?.label ?? null
  }
  return null
}

export function formatRange(range: TimeRange): string {
  const sameDay = toLocalDay(range.from) === toLocalDay(range.to)
  const day = (date: Date) => date.toLocaleDateString(undefined, { day: 'numeric', month: 'short' })
  const time = (date: Date) => date.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' })
  return sameDay
    ? `${day(range.from)} ${time(range.from)}–${time(range.to)}`
    : `${day(range.from)} ${time(range.from)} – ${day(range.to)} ${time(range.to)}`
}

/** Twice as long, around the same middle, never past now. */
export function zoomOut(range: TimeRange, now: Date): TimeRange {
  const span = range.to.getTime() - range.from.getTime()
  let to = new Date(range.to.getTime() + span / 2)
  if (to > now) to = now
  return { from: new Date(to.getTime() - span * 2), to }
}
