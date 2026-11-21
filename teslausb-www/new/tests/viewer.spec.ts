import { test, expect } from '@playwright/test'
import path from 'node:path'
import { readFileSync } from 'node:fs'

const first = '2026-11-07_12-00-00'
const last = '2026-11-07_12-01-00'
const angles = ['front', 'back', 'left_repeater', 'right_repeater']

test.beforeEach(async ({ page }) => {
  await page.route('**/cgi-bin/ls.sh?**', async route => {
    const query = decodeURIComponent(new URL(route.request().url()).search)
    const body = query === '?TeslaCam/'
      ? `D:SavedClips/${first}\nD:SentryClips/${first}\n`
      : [first, last].flatMap(time => angles.filter(angle => time !== last || angle !== 'front').map(angle => `f:${time}-${angle}.mp4:3000`)).join('\n')
    await route.fulfill({ body, contentType: 'text/plain' })
  })
  await page.route('**/cgi-bin/download.sh?**', route => {
    const data = readFileSync(path.join(import.meta.dirname, 'fixtures/camera.mp4'))
    const range = /bytes=(\d+)-(\d*)/.exec(route.request().headers()['range'] ?? '')
    const start = range ? Number(range[1]) : 0
    const end = range?.[2] ? Math.min(Number(range[2]), data.length - 1) : data.length - 1
    return route.fulfill({
      status: range ? 206 : 200,
      body: data.subarray(start, end + 1),
      contentType: 'video/mp4',
      headers: {
        'Accept-Ranges': 'bytes',
        ...(range ? { 'Content-Range': `bytes ${start}-${end}/${data.length}` } : {}),
      },
    })
  })
  await page.route('**/api/**', route => route.fulfill({ status: 404, body: '' }))
})

test('folder playback exposes every synchronized minute, including Sentry', async ({ page }) => {
  await page.goto('/new/viewer')
  await page.getByText(first, { exact: true }).last().click()
  await expect(page.getByLabel('Minute')).toHaveValue(first)
  await expect(page.locator('video')).toHaveCount(4)
  await page.getByLabel('Minute').selectOption(last)
  await expect(page.locator('video')).toHaveCount(3)
  for (const src of await page.locator('video').evaluateAll(videos => videos.map(video => video.getAttribute('src')))) {
    expect(src).toContain(last)
    expect(src).not.toContain(first + '-')
  }
  await page.getByLabel('Minute').selectOption(first)
  await expect(page.locator('video')).toHaveCount(4)
})

for (const [label, angle] of [['Front', 'front'], ['Back', 'back'], ['Left', 'left_repeater'], ['Right', 'right_repeater']]) {
  test(`Focus ${label} preserves playback and working pause/seek controls`, async ({ page }) => {
    await page.goto(`/new/viewer?clip=SavedClips/${first}/${first}-front.mp4`)
    await expect(page.locator('video')).toHaveCount(4)
    await expect.poll(() => page.locator('video').first().evaluate(video => video.readyState)).toBeGreaterThan(0)
    await page.getByTitle('Forward 10s').click()
    await page.getByRole('button', { name: '2×', exact: true }).click()
    await page.getByTitle('Play/pause (space)').click()
    await expect.poll(() => page.locator('video').first().evaluate(video => video.currentTime)).toBeGreaterThan(10)
    await page.getByRole('button', { name: /Focus/ }).click()
    await page.getByLabel('Focus camera').selectOption(angle)
    const focused = page.locator(`video[src*="-${angle}.mp4"]`).first()
    await expect(focused).toBeVisible()
    await expect.poll(() => focused.evaluate(video => video.currentTime)).toBeGreaterThan(10)
    await expect.poll(() => focused.evaluate(video => video.paused)).toBe(false)
    await expect.poll(() => focused.evaluate(video => video.playbackRate)).toBe(2)
    await page.keyboard.press('Space')
    await expect.poll(() => page.locator('video').evaluateAll(videos => videos.every(video => video.paused))).toBe(true)
    await page.getByTitle('Forward 10s').click()
    await expect.poll(() => focused.evaluate(video => video.currentTime)).toBeGreaterThan(19)
    await page.getByTitle('Play/pause (space)').click()
    await expect.poll(() => focused.evaluate(video => video.paused)).toBe(false)
    await page.getByTitle('Play/pause (space)').click()
    await expect.poll(() => page.locator('video').evaluateAll(videos => videos.every(video => video.paused))).toBe(true)
  })
}

test('Expand selects the camera and immediately switches Classic to Focus', async ({ page }) => {
  await page.goto(`/new/viewer?clip=SavedClips/${first}/${first}-front.mp4`)
  await expect(page.locator('video')).toHaveCount(4)
  const backTile = page.locator('video[src*="-back.mp4"]').locator('..')
  await backTile.hover()
  await backTile.getByTitle('Expand').click({ force: true })
  await expect(page.locator('video[src*="-front.mp4"]')).toHaveCount(0)
  await expect(page.locator('video[src*="-back.mp4"]')).toHaveCount(2)
})

