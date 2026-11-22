import { test, expect, type Page } from '@playwright/test'

const offWarning = 'Test sent, but the bot is off. Turn on Enable bot service and save to get notifications and commands.'

async function mockApi(page: Page, serviceEnabled: boolean) {
  let enabled = serviceEnabled
  const config = () => ({
    has_token: true,
    masked_token: '••••examples',
    chat_id: '123456789',
    silent_notify: false,
    service_enabled: enabled,
    service_running: enabled,
  })
  await page.route('**/api/**', route => {
    const request = route.request()
    const path = new URL(request.url()).pathname
    if (path.endsWith('/api/config/telegram/test')) {
      return route.fulfill({ json: { result: 'ok' } })
    }
    if (path.endsWith('/api/config/telegram')) {
      if (request.method() === 'POST') enabled = Boolean(request.postDataJSON().enabled)
      return route.fulfill({ json: config() })
    }
    if (path.endsWith('/api/setup/status')) {
      return route.fulfill({ json: { configured: true, version: 'test', model: 'test' } })
    }
    if (path.endsWith('/api/status/stream')) {
      return route.fulfill({ status: 204, body: '' })
    }
    if (path.endsWith('/api/status')) {
      return route.fulfill({ json: { disks: [], archive: { state: 'idle' } } })
    }
    // The other Settings sections handle a failed load on their own.
    return route.abort()
  })
}

function telegramCard(page: Page) {
  return page.locator('#notifications')
}

test('a test message warns when the bot service is off', async ({ page }) => {
  await mockApi(page, false)
  await page.goto('/new/settings')

  const card = telegramCard(page)
  await card.getByRole('button', { name: 'Send test message' }).click()
  await expect(card.getByText(offWarning)).toBeVisible()
})

test('saving with the bot on clears the warning', async ({ page }) => {
  await mockApi(page, false)
  await page.goto('/new/settings')

  const card = telegramCard(page)
  await card.getByRole('button', { name: 'Send test message' }).click()
  await expect(card.getByText(offWarning)).toBeVisible()

  await card.locator('label', { hasText: 'Enable bot service' }).locator('> div').click()
  await card.getByRole('button', { name: 'Save', exact: true }).click()
  await expect(card.getByText('Saved.')).toBeVisible()
  await expect(card.getByText(offWarning)).toHaveCount(0)

  await card.getByRole('button', { name: 'Send test message' }).click()
  await expect(card.getByText('Test message sent — check your Telegram.')).toBeVisible()
})
