// Types mirroring the Go backend's power_ops.go API (/api/power/*).
import type { PowerSummary } from './status'

export type PowerProfile =
  | 'system' | 'auto' | 'powersave' | 'conservative'
  | 'schedutil' | 'ondemand' | 'interactive' | 'performance'

export interface PowerConfig {
  temp_logging_enabled: boolean      // default: true
  power_profile: PowerProfile        // default: 'system' (unmanaged)
  dynamic_throttle_enabled: boolean  // default: false
  warn_temp_c: number                // Stage 1: cap clock to mid frequency
  crit_temp_c: number                // Stage 2: force minimum clock
  hysteresis_c: number               // recovery margin below each threshold
}

export interface PowerCapabilities {
  available_governors: string[]
  available_frequencies_mhz: number[]
  min_freq_mhz: number
  max_freq_mhz: number
  mid_freq_mhz: number
  has_devfreq: boolean
  has_gpu_temp: boolean
}

export interface PowerConfigResponse {
  config: PowerConfig
  capabilities: PowerCapabilities
  status: PowerSummary
}

export interface ThermalSample {
  ts: string
  cpu_temp_c: number
  gpu_temp_c?: number
  cpu_mhz: number
  governor?: string
  thermal_state?: string
  archive_state?: string
}

export interface ThermalHistoryResponse {
  temp_logging_enabled: boolean
  sample_count: number
  min_temp_c: number
  max_temp_c: number
  avg_temp_c: number
  samples: ThermalSample[]
}

export const PROFILE_INFO: Record<PowerProfile, { label: string; hint: string; governor?: string }> = {
  system:       { label: 'System default (unmanaged)', hint: 'The web UI never writes CPU/DRAM settings. archiveloop’s CPU_GOVERNOR (conservative by default) applies.' },
  auto:         { label: 'Auto (hybrid)', hint: 'powersave while the car is recording / idle, conservative while archiving to the NAS for full Wi-Fi throughput.' },
  powersave:    { label: 'Power save', hint: 'Lowest clock at all times. Lowest draw and heat; slower archiving.', governor: 'powersave' },
  conservative: { label: 'Conservative', hint: 'Ramps clock up gradually under sustained load.', governor: 'conservative' },
  schedutil:    { label: 'Schedutil', hint: 'Scheduler-driven frequency scaling.', governor: 'schedutil' },
  ondemand:     { label: 'On-demand', hint: 'Jumps to max clock on load, drops back when idle.', governor: 'ondemand' },
  interactive:  { label: 'Interactive', hint: 'Latency-oriented scaling (vendor kernels).', governor: 'interactive' },
  performance:  { label: 'Performance', hint: 'Max clock at all times. Highest draw and heat.', governor: 'performance' },
}
