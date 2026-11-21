import { detectAngle, type CameraFiles } from './clips'

// A folder is an event containing synchronized camera groups for each minute.
export function groupCameraMinutes(entries: { type: string; name: string }[]) {
  const minutes = new Map<string, CameraFiles>()
  for (const entry of entries) {
    if (entry.type !== 'file') continue
    const match = /^(\d{4}-\d{2}-\d{2}_\d{2}-\d{2}-\d{2})-.*\.mp4$/i.exec(entry.name)
    const angle = detectAngle(entry.name)
    if (!match || !angle) continue
    const files = minutes.get(match[1]) ?? {}
    files[angle] = entry.name
    minutes.set(match[1], files)
  }
  return [...minutes].sort(([a], [b]) => a.localeCompare(b)).map(([timestamp, files]) => ({ timestamp, files }))
}

/**
 * Size for showing a camera at its actual resolution: one CSS pixel per video
 * pixel, scaled down (never up) to fit the viewport minus a margin.
 */
export function actualSizeBox(
  videoWidth: number,
  videoHeight: number,
  viewportWidth: number,
  viewportHeight: number,
  margin = 32,
): { width: number, height: number, scale: number } {
  if (videoWidth <= 0 || videoHeight <= 0) return { width: 0, height: 0, scale: 1 }
  const scale = Math.min(
    1,
    Math.max(0, viewportWidth - 2 * margin) / videoWidth,
    Math.max(0, viewportHeight - 2 * margin) / videoHeight,
  )
  return { width: Math.round(videoWidth * scale), height: Math.round(videoHeight * scale), scale }
}
