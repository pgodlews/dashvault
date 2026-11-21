import { clipLatLons, type TelemetryClip } from './telemetry'
import { parseLocal } from './timeRange'

/** Replay speeds offered, times real time. */
export const REPLAY_SPEEDS = [1, 2, 5, 10, 30, 60]

/** Clips closer than this belong to the same trip. */
export const TRIP_GAP_MS = 5 * 60_000

export interface Trip {
  /** The first clip's local start, "YYYY-MM-DDTHH:MM:SS"; also the URL key. */
  id: string
  start: Date
  end: Date
  clips: TelemetryClip[]
  drivingSeconds: number
  distanceKm: number
  saved: number
}

function clipStart(clip: TelemetryClip): Date | null {
  return parseLocal(clip.recorded_at.slice(0, 19))
}

/** Great-circle distance in km. */
export function haversineKm(a: [number, number], b: [number, number]): number {
  const rad = Math.PI / 180
  const dLat = (b[0] - a[0]) * rad
  const dLon = (b[1] - a[1]) * rad
  const h = Math.sin(dLat / 2) ** 2 + Math.cos(a[0] * rad) * Math.cos(b[0] * rad) * Math.sin(dLon / 2) ** 2
  return 2 * 6371 * Math.asin(Math.min(1, Math.sqrt(h)))
}

export function pathKm(points: [number, number][]): number {
  let total = 0
  for (let i = 1; i < points.length; i++) total += haversineKm(points[i - 1], points[i])
  return total
}

/**
 * Groups clips into trips: consecutive clips (by recording time) with less
 * than five minutes between one's end and the next one's start. Saved and
 * Recent clips of the same drive end up in the same trip. Newest trip first.
 */
export function groupTrips(clips: TelemetryClip[]): Trip[] {
  const timed = clips
    .map(clip => ({ clip, start: clipStart(clip) }))
    .filter((item): item is { clip: TelemetryClip, start: Date } => item.start !== null)
    .sort((a, b) => a.start.getTime() - b.start.getTime())
  const trips: Trip[] = []
  let current: Trip | null = null
  for (const { clip, start } of timed) {
    const end = new Date(start.getTime() + Math.max(clip.duration, 1) * 1000)
    if (!current || start.getTime() - current.end.getTime() > TRIP_GAP_MS) {
      current = { id: clip.recorded_at.slice(0, 19), start, end, clips: [], drivingSeconds: 0, distanceKm: 0, saved: 0 }
      trips.push(current)
    }
    current.clips.push(clip)
    if (end > current.end) current.end = end
    current.drivingSeconds += clip.duration
    current.distanceKm += pathKm(clipLatLons(clip))
    if (clip.id.startsWith('SavedClips/')) current.saved += 1
  }
  return trips.reverse()
}

export function formatDuration(seconds: number): string {
  const minutes = Math.round(seconds / 60)
  if (minutes < 60) return `${minutes} min`
  return `${Math.floor(minutes / 60)} h ${String(minutes % 60).padStart(2, '0')} min`
}
