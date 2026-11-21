import { test, expect, type Page } from '@playwright/test'

/** Local "YYYY-MM-DDTHH:MM", as the page sends it. */
function local(date: Date): string {
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`
}

const now = new Date()
const yesterday = new Date(now.getTime() - 24 * 3_600_000)
const day = local(yesterday).slice(0, 10)

async function mockApi(page: Page): Promise<string[]> {
  const trackRequests: string[] = []
  await page.route('**/tile.openstreetmap.org/**', route => route.fulfill({ status: 204, body: '' }))
  await page.route('**/api/**', async route => {
    const url = new URL(route.request().url())
    if (url.pathname.endsWith('/api/telemetry/days')) {
      const hours = Array.from({ length: 24 }, (_, hour) => (hour === 8 ? 42 : 0))
      return route.fulfill({
        json: {
          days: [{ day, clips: 2, driving_seconds: 2520, first: `${day}T08:00:00`, last: `${day}T08:41:00`, hour_minutes: hours, saved: 1, recent: 1 }],
          total_clips: 2, total_bytes: 12345, retention_days: 30,
        },
      })
    }
    if (url.pathname.endsWith('/api/telemetry/tracks') && url.searchParams.get('full') === '1') {
      trackRequests.push(url.search)
      const point = (t: number, lat: number) => ({
        t, lat, lon: -9.55, speed: 13.4, heading: 90, gear: 1, autopilot: 0, accel_x: 0, accel_y: 0, accel_z: 0,
        accelerator: 20, brake: false, steering: 0, blinker_left: false, blinker_right: false,
      })
      return route.fulfill({
        json: {
          enabled: true, retention_days: 30, tile_url: '', style_url: '',
          clips: [
            { id: `RecentClips/${day}/${day}_08-00-00-front.mp4`, recorded_at: `${day}T08:00:00`, state: 'archived',
              front_file: '', duration: 59, samples: 60, extended: true, track: Array.from({ length: 60 }, (_, t) => point(t, 53.79 + t * 0.0001)) },
            { id: `RecentClips/${day}/${day}_08-01-00-front.mp4`, recorded_at: `${day}T08:01:00`, state: 'available',
              front_file: '', duration: 59, samples: 60, extended: true, track: Array.from({ length: 60 }, (_, t) => point(t, 53.80 + t * 0.0001)) },
          ],
        },
      })
    }
    if (url.pathname.endsWith('/api/telemetry/tracks')) {
      trackRequests.push(url.search)
      return route.fulfill({
        json: {
          enabled: true, retention_days: 30, tile_url: 'https://tile.openstreetmap.org/{z}/{x}/{y}.png', style_url: '',
          clips: [
            { id: `SavedClips/${day}_08-10-00/${day}_08-09-00-front.mp4`, recorded_at: `${day}T08:09:00`, state: 'available',
              front_file: `${day}_08-09-00-front.mp4`, duration: 59, samples: 59, extended: true, line: [[53.80, -9.55], [53.81, -9.54]] },
            { id: `RecentClips/${day}/${day}_08-00-00-front.mp4`, recorded_at: `${day}T08:00:00`, state: 'archived',
              front_file: `${day}_08-00-00-front.mp4`, duration: 59, samples: 59, extended: true, line: [[53.79, -9.56], [53.80, -9.55]] },
            { id: `RecentClips/${day}/${day}_08-01-00-front.mp4`, recorded_at: `${day}T08:01:00`, state: 'available',
              front_file: `${day}_08-01-00-front.mp4`, duration: 59, samples: 59, extended: true, line: [[53.80, -9.55], [53.806, -9.55]] },
          ],
        },
      })
    }
    return route.fulfill({ status: 404, body: '' })
  })
  return trackRequests
}

test('the map loads the last 7 days by default, as simplified lines', async ({ page }) => {
  const requests = await mockApi(page)
  await page.goto('/new/map')
  await expect(page.getByText('Last 7 days · 3 tracks')).toBeVisible()
  await expect(page.getByRole('button', { name: /Saved · 1/ })).toBeVisible()
  const from = new URLSearchParams(requests[0]).get('from') ?? ''
  const expected = new Date(now.getTime() - 7 * 24 * 3_600_000)
  expect(Math.abs(new Date(from).getTime() - expected.getTime())).toBeLessThan(5 * 60_000)
  await expect(page.locator('.leaflet-interactive').first()).toBeVisible()
})

test('a preset changes the URL and the request', async ({ page }) => {
  const requests = await mockApi(page)
  await page.goto('/new/map')
  await expect(page.getByText('Last 7 days · 3 tracks')).toBeVisible()
  await page.getByLabel('Time range', { exact: true }).selectOption('24h')
  await expect(page).toHaveURL(/range=24h/)
  const lastFrom = () => new Date(new URLSearchParams(requests.at(-1) ?? '').get('from') ?? '').getTime()
  await expect.poll(() => Math.abs(lastFrom() - yesterday.getTime())).toBeLessThan(5 * 60_000)
  await expect(page.getByText('Last 24 hours · 3 tracks')).toBeVisible()
})

test('dragging across the timeline selects a shorter range', async ({ page }) => {
  const requests = await mockApi(page)
  await page.goto('/new/map?range=24h')
  const timeline = page.getByTestId('timeline')
  await expect(timeline).toBeVisible()
  const box = await timeline.boundingBox()
  if (!box) throw new Error('timeline not laid out')
  await page.mouse.move(box.x + box.width * 0.25, box.y + 15)
  await page.mouse.down()
  await page.mouse.move(box.x + box.width * 0.5, box.y + 15, { steps: 5 })
  await page.mouse.up()
  await expect(page).toHaveURL(/from=.*&to=/)
  const params = new URL(page.url()).searchParams
  const span = new Date(params.get('to') ?? '').getTime() - new Date(params.get('from') ?? '').getTime()
  expect(span).toBeGreaterThan(5 * 3_600_000)
  expect(span).toBeLessThan(7 * 3_600_000)
  await expect.poll(() => requests.at(-1)).toContain(encodeURIComponent(params.get('from') ?? ''))

  // Back returns to the previous range: the range lives in the URL.
  await page.goBack()
  await expect(page).toHaveURL(/range=24h/)

  await page.getByRole('button', { name: 'Zoom out time range' }).click()
  await expect(page).toHaveURL(/from=/)
})

test('trips group nearby clips and replay plays one back', async ({ page }) => {
  const requests = await mockApi(page)
  await page.goto('/new/map')
  // 08:00 and 08:01 are one drive; the Saved clip at 08:09 starts 8 minutes later.
  await expect(page.getByRole('button', { name: 'Trips · 2' })).toBeVisible()
  const morning = page.getByRole('button', { name: /08:00( AM)?–08:01/ })
  await expect(morning).toContainText('2 min')
  await morning.click()
  await expect(page).toHaveURL(new RegExp(`trip=${day}T08%3A00%3A00`))
  await page.getByRole('button', { name: 'Replay trip' }).click()
  const replay = page.getByTestId('trip-replay')
  await expect(replay.getByText('Speed')).toBeVisible()
  expect(requests.at(-1)).toContain('full=1')
  await expect(replay.getByRole('link', { name: "Open this minute's video" })).toHaveCount(0) // 08:00 is archived
  await replay.getByLabel('Replay speed').selectOption('60')
  await replay.getByRole('button', { name: 'Play replay' }).click()
  await expect.poll(() => replay.getByLabel('Replay position').inputValue().then(Number)).toBeGreaterThan(60)
  // Past the first minute the available 08:01 clip links to its video.
  await expect(replay.getByRole('link', { name: "Open this minute's video" })).toBeVisible()
  await expect(page.locator('.teslausb-replay-cursor')).toHaveCount(1)
  await replay.getByRole('button', { name: 'Close replay' }).click()
  await expect(page.locator('.teslausb-replay-cursor')).toHaveCount(0)
  await page.getByRole('button', { name: 'Show all trips' }).click()
  await expect(page).not.toHaveURL(/trip=/)
})

test('look around: click the map, see every camera, step along the road', async ({ page }) => {
  await mockApi(page)
  const nearRequests: string[] = []
  let ffmpeg = false
  let installs = 0
  const pass = (id: string, at: string, t: number, cameras: string[]) => ({
    id, recorded_at: at.slice(0, 16) + ':00', at, t, duration: 59, distance_m: 12, speed_kmh: 40, heading: 90,
    lat: 53.8, lon: -9.55, saved: false, cameras,
  })
  await page.route('**/api/street/near?**', route => {
    nearRequests.push(route.request().url())
    return route.fulfill({
      json: {
        lat: 53.8, lon: -9.55, radius: 60, ffmpeg,
        passes: [
          pass(`RecentClips/${day}/${day}_08-01-00-front.mp4`, `${day}T08:01:30`, 30, ['front', 'back', 'left_repeater', 'right_repeater']),
          pass(`RecentClips/${day}/${day}_07-00-00-front.mp4`, `${day}T07:00:10`, 10, []),
        ],
      },
    })
  })
  await page.route('**/api/street/install-ffmpeg', route => {
    installs += 1
    ffmpeg = true
    return route.fulfill({ json: { installed: true } })
  })
  await page.route('**/api/street/frame?**', route => route.fulfill({ status: 200, contentType: 'image/jpeg', body: Buffer.from('jpeg') }))
  await page.route('**/api/telemetry/clip?**', route => route.fulfill({ status: 404, body: '' }))

  await page.goto('/new/map')
  await expect(page.getByText('Last 7 days · 3 tracks')).toBeVisible()
  await page.getByRole('button', { name: 'Look around' }).click()
  await expect(page.getByText('Click the map where you want to look around')).toBeVisible()
  const map = page.getByTestId('telemetry-map')
  const box = await map.boundingBox()
  if (!box) throw new Error('map not laid out')
  await page.mouse.click(box.x + box.width / 2, box.y + box.height / 2)
  await expect(page).toHaveURL(/look=-?\d+\.\d+%2C-?\d+\.\d+/)
  await expect.poll(() => nearRequests.length).toBeGreaterThan(0)
  expect(Number(new URL(nearRequests[0]).searchParams.get('radius'))).toBeGreaterThanOrEqual(25)

  const panel = page.getByTestId('look-around')
  await expect(panel).toContainText('2 passes within 60 m')
  // No ffmpeg yet: one click installs it, then the stills load.
  await panel.getByRole('button', { name: 'Install ffmpeg' }).click()
  const images = panel.getByTestId('look-around-cameras').locator('img')
  await expect(images).toHaveCount(4)
  expect(installs).toBe(1)
  const sources = await images.evaluateAll(list => list.map(img => img.getAttribute('src') ?? ''))
  expect(sources.map(src => new URLSearchParams(src.split('?')[1]).get('camera'))).toEqual(['left_repeater', 'front', 'right_repeater', 'back'])
  expect(sources.every(src => src.includes('t=30.0') && src.includes('w=640'))).toBe(true)

  // Arrow keys and buttons move along the road.
  await page.keyboard.press('ArrowRight')
  await expect(images.first()).toHaveAttribute('src', /t=31\.0/)
  await page.keyboard.press('Shift+ArrowLeft')
  await expect(images.first()).toHaveAttribute('src', /t=26\.0/)
  await panel.getByRole('button', { name: 'Forward 5 seconds' }).click()
  await expect(images.first()).toHaveAttribute('src', /t=31\.0/)

  // Open video goes to this moment of the minute; Street View opens Google in a new tab.
  await expect(panel.getByRole('link', { name: 'Open video' })).toHaveAttribute('href', /clip=RecentClips.*08-01-00-front\.mp4.*&t=31/)
  const streetView = panel.getByRole('link', { name: 'Street View ↗' })
  await expect(streetView).toHaveAttribute('target', '_blank')
  await expect(streetView).toHaveAttribute('rel', /noopener/)
  const href = new URL((await streetView.getAttribute('href')) ?? '')
  expect(href.origin + href.pathname).toBe('https://www.google.com/maps/@')
  expect(href.searchParams.get('map_action')).toBe('pano')
  expect(href.searchParams.get('viewpoint')).toBe('53.800000,-9.550000')
  expect(href.searchParams.get('heading')).toBe('90')

  // An older pass whose video is gone says so instead of showing pictures.
  await panel.getByLabel('Pass').selectOption('1')
  await expect(panel).toContainText('no longer on the Pi')
  await expect(images).toHaveCount(0)

  await panel.getByRole('button', { name: 'Close look around' }).click()
  await expect(page).not.toHaveURL(/look=/)
})
