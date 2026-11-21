import { createFileRoute } from '@tanstack/react-router'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import {
  ls, videoUrl, zipUrl, CAMERA_ORDER, CAMERA_LABELS,
  formatTime,
  type CameraAngle, type CameraFiles, type ClipEvent,
} from '../lib/clips'
import { actualSizeBox, groupCameraMinutes } from '../lib/viewer'
import { Icon } from '../components/Icon'
import { TelemetryMap } from '../components/TelemetryMap'
import { VehicleTelemetry } from '../components/VehicleTelemetry'
import { VectorTelemetryMap } from '../components/VectorTelemetryMap'
import { useStatus } from '../hooks/useStatus'
import type { PlaybackControls, PlayerHandles, ScrubHandlers, TelemetryClip } from '../lib/telemetry'

export const Route = createFileRoute('/viewer')({
  // clip: a file to open (its folder and minute); t: seconds into that minute
  // to start at, e.g. from the map's Look around or trip replay.
  validateSearch: (search): { clip?: string, t?: number } => ({
    clip: typeof search.clip === 'string' ? search.clip : undefined,
    t: Number.isFinite(Number(search.t)) && Number(search.t) > 0 ? Number(search.t) : undefined,
  }),
  component: ViewerPage,
})

/* ─── types ──────────────────────────────────────────────────────────────── */

type Layout = 'classic' | 'overhead' | 'mobile' | 'focus' | 'board'

const LAYOUTS: { id: Layout; name: string; tiles: string[]; hl: string }[] = [
  { id: 'classic',  name: 'Classic',  tiles: ['map','front','info','left','back','right'], hl: 'front' },
  { id: 'overhead', name: 'Overhead', tiles: ['left','front','right','map','back','info'], hl: 'front' },
  { id: 'mobile',   name: 'Mobile',   tiles: ['front','left','right','back','map','info'], hl: 'front' },
  { id: 'focus',    name: 'Focus',    tiles: ['focus','left','back','right','map','info'], hl: 'focus' },
  { id: 'board',    name: 'Board',    tiles: ['front','info','left','back','right','map'], hl: 'info' },
]

/* ─── page ───────────────────────────────────────────────────────────────── */

