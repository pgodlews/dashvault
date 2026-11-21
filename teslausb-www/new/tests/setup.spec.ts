import { test, expect, type Page } from '@playwright/test'

const networks = [
  { ssid: 'Home', signal: 80, security: 'WPA2' },
  { ssid: 'Other', signal: 40, security: 'WPA2' },
]

async function mockApi(page: Page, { configured = false, ssid = null as string | null } = {}) {
  const calls = { scans: 0 }
  await page.route('**/api/**', route => {
    const path = new URL(route.request().url()).pathname
    if (path.endsWith('/api/setup/status')) {
      return route.fulfill({ json: { configured, version: 'test', model: 'test' } })
    }
    if (path.endsWith('/api/status/stream')) {
      return route.fulfill({ status: 204, body: '' })
    }
    if (path.endsWith('/api/status')) {
      return route.fulfill({ json: { disks: [], archive: { state: 'idle' }, ...(ssid ? { wifi: { ssid } } : {}) } })
    }
    if (path.endsWith('/api/wifi/scan')) {
      calls.scans++
      return route.fulfill({ json: networks })
    }
    return route.fulfill({ json: {} })
  })
  return calls
}

test('a device already on Wi-Fi can skip the network step', async ({ page }) => {
  const calls = await mockApi(page, { ssid: 'Home' })
  await page.goto('/new/setup')

  await expect(page.getByText('Connected to “Home”')).toBeVisible()
  expect(calls.scans).toBe(0)

  await page.getByRole('button', { name: 'Continue →' }).click()
  await expect(page.getByRole('combobox')).toHaveValue('none')
})

test('a connected device can still choose another network', async ({ page }) => {
  const calls = await mockApi(page, { ssid: 'Home' })
  await page.goto('/new/setup')

  await page.getByText('Use a different network').click()
  await expect(page.getByText('Other', { exact: true })).toBeVisible()
  expect(calls.scans).toBe(1)

  await page.getByText('Keep “Home”').click()
  await expect(page.getByText('Connected to “Home”')).toBeVisible()
})

test('a device with no Wi-Fi sees the network list', async ({ page }) => {
  const calls = await mockApi(page)
  await page.goto('/new/setup')

  await expect(page.getByText('Connect to your network')).toBeVisible()
  await expect(page.getByText('Other', { exact: true })).toBeVisible()
  expect(calls.scans).toBe(1)
})

test('a configured device is sent from the wizard to the dashboard', async ({ page }) => {
  await mockApi(page, { configured: true, ssid: 'Home' })
  await page.goto('/new/setup')

  await expect(page).not.toHaveURL(/\/setup/)
  await expect(page.getByText('First-time setup wizard')).toHaveCount(0)
})
