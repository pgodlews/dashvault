import { useEffect, useRef, useState } from 'react'
import * as maplibre from 'maplibre-gl'
import type { GeoJSONSource, Map as MapLibreMap } from 'maplibre-gl'
import maplibreWorkerUrl from 'maplibre-gl/dist/maplibre-gl-worker.mjs?url'
import 'maplibre-gl/dist/maplibre-gl.css'

import {
  clipLatLons,
  cursorArrowHtml,
  nearestOnTrack,
  telemetryClipLabel,
  telemetryPointAt,
  telemetryStateLabel,
  telemetryTrackStyle,
  TELEMETRY_CATEGORY_COLORS,
  type ScrubHandlers,
  type TelemetryClip,
  type TelemetryCursor,
} from '../lib/telemetry'

let workerBlobUrl: string | undefined

async function prepareMaplibreWorker(): Promise<void> {
  if (workerBlobUrl) return
  const response = await fetch(maplibreWorkerUrl)
  if (!response.ok) throw new Error(`Worker download failed (${response.status})`)
  const code = await response.text()
  workerBlobUrl = URL.createObjectURL(new Blob([code], { type: 'text/javascript' }))
  maplibre.setWorkerUrl(workerBlobUrl)
}

interface VectorTelemetryMapProps {
  clips: TelemetryClip[]
  styleUrl: string
  playbackTime?: () => number | null
  onOpen?: (clip: TelemetryClip) => void
  /** Replay position; moving it never refits the map. */
  cursor?: TelemetryCursor | null
  /** With playbackTime: the playback dot can be dragged along the track to seek. */
  onScrub?: ScrubHandlers
  /** A click on the map, with metres per screen pixel at that zoom. */
  onMapClick?: (lat: number, lon: number, metersPerPixel: number) => void
  /** CSS cursor over the map, e.g. crosshair while picking a point. */
  mapCursor?: string
  /** Zoom to a point and keep it in the top part of the map (a panel covers the rest). */
  focus?: { lat: number, lon: number, visibleFraction: number } | null
}