function ViewerPage() {
  const [events, setEvents] = useState<ClipEvent[]>([])
  const [loading, setLoading] = useState(true)
  const [selected, setSelected] = useState<ClipEvent | null>(null)
  const [minutes, setMinutes] = useState<ReturnType<typeof groupCameraMinutes>>([])
  const [minute, setMinute] = useState('')
  const cameraFiles = minutes.find(item => item.timestamp === minute)?.files ?? {}
  const [filesLoading, setFilesLoading] = useState(false)
  const [layout, setLayout] = useState<Layout>('classic')
  const [mirrored, setMirrored] = useState(true)
  const [focus, setFocus] = useState<CameraAngle>('front')

  // Registry of the visible <video> elements rendered by CameraGrid.
  // Multiple tiles can show the same angle in the focus layout, so refs
  // are stored per-element. PlayerArea drives playback through these refs —
  // the visible tiles ARE the players (no hidden duplicate videos).
  const videoRefs = useRef<Map<HTMLVideoElement, CameraAngle>>(new Map())
  const registerVideo = useCallback((el: HTMLVideoElement, angle: CameraAngle) => {
    videoRefs.current.set(el, angle)
    return () => { videoRefs.current.delete(el) }
  }, [])
  const [muted, setMuted] = useState(true)
  const [telemetryClip, setTelemetryClip] = useState<TelemetryClip | null>(null)
  // PlayerArea owns the clock; it registers scrub handlers here so the map's
  // draggable dot (in CameraGrid) can pause, seek and resume playback.
  const scrubTarget = useRef<PlayerHandles | null>(null)
  const registerScrub = useCallback((handlers: PlayerHandles | null) => {
    scrubTarget.current = handlers
  }, [])
  const onScrub = useMemo<ScrubHandlers>(() => ({
    begin: () => scrubTarget.current?.begin(),
    move: seconds => scrubTarget.current?.move(seconds),
    end: seconds => scrubTarget.current?.end(seconds),
  }), [])
  const playback = useMemo<PlaybackControls>(() => ({
    togglePlay: () => scrubTarget.current?.togglePlay(),
    seek: seconds => scrubTarget.current?.seek(seconds),
  }), [])
  const stageRef = useRef<HTMLDivElement>(null)
  const eventsLoadedRef = useRef(false)
  const navigate = Route.useNavigate()
  const requestedClip = Route.useSearch().clip
  const requestedTime = Route.useSearch().t
  // Where the requested minute starts playing; dropped when another minute is chosen.
  const [startAt, setStartAt] = useState<{ minute: string, t: number } | null>(null)
  const chooseMinute = useCallback((value: string) => {
    setStartAt(null)
    setMinute(value)
  }, [])
  const handleSelectEvent = useCallback((event: ClipEvent) => {
    setSelected(event)
    if (requestedClip) {
      void navigate({ to: '/viewer', search: {}, replace: true })
    }
  }, [navigate, requestedClip])
  const playbackTime = useCallback(() => {
    for (const [video, angle] of videoRefs.current) {
      if (angle === 'front') return video.currentTime
    }
    const first = videoRefs.current.keys().next().value
    return first ? first.currentTime : null
  }, [])

  // Load event list once
  useEffect(() => {
    if (eventsLoadedRef.current && !requestedClip) return
    let cancelled = false
    async function load() {
      setLoading(true)
      try {
        const entries = await ls('TeslaCam/')
        if (cancelled) return
        const evts: ClipEvent[] = []
        for (const e of entries) {
          if (e.type === 'subdir') {
            const slash = e.name.indexOf('/')
            const category = slash >= 0 ? e.name.slice(0, slash) : e.name
            const label = slash >= 0 ? e.name.slice(slash + 1) : e.name
            evts.push({
              label,
              path: `TeslaCam/&${e.name}`,
              folderPath: `TeslaCam/${e.name}`,
              category,
            })
          }
        }
        evts.sort((a, b) => b.label.localeCompare(a.label))
        eventsLoadedRef.current = true
        setEvents(evts)
        if (requestedClip) {
          const requestedFolder = requestedClip.startsWith('SavedClips/') || requestedClip.startsWith('RecentClips/') || requestedClip.startsWith('SentryClips/')
            ? requestedClip.split('/').slice(0, 2).join('/')
            : requestedClip
          setSelected(evts.find(event => event.folderPath === `TeslaCam/${requestedFolder}`) ?? null)
        }
      } catch {
        // show empty state
      } finally {
        if (!cancelled) setLoading(false)
      }
    }
    load()
    return () => { cancelled = true }
  }, [requestedClip])

  // Load camera files when selection changes
  useEffect(() => {
    if (!selected) return
    let cancelled = false
    async function load() {
      // Reset so CameraGrid never renders the previous clip's files
      // against the new folderPath while the fetch is in flight.
      setMinutes([])
      setFilesLoading(true)
      try {
        const entries = await ls(selected!.path)
        if (cancelled) return
        const grouped = groupCameraMinutes(entries)
        const selectedFolder = selected!.folderPath.replace(/^TeslaCam\//, '')
        const requestedFile = requestedClip?.startsWith(`${selectedFolder}/`) ? requestedClip.split('/').pop() : undefined
        const requestedMinute = requestedFile ? grouped.find(item => Object.values(item.files).includes(requestedFile)) : undefined
        setMinutes(grouped)
        setMinute(requestedMinute?.timestamp ?? grouped[0]?.timestamp ?? '')
        setStartAt(requestedMinute && requestedTime ? { minute: requestedMinute.timestamp, t: requestedTime } : null)
      } catch {
        if (!cancelled) setMinutes([])
      } finally {
        if (!cancelled) setFilesLoading(false)
      }
    }
    load()
    return () => { cancelled = true; setMinutes([]) }
  }, [selected, requestedClip, requestedTime])

  useEffect(() => {
    if (!selected || !['SavedClips', 'RecentClips'].includes(selected.category)) return
    const selectedFolder = selected.folderPath.replace(/^TeslaCam\//, '')
    if (!cameraFiles.front) return
    const id = `${selectedFolder}/${cameraFiles.front}`
    let cancelled = false
    fetch(`/api/telemetry/clip?id=${encodeURIComponent(id)}`)
      .then(response => response.ok ? response.json() as Promise<TelemetryClip> : null)
      .then(clip => { if (!cancelled) setTelemetryClip(clip) })
      .catch(() => {})
    return () => { cancelled = true; setTelemetryClip(null) }
  }, [selected, requestedClip, cameraFiles.front])

  const angles = CAMERA_ORDER.filter(a => a in cameraFiles)

  const downloadClip = useCallback(() => {
    if (selected) window.location.href = zipUrl(selected.folderPath)
  }, [selected])

  const togglePip = useCallback(async () => {
    if (!('pictureInPictureEnabled' in document) || !document.pictureInPictureEnabled) return
    try {
      if (document.pictureInPictureElement) {
        await document.exitPictureInPicture()
        return
      }
      await leadRegisteredVideo(videoRefs)?.requestPictureInPicture()
    } catch { /* PiP rejected (e.g. video not ready) */ }
  }, [])

  const toggleFullscreen = useCallback(() => {
    const el = stageRef.current
    if (!el || !document.fullscreenEnabled) return
    if (document.fullscreenElement) {
      document.exitFullscreen().catch(() => {})
    } else {
      el.requestFullscreen().catch(() => {})
    }
  }, [])

  // Page-level shortcuts: F toggles fullscreen, digits 1-5 switch layout.
  // (Space / arrows / M live in PlayerArea, which only exists while a clip is open.)
  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.target instanceof HTMLInputElement || e.target instanceof HTMLTextAreaElement) return
      if (e.code === 'KeyF') { toggleFullscreen(); return }
      const digit = /^Digit([1-5])$/.exec(e.code)
      if (digit) setLayout(LAYOUTS[Number(digit[1]) - 1].id)
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [toggleFullscreen])

  return (
    <div style={{
      display: 'grid',
      gridTemplateColumns: '260px 1fr 280px',
      gap: 1,
      background: 'var(--border)',
      height: '100%',
      minHeight: 0,
    }}>
      <EventSidebar
        events={events}
        loading={loading}
        selected={selected}
        onSelect={handleSelectEvent}
      />
      <div ref={stageRef} className="stage panel" style={{
        display: 'grid',
        gridTemplateRows: 'auto 1fr auto',
        minHeight: 0,
        background: 'var(--bg-1)',
        overflow: 'hidden',
      }}>
        {!selected ? (
          <NoSelection />
        ) : filesLoading ? (
          <LoadingCameras />
        ) : angles.length === 0 ? (
          <div style={{
            gridRow: '1 / -1',
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            color: 'var(--fg-2)',
          }}>
            <div style={{ textAlign: 'center' }}>
              <div style={{ fontSize: 40, marginBottom: 'var(--sp-3)' }}>📹</div>
              <div style={{ fontSize: 'var(--ts-md)' }}>No camera files found in this clip</div>
            </div>
          </div>
        ) : (
          <>
            <StageHead
              clipTime={selected.label}
              category={selected.category}
              onDownload={downloadClip}
              onPip={togglePip}
              onFullscreen={toggleFullscreen}
            />
            <CameraGrid
              key={`cameras:${selected.folderPath}/${minute}`}
              folderPath={selected.folderPath}
              cameraFiles={cameraFiles}
              layout={layout}
              mirrored={mirrored}
              focus={focus}
              setFocus={angle => { setFocus(angle); setLayout('focus') }}
              registerVideo={registerVideo}
              muted={muted}
              telemetryClip={telemetryClip}
              playbackTime={playbackTime}
              onScrub={onScrub}
              playback={playback}
            />
            <PlayerArea
              key={`${selected.folderPath}/${minute}`}
              minutes={minutes.map(item => item.timestamp)}
              minute={minute}
              setMinute={chooseMinute}
              initialTime={startAt?.minute === minute ? startAt.t : 0}
              videoRefs={videoRefs}
              muted={muted}
              setMuted={setMuted}
              registerScrub={registerScrub}
            />
          </>
        )}
      </div>
      <RightRail
        layout={layout}
        setLayout={setLayout}
        mirrored={mirrored}
        setMirrored={setMirrored}
        focus={focus}
        setFocus={setFocus}
      />
    </div>
  )
}

/* ─── sidebar ────────────────────────────────────────────────────────────── */

