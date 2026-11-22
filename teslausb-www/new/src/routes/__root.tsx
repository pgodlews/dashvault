import { createRootRoute, Outlet, Link, useRouter, redirect, isRedirect } from '@tanstack/react-router'
import { useStatus } from '../hooks/useStatus'
import { Icon } from '../components/Icon'
import { useEffect, useState } from 'react'

export const Route = createRootRoute({
  beforeLoad: async ({ location }) => {
    const onSetup = location.pathname === '/setup'
    try {
      const res = await fetch('/api/setup/status')
      const data = await res.json()
      if (!data.configured && !onSetup) {
        throw redirect({ to: '/setup' })
      }
      // The wizard is for first-time setup only.
      if (data.configured && onSetup) {
        throw redirect({ to: '/' })
      }
    } catch (e) {
      if (isRedirect(e)) throw e
    }
  },
  component: RootLayout,
})

function RootLayout() {
  const router = useRouter()
  const isSetup = router.state.location.pathname === '/setup'

  // Hooks must run unconditionally on every render — keep this above the
  // isSetup early return or navigating setup → home crashes the app.
  const { status } = useStatus()
  const sdRating = status?.sdcard?.latest_rating

  if (isSetup) {
    return <Outlet />
  }

  return (
    <div style={{ height: '100%', display: 'flex', flexDirection: 'column' }}>
      <TopBar />

      {/* Global SD card speed warning banner */}
      {(sdRating === 'slow' || sdRating === 'marginal') && (
        <div style={{
          background: sdRating === 'slow' ? '#450a0a' : '#451a03',
          borderBottom: `1px solid ${sdRating === 'slow' ? '#991b1b' : '#92400e'}`,
          color: sdRating === 'slow' ? '#fca5a5' : '#fcd34d',
          padding: '6px var(--sp-6)',
          fontSize: 'var(--ts-sm)',
          display: 'flex',
          alignItems: 'center',
          gap: 'var(--sp-3)',
          flexShrink: 0,
        }}>
          <span>{sdRating === 'slow' ? '🔴' : '🟡'}</span>
          <span>
            <strong>SD card {sdRating === 'slow' ? 'too slow' : 'marginal'}
            </strong>{' '}
            {status?.sdcard?.latest_write_mbps != null
              ? `(${status.sdcard.latest_write_mbps.toFixed(1)} MB/s)`
              : status?.sdcard?.benchmark_running ? '(benchmark running…)' : ''}
            {' '}— clips may be lost.{' '}
            <Link to="/settings" style={{ color: 'inherit', textDecoration: 'underline' }}>
              View details →
            </Link>
          </span>
        </div>
      )}

      {/* Global TBW write-endurance warning banner */}
      {status?.sdcard?.write_warning && (
        <div style={{
          background: '#1c1917',
          borderBottom: '1px solid #b45309',
          color: '#fdba74',
          padding: '6px var(--sp-6)',
          fontSize: 'var(--ts-sm)',
          display: 'flex',
          alignItems: 'center',
          gap: 'var(--sp-3)',
          flexShrink: 0,
        }}>
          <span>🟠</span>
          <span>
            <strong>SD card write endurance warning</strong>{' '}
            {status.sdcard.total_written_gb != null
              ? `(${(status.sdcard.total_written_gb / 1000).toFixed(1)} TB written)`
              : ''}
            {' '}— consider replacing your SD card.{' '}
            <Link to="/settings" style={{ color: 'inherit', textDecoration: 'underline' }}>
              View details →
            </Link>
          </span>
        </div>
      )}

      <main style={{ flex: 1, overflow: 'auto' }}>
        <Outlet />
      </main>
    </div>
  )
}

