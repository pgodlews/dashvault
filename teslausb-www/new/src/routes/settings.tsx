import { createFileRoute } from '@tanstack/react-router'
import { useEffect, useRef, useState } from 'react'
import { apiFetch } from '../lib/api'
import { useStatus } from '../hooks/useStatus'
import { Icon, type IconName } from '../components/Icon'
import type { TelemetryDays, TelemetrySettings } from '../lib/telemetry'
import { PROFILE_INFO, type PowerConfig, type PowerConfigResponse, type PowerProfile, type ThermalHistoryResponse } from '../lib/power'

export const Route = createFileRoute('/settings')({
  component: SettingsPage,
})

/* ─── page shell ─────────────────────────────────────────────────────────── */

function SettingsPage() {
  const mainRef = useRef<HTMLElement>(null)
  return (
    <div style={{
      display: 'grid',
      gridTemplateColumns: '220px 1fr',
      gap: 1,
      background: 'var(--border)',
      height: '100%',
      minHeight: 0,
    }}>
      <SettingsNav scrollRef={mainRef} />
      <main ref={mainRef} style={{
        background: 'var(--bg-0)',
        overflowY: 'auto',
        padding: 'var(--sp-7) var(--sp-8)',
      }}>
        <div style={{
          maxWidth: 880,
          margin: '0 auto',
          display: 'flex',
          flexDirection: 'column',
          gap: 'var(--sp-7)',
        }}>
          <header>
            <h1 style={{
              fontSize: 'var(--ts-2xl)',
              fontWeight: 600,
              letterSpacing: '-.01em',
              margin: '0 0 var(--sp-1)',
            }}>Settings</h1>
            <p style={{
              color: 'var(--fg-2)',
              fontSize: 'var(--ts-sm)',
              margin: 0,
            }}>
              Configuration lives on the Pi in{' '}
              <code style={{
                fontFamily: 'var(--font-mono)',
                fontSize: 12,
                background: 'var(--bg-2)',
                padding: '1px 5px',
                borderRadius: 3,
              }}>/root/teslausb_setup_variables.conf</code>{' '}
              and takes effect on the next archive cycle.
            </p>
          </header>

          <ArchiveSection />
          <TelegramSection />
          <SnapshotIntervalSection />
          <TelemetrySection />
          <OtaSection />
          <USBDrivesSection />
          <WifiSection />
          <PowerThermalSection />
          <BenchmarkSection />
          <DangerSection />
        </div>
      </main>
    </div>
  )
}

/* ─── sidebar ────────────────────────────────────────────────────────────── */

const NAV_ITEMS = [
  { id: 'archive', label: 'Archive storage' },
  { id: 'notifications', label: 'Notifications' },
  { id: 'network', label: 'Network & Wi-Fi' },
  { id: 'power', label: 'Power & Thermal' },
  { id: 'advanced', label: 'Advanced' },
  { id: 'danger', label: 'Danger Zone' },
]

function SettingsNav({ scrollRef }: { scrollRef: React.RefObject<HTMLElement | null> }) {
  const [active, setActive] = useState('archive')

  useEffect(() => {
    const container = scrollRef.current
    if (!container) return
    const onScroll = () => {
      const sections = NAV_ITEMS.map(it => document.getElementById(it.id))
      const threshold = container.getBoundingClientRect().top + 100
      for (let i = sections.length - 1; i >= 0; i--) {
        const s = sections[i]
        if (s && s.getBoundingClientRect().top <= threshold) {
          setActive(NAV_ITEMS[i].id)
          break
        }
      }
    }
    container.addEventListener('scroll', onScroll)
    onScroll()
    return () => container.removeEventListener('scroll', onScroll)
  }, [scrollRef])

  const scrollTo = (id: string) => {
    const el = document.getElementById(id)
    if (el) el.scrollIntoView({ behavior: 'smooth', block: 'start' })
  }

  return (
    <nav style={{
      background: 'var(--bg-1)',
      padding: 'var(--sp-4) var(--sp-2)',
      overflowY: 'auto',
    }}>
      <h5 style={{
        margin: 'var(--sp-3) var(--sp-3) var(--sp-2)',
        fontSize: 'var(--ts-xs)',
        color: 'var(--fg-2)',
        fontWeight: 600,
        letterSpacing: '.1em',
        textTransform: 'uppercase',
      }}>dashvault</h5>
      {NAV_ITEMS.map(it => (
        <a
          key={it.id}
          onClick={() => scrollTo(it.id)}
          style={{
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'space-between',
            padding: '8px var(--sp-3)',
            borderRadius: 'var(--radius-sm)',
            color: active === it.id ? 'var(--fg-0)' : 'var(--fg-1)',
            fontSize: 'var(--ts-sm)',
            fontWeight: 500,
            textDecoration: 'none',
            cursor: 'pointer',
            borderLeft: '2px solid transparent',
            marginLeft: -2,
            background: active === it.id ? 'var(--bg-2)' : 'transparent',
            borderLeftColor: active === it.id ? 'var(--accent)' : 'transparent',
          }}
        >
          <span>{it.label}</span>
        </a>
      ))}
    </nav>
  )
}

/* ─── shared primitives ──────────────────────────────────────────────────── */

interface SectionCardProps {
  id: string
  icon: React.ReactNode
  title: string
  sub?: string
  status?: { led: 'ok' | 'warn' | 'err' | 'idle'; text: string }
  dirty?: boolean
  children: React.ReactNode
}

function SectionCard({ id, icon, title, sub, status, dirty, children }: SectionCardProps) {
  return (
    <section
      id={id}
      style={{
        background: 'var(--bg-1)',
        border: `1px solid ${dirty ? 'rgba(245,158,11,.4)' : 'var(--border)'}`,
        borderRadius: 'var(--radius-lg)',
        overflow: 'hidden',
      }}
    >
      <header style={{
        display: 'flex',
        alignItems: 'center',
        gap: 'var(--sp-3)',
        padding: 'var(--sp-4) var(--sp-5)',
        borderBottom: '1px solid var(--border)',
        background: 'var(--bg-1)',
      }}>
        <div style={{
          width: 32, height: 32,
          borderRadius: 'var(--radius-sm)',
          background: 'var(--bg-3)',
          color: 'var(--fg-0)',
          display: 'grid',
          placeItems: 'center',
          flexShrink: 0,
        }}>
          {icon}
        </div>
        <div style={{ flex: 1, minWidth: 0 }}>
          <h2 style={{
            margin: 0,
            fontSize: 'var(--ts-lg)',
            fontWeight: 600,
            display: 'flex',
            alignItems: 'center',
            gap: 'var(--sp-2)',
          }}>
            {title}
            {dirty && (
              <span style={{
                width: 8, height: 8, borderRadius: '50%',
                background: 'var(--warn)',
                boxShadow: '0 0 8px var(--warn)',
              }} title="Unsaved changes" />
            )}
          </h2>
          {sub && (
            <div style={{
              fontSize: 'var(--ts-xs)',
              color: 'var(--fg-2)',
              marginTop: 2,
            }}>{sub}</div>
          )}
        </div>
        {status && (
          <span style={{
            display: 'inline-flex',
            alignItems: 'center',
            gap: 6,
            padding: '4px 10px',
            borderRadius: 'var(--radius-pill)',
            fontFamily: 'var(--font-mono)',
            fontSize: 'var(--ts-xs)',
            letterSpacing: '.04em',
            border: '1px solid var(--border)',
            background: 'var(--bg-2)',
            color: 'var(--fg-1)',
            flexShrink: 0,
          }}>
            <span style={{
              width: 6, height: 6, borderRadius: '50%',
              background: status.led === 'ok' ? 'var(--ok)'
                : status.led === 'warn' ? 'var(--warn)'
                : status.led === 'err' ? 'var(--err)'
                : 'var(--fg-3)',
              boxShadow: status.led !== 'idle' ? `0 0 6px ${status.led === 'ok' ? 'var(--ok)' : status.led === 'warn' ? 'var(--warn)' : 'var(--err)'}` : undefined,
            }} />
            {status.text}
          </span>
        )}
      </header>
      {children}
    </section>
  )
}

function SectionBody({ children }: { children: React.ReactNode }) {
  return (
    <div style={{
      padding: 'var(--sp-5)',
      display: 'flex',
      flexDirection: 'column',
      gap: 'var(--sp-5)',
    }}>
      {children}
    </div>
  )
}

function SectionFooter({ children }: { children: React.ReactNode }) {
  return (
    <div style={{
      display: 'flex',
      alignItems: 'center',
      gap: 'var(--sp-2)',
      padding: 'var(--sp-4) var(--sp-5)',
      borderTop: '1px solid var(--border)',
      background: 'var(--bg-1)',
    }}>
      {children}
    </div>
  )
}

function Field({ label, hint, children, row }: {
  label: string
  hint?: string
  children: React.ReactNode
  row?: boolean
}) {
  return (
    <label style={{
      display: 'flex',
      flexDirection: row ? 'row' : 'column',
      gap: 6,
      alignItems: row ? 'center' : 'stretch',
      justifyContent: row ? 'space-between' : undefined,
    }}>
      <span style={{
        fontSize: 'var(--ts-sm)',
        fontWeight: 500,
        color: 'var(--fg-0)',
      }}>{label}</span>
      {children}
      {hint && !row && (
        <span style={{
          fontSize: 'var(--ts-xs)',
          color: 'var(--fg-2)',
          lineHeight: 1.5,
        }}>{hint}</span>
      )}
    </label>
  )
}

function FieldGrid({ children }: { children: React.ReactNode }) {
  return (
    <div style={{
      display: 'grid',
      gridTemplateColumns: '1fr 1fr',
      gap: 'var(--sp-4)',
    }}>
      {children}
    </div>
  )
}

function Input(props: React.InputHTMLAttributes<HTMLInputElement> & { mono?: boolean }) {
  const { mono, style, ...rest } = props
  return (
    <input
      {...rest}
      style={{
        fontFamily: mono ? 'var(--font-mono)' : 'var(--font-sans)',
        fontSize: 'var(--ts-md)',
        color: 'var(--fg-0)',
        background: 'var(--bg-0)',
        border: '1px solid var(--border)',
        borderRadius: 'var(--radius-sm)',
        padding: '10px var(--sp-3)',
        width: '100%',
        transition: 'border-color .1s, background .1s',
        letterSpacing: mono ? '.02em' : undefined,
        ...style,
      }}
    />
  )
}

function InputGroup({ children }: { children: React.ReactNode }) {
  return (
    <div style={{ position: 'relative', display: 'flex' }}>
      {children}
    </div>
  )
}

function Toggle({ on, onClick }: { on: boolean; onClick: () => void }) {
  return (
    <div
      onClick={onClick}
      style={{
        width: 36, height: 20,
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
        left: on ? 18 : 2,
        top: 2,
        width: 16, height: 16,
        borderRadius: '50%',
        background: on ? '#fff' : 'var(--fg-2)',
        transition: 'left .12s, background .12s',
      }} />
    </div>
  )
}

function Btn({ children, onClick, disabled, loading, variant = 'default', icon }: {
  children: React.ReactNode
  onClick?: () => void
  disabled?: boolean
  loading?: boolean
  variant?: 'default' | 'primary' | 'ghost' | 'danger'
  icon?: React.ReactNode
}) {
  const bg = variant === 'primary' ? 'var(--accent)'
    : variant === 'ghost' ? 'transparent'
    : variant === 'danger' ? 'transparent'
    : 'var(--bg-2)'
  const color = variant === 'primary' ? 'var(--accent-ink)'
    : variant === 'danger' ? 'var(--accent)'
    : 'var(--fg-0)'
  const border = variant === 'ghost' ? '1px solid var(--border)'
    : variant === 'danger' ? '1px solid var(--accent-dim)'
    : '1px solid var(--border-strong)'

  return (
    <button
      onClick={onClick}
      disabled={disabled || loading}
      style={{
        display: 'inline-flex',
        alignItems: 'center',
        gap: 6,
        padding: '8px 14px',
        borderRadius: 'var(--radius-sm)',
        fontSize: 'var(--ts-sm)',
        fontWeight: 500,
        border,
        background: bg,
        color,
        cursor: disabled || loading ? 'not-allowed' : 'pointer',
        opacity: disabled && !loading ? 0.4 : 1,
        transition: 'background .1s, border-color .1s',
      }}
    >
      {loading && <Spinner size={14} />}
      {icon && !loading && icon}
      {children}
    </button>
  )
}

function BtnPrimary(props: Omit<Parameters<typeof Btn>[0], 'variant'>) {
  return <Btn {...props} variant="primary" />
}

function Callout({ children, type = 'info' }: { children: React.ReactNode; type?: 'info' | 'warn' | 'danger' }) {
  const borderColor = type === 'warn' ? 'var(--warn)' : type === 'danger' ? 'var(--accent)' : 'var(--fg-2)'
  const bg = type === 'warn' ? 'rgba(245,158,11,.06)' : type === 'danger' ? 'rgba(225,29,42,.06)' : 'var(--bg-2)'
  const iconColor = type === 'warn' ? 'var(--warn)' : type === 'danger' ? 'var(--accent)' : 'var(--fg-2)'
  const iconName = type === 'warn' ? 'alert' : type === 'danger' ? 'alert' : 'info' as const

  return (
    <div style={{
      display: 'flex',
      gap: 'var(--sp-3)',
      padding: 'var(--sp-3) var(--sp-4)',
      background: bg,
      borderRadius: 'var(--radius-md)',
      borderLeft: `2px solid ${borderColor}`,
      fontSize: 'var(--ts-sm)',
      color: 'var(--fg-1)',
      lineHeight: 1.5,
    }}>
      <span style={{ flexShrink: 0, marginTop: 2, color: iconColor }}>
        <Icon name={iconName} size={16} />
      </span>
      <div>{children}</div>
    </div>
  )
}

function Spinner({ size = 16 }: { size?: number }) {
  return (
    <span style={{
      display: 'inline-block',
      width: size,
      height: size,
      borderRadius: '50%',
      border: `2px solid currentColor`,
      borderTopColor: 'transparent',
      animation: 'spin .7s linear infinite',
      flexShrink: 0,
    }}>
      <style>{`@keyframes spin { to { transform: rotate(360deg); } }`}</style>
    </span>
  )
}