function EventSidebar({
  events, loading, selected, onSelect,
}: {
  events: ClipEvent[]
  loading: boolean
  selected: ClipEvent | null
  onSelect: (e: ClipEvent) => void
}) {
  const categories = Array.from(new Set(events.map(e => e.category)))

  return (
    <div style={{
      background: 'var(--bg-1)',
      minHeight: 0,
      overflow: 'hidden',
      display: 'flex',
      flexDirection: 'column',
    }}>
      <div style={{
        padding: 'var(--sp-4)',
        borderBottom: '1px solid var(--border)',
      }}>
        <h3 style={{
          margin: 0,
          fontSize: 'var(--ts-lg)',
          fontWeight: 600,
        }}>Clips</h3>
        <div style={{
          marginTop: 4,
          fontSize: 'var(--ts-xs)',
          color: 'var(--fg-2)',
          letterSpacing: '.04em',
          textTransform: 'uppercase',
        }}>
          {events.length} clips
        </div>
      </div>
      <div style={{ flex: 1, overflowY: 'auto', padding: 'var(--sp-2) 0' }}>
        {loading ? (
          <div style={{ padding: 'var(--sp-4)', color: 'var(--fg-2)', fontSize: 'var(--ts-sm)' }}>
            Loading…
          </div>
        ) : events.length === 0 ? (
          <div style={{ padding: 'var(--sp-4)', color: 'var(--fg-2)', fontSize: 'var(--ts-sm)' }}>
            No clips found
          </div>
        ) : (
          categories.map(cat => (
            <div key={cat}>
              <div style={{
                padding: 'var(--sp-4) var(--sp-4) var(--sp-2)',
                fontSize: 'var(--ts-xs)',
                fontWeight: 600,
                color: 'var(--fg-2)',
                letterSpacing: '.08em',
                textTransform: 'uppercase',
                position: 'sticky',
                top: 0,
                background: 'linear-gradient(var(--bg-1) 70%, transparent)',
                zIndex: 1,
              }}>
                {cat}
              </div>
              {events.filter(e => e.category === cat).map(e => (
                <button
                  key={e.path}
                  onClick={() => onSelect(e)}
                  style={{
                    display: 'grid',
                    gridTemplateColumns: '1fr auto',
                    gap: 'var(--sp-3)',
                    width: '100%',
                    textAlign: 'left',
                    padding: 'var(--sp-2) var(--sp-4)',
                    alignItems: 'center',
                    cursor: 'pointer',
                    borderLeft: '2px solid transparent',
                    background: selected?.path === e.path ? 'var(--bg-2)' : 'transparent',
                    borderLeftColor: selected?.path === e.path ? 'var(--accent)' : 'transparent',
                    transition: 'background .1s',
                  }}
                >
                  <div>
                    <div style={{
                      fontFamily: 'var(--font-mono)',
                      fontSize: 'var(--ts-md)',
                      fontWeight: 500,
                      letterSpacing: '.01em',
                      color: selected?.path === e.path ? 'var(--fg-0)' : 'var(--fg-1)',
                    }}>
                      {e.label}
                    </div>
                  </div>
                  <span style={{
                    fontFamily: 'var(--font-mono)',
                    fontSize: 10,
                    padding: '2px 6px',
                    borderRadius: 4,
                    background: 'var(--bg-3)',
                    color: 'var(--fg-1)',
                    letterSpacing: '.04em',
                  }}>
                    {cat.slice(0,4).toUpperCase()}
                  </span>
                </button>
              ))}
            </div>
          ))
        )}
      </div>
    </div>
  )
}

/* ─── stage head ─────────────────────────────────────────────────────────── */

function StageHead({ clipTime, category, onDownload, onPip, onFullscreen }: {
  clipTime: string
  category: string
  onDownload: () => void
  onPip: () => void
  onFullscreen: () => void
}) {
  const isSentry = category.toLowerCase().includes('sentry') || category.toLowerCase().includes('saved')
  return (
    <div style={{
      padding: 'var(--sp-3) var(--sp-5)',
      borderBottom: '1px solid var(--border)',
      display: 'flex',
      alignItems: 'center',
      gap: 'var(--sp-4)',
      background: 'var(--bg-1)',
    }}>
      <div style={{ flex: 1, minWidth: 0 }}>
        <div style={{
          fontFamily: 'var(--font-mono)',
          fontSize: 'var(--ts-lg)',
          fontWeight: 600,
        }}>
          {clipTime}
        </div>
        <div style={{
          fontSize: 'var(--ts-xs)',
          color: 'var(--fg-2)',
          marginTop: 2,
          letterSpacing: '.03em',
          textTransform: 'uppercase',
        }}>
          {category}
        </div>
      </div>
      {isSentry && (
        <span style={{
          display: 'inline-flex',
          alignItems: 'center',
          gap: 6,
          fontFamily: 'var(--font-mono)',
          fontSize: 'var(--ts-xs)',
          color: 'var(--accent)',
          padding: '4px 10px',
          borderRadius: 'var(--radius-pill)',
          background: 'rgba(225,29,42,.1)',
          letterSpacing: '.04em',
          flexShrink: 0,
        }}>
          <span style={{
            width: 6, height: 6, borderRadius: '50%',
            background: 'var(--accent)',
            animation: 'pulse 1.4s ease-in-out infinite',
          }} />
          SENTRY EVENT
        </span>
      )}
      <div style={{ display: 'flex', gap: 6, flexShrink: 0 }}>
        <IconButton title="Download clip" onClick={onDownload}>
          <Icon name="download" />
        </IconButton>
        <IconButton title="Picture-in-picture" onClick={onPip}>
          <Icon name="pip" />
        </IconButton>
        <IconButton title="Fullscreen" primary onClick={onFullscreen}>
          <Icon name="fullscreen" />
        </IconButton>
      </div>
    </div>
  )
}

function IconButton({ children, title, primary, onClick }: {
  children: React.ReactNode
  title: string
  primary?: boolean
  onClick?: () => void
}) {
  return (
    <button
      onClick={onClick}
      title={title}
      style={{
        width: 32, height: 32,
        display: 'grid', placeItems: 'center',
        borderRadius: 'var(--radius-sm)',
        color: primary ? 'var(--accent-ink)' : 'var(--fg-1)',
        background: primary ? 'var(--accent)' : 'transparent',
        border: 'none',
        cursor: 'pointer',
        transition: 'background .1s',
      }}
    >
      {children}
    </button>
  )
}

/* ─── camera grid ────────────────────────────────────────────────────────── */

