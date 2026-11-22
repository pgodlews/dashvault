import { test, expect, type Page } from '@playwright/test'

// The tests are compiled without the DOM library, so describe what the audit reads in the page.
interface AuditElement {
  tagName: string
  id: string
  namespaceURI: string | null
  textContent: string | null
  scrollWidth: number
  clientWidth: number
  parentElement: AuditElement | null
  closest(selector: string): AuditElement | null
  getBoundingClientRect(): { left: number; right: number; width: number; height: number }
}
declare const document: {
  documentElement: AuditElement
  querySelectorAll(selector: string): Iterable<AuditElement>
}

const LONG_SSID = 'Workshop-Guest-Network-5GHz-Extended'
const LONG_CHAT = 'Alexandra Montgomery-Fitzgerald'

const now = Date.parse('2026-01-15T12:00:00Z')
const samples = Array.from({ length: 30 }, (_, i) => ({
  ts: new Date(now - (30 - i) * 60_000).toISOString(),
  cpu_temp_c: 52 + (i % 7),
  gpu_temp_c: 50 + (i % 5),
  cpu_mhz: 600 + (i % 4) * 200,
}))

const fixtures: Record<string, unknown> = {
  '/api/setup/status': { configured: true, version: 'test', model: 'test' },
  '/api/status': {
    disks: [{ mount: '/backingfiles', used_gb: 120, total_gb: 238, used_pct: 50 }],
    archive: { state: 'idle' },
    // Both warning banners, so the audit covers the tallest top of the page.
    sdcard: { latest_rating: 'slow', latest_write_mbps: 9.1, write_warning: true, total_written_gb: 2500 },
  },
  '/api/config/archive': {
    system: 'rsync',
    saved_clips: true,
    sentry_clips: true,
    recent_clips: true,
    recent_cameras: ['front', 'back'],
    recent_prune_snapshots: false,
    detected_cameras: ['front', 'back', 'left_repeater', 'right_repeater', 'left_pillar', 'right_pillar'],
    cifs: { server: 'nas.example.test', share: 'dashcam', user: 'archive', password: '', password_set: true },
    nfs: { server: 'nas.example.test', path: '/volume1/dashcam/with/a/rather/long/export/path' },
    rsync: { user: 'archive', server: 'nas.example.test', path: '/volume1/dashcam' },
    rclone: { drive: 'example-remote', path: 'dashcam' },
  },
  '/api/config/rsync/ssh-key': {
    has_key: true,
    key_type: 'ed25519',
    public_key: `ssh-ed25519 ${'AAAAC3NzaC1lZDI1NTE5'.repeat(4)} dashvault@example`,
  },
  '/api/config/telegram': {
    has_token: true,
    masked_token: '••••examples',
    chat_id: '123456789',
    silent_notify: false,
    service_enabled: true,
    service_running: true,
  },
  '/api/config/telegram/find-chats': {
    chats: [
      { id: '555000111222', type: 'private', name: LONG_CHAT, username: 'a_very_long_telegram_username', last_seen: 1_700_000_000 },
      { id: '-1001234567890', type: 'supergroup', name: 'Family car notifications group', last_seen: 1_700_000_100 },
    ],
  },
  '/api/config/snapshot-interval': { interval: 480 },
  '/api/telemetry/settings': {
    enabled: true,
    retention_days: 45,
    tile_url: 'https://tile.openstreetmap.org/{z}/{x}/{y}.png',
    style_url: 'http://maps.example.test:8090/styles/basic-preview/style.json',
    while_driving: true,
  },
  '/api/telemetry/days': { days: [{ date: '2026-01-15' }], total_clips: 176, total_bytes: 700_000, retention_days: 45 },
  '/api/update/status': {
    ota_enabled: true,
    release_url_configured: true,
    current_version: 'v0.0.1',
    latest_version: 'v0.0.2',
    update_available: true,
    rollback_available: true,
    last_checked: new Date(now).toISOString(),
  },
  '/api/wifi/networks': {
    connected_via_wifi: true,
    networks: [
      { ssid: LONG_SSID, uuid: 'a', active: true },
      { ssid: 'Home', uuid: 'b', active: false },
    ],
  },
  '/api/wifi/scan': [
    { ssid: LONG_SSID, signal: 80, security: 'WPA2 WPA3', saved: true },
    { ssid: 'Neighbour-Network-With-A-Long-Name-5G', signal: 40, security: 'WPA2', saved: false },
  ],
  '/api/power/config': {
    config: {
      temp_logging_enabled: true,
      power_profile: 'auto',
      dynamic_throttle_enabled: true,
      warn_temp_c: 70,
      crit_temp_c: 80,
      hysteresis_c: 5,
    },
    capabilities: {
      available_governors: ['powersave', 'conservative', 'performance'],
      available_frequencies_mhz: [600, 1000, 1400],
      min_freq_mhz: 600,
      max_freq_mhz: 1400,
      mid_freq_mhz: 1000,
      has_devfreq: false,
      has_gpu_temp: true,
    },
    status: { cpu_mhz: 1000, governor: 'conservative', gpu_temp_c: 51.5, thermal_state: 'stage1_warn' },
  },
  '/api/power/history': {
    temp_logging_enabled: true,
    sample_count: samples.length,
    min_temp_c: 50,
    max_temp_c: 58,
    avg_temp_c: 54,
    samples,
  },
  '/api/benchmark/status': {
    running: false,
    results: [
      { timestamp: new Date(now).toISOString(), write_mbps: 31.4, rating: 'good', timed_out: false, size_mb: 160, elapsed_ms: 5000 },
      { timestamp: new Date(now - 86_400_000).toISOString(), write_mbps: null, rating: 'slow', timed_out: true, size_mb: 160, elapsed_ms: 60000 },
    ],
    card_info: { available: true, name: 'EXAMPLE-CARD', manufacturer: 'Example Memory', capacity_gb: 256, date: '2025-06' },
    write_stats: { total_bytes_written: 2_500_000_000_000, tracking_since: new Date(now).toISOString(), last_updated: new Date(now).toISOString() },
    threshold_tb: 128,
    default_threshold_tb: 128,
  },
}