function ResultPill({ kind, children }: { kind: 'ok' | 'err' | 'info'; children: React.ReactNode }) {
  const color = kind === 'ok' ? 'var(--ok)' : kind === 'err' ? 'var(--err)' : 'var(--fg-1)'
  const bg = kind === 'ok' ? 'rgba(16,185,129,.1)' : kind === 'err' ? 'rgba(225,29,42,.1)' : 'var(--bg-2)'
  return (
    <span style={{
      display: 'inline-flex',
      alignItems: 'center',
      gap: 6,
      fontSize: 'var(--ts-xs)',
      padding: '4px 10px',
      borderRadius: 'var(--radius-sm)',
      fontFamily: 'var(--font-mono)',
      color,
      background: bg,
    }}>
      {children}
    </span>
  )
}

function DirtyText() {
  return (
    <span style={{
      fontSize: 'var(--ts-xs)',
      color: 'var(--warn)',
      display: 'inline-flex',
      alignItems: 'center',
      gap: 6,
      fontFamily: 'var(--font-mono)',
    }}>
      <span style={{
        width: 6, height: 6, borderRadius: '50%',
        background: 'var(--warn)',
        boxShadow: '0 0 6px var(--warn)',
      }} />
      unsaved
    </span>
  )
}

/* ─── archive ────────────────────────────────────────────────────────────── */

type ArchiveSystem = 'cifs' | 'nfs' | 'rsync' | 'rclone' | 'none'

// The saved password is never sent to the browser: password_set says one exists,
// and saving with an empty password keeps it.
interface CIFSConfig  { server: string; share: string; user: string; password: string; password_set?: boolean; domain?: string; version?: string; sec?: string }
interface NFSConfig   { server: string; path: string }
interface RsyncConfig { user: string; server: string; path: string }
interface RcloneConfig{ drive: string; path: string }

interface ArchiveConfig {
  system: ArchiveSystem
  saved_clips: boolean
  sentry_clips: boolean
  recent_clips: boolean
  recent_cameras: RecentCamera[]
  recent_prune_snapshots: boolean
  detected_cameras?: RecentCamera[]
  cifs:   CIFSConfig
  nfs:    NFSConfig
  rsync:  RsyncConfig
  rclone: RcloneConfig
}

// Cameras the car records; HW4 cars add the pillar cameras.
const RECENT_CAMERAS = [
  { id: 'front',          label: 'Front' },
  { id: 'back',           label: 'Back' },
  { id: 'left_repeater',  label: 'Left repeater' },
  { id: 'right_repeater', label: 'Right repeater' },
  { id: 'left_pillar',    label: 'Left pillar' },
  { id: 'right_pillar',   label: 'Right pillar' },
] as const

type RecentCamera = (typeof RECENT_CAMERAS)[number]['id']

// Cameras to offer: the ones found in this car's clips, else the four every
// car records. HW4 pillar cameras only appear once the car has recorded them.
function offeredCameras(detected: RecentCamera[] | undefined) {
  const ids = detected && detected.length > 0
    ? detected
    : (['front', 'back', 'left_repeater', 'right_repeater'] as RecentCamera[])
  return RECENT_CAMERAS.filter(c => ids.includes(c.id))
}

// Selecting every offered camera is saved as "all cameras", so a camera the
// car starts recording later is not silently left out.
function cameraSelectionForSave(cfg: ArchiveConfig): RecentCamera[] {
  const offered = offeredCameras(cfg.detected_cameras).map(c => c.id)
  const selected = offered.filter(id => cfg.recent_cameras.includes(id))
  return selected.length === offered.length ? RECENT_CAMERAS.map(c => c.id) : selected
}

const EMPTY_ARCHIVE_CONFIG: ArchiveConfig = {
  system: 'none',
  saved_clips: true,
  sentry_clips: true,
  recent_clips: false,
  recent_cameras: RECENT_CAMERAS.map(c => c.id),
  recent_prune_snapshots: false,
  cifs:   { server: '', share: '', user: '', password: '', domain: '', version: '', sec: '' },
  nfs:    { server: '', path: '' },
  rsync:  { user: '', server: '', path: '' },
  rclone: { drive: '', path: '' },
}

const BACKENDS: { id: ArchiveSystem; name: string; tag: string; icon: IconName }[] = [
  { id: 'cifs',   name: 'CIFS/SMB', tag: 'Windows / NAS', icon: 'folder' },
  { id: 'nfs',    name: 'NFS',      tag: 'Linux / NAS',   icon: 'hdd' },
  { id: 'rsync',  name: 'rsync',    tag: 'SSH',           icon: 'sync' },
  { id: 'rclone', name: 'rclone',   tag: 'Cloud',         icon: 'cloud' },
  { id: 'none',   name: 'None',     tag: 'SD only',       icon: 'plug' },
]

function ArchiveSection() {
  const [cfg, setCfg] = useState<ArchiveConfig | null>(null)
  const [draft, setDraft] = useState<ArchiveConfig>(EMPTY_ARCHIVE_CONFIG)
  const [loadErr, setLoadErr] = useState('')
  const [saveStatus, setSaveStatus] = useState<'idle' | 'saving' | 'done' | 'error'>('idle')
  const [saveMsg, setSaveMsg] = useState('')
  const [syncStatus, setSyncStatus] = useState<'idle' | 'running' | 'done' | 'error'>('idle')
  const [syncMsg, setSyncMsg] = useState('')
  const [reveal, setReveal] = useState(false)

  useEffect(() => {
    apiFetch('/api/config/archive')
      .then(r => r.json())
      .then((data: ArchiveConfig) => {
        const merged = { ...EMPTY_ARCHIVE_CONFIG, ...data,
          cifs:   { ...EMPTY_ARCHIVE_CONFIG.cifs,   ...data.cifs },
          nfs:    { ...EMPTY_ARCHIVE_CONFIG.nfs,    ...data.nfs },
          rsync:  { ...EMPTY_ARCHIVE_CONFIG.rsync,  ...data.rsync },
          rclone: { ...EMPTY_ARCHIVE_CONFIG.rclone, ...data.rclone },
        }
        setCfg(merged); setDraft(merged)
      })
      .catch(() => setLoadErr('Could not load archive config.'))
  }, [])

  const dirty = cfg !== null && JSON.stringify(draft) !== JSON.stringify(cfg)

  async function save() {
    setSaveStatus('saving'); setSaveMsg('')
    try {
      const res = await apiFetch('/api/config/archive', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ ...draft, recent_cameras: cameraSelectionForSave(draft) }),
      })
      if (!res.ok) { const e = await res.json(); throw new Error(e.error || 'Save failed') }
      const saved: ArchiveConfig = await res.json()
      const merged = { ...EMPTY_ARCHIVE_CONFIG, ...saved,
        cifs:   { ...EMPTY_ARCHIVE_CONFIG.cifs,   ...saved.cifs },
        nfs:    { ...EMPTY_ARCHIVE_CONFIG.nfs,    ...saved.nfs },
        rsync:  { ...EMPTY_ARCHIVE_CONFIG.rsync,  ...saved.rsync },
        rclone: { ...EMPTY_ARCHIVE_CONFIG.rclone, ...saved.rclone },
      }
      setCfg(merged); setDraft(merged)
      setSaveStatus('done'); setSaveMsg('Saved — archiveloop restarted.')
    } catch (e: unknown) {
      setSaveStatus('error'); setSaveMsg(e instanceof Error ? e.message : 'Save failed.')
    }
  }

  async function triggerSync() {
    setSyncStatus('running'); setSyncMsg('')
    try {
      const res = await apiFetch('/cgi-bin/trigger_sync.sh', { method: 'POST' })
      const text = await res.text()
      setSyncStatus('done'); setSyncMsg(text.trim() || 'Sync triggered.')
    } catch {
      setSyncStatus('error'); setSyncMsg('Request failed.')
    }
  }

  function setCIFS(k: keyof CIFSConfig, v: string)    { setDraft(d => ({ ...d, cifs:   { ...d.cifs,   [k]: v } })) }
  function setNFS(k: keyof NFSConfig, v: string)       { setDraft(d => ({ ...d, nfs:    { ...d.nfs,    [k]: v } })) }
  function setRsync(k: keyof RsyncConfig, v: string)   { setDraft(d => ({ ...d, rsync:  { ...d.rsync,  [k]: v } })) }
  function setRclone(k: keyof RcloneConfig, v: string) { setDraft(d => ({ ...d, rclone: { ...d.rclone, [k]: v } })) }

  const statusLed: 'ok' | 'idle' = draft.system === 'none' ? 'idle' : 'ok'
  const statusText = draft.system === 'none' ? 'local only' : `backend · ${BACKENDS.find(b => b.id === draft.system)?.name.toLowerCase()}`

  return (
    <SectionCard
      id="archive"
      icon={<Icon name="hdd" size={16} />}
      title="Archive storage"
      sub="Where Dashvault ships clips after each drive. Runs at the end of every archive cycle."
      status={{ led: statusLed, text: statusText }}
      dirty={dirty}
    >
      <SectionBody>
        {loadErr && <Callout type="danger">{loadErr}</Callout>}

        {/* Backend picker */}
        <div>
          <label style={{
            fontSize: 'var(--ts-sm)',
            fontWeight: 500,
            color: 'var(--fg-0)',
            marginBottom: 6,
            display: 'block',
          }}>Backend</label>
          <div style={{
            display: 'grid',
            gridTemplateColumns: 'repeat(5, 1fr)',
            gap: 'var(--sp-2)',
          }}>
            {BACKENDS.map(b => {
              const active = draft.system === b.id
              return (
                <button
                  key={b.id}
                  onClick={() => { setDraft(d => ({ ...d, system: b.id })); setSaveStatus('idle'); setSaveMsg('') }}
                  style={{
                    display: 'flex',
                    flexDirection: 'column',
                    alignItems: 'center',
                    gap: 'var(--sp-2)',
                    padding: 'var(--sp-4) var(--sp-2)',
                    background: active ? 'rgba(225,29,42,.08)' : 'var(--bg-2)',
                    border: `1px solid ${active ? 'var(--accent)' : 'var(--border)'}`,
                    borderRadius: 'var(--radius-md)',
                    color: active ? 'var(--fg-0)' : 'var(--fg-1)',
                    cursor: 'pointer',
                    textAlign: 'center',
                    transition: 'border-color .12s, background .12s, transform .12s',
                    boxShadow: active ? '0 0 0 1px var(--accent), 0 6px 18px rgba(225,29,42,.18)' : undefined,
                  }}
                >
                  <div style={{
                    width: 42, height: 42,
                    display: 'grid', placeItems: 'center',
                    borderRadius: 'var(--radius-sm)',
                    background: active ? 'var(--accent)' : 'var(--bg-1)',
                    color: active ? 'var(--accent-ink)' : 'var(--fg-0)',
                  }}>
                    <Icon name={b.icon} size={22} />
                  </div>
                  <div style={{
                    fontFamily: 'var(--font-mono)',
                    fontSize: 'var(--ts-sm)',
                    fontWeight: 600,
                    letterSpacing: '.02em',
                  }}>{b.name}</div>
                  <div style={{
                    fontSize: 10,
                    color: 'var(--fg-2)',
                    textTransform: 'uppercase',
                    letterSpacing: '.08em',
                  }}>{b.tag}</div>
                </button>
              )
            })}
          </div>
        </div>

        <div>
          <div style={{ marginBottom: 8, color: 'var(--fg-0)', fontSize: 'var(--ts-sm)', fontWeight: 500 }}>
            Content to archive
          </div>
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(3, 1fr)', gap: 12 }}>
            <Field label="SavedClips" hint="Driver-saved events">
              <Toggle on={draft.saved_clips} onClick={() => setDraft(d => ({ ...d, saved_clips: !d.saved_clips }))} />
            </Field>
            <Field label="SentryClips" hint="Sentry and security events">
              <Toggle on={draft.sentry_clips} onClick={() => setDraft(d => ({ ...d, sentry_clips: !d.sentry_clips }))} />
            </Field>
            <Field label="RecentClips" hint="Continuous driving history; off by default for new installs">
              <Toggle on={draft.recent_clips} onClick={() => setDraft(d => ({ ...d, recent_clips: !d.recent_clips }))} />
            </Field>
          </div>
          <div style={{ marginTop: 14 }} data-testid="recent-cameras">
            <div style={{ marginBottom: 6, color: 'var(--fg-0)', fontSize: 'var(--ts-sm)', fontWeight: 500 }}>
              RecentClips cameras
            </div>
            <div style={{ display: 'flex', flexWrap: 'wrap', gap: 8 }}>
              {offeredCameras(draft.detected_cameras).map((c, _, offered) => {
                const on = draft.recent_cameras.includes(c.id)
                const last = on && offered.filter(x => draft.recent_cameras.includes(x.id)).length === 1
                return (
                  <button
                    key={c.id}
                    type="button"
                    aria-pressed={on}
                    disabled={last}
                    title={last ? 'Keep at least one camera, or turn RecentClips off' : undefined}
                    onClick={() => setDraft(d => ({
                      ...d,
                      recent_cameras: on
                        ? d.recent_cameras.filter(x => x !== c.id)
                        : RECENT_CAMERAS.map(x => x.id).filter(x => x === c.id || d.recent_cameras.includes(x)),
                    }))}
                    style={{
                      padding: '6px 12px',
                      borderRadius: 999,
                      fontSize: 'var(--ts-sm)',
                      border: on ? '1px solid var(--accent)' : '1px solid var(--border)',
                      background: on ? 'var(--accent-dim)' : 'transparent',
                      color: on ? 'var(--fg-0)' : 'var(--fg-2)',
                      cursor: last ? 'not-allowed' : 'pointer',
                    }}
                  >{c.label}</button>
                )
              })}
            </div>
            <div style={{ marginTop: 6, fontSize: 'var(--ts-xs)', color: 'var(--fg-2)', lineHeight: 1.5 }}>
              {draft.recent_clips
                ? 'Only these cameras\u2019 RecentClips are copied to the archive. '
                : 'RecentClips archiving is off, so this only applies to the option below. '}
              Saved and Sentry events, including clips saved with the steering-wheel button,
              always keep every camera. Lists the cameras found in this car&apos;s clips.
            </div>
            <div style={{ marginTop: 12 }}>
              <Field
                row
                label="Also delete other cameras from the Pi's snapshots (experimental)"
              >
                <Toggle
                  on={draft.recent_prune_snapshots}
                  onClick={() => setDraft(d => ({ ...d, recent_prune_snapshots: !d.recent_prune_snapshots }))}
                />
              </Field>
              <div style={{ marginTop: 4, fontSize: 'var(--ts-xs)', color: 'var(--fg-2)', lineHeight: 1.5 }}>
                Keeps more driving history on the Pi before old snapshots are deleted. The other
                cameras&apos; RecentClips can no longer be played or archived. The front camera
                is kept while the telemetry map is on. Takes effect for new snapshots.
              </div>
            </div>
          </div>
        </div>

        {/* Backend forms */}
        {draft.system === 'cifs' && (
          <>
            <FieldGrid>
              <Field label="Server (host or IP)">
                <Input mono value={draft.cifs.server} onChange={e => setCIFS('server', e.target.value)} placeholder="nas.local" />
              </Field>
              <Field label="Share name">
                <Input mono value={draft.cifs.share} onChange={e => setCIFS('share', e.target.value)} placeholder="dashcam" />
              </Field>
              <Field label="Username">
                <Input mono value={draft.cifs.user} onChange={e => setCIFS('user', e.target.value)} />
              </Field>
              <Field label="Password">
                <InputGroup>
                  <Input
                    mono
                    type={reveal ? 'text' : 'password'}
                    value={draft.cifs.password}
                    onChange={e => setCIFS('password', e.target.value)}
                    placeholder={draft.cifs.password_set ? 'Saved. Leave blank to keep it' : ''}
                    style={{ paddingRight: 40 }}
                  />
                  <button
                    onClick={() => setReveal(r => !r)}
                    style={{
                      position: 'absolute',
                      right: 6, top: '50%',
                      transform: 'translateY(-50%)',
                      width: 32, height: 32,
                      display: 'grid', placeItems: 'center',
                      color: 'var(--fg-2)',
                      borderRadius: 'var(--radius-sm)',
                      background: 'none',
                      border: 'none',
                      cursor: 'pointer',
                    }}
                  >
                    <Icon name={reveal ? 'eye-off' : 'eye'} size={16} />
                  </button>
                </InputGroup>
              </Field>
              <Field label="Domain (optional)">
                <Input mono value={draft.cifs.domain ?? ''} onChange={e => setCIFS('domain', e.target.value)} placeholder="WORKGROUP" />
              </Field>
              <Field label="CIFS version (optional)">
                <Input mono value={draft.cifs.version ?? ''} onChange={e => setCIFS('version', e.target.value)} placeholder="3.0" />
              </Field>
              <Field label="Security mode (optional)">
                <Input mono value={draft.cifs.sec ?? ''} onChange={e => setCIFS('sec', e.target.value)} placeholder="ntlm" />
              </Field>
            </FieldGrid>
            <Callout type="info">
              Uses <code style={{ fontFamily: 'var(--font-mono)', background: 'var(--bg-1)', padding: '1px 5px', borderRadius: 3, color: 'var(--fg-0)', fontSize: 11 }}>mount.cifs</code> with <code style={{ fontFamily: 'var(--font-mono)', background: 'var(--bg-1)', padding: '1px 5px', borderRadius: 3, color: 'var(--fg-0)', fontSize: 11 }}>vers=3.0</code> by default. For very old NAS targets, enter <code style={{ fontFamily: 'var(--font-mono)', background: 'var(--bg-1)', padding: '1px 5px', borderRadius: 3, color: 'var(--fg-0)', fontSize: 11 }}>1.0</code> in the CIFS version field above.
            </Callout>
          </>
        )}

        {draft.system === 'nfs' && (
          <>
            <FieldGrid>
              <Field label="Server">
                <Input mono value={draft.nfs.server} onChange={e => setNFS('server', e.target.value)} placeholder="nas.local" />
              </Field>
              <Field label="Export path">
                <Input mono value={draft.nfs.path} onChange={e => setNFS('path', e.target.value)} placeholder="/volume1/dashcam" />
              </Field>
            </FieldGrid>
          </>
        )}

        {draft.system === 'rsync' && (
          <>
            <FieldGrid>
              <Field label="Username">
                <Input mono value={draft.rsync.user} onChange={e => setRsync('user', e.target.value)} />
              </Field>
              <Field label="Server / IP">
                <Input mono value={draft.rsync.server} onChange={e => setRsync('server', e.target.value)} />
              </Field>
              <Field label="Remote path">
                <Input mono value={draft.rsync.path} onChange={e => setRsync('path', e.target.value)} />
              </Field>
            </FieldGrid>
            <RsyncSSHKeySection />
          </>
        )}

        {draft.system === 'rclone' && (
          <>
            <FieldGrid>
              <Field label="Remote name">
                <Input mono value={draft.rclone.drive} onChange={e => setRclone('drive', e.target.value)} placeholder="gdrive-dashcam" />
              </Field>
              <Field label="Remote path">
                <Input mono value={draft.rclone.path} onChange={e => setRclone('path', e.target.value)} />
              </Field>
            </FieldGrid>
            <Callout type="info">
              <b>rclone.conf is managed on the Pi directly.</b> SSH in and run <code style={{ fontFamily: 'var(--font-mono)', background: 'var(--bg-1)', padding: '1px 5px', borderRadius: 3, color: 'var(--fg-0)', fontSize: 11 }}>rclone config</code> — the web UI doesn't expose provider OAuth flows yet.
            </Callout>
          </>
        )}

        {draft.system === 'none' && (
          <Callout type="danger">
            <b>No archive backend selected.</b> Clips stay on the SD card. When it fills up, Dashvault will delete the oldest clips to free space — nothing gets uploaded. This is fine for a throwaway setup; for long-term retention, pick a backend above.
          </Callout>
        )}
      </SectionBody>

      <SectionFooter>
        <Btn onClick={triggerSync} disabled={syncStatus === 'running'} loading={syncStatus === 'running'} icon={<Icon name="sync" size={14} />}>
          Trigger sync now
        </Btn>
        {syncMsg && (
          <ResultPill kind={syncStatus === 'error' ? 'err' : 'ok'}>
            {syncMsg}
          </ResultPill>
        )}
        <div style={{ flex: 1 }} />
        {dirty && <DirtyText />}
        <BtnPrimary onClick={save} disabled={!dirty || saveStatus === 'saving'} loading={saveStatus === 'saving'}>
          Save archive config
        </BtnPrimary>
        {saveMsg && (
          <ResultPill kind={saveStatus === 'error' ? 'err' : 'ok'}>
            {saveMsg}
          </ResultPill>
        )}
      </SectionFooter>
    </SectionCard>
  )
}