function CameraGrid({
  folderPath,
  cameraFiles,
  layout,
  mirrored,
  focus,
  setFocus,
  registerVideo,
  muted,
  telemetryClip,
  playbackTime,
  onScrub,
  playback,
}: {
  folderPath: string
  cameraFiles: CameraFiles
  layout: Layout
  mirrored: boolean
  focus: CameraAngle
  setFocus: (a: CameraAngle) => void
  registerVideo: (el: HTMLVideoElement, angle: CameraAngle) => () => void
  muted: boolean
  telemetryClip: TelemetryClip | null
  playbackTime: () => number | null
  onScrub: ScrubHandlers
  playback: PlaybackControls
}) {
  const layoutDef = LAYOUTS.find(l => l.id === layout)!
  const tiles = layoutDef.tiles

  // Clicking a camera shows it at its actual resolution (one CSS pixel per
  // video pixel, scaled down only to fit the window). The same <video> element
  // moves into the overlay, so it stays registered and in sync.
  const [actual, setActual] = useState<{ tile: string, width: number, height: number, video: HTMLVideoElement } | null>(null)
  const [viewport, setViewport] = useState({ width: 0, height: 0 })
  const shown = actual && tiles.includes(actual.tile) ? actual : null
  useEffect(() => {
    if (!shown) return
    const onResize = () => setViewport({ width: window.innerWidth, height: window.innerHeight })
    const onKey = (event: KeyboardEvent) => { if (event.key === 'Escape') setActual(null) }
    window.addEventListener('resize', onResize)
    window.addEventListener('keydown', onKey)
    return () => {
      window.removeEventListener('resize', onResize)
      window.removeEventListener('keydown', onKey)
    }
  }, [shown])
  const toggleActual = (tile: string, video: HTMLVideoElement) => {
    if (shown?.tile === tile) {
      setActual(null)
      return
    }
    if (!video.videoWidth || !video.videoHeight) return // metadata not loaded yet
    setViewport({ width: window.innerWidth, height: window.innerHeight })
    setActual({ tile, width: video.videoWidth, height: video.videoHeight, video })
  }
  const box = shown ? actualSizeBox(shown.width, shown.height, viewport.width, viewport.height) : null

  const gridAreas: Record<string, string> = {
    classic: '"map front info" "left back right"',
    overhead: '"left front right" "map back info"',
    mobile: '"front front" "left right" "back back" "map info"',
    focus: '"focus focus focus focus focus" "left back right map info"',
    board: '"front front info info" "left back info info" "right map info info"',
  }

  const gridRows: Record<string, string> = {
    classic: '1fr 1fr',
    overhead: '1fr 1fr',
    mobile: '1.2fr 1fr 1fr .8fr',
    focus: '3fr 1fr',
    board: '1fr 1fr 1fr',
  }

  const gridCols: Record<string, string> = {
    classic: '1fr 1.3fr 1fr',
    overhead: '1fr 1.3fr 1fr',
    mobile: '1fr 1fr',
    focus: 'repeat(5, 1fr)',
    board: 'repeat(4, 1fr)',
  }

  const tileDefs: Record<string, { label: string; angle?: CameraAngle; flipped?: boolean; kind?: 'map' | 'info' }> = {
    front:   { label: 'FRONT', angle: 'front' },
    back:    { label: 'BACK', angle: 'back', flipped: mirrored },
    left:    { label: 'LEFT · REPEATER', angle: 'left_repeater', flipped: mirrored },
    right:   { label: 'RIGHT · REPEATER', angle: 'right_repeater', flipped: mirrored },
    focus:   { label: focus.toUpperCase(), angle: focus },
    map:     { label: 'MAP', kind: 'map' },
    info:    { label: 'INFO', kind: 'info' },
  }

  return (
    <div style={{
      padding: 'var(--sp-3)',
      display: 'grid',
      gap: 'var(--sp-2)',
      minHeight: 0,
      background: 'var(--bg-0)',
      gridTemplateAreas: gridAreas[layout],
      gridTemplateRows: gridRows[layout],
      gridTemplateColumns: gridCols[layout],
    }}>
      {shown && (
        <div
          data-testid="actual-size-backdrop"
          onClick={() => setActual(null)}
          style={{ position: 'fixed', inset: 0, background: 'rgba(0,0,0,.78)', zIndex: 2000, cursor: 'zoom-out' }}
        />
      )}
      {tiles.map(t => {
        const def = tileDefs[t]
        if (!def) return null
        if (def.kind === 'map') {
          return (
            <div key={t} style={{
              gridArea: t,
              background: 'var(--bg-2)',
              borderRadius: 'var(--radius-md)',
              overflow: 'hidden',
              position: 'relative',
              minHeight: 0,
              border: '1px solid var(--border)',
              display: 'grid',
              placeItems: 'center',
              color: 'var(--fg-2)',
              fontSize: 'var(--ts-sm)',
            }}>
              {telemetryClip?.track?.length ? (
                telemetryClip.style_url ? (
                  <VectorTelemetryMap clips={[telemetryClip]} playbackTime={playbackTime} styleUrl={telemetryClip.style_url} onScrub={onScrub} />
                ) : (
                  <TelemetryMap clips={[telemetryClip]} playbackTime={playbackTime} tileUrl={telemetryClip.tile_url} onScrub={onScrub} />
                )
              ) : (
                <>
                  <Icon name="map-pin" size={24} />
                  <span style={{
                    position: 'absolute',
                    bottom: 8, left: 8,
                    fontFamily: 'var(--font-mono)',
                    fontSize: 10,
                    letterSpacing: '.08em',
                    textTransform: 'uppercase',
                    color: 'var(--fg-2)',
                    background: 'var(--bg-1)',
                    padding: '3px 8px',
                    borderRadius: 4,
                    border: '1px solid var(--border)',
                  }}>NO GPS TRACK</span>
                </>
              )}
            </div>
          )
        }
        if (def.kind === 'info') {
          return (
            <div key={t} style={{
              gridArea: t,
              background: 'var(--bg-1)',
              borderRadius: 'var(--radius-md)',
              overflow: 'hidden',
              position: 'relative',
              minHeight: 0,
              border: '1px solid var(--border)',
              padding: 'var(--sp-4)',
              color: 'var(--fg-0)',
              fontSize: 'var(--ts-sm)',
            }}>
              <div style={{
                fontSize: 'var(--ts-xs)',
                color: 'var(--fg-2)',
                fontWeight: 500,
                letterSpacing: '.1em',
                textTransform: 'uppercase',
                marginBottom: 'var(--sp-2)',
              }}>Telemetry</div>
              <VehicleTelemetry key={telemetryClip?.id ?? 'none'} clip={telemetryClip} playbackTime={playbackTime} />
            </div>
          )
        }
        // video tile
        const hasFile = def.angle && cameraFiles[def.angle]
        const enlarged = shown?.tile === t && box !== null
        return (
          <div key={t} data-testid={`camera-tile-${t}`} style={{
            gridArea: t,
            background: '#000',
            borderRadius: 'var(--radius-md)',
            overflow: 'hidden',
            position: 'relative',
            minHeight: 0,
            border: '1px solid var(--border)',
            ...(enlarged ? {
              position: 'fixed',
              left: '50%',
              top: '50%',
              transform: 'translate(-50%, -50%)',
              width: box.width,
              height: box.height,
              zIndex: 2001,
              border: 'none', // an outline takes no space, so the video gets every pixel
              outline: '1px solid var(--accent)',
              boxShadow: '0 16px 48px rgba(0,0,0,.6)',
            } : {}),
          }}>
            {hasFile ? (
              <video
                ref={el => (el && def.angle ? registerVideo(el, def.angle) : undefined)}
                src={videoUrl(folderPath, cameraFiles[def.angle!]!)}
                muted={muted}
                playsInline
                preload="metadata"
                onClick={event => toggleActual(t, event.currentTarget)}
                title={enlarged ? 'Back to the grid (Esc)' : 'Show at actual size'}
                style={{
                  width: '100%',
                  height: '100%',
                  objectFit: 'contain',
                  display: 'block',
                  cursor: enlarged ? 'zoom-out' : 'zoom-in',
                  transform: def.flipped ? 'scaleX(-1)' : undefined,
                }}
              />
            ) : (
              <div style={{
                width: '100%',
                height: '100%',
                display: 'grid',
                placeItems: 'center',
                color: 'var(--fg-3)',
                fontSize: 'var(--ts-sm)',
              }}>
                No video
              </div>
            )}
            <div style={{
              position: 'absolute',
              top: 8, left: 8,
              fontFamily: 'var(--font-mono)',
              fontSize: 'var(--ts-xs)',
              letterSpacing: '.06em',
              textTransform: 'uppercase',
              color: 'rgba(255,255,255,.8)',
              background: 'rgba(0,0,0,.5)',
              padding: '3px 8px',
              borderRadius: 4,
              backdropFilter: 'blur(8px)',
            }}>
              {def.label}
              {enlarged && shown && ` · ${shown.width}×${shown.height} · ${Math.round(box.scale * 100)}%`}
            </div>
            {enlarged && (
              <ActualSizeOverlay
                video={shown?.video ?? null}
                playback={playback}
                telemetryClip={telemetryClip}
                playbackTime={playbackTime}
              />
            )}
            {def.angle && !enlarged && (
              <button
                onClick={() => def.angle && setFocus(def.angle)}
                style={{
                  position: 'absolute',
                  top: 8, right: 8,
                  width: 28, height: 28,
                  background: 'rgba(0,0,0,.6)',
                  color: '#fff',
                  borderRadius: 4,
                  display: 'grid',
                  placeItems: 'center',
                  opacity: 0,
                  transition: 'opacity .15s',
                  border: 'none',
                  cursor: 'pointer',
                  backdropFilter: 'blur(8px)',
                }}
                onMouseEnter={e => { (e.currentTarget as HTMLButtonElement).style.opacity = '1' }}
                onMouseLeave={e => { (e.currentTarget as HTMLButtonElement).style.opacity = '0' }}
                title="Expand"
              >
                <Icon name="expand" size={14} />
              </button>
            )}
          </div>
        )
      })}
    </div>
  )
}

