import { Link } from '@tanstack/react-router'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { VehicleTelemetry } from './VehicleTelemetry'
import { Icon } from './Icon'
import {
  telemetryPointAt,
  type TelemetryClip,
  type TelemetryCursor,
  type TelemetryHistory,
} from '../lib/telemetry'
import { parseLocal, toLocalInput } from '../lib/timeRange'
import { formatDuration, REPLAY_SPEEDS, type Trip } from '../lib/trips'

interface TripReplayProps {
  trip: Trip
  onCursor: (cursor: TelemetryCursor | null) => void
  onClose: () => void
}

interface Segment {
  clip: TelemetryClip & { track: NonNullable<TelemetryClip['track']> }
  /** Seconds of driving before this clip, in the replay timeline. */
  offset: number
  duration: number
  start: Date
}

/**
 * Plays a trip back from its full tracks: driving time only (the gaps between
 * clips are skipped), at 1x to 60x, with a heading marker on the map and the
 * vehicle panel the viewer uses.
 */
export function TripReplay({ trip, onCursor, onClose }: TripReplayProps) {
  const [segments, setSegments] = useState<Segment[] | null>(null)
  const [error, setError] = useState('')
  const [playing, setPlaying] = useState(false)
  const [speed, setSpeed] = useState(10)
  const [position, setPosition] = useState(0)
  const positionRef = useRef(0)
  const speedRef = useRef(speed)
  useEffect(() => { speedRef.current = speed }, [speed])

  useEffect(() => {
    let cancelled = false
    const from = toLocalInput(new Date(trip.start.getTime() - 60_000))
    const to = toLocalInput(new Date(trip.end.getTime() + 60_000))
    const wanted = new Set(trip.clips.map(clip => clip.id))
    fetch(`/api/telemetry/tracks?from=${encodeURIComponent(from)}&to=${encodeURIComponent(to)}&full=1`)
      .then(response => {
        if (!response.ok) throw new Error('Could not load the trip')
        return response.json() as Promise<TelemetryHistory>
      })
      .then(history => {
        if (cancelled) return
        let offset = 0
        const built: Segment[] = []
        for (const clip of history.clips) {
          const start = parseLocal(clip.recorded_at.slice(0, 19))
          if (!wanted.has(clip.id) || !clip.track?.length || !start) continue
          const duration = Math.max(clip.track[clip.track.length - 1].t, 1)
          built.push({ clip: clip as Segment['clip'], offset, duration, start })
          offset += duration
        }
        setSegments(built)
      })
      .catch(reason => { if (!cancelled) setError(reason instanceof Error ? reason.message : 'Could not load the trip') })
    return () => { cancelled = true }
  }, [trip])

  const total = segments?.length ? segments[segments.length - 1].offset + segments[segments.length - 1].duration : 0
  const segmentAt = useCallback((seconds: number): Segment | null => {
    if (!segments?.length) return null
    let found = segments[0]
    for (const segment of segments) {
      if (segment.offset <= seconds) found = segment
      else break
    }
    return found
  }, [segments])
  const current = segmentAt(position)

  // Publish the marker position.
  useEffect(() => {
    if (!current) {
      onCursor(null)
      return
    }
    const point = telemetryPointAt(current.clip.track, position - current.offset)
    onCursor({ lat: point.lat, lon: point.lon, heading: point.heading })
  }, [current, position, onCursor])
  useEffect(() => () => onCursor(null), [onCursor])

  // Advance while playing; updates at most ten times a second.
  useEffect(() => {
    if (!playing || total === 0) return
    let last = performance.now()
    const timer = setInterval(() => {
      const now = performance.now()
      const next = Math.min(total, positionRef.current + ((now - last) / 1000) * speedRef.current)
      last = now
      positionRef.current = next
      setPosition(next)
      if (next >= total) setPlaying(false)
    }, 100)
    return () => clearInterval(timer)
  }, [playing, total])

  const seek = (seconds: number) => {
    positionRef.current = seconds
    setPosition(seconds)
  }
  const playbackTime = useCallback(() => {
    const segment = segmentAt(positionRef.current)
    return segment ? positionRef.current - segment.offset : null
  }, [segmentAt])

  const clock = current ? new Date(current.start.getTime() + (position - current.offset) * 1000) : trip.start
  const tripLabel = useMemo(() => trip.start.toLocaleDateString(undefined, { weekday: 'short', day: 'numeric', month: 'short' }), [trip])

  return (
    <div
      data-testid="trip-replay"
      style={{
        position: 'absolute', left: 16, bottom: 16, zIndex: 1000,
        width: 'min(380px, calc(100% - 32px))', maxHeight: 'calc(100% - 32px)', overflow: 'auto',
        background: 'var(--bg-1)', border: '1px solid var(--border)', borderRadius: 'var(--radius-md, 10px)',
        boxShadow: '0 8px 24px rgba(0,0,0,.35)', padding: 'var(--sp-4)',
      }}
    >
      <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8 }}>
        <strong style={{ color: 'var(--fg-0)' }}>Replay · {tripLabel}</strong>
        <span style={{ color: 'var(--fg-2)', fontSize: 'var(--ts-sm)' }}>
          {clock.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit', second: '2-digit' })}
        </span>
        <div style={{ flex: 1 }} />
        <button onClick={onClose} aria-label="Close replay" style={{ background: 'none', border: 0, color: 'var(--fg-2)', cursor: 'pointer' }}>
          <Icon name="x" size={16} />
        </button>
      </div>
      {error && <div style={{ color: 'var(--err)' }}>{error}</div>}
      {!error && !segments && <div style={{ color: 'var(--fg-2)' }}>Loading the trip…</div>}
      {segments && segments.length === 0 && <div style={{ color: 'var(--fg-2)' }}>No full tracks are stored for this trip.</div>}
      {segments && segments.length > 0 && current && (
        <>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
            <button
              onClick={() => {
                if (position >= total) seek(0)
                setPlaying(value => !value)
              }}
              aria-label={playing ? 'Pause replay' : 'Play replay'}
              style={{ display: 'grid', placeItems: 'center', width: 34, height: 34, borderRadius: '50%', border: '1px solid var(--border)', background: 'var(--bg-2)', color: 'var(--fg-0)', cursor: 'pointer' }}
            >
              <Icon name={playing ? 'pause' : 'play'} size={16} />
            </button>
            <input
              type="range"
              aria-label="Replay position"
              min={0}
              max={total}
              step={1}
              value={position}
              onChange={event => seek(Number(event.target.value))}
              style={{ flex: 1 }}
            />
            <select
              aria-label="Replay speed"
              value={speed}
              onChange={event => setSpeed(Number(event.target.value))}
              style={{ background: 'var(--bg-2)', color: 'var(--fg-1)', border: '1px solid var(--border)', borderRadius: 6, padding: '4px 6px' }}
            >
              {REPLAY_SPEEDS.map(value => <option key={value} value={value}>{value}×</option>)}
            </select>
          </div>
          <div style={{ display: 'flex', justifyContent: 'space-between', color: 'var(--fg-2)', fontSize: 'var(--ts-xs)', margin: '4px 0 10px' }}>
            <span>{formatDuration(position)} of {formatDuration(total)} driving</span>
            {current.clip.state === 'available' ? (
              <Link to="/viewer" search={{ clip: current.clip.id, t: Math.round((position - current.offset) * 10) / 10 }} style={{ color: 'var(--accent)' }}>Open this minute's video</Link>
            ) : (
              <span>Video {current.clip.state === 'archived' ? 'archived' : 'no longer on the Pi'}</span>
            )}
          </div>
          <VehicleTelemetry key={current.clip.id} clip={current.clip} playbackTime={playbackTime} />
        </>
      )}
    </div>
  )
}
