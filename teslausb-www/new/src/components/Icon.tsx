import type { CSSProperties } from 'react'

interface IconProps {
  name: IconName
  size?: number
  style?: CSSProperties
}

export type IconName =
  | 'eye' | 'eye-off' | 'bell' | 'hdd' | 'plug' | 'send' | 'check' | 'x' | 'x-small'
  | 'alert' | 'info' | 'copy' | 'sun' | 'moon' | 'folder' | 'cloud' | 'sync'
  | 'shield-off' | 'play' | 'pause' | 'skipBack' | 'skipFwd' | 'back10' | 'fwd10'
  | 'fullscreen' | 'pip' | 'share' | 'download' | 'expand' | 'compress' | 'more'
  | 'cpu' | 'wifi' | 'thermometer' | 'activity' | 'rotate-ccw' | 'trash'
  | 'chevron-down' | 'chevron-up' | 'settings' | 'sliders' | 'zap' | 'map-pin'
  | 'clock' | 'disc' | 'server' | 'hard-drive' | 'arrow-right' | 'refresh-cw'
  | 'zoom-out'

export function Icon({ name, size = 18, style }: IconProps) {
  const svgProps = {
    width: size,
    height: size,
    viewBox: '0 0 24 24',
    style,
  }
  const stroke = { fill: 'none', stroke: 'currentColor', strokeWidth: 1.6, strokeLinecap: 'round' as const, strokeLinejoin: 'round' as const }

  switch (name) {
    case 'eye':
      return (
        <svg {...svgProps} {...stroke}>
          <path d="M1 12s4-7 11-7 11 7 11 7-4 7-11 7S1 12 1 12z" />
          <circle cx="12" cy="12" r="3" />
        </svg>
      )
    case 'eye-off':
      return (
        <svg {...svgProps} {...stroke}>
          <path d="M17.94 17.94A10 10 0 0112 20c-7 0-11-8-11-8a19.4 19.4 0 014.22-5.19M9.9 5.15A10 10 0 0112 5c7 0 11 7 11 7a19.5 19.5 0 01-2.16 3.19M14.12 14.12A3 3 0 119.88 9.88" />
          <path d="M1 1l22 22" />
        </svg>
      )
    case 'bell':
      return (
        <svg {...svgProps} {...stroke}>
          <path d="M18 8a6 6 0 00-12 0c0 7-3 9-3 9h18s-3-2-3-9" />
          <path d="M13.73 21a2 2 0 01-3.46 0" />
        </svg>
      )
    case 'hdd':
    case 'hard-drive':
      return (
        <svg {...svgProps} {...stroke}>
          <rect x="3" y="13" width="18" height="8" rx="1" />
          <path d="M5 17h2M4 13l2-7a2 2 0 012-2h8a2 2 0 012 2l2 7" />
        </svg>
      )
    case 'plug':
      return (
        <svg {...svgProps} {...stroke}>
          <path d="M8 3v4M16 3v4M6 7h12l-1 6a5 5 0 01-10 0L6 7zM12 17v4" />
        </svg>
      )
    case 'send':
      return (
        <svg {...svgProps} {...stroke}>
          <path d="M22 2L11 13M22 2l-7 20-4-9-9-4 20-7z" />
        </svg>
      )
    case 'check':
      return (
        <svg {...svgProps} {...stroke}>
          <path d="M5 13l4 4L19 7" />
        </svg>
      )
    case 'x':
      return (
        <svg {...svgProps} {...stroke}>
          <path d="M18 6L6 18M6 6l12 12" />
        </svg>
      )
    case 'x-small':
      return (
        <svg {...svgProps} {...stroke}>
          <path d="M18 6L6 18M6 6l12 12" />
        </svg>
      )
    case 'alert':
      return (
        <svg {...svgProps} {...stroke}>
          <path d="M12 9v4M12 17h.01" />
          <path d="M10.29 3.86L1.82 18a2 2 0 001.71 3h16.94a2 2 0 001.71-3L13.71 3.86a2 2 0 00-3.42 0z" />
        </svg>
      )
    case 'info':
      return (
        <svg {...svgProps} {...stroke}>
          <circle cx="12" cy="12" r="10" />
          <path d="M12 16v-4M12 8h.01" />
        </svg>
      )
    case 'copy':
      return (
        <svg {...svgProps} {...stroke}>
          <rect x="9" y="9" width="13" height="13" rx="2" />
          <path d="M5 15H4a2 2 0 01-2-2V4a2 2 0 012-2h9a2 2 0 012 2v1" />
        </svg>
      )
    case 'sun':
      return (
        <svg {...svgProps} {...stroke}>
          <circle cx="12" cy="12" r="4" />
          <path d="M12 2v2M12 20v2M5 5l1.5 1.5M17.5 17.5L19 19M2 12h2M20 12h2M5 19l1.5-1.5M17.5 6.5L19 5" />
        </svg>
      )
    case 'moon':
      return (
        <svg {...svgProps} {...stroke}>
          <path d="M21 12.8A9 9 0 1111.2 3a7 7 0 009.8 9.8z" />
        </svg>
      )
    case 'folder':
      return (
        <svg {...svgProps} {...stroke}>
          <path d="M22 19a2 2 0 01-2 2H4a2 2 0 01-2-2V5a2 2 0 012-2h5l2 3h9a2 2 0 012 2z" />
        </svg>
      )
    case 'cloud':
      return (
        <svg {...svgProps} {...stroke}>
          <path d="M18 10h-1.26A8 8 0 109 20h9a5 5 0 000-10z" />
        </svg>
      )
    case 'zoom-out':
      return (
        <svg {...svgProps} {...stroke}>
          <circle cx="11" cy="11" r="7" />
          <path d="M21 21l-4.35-4.35" />
          <path d="M8 11h6" />
        </svg>
      )
    case 'sync':
    case 'refresh-cw':
      return (
        <svg {...svgProps} {...stroke}>
          <path d="M21 12a9 9 0 00-9-9 9.75 9.75 0 00-6.74 2.74L3 8" />
          <path d="M3 3v5h5" />
          <path d="M3 12a9 9 0 009 9 9.75 9.75 0 006.74-2.74L21 16" />
          <path d="M16 16h5v5" />
        </svg>
      )
    case 'shield-off':
      return (
        <svg {...svgProps} {...stroke}>
          <path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z" />
        </svg>
      )
    case 'play':
      return (
        <svg {...svgProps} fill="currentColor">
          <path d="M7 5v14l12-7z" />
        </svg>
      )
    case 'pause':
      return (
        <svg {...svgProps} fill="currentColor">
          <path d="M6 5h4v14H6zM14 5h4v14h-4z" />
        </svg>
      )
    case 'skipBack':
      return (
        <svg {...svgProps} fill="currentColor">
          <path d="M6 6h2v12H6zM20 6L10 12l10 6z" />
        </svg>
      )
    case 'skipFwd':
      return (
        <svg {...svgProps} fill="currentColor">
          <path d="M4 6l10 6L4 18zM16 6h2v12h-2z" />
        </svg>
      )
    case 'back10':
      return (
        <svg {...svgProps} {...stroke}>
          <path d="M12.5 8V5l-5 4 5 4v-3a5 5 0 11-5 5" fill="none" />
        </svg>
      )
    case 'fwd10':
      return (
        <svg {...svgProps} {...stroke}>
          <path d="M11.5 8V5l5 4-5 4v-3a5 5 0 105 5" fill="none" />
        </svg>
      )
    case 'fullscreen':
      return (
        <svg {...svgProps} {...stroke}>
          <path d="M4 9V4h5M20 9V4h-5M4 15v5h5M20 15v5h-5" />
        </svg>
      )
    case 'pip':
      return (
        <svg {...svgProps} {...stroke}>
          <path d="M4 5h16v12H4zM13 11h6v4h-6z" />
        </svg>
      )
    case 'share':
      return (
        <svg {...svgProps} {...stroke}>
          <path d="M18 8a3 3 0 10-2.8-4L9 7.5M6 12a3 3 0 100 0M18 16a3 3 0 10-2.8 4L9 16.5" />
        </svg>
      )
    case 'download':
      return (
        <svg {...svgProps} {...stroke}>
          <path d="M12 4v12m0 0l-4-4m4 4l4-4M4 20h16" />
        </svg>
      )
    case 'expand':
      return (
        <svg {...svgProps} {...stroke}>
          <path d="M4 4h6M4 4v6M20 4h-6M20 4v6M4 20h6M4 20v-6M20 20h-6M20 20v-6" />
        </svg>
      )
    case 'compress':
      return (
        <svg {...svgProps} {...stroke}>
          <path d="M9 4v5H4M15 4v5h5M9 20v-5H4M15 20v-5h5" />
        </svg>
      )
    case 'more':
      return (
        <svg {...svgProps} fill="currentColor">
          <circle cx="5" cy="12" r="1.7" />
          <circle cx="12" cy="12" r="1.7" />
          <circle cx="19" cy="12" r="1.7" />
        </svg>
      )
    case 'cpu':
      return (
        <svg {...svgProps} {...stroke}>
          <rect x="4" y="4" width="16" height="16" rx="2" />
          <path d="M9 9h6v6H9z" />
          <path d="M9 1v3M15 1v3M9 20v3M15 20v3M20 9h3M20 15h3M1 9h3M1 15h3" />
        </svg>
      )
    case 'wifi':
      return (
        <svg {...svgProps} {...stroke}>
          <path d="M5 12.55a11 11 0 0114.08 0M1.42 9a16 16 0 0121.16 0M8.53 16.11a6 6 0 016.95 0M12 20h.01" />
        </svg>
      )
    case 'thermometer':
      return (
        <svg {...svgProps} {...stroke}>
          <path d="M14 14.76V3.5a2.5 2.5 0 00-5 0v11.26a4.5 4.5 0 105 0z" />
        </svg>
      )
    case 'activity':
      return (
        <svg {...svgProps} {...stroke}>
          <path d="M22 12h-4l-3 9L9 3l-3 9H2" />
        </svg>
      )
    case 'rotate-ccw':
      return (
        <svg {...svgProps} {...stroke}>
          <path d="M3 12a9 9 0 109-9 9.75 9.75 0 00-6.74 2.74L3 8" />
          <path d="M3 3v5h5" />
        </svg>
      )
    case 'trash':
      return (
        <svg {...svgProps} {...stroke}>
          <path d="M3 6h18M19 6v14a2 2 0 01-2 2H7a2 2 0 01-2-2V6m3 0V4a2 2 0 012-2h4a2 2 0 012 2v2" />
        </svg>
      )
    case 'chevron-down':
      return (
        <svg {...svgProps} {...stroke}>
          <path d="M6 9l6 6 6-6" />
        </svg>
      )
    case 'chevron-up':
      return (
        <svg {...svgProps} {...stroke}>
          <path d="M18 15l-6-6-6 6" />
        </svg>
      )
    case 'settings':
    case 'sliders':
      return (
        <svg {...svgProps} {...stroke}>
          <path d="M4 21v-7M4 10V3M12 21v-9M12 8V3M20 21v-5M20 12V3M1 14h6M9 8h6M17 16h6" />
        </svg>
      )
    case 'zap':
      return (
        <svg {...svgProps} {...stroke}>
          <path d="M13 2L3 14h9l-1 8 10-12h-9l1-8z" />
        </svg>
      )
    case 'map-pin':
      return (
        <svg {...svgProps} {...stroke}>
          <path d="M21 10c0 7-9 13-9 13s-9-6-9-13a9 9 0 0118 0z" />
          <circle cx="12" cy="10" r="3" />
        </svg>
      )
    case 'clock':
      return (
        <svg {...svgProps} {...stroke}>
          <circle cx="12" cy="12" r="10" />
          <path d="M12 6v6l4 2" />
        </svg>
      )
    case 'disc':
      return (
        <svg {...svgProps} {...stroke}>
          <circle cx="12" cy="12" r="10" />
          <circle cx="12" cy="12" r="3" />
        </svg>
      )
    case 'server':
      return (
        <svg {...svgProps} {...stroke}>
          <rect x="2" y="2" width="20" height="8" rx="2" />
          <rect x="2" y="14" width="20" height="8" rx="2" />
          <path d="M6 6h.01M6 18h.01" />
        </svg>
      )
    case 'arrow-right':
      return (
        <svg {...svgProps} {...stroke}>
          <path d="M5 12h14M12 5l7 7-7 7" />
        </svg>
      )
    default:
      return null
  }
}