/* ─── actual-size overlays ───────────────────────────────────────────────── */

const OVERLAY_IDLE_MS = 2500

function formatClock(seconds: number): string {
  const whole = Math.max(0, Math.floor(seconds))
  return `${Math.floor(whole / 60)}:${String(whole % 60).padStart(2, '0')}`
}

/**
 * Controls drawn over a camera shown at actual size: a playback bar along the
 * bottom (fades after a few seconds without the mouse moving) and a compact
 * telemetry panel top right. They drive the same PlayerArea transport, so
 * every camera stays in sync.
 */
function ActualSizeOverlay({ video, playback, telemetryClip, playbackTime }: {
  video: HTMLVideoElement | null
  playback: PlaybackControls
  telemetryClip: TelemetryClip | null
  playbackTime: () => number | null
}) {
  const [state, setState] = useState({ time: 0, duration: 0, paused: true })
  const [active, setActive] = useState(true)
  const idleTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)

  useEffect(() => {
    if (!video) return
    const read = () => setState({ time: video.currentTime, duration: Number.isFinite(video.duration) ? video.duration : 0, paused: video.paused })
    read()
    const timer = setInterval(read, 200)
    return () => clearInterval(timer)
  }, [video])

  const wake = useCallback(() => {
    setActive(true)
    clearTimeout(idleTimer.current)
    idleTimer.current = setTimeout(() => setActive(false), OVERLAY_IDLE_MS)
  }, [])
  useEffect(() => {
    const parent = video?.parentElement
    if (!parent) return
    idleTimer.current = setTimeout(() => setActive(false), OVERLAY_IDLE_MS)
    parent.addEventListener('pointermove', wake)
    return () => {
      parent.removeEventListener('pointermove', wake)
      clearTimeout(idleTimer.current)
    }
  }, [video, wake])

  const visible = active || state.paused
  const stop = (event: React.SyntheticEvent) => event.stopPropagation()
  const button = {
    display: 'grid', placeItems: 'center', width: 34, height: 34, borderRadius: '50%',
    border: '1px solid rgba(255,255,255,.25)', background: 'rgba(0,0,0,.45)', color: '#fff', cursor: 'pointer',
  } as const

  return (
    <>
      {telemetryClip?.track?.length ? (
        <div
          data-testid="actual-size-telemetry"
          onClick={stop}
          style={{
            position: 'absolute', top: 8, right: 8, width: 230, padding: '10px 12px',
            background: 'rgba(10,10,12,.62)', backdropFilter: 'blur(8px)', borderRadius: 8,
            border: '1px solid rgba(255,255,255,.12)', pointerEvents: 'auto',
          }}
        >
          <VehicleTelemetry key={telemetryClip.id} clip={telemetryClip} playbackTime={playbackTime} compact />
        </div>
      ) : null}
      <div
        data-testid="actual-size-controls"
        onClick={stop}
        onPointerMove={wake}
        style={{
          position: 'absolute', left: 0, right: 0, bottom: 0,
          display: 'flex', alignItems: 'center', gap: 10, padding: '28px 14px 12px',
          background: 'linear-gradient(transparent, rgba(0,0,0,.72))',
          opacity: visible ? 1 : 0, transition: 'opacity .25s',
          pointerEvents: visible ? 'auto' : 'none',
        }}
      >
        <button aria-label="Back 5 seconds" style={button} onClick={() => playback.seek(Math.max(0, state.time - 5))}>
          <Icon name="back10" size={14} />
        </button>
        <button aria-label={state.paused ? 'Play' : 'Pause'} style={{ ...button, width: 40, height: 40 }} onClick={() => playback.togglePlay()}>
          <Icon name={state.paused ? 'play' : 'pause'} size={16} />
        </button>
        <button aria-label="Forward 5 seconds" style={button} onClick={() => playback.seek(Math.min(state.duration, state.time + 5))}>
          <Icon name="fwd10" size={14} />
        </button>
        <input
          type="range"
          aria-label="Playback position"
          min={0}
          max={state.duration || 1}
          step={0.1}
          value={state.time}
          onChange={event => playback.seek(Number(event.target.value))}
          style={{ flex: 1, accentColor: 'var(--accent)' }}
        />
        <span style={{ color: '#fff', fontFamily: 'var(--font-mono)', fontSize: 'var(--ts-xs)', minWidth: 76, textAlign: 'right' }}>
          {formatClock(state.time)} / {formatClock(state.duration)}
        </span>
      </div>
    </>
  )
}

/* ─── player area ────────────────────────────────────────────────────────── */

function registeredVideos(registry: React.RefObject<Map<HTMLVideoElement, CameraAngle>>) {
  return Array.from(registry.current.keys())
}

function leadRegisteredVideo(registry: React.RefObject<Map<HTMLVideoElement, CameraAngle>>) {
  for (const [video, videoAngle] of registry.current) {
    if (videoAngle === 'front') return video
  }
  return registry.current.keys().next().value
}

/** Seeks at most this often while the map dot is dragged; the last position always lands. */
const SCRUB_SEEK_MS = 150

