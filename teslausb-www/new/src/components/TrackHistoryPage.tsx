import { Link, useNavigate, useSearch } from '@tanstack/react-router'
import { useCallback, useEffect, useMemo, useState } from 'react'
import { TelemetryMap } from './TelemetryMap'
import { VectorTelemetryMap } from './VectorTelemetryMap'
import { TimelineStrip } from './TimelineStrip'
import { TripReplay } from './TripReplay'
import { LookAroundPanel } from './LookAroundPanel'
import { parseLook } from '../lib/street'
import { Icon } from './Icon'
import {
  sortTracksForDrawing,
  telemetryClipCategory,
  TELEMETRY_CATEGORY_COLORS,
  TELEMETRY_CATEGORY_LABELS,
  type TelemetryCategory,
  type TelemetryClip,
  type TelemetryCursor,
  type TelemetryDays,
  type TelemetryHistory,
} from '../lib/telemetry'
import {
  formatRange,
  presetLabel,
  RANGE_PRESETS,
  resolveRange,
  toLocalInput,
  zoomOut,
  type MapSearch,
  type TimeRange,
} from '../lib/timeRange'
import { formatDuration, groupTrips } from '../lib/trips'

const CATEGORIES: TelemetryCategory[] = ['SavedClips', 'RecentClips']
const VISIBLE_KEY = 'teslausb.map.visible'

function loadVisible(): Record<TelemetryCategory, boolean> {
  const all = { SavedClips: true, RecentClips: true }
  try {
    const stored = JSON.parse(localStorage.getItem(VISIBLE_KEY) ?? 'null') as Partial<Record<TelemetryCategory, boolean>> | null
    return { ...all, ...(stored ?? {}) }
  } catch {
    return all
  }
}

function LegendSwatch({ color, dashed }: { color: string, dashed?: boolean }) {
  return (
    <svg width="22" height="8" aria-hidden style={{ flex: 'none' }}>
      <line x1="2" y1="4" x2="20" y2="4" stroke="#111" strokeOpacity={dashed ? 0.15 : 0.35} strokeWidth="7" strokeLinecap="round" />
      <line x1="2" y1="4" x2="20" y2="4" stroke={color} strokeWidth="4" strokeLinecap={dashed ? 'butt' : 'round'}
        strokeOpacity={dashed ? 0.6 : 1} strokeDasharray={dashed ? '5 3' : undefined} />
    </svg>
  )
}

const controlStyle = {
  display: 'flex', alignItems: 'center', gap: 6, padding: '6px 10px',
  border: '1px solid var(--border)', borderRadius: 'var(--radius-sm)',
  background: 'var(--bg-2)', color: 'var(--fg-1)', cursor: 'pointer', fontSize: 'var(--ts-sm)',
} as const