export function VectorTelemetryMap({ clips, styleUrl, playbackTime, onOpen, cursor, onScrub, onMapClick, mapCursor, focus }: VectorTelemetryMapProps) {
  const container = useRef<HTMLDivElement>(null)
  const map = useRef<MapLibreMap | null>(null)
  const markers = useRef<maplibre.Marker[]>([])
  const playbackMarker = useRef<maplibre.Marker | null>(null)
  const cursorMarker = useRef<{ marker: maplibre.Marker, element: HTMLDivElement } | null>(null)
  const dragging = useRef(false)
  const scrubRef = useRef(onScrub)
  useEffect(() => { scrubRef.current = onScrub }, [onScrub])
  const scrubbable = Boolean(playbackTime && onScrub)
  const mapClickRef = useRef(onMapClick)
  useEffect(() => { mapClickRef.current = onMapClick }, [onMapClick])
  const onOpenRef = useRef(onOpen)
  const clipsRef = useRef(clips)
  const [mapError, setMapError] = useState('')
  const [mapReady, setMapReady] = useState(false)

  useEffect(() => { onOpenRef.current = onOpen }, [onOpen])
  useEffect(() => { clipsRef.current = clips }, [clips])

  useEffect(() => {
    let cancelled = false
    void prepareMaplibreWorker().then(() => {
      if (cancelled || !container.current) return
      const instance = new maplibre.Map({
        container: container.current,
        style: styleUrl,
        center: [0, 20],
        zoom: 2,
      })
      instance.addControl(new maplibre.NavigationControl(), 'top-right')
      instance.on('click', event => {
        const { lat, lng } = event.lngLat
        // Vector tiles are 512 px, so one zoom level less than raster for the same scale.
        const metersPerPixel = (40075016.686 * Math.cos((lat * Math.PI) / 180)) / 2 ** (instance.getZoom() + 9)
        mapClickRef.current?.(lat, lng, metersPerPixel)
      })
      instance.on('error', event => setMapError(event.error?.message ?? 'Map rendering failed'))
      map.current = instance
      setMapReady(true)
    }).catch(error => setMapError(error instanceof Error ? error.message : 'Worker initialization failed'))
    return () => {
      cancelled = true
      map.current?.remove()
      map.current = null
      setMapReady(false)
    }
  }, [styleUrl])

  useEffect(() => {
    const instance = map.current
    if (!instance) return
    const update = () => {
      const features = clips.flatMap(clip => clipLatLons(clip).length ? [{
        type: 'Feature' as const,
        properties: {
          id: clip.id,
          state: clip.state,
          category: clip.id.startsWith('SavedClips/') ? 'SavedClips' : 'RecentClips',
        },
        geometry: {
          type: 'LineString' as const,
          coordinates: clipLatLons(clip).map(([lat, lon]) => [lon, lat]),
        },
      }] : [])
      const source = instance.getSource('teslausb-tracks') as GeoJSONSource | undefined
      if (source) source.setData({ type: 'FeatureCollection', features })
      else {
        instance.addSource('teslausb-tracks', { type: 'geojson', data: { type: 'FeatureCollection', features } })
        const onPi = ['==', ['get', 'state'], 'available'] as maplibre.FilterSpecification
        const offPi = ['!=', ['get', 'state'], 'available'] as maplibre.FilterSpecification
        const color = ['match', ['get', 'category'],
          'SavedClips', TELEMETRY_CATEGORY_COLORS.SavedClips,
          TELEMETRY_CATEGORY_COLORS.RecentClips] as maplibre.ExpressionSpecification
        // Saved draws above Recent where the two overlap.
        const sortKey = ['match', ['get', 'category'], 'SavedClips', 1, 0] as maplibre.ExpressionSpecification
        instance.addLayer({
          id: 'teslausb-track-casing',
          type: 'line',
          source: 'teslausb-tracks',
          layout: { 'line-sort-key': sortKey, 'line-join': 'round', 'line-cap': 'round' },
          paint: {
            'line-color': '#111111',
            'line-width': 7,
            'line-opacity': ['case', ['==', ['get', 'state'], 'available'], 0.35, 0.15],
          },
        })
        instance.addLayer({
          id: 'teslausb-track-lines-archived',
          type: 'line',
          source: 'teslausb-tracks',
          filter: offPi,
          layout: { 'line-sort-key': sortKey },
          paint: {
            'line-color': color,
            'line-width': 4,
            'line-opacity': 0.6,
            'line-dasharray': [2, 1.75],
          },
        })
        instance.addLayer({
          id: 'teslausb-track-lines',
          type: 'line',
          source: 'teslausb-tracks',
          filter: onPi,
          layout: { 'line-sort-key': sortKey, 'line-join': 'round', 'line-cap': 'round' },
          paint: {
            'line-color': color,
            'line-width': 4,
            'line-opacity': 0.95,
          },
        })
        instance.on('click', 'teslausb-track-lines', event => {
          const clip = clipsRef.current.find(item => item.id === event.features?.[0]?.properties.id)
          if (clip?.state === 'available') onOpenRef.current?.(clip)
        })
      }
      markers.current.forEach(marker => marker.remove())
      markers.current = []
      playbackMarker.current = null
      const bounds = new maplibre.LngLatBounds()
      for (const clip of clips) {
        const points = clipLatLons(clip)
        if (!points.length) continue
        points.forEach(([lat, lon]) => bounds.extend([lon, lat]))
        const [lastLat, lastLon] = points[points.length - 1]
        const { color, onPi } = telemetryTrackStyle(clip)
        const element = document.createElement('div')
        element.style.cssText = `width:12px;height:12px;border-radius:50%;background:${color};border:2px solid white;box-shadow:0 1px 4px #000;opacity:${onPi ? 1 : 0.6}`
        const popupNode = document.createElement('div')
        const title = document.createElement('strong')
        title.textContent = telemetryClipLabel(clip)
        popupNode.append(title, document.createElement('br'), document.createTextNode(telemetryStateLabel(clip.state)))
        const track = clip.track ?? []
        const draggable = scrubbable && track.length > 1
        let marker: maplibre.Marker
        if (draggable) {
          // A larger invisible hit area makes the dot easy to grab, also by touch.
          const handle = document.createElement('div')
          handle.className = 'teslausb-scrub-dot'
          handle.title = 'Drag along the track to move playback'
          handle.style.cssText = 'display:grid;place-items:center;width:28px;height:28px;cursor:grab'
          handle.append(element)
          marker = new maplibre.Marker({ element: handle, draggable: true }).setLngLat([lastLon, lastLat]).addTo(instance)
          const snap = () => {
            const { lat, lng } = marker.getLngLat()
            const point = nearestOnTrack(track, lat, lng)
            if (point) marker.setLngLat([point.lon, point.lat])
            return point
          }
          marker.on('dragstart', () => {
            dragging.current = true
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
        } else {
          marker = new maplibre.Marker({ element })
            .setLngLat([lastLon, lastLat])
            .setPopup(new maplibre.Popup().setDOMContent(popupNode))
            .addTo(instance)
          if (clip.state === 'available') element.addEventListener('click', () => onOpenRef.current?.(clip))
          element.addEventListener('click', event => {
            if (!mapClickRef.current) return
            event.stopPropagation() // the map's own click would fire too
            const metersPerPixel = (40075016.686 * Math.cos((lastLat * Math.PI) / 180)) / 2 ** (instance.getZoom() + 9)
            mapClickRef.current(lastLat, lastLon, metersPerPixel)
          })
        }
        markers.current.push(marker)
        if (playbackTime) playbackMarker.current = marker
      }
      if (!bounds.isEmpty()) instance.fitBounds(bounds, { padding: 35, maxZoom: 16 })
    }
    if (instance.isStyleLoaded()) update()
    else instance.once('load', update)
    return () => { instance.off('load', update) }
  }, [clips, playbackTime, mapReady, scrubbable])

  useEffect(() => {
    if (!playbackTime) return
    const timer = setInterval(() => {
      const marker = playbackMarker.current
      const track = clips[0]?.track ?? []
      const seconds = playbackTime()
      if (marker && track.length && seconds !== null && !dragging.current) {
        const point = telemetryPointAt(track, seconds)
        marker.setLngLat([point.lon, point.lat])
      }
    }, 100)
    return () => clearInterval(timer)
  }, [clips, playbackTime])

  const focusKey = focus ? `${focus.lat},${focus.lon},${focus.visibleFraction}` : ''
  useEffect(() => {
    const instance = map.current
    if (!instance || !mapReady || !focus) return
    const height = instance.getContainer().clientHeight
    instance.jumpTo({
      center: [focus.lon, focus.lat],
      zoom: Math.max(instance.getZoom(), 15),
      // Center the point in the visible top part of the map.
      padding: { top: 0, bottom: height * (1 - focus.visibleFraction), left: 0, right: 0 },
    })
    // Re-run only for a new point, not for each render's new object.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [focusKey, mapReady, clips])

  useEffect(() => {
    const instance = map.current
    if (!instance || !mapReady) return
    if (!cursor) {
      cursorMarker.current?.marker.remove()
      cursorMarker.current = null
      return
    }
    if (!cursorMarker.current) {
      const element = document.createElement('div')
      element.style.pointerEvents = 'none'
      cursorMarker.current = { element, marker: new maplibre.Marker({ element }).setLngLat([cursor.lon, cursor.lat]).addTo(instance) }
    }
    cursorMarker.current.element.innerHTML = cursorArrowHtml(cursor.heading)
    cursorMarker.current.marker.setLngLat([cursor.lon, cursor.lat])
  }, [cursor, mapReady])

  return (
    <div style={{ position: 'relative', width: '100%', height: '100%', minHeight: 180 }}>
      <div ref={container} data-testid="telemetry-map" style={{ width: '100%', height: '100%', background: 'var(--bg-2)', cursor: mapCursor }} />
      {mapError && <div style={{ position: 'absolute', inset: 16, color: 'var(--err)', background: 'var(--bg-1)', padding: 16 }}>{mapError}</div>}
    </div>
  )
}
