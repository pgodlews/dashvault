import { useEffect, useState } from 'react'
import { telemetryPointAt, type TelemetryClip, type TelemetryPoint } from '../lib/telemetry'

interface VehicleTelemetryProps {
  clip: TelemetryClip | null
  playbackTime: () => number | null
  /** Smaller, without the acceleration rows: for overlays on video. */
  compact?: boolean
}

const gearLabels = ['Park', 'Drive', 'Reverse', 'Neutral']
const autopilotLabels = ['Off', 'Self-driving', 'Autosteer', 'TACC']
const directions = ['N', 'NE', 'E', 'SE', 'S', 'SW', 'W', 'NW']

export function VehicleTelemetry({ clip, playbackTime, compact = false }: VehicleTelemetryProps) {
  const [point, setPoint] = useState<TelemetryPoint | null>(null)
  const track = clip?.track

  useEffect(() => {
    if (!track?.length) return
    const timer = setInterval(() => {
      const seconds = playbackTime()
      if (seconds !== null) setPoint(telemetryPointAt(track, seconds))
    }, 100)
    return () => clearInterval(timer)
  }, [track, playbackTime])

  if (!clip || !track?.length) {
    return <div style={{ color: 'var(--fg-2)' }}>No telemetry recorded for this clip.</div>
  }
  if (!point) return <div style={{ color: 'var(--fg-2)' }}>Start playback to view vehicle telemetry.</div>

  const heading = ((point.heading % 360) + 360) % 360
  const direction = directions[Math.round(heading / 45) % 8]
  const pedal = Math.max(0, Math.min(100, point.accelerator))
  const steering = point.steering ?? 0

  return (
    <div style={{ color: 'var(--fg-1)', fontSize: 'var(--ts-xs)', lineHeight: 1.4 }}>
      <style>{`@keyframes teslausb-blinker { 50% { opacity: .2; } }`}</style>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 8 }}>
        <div>
          <div style={{ color: 'var(--fg-2)', textTransform: 'uppercase', letterSpacing: '.08em' }}>Speed</div>
          <strong style={{ fontSize: 22, color: 'var(--fg-0)' }}>{Math.round(point.speed * 2.23694)}</strong> mph
          <span style={{ color: 'var(--fg-3)' }}> · {(point.speed * 3.6).toFixed(0)} km/h</span>
        </div>
        {clip.extended && (
          <div style={{ textAlign: 'center', color: 'var(--fg-2)' }}>
            <div style={{ fontSize: 25, color: 'var(--fg-0)', transform: `rotate(${heading}deg)`, transition: 'transform .12s' }}>↑</div>
            {direction} · {Math.round(heading)}°
          </div>
        )}
      </div>

      {clip.extended && (
        <div style={{ display: 'flex', gap: 6, marginTop: 10, flexWrap: 'wrap' }}>
          <Badge label={gearLabels[point.gear] ?? `Gear ${point.gear}`} />
          <Badge label={`AP: ${autopilotLabels[point.autopilot] ?? point.autopilot}`} />
        </div>
      )}

      {clip.extended ? (
        <>
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 10, marginTop: 12 }}>
            <div>
              <div style={{ color: 'var(--fg-2)', marginBottom: 4 }}>Accelerator · {pedal.toFixed(0)}%</div>
              <div style={{ height: 8, background: 'var(--bg-3)', borderRadius: 8, overflow: 'hidden' }}>
                <div style={{ width: `${pedal}%`, height: '100%', background: 'var(--ok)', transition: 'width .1s linear' }} />
              </div>
            </div>
            <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
              <Badge label="BRAKE" active={point.brake} color="var(--err)" />
            </div>
          </div>

          <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginTop: 12 }}>
            <Blinker active={point.blinker_left} direction="left" />
            <div style={{ textAlign: 'center' }}>
              <div style={{
                width: 34, height: 34, border: '2px solid var(--fg-2)', borderRadius: '50%',
                display: 'grid', placeItems: 'center', margin: '0 auto 2px',
                transform: `rotate(${steering}deg)`, transition: 'transform .1s linear',
              }}>↑</div>
              <span style={{ color: 'var(--fg-2)' }}>Steering {steering.toFixed(0)}°</span>
            </div>
            <Blinker active={point.blinker_right} direction="right" />
          </div>

          {!compact && <div style={{ marginTop: 12, color: 'var(--fg-2)' }}>
            Linear acceleration <span style={{ color: 'var(--fg-3)' }}>(m/s²)</span>
            <div style={{ display: 'grid', gridTemplateColumns: 'repeat(3, 1fr)', gap: 5, marginTop: 4 }}>
              <Badge label={`X ${point.accel_x.toFixed(1)}`} />
              <Badge label={`Y ${point.accel_y.toFixed(1)}`} />
              <Badge label={`Z ${point.accel_z.toFixed(1)}`} />
            </div>
          </div>}
        </>
      ) : (
        <div style={{ marginTop: 10, color: 'var(--fg-3)' }}>Extended vehicle telemetry is unavailable for this older track.</div>
      )}
    </div>
  )
}

function Badge({ label, active = true, color }: { label: string; active?: boolean; color?: string }) {
  return <span style={{
    padding: '3px 7px', borderRadius: 4, background: active ? 'var(--bg-3)' : 'var(--bg-2)',
    color: active ? color ?? 'var(--fg-0)' : 'var(--fg-3)',
    border: `1px solid ${active && color ? color : 'var(--border)'}`,
  }}>{label}</span>
}

function Blinker({ active, direction }: { active: boolean; direction: 'left' | 'right' }) {
  return <span style={{
    color: active ? '#65d66e' : 'var(--bg-4)',
    fontSize: 28,
    animation: active ? 'teslausb-blinker 1s steps(1) infinite' : undefined,
  }}>{direction === 'left' ? '◀' : '▶'}</span>
}
