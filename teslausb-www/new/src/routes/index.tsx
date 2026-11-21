import { createFileRoute, Link } from '@tanstack/react-router'
import { useStatus } from '../hooks/useStatus'
import { Tile, TileValue, Bar } from '../components/Tile'
import {
  formatUptime,
  tempColor,
  diskColor,
  archiveColor,
  wifiBars,
  throttleWarnings,
} from '../lib/status'
import type { ArchiveState, PowerSummary, SdCardSummary } from '../lib/status'

export const Route = createFileRoute('/')({
  component: DashboardPage,
})

// ── page ─────────────────────────────────────────────────────────────────────

function DashboardPage() {
  const { status, conn, lastUpdate } = useStatus()

  return (
    <div style={{ padding: 'var(--sp-6)', maxWidth: 900 }}>
      {/* header row */}
      <div style={{ display: 'flex', alignItems: 'baseline', gap: 'var(--sp-4)', marginBottom: 'var(--sp-6)' }}>
        <h1 style={{ color: 'var(--fg-0)', fontSize: 'var(--ts-xl)', fontWeight: 600, margin: 0 }}>
          Dashboard
        </h1>
        <ConnectionBadge conn={conn} lastUpdate={lastUpdate} />
      </div>

      {/* tile grid */}
      {status == null ? (
        <SkeletonGrid />
      ) : (
        <div style={{
          display: 'grid',
          gridTemplateColumns: 'repeat(auto-fill, minmax(220px, 1fr))',
          gap: 'var(--sp-4)',
        }}>
          <ArchiveTile
            state={status.archive.state}
            message={status.archive.message}
            filesTransferred={status.archive.files_transferred}
            currentFile={status.archive.current_file}
          />
          <UptimeTile seconds={status.uptime_seconds} />
          <TempTile celsius={status.cpu_temp_c} gpuCelsius={status.gpu_temp_c} power={status.power} />
          {status.fan_rpm != null && (
            <Tile label="Fan speed"><TileValue value={String(status.fan_rpm)} unit="RPM" /></Tile>
          )}
          {status.external_5v != null && (
            <Tile label="External 5V"><TileValue value={status.external_5v.toFixed(3)} unit="V" /></Tile>
          )}
          {status.rtc_battery_v != null && status.rtc_battery_v >= 2.5 && (
            <Tile label="RTC Battery"><TileValue value={status.rtc_battery_v.toFixed(3)} unit="V" /></Tile>
          )}
          {status.throttled != null && throttleWarnings(status.throttled).length > 0 && (
            <Tile label="Power / temperature warnings" accent="var(--err)">
              {throttleWarnings(status.throttled).map(warning => (
                <span key={warning} style={{ color: 'var(--err)', fontSize: 'var(--ts-sm)' }}>{warning}</span>
              ))}
            </Tile>
          )}
          {status.disks.map(d => (
            <DiskTile key={d.mount} mount={d.mount} usedGB={d.used_gb} totalGB={d.total_gb} pct={d.used_pct} />
          ))}
          {status.wifi && (
            <WifiTile ssid={status.wifi.ssid} linkLevel={status.wifi.link_level} signalDbm={status.wifi.signal_dbm} />
          )}
          <SdCardTile sdcard={status.sdcard} />
        </div>
      )}
    </div>
  )
}

// ── connection badge ──────────────────────────────────────────────────────────

function ConnectionBadge({ conn, lastUpdate }: { conn: string; lastUpdate: Date | null }) {
  const dot = conn === 'live' ? 'var(--ok)'
    : conn === 'error' ? 'var(--err)'
    : 'var(--warn)'

  const label = conn === 'live' ? (lastUpdate ? `updated ${timeAgo(lastUpdate)}` : 'live')
    : conn === 'connecting' ? 'connecting…'
    : 'reconnecting…'

  return (
    <span style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 'var(--ts-sm)', color: 'var(--fg-2)' }}>
      <span style={{ width: 7, height: 7, borderRadius: '50%', background: dot, flexShrink: 0 }} />
      {label}
    </span>
  )
}

function timeAgo(d: Date): string {
  const s = Math.round((Date.now() - d.getTime()) / 1000)
  if (s < 5) return 'just now'
  if (s < 60) return `${s}s ago`
  return `${Math.round(s / 60)}m ago`
}