/* ─── rsync SSH key ──────────────────────────────────────────────────────── */

interface RsyncSSHKeyInfo {
  has_key: boolean
  public_key?: string
  key_type?: string
}

function RsyncSSHKeySection() {
  const [info, setInfo] = useState<RsyncSSHKeyInfo | null>(null)
  const [loadErr, setLoadErr] = useState('')
  const [genStatus, setGenStatus] = useState<'idle' | 'confirm' | 'generating'>('idle')
  const [genMsg, setGenMsg] = useState('')
  const [showImport, setShowImport] = useState(false)
  const [importVal, setImportVal] = useState('')
  const [importStatus, setImportStatus] = useState<'idle' | 'importing'>('idle')
  const [importMsg, setImportMsg] = useState('')
  const [copied, setCopied] = useState(false)

  useEffect(() => {
    fetch('/api/config/rsync/ssh-key')
      .then(r => r.json())
      .then(setInfo)
      .catch(() => setLoadErr('Failed to load SSH key info'))
  }, [])

  async function generateKey() {
    setGenStatus('generating')
    setGenMsg('')
    try {
      const res = await apiFetch('/api/config/rsync/keygen', { method: 'POST' })
      if (!res.ok) { const e = await res.json(); throw new Error(e.error || 'failed'); }
      const data: RsyncSSHKeyInfo = await res.json()
      setInfo(data)
      setGenMsg('New keypair generated.')
    } catch (e: unknown) {
      setGenMsg('Error: ' + (e instanceof Error ? e.message : String(e)))
    } finally {
      setGenStatus('idle')
    }
  }

  async function importKey() {
    if (!importVal.trim()) return
    setImportStatus('importing')
    setImportMsg('')
    try {
      const res = await apiFetch('/api/config/rsync/import-key', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ private_key_b64: importVal.trim() }),
      })
      if (!res.ok) { const e = await res.json(); throw new Error(e.error || 'failed'); }
      const data: RsyncSSHKeyInfo = await res.json()
      setInfo(data)
      setImportVal('')
      setShowImport(false)
      setImportMsg('Key imported successfully.')
    } catch (e: unknown) {
      setImportMsg('Error: ' + (e instanceof Error ? e.message : String(e)))
    } finally {
      setImportStatus('idle')
    }
  }

  async function copyKey() {
    if (!info?.public_key) return
    try {
      if (navigator.clipboard?.writeText) {
        await navigator.clipboard.writeText(info.public_key)
      } else {
        // Fallback for non-secure (plain HTTP) contexts.
        const ta = document.createElement('textarea')
        ta.value = info.public_key
        ta.style.position = 'fixed'
        ta.style.opacity = '0'
        document.body.appendChild(ta)
        ta.select()
        document.execCommand('copy')
        document.body.removeChild(ta)
      }
      setCopied(true)
      setTimeout(() => setCopied(false), 2000)
    } catch { /* leave the button in its uncopied state */ }
  }

  return (
    <div style={{
      marginTop: 'var(--sp-4)',
      paddingTop: 'var(--sp-4)',
      borderTop: '1px solid var(--border)',
      display: 'flex',
      flexDirection: 'column',
      gap: 'var(--sp-3)',
    }}>
      <div style={{ fontSize: 'var(--ts-sm)', fontWeight: 700, color: 'var(--fg-1)' }}>
        SSH Key
      </div>

      {loadErr && <span style={{ fontSize: 'var(--ts-sm)', color: 'var(--err)' }}>{loadErr}</span>}

      {info && (
        <>
          {info.has_key && info.public_key ? (
            <div>
              <div style={{
                fontSize: 'var(--ts-sm)',
                color: 'var(--fg-2)',
                fontWeight: 600,
                marginBottom: 'var(--sp-1)',
              }}>
                Public key ({info.key_type}) — add this to <code style={{ fontFamily: 'var(--font-mono)' }}>~/.ssh/authorized_keys</code> on your server
              </div>
              <div style={{ display: 'flex', gap: 'var(--sp-2)', alignItems: 'flex-start' }}>
                <textarea
                  readOnly
                  value={info.public_key}
                  rows={3}
                  style={{
                    flex: 1,
                    fontFamily: 'var(--font-mono)',
                    fontSize: 'var(--ts-xs)',
                    padding: 'var(--sp-2)',
                    background: 'var(--bg-3)',
                    border: '1px solid var(--border)',
                    borderRadius: 'var(--radius-sm)',
                    color: 'var(--fg-1)',
                    resize: 'vertical',
                    wordBreak: 'break-all',
                  }}
                />
                <Btn onClick={copyKey} variant="ghost">
                  {copied ? 'Copied' : 'Copy'}
                </Btn>
              </div>
            </div>
          ) : (
            <span style={{ fontSize: 'var(--ts-sm)', color: 'var(--fg-3)' }}>
              No SSH key configured yet — generate one or import an existing key.
            </span>
          )}

          <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--sp-3)', flexWrap: 'wrap' }}>
            {genStatus !== 'confirm' ? (
              <Btn
                onClick={() => info.has_key ? setGenStatus('confirm') : generateKey()}
                loading={genStatus === 'generating'}
                disabled={genStatus === 'generating'}
              >
                {info.has_key ? 'Regenerate keypair' : 'Generate keypair'}
              </Btn>
            ) : (
              <>
                <span style={{ fontSize: 'var(--ts-sm)', color: 'var(--warn)' }}>
                  This will replace the existing key — you'll need to update authorized_keys on your server.
                </span>
                <Btn onClick={generateKey} variant="danger">
                  Confirm
                </Btn>
                <Btn onClick={() => setGenStatus('idle')} variant="ghost">
                  Cancel
                </Btn>
              </>
            )}
            {genMsg && (
              <span style={{ fontSize: 'var(--ts-sm)', color: genMsg.startsWith('Error') ? 'var(--err)' : 'var(--ok)' }}>
                {genMsg}
              </span>
            )}
          </div>

          <div>
            <Btn onClick={() => setShowImport(v => !v)} variant="ghost">
              {showImport ? 'Cancel import' : 'Import existing private key'}
            </Btn>
            {showImport && (
              <div style={{ marginTop: 'var(--sp-3)', display: 'flex', flexDirection: 'column', gap: 'var(--sp-2)' }}>
                <div style={{
                  fontSize: 'var(--ts-sm)',
                  color: 'var(--fg-2)',
                  fontWeight: 600,
                  marginBottom: 'var(--sp-1)',
                }}>
                  Paste the private key encoded as base64 (standard PEM → <code style={{ fontFamily: 'var(--font-mono)' }}>base64 id_ed25519</code> or <code style={{ fontFamily: 'var(--font-mono)' }}>base64 id_rsa</code>)
                </div>
                <textarea
                  value={importVal}
                  onChange={e => setImportVal(e.target.value)}
                  rows={4}
                  placeholder="LS0tLS1CRUdJTiBPUEVOU1NIIFBSSVZBVEUgS0VZLS0t..."
                  style={{
                    fontFamily: 'var(--font-mono)',
                    fontSize: 'var(--ts-xs)',
                    padding: 'var(--sp-2)',
                    background: 'var(--bg-3)',
                    border: '1px solid var(--border)',
                    borderRadius: 'var(--radius-sm)',
                    color: 'var(--fg-1)',
                    resize: 'vertical',
                    width: '100%',
                    boxSizing: 'border-box',
                  }}
                />
                <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--sp-3)' }}>
                  <Btn
                    onClick={importKey}
                    disabled={!importVal.trim() || importStatus === 'importing'}
                    loading={importStatus === 'importing'}
                  >
                    Import
                  </Btn>
                  {importMsg && (
                    <span style={{ fontSize: 'var(--ts-sm)', color: importMsg.startsWith('Error') ? 'var(--err)' : 'var(--ok)' }}>
                      {importMsg}
                    </span>
                  )}
                </div>
              </div>
            )}
          </div>
        </>
      )}
    </div>
  )
}