test('minute deep links select their timestamp and still allow earlier footage', async ({ page }) => {
  await page.goto(`/new/viewer?clip=SavedClips/${first}/${last}-back.mp4`)
  await expect(page.getByLabel('Minute')).toHaveValue(last)
  await expect(page.locator('video')).toHaveCount(3)
  await page.getByLabel('Minute').selectOption(first)
  await expect(page.locator('video')).toHaveCount(4)
})

/** The <video> properties the actual-size tests read (the test tsconfig has no DOM types). */
type Video = { videoWidth: number, videoHeight: number, marker?: string }

test('clicking a camera shows it at its actual resolution, Esc and the backdrop return to the grid', async ({ page }) => {
  await page.goto(`/new/viewer?clip=SavedClips/${first}/${first}-front.mp4`)
  const tile = page.getByTestId('camera-tile-back')
  const video = tile.locator('video')
  await expect.poll(() => video.evaluate(v => (v as unknown as Video).videoWidth)).toBeGreaterThan(0)
  const [width, height] = await video.evaluate(v => {
    const element = v as unknown as Video
    element.marker = 'same element'
    return [element.videoWidth, element.videoHeight]
  })
  const before = await tile.boundingBox()

  await video.click()
  await expect(page.getByTestId('actual-size-backdrop')).toBeVisible()
  await expect(tile).toContainText(`${width}×${height} · 100%`)
  const box = await tile.boundingBox()
  expect(Math.round(box?.width ?? 0)).toBe(width)
  expect(Math.round(box?.height ?? 0)).toBe(height)
  expect(box?.width).not.toBe(before?.width)
  // The same element moved, so it stays registered for synchronised playback.
  expect(await video.evaluate(v => (v as unknown as Video).marker)).toBe('same element')
  await expect(page.locator('video')).toHaveCount(4)

  await page.keyboard.press('Escape')
  await expect(page.getByTestId('actual-size-backdrop')).toHaveCount(0)
  await expect(tile).not.toContainText('×')

  await video.click()
  await page.getByTestId('actual-size-backdrop').click({ position: { x: 5, y: 5 } })
  await expect(page.getByTestId('actual-size-backdrop')).toHaveCount(0)
})

test('a camera larger than the window is scaled down to fit', async ({ page }) => {
  // The 160x90 fixture is larger than a 200px-wide window minus the 32px margins.
  await page.setViewportSize({ width: 200, height: 400 })
  await page.goto(`/new/viewer?clip=SavedClips/${first}/${first}-front.mp4`)
  const tile = page.getByTestId('camera-tile-front')
  const video = tile.locator('video')
  await expect.poll(() => video.evaluate(v => (v as unknown as Video).videoWidth)).toBeGreaterThan(0)
  const width = await video.evaluate(v => (v as unknown as Video).videoWidth)
  expect(width).toBeGreaterThan(200 - 64)
  await video.dispatchEvent('click') // at 200px other panels overlap the tile
  const box = await tile.boundingBox()
  expect(Math.round(box?.width ?? 0)).toBe(200 - 64)
  await expect(tile).toContainText(`${width}×`)
  await expect(tile).toContainText(`${Math.round(((200 - 64) / width) * 100)}%`)
})

test('dragging the map dot along the track seeks every camera and resumes playback', async ({ page }) => {
  // A straight 10-second drive north: t seconds into the clip is t * 55 m up the line.
  const point = (t: number) => ({
    t, lat: 53.8 + t * 0.0005, lon: -9.55, speed: 13, heading: 0, gear: 1, autopilot: 0,
    accel_x: 0, accel_y: 0, accel_z: 0, accelerator: 10, brake: false, steering: 0,
    blinker_left: false, blinker_right: false,
  })
  await page.route('**/tile.openstreetmap.org/**', route => route.fulfill({ status: 204, body: '' }))
  await page.route('**/api/telemetry/clip?**', route => route.fulfill({
    json: {
      id: `SavedClips/${first}/${first}-front.mp4`, recorded_at: '2026-11-07T12:00:00', state: 'available',
      front_file: `${first}-front.mp4`, duration: 10, samples: 11, extended: true,
      tile_url: 'https://tile.openstreetmap.org/{z}/{x}/{y}.png',
      track: Array.from({ length: 11 }, (_, t) => point(t)),
    },
  }))
  await page.goto(`/new/viewer?clip=SavedClips/${first}/${first}-front.mp4`)
  await expect(page.locator('video')).toHaveCount(4)
  const dot = page.locator('.teslausb-scrub-dot')
  await expect(dot).toBeVisible()
  const route = page.locator('.leaflet-overlay-pane path.leaflet-interactive').first()
  const line = await route.boundingBox()
  if (!line) throw new Error('track not drawn')

  await page.getByTitle('Play/pause (space)').click()
  await expect.poll(() => page.locator('video').evaluateAll(videos => videos.every(video => !video.paused))).toBe(true)

  const start = await dot.boundingBox()
  if (!start) throw new Error('dot not laid out')
  await page.mouse.move(start.x + start.width / 2, start.y + start.height / 2)
  await page.mouse.down()
  // Off to the side of the middle of the line: the dot snaps back onto it.
  await page.mouse.move(line.x + line.width / 2 + 40, line.y + line.height / 2, { steps: 12 })
  await expect.poll(() => page.locator('video').evaluateAll(videos => videos.every(video => video.paused))).toBe(true)
  await page.mouse.up()

  const times = await page.locator('video').evaluateAll(videos => videos.map(video => video.currentTime))
  for (const time of times) {
    expect(time).toBeGreaterThan(4)
    expect(time).toBeLessThan(6)
  }
  const snapped = await dot.boundingBox()
  expect(Math.abs((snapped?.x ?? 0) + (snapped?.width ?? 0) / 2 - (line.x + line.width / 2))).toBeLessThan(6)
  // Playing before the drag, so it plays again after it.
  await expect.poll(() => page.locator('video').evaluateAll(videos => videos.every(video => !video.paused))).toBe(true)
})