async function mockApi(page: Page) {
  await page.route('**/api/**', route => {
    const path = new URL(route.request().url()).pathname
    if (path === '/api/status/stream') {
      return route.fulfill({
        contentType: 'text/event-stream',
        body: `data: ${JSON.stringify(fixtures['/api/status'])}\n\n`,
      })
    }
    if (path === '/api/update/log') return route.fulfill({ body: `applying ${'/opt/dashvault/release/file-name-'.repeat(6)}` })
    const body = fixtures[path]
    return body === undefined ? route.abort() : route.fulfill({ json: body })
  })
}

async function openSettings(page: Page, width: number) {
  await page.setViewportSize({ width, height: 844 })
  await mockApi(page)
  await page.goto('/new/settings')
  await expect(page.locator('#advanced-sd').getByText('Example Memory EXAMPLE-CARD')).toBeVisible()
  await expect(page.locator('#power svg').first()).toBeVisible()
  await expect(page.getByText('archive idle')).toBeVisible()
}

/** Elements that stick out of the page or get cut off by their Settings card. */
async function overflowReport(page: Page) {
  return page.evaluate(() => {
    const viewport = document.documentElement.clientWidth
    const problems: string[] = []
    // The two menus are sideways strips on purpose, so their items may run past the edge.
    const scrollsSideways = (el: AuditElement) => el.parentElement?.closest('[data-scroll-x]') != null
    const insideSvg = (el: AuditElement) => el.namespaceURI?.endsWith('/svg') && el.tagName !== 'svg'
    const label = (el: AuditElement) => {
      const text = (el.textContent ?? '').trim().slice(0, 30)
      return `${el.tagName.toLowerCase()}${el.id ? `#${el.id}` : ''} "${text}"`
    }
    if (document.documentElement.scrollWidth > viewport) {
      problems.push(`page is ${document.documentElement.scrollWidth}px wide in a ${viewport}px viewport`)
    }
    for (const el of document.querySelectorAll('#root *')) {
      if (insideSvg(el) || el.tagName === 'STYLE' || scrollsSideways(el)) continue
      const box = el.getBoundingClientRect()
      if (box.width === 0 || box.height === 0) continue
      if (box.right > viewport + 0.5 || box.left < -0.5) problems.push(`${label(el)} leaves the viewport`)
      const card = el.closest('section')
      if (card && el !== card) {
        const edge = card.getBoundingClientRect()
        if (box.right > edge.right + 0.5) problems.push(`${label(el)} is clipped by #${card.id}`)
      }
    }
    for (const scroller of document.querySelectorAll('main')) {
      if (scroller.scrollWidth > scroller.clientWidth + 1) problems.push('a main area scrolls sideways')
    }
    return [...new Set(problems)]
  })
}

