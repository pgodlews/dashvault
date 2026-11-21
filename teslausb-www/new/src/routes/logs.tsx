import { createFileRoute } from '@tanstack/react-router'
import { useEffect, useLayoutEffect, useRef, useState, useCallback } from 'react'

export const Route = createFileRoute('/logs')({
  component: LogsPage,
})

// ── types ─────────────────────────────────────────────────────────────────────

interface LogLine {
  id: number
  text: string
  level: LogLevel
}

type LogLevel = 'error' | 'warn' | 'info' | 'debug' | 'plain'

// Known log unit keys — must match go-backend allowedLogUnits map keys + "" for all.
const UNITS = [
  { key: '',        label: 'All' },
  { key: 'backend', label: 'Backend' },
  { key: 'archive', label: 'Archive' },
  { key: 'nginx',   label: 'Nginx' },
  { key: 'kernel',  label: 'Kernel' },
] as const

type UnitKey = (typeof UNITS)[number]['key']

const LINE_BUFFER = 500   // keep at most this many lines in state
const RECONNECT_MS = 5_000

// ── page ─────────────────────────────────────────────────────────────────────

function LogsPage() {
  const [lines, setLines] = useState<LogLine[]>([])
  const [unit, setUnit] = useState<UnitKey>('')
  const [filter, setFilter] = useState('')
  const [follow, setFollow] = useState(true)
  const [conn, setConn] = useState<'connecting' | 'live' | 'error'>('connecting')
  const counterRef = useRef(0)
  const esRef = useRef<EventSource | null>(null)
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const scrollRef = useRef<HTMLDivElement>(null)
  const atBottomRef = useRef(true)

  const appendLines = useCallback((incoming: string[]) => {
    if (incoming.length === 0) return
    const newLines = incoming.map(text => ({
      id: counterRef.current++,
      text,
      level: detectLevel(text),
    }))
    setLines(prev => {
      const combined = [...prev, ...newLines]
      return combined.length > LINE_BUFFER ? combined.slice(combined.length - LINE_BUFFER) : combined
    })
  }, [])

  // SSE connection
  useEffect(() => {
    let cancelled = false

    function connect() {
      if (cancelled) return
      setConn('connecting')

      const url = `/api/logs/stream?unit=${encodeURIComponent(unit)}&lines=200`
      const es = new EventSource(url)
      esRef.current = es

      es.onmessage = (ev) => {
        try {
          const text = JSON.parse(ev.data) as string
          appendLines([text])
          setConn('live')
        } catch { /* ignore */ }
      }

      es.onerror = () => {
        es.close()
        esRef.current = null
        if (!cancelled) {
          setConn('error')
          timerRef.current = setTimeout(connect, RECONNECT_MS)
        }
      }
    }

    counterRef.current = 0
    connect()

    return () => {
      setLines([])
      cancelled = true
      esRef.current?.close()
      esRef.current = null
      if (timerRef.current) clearTimeout(timerRef.current)
    }
  }, [unit, appendLines])

  // Auto-scroll to bottom when new lines arrive
  useLayoutEffect(() => {
    const el = scrollRef.current
    if (!el || !follow || !atBottomRef.current) return
    el.scrollTop = el.scrollHeight
  }, [lines, follow])

  // Track whether user has scrolled away from bottom
  function onScroll() {
    const el = scrollRef.current
    if (!el) return
    const nearBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 40
    atBottomRef.current = nearBottom
    if (!nearBottom && follow) setFollow(false)
  }

  function scrollToBottom() {
    const el = scrollRef.current
    if (el) el.scrollTop = el.scrollHeight
    atBottomRef.current = true
    setFollow(true)
  }

  // Apply filter
  const filterLower = filter.toLowerCase()
  const visible = filterLower
    ? lines.filter(l => l.text.toLowerCase().includes(filterLower))
    : lines

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%', overflow: 'hidden' }}>
      {/* toolbar */}
      <div style={{
        display: 'flex',
        alignItems: 'center',
        gap: 'var(--sp-3)',
        padding: 'var(--sp-3) var(--sp-4)',
        borderBottom: '1px solid var(--border)',
        background: 'var(--bg-1)',
        flexShrink: 0,
        flexWrap: 'wrap',
      }}>
        {/* unit tabs */}
        <div style={{ display: 'flex', gap: 'var(--sp-1)' }}>
          {UNITS.map(u => (
            <button
              key={u.key}
              onClick={() => setUnit(u.key)}
              style={{
                padding: '3px 10px',
                borderRadius: 'var(--radius-pill)',
                fontSize: 'var(--ts-sm)',
                background: unit === u.key ? 'var(--accent)' : 'var(--bg-3)',
                color: unit === u.key ? 'var(--accent-ink)' : 'var(--fg-1)',
                cursor: 'pointer',
                fontWeight: unit === u.key ? 600 : 400,
              }}
            >
              {u.label}
            </button>
          ))}
        </div>

        {/* filter input */}
        <input
          type="text"
          placeholder="Filter…"
          value={filter}
          onChange={e => setFilter(e.target.value)}
          style={{
            flex: 1,
            minWidth: 120,
            maxWidth: 260,
            background: 'var(--bg-3)',
            border: '1px solid var(--border)',
            borderRadius: 'var(--radius-sm)',
            padding: '3px 8px',
            fontSize: 'var(--ts-sm)',
            color: 'var(--fg-0)',
            fontFamily: 'var(--font-mono)',
            outline: 'none',
          }}
        />
        {filter && (
          <span style={{ fontSize: 'var(--ts-xs)', color: 'var(--fg-2)' }}>
            {visible.length} / {lines.length}
          </span>
        )}

        {/* right side */}
        <div style={{ marginLeft: 'auto', display: 'flex', alignItems: 'center', gap: 'var(--sp-3)' }}>
          <ConnectionBadge conn={conn} />

          <button
            onClick={() => { setLines([]); counterRef.current = 0 }}
            style={{
              fontSize: 'var(--ts-sm)',
              color: 'var(--fg-2)',
              cursor: 'pointer',
              padding: '3px 8px',
              borderRadius: 'var(--radius-sm)',
              background: 'var(--bg-3)',
            }}
          >
            Clear
          </button>

          <button
            onClick={() => setFollow(f => !f)}
            style={{
              fontSize: 'var(--ts-sm)',
              padding: '3px 10px',
              borderRadius: 'var(--radius-sm)',
              background: follow ? 'var(--ok)' : 'var(--bg-3)',
              color: follow ? '#fff' : 'var(--fg-2)',
              cursor: 'pointer',
              fontWeight: follow ? 600 : 400,
            }}
          >
            {follow ? '⏬ Follow' : '⏸ Paused'}
          </button>
        </div>
      </div>

      {/* log output */}
      <div
        ref={scrollRef}
        onScroll={onScroll}
        style={{
          flex: 1,
          overflowY: 'auto',
          background: 'var(--bg-0)',
          padding: 'var(--sp-2) 0',
          fontFamily: 'var(--font-mono)',
          fontSize: 'var(--ts-sm)',
          lineHeight: 1.55,
        }}
      >
        {visible.length === 0 && conn === 'connecting' ? (
          <div style={{ color: 'var(--fg-3)', padding: 'var(--sp-4) var(--sp-5)' }}>Connecting…</div>
        ) : visible.length === 0 ? (
          <div style={{ color: 'var(--fg-3)', padding: 'var(--sp-4) var(--sp-5)' }}>
            {filter ? 'No lines match filter.' : 'No log lines yet.'}
          </div>
        ) : (
          visible.map(line => (
            <LogRow key={line.id} line={line} filter={filterLower} />
          ))
        )}
      </div>

      {/* scroll-to-bottom FAB when not following */}
      {!follow && (
        <button
          onClick={scrollToBottom}
          style={{
            position: 'absolute',
            bottom: 'var(--sp-5)',
            right: 'var(--sp-5)',
            background: 'var(--accent)',
            color: '#fff',
            borderRadius: 'var(--radius-pill)',
            padding: '6px 14px',
            fontSize: 'var(--ts-sm)',
            fontWeight: 600,
            cursor: 'pointer',
            boxShadow: 'var(--shadow-md)',
            zIndex: 10,
          }}
        >
          ↓ Jump to latest
        </button>
      )}
    </div>
  )
}