// ── individual tiles ──────────────────────────────────────────────────────────

function ArchiveTile({ state, message, filesTransferred, currentFile }: {
  state: ArchiveState
  message?: string
  filesTransferred?: number
  currentFile?: string
}) {
  const color = archiveColor(state)
  const label = state === 'archiving' ? 'Archiving…'
    : state === 'error' ? 'Archive error'
    : state === 'idle' ? 'Archive idle'
    : 'Archive unknown'

  return (
    <Tile label="Archive" accent={color}>
      <TileValue value={label} color={color} />
      {message && (
        <span style={{ fontSize: 'var(--ts-sm)', color: 'var(--fg-2)', wordBreak: 'break-all' }}>
          {message}
        </span>
      )}
      {state === 'archiving' && (filesTransferred ?? 0) > 0 && (
        <span style={{ fontSize: 'var(--ts-sm)', color: 'var(--fg-2)', fontFamily: 'var(--font-mono)' }}>
          {filesTransferred} file{filesTransferred === 1 ? '' : 's'} transferred
        </span>
      )}
      {state === 'archiving' && currentFile && (
        <span style={{
          fontSize: 'var(--ts-xs)',
          color: 'var(--fg-3)',
          fontFamily: 'var(--font-mono)',
          wordBreak: 'break-all',
          opacity: 0.8,
        }}>
          {currentFile}
        </span>
      )}
    </Tile>
  )
}

function UptimeTile({ seconds }: { seconds: number }) {
  return (
    <Tile label="Uptime">
      <TileValue value={formatUptime(seconds)} />
    </Tile>
  )
}

function TempTile({ celsius, gpuCelsius, power }: { celsius: number; gpuCelsius?: number; power?: PowerSummary }) {
  const hottest = gpuCelsius != null ? Math.max(celsius, gpuCelsius) : celsius
  const color = tempColor(hottest)
  const throttling = power?.thermal_state === 'stage1_warn' || power?.thermal_state === 'stage2_crit'
  const mono = { fontSize: 'var(--ts-xs)', color: 'var(--fg-2)', fontFamily: 'var(--font-mono)' } as const
  return (
    <Tile label={gpuCelsius != null ? 'SoC Temp' : 'CPU Temp'} accent={throttling ? 'var(--warn)' : color}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'baseline', gap: 'var(--sp-2)' }}>
        <TileValue value={celsius.toFixed(1)} unit="°C" color={tempColor(celsius)} />
        {gpuCelsius != null && (
          <span style={mono}>GPU {gpuCelsius.toFixed(1)} °C</span>
        )}
      </div>
      <Bar pct={(hottest / 100) * 100} color={color} />
      {power && power.cpu_mhz > 0 && (
        <span style={mono}>
          {Math.round(power.cpu_mhz)} MHz{power.governor ? ` · ${power.governor}` : ''}
          {power.profile !== 'system' ? ` · profile: ${power.profile}` : ''}
        </span>
      )}
      {throttling && (
        <span style={{ fontSize: 'var(--ts-xs)', color: power?.thermal_state === 'stage2_crit' ? 'var(--err)' : 'var(--warn)', fontWeight: 600 }}>
          {power?.thermal_state === 'stage2_crit' ? 'Thermal throttle: minimum clock' : 'Thermal throttle: clock reduced'}
        </span>
      )}
    </Tile>
  )
}

function DiskTile({ mount, usedGB, totalGB, pct }: { mount: string; usedGB: number; totalGB: number; pct: number }) {
  const color = diskColor(pct)
  const label = mount === '/' ? 'Root' : mount.replace(/^\//, '')
  return (
    <Tile label={`Disk · ${label}`} accent={color}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'baseline' }}>
        <TileValue value={usedGB.toFixed(1)} unit="GB used" color={color} />
        <span style={{ fontSize: 'var(--ts-sm)', color: 'var(--fg-2)', fontFamily: 'var(--font-mono)' }}>
          / {totalGB.toFixed(1)} GB
        </span>
      </div>
      <Bar pct={pct} color={color} />
      <span style={{ fontSize: 'var(--ts-xs)', color: 'var(--fg-2)', fontFamily: 'var(--font-mono)' }}>
        {pct.toFixed(1)}% used
      </span>
    </Tile>
  )
}