/* ─── telegram ───────────────────────────────────────────────────────────── */

interface TelegramConfig {
  has_token: boolean
  masked_token: string
  chat_id: string
  silent_notify: boolean
  service_enabled: boolean
  service_running: boolean
}

function TelegramSection() {
  const [cfg, setCfg] = useState<TelegramConfig | null>(null)
  const [loadErr, setLoadErr] = useState('')
  const [botToken, setBotToken] = useState('')
  const [chatID, setChatID] = useState('')
  const [silent, setSilent] = useState(false)
  const [enabled, setEnabled] = useState(false)
  const [reveal, setReveal] = useState(false)

  const [saveStatus, setSaveStatus] = useState<'idle' | 'saving' | 'done' | 'error'>('idle')
  const [saveMsg, setSaveMsg] = useState('')
  const [testStatus, setTestStatus] = useState<'idle' | 'running' | 'done' | 'error'>('idle')
  const [testMsg, setTestMsg] = useState('')

  useEffect(() => {
    apiFetch('/api/config/telegram')
      .then(r => r.json())
      .then((data: TelegramConfig) => {
        setCfg(data)
        setChatID(data.chat_id)
        setSilent(data.silent_notify)
        setEnabled(data.service_enabled)
      })
      .catch(() => setLoadErr('Could not load Telegram config.'))
  }, [])

  const dirty = cfg !== null && (
    botToken !== '' ||
    chatID !== cfg.chat_id ||
    silent !== cfg.silent_notify ||
    enabled !== cfg.service_enabled
  )

  async function save() {
    setSaveStatus('saving'); setSaveMsg('')
    try {
      const res = await apiFetch('/api/config/telegram', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          bot_token: botToken,
          keep_token: botToken === '',
          chat_id: chatID,
          silent_notify: silent,
          enabled,
        }),
      })
      if (!res.ok) { const t = await res.json().catch(() => ({})); throw new Error((t as {error?:string}).error ?? 'Save failed.') }
      const saved: TelegramConfig = await res.json()
      setCfg(saved)
      setChatID(saved.chat_id)
      setSilent(saved.silent_notify)
      setEnabled(saved.service_enabled)
      setBotToken('')
      setTestStatus('idle'); setTestMsg('')
      setSaveStatus('done'); setSaveMsg('Saved.')
    } catch (e: unknown) {
      setSaveStatus('error'); setSaveMsg(e instanceof Error ? e.message : 'Save failed.')
    }
  }

  async function sendTest() {
    setTestStatus('running'); setTestMsg('')
    try {
      const res = await apiFetch('/api/config/telegram/test', { method: 'POST' })
      if (!res.ok) { const t = await res.json().catch(() => ({})); throw new Error((t as {error?:string}).error ?? 'Test failed.') }
      setTestStatus('done')
      setTestMsg(cfg?.service_enabled
        ? 'Test message sent — check your Telegram.'
        : 'Test sent, but the bot is off. Turn on Enable bot service and save to get notifications and commands.')
    } catch (e: unknown) {
      setTestStatus('error'); setTestMsg(e instanceof Error ? e.message : 'Could not reach Telegram API — check Pi internet access.')
    }
  }

  const canTest = cfg?.has_token && cfg?.chat_id !== ''

  const statusLed: 'ok' | 'warn' | 'idle' = cfg === null ? 'idle'
    : cfg.service_running ? 'ok'
    : cfg.service_enabled ? 'warn'
    : 'idle'
  const statusText = cfg === null ? 'not configured'
    : cfg.service_running ? 'connected'
    : cfg.service_enabled ? 'stopped'
    : 'disabled'

  return (
    <SectionCard
      id="notifications"
      icon={<Icon name="send" size={16} />}
      title="Notifications — Telegram"
      sub="Messages this chat when archiving starts and finishes, with SD card wear and configured temperature alerts. You can also send it commands."
      status={{ led: statusLed, text: statusText }}
      dirty={dirty}
    >
      <SectionBody>
        {loadErr && <Callout type="danger">{loadErr}</Callout>}

        <Field label="Bot token">
          <div style={{
            fontSize: 'var(--ts-xs)',
            color: 'var(--fg-2)',
            lineHeight: 1.5,
            marginBottom: 4,
          }}>
            Create a bot at <code style={{ fontFamily: 'var(--font-mono)', background: 'var(--bg-2)', padding: '1px 5px', borderRadius: 3 }}>t.me/BotFather</code> → <code style={{ fontFamily: 'var(--font-mono)', background: 'var(--bg-2)', padding: '1px 5px', borderRadius: 3 }}>/newbot</code> → copy the token it returns.
          </div>
          <InputGroup>
            <Input
              mono
              type={reveal ? 'text' : 'password'}
              value={botToken}
              placeholder={cfg?.has_token ? 'Leave blank to keep existing token' : 'Paste token from @BotFather'}
              onChange={e => setBotToken(e.target.value)}
              style={{ paddingRight: 80 }}
            />
            <div style={{
              position: 'absolute',
              right: 6, top: '50%',
              transform: 'translateY(-50%)',
              display: 'flex',
              gap: 2,
            }}>
              <button
                onClick={() => setReveal(r => !r)}
                style={{
                  width: 32, height: 32,
                  display: 'grid', placeItems: 'center',
                  color: 'var(--fg-2)',
                  borderRadius: 'var(--radius-sm)',
                  background: 'none',
                  border: 'none',
                  cursor: 'pointer',
                }}
              >
                <Icon name={reveal ? 'eye-off' : 'eye'} size={16} />
              </button>
            </div>
          </InputGroup>
        </Field>

        <Field label="Chat ID">
          <Input
            mono
            value={chatID}
            placeholder="Your Telegram user or group ID"
            onChange={e => setChatID(e.target.value)}
          />
        </Field>

        <div style={{
          display: 'flex',
          flexDirection: 'column',
          gap: 'var(--sp-3)',
        }}>
          <Field label="Silent notifications" row>
            <Toggle on={silent} onClick={() => setSilent(v => !v)} />
          </Field>
          <span style={{ fontSize: 'var(--ts-xs)', color: 'var(--fg-2)', lineHeight: 1.5 }}>
            Deliver without a phone buzz. Good for archive-complete and low-priority pings.
          </span>

          <Field label="Enable bot service" row>
            <Toggle on={enabled} onClick={() => setEnabled(v => !v)} />
          </Field>
        </div>
      </SectionBody>

      <SectionFooter>
        <Btn onClick={sendTest} disabled={!canTest || testStatus === 'running'} loading={testStatus === 'running'} icon={<Icon name="send" size={14} />}>
          Send test message
        </Btn>
        {testMsg && (
          <ResultPill kind={testStatus === 'error' ? 'err' : cfg?.service_enabled ? 'ok' : 'info'}>
            {testMsg}
          </ResultPill>
        )}
        <div style={{ flex: 1 }} />
        {dirty && <DirtyText />}
        <BtnPrimary onClick={save} disabled={!dirty || saveStatus === 'saving'} loading={saveStatus === 'saving'}>
          Save
        </BtnPrimary>
        {saveMsg && (
          <ResultPill kind={saveStatus === 'error' ? 'err' : 'ok'}>
            {saveMsg}
          </ResultPill>
        )}
      </SectionFooter>
    </SectionCard>
  )
}

/* ─── snapshot interval ──────────────────────────────────────────────────── */

function SnapshotIntervalSection() {
  const [interval, setInterval] = useState<number | null>(null)
  const [draft, setDraft] = useState('')
  const [status, setStatus] = useState<'idle' | 'loading' | 'saving' | 'done' | 'error'>('loading')
  const [msg, setMsg] = useState('')

  useEffect(() => {
    apiFetch('/api/config/snapshot-interval')
      .then(r => r.json())
      .then((data: { interval: number }) => {
        setInterval(data.interval)
        setDraft(String(data.interval))
        setStatus('idle')
      })
      .catch(() => {
        setMsg('Failed to load current value.')
        setStatus('error')
      })
  }, [])

  async function save() {
    const v = parseInt(draft, 10)
    if (isNaN(v) || v < 60) {
      setMsg('Interval must be at least 60 seconds.')
      setStatus('error')
      return
    }
    setStatus('saving')
    setMsg('')
    try {
      const res = await apiFetch('/api/config/snapshot-interval', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ interval: v }),
      })
      if (!res.ok) {
        const d = await res.json().catch(() => ({})) as { error?: string }
        setStatus('error')
        setMsg(d.error ?? 'Save failed.')
        return
      }
      const data: { interval: number } = await res.json()
      if (typeof data.interval !== 'number') throw new Error('bad response')
      setInterval(data.interval)
      setDraft(String(data.interval))
      setStatus('done')
      setMsg('Saved.')
      setTimeout(() => setStatus('idle'), 2500)
    } catch {
      setStatus('error')
      setMsg('Save failed.')
    }
  }

  const dirty = interval !== null && parseInt(draft, 10) !== interval

  return (
    <SectionCard
      id="advanced"
      icon={<Icon name="clock" size={16} />}
      title="Snapshot Interval"
      sub="How often (in seconds) a snapshot is taken while the archive is reachable. Default: 480 s (8 min)."
      dirty={dirty}
    >
      <SectionBody>
        <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--sp-3)', flexWrap: 'wrap' }}>
          <Input
            mono
            type="number"
            min={60}
            value={draft}
            disabled={status === 'loading' || status === 'saving'}
            onChange={e => { setDraft(e.target.value); setStatus('idle'); setMsg('') }}
            style={{ width: 100 }}
          />
          <span style={{ fontSize: 'var(--ts-sm)', color: 'var(--fg-2)' }}>seconds</span>
          <BtnPrimary
            onClick={save}
            disabled={!dirty || status === 'saving' || status === 'loading'}
            loading={status === 'saving'}
          >
            Save
          </BtnPrimary>
          {msg && (
            <ResultPill kind={status === 'error' ? 'err' : 'ok'}>
              {msg}
            </ResultPill>
          )}
        </div>
      </SectionBody>
    </SectionCard>
  )
}

/* ─── OTA updates ────────────────────────────────────────────────────────── */

interface OtaStatus {
  ota_enabled: boolean
  release_url_configured: boolean
  current_version: string
  latest_version?: string
  update_available: boolean
  rollback_available: boolean
  last_checked?: string
}

type OtaPhase =
  | 'idle'
  | 'checking'
  | 'applying'
  | 'rolling_back'
  | 'restarting'