// ── log row ───────────────────────────────────────────────────────────────────

function LogRow({ line, filter }: { line: LogLine; filter: string }) {
  const color = levelColor(line.level)
  const text = filter ? highlightMatch(line.text, filter) : line.text

  return (
    <div
      style={{
        padding: '1px var(--sp-5)',
        color,
        whiteSpace: 'pre-wrap',
        wordBreak: 'break-all',
        borderLeft: line.level === 'error' ? '2px solid var(--err)'
          : line.level === 'warn' ? '2px solid var(--warn)'
          : '2px solid transparent',
      }}
    >
      {text}
    </div>
  )
}

// ── helpers ───────────────────────────────────────────────────────────────────

function ConnectionBadge({ conn }: { conn: 'connecting' | 'live' | 'error' }) {
  const dot = conn === 'live' ? 'var(--ok)' : conn === 'error' ? 'var(--err)' : 'var(--warn)'
  const label = conn === 'live' ? 'Live' : conn === 'error' ? 'Reconnecting…' : 'Connecting…'
  return (
    <span style={{ display: 'flex', alignItems: 'center', gap: 5, fontSize: 'var(--ts-sm)', color: 'var(--fg-2)' }}>
      <span style={{ width: 7, height: 7, borderRadius: '50%', background: dot, flexShrink: 0 }} />
      {label}
    </span>
  )
}

function detectLevel(text: string): LogLevel {
  const lower = text.toLowerCase()
  if (/\b(error|err|fatal|critical|crit|emerg|alert)\b/.test(lower)) return 'error'
  if (/\b(warn|warning)\b/.test(lower)) return 'warn'
  if (/\b(debug|trace|verbose)\b/.test(lower)) return 'debug'
  if (/\b(info|notice)\b/.test(lower)) return 'info'
  return 'plain'
}

function levelColor(level: LogLevel): string {
  switch (level) {
    case 'error': return 'var(--err)'
    case 'warn':  return 'var(--warn)'
    case 'debug': return 'var(--fg-3)'
    case 'info':  return 'var(--fg-1)'
    default:      return 'var(--fg-1)'
  }
}

/** Render log text as React text nodes; appending rows preserves existing selection. */
function highlightMatch(text: string, filter: string) {
  const re = new RegExp(`(${escapeRegex(filter)})`, 'gi')
  return text.split(re).map((part, i) => i % 2 === 1
    ? <mark key={i} style={{ background: 'var(--warn)', color: '#000', borderRadius: 2 }}>{part}</mark>
    : part)
}

function escapeRegex(s: string): string {
  return s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
}
