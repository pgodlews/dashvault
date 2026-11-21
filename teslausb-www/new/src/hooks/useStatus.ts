import { useSyncExternalStore } from 'react'
import type { Status } from '../lib/status'

export type ConnectionState = 'connecting' | 'live' | 'error' | 'closed'

export interface UseStatusResult {
  status: Status | null
  conn: ConnectionState
  lastUpdate: Date | null
}

const SSE_URL = '/api/status/stream'
const SNAPSHOT_URL = '/api/status'
const RECONNECT_DELAY_MS = 5_000

/* ─── shared stream ────────────────────────────────────────────────────────
 * A single EventSource is shared by every useStatus() consumer (pages mount
 * several hooks at once). Over HTTP/1.1 each EventSource holds one of the
 * browser's ~6 per-host connections, so per-hook streams would compete with
 * video streaming. The first subscriber opens the stream; the last
 * unsubscribe closes it. */

let shared: UseStatusResult = { status: null, conn: 'connecting', lastUpdate: null }
let es: EventSource | null = null
let timer: ReturnType<typeof setTimeout> | null = null
let subscribers = 0
const listeners = new Set<() => void>()

function emit(patch: Partial<UseStatusResult>) {
  shared = { ...shared, ...patch }
  listeners.forEach(l => l())
}

function connect() {
  if (!window.EventSource) {
    // Fallback: poll the snapshot endpoint
    pollSnapshot()
    return
  }

  emit({ conn: 'connecting' })
  es = new EventSource(SSE_URL)

  es.onmessage = (ev) => {
    try {
      const data = JSON.parse(ev.data) as Status
      emit({ status: data, lastUpdate: new Date(), conn: 'live' })
    } catch {
      // ignore malformed frames
    }
  }

  es.onerror = () => {
    es?.close()
    es = null
    if (subscribers > 0) {
      emit({ conn: 'error' })
      timer = setTimeout(connect, RECONNECT_DELAY_MS)
    }
  }
}

async function pollSnapshot() {
  try {
    const res = await fetch(SNAPSHOT_URL)
    if (res.ok) {
      const data = (await res.json()) as Status
      emit({ status: data, lastUpdate: new Date(), conn: 'live' })
    } else {
      emit({ conn: 'error' })
    }
  } catch {
    emit({ conn: 'error' })
  }
  if (subscribers > 0) {
    timer = setTimeout(pollSnapshot, 10_000)
  }
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener)
  subscribers++
  if (subscribers === 1) connect()
  return () => {
    listeners.delete(listener)
    subscribers--
    if (subscribers === 0) {
      es?.close()
      es = null
      if (timer) {
        clearTimeout(timer)
        timer = null
      }
    }
  }
}

function getSnapshot(): UseStatusResult {
  return shared
}

/**
 * Subscribes to the /api/status/stream SSE endpoint.
 * Falls back to polling /api/status every 10 s if EventSource is not
 * available (should not happen in any modern browser, but just in case).
 */
export function useStatus(): UseStatusResult {
  return useSyncExternalStore(subscribe, getSnapshot)
}