function OtaSection() {
  const [status, setStatus] = useState<OtaStatus | null>(null)
  const [phase, setPhase] = useState<OtaPhase>('idle')
  const [msg, setMsg] = useState('')
  const [showLog, setShowLog] = useState(false)
  const [log, setLog] = useState('')
  const [countdown, setCountdown] = useState(0)
  const countdownRef = useRef<ReturnType<typeof setInterval> | null>(null)
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  useEffect(() => {
    apiFetch('/api/update/status')
      .then(r => r.json())
      .then((s: OtaStatus) => setStatus(s))
      .catch(() => setMsg('Failed to load OTA status.'))
  }, [])

  useEffect(() => {
    if (!showLog) return
    apiFetch('/api/update/log').then(r => r.text()).then(setLog).catch(() => {})
  }, [showLog])

  function startRestartCountdown(seconds: number) {
    setCountdown(seconds)
    countdownRef.current = setInterval(() => {
      setCountdown(prev => {
        if (prev <= 1) {
          clearInterval(countdownRef.current!)
          window.location.reload()
          return 0
        }
        return prev - 1
      })
    }, 1000)
  }

  useEffect(() => () => {
    if (countdownRef.current) clearInterval(countdownRef.current)
    if (timerRef.current) clearTimeout(timerRef.current)
  }, [])

  async function checkForUpdates() {
    setPhase('checking')
    setMsg('')
    try {
      const res = await apiFetch('/api/update/check', { method: 'POST' })
      if (!res.ok) {
        const body = await res.json() as { error?: string }
        setMsg(body.error ?? 'Check failed.')
        setPhase('idle')
        return
      }
      const s: OtaStatus = await res.json()
      setStatus(s)
      setMsg(s.update_available
        ? `Update available: ${s.latest_version}`
        : 'Already up to date.')
    } catch {
      setMsg('Check failed — network error.')
    } finally {
      setPhase('idle')
    }
  }

  async function applyUpdate() {
    setPhase('applying')
    setMsg('')
    setShowLog(true)
    try {
      const res = await apiFetch('/api/update/apply', { method: 'POST' })
      if (!res.ok) {
        const body = await res.json() as { error?: string }
        setMsg(body.error ?? 'Apply failed.')
        setPhase('idle')
        return
      }
      setPhase('restarting')
      startRestartCountdown(25)
    } catch {
      setMsg('Apply failed — network error.')
      setPhase('idle')
    }
  }

  async function rollback() {
    setPhase('rolling_back')
    setMsg('')
    setShowLog(true)
    try {
      const res = await apiFetch('/api/update/rollback', { method: 'POST' })
      if (!res.ok) {
        const body = await res.json() as { error?: string }
        setMsg(body.error ?? 'Rollback failed.')
        setPhase('idle')
        return
      }
      setPhase('restarting')
      startRestartCountdown(25)
    } catch {
      setMsg('Rollback failed — network error.')
      setPhase('idle')
    }
  }

  if (status && !status.ota_enabled) {
    return (
      <SectionCard
        id="advanced-ota"
        icon={<Icon name="refresh-cw" size={16} />}
        title="OTA Updates"
      >
        <SectionBody>
          <p style={{ margin: 0, fontSize: 'var(--ts-sm)', color: 'var(--fg-2)' }}>
            OTA updates are <strong>disabled</strong>. Set{' '}
            <code style={{ fontFamily: 'var(--font-mono)', background: 'var(--bg-3)', padding: '1px 4px', borderRadius: 3 }}>
              OTA_ENABLED=true
            </code>{' '}
            and{' '}
            <code style={{ fontFamily: 'var(--font-mono)', background: 'var(--bg-3)', padding: '1px 4px', borderRadius: 3 }}>
              OTA_RELEASE_URL=&lt;releases API URL&gt;
            </code>{' '}
            in your config to enable.
          </p>
          <p style={{ margin: '8px 0 0', fontSize: 'var(--ts-sm)', color: 'var(--fg-3)' }}>
            Current version: <code style={{ fontFamily: 'var(--font-mono)' }}>{status.current_version}</code>
          </p>
        </SectionBody>
      </SectionCard>
    )
  }

  if (phase === 'restarting') {
    return (
      <SectionCard
        id="advanced-ota"
        icon={<Icon name="refresh-cw" size={16} />}
        title="OTA Updates"
      >
        <SectionBody>
          <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--sp-3)' }}>
            <Spinner />
            <span style={{ fontSize: 'var(--ts-sm)', color: 'var(--fg-1)' }}>
              {countdown > 0
                ? `Service restarting — page will reload in ${countdown}s…`
                : 'Restarting… refresh when the Pi comes back online.'}
            </span>
          </div>
        </SectionBody>
      </SectionCard>
    )
  }

  const isIdle = phase === 'idle'
  const isChecking = phase === 'checking'
  const isApplying = phase === 'applying'
  const canCheck = !!status?.ota_enabled && !!status?.release_url_configured && isIdle
  const canApply = canCheck && !!status?.update_available
  const canRollback = isIdle && !!status?.rollback_available

  return (
    <SectionCard
      id="advanced-ota"
      icon={<Icon name="refresh-cw" size={16} />}
      title="OTA Updates"
      sub="Check for and apply over-the-air releases."
    >
      <SectionBody>
        <div style={{ fontSize: 'var(--ts-sm)', color: 'var(--fg-2)', marginBottom: 'var(--sp-3)' }}>
          Current:{' '}
          <code style={{ fontFamily: 'var(--font-mono)', color: 'var(--fg-0)' }}>
            {status?.current_version ?? '…'}
          </code>
          {status?.latest_version && (
            <>
              {' '}→ Latest:{' '}
              <code style={{
                fontFamily: 'var(--font-mono)',
                color: status.update_available ? 'var(--ok)' : 'var(--fg-0)',
              }}>
                {status.latest_version}
              </code>
            </>
          )}
          {status?.last_checked && (
            <span style={{ marginLeft: 8, color: 'var(--fg-3)' }}>
              (checked {new Date(status.last_checked).toLocaleTimeString()})
            </span>
          )}
        </div>

        {status?.ota_enabled && !status?.release_url_configured && (
          <Callout type="warn">
            <code style={{ fontFamily: 'var(--font-mono)' }}>OTA_RELEASE_URL</code> is not configured.
          </Callout>
        )}

        <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--sp-3)', flexWrap: 'wrap' }}>
          <Btn onClick={checkForUpdates} disabled={!canCheck} loading={isChecking}>
            Check for updates
          </Btn>

          {canApply && (
            <Btn onClick={applyUpdate} loading={isApplying} variant="danger">
              Update to {status!.latest_version}
            </Btn>
          )}

          {canRollback && (
            <Btn onClick={rollback} variant="ghost">
              Rollback
            </Btn>
          )}

          {msg && (
            <ResultPill kind={msg.startsWith('Already') || msg.startsWith('Update') ? 'ok' : 'err'}>
              {msg}
            </ResultPill>
          )}
        </div>

        <div>
          <button
            onClick={() => setShowLog(v => !v)}
            style={{
              background: 'none',
              border: 'none',
              color: 'var(--fg-2)',
              fontSize: 'var(--ts-sm)',
              cursor: 'pointer',
              padding: 0,
            }}
          >
            {showLog ? '▲ Hide update log' : '▼ Show update log'}
          </button>
          {showLog && (
            <pre style={{
              marginTop: 'var(--sp-2)',
              padding: 'var(--sp-3)',
              background: 'var(--bg-3)',
              borderRadius: 'var(--radius-sm)',
              fontSize: 12,
              fontFamily: 'var(--font-mono)',
              color: 'var(--fg-1)',
              whiteSpace: 'pre-wrap',
              wordBreak: 'break-all',
              maxHeight: 240,
              overflowY: 'auto',
            }}>
              {log || '(no log entries yet)'}
            </pre>
          )}
        </div>
      </SectionBody>
    </SectionCard>
  )
}

/* ─── USB drives ─────────────────────────────────────────────────────────── */

function USBDrivesSection() {
  const [confirm, setConfirm] = useState(false)
  const [status, setStatus] = useState<'idle' | 'running' | 'done' | 'error'>('idle')
  const [msg, setMsg] = useState('')
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  useEffect(() => {
    if (confirm) {
      timerRef.current = setTimeout(() => setConfirm(false), 8000)
    }
    return () => { if (timerRef.current) clearTimeout(timerRef.current) }
  }, [confirm])

  async function toggle() {
    setConfirm(false)
    setStatus('running')
    setMsg('')
    try {
      const res = await apiFetch('/cgi-bin/toggledrives.sh', { method: 'POST' })
      const text = await res.text()
      setStatus('done')
      setMsg(text.trim() || 'Done.')
      setTimeout(() => setStatus('idle'), 3000)
    } catch {
      setStatus('error')
      setMsg('Request failed.')
    }
  }

  return (
    <SectionCard
      id="advanced-usb"
      icon={<Icon name="disc" size={16} />}
      title="USB Drives"
      sub="Enable or disable the USB mass-storage gadget exposed to the car. The drive must be unmounted from Tesla before toggling."
    >
      <SectionBody>
        <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--sp-3)', flexWrap: 'wrap' }}>
          {!confirm ? (
            <Btn onClick={() => setConfirm(true)} disabled={status === 'running'}>
              Toggle USB drives
            </Btn>
          ) : (
            <>
              <span style={{ fontSize: 'var(--ts-sm)', color: 'var(--warn)' }}>
                Toggle drives now?
              </span>
              <Btn onClick={toggle} loading={status === 'running'} variant="danger">
                Confirm
              </Btn>
              <Btn onClick={() => setConfirm(false)} variant="ghost">
                Cancel
              </Btn>
            </>
          )}
          {msg && !confirm && (
            <ResultPill kind={status === 'error' ? 'err' : 'ok'}>
              {msg}
            </ResultPill>
          )}
        </div>
      </SectionBody>
    </SectionCard>
  )
}

/* ─── wifi networks ──────────────────────────────────────────────────────── */

interface WifiNetwork {
  ssid: string
  uuid: string
  active: boolean
}

interface WifiNetworksResponse {
  networks: WifiNetwork[]
  connected_via_wifi: boolean
}

interface ScannedNetwork {
  ssid: string
  signal: number
  security: string
  saved: boolean
}

function signalBars(signal: number): string {
  if (signal >= 75) return '▂▄▆█'
  if (signal >= 50) return '▂▄▆░'
  if (signal >= 25) return '▂▄░░'
  return '▂░░░'
}

function WifiSection() {
  const [data, setData] = useState<WifiNetworksResponse | null>(null)
  const [loadErr, setLoadErr] = useState('')

  const [scanResults, setScanResults] = useState<ScannedNetwork[] | null>(null)
  const [scanning, setScanning] = useState(false)
  const [scanErr, setScanErr] = useState('')

  const [showAddForm, setShowAddForm] = useState(false)
  const [addSSID, setAddSSID] = useState('')
  const [addPassword, setAddPassword] = useState('')
  const [showPassword, setShowPassword] = useState(false)
  const [addStatus, setAddStatus] = useState<'idle' | 'saving' | 'done' | 'error'>('idle')
  const [addMsg, setAddMsg] = useState('')

  const [deletingUUID, setDeletingUUID] = useState<string | null>(null)
  const [deleteMsg, setDeleteMsg] = useState('')

  useEffect(() => { loadNetworks() }, [])

  async function loadNetworks() {
    setLoadErr('')
    try {
      const res = await apiFetch('/api/wifi/networks')
      const d: WifiNetworksResponse = await res.json()
      setData(d)
    } catch {
      setLoadErr('Failed to load saved networks.')
    }
  }

  async function scan() {
    setScanning(true)
    setScanErr('')
    setScanResults(null)
    try {
      const res = await apiFetch('/api/wifi/scan')
      const d: ScannedNetwork[] = await res.json()
      setScanResults(d)
    } catch {
      setScanErr('Scan failed.')
    } finally {
      setScanning(false)
    }
  }

  function openAddForm(prefillSSID = '') {
    setAddSSID(prefillSSID)
    setAddPassword('')
    setShowPassword(false)
    setAddStatus('idle')
    setAddMsg('')
    setShowAddForm(true)
  }

  async function addNetwork() {
    if (!addSSID.trim()) {
      setAddMsg('SSID is required.')
      setAddStatus('error')
      return
    }
    setAddStatus('saving')
    setAddMsg('')
    try {
      const res = await apiFetch('/api/wifi/networks', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ ssid: addSSID.trim(), password: addPassword }),
      })
      if (!res.ok) {
        const d = await res.json().catch(() => ({ error: 'Request failed.' }))
        setAddMsg(d.error ?? 'Request failed.')
        setAddStatus('error')
        return
      }
      setAddStatus('done')
      setAddMsg(`"${addSSID.trim()}" saved.`)
      setShowAddForm(false)
      await loadNetworks()
      if (scanResults) {
        setScanResults(sr => sr?.map(n => ({ ...n, saved: n.ssid === addSSID.trim() ? true : n.saved })) ?? null)
      }
    } catch {
      setAddStatus('error')
      setAddMsg('Request failed.')
    }
  }

  async function deleteNetwork(network: WifiNetwork) {
    setDeletingUUID(network.uuid)
    setDeleteMsg('')
    try {
      const res = await apiFetch(`/api/wifi/networks/${encodeURIComponent(network.ssid)}`, { method: 'DELETE' })
      if (!res.ok) {
        const d = await res.json().catch(() => ({ error: 'Request failed.' }))
        setDeleteMsg(d.error ?? 'Request failed.')
        return
      }
      await loadNetworks()
    } catch {
      setDeleteMsg('Request failed.')
    } finally {
      setDeletingUUID(null)
    }
  }

  const showLockoutWarning = data?.connected_via_wifi && showAddForm

  return (
    <SectionCard
      id="network"
      icon={<Icon name="wifi" size={16} />}
      title="Wi-Fi Networks"
      sub="Manage saved networks. The Pi connects automatically when a saved network is in range."
    >
      <SectionBody>
        {loadErr && <Callout type="danger">{loadErr}</Callout>}

        {showLockoutWarning && (
          <Callout type="warn">
            You are connected over WiFi. A misconfigured network could disconnect you from this page.
          </Callout>
        )}

        {data && data.networks.length === 0 && (
          <p style={{ margin: 0, fontSize: 'var(--ts-sm)', color: 'var(--fg-2)' }}>No saved networks.</p>
        )}

        {data && data.networks.length > 0 && (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 4, marginBottom: 'var(--sp-3)' }}>
            {data.networks.map(n => (
              <div key={n.uuid} style={{
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'space-between',
                padding: '7px 10px',
                background: 'var(--bg-0)',
                border: '1px solid var(--border)',
                borderRadius: 'var(--radius-sm)',
              }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 'var(--ts-sm)' }}>
                  <Icon name="wifi" size={14} />
                  <span style={{ fontWeight: n.active ? 600 : 400 }}>{n.ssid}</span>
                  {n.active && (
                    <span style={{
                      fontSize: 11, padding: '1px 6px',
                      background: 'rgba(34,197,94,.15)', color: 'var(--ok)',
                      borderRadius: 99, border: '1px solid rgba(34,197,94,.3)',
                    }}>
                      connected
                    </span>
                  )}
                </div>
                <Btn
                  variant="ghost"
                  onClick={() => deleteNetwork(n)}
                  disabled={n.active || deletingUUID === n.uuid}
                  loading={deletingUUID === n.uuid}
                >
                  {n.active ? 'Locked' : 'Delete'}
                </Btn>
              </div>
            ))}
            {deleteMsg && (
              <p style={{ margin: '4px 0 0', fontSize: 'var(--ts-sm)', color: 'var(--err)' }}>{deleteMsg}</p>
            )}
          </div>
        )}

        <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--sp-3)', flexWrap: 'wrap', marginBottom: showAddForm || scanResults ? 'var(--sp-3)' : 0 }}>
          <Btn onClick={scan} disabled={scanning} loading={scanning}>
            Scan for networks
          </Btn>
          <Btn variant="ghost" onClick={() => openAddForm()}>
            + Add manually
          </Btn>
          {!showAddForm && addMsg && (
            <ResultPill kind={addStatus === 'error' ? 'err' : 'ok'}>
              {addMsg}
            </ResultPill>
          )}
        </div>
        {scanErr && (
          <p style={{ margin: 'var(--sp-2) 0 0', fontSize: 'var(--ts-sm)', color: 'var(--err)' }}>{scanErr}</p>
        )}

        {scanResults && scanResults.length === 0 && (
          <p style={{ margin: 'var(--sp-2) 0 0', fontSize: 'var(--ts-sm)', color: 'var(--fg-2)' }}>
            No networks found nearby.
          </p>
        )}

        {scanResults && scanResults.length > 0 && (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 4, marginTop: 'var(--sp-2)' }}>
            <p style={{ margin: '0 0 4px', fontSize: 'var(--ts-sm)', color: 'var(--fg-2)', fontWeight: 500 }}>
              Nearby networks
            </p>
            {scanResults.map(n => (
              <div key={n.ssid} style={{
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'space-between',
                padding: '7px 10px',
                background: 'var(--bg-0)',
                border: '1px solid var(--border)',
                borderRadius: 'var(--radius-sm)',
              }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: 10, fontSize: 'var(--ts-sm)', minWidth: 0 }}>
                  <span style={{ fontFamily: 'monospace', fontSize: 11, color: 'var(--ok)', flexShrink: 0 }}>
                    {signalBars(n.signal)}
                  </span>
                  <span style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{n.ssid}</span>
                  <span style={{ flexShrink: 0, fontSize: 11, color: 'var(--fg-3)' }}>{n.security}</span>
                  {n.saved && (
                    <span style={{
                      flexShrink: 0, fontSize: 11, padding: '1px 6px',
                      background: 'var(--bg-3)', color: 'var(--fg-2)',
                      borderRadius: 99, border: '1px solid var(--border)',
                    }}>
                      saved
                    </span>
                  )}
                </div>
                {!n.saved && (
                  <Btn variant="ghost" onClick={() => openAddForm(n.ssid)}>
                    Add
                  </Btn>
                )}
              </div>
            ))}
          </div>
        )}

        {showAddForm && (
          <div style={{
            marginTop: 'var(--sp-3)',
            padding: 'var(--sp-4)',
            background: 'var(--bg-0)',
            border: '1px solid var(--border)',
            borderRadius: 'var(--radius-sm)',
            display: 'flex',
            flexDirection: 'column',
            gap: 'var(--sp-3)',
          }}>
            <p style={{ margin: 0, fontSize: 'var(--ts-sm)', fontWeight: 600, color: 'var(--fg-0)' }}>
              Add network
            </p>

            <Field label="SSID">
              <Input value={addSSID} onChange={e => setAddSSID(e.target.value)} placeholder="Network name" />
            </Field>

            <Field label="Password (leave blank for open networks)">
              <div style={{ display: 'flex', gap: 8 }}>
                <Input
                  type={showPassword ? 'text' : 'password'}
                  value={addPassword}
                  onChange={e => setAddPassword(e.target.value)}
                  onKeyDown={e => e.key === 'Enter' && addNetwork()}
                  placeholder="Password"
                  style={{ flex: 1 }}
                />
                <Btn variant="ghost" onClick={() => setShowPassword(v => !v)}>
                  {showPassword ? 'Hide' : 'Show'}
                </Btn>
              </div>
            </Field>

            <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--sp-3)' }}>
              <Btn
                onClick={addNetwork}
                disabled={addStatus === 'saving'}
                loading={addStatus === 'saving'}
              >
                Save
              </Btn>
              <Btn
                variant="ghost"
                onClick={() => setShowAddForm(false)}
                disabled={addStatus === 'saving'}
              >
                Cancel
              </Btn>
              {addMsg && (
                <ResultPill kind={addStatus === 'error' ? 'err' : 'ok'}>
                  {addMsg}
                </ResultPill>
              )}
            </div>
          </div>
        )}
      </SectionBody>
    </SectionCard>
  )
}