function TopBar() {
  const router = useRouter()
  const { status } = useStatus()
  const [theme, setTheme] = useState(() => localStorage.getItem('teslausb.theme') || 'dark')

  useEffect(() => {
    const root = document.documentElement
    root.classList.toggle('theme-dark', theme === 'dark')
    root.classList.toggle('theme-light', theme === 'light')
    localStorage.setItem('teslausb.theme', theme)
  }, [theme])

  const pathname = router.state.location.pathname

  const tabs = [
    { to: '/', label: 'Dashboard' },
    { to: '/clips', label: 'Clips' },
    { to: '/viewer', label: 'Viewer' },
    { to: '/map', label: 'Map' },
    { to: '/logs', label: 'Logs' },
    { to: '/settings', label: 'Settings' },
  ]

  const archiveState = status?.archive?.state ?? 'unknown'
  const archiveLabel = archiveState === 'archiving' ? 'archiving…'
    : archiveState === 'error' ? 'archive error'
    : archiveState === 'unknown' ? 'archive unknown'
    : 'archive idle'
  const archiveLed = archiveState === 'archiving' ? 'var(--warn)'
    : archiveState === 'error' ? 'var(--err)'
    : archiveState === 'unknown' ? 'var(--fg-2)'
    : 'var(--ok)'

  // The collector stats /, /mutable, /backingfiles in that order — the pill
  // should show clip storage (/backingfiles), not the root filesystem.
  const disks = status?.disks ?? []
  const clipDisk = disks.find(d => d.mount === '/backingfiles') ?? disks[disks.length - 1]
  const freePct = clipDisk ? (100 - clipDisk.used_pct).toFixed(0) : '—'

  return (
    <nav aria-label="Main" className="app-topbar" style={{
      display: 'flex',
      alignItems: 'center',
      background: 'var(--bg-1)',
      borderBottom: '1px solid var(--border)',
      flexShrink: 0,
    }}>
      {/* Brand */}
      <div style={{
        display: 'flex',
        alignItems: 'center',
        gap: 'var(--sp-2)',
        fontFamily: 'var(--font-mono)',
        fontWeight: 600,
        letterSpacing: '.02em',
        fontSize: 'var(--ts-sm)',
      }}>
        <span style={{
          width: 8, height: 8, borderRadius: '50%',
          background: 'var(--accent)',
          boxShadow: '0 0 10px var(--accent)',
        }} />
        <span>dashvault</span>
      </div>

      {/* Tabs */}
      <div className="app-topbar-tabs" data-scroll-x>
        {tabs.map(t => {
          const active = pathname === t.to
          return (
            <Link
              key={t.to}
              to={t.to}
              className="app-topbar-tab"
              style={{
                borderRadius: 'var(--radius-pill)',
                color: active ? 'var(--accent-ink)' : 'var(--fg-2)',
                background: active ? 'var(--accent)' : 'transparent',
                fontSize: 'var(--ts-sm)',
                fontWeight: 500,
                textDecoration: 'none',
                transition: 'background .1s, color .1s',
              }}
              activeProps={{}}
            >
              {t.label}
            </Link>
          )
        })}
      </div>

      <div style={{ flex: 1 }} />

      {/* Meta pills */}
      <div style={{
        display: 'flex',
        gap: 'var(--sp-4)',
        alignItems: 'center',
        fontFamily: 'var(--font-mono)',
        fontSize: 'var(--ts-xs)',
        color: 'var(--fg-2)',
      }}>
        <span style={{
          display: 'inline-flex',
          alignItems: 'center',
          gap: 6,
          padding: '4px 10px',
          borderRadius: 'var(--radius-pill)',
          border: '1px solid var(--border)',
          background: 'var(--bg-2)',
        }}>
          <span style={{
            width: 6, height: 6, borderRadius: '50%',
            background: archiveLed,
            boxShadow: `0 0 6px ${archiveLed}`,
          }} />
          {archiveLabel}
        </span>
        <span className="app-topbar-storage" style={{
          display: 'inline-flex',
          alignItems: 'center',
          gap: 6,
          padding: '4px 10px',
          borderRadius: 'var(--radius-pill)',
          border: '1px solid var(--border)',
          background: 'var(--bg-2)',
        }}>
          {clipDisk?.total_gb != null
            ? `${clipDisk.total_gb.toFixed(0)} GB · ${freePct}% free`
            : '—'}
        </span>
      </div>

      {/* Theme toggle */}
      <div style={{
        display: 'flex',
        alignItems: 'center',
        gap: 0,
        border: '1px solid var(--border)',
        background: 'var(--bg-2)',
        borderRadius: 'var(--radius-pill)',
        padding: 3,
      }}>
        <button
          onClick={() => setTheme('light')}
          title="Light"
          style={{
            width: 26, height: 22,
            borderRadius: 'var(--radius-pill)',
            color: theme === 'light' ? 'var(--fg-0)' : 'var(--fg-2)',
            background: theme === 'light' ? 'var(--bg-4)' : 'transparent',
            display: 'grid',
            placeItems: 'center',
            border: 'none',
            cursor: 'pointer',
          }}
        >
          <Icon name="sun" size={14} />
        </button>
        <button
          onClick={() => setTheme('dark')}
          title="Dark"
          style={{
            width: 26, height: 22,
            borderRadius: 'var(--radius-pill)',
            color: theme === 'dark' ? 'var(--fg-0)' : 'var(--fg-2)',
            background: theme === 'dark' ? 'var(--bg-4)' : 'transparent',
            display: 'grid',
            placeItems: 'center',
            border: 'none',
            cursor: 'pointer',
          }}
        >
          <Icon name="moon" size={14} />
        </button>
      </div>
    </nav>
  )
}
