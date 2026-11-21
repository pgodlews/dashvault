// Types mirroring the Go backend's collect.go Status struct.

export interface DiskStat {
  mount: string
  total_gb: number
  used_gb: number
  free_gb: number
  used_pct: number
}

export interface WiFiInfo {
  ssid: string
  link_level: number
  signal_dbm: number
  noise_dbm: number
}

export type ArchiveState = 'idle' | 'archiving' | 'error' | 'unknown'

export interface ArchiveStat {
  state: ArchiveState
  message?: string
  files_transferred?: number  // count of files rsync has sent so far
  current_file?: string       // last file rsync reported transferring
}

export type SdCardRating = 'good' | 'marginal' | 'slow' | 'unknown'

export interface SdCardSummary {
  latest_rating?: SdCardRating
  latest_write_mbps?: number
  total_written_gb?: number
  benchmark_running: boolean
  write_warning?: boolean  // true when TBW endurance threshold reached
}

export type ThermalState = 'normal' | 'stage1_warn' | 'stage2_crit'

export interface PowerSummary {
  cpu_mhz: number
  gpu_temp_c?: number
  governor?: string
  profile: string                 // 'system' = unmanaged (default)
  thermal_state: ThermalState
  temp_logging_enabled: boolean
  dynamic_throttle_enabled: boolean
}

export interface Status {
  collected_at: string   // ISO-8601 UTC
  uptime_seconds: number
  cpu_temp_c: number
  gpu_temp_c?: number
  fan_rpm?: number
  external_5v?: number
  rtc_battery_v?: number
  throttled?: number
  disks: DiskStat[]
  wifi?: WiFiInfo
  archive: ArchiveStat
  sdcard?: SdCardSummary
  power?: PowerSummary
}

// ── helpers ──────────────────────────────────────────────────────────────────

export function formatUptime(seconds: number): string {
  const s = Math.floor(seconds)
  const d = Math.floor(s / 86400)
  const h = Math.floor((s % 86400) / 3600)
  const m = Math.floor((s % 3600) / 60)
  if (d > 0) return `${d}d ${h}h ${m}m`
  if (h > 0) return `${h}h ${m}m`
  return `${m}m`
}

export function tempColor(c: number): string {
  if (c >= 80) return 'var(--err)'
  if (c >= 65) return 'var(--warn)'
  return 'var(--ok)'
}

export function diskColor(pct: number): string {
  if (pct >= 95) return 'var(--err)'
  if (pct >= 80) return 'var(--warn)'
  return 'var(--ok)'
}

export function archiveColor(state: ArchiveState): string {
  if (state === 'error') return 'var(--err)'
  if (state === 'archiving') return 'var(--accent)'
  if (state === 'idle') return 'var(--ok)'
  return 'var(--fg-2)'
}

/** Wifi signal: 0-3 bars based on link level (0–70 scale in /proc/net/wireless). */
export function wifiBars(linkLevel: number): number {
  if (linkLevel >= 55) return 3
  if (linkLevel >= 35) return 2
  if (linkLevel >= 15) return 1
  return 0
}

/** Current and historical firmware power/temperature flags. */
export function throttleWarnings(bits: number): string[] {
  const flags: [number, string][] = [
    [0, 'Undervoltage detected'],
    [1, 'Arm frequency capped'],
    [2, 'Currently throttled'],
    [3, 'Soft temperature limit active'],
    [16, 'Undervoltage has occurred since last reboot'],
    [17, 'Arm frequency capping has occurred since last reboot'],
    [18, 'Throttling has occurred since last reboot'],
    [19, 'Soft temperature limit has occurred since last reboot'],
  ]
  return flags.filter(([bit]) => (bits & (1 << bit)) !== 0).map(([, label]) => label)
}
