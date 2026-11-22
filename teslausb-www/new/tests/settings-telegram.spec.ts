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

const ann = { id: '555', type: 'private', name: 'Ann Lee', username: 'ann', last_seen: 1_700_000_000 }
const stranger = { id: '777', type: 'private', name: 'Someone Else', last_seen: 1_700_000_100 }

async function mockFindChats(page: Page, chatID: string, reply: { status?: number; json: unknown }) {
  const saved: unknown[] = []
  const lookups: unknown[] = []
  const config = {
    has_token: true,
    masked_token: '••••examples',
    chat_id: chatID,
    silent_notify: false,
    service_enabled: false,
    service_running: false,
  }
  await page.route('**/api/**', route => {
    const request = route.request()
    const path = new URL(request.url()).pathname
    if (path.endsWith('/api/config/telegram/find-chats')) {
      lookups.push(request.postDataJSON())
      return route.fulfill(reply)
    }
    if (path.endsWith('/api/config/telegram')) {
      if (request.method() === 'POST') saved.push(request.postDataJSON())
      return route.fulfill({ json: config })
    }
    if (path.endsWith('/api/setup/status')) {
      return route.fulfill({ json: { configured: true, version: 'test', model: 'test' } })
    }
    if (path.endsWith('/api/status/stream')) return route.fulfill({ status: 204, body: '' })
    if (path.endsWith('/api/status')) {
      return route.fulfill({ json: { disks: [], archive: { state: 'idle' } } })
    }
    return route.abort()
  })
  return { saved, lookups }
}

test('one recent chat fills an empty field without saving it', async ({ page }) => {
  const { saved, lookups } = await mockFindChats(page, '', { json: { chats: [ann] } })
  await page.goto('/new/settings')

  const card = telegramCard(page)
  await card.getByRole('button', { name: 'Find my chat ID' }).click()
  await expect(card.getByLabel('Chat ID')).toHaveValue('555')
  await expect(card.getByText('Added Ann Lee (@ann). Check this is you, then press Save.')).toBeVisible()
  expect(lookups).toEqual([{ bot_token: '' }])
  expect(saved).toEqual([])

  await card.getByLabel('Chat ID').fill('12345')
  await expect(card.getByLabel('Chat ID')).toHaveValue('12345')
})

test('several chats must be chosen and nothing is authorised automatically', async ({ page }) => {
  const { saved } = await mockFindChats(page, '', { json: { chats: [stranger, ann] } })
  await page.goto('/new/settings')

  const card = telegramCard(page)
  await card.getByRole('button', { name: 'Find my chat ID' }).click()
  await expect(card.getByText('Choose your chat.', { exact: false })).toBeVisible()
  await expect(card.getByLabel('Chat ID')).toHaveValue('')

  await card.getByRole('listitem').filter({ hasText: 'Ann Lee' })
    .getByRole('button', { name: 'Use this chat' }).click()
  await expect(card.getByLabel('Chat ID')).toHaveValue('555')
  await expect(card.getByRole('button', { name: 'Use this chat' })).toHaveCount(0)
  expect(saved).toEqual([])
})

test('a found chat never replaces an existing allowed chat', async ({ page }) => {
  await mockFindChats(page, '123', { json: { chats: [ann] } })
  await page.goto('/new/settings')

  const card = telegramCard(page)
  await card.getByRole('button', { name: 'Find my chat ID' }).click()
  await expect(card.getByLabel('Chat ID')).toHaveValue('123')
  await card.getByRole('button', { name: 'Use this chat' }).click()
  await expect(card.getByLabel('Chat ID')).toHaveValue('123, 555')
})

test('an empty result tells the user to message their bot', async ({ page }) => {
  await mockFindChats(page, '', { json: { chats: [] } })
  await page.goto('/new/settings')

  const card = telegramCard(page)
  await card.getByRole('button', { name: 'Find my chat ID' }).click()
  await expect(card.getByText('No recent messages found. Open your bot in Telegram, send it any message, then try again.')).toBeVisible()
})

test('a lookup error is shown and the field stays usable', async ({ page }) => {
  await mockFindChats(page, '', {
    status: 409,
    json: { error: "Another device is reading this bot's messages. Stop it and try again, or enter the chat ID manually." },
  })
  await page.goto('/new/settings')

  const card = telegramCard(page)
  await card.getByRole('button', { name: 'Find my chat ID' }).click()
  await expect(card.getByText('Another device is reading', { exact: false })).toBeVisible()
  await card.getByLabel('Chat ID').fill('555')
  await expect(card.getByLabel('Chat ID')).toHaveValue('555')
})