/* ─── SD card benchmark ──────────────────────────────────────────────────── */

interface BenchmarkResult {
  timestamp: string
  write_mbps: number | null
  rating: 'good' | 'marginal' | 'slow' | 'unknown'
  timed_out: boolean
  size_mb: number
  elapsed_ms: number
  error?: string
}

interface BenchmarkStatusResp {
  running: boolean
  results: BenchmarkResult[]
  card_info?: {
    available: boolean
    name?: string
    manufacturer?: string
    capacity_gb?: number
    date?: string
    serial?: string
  }
  write_stats?: {
    total_bytes_written: number
    tracking_since: string
    last_updated: string
  }
  latest_rating?: string
  write_warning?: boolean
  threshold_tb?: number
  default_threshold_tb?: number
}

function BenchmarkSection() {
  const { status: sysStatus } = useStatus()
  const archiveIsRunning = sysStatus?.archive?.state === 'archiving'

  const [bStatus, setBStatus] = useState<BenchmarkStatusResp | null>(null)
  const [running, setRunning] = useState(false)
  const [log, setLog] = useState<string[]>([])
  const [latestResult, setLatestResult] = useState<BenchmarkResult | null>(null)
  const logEndRef = useRef<HTMLDivElement>(null)

  const [thresholdDraft, setThresholdDraft] = useState('')
  const [thresholdStatus, setThresholdStatus] = useState<'idle' | 'saving' | 'done' | 'error'>('idle')
  const [thresholdMsg, setThresholdMsg] = useState('')

  useEffect(() => { loadStatus() }, [])
  useEffect(() => { logEndRef.current?.scrollIntoView({ behavior: 'smooth' }) }, [log])

  async function loadStatus() {
    try {
      const res = await fetch('/api/benchmark/status')
      const data: BenchmarkStatusResp = await res.json()
      setBStatus(data)
      if (data.threshold_tb != null) {
        setThresholdDraft(String(data.threshold_tb))
      }
    } catch { /* ignore */ }
  }

  async function saveThreshold() {
    const v = parseFloat(thresholdDraft)
    if (isNaN(v) || v <= 0) {
      setThresholdMsg('Must be a positive number.')
      setThresholdStatus('error')
      return
    }
    setThresholdStatus('saving')
    setThresholdMsg('')
    try {
      const res = await apiFetch('/api/benchmark/threshold', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ threshold_tb: v }),
      })
      if (!res.ok) {
        const d = await res.json().catch(() => ({})) as { error?: string }
        setThresholdMsg(d.error ?? 'Save failed.')
        setThresholdStatus('error')
        return
      }
      setThresholdStatus('done')
      setThresholdMsg('Saved.')
      setTimeout(() => setThresholdStatus('idle'), 2500)
      await loadStatus()
    } catch {
      setThresholdStatus('error')
      setThresholdMsg('Save failed.')
    }
  }

  async function runBenchmark() {
    setRunning(true)
    setLog(['Starting benchmark…'])
    setLatestResult(null)

    try {
      const res = await apiFetch('/api/benchmark/run', { method: 'POST' })
      if (!res.ok || !res.body) throw new Error(res.statusText)

      const reader = res.body.getReader()
      const decoder = new TextDecoder()
      let buf = ''

      while (true) {
        const { done, value } = await reader.read()
        if (done) break
        buf += decoder.decode(value, { stream: true })
        const parts = buf.split('\n\n')
        buf = parts.pop() ?? ''
        for (const part of parts) {
          const line = part.replace(/^data: /, '').trim()
          if (!line) continue
          try {
            const parsed = JSON.parse(line)
            if (typeof parsed === 'string') {
              setLog(l => [...l, parsed])
            } else if (parsed.error) {
              setLog(l => [...l, `Error: ${parsed.error}`])
            } else if (parsed.result) {
              setLatestResult(parsed.result)
            } else if (parsed.done) {
              loadStatus()
            }
          } catch { setLog(l => [...l, line]) }
        }
      }
    } catch (e: unknown) {
      setLog(l => [...l, `Error: ${e instanceof Error ? e.message : String(e)}`])
    } finally {
      setRunning(false)
    }
  }

  const results = bStatus?.results ?? []
  const cardInfo = bStatus?.card_info
  const writeStats = bStatus?.write_stats
  const totalWrittenGB = writeStats ? writeStats.total_bytes_written / 1e9 : 0
  const thresholdTB = bStatus?.threshold_tb ?? null
  const defaultThresholdTB = bStatus?.default_threshold_tb ?? null
  const thresholdDirty = thresholdTB != null && parseFloat(thresholdDraft) !== thresholdTB

  return (
    <SectionCard
      id="advanced-sd"
      icon={<Icon name="activity" size={16} />}
      title="SD Card"
      sub="Benchmark write speed and track card health. Tesla records at ~20 MB/s sustained."
    >
      <SectionBody>
        {cardInfo?.available && (
          <div style={{
            background: 'var(--bg-0)',
            border: '1px solid var(--border)',
            borderRadius: 'var(--radius-sm)',
            padding: 'var(--sp-3) var(--sp-4)',
            marginBottom: 'var(--sp-4)',
            display: 'flex',
            flexWrap: 'wrap',
            gap: 'var(--sp-4)',
          }}>
            <InfoPair label="Card" value={[cardInfo.manufacturer, cardInfo.name].filter(Boolean).join(' ') || '—'} />
            {cardInfo.capacity_gb != null && (
              <InfoPair label="Capacity" value={`${cardInfo.capacity_gb.toFixed(0)} GB`} />
            )}
            {cardInfo.date && <InfoPair label="Manufactured" value={cardInfo.date} />}
            {totalWrittenGB > 0 && (
              <InfoPair
                label="Written (tracked)"
                value={totalWrittenGB >= 1000
                  ? `${(totalWrittenGB / 1000).toFixed(2)} TB`
                  : `${totalWrittenGB.toFixed(1)} GB`}
              />
            )}
            {writeStats?.tracking_since && (
              <InfoPair label="Tracking since" value={new Date(writeStats.tracking_since).toLocaleDateString()} />
            )}
          </div>
        )}

        <div style={{
          background: bStatus?.write_warning ? 'rgba(180,83,9,.1)' : 'var(--bg-0)',
          border: `1px solid ${bStatus?.write_warning ? '#b45309' : 'var(--border)'}`,
          borderRadius: 'var(--radius-sm)',
          padding: 'var(--sp-3) var(--sp-4)',
          marginBottom: 'var(--sp-4)',
        }}>
          <div style={{ fontSize: 'var(--ts-sm)', fontWeight: 600, color: 'var(--fg-0)', marginBottom: 'var(--sp-2)' }}>
            Write Endurance Warning
            {bStatus?.write_warning && (
              <span style={{ marginLeft: 8, color: '#fdba74' }}>Threshold reached</span>
            )}
          </div>
          <p style={{ margin: '0 0 var(--sp-3)', fontSize: 'var(--ts-sm)', color: 'var(--fg-2)', lineHeight: 1.5 }}>
            SD cards wear out as data is written. Set a threshold to receive a warning
            banner and Telegram notification when it is reached.{' '}
            <strong style={{ color: 'var(--fg-1)' }}>
              Standard cards last ~40–80 TB; High Endurance cards (Samsung PRO Endurance,
              SanDisk Max Endurance) last 1000+ TB.
            </strong>{' '}
            {defaultThresholdTB != null && cardInfo?.capacity_gb != null && (
              <>
                Default for your {cardInfo.capacity_gb.toFixed(0)} GB card:{' '}
                <strong style={{ color: 'var(--fg-1)' }}>{defaultThresholdTB.toFixed(1)} TB</strong>
                {' '}(500 × capacity).
              </>
            )}
          </p>
          <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--sp-3)', flexWrap: 'wrap' }}>
            <Input
              mono
              type="number"
              min={0.1}
              step={1}
              value={thresholdDraft}
              onChange={e => { setThresholdDraft(e.target.value); setThresholdStatus('idle'); setThresholdMsg('') }}
              disabled={thresholdStatus === 'saving'}
              style={{ width: 90 }}
            />
            <span style={{ fontSize: 'var(--ts-sm)', color: 'var(--fg-2)' }}>TB</span>
            <BtnPrimary
              onClick={saveThreshold}
              disabled={!thresholdDirty || thresholdStatus === 'saving'}
              loading={thresholdStatus === 'saving'}
            >
              Save
            </BtnPrimary>
            {defaultThresholdTB != null && (
              <Btn
                variant="ghost"
                onClick={() => { setThresholdDraft(String(defaultThresholdTB)); setThresholdStatus('idle'); setThresholdMsg('') }}
                disabled={thresholdStatus === 'saving'}
              >
                Reset to default
              </Btn>
            )}
            {thresholdMsg && (
              <ResultPill kind={thresholdStatus === 'error' ? 'err' : 'ok'}>
                {thresholdMsg}
              </ResultPill>
            )}
          </div>
        </div>

        <div style={{ marginBottom: 'var(--sp-4)' }}>
          <Btn onClick={runBenchmark} loading={running} disabled={running || archiveIsRunning}>
            {running ? 'Running…' : 'Run benchmark'}
          </Btn>
          <span style={{ marginLeft: 'var(--sp-3)', fontSize: 'var(--ts-sm)', color: archiveIsRunning ? 'var(--warn)' : 'var(--fg-2)' }}>
            {archiveIsRunning
              ? 'Archive is running — benchmark will be available once it finishes'
              : 'Writes 160 MB · 60s timeout'}
          </span>
        </div>

        {(running || log.length > 0) && (
          <div style={{
            background: 'var(--bg-0)',
            border: '1px solid var(--border)',
            borderRadius: 'var(--radius-sm)',
            padding: 'var(--sp-3)',
            fontFamily: 'var(--font-mono)',
            fontSize: 'var(--ts-sm)',
            color: 'var(--fg-1)',
            maxHeight: 160,
            overflowY: 'auto',
            marginBottom: 'var(--sp-4)',
          }}>
            {log.map((l, i) => <div key={i}>{l}</div>)}
            {latestResult && (
              <div style={{ color: ratingColor(latestResult.rating), fontWeight: 600, marginTop: 4 }}>
                {latestResult.write_mbps != null
                  ? `→ ${latestResult.write_mbps.toFixed(1)} MB/s — ${latestResult.rating}`
                  : `→ Timed out (< ${(160 / 60).toFixed(1)} MB/s) — too slow`}
              </div>
            )}
            <div ref={logEndRef} />
          </div>
        )}

        {results.length > 0 && (
          <div>
            <div style={{ fontSize: 'var(--ts-sm)', color: 'var(--fg-2)', marginBottom: 'var(--sp-2)', fontWeight: 600 }}>
              Last {results.length} result{results.length !== 1 ? 's' : ''}
            </div>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--sp-2)' }}>
              {results.map((r, i) => (
                <div key={i} style={{
                  display: 'flex',
                  alignItems: 'center',
                  gap: 'var(--sp-4)',
                  padding: 'var(--sp-2) var(--sp-3)',
                  background: 'var(--bg-0)',
                  border: '1px solid var(--border)',
                  borderRadius: 'var(--radius-sm)',
                  fontSize: 'var(--ts-sm)',
                }}>
                  <RatingBadge rating={r.rating} />
                  <span style={{ fontFamily: 'var(--font-mono)', flex: 1 }}>
                    {r.write_mbps != null
                      ? `${r.write_mbps.toFixed(1)} MB/s`
                      : r.timed_out ? 'Timed out' : 'Error'}
                  </span>
                  <span style={{ color: 'var(--fg-2)' }}>
                    {new Date(r.timestamp).toLocaleString()}
                  </span>
                </div>
              ))}
            </div>
          </div>
        )}

        {results.length === 0 && !running && (
          <p style={{ fontSize: 'var(--ts-sm)', color: 'var(--fg-2)', margin: 0 }}>
            No benchmark results yet. Run the test to check your card's write speed.
          </p>
        )}

        <div style={{
          marginTop: 'var(--sp-4)',
          display: 'flex',
          gap: 'var(--sp-3)',
          fontSize: 'var(--ts-xs)',
          color: 'var(--fg-2)',
        }}>
          <span style={{ color: 'var(--ok)' }}>● ≥ 25 MB/s good</span>
          <span style={{ color: 'var(--warn)' }}>● 15–25 MB/s marginal</span>
          <span style={{ color: 'var(--err)' }}>● &lt; 15 MB/s too slow</span>
        </div>
      </SectionBody>
    </SectionCard>
  )
}