test('the actual-size view has playback controls and a telemetry overlay', async ({ page }) => {
  const point = (t: number) => ({
    t, lat: 53.8 + t * 0.0005, lon: -9.55, speed: 20, heading: 90, gear: 1, autopilot: 2,
    accel_x: 0, accel_y: 0, accel_z: 0, accelerator: 30, brake: false, steering: 0,
    blinker_left: false, blinker_right: false,
  })
  await page.route('**/tile.openstreetmap.org/**', route => route.fulfill({ status: 204, body: '' }))
  await page.route('**/api/telemetry/clip?**', route => route.fulfill({
    json: {
      id: `SavedClips/${first}/${first}-front.mp4`, recorded_at: '2026-11-07T12:00:00', state: 'available',
      front_file: `${first}-front.mp4`, duration: 30, samples: 31, extended: true,
      tile_url: 'https://tile.openstreetmap.org/{z}/{x}/{y}.png',
      track: Array.from({ length: 31 }, (_, t) => point(t)),
    },
  }))
  await page.goto(`/new/viewer?clip=SavedClips/${first}/${first}-front.mp4`)
  const tile = page.getByTestId('camera-tile-front')
  const video = tile.locator('video')
  await expect.poll(() => video.evaluate(v => (v as unknown as Video).videoWidth)).toBeGreaterThan(0)
  await video.click()

  const controls = tile.getByTestId('actual-size-controls')
  await expect(controls).toBeVisible()
  // Compact telemetry: speed and Autopilot, without the acceleration rows.
  const telemetry = tile.getByTestId('actual-size-telemetry')
  await controls.getByRole('button', { name: 'Play' }).click()
  await expect(telemetry).toContainText('mph')
  await expect(telemetry).toContainText('AP: Autosteer')
  await expect(telemetry).not.toContainText('Linear acceleration')
  // Every camera plays, not only the enlarged one.
  await expect.poll(() => page.locator('video').evaluateAll(videos => videos.every(v => !v.paused))).toBe(true)
  await controls.getByRole('button', { name: 'Pause' }).click()
  await expect.poll(() => page.locator('video').evaluateAll(videos => videos.every(v => v.paused))).toBe(true)
  const before = await video.evaluate(v => (v as unknown as { currentTime: number }).currentTime)
  await controls.getByRole('button', { name: 'Forward 5 seconds' }).click()
  const times = await page.locator('video').evaluateAll(videos => videos.map(v => v.currentTime))
  for (const time of times) expect(time).toBeCloseTo(before + 5, 0)
  // Clicking the controls does not close the view; the bar fades while playing and returns on mouse movement.
  await expect(page.getByTestId('actual-size-backdrop')).toBeVisible()
  await controls.getByRole('button', { name: 'Play' }).click()
  await page.mouse.move(5, 5)
  await expect(controls).toHaveCSS('opacity', '0', { timeout: 6000 })
  const box = await tile.boundingBox()
  await page.mouse.move((box?.x ?? 0) + 40, (box?.y ?? 0) + 40)
  await expect(controls).toHaveCSS('opacity', '1')
  await page.keyboard.press('Escape')
  await expect(controls).toHaveCount(0)
})

test('a link with t opens the minute at that moment; another minute starts at 0', async ({ page }) => {
  await page.goto(`/new/viewer?clip=SavedClips/${first}/${first}-front.mp4&t=12`)
  await expect(page.locator('video')).toHaveCount(4)
  await expect.poll(() => page.locator('video').evaluateAll(videos => videos.map(video => Math.round(video.currentTime)))).toEqual([12, 12, 12, 12])
  await page.getByLabel('Minute').selectOption(last)
  await expect(page.locator('video')).toHaveCount(3)
  await expect.poll(() => page.locator('video').evaluateAll(videos => videos.every(video => video.readyState > 0 && video.currentTime < 1))).toBe(true)
})
