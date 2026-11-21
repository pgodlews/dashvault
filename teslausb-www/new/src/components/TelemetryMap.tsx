import { useEffect, useRef } from 'react'
import L from 'leaflet'
import 'leaflet/dist/leaflet.css'
import {
  clipLatLons,
  cursorArrowHtml,
  nearestOnTrack,
  telemetryClipLabel,
  telemetryStateLabel,
  telemetryTrackStyle,
  type TelemetryClip,
  type ScrubHandlers,
  type TelemetryCursor,
  type TelemetryPoint,
} from '../lib/telemetry'

interface TelemetryMapProps {
  clips: TelemetryClip[]
  playbackTime?: () => number | null
  onOpen?: (clip: TelemetryClip) => void
  tileUrl?: string
  /** Replay position; moving it never refits the map. */
  cursor?: TelemetryCursor | null
  /** With playbackTime: the playback dot can be dragged along the track to seek. */
  onScrub?: ScrubHandlers
  /** A click on the map (not on a track), with metres per screen pixel at that zoom. */
  onMapClick?: (lat: number, lon: number, metersPerPixel: number) => void
  /** CSS cursor over the map, e.g. crosshair while picking a point. */
  mapCursor?: string
  /** Zoom to a point and keep it in the top part of the map (a panel covers the rest). */
  focus?: { lat: number, lon: number, visibleFraction: number } | null
}



function positionAt(track: TelemetryPoint[], seconds: number): [number, number] {
  if (seconds <= track[0].t) return [track[0].lat, track[0].lon]
  const last = track[track.length - 1]
  if (seconds >= last.t) return [last.lat, last.lon]
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
  return [a.lat + (b.lat - a.lat) * ratio, a.lon + (b.lon - a.lon) * ratio]
}