function RatingBadge({ rating }: { rating: string }) {
  const color = ratingColor(rating)
  const label = rating === 'good' ? 'Good'
    : rating === 'marginal' ? 'Marginal'
    : rating === 'slow' ? 'Slow'
    : 'Unknown'
  return (
    <span style={{
      color,
      fontWeight: 600,
      minWidth: 80,
      fontSize: 'var(--ts-xs)',
      textTransform: 'uppercase',
      letterSpacing: '.04em',
    }}>
      {label}
    </span>
  )
}

function InfoPair({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <div style={{ fontSize: 'var(--ts-xs)', color: 'var(--fg-2)', marginBottom: 2 }}>{label}</div>
      <div style={{ fontSize: 'var(--ts-sm)', fontWeight: 500 }}>{value}</div>
    </div>
  )
}

function ratingColor(rating: string): string {
  if (rating === 'good') return 'var(--ok)'
  if (rating === 'marginal') return 'var(--warn)'
  if (rating === 'slow') return 'var(--err)'
  return 'var(--fg-2)'
}

/* ─── danger zone ────────────────────────────────────────────────────────── */

function DangerSection() {
  const [phase, setPhase] = useState<'idle' | 'confirm' | 'rebooting'>('idle')
  const [countdown, setCountdown] = useState(5)
  const intervalRef = useRef<ReturnType<typeof setInterval> | null>(null)
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  useEffect(() => {
    if (phase === 'confirm') {
      timerRef.current = setTimeout(() => {
        if (phase === 'confirm') setPhase('idle')
      }, 10_000)
    }
    return () => { if (timerRef.current) clearTimeout(timerRef.current) }
  }, [phase])

  async function startReboot() {
    setPhase('rebooting')
    setCountdown(5)
    await apiFetch('/cgi-bin/reboot.sh', { method: 'POST' })

    intervalRef.current = setInterval(() => {
      setCountdown(prev => {
        if (prev <= 1) {
          clearInterval(intervalRef.current!)
          return 0
        }
        return prev - 1
      })
    }, 1000)
  }

  useEffect(() => () => {
    if (intervalRef.current) clearInterval(intervalRef.current)
  }, [])

  return (
    <SectionCard
      id="danger"
      icon={<Icon name="zap" size={16} />}
      title="Danger Zone"
      sub="These actions affect system availability. Rebooting will interrupt any active archive operation."
    >
      <SectionBody>
        {phase === 'idle' && (
          <Btn onClick={() => setPhase('confirm')} variant="danger">
            Restart Pi
          </Btn>
        )}

        {phase === 'confirm' && (
          <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--sp-3)', flexWrap: 'wrap' }}>
            <span style={{ fontSize: 'var(--ts-sm)', color: 'var(--err)' }}>
              Restart now? This will disconnect the car.
            </span>
            <Btn onClick={startReboot} variant="danger">
              Yes, restart
            </Btn>
            <Btn onClick={() => setPhase('idle')} variant="ghost">
              Cancel
            </Btn>
          </div>
        )}

        {phase === 'rebooting' && (
          <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--sp-3)' }}>
            <Spinner />
            <span style={{ fontSize: 'var(--ts-sm)', color: 'var(--fg-1)' }}>
              {countdown > 0
                ? `Rebooting — page will reload in ~${countdown}s…`
                : 'Rebooting… refresh when the Pi comes back online.'}
            </span>
          </div>
        )}
      </SectionBody>
    </SectionCard>
  )
}


const RETENTION_PRESETS = [7, 30, 90, 365]

function formatBytes(bytes: number): string {
  if (bytes >= 1e9) return `${(bytes / 1e9).toFixed(1)} GB`
  if (bytes >= 1e6) return `${(bytes / 1e6).toFixed(1)} MB`
  return `${Math.max(1, Math.round(bytes / 1e3))} KB`
}

/** "176 tracks over 6 days, 0.7 MB" for the stored history, or null before it loads. */
function describeStore(days: TelemetryDays | null): string | null {
  if (!days) return null
  if (days.total_clips === 0) return 'No tracks stored'
  const count = days.days.length
  return `${days.total_clips} tracks over ${count} day${count === 1 ? '' : 's'}, ${formatBytes(days.total_bytes)}`
}

function TelemetrySection() {
  const [settings, setSettings] = useState<TelemetrySettings | null>(null)
  const [store, setStore] = useState<TelemetryDays | null>(null)
  const [customRetention, setCustomRetention] = useState(false)
  const [enabled, setEnabled] = useState(false)
  const [retention, setRetention] = useState(30)
  const [tileUrl, setTileUrl] = useState('https://tile.openstreetmap.org/{z}/{x}/{y}.png')
  const [styleUrl, setStyleUrl] = useState('')
  const [whileDriving, setWhileDriving] = useState(false)
  const [saving, setSaving] = useState(false)
  const [msg, setMsg] = useState('')

  const loadStore = () => {
    fetch('/api/telemetry/days')
      .then(response => (response.ok ? response.json() as Promise<TelemetryDays> : null))
      .then(value => setStore(value))
      .catch(() => setStore(null))
  }
  useEffect(loadStore, [])

  useEffect(() => {
    fetch('/api/telemetry/settings')
      .then(response => response.json() as Promise<TelemetrySettings>)
      .then(value => {
        setSettings(value)
        setEnabled(value.enabled)
        setRetention(value.retention_days)
        setTileUrl(value.tile_url)
        setStyleUrl(value.style_url)
        setWhileDriving(value.while_driving ?? false)
      })
      .catch(() => setMsg('Could not load GPS track settings.'))
  }, [])

  async function save() {
    setSaving(true)
    setMsg('')
    try {
      const response = await apiFetch('/api/telemetry/settings', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ enabled, retention_days: retention, tile_url: tileUrl, style_url: styleUrl, while_driving: whileDriving }),
      })
      if (!response.ok) throw new Error('Save failed')
      const value = await response.json() as TelemetrySettings
      setSettings(value)
      setEnabled(value.enabled)
      setRetention(value.retention_days)
      setTileUrl(value.tile_url)
      setStyleUrl(value.style_url)
      setWhileDriving(value.while_driving ?? false)
      loadStore()
      setMsg(value.enabled && value.while_driving
        ? 'Saved. New clips are indexed after every snapshot.'
        : 'Saved. New clips will be indexed when the car is home.')
    } catch {
      setMsg('Could not save GPS track settings.')
    } finally {
      setSaving(false)
    }
  }

  async function clearHistory() {
    const what = describeStore(store)
    if (!window.confirm(`Delete all retained GPS tracks${what ? ` (${what})` : ''}? Clips are not deleted.`)) return
    const response = await apiFetch('/api/telemetry/clear', { method: 'POST' })
    setMsg(response.ok ? 'GPS track history cleared.' : 'Could not clear track history.')
    loadStore()
  }

  const dirty = settings !== null && (settings.enabled !== enabled || settings.retention_days !== retention || settings.tile_url !== tileUrl || settings.style_url !== styleUrl || (settings.while_driving ?? false) !== whileDriving)
  return (
    <SectionCard
      id="advanced-telemetry"
      icon={<Icon name="map-pin" size={16} />}
      title="GPS Track History"
      sub="GPS tracks of SavedClips and RecentClips for the map. Tracks stay on the Pi; NAS playback is not included."
      status={{ led: enabled ? 'ok' : 'idle', text: enabled ? 'Enabled' : 'Disabled' }}
      dirty={dirty}
    >
      <SectionBody>
        <Field label="GPS tracks" hint="Read front-camera telemetry of new clips when the car is home and the archive is reachable, after the clips are copied.">
          <Toggle on={enabled} onClick={() => setEnabled(value => !value)} />
        </Field>
        <Field
          label="Index while driving (experimental)"
          hint="After every snapshot, read new clips' tracks in the background, so a long trip is kept even if its clips are deleted before the car is home. Tracks appear about one snapshot interval after recording. Uses some CPU while driving; each run stops starting new clips after half the snapshot interval."
        >
          <Toggle on={whileDriving && enabled} onClick={() => { if (enabled) setWhileDriving(value => !value) }} />
        </Field>
        <Field
          label="Track retention"
          hint={`Whole days older than this are deleted. Clips are not affected. A year of driving is about 160 MB, so the limit is about privacy, not space.${describeStore(store) ? ` Stored now: ${describeStore(store)}.` : ''}`}
        >
          <InputGroup>
            <select
              aria-label="Track retention"
              value={customRetention || !RETENTION_PRESETS.includes(retention) ? 'custom' : String(retention)}
              onChange={event => {
                if (event.target.value === 'custom') setCustomRetention(true)
                else {
                  setCustomRetention(false)
                  setRetention(Number(event.target.value))
                }
              }}
              style={{
                fontSize: 'var(--ts-md)', color: 'var(--fg-0)', background: 'var(--bg-0)',
                border: '1px solid var(--border)', borderRadius: 'var(--radius-sm)',
                padding: '10px var(--sp-3)',
              }}
            >
              {RETENTION_PRESETS.map(days => <option key={days} value={days}>{days === 365 ? '1 year' : `${days} days`}</option>)}
              <option value="custom">Custom…</option>
            </select>
            {(customRetention || !RETENTION_PRESETS.includes(retention)) && (
              <>
                <Input
                  type="number"
                  min={1}
                  max={3650}
                  aria-label="Retention in days"
                  value={retention}
                  onChange={event => setRetention(Number(event.target.value))}
                  style={{ width: 100 }}
                />
                <span style={{ color: 'var(--fg-2)', fontSize: 'var(--ts-sm)' }}>days</span>
              </>
            )}
          </InputGroup>
        </Field>
        <Field label="Map tile URL" hint="OpenStreetMap by default. Use a local HTTP(S) tile server for private/offline maps; include {z}, {x}, and {y}.">
          <Input mono value={tileUrl} onChange={event => setTileUrl(event.target.value)} style={{ width: '100%' }} />
        </Field>
        <Field label="Local vector map style (optional)" hint="A MapLibre style URL such as http://maps.local:8090/style.json. Overrides the raster tile URL when set.">
          <Input mono value={styleUrl} onChange={event => setStyleUrl(event.target.value)} placeholder="http://maps.local:8090/style.json" style={{ width: '100%' }} />
        </Field>
        <div style={{ display: 'flex', gap: 10, alignItems: 'center' }}>
          <BtnPrimary onClick={save} disabled={!dirty || saving} loading={saving}>Save</BtnPrimary>
          <Btn onClick={clearHistory} variant="danger" disabled={store?.total_clips === 0}>Clear track history</Btn>
          {msg && <span style={{ color: 'var(--fg-2)', fontSize: 'var(--ts-sm)' }}>{msg}</span>}
        </div>
      </SectionBody>
    </SectionCard>
  )
}

/* ─── power & thermal ────────────────────────────────────────────────────── */