function PlayerArea({ minutes, minute, setMinute, initialTime, videoRefs, muted, setMuted, registerScrub }: {
  minutes: string[]
  minute: string
  setMinute: (minute: string) => void
  /** Seconds to start at; every camera is set to the clock when it loads. */
  initialTime: number
  videoRefs: React.RefObject<Map<HTMLVideoElement, CameraAngle>>
  muted: boolean
  setMuted: React.Dispatch<React.SetStateAction<boolean>>
  registerScrub: (handlers: PlayerHandles | null) => void
}) {
  const [playing, setPlaying] = useState(false)
  const [currentTime, setCurrentTime] = useState(initialTime)
  const [duration, setDuration] = useState(0)
  const [speed, setSpeed] = useState(1)

  const clock = useRef(initialTime)
  const initialized = useRef(new WeakMap<HTMLVideoElement, string>())
  const rafRef = useRef<number>(0)

  const leadVideo = useCallback(
    () => leadRegisteredVideo(videoRefs),
    [videoRefs],
  )

  const allVideos = useCallback(() => registeredVideos(videoRefs), [videoRefs])

  useEffect(() => {
    function tick() {
      for (const video of videoRefs.current.keys()) {
        if (initialized.current.get(video) !== video.src && video.readyState >= 1) {
          video.currentTime = clock.current
          video.playbackRate = speed
          if (playing) void video.play().catch(() => {})
          initialized.current.set(video, video.src)
        }
      }
      const lead = leadVideo()
      if (lead && lead.readyState >= 1 && initialized.current.get(lead) === lead.src) {
        clock.current = lead.currentTime
        setCurrentTime(lead.currentTime)
        if (lead.duration > 0 && !Number.isNaN(lead.duration)) {
          setDuration(prev => (prev === lead.duration ? prev : lead.duration))
        }
        lead.onended ??= () => setPlaying(false)
        for (const el of videoRefs.current.keys()) {
          if (el === lead || initialized.current.get(el) !== el.src) continue
          if (Math.abs(el.currentTime - lead.currentTime) > 0.5) {
            el.currentTime = lead.currentTime
          }
        }
      }
      rafRef.current = requestAnimationFrame(tick)
    }
    rafRef.current = requestAnimationFrame(tick)
    return () => cancelAnimationFrame(rafRef.current)
  }, [leadVideo, videoRefs, playing, speed])

  const togglePlay = useCallback(() => {
    const lead = leadVideo()
    if (!lead) return
    if (!playing) {
      allVideos().forEach(v => v.play().catch(() => {}))
      setPlaying(true)
    } else {
      allVideos().forEach(v => v.pause())
      setPlaying(false)
    }
  }, [leadVideo, allVideos, playing])

  const seek = useCallback((time: number) => {
    clock.current = time
    allVideos().forEach(v => { v.currentTime = time })
    setCurrentTime(time)
  }, [allVideos])

  // Dragging the map dot: pause, seek all cameras a few times a second while
  // it moves (each seek decodes four streams in the browser; the Pi only
  // serves file ranges), seek exactly on release, then resume if it played.
  const playingRef = useRef(playing)
  useEffect(() => { playingRef.current = playing }, [playing])
  const scrub = useRef<{ resume: boolean, lastSeek: number, pending: number | null, timer?: ReturnType<typeof setTimeout> }>(
    { resume: false, lastSeek: 0, pending: null },
  )
  const flushScrub = useCallback(() => {
    const state = scrub.current
    state.timer = undefined
    if (state.pending === null) return
    state.lastSeek = performance.now()
    seek(state.pending)
    state.pending = null
  }, [seek])
  const scrubBegin = useCallback(() => {
    scrub.current.resume = playingRef.current
    if (scrub.current.resume) {
      allVideos().forEach(v => v.pause())
      setPlaying(false)
    }
  }, [allVideos])
  const scrubMove = useCallback((seconds: number) => {
    const state = scrub.current
    state.pending = seconds
    const wait = SCRUB_SEEK_MS - (performance.now() - state.lastSeek)
    if (wait <= 0) flushScrub()
    else state.timer ??= setTimeout(flushScrub, wait)
  }, [flushScrub])
  const scrubEnd = useCallback((seconds: number) => {
    const state = scrub.current
    clearTimeout(state.timer)
    state.timer = undefined
    state.pending = null
    seek(seconds)
    if (state.resume) {
      allVideos().forEach(v => v.play().catch(() => {}))
      setPlaying(true)
    }
  }, [seek, allVideos])
  useEffect(() => {
    registerScrub({ begin: scrubBegin, move: scrubMove, end: scrubEnd, togglePlay, seek })
    const state = scrub.current
    return () => {
      clearTimeout(state.timer)
      registerScrub(null)
    }
  }, [registerScrub, scrubBegin, scrubMove, scrubEnd, togglePlay, seek])

  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.target instanceof HTMLInputElement || e.target instanceof HTMLTextAreaElement) return
      if (e.code === 'Space') { e.preventDefault(); togglePlay() }
      if (e.code === 'ArrowLeft') { e.preventDefault(); seek(Math.max(0, currentTime - (e.shiftKey ? 1 : 5))) }
      if (e.code === 'ArrowRight') { e.preventDefault(); seek(Math.min(duration, currentTime + (e.shiftKey ? 1 : 5))) }
      if (e.code === 'KeyM') { setMuted(m => !m) }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [togglePlay, seek, currentTime, duration, setMuted])

  useEffect(() => {
    allVideos().forEach(v => {
      v.playbackRate = speed
      if (!playing) v.pause()
    })
  }, [speed, playing, allVideos])

  useEffect(() => {
    allVideos().forEach(v => { v.muted = muted })
  }, [muted, allVideos])

  const pct = duration > 0 ? (currentTime / duration) * 100 : 0

  return (
    <div style={{
      padding: 'var(--sp-3) var(--sp-5) var(--sp-4)',
      background: 'var(--bg-1)',
      borderTop: '1px solid var(--border)',
      display: 'grid',
      gridTemplateRows: 'auto auto',
      gap: 'var(--sp-2)',
    }}>
      {minutes.length > 1 && (
        <label style={{ color: 'var(--fg-1)', fontSize: 'var(--ts-sm)' }}>
          Minute{' '}
          <select aria-label="Minute" value={minute} onChange={event => setMinute(event.target.value)}>
            {minutes.map(timestamp => <option key={timestamp} value={timestamp}>{timestamp.replace('_', ' ')}</option>)}
          </select>
        </label>
      )}
      {/* Scrubber */}
      <div style={{ position: 'relative', height: 32 }}>
        <div style={{
          position: 'absolute',
          left: 0, right: 0,
          top: 12,
          height: 6,
          background: 'var(--bg-3)',
          borderRadius: 3,
          overflow: 'hidden',
        }}>
          <div style={{
            position: 'absolute',
            left: 0, top: 0, bottom: 0,
            width: `${pct}%`,
            background: 'var(--accent)',
            borderRadius: 3,
          }} />
        </div>
        <input
          type="range"
          min={0}
          max={duration || 1}
          step={0.1}
          value={currentTime}
          onChange={e => seek(Number(e.target.value))}
          style={{
            position: 'absolute',
            left: 0, right: 0,
            top: 0,
            width: '100%',
            height: 32,
            appearance: 'none',
            background: 'none',
            cursor: 'pointer',
            margin: 0,
            accentColor: 'var(--accent)',
          }}
        />
      </div>

      {/* Controls */}
      <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--sp-4)' }}>
        <span style={{
          fontFamily: 'var(--font-mono)',
          fontSize: 'var(--ts-md)',
          color: 'var(--fg-1)',
          fontVariantNumeric: 'tabular-nums',
          minWidth: 140,
        }}>
          <span style={{ color: 'var(--fg-0)', fontWeight: 600 }}>{formatTime(currentTime)}</span>
          <span style={{ color: 'var(--fg-3)', margin: '0 6px' }}>/</span>
          <span>{formatTime(duration)}</span>
        </span>

        <div style={{ display: 'flex', gap: 4, margin: '0 auto' }}>
          <TransportBtn onClick={() => seek(Math.max(0, currentTime - 10))} title="Back 10s">
            <Icon name="back10" size={22} />
          </TransportBtn>
          <TransportBtn onClick={() => seek(Math.max(0, currentTime - 1))} title="Back 1s">
            <Icon name="skipBack" size={20} />
          </TransportBtn>
          <TransportBtn onClick={togglePlay} title="Play/pause (space)" play>
            <Icon name={playing ? 'pause' : 'play'} size={22} />
          </TransportBtn>
          <TransportBtn onClick={() => seek(Math.min(duration, currentTime + 1))} title="Forward 1s">
            <Icon name="skipFwd" size={20} />
          </TransportBtn>
          <TransportBtn onClick={() => seek(Math.min(duration, currentTime + 10))} title="Forward 10s">
            <Icon name="fwd10" size={22} />
          </TransportBtn>
        </div>

        <div style={{ display: 'flex', gap: 4 }}>
          {[0.5, 1, 2, 4].map(s => (
            <button
              key={s}
              onClick={() => setSpeed(s)}
              style={{
                padding: '4px 10px',
                borderRadius: 'var(--radius-sm)',
                fontFamily: 'var(--font-mono)',
                fontSize: 'var(--ts-xs)',
                color: speed === s ? 'var(--fg-0)' : 'var(--fg-1)',
                border: `1px solid ${speed === s ? 'var(--border-strong)' : 'var(--border)'}`,
                background: speed === s ? 'var(--bg-3)' : 'transparent',
                cursor: 'pointer',
              }}
            >
              {s}×
            </button>
          ))}
        </div>

        <button
          onClick={() => setMuted(v => !v)}
          title="M"
          style={{ fontSize: 16, color: muted ? 'var(--fg-3)' : 'var(--fg-0)', cursor: 'pointer', background: 'none', border: 'none' }}
        >
          {muted ? <Icon name="x" size={18} /> : <Icon name="check" size={18} />}
        </button>

        <span style={{ fontSize: 'var(--ts-xs)', color: 'var(--fg-3)', marginLeft: 'auto', whiteSpace: 'nowrap' }}>
          Space · ← → · M
        </span>
      </div>
    </div>
  )
}