export function TelemetryMap({ clips, playbackTime, onOpen, tileUrl, cursor, onScrub, onMapClick, mapCursor, focus }: TelemetryMapProps) {
  const container = useRef<HTMLDivElement>(null)
  const map = useRef<L.Map | null>(null)
  const routes = useRef<L.LayerGroup | null>(null)
  const playbackMarker = useRef<L.Marker | null>(null)
  const cursorMarker = useRef<L.Marker | null>(null)
  const dragging = useRef(false)
  const scrubRef = useRef(onScrub)
  useEffect(() => { scrubRef.current = onScrub }, [onScrub])
  const scrubbable = Boolean(playbackTime && onScrub)
  const mapClickRef = useRef(onMapClick)
  useEffect(() => { mapClickRef.current = onMapClick }, [onMapClick])

  useEffect(() => {
    if (!container.current) return
    const instance = L.map(container.current, { zoomControl: true, attributionControl: true })
    L.tileLayer(tileUrl ?? 'https://tile.openstreetmap.org/{z}/{x}/{y}.png', {
      maxZoom: 19,
      attribution: '&copy; <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a>',
    }).addTo(instance)
    routes.current = L.layerGroup().addTo(instance)
    instance.on('click', event => {
      const { lat, lng } = event.latlng
      const metersPerPixel = (40075016.686 * Math.cos((lat * Math.PI) / 180)) / 2 ** (instance.getZoom() + 8)
      mapClickRef.current?.(lat, lng, metersPerPixel)
    })
    map.current = instance
    return () => {
      instance.remove()
      map.current = null
    }
  }, [tileUrl])

  useEffect(() => {
    const instance = map.current
    const layer = routes.current
    if (!instance || !layer) return
    layer.clearLayers()
    playbackMarker.current = null
    const bounds: L.LatLngExpression[] = []
    for (const clip of clips) {
      const points = clipLatLons(clip) as L.LatLngExpression[]
      if (!points.length) continue
      bounds.push(...points)
      const { color, onPi } = telemetryTrackStyle(clip)
      // Dark casing keeps the yellow Recent tracks readable on light tiles.
      L.polyline(points, { color: '#111', weight: 7, opacity: onPi ? 0.35 : 0.15, interactive: false }).addTo(layer)
      const route = L.polyline(points, {
        color, weight: 4, opacity: onPi ? 0.95 : 0.6, dashArray: onPi ? undefined : '8 7',
      }).addTo(layer)
      const dot = `<span style="display:block;width:12px;height:12px;border-radius:50%;background:${color};border:2px solid white;box-shadow:0 1px 4px #000;opacity:${onPi ? 1 : 0.6}"></span>`
      const track = clip.track ?? []
      const draggable = scrubbable && track.length > 1
      const marker = L.marker(points[points.length - 1], {
        draggable,
        title: draggable ? 'Drag along the track to move playback' : undefined,
        icon: L.divIcon({
          // A larger invisible hit area makes the dot easy to grab, also by touch.
          className: draggable ? 'teslausb-track-marker teslausb-scrub-dot' : 'teslausb-track-marker',
          html: draggable
            ? `<span style="display:grid;place-items:center;width:28px;height:28px;cursor:grab">${dot}</span>`
            : dot,
          iconSize: draggable ? [28, 28] : [16, 16],
          iconAnchor: draggable ? [14, 14] : [8, 8],
        }),
      }).addTo(layer)
      if (draggable) {
        const snap = () => {
          const { lat, lng } = marker.getLatLng()
          const point = nearestOnTrack(track, lat, lng)
          if (point) marker.setLatLng([point.lat, point.lon])
          return point
        }
        marker.on('dragstart', () => {
          dragging.current = true
          marker.closePopup()
          scrubRef.current?.begin()
        })
        marker.on('drag', () => {
          const point = snap()
          if (point) scrubRef.current?.move(point.t)
        })
        marker.on('dragend', () => {
          const point = snap()
          dragging.current = false
          if (point) scrubRef.current?.end(point.t)
        })
      }
      const popup = document.createElement('div')
      const title = document.createElement('strong')
      title.textContent = telemetryClipLabel(clip)
      popup.append(title, document.createElement('br'), document.createTextNode(telemetryStateLabel(clip.state)))
      route.bindPopup(popup)
      if (!draggable) marker.bindPopup(popup)
      // Markers do not pass clicks to the map: picking a point (look around) on a dot uses the dot.
      marker.on('click', () => {
        const { lat, lng } = marker.getLatLng()
        const metersPerPixel = (40075016.686 * Math.cos((lat * Math.PI) / 180)) / 2 ** (instance.getZoom() + 8)
        mapClickRef.current?.(lat, lng, metersPerPixel)
      })
      if (clip.state === 'available' && onOpen) {
        route.on('click', () => onOpen(clip))
        marker.on('click', () => onOpen(clip))
      }
      if (playbackTime) {
        playbackMarker.current = marker
      }
    }
    if (bounds.length) instance.fitBounds(L.latLngBounds(bounds), { padding: [20, 20], maxZoom: 17 })
  }, [clips, onOpen, playbackTime, scrubbable])

  useEffect(() => {
    if (!playbackTime) return
    const timer = setInterval(() => {
      const marker = playbackMarker.current
      const track = clips[0]?.track ?? []
      const seconds = playbackTime()
      if (marker && track.length && seconds !== null && !dragging.current) {
        marker.setLatLng(positionAt(track, seconds))
      }
    }, 100)
    return () => clearInterval(timer)
  }, [clips, playbackTime])

  const focusKey = focus ? `${focus.lat},${focus.lon},${focus.visibleFraction}` : ''
  useEffect(() => {
    const instance = map.current
    if (!instance || !focus) return
    // Center the point in the visible top part: move the view down by the hidden half.
    instance.setView([focus.lat, focus.lon], Math.max(instance.getZoom(), 16), { animate: false })
    const height = instance.getSize().y
    instance.panBy([0, (height * (1 - focus.visibleFraction)) / 2], { animate: false })
    // Re-run only for a new point, not for each render's new object.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [focusKey, clips])

  useEffect(() => {
    const instance = map.current
    if (!instance) return
    if (!cursor) {
      cursorMarker.current?.remove()
      cursorMarker.current = null
      return
    }
    const icon = L.divIcon({ className: 'teslausb-replay-cursor', html: cursorArrowHtml(cursor.heading), iconSize: [26, 26], iconAnchor: [13, 13] })
    if (!cursorMarker.current) {
      cursorMarker.current = L.marker([cursor.lat, cursor.lon], { icon, interactive: false, zIndexOffset: 1000 }).addTo(instance)
    } else {
      cursorMarker.current.setLatLng([cursor.lat, cursor.lon])
      cursorMarker.current.setIcon(icon)
    }
  }, [cursor])

  return <div ref={container} data-testid="telemetry-map" style={{ width: '100%', height: '100%', minHeight: 180, background: 'var(--bg-2)', cursor: mapCursor }} />
}