function WifiTile({ ssid, linkLevel, signalDbm }: { ssid: string; linkLevel: number; signalDbm: number }) {
  const bars = wifiBars(linkLevel)
  return (
    <Tile label="Wi-Fi">
      <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--sp-3)' }}>
        <WifiBarsIcon bars={bars} />
        <div style={{ minWidth: 0 }}>
          <div style={{ fontWeight: 600, fontSize: 'var(--ts-md)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
            {ssid || '(unknown)'}
          </div>
          <div style={{ fontSize: 'var(--ts-sm)', color: 'var(--fg-2)', fontFamily: 'var(--font-mono)' }}>
            {signalDbm} dBm
          </div>
        </div>
      </div>
    </Tile>
  )
}

function WifiBarsIcon({ bars }: { bars: number }) {
  const heights = [10, 16, 22, 28]
  return (
    <div style={{ display: 'flex', alignItems: 'flex-end', gap: 3, height: 28, flexShrink: 0 }}>
      {heights.map((h, i) => (
        <div key={i} style={{
          width: 5,
          height: h,
          borderRadius: 2,
          background: i < bars ? 'var(--ok)' : 'var(--bg-4)',
        }} />
      ))}
    </div>
  )
}

function SdCardTile({ sdcard }: { sdcard?: SdCardSummary }) {
  if (!sdcard || (!sdcard.latest_rating && !sdcard.benchmark_running)) {
    // No data yet — show a prompt tile
    return (
      <Tile label="SD Card">
        <span style={{ fontSize: 'var(--ts-sm)', color: 'var(--fg-2)' }}>
          Not tested.{' '}
          <Link to="/settings" style={{ color: 'var(--accent)', textDecoration: 'none' }}>
            Run benchmark →
          </Link>
        </span>
      </Tile>
    )
  }

  const rating = sdcard?.latest_rating
  const mbps   = sdcard?.latest_write_mbps
  const writtenGB = sdcard?.total_written_gb ?? 0

  const ratingColor = rating === 'good' ? 'var(--ok)'
    : rating === 'marginal' ? 'var(--warn)'
    : rating === 'slow' ? 'var(--err)'
    : 'var(--fg-2)'

  const ratingLabel = rating === 'good' ? '✓ Good'
    : rating === 'marginal' ? '⚠ Marginal'
    : rating === 'slow' ? '✗ Too slow'
    : sdcard?.benchmark_running ? 'Running…'
    : 'Not tested'

  return (
    <Tile label="SD Card" accent={ratingColor}>
      <TileValue
        value={mbps != null ? mbps.toFixed(1) : '—'}
        unit={mbps != null ? 'MB/s' : ''}
        color={ratingColor}
      />
      <span style={{ fontSize: 'var(--ts-sm)', color: ratingColor, fontWeight: 600 }}>
        {ratingLabel}
      </span>
      {writtenGB > 0 && (
        <span style={{ fontSize: 'var(--ts-xs)', color: 'var(--fg-2)', fontFamily: 'var(--font-mono)' }}>
          {writtenGB >= 1000
            ? `${(writtenGB / 1000).toFixed(2)} TB written`
            : `${writtenGB.toFixed(1)} GB written`}
        </span>
      )}
    </Tile>
  )
}

// ── skeleton ──────────────────────────────────────────────────────────────────

function SkeletonGrid() {
  return (
    <div style={{
      display: 'grid',
      gridTemplateColumns: 'repeat(auto-fill, minmax(220px, 1fr))',
      gap: 'var(--sp-4)',
    }}>
      {Array.from({ length: 5 }, (_, i) => (
        <div key={i} style={{
          background: 'var(--bg-2)',
          border: '1px solid var(--border)',
          borderRadius: 'var(--radius-md)',
          height: 96,
          animation: 'pulse 1.6s ease-in-out infinite',
        }} />
      ))}
      <style>{`
        @keyframes pulse {
          0%, 100% { opacity: 1; }
          50% { opacity: .4; }
        }
      `}</style>
    </div>
  )
}