function TransportBtn({ children, onClick, title, play }: {
  children: React.ReactNode
  onClick: () => void
  title: string
  play?: boolean
}) {
  return (
    <button
      onClick={onClick}
      title={title}
      style={{
        width: play ? 44 : 36,
        height: play ? 44 : 36,
        display: 'grid',
        placeItems: 'center',
        borderRadius: play ? '50%' : 'var(--radius-sm)',
        color: play ? 'var(--accent-ink)' : 'var(--fg-1)',
        background: play ? 'var(--accent)' : 'transparent',
        border: 'none',
        cursor: 'pointer',
        transition: 'background .1s',
      }}
    >
      {children}
    </button>
  )
}

/* ─── right rail ─────────────────────────────────────────────────────────── */

function RightRail({
  layout, setLayout,
  mirrored, setMirrored,
  focus, setFocus,
}: {
  layout: Layout
  setLayout: (l: Layout) => void
  mirrored: boolean
  setMirrored: (v: boolean) => void
  focus: CameraAngle
  setFocus: (a: CameraAngle) => void
}) {
  const { status } = useStatus()

  return (
    <div style={{
      background: 'var(--bg-1)',
      minHeight: 0,
      overflow: 'hidden',
      display: 'flex',
      flexDirection: 'column',
    }}>
      {/* Layout picker */}
      <div style={{
        padding: 'var(--sp-4)',
        borderBottom: '1px solid var(--border)',
      }}>
        <h4 style={{
          margin: '0 0 var(--sp-3)',
          fontSize: 'var(--ts-xs)',
          color: 'var(--fg-2)',
          fontWeight: 600,
          letterSpacing: '.1em',
          textTransform: 'uppercase',
        }}>Layout</h4>
        <div style={{
          display: 'grid',
          gridTemplateColumns: '1fr 1fr 1fr',
          gap: 'var(--sp-2)',
        }}>
          {LAYOUTS.map((l, i) => (
            <button
              key={l.id}
              onClick={() => setLayout(l.id)}
              style={{
                aspectRatio: 1.2,
                border: `1px solid ${layout === l.id ? 'var(--accent)' : 'var(--border)'}`,
                borderRadius: 'var(--radius-md)',
                background: layout === l.id ? 'rgba(225,29,42,.08)' : 'var(--bg-2)',
                padding: 8,
                cursor: 'pointer',
                display: 'flex',
                flexDirection: 'column',
                gap: 6,
                transition: 'border-color .12s, background .12s',
                boxShadow: layout === l.id ? 'var(--shadow-accent)' : undefined,
              }}
            >
              <MiniLayout layoutId={l.id} active={layout === l.id} />
              <div style={{
                fontFamily: 'var(--font-mono)',
                fontSize: 10,
                color: layout === l.id ? 'var(--fg-0)' : 'var(--fg-2)',
                textAlign: 'center',
                letterSpacing: '.04em',
              }}>
                {i + 1} · {l.name}
              </div>
            </button>
          ))}
        </div>
      </div>

      {/* Options */}
      <div style={{
        padding: 'var(--sp-4)',
        borderBottom: '1px solid var(--border)',
      }}>
        <h4 style={{
          margin: '0 0 var(--sp-3)',
          fontSize: 'var(--ts-xs)',
          color: 'var(--fg-2)',
          fontWeight: 600,
          letterSpacing: '.1em',
          textTransform: 'uppercase',
        }}>Options</h4>
        <div style={{
          display: 'flex',
          justifyContent: 'space-between',
          alignItems: 'center',
          padding: '6px 0',
          fontSize: 'var(--ts-sm)',
          color: 'var(--fg-1)',
        }}>
          <span>Mirror side & rear cams</span>
          <Toggle on={mirrored} onClick={() => setMirrored(!mirrored)} />
        </div>
        <div style={{
          display: 'flex',
          justifyContent: 'space-between',
          alignItems: 'center',
          padding: '6px 0',
          fontSize: 'var(--ts-sm)',
          color: 'var(--fg-1)',
        }}>
          <span>Focus camera</span>
          <select
            aria-label="Focus camera"
            value={focus}
            onChange={e => setFocus(e.target.value as CameraAngle)}
            style={{
              background: 'var(--bg-2)',
              border: '1px solid var(--border)',
              borderRadius: 'var(--radius-sm)',
              color: 'var(--fg-0)',
              fontSize: 'var(--ts-xs)',
              fontFamily: 'var(--font-mono)',
              padding: '4px 8px',
            }}
          >
            {CAMERA_ORDER.map(a => (
              <option key={a} value={a}>{CAMERA_LABELS[a]}</option>
            ))}
          </select>
        </div>
      </div>

      {/* Shortcuts */}
      <div style={{
        padding: 'var(--sp-4)',
        borderBottom: '1px solid var(--border)',
      }}>
        <h4 style={{
          margin: '0 0 var(--sp-3)',
          fontSize: 'var(--ts-xs)',
          color: 'var(--fg-2)',
          fontWeight: 600,
          letterSpacing: '.1em',
          textTransform: 'uppercase',
        }}>Shortcuts</h4>
        <div style={{
          display: 'grid',
          gridTemplateColumns: '1fr auto',
          gap: '6px var(--sp-3)',
          fontSize: 'var(--ts-sm)',
        }}>
          <Shortcut label="Play / pause" keys="space" />
          <Shortcut label="Rewind 5s" keys="←" />
          <Shortcut label="Forward 5s" keys="→" />
          <Shortcut label="Step 1s" keys="⇧ ← →" />
          <Shortcut label="Mute" keys="M" />
          <Shortcut label="Fullscreen" keys="F" />
          <Shortcut label="Switch layout" keys="1 – 5" />
          <Shortcut label="Camera at actual size" keys="click" />
          <Shortcut label="Back to the grid" keys="Esc" />
        </div>
      </div>

      {/* Pi status */}
      <div style={{
        padding: 'var(--sp-4)',
        flex: 1,
        overflow: 'auto',
      }}>
        <h4 style={{
          margin: '0 0 var(--sp-3)',
          fontSize: 'var(--ts-xs)',
          color: 'var(--fg-2)',
          fontWeight: 600,
          letterSpacing: '.1em',
          textTransform: 'uppercase',
        }}>Pi status</h4>
        <StatusRow label="uptime" value={status?.uptime_seconds ? formatUptime(status.uptime_seconds) : '—'} />
        <StatusRow label="cpu temp" value={status?.cpu_temp_c != null ? `${status.cpu_temp_c.toFixed(1)} °C` : '—'} />
        <StatusRow label="last archive" value={status?.archive?.message ? status.archive.message.slice(0, 30) : '—'} />
      </div>
    </div>
  )
}