export function TrackHistoryPage() {
  const search = useSearch({ from: '/map' }) as MapSearch
  const [history, setHistory] = useState<TelemetryHistory | null>(null)
  const [days, setDays] = useState<TelemetryDays | null>(null)
  const [error, setError] = useState('')
  const [loadedKey, setLoadedKey] = useState('')
  const [visible, setVisible] = useState(loadVisible)
  const [now, setNow] = useState(() => new Date())
  const [customOpen, setCustomOpen] = useState(false)
  const navigate = useNavigate()
  const retentionDays = days?.retention_days ?? history?.retention_days ?? 30
  const range = useMemo(() => resolveRange(search, now, retentionDays), [search, now, retentionDays])
  const [customFrom, setCustomFrom] = useState('')
  const [customTo, setCustomTo] = useState('')

  const [lookMode, setLookMode] = useState(false)
  const look = useMemo(() => parseLook(search.look), [search.look])
  const lookRadius = search.lr ?? 60
  // The Look around panel covers the bottom ~62% of the map.
  const lookFocus = useMemo(() => (look ? { lat: look.lat, lon: look.lon, visibleFraction: 0.38 } : null), [look])
  const [tripsOpen, setTripsOpen] = useState(true)
  const [replaying, setReplaying] = useState(false)
  const [cursor, setCursor] = useState<TelemetryCursor | null>(null)

  const setRange = useCallback((next: TimeRange) => {
    setCustomOpen(false)
    setReplaying(false)
    void navigate({ to: '/map', search: { from: toLocalInput(next.from), to: toLocalInput(next.to) } })
  }, [navigate])
  const setPreset = useCallback((id: string) => {
    setCustomOpen(false)
    setReplaying(false)
    setNow(new Date())
    void navigate({ to: '/map', search: { range: id } })
  }, [navigate])
  const lookAt = useCallback((lat: number, lon: number, metersPerPixel: number) => {
    // About 40 screen pixels around the click, at least 25 m (GPS is a few metres off).
    const radius = Math.round(Math.min(2000, Math.max(25, metersPerPixel * 40)))
    setReplaying(false)
    void navigate({ to: '/map', search: { ...search, look: `${lat.toFixed(6)},${lon.toFixed(6)}`, lr: radius } })
  }, [navigate, search])
  const closeLook = useCallback(() => {
    setLookMode(false)
    void navigate({ to: '/map', search: { ...search, look: undefined, lr: undefined } })
  }, [navigate, search])
  const selectTrip = useCallback((id: string | undefined) => {
    setReplaying(false)
    void navigate({ to: '/map', search: { ...search, trip: id } })
  }, [navigate, search])

  const toggle = useCallback((category: TelemetryCategory) => {
    setVisible(current => {
      const next = { ...current, [category]: !current[category] }
      try { localStorage.setItem(VISIBLE_KEY, JSON.stringify(next)) } catch { /* per-viewer convenience only */ }
      return next
    })
  }, [])

  const fromParam = toLocalInput(range.from)
  const toParam = toLocalInput(new Date(range.to.getTime() + 60_000)) // include the last minute
  const rangeKey = `${fromParam}/${toParam}`
  const loading = loadedKey !== rangeKey
  useEffect(() => {
    let cancelled = false
    fetch(`/api/telemetry/tracks?from=${encodeURIComponent(fromParam)}&to=${encodeURIComponent(toParam)}`)
      .then(response => {
        if (!response.ok) throw new Error('Could not load track history')
        return response.json() as Promise<TelemetryHistory>
      })
      .then(value => { if (!cancelled) { setHistory(value); setError('') } })
      .catch(reason => { if (!cancelled) setError(reason instanceof Error ? reason.message : 'Could not load track history') })
      .finally(() => { if (!cancelled) setLoadedKey(`${fromParam}/${toParam}`) })
    return () => { cancelled = true }
  }, [fromParam, toParam])

  const loadDays = useCallback(() => {
    fetch('/api/telemetry/days')
      .then(response => (response.ok ? response.json() as Promise<TelemetryDays> : null))
      .then(value => { if (value) setDays(value) })
      .catch(() => { /* the timeline is optional */ })
  }, [])
  useEffect(loadDays, [loadDays])

  const refresh = useCallback(() => {
    setNow(new Date()) // a relative range moves to the current time and refetches
    loadDays()
  }, [loadDays])

  const onOpen = useCallback((clip: TelemetryClip) => {
    navigate({ to: '/viewer', search: { clip: clip.id } })
  }, [navigate])

  const allClips = useMemo(
    () => (history?.clips ?? []).filter(clip => CATEGORIES.some(category => clip.id.startsWith(`${category}/`))),
    [history],
  )
  const counts = useMemo(() => {
    const result: Record<TelemetryCategory, number> = { SavedClips: 0, RecentClips: 0 }
    for (const clip of allClips) result[telemetryClipCategory(clip)] += 1
    return result
  }, [allClips])
  const trips = useMemo(() => groupTrips(allClips), [allClips])
  const selectedTrip = useMemo(() => trips.find(trip => trip.id === search.trip) ?? null, [trips, search.trip])
  const clips = useMemo(
    () => sortTracksForDrawing((selectedTrip?.clips ?? allClips).filter(clip => visible[telemetryClipCategory(clip)])),
    [allClips, selectedTrip, visible],
  )
  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%', minHeight: 0 }}>
      <header style={{
        display: 'flex', alignItems: 'center', gap: 'var(--sp-4)',
        padding: 'var(--sp-4) var(--sp-6)', background: 'var(--bg-1)',
        borderBottom: '1px solid var(--border)',
      }}>
        <Icon name="map-pin" size={20} />
        <div>
          <h1 style={{ margin: 0, fontSize: 'var(--ts-xl)' }}>Clip tracks</h1>
          <p style={{ margin: '3px 0 0', color: 'var(--fg-2)', fontSize: 'var(--ts-sm)' }}>
            {presetLabel(search) ?? formatRange(range)} · {allClips.length} tracks · {retentionDays}-day retention
            {loading ? ' · loading…' : ''}
          </p>
        </div>
        <div role="group" aria-label="Map legend" style={{
          display: 'flex', alignItems: 'center', gap: 'var(--sp-2)', flexWrap: 'wrap',
          fontSize: 'var(--ts-sm)', color: 'var(--fg-2)',
        }}>
          {CATEGORIES.map(category => (
            <button
              key={category}
              onClick={() => toggle(category)}
              aria-pressed={visible[category]}
              title={visible[category] ? `Hide ${TELEMETRY_CATEGORY_LABELS[category]} tracks` : `Show ${TELEMETRY_CATEGORY_LABELS[category]} tracks`}
              style={{
                display: 'flex', alignItems: 'center', gap: 6, padding: '5px 10px',
                border: '1px solid var(--border)', borderRadius: 999,
                background: visible[category] ? 'var(--bg-2)' : 'transparent',
                color: visible[category] ? 'var(--fg-1)' : 'var(--fg-2)',
                opacity: visible[category] ? 1 : 0.55, cursor: 'pointer',
                textDecoration: visible[category] ? 'none' : 'line-through',
              }}
            >
              <LegendSwatch color={TELEMETRY_CATEGORY_COLORS[category]} />
              {TELEMETRY_CATEGORY_LABELS[category]} · {counts[category]}
            </button>
          ))}
          <span style={{ display: 'flex', alignItems: 'center', gap: 6, paddingLeft: 4 }}
            title="Solid tracks are still on the Pi and open in the viewer when clicked">
            <LegendSwatch color="var(--fg-2)" dashed /> archived / not on Pi
          </span>
        </div>
        <div style={{ flex: 1 }} />
        <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--sp-2)', flexWrap: 'wrap' }}>
          <select
            aria-label="Time range"
            value={search.range ?? (search.from ? 'custom' : '7d')}
            onChange={event => {
              if (event.target.value === 'custom') {
                setCustomFrom(toLocalInput(range.from))
                setCustomTo(toLocalInput(range.to))
                setCustomOpen(true)
              } else {
                setPreset(event.target.value)
              }
            }}
            style={controlStyle}
          >
            {RANGE_PRESETS.map(preset => <option key={preset.id} value={preset.id}>{preset.label}</option>)}
            <option value="custom">{search.from && !search.range ? formatRange(range) : 'Custom range…'}</option>
          </select>
          <button
            onClick={() => setLookMode(on => !on)}
            aria-pressed={lookMode}
            title="Click the map to see the dashcam view of drives that passed there"
            style={{ ...controlStyle, ...(lookMode ? { borderColor: 'var(--accent)', color: 'var(--fg-0)' } : {}) }}
          >
            <Icon name="eye" size={14} /> Look around
          </button>
          <button onClick={() => setRange(zoomOut(range, new Date()))} style={controlStyle} aria-label="Zoom out time range" title="Show twice as long a range">
            <Icon name="zoom-out" size={14} /> Zoom out
          </button>
          <button onClick={refresh} style={controlStyle}>
            <Icon name="refresh-cw" size={14} /> Refresh
          </button>
        </div>
      </header>
      {customOpen && (
        <form
          onSubmit={event => {
            event.preventDefault()
            const from = new Date(customFrom)
            const to = new Date(customTo)
            if (!Number.isNaN(from.getTime()) && !Number.isNaN(to.getTime()) && to > from) setRange({ from, to })
          }}
          style={{
            display: 'flex', gap: 'var(--sp-3)', alignItems: 'center', flexWrap: 'wrap',
            padding: 'var(--sp-2) var(--sp-6)', background: 'var(--bg-1)', borderBottom: '1px solid var(--border)',
            fontSize: 'var(--ts-sm)', color: 'var(--fg-2)',
          }}
        >
          <label>From <input type="datetime-local" value={customFrom} onChange={event => setCustomFrom(event.target.value)} style={controlStyle} /></label>
          <label>To <input type="datetime-local" value={customTo} onChange={event => setCustomTo(event.target.value)} style={controlStyle} /></label>
          <button type="submit" style={controlStyle}>Apply</button>
          <button type="button" onClick={() => setCustomOpen(false)} style={controlStyle}>Cancel</button>
        </form>
      )}
      {history?.enabled && (
        <TimelineStrip
          days={days?.days ?? []}
          range={range}
          onSelect={setRange}
          onZoomOut={() => setRange(zoomOut(range, new Date()))}
        />
      )}
      <div style={{ flex: 1, minHeight: 0, position: 'relative' }}>
        {history?.enabled && allClips.length > 0 ? (
          <>
            {history.style_url ? (
              <VectorTelemetryMap
                clips={clips} onOpen={lookMode ? undefined : onOpen} styleUrl={history.style_url} cursor={cursor}
                onMapClick={lookMode ? lookAt : undefined} mapCursor={lookMode ? 'crosshair' : undefined}
                focus={lookFocus}
              />
            ) : (
              <TelemetryMap
                clips={clips} onOpen={lookMode ? undefined : onOpen} tileUrl={history.tile_url} cursor={cursor}
                onMapClick={lookMode ? lookAt : undefined} mapCursor={lookMode ? 'crosshair' : undefined}
                focus={lookFocus}
              />
            )}
            {lookMode && !look && (
              <div style={{
                position: 'absolute', left: '50%', top: 12, transform: 'translateX(-50%)', zIndex: 1000,
                padding: '6px 12px', borderRadius: 999, background: 'var(--bg-1)', border: '1px solid var(--accent)',
                color: 'var(--fg-1)', fontSize: 'var(--ts-sm)',
              }}>
                Click the map where you want to look around
              </div>
            )}
            {look && (
              <LookAroundPanel
                key={search.look}
                lat={look.lat}
                lon={look.lon}
                radius={lookRadius}
                onCursor={setCursor}
                onClose={closeLook}
              />
            )}
            {!look && (
            <aside
              aria-label="Trips"
              style={{
                position: 'absolute', top: 12, right: 12, zIndex: 1000,
                width: 'min(300px, calc(100% - 24px))', maxHeight: 'calc(100% - 24px)',
                display: 'flex', flexDirection: 'column',
                background: 'var(--bg-1)', border: '1px solid var(--border)', borderRadius: 'var(--radius-md, 10px)',
                boxShadow: '0 8px 24px rgba(0,0,0,.3)', fontSize: 'var(--ts-sm)',
              }}
            >
              <button
                onClick={() => setTripsOpen(open => !open)}
                aria-expanded={tripsOpen}
                style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '10px 12px', background: 'none', border: 0, color: 'var(--fg-0)', cursor: 'pointer', textAlign: 'left' }}
              >
                <strong>Trips · {trips.length}</strong>
                <span style={{ flex: 1 }} />
                <Icon name={tripsOpen ? 'chevron-up' : 'chevron-down'} size={14} />
              </button>
              {selectedTrip && (
                <div style={{ display: 'flex', gap: 6, padding: '0 12px 10px', flexWrap: 'wrap' }}>
                  <button onClick={() => setReplaying(true)} style={controlStyle} disabled={replaying}>
                    <Icon name="play" size={12} /> Replay trip
                  </button>
                  <button onClick={() => selectTrip(undefined)} style={controlStyle}>Show all trips</button>
                </div>
              )}
              {tripsOpen && (
                <ol style={{ listStyle: 'none', margin: 0, padding: '0 6px 6px', overflow: 'auto' }}>
                  {trips.map(trip => {
                    const selected = trip.id === selectedTrip?.id
                    return (
                      <li key={trip.id}>
                        <button
                          onClick={() => selectTrip(selected ? undefined : trip.id)}
                          aria-pressed={selected}
                          style={{
                            width: '100%', textAlign: 'left', padding: '8px 8px', borderRadius: 8, cursor: 'pointer',
                            border: `1px solid ${selected ? 'var(--accent)' : 'transparent'}`,
                            background: selected ? 'var(--bg-2)' : 'transparent', color: 'var(--fg-1)',
                          }}
                        >
                          <div style={{ color: 'var(--fg-0)' }}>
                            {trip.start.toLocaleDateString(undefined, { weekday: 'short', day: 'numeric', month: 'short' })}
                            {' · '}
                            {trip.start.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' })}
                            –{trip.end.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' })}
                          </div>
                          <div style={{ color: 'var(--fg-2)', fontSize: 'var(--ts-xs)' }}>
                            {formatDuration(trip.drivingSeconds)} · {trip.distanceKm.toFixed(1)} km
                            {trip.saved > 0 ? ` · ${trip.saved} saved` : ''}
                          </div>
                        </button>
                      </li>
                    )
                  })}
                </ol>
              )}
            </aside>
            )}
            {replaying && selectedTrip && !look && (
              <TripReplay key={selectedTrip.id} trip={selectedTrip} onCursor={setCursor} onClose={() => setReplaying(false)} />
            )}
          </>
        ) : (
          <div style={{
            height: '100%', display: 'grid', placeItems: 'center', padding: 24,
            color: 'var(--fg-2)', textAlign: 'center',
          }}>
            <div>
              <Icon name="map-pin" size={36} />
              <h2 style={{ color: 'var(--fg-1)' }}>
                {error || (!history
                  ? 'Loading…'
                  : !history.enabled
                    ? 'GPS track history is disabled'
                    : (days?.total_clips ?? 0) > 0 ? 'No tracks in this range' : 'No GPS tracks indexed yet')}
              </h2>
              {!error && history && (
                <p>
                  {!history.enabled
                    ? 'Enable it in Settings to index clips when the car is home.'
                    : (days?.total_clips ?? 0) > 0
                      ? 'Choose a longer range, zoom out, or drag across the timeline.'
                      : history.while_driving
                        ? 'New clips are indexed after every snapshot, about one snapshot interval after recording.'
                        : 'New SavedClips and RecentClips are indexed when the car is home on Wi-Fi.'}
                </p>
              )}
              {!history?.enabled && (
                <Link to="/settings" style={{ color: 'var(--accent)' }}>Open Settings</Link>
              )}
            </div>
          </div>
        )}
      </div>
    </div>
  )
}