/** Open everything that adds content, so the audit sees each section's fullest state. */
async function expandSections(page: Page) {
  const network = page.locator('#network')
  await network.getByRole('button', { name: 'Scan for networks' }).click()
  await expect(network.getByText('Nearby networks')).toBeVisible()
  await network.getByRole('button', { name: '+ Add manually' }).click()
  await page.locator('#advanced-ota').getByRole('button', { name: /Show update log/ }).click()
  await page.locator('#archive').getByRole('button', { name: 'Import existing private key' }).click()
  const telegram = page.locator('#notifications')
  await telegram.getByRole('button', { name: 'Find my chat ID' }).click()
  await expect(telegram.getByRole('button', { name: 'Use this chat' }).first()).toBeVisible()
}

for (const width of [360, 390, 768]) {
  test(`Settings fits a ${width}px screen with every section open`, async ({ page }) => {
    await openSettings(page, width)
    await expandSections(page)
    expect(await overflowReport(page)).toEqual([])

    for (const id of ['archive', 'notifications', 'advanced-telemetry', 'network', 'power', 'advanced-sd']) {
      const box = await page.locator(`#${id}`).boundingBox()
      expect(box!.width, `#${id} width`).toBeGreaterThan(width - 60)
    }
  })

  test(`every archive backend fits a ${width}px screen`, async ({ page }) => {
    await openSettings(page, width)
    const archive = page.locator('#archive')
    for (const name of ['CIFS/SMB', 'NFS', 'rsync', 'rclone', 'None']) {
      await archive.getByRole('button', { name: new RegExp(`^${name}`) }).click()
      expect(await overflowReport(page), name).toEqual([])
    }
  })
}

test('the Telegram chat list can be used on a phone', async ({ page }) => {
  await openSettings(page, 360)
  const card = page.locator('#notifications')
  await card.getByRole('button', { name: 'Find my chat ID' }).click()

  const row = card.getByRole('listitem').filter({ hasText: LONG_CHAT })
  const button = row.getByRole('button', { name: 'Use this chat' })
  await button.scrollIntoViewIfNeeded()
  const box = await button.boundingBox()
  expect(box!.x + box!.width).toBeLessThanOrEqual(360)
  expect(box!.height).toBeGreaterThanOrEqual(40)

  await button.click()
  await expect(card.getByLabel('Chat ID')).toHaveValue('123456789, 555000111222')
})

test('the section menu jumps to a section on a phone', async ({ page }) => {
  await openSettings(page, 390)
  await page.getByRole('navigation', { name: 'Settings sections' }).getByText('Danger Zone').click()
  await expect(page.locator('#danger')).toBeInViewport()
})

for (const width of [390, 1440]) {
  test(`the section menu highlights the section on screen at ${width}px`, async ({ page }) => {
    await openSettings(page, width)
    const menu = page.getByRole('navigation', { name: 'Settings sections' })
    const current = menu.locator('[aria-current="true"]')

    for (const label of ['Network & Wi-Fi', 'Power & Thermal', 'Advanced', 'Notifications', 'Danger Zone', 'Archive storage']) {
      await menu.getByText(label, { exact: true }).click()
      await expect(current).toHaveText(label)
    }

    // These cards have no menu entry of their own and belong under Advanced.
    for (const id of ['advanced-telemetry', 'advanced-ota', 'advanced-usb', 'advanced-sd']) {
      await page.locator(`#${id}`).evaluate(el => el.scrollIntoView({ block: 'start', behavior: 'instant' }))
      await expect(current, id).toHaveText('Advanced')
    }
  })
}

test('the desktop layout keeps its sidebar and single-row top bar', async ({ page }) => {
  await openSettings(page, 1440)
  const sidebar = await page.getByRole('navigation', { name: 'Settings sections' }).boundingBox()
  expect(sidebar!.width).toBe(220)
  const topBar = await page.getByRole('navigation', { name: 'Main' }).boundingBox()
  expect(topBar!.height).toBe(48)

  const archive = page.locator('#archive')
  const nfs = await archive.getByRole('button', { name: /^NFS/ }).boundingBox()
  const cifs = await archive.getByRole('button', { name: /^CIFS/ }).boundingBox()
  expect(nfs!.y).toBe(cifs!.y)
  expect(await overflowReport(page)).toEqual([])
})
