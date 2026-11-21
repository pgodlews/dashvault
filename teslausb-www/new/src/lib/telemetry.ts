export interface TelemetryPoint {
  t: number
  lat: number
  lon: number
  speed: number
  heading: number
  gear: number
  autopilot: number
  accel_x: number
  accel_y: number
  accel_z: number
  accelerator: number
  brake: boolean
  steering: number
  blinker_left: boolean
  blinker_right: boolean
}

export type TelemetryState = 'available' | 'archived' | 'overwritten'

export interface TelemetryClip {
  id: string
  recorded_at: string
  state: TelemetryState
  front_file: string
  duration: number
  samples: number
  extended: boolean
  tile_url?: string
  style_url?: string
  track?: TelemetryPoint[]
  /** Simplified [lat, lon] map line, returned by ranged /api/telemetry/tracks requests. */
  line?: [number, number][]
}

/** A clip's points for drawing: the simplified line when present, else the full track. */
export function clipLatLons(clip: TelemetryClip): [number, number][] {
  if (clip.line) return clip.line
  return (clip.track ?? []).map(point => [point.lat, point.lon])
}

/** Position and direction of the replay marker. */
export interface TelemetryCursor {
  lat: number
  lon: number
  heading: number
}

/** An arrow pointing along the heading (degrees clockwise from north), for map markers. */
export function cursorArrowHtml(heading: number): string {
  return `<div style="width:26px;height:26px;transform:rotate(${heading}deg)"><svg viewBox="0 0 26 26" width="26" height="26"><path d="M13 2 L22 22 L13 17 L4 22 Z" fill="#2563eb" stroke="white" stroke-width="2" stroke-linejoin="round"/></svg></div>`
}

/** One day of driving from /api/telemetry/days (local car time). */
export interface TelemetryDay {
  day: string
  clips: number
  driving_seconds: number
  first: string
  last: string
  hour_minutes: number[]
  saved: number
  recent: number
}

export interface TelemetryDays {
  days: TelemetryDay[]
  total_clips: number
  total_bytes: number
  retention_days: number
}

export interface TelemetrySettings {
  enabled: boolean
  retention_days: number
  tile_url: string
  style_url: string
  /** Experimental: index new clips after every snapshot, also while driving. */
  while_driving?: boolean
}

export interface TelemetryHistory extends TelemetrySettings {
  clips: TelemetryClip[]
}

export type TelemetryCategory = 'SavedClips' | 'RecentClips'

/** Track colour per clip folder: Saved red, Recent yellow. */
export const TELEMETRY_CATEGORY_COLORS: Record<TelemetryCategory, string> = {
  SavedClips: '#e11d2e',
  RecentClips: '#f5b800',
}

export const TELEMETRY_CATEGORY_LABELS: Record<TelemetryCategory, string> = {
  SavedClips: 'Saved',
  RecentClips: 'Recent',
}

export function telemetryClipCategory(clip: TelemetryClip): TelemetryCategory {
  return clip.id.startsWith('SavedClips/') ? 'SavedClips' : 'RecentClips'
}

/**
 * Colour encodes the clip folder; line style encodes whether the clip is
 * still on the Pi (solid, clickable) or archived/overwritten (dashed, faded).
 */
export function telemetryTrackStyle(clip: TelemetryClip): { color: string, onPi: boolean } {
  return {
    color: TELEMETRY_CATEGORY_COLORS[telemetryClipCategory(clip)],
    onPi: clip.state === 'available',
  }
}

/** Recent first, Saved last, so Saved tracks draw on top where they overlap. */
export function sortTracksForDrawing(clips: TelemetryClip[]): TelemetryClip[] {
  const rank = (clip: TelemetryClip) => (telemetryClipCategory(clip) === 'SavedClips' ? 1 : 0)
  return [...clips].sort((a, b) => rank(a) - rank(b))
}

export function telemetryClipLabel(clip: TelemetryClip): string {
  return clip.id.split('/').pop() ?? clip.id
}

export function telemetryStateLabel(state: TelemetryState): string {
  if (state === 'archived') return 'Clip archived'
  if (state === 'overwritten') return 'Clip overwritten before archiving'
  return 'On Pi — click to play'
}


export function telemetryPointAt(track: TelemetryPoint[], seconds: number): TelemetryPoint {
  if (seconds <= track[0].t) return track[0]
  const last = track[track.length - 1]
  if (seconds >= last.t) return last
  let low = 0
  let high = track.length - 1
  while (high - low > 1) {
    const middle = (low + high) >> 1
    if (track[middle].t <= seconds) low = middle
    else high = middle
  }
  const a = track[low]
  const b = track[high]
  const span = b.t - a.t
  const ratio = span > 0 ? (seconds - a.t) / span : 0
  const mix = (x: number, y: number) => x + (y - x) * ratio
  return {
    t: seconds,
    lat: mix(a.lat, b.lat),
    lon: mix(a.lon, b.lon),
    speed: mix(a.speed, b.speed),
    heading: a.heading,
    gear: a.gear,
    autopilot: a.autopilot,
    accel_x: mix(a.accel_x, b.accel_x),
    accel_y: mix(a.accel_y, b.accel_y),
    accel_z: mix(a.accel_z, b.accel_z),
    accelerator: mix(a.accelerator, b.accelerator),
    brake: a.brake,
    steering: mix(a.steering, b.steering),
    blinker_left: a.blinker_left,
    blinker_right: a.blinker_right,
  }
}

/**
 * The point on a track closest to a map position, between samples: the
 * position is projected onto each segment (flat local coordinates are fine
 * at street scale), and the time is interpolated along the closest one. Used
 * to snap the dragged playback dot to the road and find where to seek.
 */
export function nearestOnTrack(track: TelemetryPoint[], lat: number, lon: number): TelemetryPoint | null {
  if (!track.length) return null
  if (track.length === 1) return track[0]
  const scale = Math.cos((lat * Math.PI) / 180)
  const px = lon * scale
  const py = lat
  let best = { distance: Infinity, t: track[0].t }
  for (let i = 1; i < track.length; i++) {
    const a = track[i - 1]
    const b = track[i]
    const ax = a.lon * scale, ay = a.lat, bx = b.lon * scale, by = b.lat
    const dx = bx - ax, dy = by - ay
    const length = dx * dx + dy * dy
    const ratio = length > 0 ? Math.min(1, Math.max(0, ((px - ax) * dx + (py - ay) * dy) / length)) : 0
    const x = ax + dx * ratio, y = ay + dy * ratio
    const distance = (px - x) ** 2 + (py - y) ** 2
    if (distance < best.distance) best = { distance, t: a.t + (b.t - a.t) * ratio }
  }
  return telemetryPointAt(track, best.t)
}

/** How the viewer's map tells playback that the dot is being dragged. */
export interface ScrubHandlers {
  begin: () => void
  move: (seconds: number) => void
  end: (seconds: number) => void
}

/** The transport the actual-size overlay drives (owned by the viewer's PlayerArea). */
export interface PlaybackControls {
  togglePlay: () => void
  seek: (seconds: number) => void
}

export type PlayerHandles = ScrubHandlers & PlaybackControls
