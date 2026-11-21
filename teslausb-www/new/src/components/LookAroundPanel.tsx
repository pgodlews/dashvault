import { Link } from '@tanstack/react-router'
import { useCallback, useEffect, useMemo, useState } from 'react'
import { Icon } from './Icon'
import { apiFetch } from '../lib/api'
import { telemetryPointAt, type TelemetryClip, type TelemetryCursor } from '../lib/telemetry'
import {
  formatPassTime,
  googleStreetViewUrl,
  LOOK_CAMERAS,
  lookCameraLabel,
  type StreetNearResponse,
  type StreetPass,
} from '../lib/street'

interface LookAroundPanelProps {
  lat: number
  lon: number
  radius: number
  onCursor: (cursor: TelemetryCursor | null) => void
  onClose: () => void
}

function frameUrl(pass: StreetPass, camera: string, t: number, width?: number): string {
  const params = new URLSearchParams({ id: pass.id, camera, t: t.toFixed(1) })
  if (width) params.set('w', String(width))
  return `/api/street/frame?${params.toString()}`
}

/**
 * Look around from the dashcam: the drives that passed near a point, with
 * every camera's still at the closest moment, and steps along the road. Only
 * clips still on the Pi have pictures; frames come from GET /api/street/frame.
 */
export function LookAroundPanel({ lat, lon, radius, onCursor, onClose }: LookAroundPanelProps) {
  const [result, setResult] = useState<StreetNearResponse | null>(null)
  const [error, setError] = useState('')
  const [selected, setSelected] = useState(0)
  const [offset, setOffset] = useState(0)
  const [track, setTrack] = useState<TelemetryClip | null>(null)
  const [installing, setInstalling] = useState(false)
  const [ffmpegReady, setFfmpegReady] = useState<boolean | null>(null)

  useEffect(() => {
    let cancelled = false
    fetch(`/api/street/near?lat=${lat}&lon=${lon}&radius=${Math.round(radius)}`)
      .then(async response => {
        const body = await response.json() as StreetNearResponse & { error?: string }
        if (!response.ok) throw new Error(body.error ?? 'Search failed')
        return body
      })
      .then(body => {
        if (cancelled) return
        setResult(body)
        setFfmpegReady(body.ffmpeg)
        setSelected(0)
        setOffset(0)
        setError('')
      })
      .catch(reason => { if (!cancelled) setError(reason instanceof Error ? reason.message : 'Search failed') })
    return () => { cancelled = true }
  }, [lat, lon, radius])

  const pass = result?.passes[selected] ?? null
  const t = pass ? Math.max(0, Math.min(pass.duration - 0.5, pass.t + offset)) : 0

  // The selected clip's full track: heading for the map arrow while stepping.
  useEffect(() => {
    if (!pass) return
    let cancelled = false
    fetch(`/api/telemetry/clip?id=${encodeURIComponent(pass.id)}`)
      .then(response => (response.ok ? response.json() as Promise<TelemetryClip> : null))
      .then(clip => { if (!cancelled) setTrack(clip) })
      .catch(() => { if (!cancelled) setTrack(null) })
    return () => { cancelled = true }
  }, [pass])

  const point = useMemo(() => (track?.id === pass?.id && track?.track?.length ? telemetryPointAt(track.track, t) : null), [track, pass, t])
  useEffect(() => {
    if (point) onCursor({ lat: point.lat, lon: point.lon, heading: point.heading })
    else if (pass) onCursor({ lat: pass.lat, lon: pass.lon, heading: pass.heading })
    else onCursor(null)
  }, [point, pass, onCursor])
  useEffect(() => () => onCursor(null), [onCursor])

  const step = useCallback((seconds: number) => {
    if (!pass) return
    setOffset(current => {
      const next = Math.max(-pass.t, Math.min(pass.duration - 0.5 - pass.t, current + seconds))
      return Math.round(next * 10) / 10
    })
  }, [pass])

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.target instanceof HTMLInputElement || event.target instanceof HTMLSelectElement) return
      if (event.key === 'ArrowLeft') { event.preventDefault(); step(event.shiftKey ? -5 : -1) }
      if (event.key === 'ArrowRight') { event.preventDefault(); step(event.shiftKey ? 5 : 1) }
      if (event.key === 'Escape') onClose()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [step, onClose])

  async function installFfmpeg() {
    setInstalling(true)
    try {
      const response = await apiFetch('/api/street/install-ffmpeg', { method: 'POST' })
      const body = await response.json() as { installed?: boolean, error?: string }
      if (!response.ok || !body.installed) throw new Error(body.error || 'Install failed')
      setFfmpegReady(true)
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : 'Install failed')
    } finally {
      setInstalling(false)
    }
  }

  const cameras = pass?.cameras ?? []
  const button = {
    display: 'flex', alignItems: 'center', gap: 4, padding: '5px 9px', borderRadius: 'var(--radius-sm)',
    border: '1px solid var(--border)', background: 'var(--bg-2)', color: 'var(--fg-1)', cursor: 'pointer',
    fontSize: 'var(--ts-sm)',
  } as const

  return (
    <section
      aria-label="Look around"
      data-testid="look-around"
      style={{
        // The map stays visible above, with the heading arrow at the spot.
        position: 'absolute', left: 12, right: 12, bottom: 12, zIndex: 1000, height: 'min(62%, 620px)',
        display: 'flex', flexDirection: 'column', overflow: 'hidden', background: 'var(--bg-1)',
        border: '1px solid var(--border)', borderRadius: 'var(--radius-md, 10px)',
        boxShadow: '0 8px 24px rgba(0,0,0,.35)', padding: 'var(--sp-3)',
      }}
    >
      <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap', marginBottom: 8 }}>
        <strong style={{ color: 'var(--fg-0)' }}>Look around</strong>
        {result && (
          <span style={{ color: 'var(--fg-2)', fontSize: 'var(--ts-sm)' }}>
            {result.passes.length} pass{result.passes.length === 1 ? '' : 'es'} within {Math.round(result.radius)} m
          </span>
        )}
        {result && result.passes.length > 1 && (
          <select
            aria-label="Pass"
            value={selected}
            onChange={event => { setSelected(Number(event.target.value)); setOffset(0) }}
            style={button}
          >
            {result.passes.map((item, index) => (
              <option key={item.id + item.at} value={index}>
                {formatPassTime(item.at)} · {item.distance_m} m{item.cameras.length ? '' : ' · no video'}
              </option>
            ))}
          </select>
        )}
        <div style={{ flex: 1 }} />
        {pass && cameras.length > 0 && (
          <>
            <button style={button} aria-label="Back 5 seconds" onClick={() => step(-5)}>⏪</button>
            <button style={button} aria-label="Back 1 second" onClick={() => step(-1)}>◀</button>
            <span style={{ color: 'var(--fg-1)', fontFamily: 'var(--font-mono)', fontSize: 'var(--ts-sm)', minWidth: 72, textAlign: 'center' }}>
              {formatPassTime(pass.at, offset)}
            </span>
            <button style={button} aria-label="Forward 1 second" onClick={() => step(1)}>▶</button>
            <button style={button} aria-label="Forward 5 seconds" onClick={() => step(5)}>⏩</button>
            <Link
              to="/viewer"
              search={{ clip: pass.id, t: Math.round(t * 10) / 10 }}
              title="Open this minute in the viewer at this moment"
              style={{ color: 'var(--accent)', fontSize: 'var(--ts-sm)' }}
            >
              Open video
            </Link>
          </>
        )}
        {pass && (
          <a
            href={googleStreetViewUrl(point?.lat ?? pass.lat, point?.lon ?? pass.lon, point?.heading ?? pass.heading)}
            target="_blank"
            rel="noopener noreferrer"
            title="Google Street View here, looking the way the car was driving (opens a new tab on Google's site)"
            style={{ color: 'var(--accent)', fontSize: 'var(--ts-sm)' }}
          >
            Street View ↗
          </a>
        )}
        <button onClick={onClose} aria-label="Close look around" style={{ ...button, border: 0, background: 'none' }}>
          <Icon name="x" size={16} />
        </button>
      </div>

      {error && <div style={{ color: 'var(--err)' }}>{error}</div>}
      {!error && !result && <div style={{ color: 'var(--fg-2)' }}>Searching the drives near this spot…</div>}
      {result && result.passes.length === 0 && (
        <div style={{ color: 'var(--fg-2)' }}>No stored drive passed within {Math.round(result.radius)} m. Zoom in and click closer to a track.</div>
      )}
      {pass && cameras.length === 0 && (
        <div style={{ color: 'var(--fg-2)' }}>
          {formatPassTime(pass.at)} · {pass.distance_m} m away · {pass.speed_kmh} km/h. The video of this pass is no longer on the Pi, so there is nothing to look at.
        </div>
      )}
      {pass && cameras.length > 0 && ffmpegReady === false && (
        <div style={{ display: 'flex', alignItems: 'center', gap: 10, color: 'var(--fg-2)' }}>
          Camera stills need ffmpeg on the Pi (a one-time download of about 15-20 MB).
          <button style={button} onClick={installFfmpeg} disabled={installing}>{installing ? 'Installing…' : 'Install ffmpeg'}</button>
        </div>
      )}
      {pass && cameras.length > 0 && ffmpegReady && (
        <div
          data-testid="look-around-cameras"
          style={{
            // Two rows share the panel's height; each picture is 4:3 at that height.
            flex: 1, minHeight: 0, display: 'grid', gap: 6, justifyContent: 'center',
            gridTemplateRows: 'repeat(2, minmax(0, 1fr))', gridTemplateColumns: 'repeat(3, auto)',
            gridTemplateAreas: '"left_repeater front right_repeater" "left_pillar back right_pillar"',
          }}
        >
          {LOOK_CAMERAS.filter(camera => cameras.includes(camera)).map(camera => (
            <a
              key={camera}
              href={frameUrl(pass, camera, t)}
              target="_blank"
              rel="noreferrer"
              title="Open at full size"
              style={{ gridArea: camera, position: 'relative', display: 'block', background: '#000', borderRadius: 6, overflow: 'hidden', height: '100%', aspectRatio: '4 / 3', maxWidth: '100%' }}
            >
              <img
                src={frameUrl(pass, camera, t, 640)}
                alt={`${lookCameraLabel(camera)} at ${formatPassTime(pass.at, offset)}`}
                style={{ width: '100%', height: '100%', objectFit: 'contain', display: 'block' }}
              />
              <span style={{
                position: 'absolute', top: 6, left: 6, padding: '2px 7px', borderRadius: 4, fontSize: 'var(--ts-xs)',
                fontFamily: 'var(--font-mono)', letterSpacing: '.06em', textTransform: 'uppercase',
                color: 'rgba(255,255,255,.85)', background: 'rgba(0,0,0,.55)',
              }}>{lookCameraLabel(camera)}</span>
            </a>
          ))}
        </div>
      )}
    </section>
  )
}