function PowerThermalSection() {
  const [resp, setResp] = useState<PowerConfigResponse | null>(null)
  const [draft, setDraft] = useState<PowerConfig | null>(null)
  const [history, setHistory] = useState<ThermalHistoryResponse | null>(null)
  const [saving, setSaving] = useState(false)
  const [msg, setMsg] = useState<{ kind: 'ok' | 'err' | 'info'; text: string } | null>(null)

  const loadHistory = () => {
    fetch('/api/power/history')
      .then(r => r.json() as Promise<ThermalHistoryResponse>)
      .then(setHistory)
      .catch(() => {})
  }

  useEffect(() => {
    fetch('/api/power/config')
      .then(r => r.json() as Promise<PowerConfigResponse>)
      .then(v => { setResp(v); setDraft(v.config) })
      .catch(() => setMsg({ kind: 'err', text: 'Could not load power settings.' }))
    loadHistory()
    const t = window.setInterval(loadHistory, 60_000)
    return () => window.clearInterval(t)
  }, [])

  async function save() {
    if (!draft) return
    setSaving(true)
    setMsg(null)
    try {
      const r = await apiFetch('/api/power/config', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(draft),
      })
      if (!r.ok) throw new Error()
      const v = await r.json() as PowerConfigResponse
      setResp(v)
      setDraft(v.config)
      setMsg({ kind: 'ok', text: 'Saved — applied immediately.' })
      loadHistory()
    } catch {
      setMsg({ kind: 'err', text: 'Could not save power settings.' })
    } finally {
      setSaving(false)
    }
  }

  async function clearHistory() {
    if (!window.confirm('Delete the temperature history log?')) return
    const r = await apiFetch('/api/power/history/clear', { method: 'POST' })
    setMsg(r.ok ? { kind: 'info', text: 'Temperature history cleared.' } : { kind: 'err', text: 'Could not clear history.' })
    loadHistory()
  }

  const caps = resp?.capabilities
  const live = resp?.status
  const cfg = resp?.config
  const dirty = !!(cfg && draft && JSON.stringify(cfg) !== JSON.stringify(draft))
  const set = <K extends keyof PowerConfig>(k: K, v: PowerConfig[K]) => setDraft(d => d ? { ...d, [k]: v } : d)

  // Only offer profiles the kernel can actually honour.
  const govs = caps?.available_governors ?? []
  const profiles = (Object.keys(PROFILE_INFO) as PowerProfile[]).filter(p => {
    if (p === 'system' || p === 'auto') return true
    const g = PROFILE_INFO[p].governor
    return govs.length === 0 ? ['powersave', 'conservative', 'performance'].includes(p) : !!g && govs.includes(g)
  })

  const managed = !!cfg && (cfg.power_profile !== 'system' || cfg.dynamic_throttle_enabled)
  const thermal = live?.thermal_state ?? 'normal'
  const statusPill: SectionCardProps['status'] = thermal === 'stage2_crit' ? { led: 'err', text: 'Throttling · min clock' }
    : thermal === 'stage1_warn' ? { led: 'warn', text: 'Throttling · reduced' }
    : managed ? { led: 'ok', text: 'Managed' }
    : { led: 'idle', text: 'System default' }

  return (
    <SectionCard
      id="power"
      icon={<Icon name="thermometer" size={16} />}
      title="Power & Thermal"
      sub="Temperature history, CPU power profile, and optional dynamic thermal soft-throttling."
      status={statusPill}
      dirty={dirty}
    >
      <SectionBody>
        {live && (
          <div style={{ display: 'flex', flexWrap: 'wrap', gap: 'var(--sp-2)' }}>
            <ResultPill kind="info">CPU {live.cpu_mhz > 0 ? `${Math.round(live.cpu_mhz)} MHz` : 'n/a'}</ResultPill>
            {live.governor && <ResultPill kind="info">governor: {live.governor}</ResultPill>}
            {live.gpu_temp_c != null && <ResultPill kind="info">GPU {live.gpu_temp_c.toFixed(1)} °C</ResultPill>}
            {caps && caps.min_freq_mhz > 0 && (
              <ResultPill kind="info">range {caps.min_freq_mhz}–{caps.max_freq_mhz} MHz</ResultPill>
            )}
          </div>
        )}

        <ThermalHistoryChart
          history={history}
          warn={draft?.dynamic_throttle_enabled ? draft.warn_temp_c : undefined}
          crit={draft?.dynamic_throttle_enabled ? draft.crit_temp_c : undefined}
          maxMHz={caps?.max_freq_mhz}
        />

        {draft && (
          <>
            <Field label="Temperature logging" hint="Samples CPU/GPU temperature and clock every 15 s (last hour in memory) and every minute to /mutable (last 24 h, append-only).">
              <Toggle on={draft.temp_logging_enabled} onClick={() => set('temp_logging_enabled', !draft.temp_logging_enabled)} />
            </Field>

            <Field label="Power profile" hint={PROFILE_INFO[draft.power_profile]?.hint}>
              <select
                value={draft.power_profile}
                onChange={e => set('power_profile', e.target.value as PowerProfile)}
                style={{
                  fontSize: 'var(--ts-md)', color: 'var(--fg-0)', background: 'var(--bg-0)',
                  border: '1px solid var(--border)', borderRadius: 'var(--radius-sm)',
                  padding: '10px var(--sp-3)', maxWidth: 360,
                }}
              >
                {profiles.map(p => <option key={p} value={p}>{PROFILE_INFO[p].label}</option>)}
              </select>
            </Field>

            <Field label="Dynamic thermal soft-throttling" hint="Steps the CPU clock down before the SoC reaches its hardware throttle point, then restores it once it cools by the hysteresis margin.">
              <Toggle on={draft.dynamic_throttle_enabled} onClick={() => set('dynamic_throttle_enabled', !draft.dynamic_throttle_enabled)} />
            </Field>

            {draft.dynamic_throttle_enabled && (
              <>
                <FieldGrid>
                  <Field label="Stage 1 — reduce clock at" hint={caps?.mid_freq_mhz ? `Caps max clock to ${caps.mid_freq_mhz} MHz.` : 'Caps max clock to mid frequency.'}>
                    <InputGroup>
                      <Input type="number" min={45} max={95} step={1} value={draft.warn_temp_c}
                        onChange={e => set('warn_temp_c', Number(e.target.value))} style={{ width: 100 }} />
                      <span style={{ color: 'var(--fg-2)', fontSize: 'var(--ts-sm)', alignSelf: 'center', marginLeft: 8 }}>°C</span>
                    </InputGroup>
                  </Field>
                  <Field label="Stage 2 — minimum clock at" hint={caps?.min_freq_mhz ? `Forces powersave @ ${caps.min_freq_mhz} MHz.` : 'Forces powersave at minimum clock.'}>
                    <InputGroup>
                      <Input type="number" min={50} max={100} step={1} value={draft.crit_temp_c}
                        onChange={e => set('crit_temp_c', Number(e.target.value))} style={{ width: 100 }} />
                      <span style={{ color: 'var(--fg-2)', fontSize: 'var(--ts-sm)', alignSelf: 'center', marginLeft: 8 }}>°C</span>
                    </InputGroup>
                  </Field>
                  <Field label="Recovery hysteresis" hint="Must cool this far below a threshold before stepping back up.">
                    <InputGroup>
                      <Input type="number" min={1} max={15} step={0.5} value={draft.hysteresis_c}
                        onChange={e => set('hysteresis_c', Number(e.target.value))} style={{ width: 100 }} />
                      <span style={{ color: 'var(--fg-2)', fontSize: 'var(--ts-sm)', alignSelf: 'center', marginLeft: 8 }}>°C</span>
                    </InputGroup>
                  </Field>
                </FieldGrid>
                {draft.crit_temp_c <= draft.warn_temp_c && (
                  <Callout type="warn">Stage 2 must be higher than Stage 1 — the backend will adjust it on save.</Callout>
                )}
                <Callout type="info">
                  Hardware limits for reference: Raspberry Pi firmware soft-throttles at 80 °C (hard 85 °C);
                  RK3566 boards start kernel passive throttling at 75 °C (second step 85 °C). Soft-throttling here acts earlier and more gently.
                </Callout>
              </>
            )}
          </>
        )}
      </SectionBody>
      <SectionFooter>
        <BtnPrimary onClick={save} disabled={!dirty || saving} loading={saving}>Save</BtnPrimary>
        <Btn onClick={loadHistory} variant="ghost" icon={<Icon name="refresh-cw" size={14} />}>Refresh</Btn>
        <Btn onClick={clearHistory} variant="danger">Clear history</Btn>
        {dirty && <DirtyText />}
        {msg && <ResultPill kind={msg.kind}>{msg.text}</ResultPill>}
      </SectionFooter>
    </SectionCard>
  )
}

function ThermalHistoryChart({ history, warn, crit, maxMHz }: {
  history: ThermalHistoryResponse | null
  warn?: number
  crit?: number
  maxMHz?: number
}) {
  const [hover, setHover] = useState<number | null>(null)
  const samples = history?.samples ?? []

  const box: React.CSSProperties = {
    background: 'var(--bg-0)', border: '1px solid var(--border)',
    borderRadius: 'var(--radius-md)', padding: 'var(--sp-3)',
  }
  if (!history) return <div style={{ ...box, color: 'var(--fg-2)', fontSize: 'var(--ts-sm)' }}>Loading temperature history…</div>
  if (samples.length < 2) {
    return (
      <div style={{ ...box, color: 'var(--fg-2)', fontSize: 'var(--ts-sm)' }}>
        {history.temp_logging_enabled ? 'Collecting temperature samples — the chart appears after the first few readings.' : 'Temperature logging is disabled.'}
      </div>
    )
  }

  const W = 800, H = 220, padL = 40, padR = 48, padT = 12, padB = 24
  const t0 = Date.parse(samples[0].ts)
  const t1 = Date.parse(samples[samples.length - 1].ts)
  const span = Math.max(1, t1 - t0)
  const temps = samples.flatMap(s => s.gpu_temp_c != null ? [s.cpu_temp_c, s.gpu_temp_c] : [s.cpu_temp_c])
  if (warn != null) temps.push(warn)
  if (crit != null) temps.push(crit)
  const tMin = Math.floor((Math.min(...temps) - 3) / 5) * 5
  const tMax = Math.ceil((Math.max(...temps) + 3) / 5) * 5
  const mMax = Math.max(maxMHz ?? 0, ...samples.map(s => s.cpu_mhz)) || 1

  const x = (ts: string) => padL + ((Date.parse(ts) - t0) / span) * (W - padL - padR)
  const yT = (c: number) => padT + (1 - (c - tMin) / (tMax - tMin)) * (H - padT - padB)
  const yM = (m: number) => padT + (1 - m / mMax) * (H - padT - padB)
  const path = (pts: [number, number][]) => pts.map((p, i) => `${i ? 'L' : 'M'}${p[0].toFixed(1)},${p[1].toFixed(1)}`).join('')

  const cpuPath = path(samples.map(s => [x(s.ts), yT(s.cpu_temp_c)]))
  const hasGpu = samples.some(s => s.gpu_temp_c != null)
  const gpuPath = hasGpu ? path(samples.filter(s => s.gpu_temp_c != null).map(s => [x(s.ts), yT(s.gpu_temp_c!)])) : ''
  const mhzPath = path(samples.map(s => [x(s.ts), yM(s.cpu_mhz)]))
  const ticks: number[] = []
  for (let c = tMin; c <= tMax; c += (tMax - tMin) > 30 ? 10 : 5) ticks.push(c)
  const fmtTime = (ms: number) => new Date(ms).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
  const hs = hover != null ? samples[hover] : null

  const onMove = (e: React.MouseEvent<SVGSVGElement>) => {
    const rect = e.currentTarget.getBoundingClientRect()
    const px = ((e.clientX - rect.left) / rect.width) * W
    const tt = t0 + ((px - padL) / (W - padL - padR)) * span
    let best = 0, bestD = Infinity
    samples.forEach((s, i) => { const d = Math.abs(Date.parse(s.ts) - tt); if (d < bestD) { bestD = d; best = i } })
    setHover(best)
  }

  const legend = (color: string, label: string, dashed = false) => (
    <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }}>
      <svg width={18} height={6}><line x1={0} y1={3} x2={18} y2={3} stroke={color} strokeWidth={2} strokeDasharray={dashed ? '4 3' : undefined} /></svg>
      {label}
    </span>
  )

  return (
    <div style={box}>
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 'var(--sp-2)', marginBottom: 'var(--sp-2)', alignItems: 'center' }}>
        <ResultPill kind="info">min {history.min_temp_c.toFixed(1)} °C</ResultPill>
        <ResultPill kind="info">avg {history.avg_temp_c.toFixed(1)} °C</ResultPill>
        <ResultPill kind={history.max_temp_c >= (warn ?? 80) ? 'err' : 'info'}>max {history.max_temp_c.toFixed(1)} °C</ResultPill>
        <span style={{ marginLeft: 'auto', fontSize: 'var(--ts-xs)', color: 'var(--fg-2)', fontFamily: 'var(--font-mono)' }}>
          {history.sample_count} samples · {fmtTime(t0)}–{fmtTime(t1)}
        </span>
      </div>
      <svg viewBox={`0 0 ${W} ${H}`} style={{ width: '100%', height: 'auto', display: 'block' }}
        onMouseMove={onMove} onMouseLeave={() => setHover(null)}>
        {ticks.map(c => (
          <g key={c}>
            <line x1={padL} x2={W - padR} y1={yT(c)} y2={yT(c)} stroke="var(--border)" strokeWidth={1} />
            <text x={padL - 6} y={yT(c) + 4} textAnchor="end" fontSize={10} fill="var(--fg-2)">{c}°</text>
          </g>
        ))}
        {[0, 0.5, 1].map(f => (
          <text key={f} x={W - padR + 6} y={yM(mMax * f) + 4} fontSize={10} fill="var(--fg-3)">{Math.round(mMax * f)}</text>
        ))}
        <text x={W - 4} y={padT - 2} textAnchor="end" fontSize={9} fill="var(--fg-3)">MHz</text>
        {warn != null && <line x1={padL} x2={W - padR} y1={yT(warn)} y2={yT(warn)} stroke="var(--warn)" strokeDasharray="6 4" strokeWidth={1.2} />}
        {crit != null && <line x1={padL} x2={W - padR} y1={yT(crit)} y2={yT(crit)} stroke="var(--err)" strokeDasharray="6 4" strokeWidth={1.2} />}
        <path d={mhzPath} fill="none" stroke="var(--fg-3)" strokeWidth={1} opacity={0.7} />
        {hasGpu && <path d={gpuPath} fill="none" stroke="var(--warn)" strokeWidth={1.5} opacity={0.8} />}
        <path d={cpuPath} fill="none" stroke="var(--accent)" strokeWidth={2} />
        <text x={padL} y={H - 6} fontSize={10} fill="var(--fg-2)">{fmtTime(t0)}</text>
        <text x={W - padR} y={H - 6} textAnchor="end" fontSize={10} fill="var(--fg-2)">{fmtTime(t1)}</text>
        {hs && (
          <g>
            <line x1={x(hs.ts)} x2={x(hs.ts)} y1={padT} y2={H - padB} stroke="var(--fg-2)" strokeWidth={1} />
            <circle cx={x(hs.ts)} cy={yT(hs.cpu_temp_c)} r={3.5} fill="var(--accent)" />
          </g>
        )}
      </svg>
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 'var(--sp-4)', fontSize: 'var(--ts-xs)', color: 'var(--fg-2)', marginTop: 'var(--sp-2)' }}>
        {legend('var(--accent)', 'CPU °C')}
        {hasGpu && legend('var(--warn)', 'GPU °C')}
        {legend('var(--fg-3)', 'CPU MHz (right axis)')}
        {warn != null && legend('var(--warn)', `Stage 1 ${warn} °C`, true)}
        {crit != null && legend('var(--err)', `Stage 2 ${crit} °C`, true)}
        {hs && (
          <span style={{ marginLeft: 'auto', fontFamily: 'var(--font-mono)', color: 'var(--fg-1)' }}>
            {new Date(hs.ts).toLocaleTimeString()} · {hs.cpu_temp_c.toFixed(1)} °C
            {hs.gpu_temp_c != null ? ` / GPU ${hs.gpu_temp_c.toFixed(1)} °C` : ''} · {Math.round(hs.cpu_mhz)} MHz
            {hs.archive_state ? ` · ${hs.archive_state}` : ''}
            {hs.thermal_state && hs.thermal_state !== 'normal' ? ` · ${hs.thermal_state}` : ''}
          </span>
        )}
      </div>
    </div>
  )
}
