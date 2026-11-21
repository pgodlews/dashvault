// API helpers for listing TeslaCam clip events and building video URLs.

export interface LsEntry {
  type: 'dir' | 'subdir' | 'file'
  name: string
  size?: number
}

/**
 * Call /cgi-bin/ls.sh with a raw query string.
 * The backend splits on '&' so pass "TeslaCam/&SavedClips/event" for sub-paths.
 */
export async function ls(query: string): Promise<LsEntry[]> {
  const res = await fetch(`/cgi-bin/ls.sh?${query}`)
  if (!res.ok) throw new Error(`ls failed: ${res.status}`)
  const text = await res.text()
  const entries: LsEntry[] = []
  for (const line of text.split('\n')) {
    const t = line.trim()
    if (!t || t.startsWith('s:')) continue
    const colon1 = t.indexOf(':')
    if (colon1 < 0) continue
    const type = t.slice(0, colon1)
    const rest = t.slice(colon1 + 1)
    if (type === 'f') {
      const colon2 = rest.lastIndexOf(':')
      const name = colon2 >= 0 ? rest.slice(0, colon2) : rest
      const size = colon2 >= 0 ? parseInt(rest.slice(colon2 + 1), 10) : 0
      if (name) entries.push({ type: 'file', name, size })
    } else if (type === 'd') {
      if (rest) entries.push({ type: 'dir', name: rest })
    } else if (type === 'D') {
      if (rest) entries.push({ type: 'subdir', name: rest })
    }
  }
  return entries
}

/**
 * Build the URL to stream a video file via download.sh.
 * folder = "TeslaCam/SavedClips/2024-01-15_12-30-00", filename = "...-front.mp4"
 */
export function videoUrl(folder: string, filename: string): string {
  return `/cgi-bin/download.sh?${folder}&${filename}`
}

/** Build URL to download an entire event folder as a ZIP. */
export function zipUrl(folder: string): string {
  return `/cgi-bin/downloadzip.sh?${folder}`
}

/** Build the rm.sh query for deleting an event folder. */
export function rmQuery(folder: string): string {
  return `/cgi-bin/rm.sh?${folder}`
}

// ── camera angles ─────────────────────────────────────────────────────────────

export const CAMERA_ORDER = [
  'front',
  'left_repeater',
  'right_repeater',
  'back',
  'narrow_left',
  'narrow_right',
] as const

export type CameraAngle = (typeof CAMERA_ORDER)[number]

export const CAMERA_LABELS: Record<CameraAngle, string> = {
  front: 'Front',
  left_repeater: 'Left',
  right_repeater: 'Right',
  back: 'Back',
  narrow_left: 'Narrow L',
  narrow_right: 'Narrow R',
}

/** Detect which camera angle a filename belongs to. */
export function detectAngle(filename: string): CameraAngle | null {
  const lower = filename.toLowerCase()
  for (const angle of CAMERA_ORDER) {
    if (lower.includes(angle)) return angle
  }
  return null
}

// ── event types ───────────────────────────────────────────────────────────────

export interface ClipEvent {
  label: string      // display name, e.g. "2024-01-15_12-30-00"
  path: string       // ls.sh query, e.g. "TeslaCam/&SavedClips/2024-01-15_12-30-00"
  folderPath: string // raw folder for videoUrl, e.g. "TeslaCam/SavedClips/2024-01-15_12-30-00"
  category: string   // "SavedClips" | "SentryClips" | "RecentClips"
}

export type CameraFiles = Partial<Record<CameraAngle, string>>

// ── formatting ────────────────────────────────────────────────────────────────

/** Format seconds as mm:ss or h:mm:ss */
export function formatTime(s: number): string {
  if (!isFinite(s) || s < 0) return '0:00'
  const h = Math.floor(s / 3600)
  const m = Math.floor((s % 3600) / 60)
  const sec = Math.floor(s % 60)
  if (h > 0) return `${h}:${String(m).padStart(2, '0')}:${String(sec).padStart(2, '0')}`
  return `${m}:${String(sec).padStart(2, '0')}`
}

/** Parse Tesla timestamp label "2024-01-15_12-30-00" → Date (UTC). */
export function parseEventDate(label: string): Date | null {
  const m = label.match(/^(\d{4})-(\d{2})-(\d{2})_(\d{2})-(\d{2})-(\d{2})$/)
  if (!m) return null
  return new Date(Date.UTC(+m[1], +m[2] - 1, +m[3], +m[4], +m[5], +m[6]))
}

/** Human-readable date/time for an event label. */
export function formatEventDate(label: string): string {
  const d = parseEventDate(label)
  if (!d) return label
  return d.toLocaleString(undefined, {
    year: 'numeric', month: 'short', day: 'numeric',
    hour: '2-digit', minute: '2-digit', timeZone: 'UTC',
  })
}

/** Format bytes as human-readable size string. */
export function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  if (bytes < 1024 * 1024 * 1024) return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
  return `${(bytes / (1024 * 1024 * 1024)).toFixed(2)} GB`
}