function Toggle({ on, onClick }: { on: boolean; onClick: () => void }) {
  return (
    <div
      onClick={onClick}
      style={{
        width: 32, height: 18,
        borderRadius: 999,
        background: on ? 'var(--accent)' : 'var(--bg-3)',
        position: 'relative',
        cursor: 'pointer',
        flexShrink: 0,
        transition: 'background .12s',
      }}
    >
      <div style={{
        position: 'absolute',
        left: on ? 16 : 2,
        top: 2,
        width: 14, height: 14,
        borderRadius: '50%',
        background: on ? '#fff' : 'var(--fg-2)',
        transition: 'left .12s, background .12s',
      }} />
    </div>
  )
}

function Shortcut({ label, keys }: { label: string; keys: string }) {
  return (
    <>
      <span style={{ color: 'var(--fg-2)' }}>{label}</span>
      <span style={{
        fontFamily: 'var(--font-mono)',
        fontSize: 'var(--ts-xs)',
        padding: '2px 6px',
        borderRadius: 3,
        background: 'var(--bg-3)',
        color: 'var(--fg-0)',
        border: '1px solid var(--border)',
        borderBottomWidth: 2,
        textAlign: 'center',
        minWidth: 18,
      }}>{keys}</span>
    </>
  )
}

function StatusRow({ label, value }: { label: string; value: string }) {
  return (
    <div style={{
      display: 'grid',
      gridTemplateColumns: 'auto 1fr auto',
      alignItems: 'center',
      gap: 'var(--sp-2)',
      padding: '6px 0',
      fontFamily: 'var(--font-mono)',
      fontSize: 'var(--ts-xs)',
    }}>
      <span style={{ color: 'var(--fg-2)', letterSpacing: '.04em', textTransform: 'uppercase' }}>{label}</span>
      <span />
      <span style={{ color: 'var(--fg-0)', textAlign: 'right' }}>{value}</span>
    </div>
  )
}

function MiniLayout({ layoutId, active }: { layoutId: Layout; active: boolean }) {
  const cfg: Record<string, string[][]> = {
    classic: [['map','front','info'],['l','back','r']],
    overhead: [['l','front','r'],['map','back','info']],
    mobile: [['front','front'],['l','r'],['back','back'],['map','info']],
    focus: [['focus','focus','focus','focus','focus'],['l','b','r','m','i']],
    board: [['front','front','info','info'],['l','b','info','info'],['r','map','info','info']],
  }
  const grid = cfg[layoutId]
  return (
    <div style={{
      flex: 1,
      display: 'grid',
      gap: 2,
      gridTemplateColumns: `repeat(${grid[0].length}, 1fr)`,
      gridTemplateRows: `repeat(${grid.length}, 1fr)`,
    }}>
      {grid.flatMap((row, ri) => row.map((cell, ci) => (
        <div key={`${ri}-${ci}`}
          style={{
            background: cell === '' ? 'transparent' : active && (cell === 'front' || cell === 'focus') ? 'var(--accent)' : active ? 'var(--fg-2)' : 'var(--bg-4)',
            borderRadius: 2,
            visibility: cell === '' ? 'hidden' : 'visible',
          }}
        />
      )))}
    </div>
  )
}

function formatUptime(seconds: number): string {
  const d = Math.floor(seconds / 86400)
  const h = Math.floor((seconds % 86400) / 3600)
  const m = Math.floor((seconds % 3600) / 60)
  if (d > 0) return `${d}d ${h}h`
  if (h > 0) return `${h}h ${m}m`
  return `${m}m`
}

/* ─── empty states ───────────────────────────────────────────────────────── */

function NoSelection() {
  return (
    <div style={{
      gridRow: '1 / -1',
      display: 'flex',
      alignItems: 'center',
      justifyContent: 'center',
      color: 'var(--fg-2)',
    }}>
      <div style={{ textAlign: 'center' }}>
        <div style={{ fontSize: 40, marginBottom: 'var(--sp-3)' }}>🎥</div>
        <div style={{ fontSize: 'var(--ts-md)' }}>Select a clip from the sidebar</div>
      </div>
    </div>
  )
}

function LoadingCameras() {
  return (
    <div style={{
      gridRow: '1 / -1',
      display: 'flex',
      alignItems: 'center',
      justifyContent: 'center',
      color: 'var(--fg-2)',
    }}>
      <div style={{ fontSize: 'var(--ts-sm)' }}>Loading cameras…</div>
    </div>
  )
}
