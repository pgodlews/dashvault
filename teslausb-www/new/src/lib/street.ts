/** Drives near a point and camera stills: GET /api/street/near and /api/street/frame. */

export interface StreetPass {
  id: string
  recorded_at: string
  /** Local time of the closest moment, YYYY-MM-DDTHH:MM:SS. */
  at: string
  /** Seconds into the clip of the closest moment. */
  t: number
  duration: number
  distance_m: number
  speed_kmh: number
  heading: number
  lat: number
  lon: number
  saved: boolean
  /** Cameras whose video is still on the Pi. */
  cameras: string[]
}

export interface StreetNearResponse {
  lat: number
  lon: number
  radius: number
  passes: StreetPass[]
  /** Whether frames can be rendered (static ffmpeg installed). */
  ffmpeg: boolean
}

/** Grid order: the side cameras around front, back below (HW4 pillars beside it). */
export const LOOK_CAMERAS = ['left_repeater', 'front', 'right_repeater', 'left_pillar', 'back', 'right_pillar']

const LABELS: Record<string, string> = {
  front: 'Front',
  back: 'Back',
  left_repeater: 'Left repeater',
  right_repeater: 'Right repeater',
  left_pillar: 'Left pillar',
  right_pillar: 'Right pillar',
}

export function lookCameraLabel(camera: string): string {
  return LABELS[camera] ?? camera
}

/** "Wed 12 Nov 10:01:59", optionally moved by offset seconds. */
export function formatPassTime(at: string, offsetSeconds = 0): string {
  const match = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})/.exec(at)
  if (!match) return at
  const [, y, mo, d, h, mi, s] = match
  const date = new Date(Number(y), Number(mo) - 1, Number(d), Number(h), Number(mi), Number(s) + Math.round(offsetSeconds))
  const day = date.toLocaleDateString(undefined, { weekday: 'short', day: 'numeric', month: 'short' })
  const time = date.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit', second: '2-digit' })
  return offsetSeconds ? time : `${day} ${time}`
}

/** Parses the map's look=lat,lon search value. */
export function parseLook(value: string | undefined): { lat: number, lon: number } | null {
  if (!value) return null
  const [lat, lon] = value.split(',').map(Number)
  if (!Number.isFinite(lat) || !Number.isFinite(lon) || Math.abs(lat) > 90 || Math.abs(lon) > 180) return null
  return { lat, lon }
}

/**
 * Google Street View at a point, looking along the heading (Google's
 * documented Maps URL). Opens Google's site, which then knows the location.
 */
export function googleStreetViewUrl(lat: number, lon: number, heading: number): string {
  const params = new URLSearchParams({
    api: '1',
    map_action: 'pano',
    viewpoint: `${lat.toFixed(6)},${lon.toFixed(6)}`,
    heading: String(Math.round(((heading % 360) + 360) % 360)),
  })
  return `https://www.google.com/maps/@?${params.toString()}`
}
