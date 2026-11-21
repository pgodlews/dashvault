import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { useEffect, useRef, useState } from 'react'
import { ls, zipUrl, rmQuery, formatEventDate, parseEventDate, formatBytes, type ClipEvent } from '../lib/clips'
import { apiFetch } from '../lib/api'

export const Route = createFileRoute('/clips')({
  component: ClipsPage,
})

// ── page ──────────────────────────────────────────────────────────────────────

type CategoryFilter = 'all' | string

function ClipsPage() {
  const [events, setEvents] = useState<ClipEvent[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [filter, setFilter] = useState<CategoryFilter>('all')
  const [confirmDelete, setConfirmDelete] = useState<ClipEvent | null>(null)
  const [deleting, setDeleting] = useState<Set<string>>(new Set())
  const [sortDir, setSortDir] = useState<'desc' | 'asc'>('desc')
  // per-event detail expansion: folderPath → file list
  const [expanded, setExpanded] = useState<Set<string>>(new Set())
  const [fileCache, setFileCache] = useState<Record<string, EventDetail>>({})

  const navigate = useNavigate()

  useEffect(() => {
    let cancelled = false
    async function load() {
      setLoading(true)
      setError(null)
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
        setEvents(evts)
      } catch (err) {
        if (!cancelled) setError(String(err))
      } finally {
        if (!cancelled) setLoading(false)
      }
    }
    load()
    return () => { cancelled = true }
  }, [])

  const categories = Array.from(new Set(events.map(e => e.category))).sort()

  const visible = events
    .filter(e => filter === 'all' || e.category === filter)
    .sort((a, b) => {
      const da = parseEventDate(a.label)?.getTime() ?? 0
      const db = parseEventDate(b.label)?.getTime() ?? 0
      return sortDir === 'desc' ? db - da : da - db
    })

  async function handleDelete(evt: ClipEvent) {
    setDeleting(prev => new Set(prev).add(evt.folderPath))
    setConfirmDelete(null)
    try {
      const res = await apiFetch(rmQuery(evt.folderPath), { method: 'POST' })
      if (res.ok) {
        setEvents(prev => prev.filter(e => e.folderPath !== evt.folderPath))
        setExpanded(prev => { const s = new Set(prev); s.delete(evt.folderPath); return s })
      }
    } finally {
      setDeleting(prev => { const s = new Set(prev); s.delete(evt.folderPath); return s })
    }
  }

  async function loadDetails(evt: ClipEvent) {
    if (fileCache[evt.folderPath]) return
    try {
      const entries = await ls(evt.path)
      const files = entries
        .filter(e => e.type === 'file' && e.name.toLowerCase().endsWith('.mp4'))
        .map(e => ({ name: e.name, size: e.size ?? 0 }))
      const total = files.reduce((s, f) => s + f.size, 0)
      setFileCache(prev => ({ ...prev, [evt.folderPath]: { files, total } }))
    } catch {
      setFileCache(prev => ({ ...prev, [evt.folderPath]: { files: [], total: 0 } }))
    }
  }

  function toggleExpand(evt: ClipEvent) {
    setExpanded(prev => {
      const s = new Set(prev)
      if (s.has(evt.folderPath)) {
        s.delete(evt.folderPath)
      } else {
        s.add(evt.folderPath)
        loadDetails(evt)
      }
      return s
    })
  }

  return (
    <div style={{ padding: 'var(--sp-6)', maxWidth: 900 }}>
      {/* header */}
      <div style={{ display: 'flex', alignItems: 'baseline', gap: 'var(--sp-4)', marginBottom: 'var(--sp-5)' }}>
        <h1 style={{ margin: 0, fontSize: 'var(--ts-xl)', fontWeight: 600 }}>Clips</h1>
        {!loading && (
          <span style={{ color: 'var(--fg-2)', fontSize: 'var(--ts-sm)' }}>
            {visible.length} event{visible.length !== 1 ? 's' : ''}
          </span>
        )}
      </div>

      {/* category tabs */}
      <div style={{ display: 'flex', gap: 'var(--sp-2)', marginBottom: 'var(--sp-4)', flexWrap: 'wrap' }}>
        {(['all', ...categories] as CategoryFilter[]).map(cat => (
          <button
            key={cat}
            onClick={() => setFilter(cat)}
            style={{
              padding: '4px 12px',
              borderRadius: 'var(--radius-pill)',
              fontSize: 'var(--ts-sm)',
              background: filter === cat ? 'var(--accent)' : 'var(--bg-3)',
              color: filter === cat ? 'var(--accent-ink)' : 'var(--fg-1)',
              cursor: 'pointer',
              fontWeight: filter === cat ? 600 : 400,
            }}
          >
            {cat === 'all' ? 'All' : cat}
            {cat !== 'all' && (
              <span style={{ marginLeft: 5, color: filter === cat ? 'rgba(255,245,246,.7)' : 'var(--fg-2)', fontSize: 'var(--ts-xs)' }}>
                {events.filter(e => e.category === cat).length}
              </span>
            )}
          </button>
        ))}

        {/* sort toggle */}
        <button
          onClick={() => setSortDir(d => d === 'desc' ? 'asc' : 'desc')}
          style={{
            marginLeft: 'auto',
            padding: '4px 10px',
            borderRadius: 'var(--radius-sm)',
            fontSize: 'var(--ts-sm)',
            background: 'var(--bg-3)',
            color: 'var(--fg-1)',
            cursor: 'pointer',
            fontFamily: 'var(--font-mono)',
          }}
          title="Toggle sort order"
        >
          {sortDir === 'desc' ? '↓ Newest' : '↑ Oldest'}
        </button>
      </div>

      {/* content */}
      {loading ? (
        <SkeletonList />
      ) : error ? (
        <ErrorState message={error} />
      ) : visible.length === 0 ? (
        <EmptyState />
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--sp-2)' }}>
          {visible.map(evt => (
            <EventCard
              key={evt.folderPath}
              evt={evt}
              isDeleting={deleting.has(evt.folderPath)}
              isExpanded={expanded.has(evt.folderPath)}
              detail={fileCache[evt.folderPath] ?? null}
              onToggleExpand={() => toggleExpand(evt)}
              onOpenViewer={() => navigate({ to: '/viewer', search: { clip: evt.folderPath.replace(/^TeslaCam\//, '') } })}
              onDownload={() => window.open(zipUrl(evt.folderPath), '_blank')}
              onDeleteRequest={() => setConfirmDelete(evt)}
            />
          ))}
        </div>
      )}

      {/* delete confirmation modal */}
      {confirmDelete && (
        <DeleteModal
          evt={confirmDelete}
          onConfirm={() => handleDelete(confirmDelete)}
          onCancel={() => setConfirmDelete(null)}
        />
      )}
    </div>
  )
}

// ── event card ────────────────────────────────────────────────────────────────

interface EventDetail {
  files: { name: string; size: number }[]
  total: number
}

interface EventCardProps {
  evt: ClipEvent
  isDeleting: boolean
  isExpanded: boolean
  detail: EventDetail | null
  onToggleExpand: () => void
  onOpenViewer: () => void
  onDownload: () => void
  onDeleteRequest: () => void
}

function EventCard({
  evt, isDeleting, isExpanded, detail,
  onToggleExpand, onOpenViewer, onDownload, onDeleteRequest,
}: EventCardProps) {
  return (
    <div style={{
      background: 'var(--bg-2)',
      border: '1px solid var(--border)',
      borderRadius: 'var(--radius-md)',
      overflow: 'hidden',
      opacity: isDeleting ? 0.5 : 1,
      transition: 'opacity .2s',
    }}>
      {/* main row */}
      <div style={{
        display: 'flex',
        alignItems: 'center',
        padding: 'var(--sp-3) var(--sp-4)',
        gap: 'var(--sp-3)',
      }}>
        {/* expand toggle */}
        <button
          onClick={onToggleExpand}
          title="Show files"
          style={{
            fontSize: 10,
            color: 'var(--fg-3)',
            cursor: 'pointer',
            transform: isExpanded ? 'rotate(90deg)' : 'none',
            transition: 'transform .15s',
            flexShrink: 0,
          }}
        >
          ▶
        </button>

        {/* category badge */}
        <span style={{
          fontSize: 'var(--ts-xs)',
          padding: '2px 7px',
          borderRadius: 'var(--radius-pill)',
          background: categoryColor(evt.category) + '22',
          color: categoryColor(evt.category),
          fontWeight: 600,
          letterSpacing: '.03em',
          flexShrink: 0,
        }}>
          {categoryShort(evt.category)}
        </span>

        {/* timestamp */}
        <div style={{ flex: 1, minWidth: 0 }}>
          <div style={{ fontSize: 'var(--ts-md)', fontWeight: 500, color: 'var(--fg-0)' }}>
            {formatEventDate(evt.label)}
          </div>
          <div style={{ fontSize: 'var(--ts-xs)', color: 'var(--fg-3)', fontFamily: 'var(--font-mono)', marginTop: 2 }}>
            {evt.label}
            {detail && (
              <span style={{ marginLeft: 8 }}>
                · {detail.files.length} file{detail.files.length !== 1 ? 's' : ''}
                · {formatBytes(detail.total)}
              </span>
            )}
          </div>
        </div>

        {/* actions */}
        <div style={{ display: 'flex', gap: 'var(--sp-2)', flexShrink: 0 }}>
          <ActionBtn onClick={onOpenViewer} title="Open in Viewer">▶ View</ActionBtn>
          <ActionBtn onClick={onDownload} title="Download ZIP">⬇ ZIP</ActionBtn>
          <ActionBtn
            onClick={onDeleteRequest}
            danger
            disabled={isDeleting}
            title="Delete event"
          >
            {isDeleting ? '…' : '✕'}
          </ActionBtn>
        </div>
      </div>

      {/* expanded file list */}
      {isExpanded && (
        <div style={{
          borderTop: '1px solid var(--border)',
          background: 'var(--bg-1)',
          padding: 'var(--sp-2) var(--sp-4) var(--sp-3)',
        }}>
          {detail === null ? (
            <div style={{ color: 'var(--fg-2)', fontSize: 'var(--ts-sm)', padding: 'var(--sp-2) 0' }}>Loading…</div>
          ) : detail.files.length === 0 ? (
            <div style={{ color: 'var(--fg-2)', fontSize: 'var(--ts-sm)', padding: 'var(--sp-2) 0' }}>No files found</div>
          ) : (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 1, marginTop: 'var(--sp-1)' }}>
              {detail.files.map(f => (
                <div key={f.name} style={{
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: 'space-between',
                  fontSize: 'var(--ts-sm)',
                  padding: '2px 0',
                  gap: 'var(--sp-4)',
                }}>
                  <span style={{ fontFamily: 'var(--font-mono)', color: 'var(--fg-1)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                    {f.name}
                  </span>
                  <span style={{ color: 'var(--fg-3)', fontFamily: 'var(--font-mono)', flexShrink: 0 }}>
                    {formatBytes(f.size)}
                  </span>
                </div>
              ))}
              <div style={{
                display: 'flex',
                justifyContent: 'flex-end',
                marginTop: 'var(--sp-2)',
                paddingTop: 'var(--sp-1)',
                borderTop: '1px solid var(--border)',
                fontSize: 'var(--ts-sm)',
                color: 'var(--fg-2)',
                fontFamily: 'var(--font-mono)',
              }}>
                Total: {formatBytes(detail.total)}
              </div>
            </div>
          )}
        </div>
      )}
    </div>
  )
}

// ── delete modal ──────────────────────────────────────────────────────────────

function DeleteModal({ evt, onConfirm, onCancel }: { evt: ClipEvent; onConfirm: () => void; onCancel: () => void }) {
  // Close on Escape
  const ref = useRef<HTMLButtonElement>(null)
  useEffect(() => { ref.current?.focus() }, [])
  useEffect(() => {
    function onKey(e: KeyboardEvent) { if (e.code === 'Escape') onCancel() }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onCancel])

  return (
    <div
      onClick={onCancel}
      style={{
        position: 'fixed', inset: 0,
        background: 'rgba(0,0,0,.6)',
        display: 'flex', alignItems: 'center', justifyContent: 'center',
        zIndex: 100,
      }}
    >
      <div
        onClick={e => e.stopPropagation()}
        style={{
          background: 'var(--bg-2)',
          border: '1px solid var(--border-strong)',
          borderRadius: 'var(--radius-lg)',
          padding: 'var(--sp-6)',
          width: 380,
          boxShadow: 'var(--shadow-md)',
        }}
      >
        <div style={{ fontSize: 'var(--ts-lg)', fontWeight: 600, marginBottom: 'var(--sp-3)' }}>
          Delete clip?
        </div>
        <div style={{ fontSize: 'var(--ts-sm)', color: 'var(--fg-1)', marginBottom: 'var(--sp-2)' }}>
          This will permanently delete:
        </div>
        <div style={{
          fontFamily: 'var(--font-mono)',
          fontSize: 'var(--ts-sm)',
          color: 'var(--fg-0)',
          background: 'var(--bg-3)',
          padding: 'var(--sp-2) var(--sp-3)',
          borderRadius: 'var(--radius-sm)',
          marginBottom: 'var(--sp-5)',
          wordBreak: 'break-all',
        }}>
          {evt.folderPath}
        </div>
        <div style={{ display: 'flex', gap: 'var(--sp-3)', justifyContent: 'flex-end' }}>
          <button
            ref={ref}
            onClick={onCancel}
            style={{
              padding: '6px 16px',
              borderRadius: 'var(--radius-sm)',
              fontSize: 'var(--ts-sm)',
              background: 'var(--bg-3)',
              color: 'var(--fg-1)',
              cursor: 'pointer',
            }}
          >
            Cancel
          </button>
          <button
            onClick={onConfirm}
            style={{
              padding: '6px 16px',
              borderRadius: 'var(--radius-sm)',
              fontSize: 'var(--ts-sm)',
              background: 'var(--err)',
              color: '#fff',
              fontWeight: 600,
              cursor: 'pointer',
            }}
          >
            Delete
          </button>
        </div>
      </div>
    </div>
  )
}

// ── small helpers ─────────────────────────────────────────────────────────────

function ActionBtn({
  onClick, children, danger, disabled, title,
}: {
  onClick: () => void
  children: React.ReactNode
  danger?: boolean
  disabled?: boolean
  title?: string
}) {
  return (
    <button
      onClick={onClick}
      disabled={disabled}
      title={title}
      style={{
        padding: '4px 10px',
        borderRadius: 'var(--radius-sm)',
        fontSize: 'var(--ts-sm)',
        background: danger ? 'transparent' : 'var(--bg-3)',
        color: danger ? 'var(--err)' : 'var(--fg-1)',
        border: danger ? '1px solid var(--err)' : '1px solid transparent',
        cursor: disabled ? 'not-allowed' : 'pointer',
        opacity: disabled ? 0.5 : 1,
        whiteSpace: 'nowrap',
      }}
    >
      {children}
    </button>
  )
}

function categoryColor(cat: string): string {
  if (cat === 'SavedClips') return 'var(--ok)'
  if (cat === 'SentryClips') return 'var(--warn)'
  return 'var(--accent)'
}

function categoryShort(cat: string): string {
  if (cat === 'SavedClips') return 'Saved'
  if (cat === 'SentryClips') return 'Sentry'
  if (cat === 'RecentClips') return 'Recent'
  return cat
}

// ── loading / error / empty ───────────────────────────────────────────────────

function SkeletonList() {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--sp-2)' }}>
      {Array.from({ length: 6 }, (_, i) => (
        <div key={i} style={{
          height: 60,
          background: 'var(--bg-2)',
          border: '1px solid var(--border)',
          borderRadius: 'var(--radius-md)',
          animation: 'pulse 1.6s ease-in-out infinite',
          animationDelay: `${i * 80}ms`,
        }} />
      ))}
      <style>{`@keyframes pulse { 0%,100%{opacity:1} 50%{opacity:.4} }`}</style>
    </div>
  )
}

function ErrorState({ message }: { message: string }) {
  return (
    <div style={{
      padding: 'var(--sp-6)',
      background: 'var(--bg-2)',
      border: '1px solid var(--border)',
      borderRadius: 'var(--radius-md)',
      color: 'var(--err)',
      fontSize: 'var(--ts-sm)',
      fontFamily: 'var(--font-mono)',
    }}>
      {message}
    </div>
  )
}

function EmptyState() {
  return (
    <div style={{ padding: 'var(--sp-8)', textAlign: 'center', color: 'var(--fg-2)' }}>
      <div style={{ fontSize: 36, marginBottom: 'var(--sp-3)' }}>📂</div>
      <div>No clips found in TeslaCam</div>
    </div>
  )
}
