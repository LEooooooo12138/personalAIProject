import { expect, test, type BrowserContext, type Page } from '@playwright/test'
import { startAuthProxy } from './helpers/auth-proxy'

const api = '/api/console/v1'
let proxy: Awaited<ReturnType<typeof startAuthProxy>>

test.beforeEach(async ({ baseURL }) => { proxy = await startAuthProxy(baseURL!) })
test.afterEach(async () => { await proxy.close() })

async function login(page: Page, username: string) {
  await page.goto(`${proxy.origin}/app/`)
  await page.getByLabel('用户名').fill(username)
  await page.getByLabel('密码', { exact: true }).fill('fixture-password-123')
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await expect(page.locator('.topbar-greeting').getByText(`你好，${username}`)).toBeVisible()
}

async function delayedLogoutCannotEraseNewLogin(context: BrowserContext) {
  const first = await context.newPage()
  const second = await context.newPage()
  await login(first, 'alice')
  await second.goto(`${proxy.origin}/app/`)
  await expect(second.locator('.topbar-greeting').getByText('你好，alice')).toBeVisible()
  const gate = proxy.gate(`${api}/auth/logout`, 'response')
  const initialLogins = proxy.counts(`${api}/auth/login`)
  try {
    await first.getByRole('button', { name: '退出登录' }).last().click()
    await gate.seen
    await expect(second.getByRole('button', { name: '登录', exact: true })).toBeVisible()
    await second.getByLabel('用户名').fill('bobby')
    await second.getByLabel('密码', { exact: true }).fill('fixture-password-123')
    await second.getByRole('button', { name: '登录', exact: true }).click()
    await expect(second.getByRole('button', { name: '正在登录…' })).toBeVisible()
    // Give a wrongly uncoordinated worker enough time to reach the real proxy.
    await second.waitForTimeout(250)
    expect(proxy.counts(`${api}/auth/login`), 'new login must wait for the old real logout response').toBe(initialLogins)
    gate.release()
    await gate.completed
    await expect(second.locator('.topbar-greeting').getByText('你好，bobby')).toBeVisible()
    const me = await context.request.get(`${proxy.origin}${api}/auth/me`)
    expect(me.status()).toBe(200)
    expect((await me.json()).user.username).toBe('bobby')
  } finally {
    gate.release()
    await first.close()
    await second.close()
  }
}

test('real delayed logout response cannot erase another tab login', async ({ browser }) => {
  const context = await browser.newContext()
  try { await delayedLogoutCannotEraseNewLogin(context) } finally { await context.close() }
})

test('without Web Locks, LAN HTTP still serializes auth writes', async ({ browser }) => {
  const context = await browser.newContext()
  await context.addInitScript(() => Object.defineProperty(navigator, 'locks', { value: undefined, configurable: true }))
  try {
    const check = await context.newPage()
    await check.goto(`${proxy.origin}/app/`)
    expect(await check.evaluate(() => navigator.locks === undefined)).toBeTruthy()
    await check.close()
    await delayedLogoutCannotEraseNewLogin(context)
  } finally { await context.close() }
})

test('old private UI is removed before login Cookie headers arrive ahead of JSON', async ({ browser }) => {
  const preparation = await browser.newContext()
  const context = await browser.newContext()
  const oldPage = await context.newPage()
  const loginPage = await context.newPage()
  let gate: ReturnType<typeof proxy.gate> | undefined
  try {
    const privatePage = await preparation.newPage()
    await login(privatePage, 'bobby')
    await privatePage.goto(`${proxy.origin}/app/chat`)
    await privatePage.getByRole('textbox', { name: '消息内容' }).fill('BOBBY-PRIVATE-MARKER')
    await privatePage.getByRole('button', { name: '发送消息' }).click()
    await expect(privatePage.getByRole('log', { name: '对话消息' }).getByText('BOBBY-PRIVATE-MARKER')).toBeVisible()
    await expect(privatePage.getByRole('log', { name: '对话消息' }).getByText('离线测试回复')).toBeVisible()
    await expect.poll(() => new URL(privatePage.url()).searchParams.get('sid')).not.toBeNull()
    const sid = new URL(privatePage.url()).searchParams.get('sid')!
    const history = await preparation.request.get(proxy.origin + api + '/sessions/' + encodeURIComponent(sid) + '/messages')
    expect(history.status()).toBe(200)
    expect((await history.json()).messages.some((message: { role: string; content: string }) => message.role === 'user' && message.content === 'BOBBY-PRIVATE-MARKER')).toBe(true)

    await loginPage.goto(`${proxy.origin}/app/`)
    await expect(loginPage.getByRole('button', { name: '登录', exact: true })).toBeVisible()
    await login(oldPage, 'alice')
    gate = proxy.gate(`${api}/auth/login`, 'body')
    await loginPage.getByLabel('用户名').fill('bobby')
    await loginPage.getByLabel('密码', { exact: true }).fill('fixture-password-123')
    await loginPage.getByRole('button', { name: '登录', exact: true }).click()
    await gate.seen
    await expect.poll(async () => {
      const me = await context.request.get(`${proxy.origin}${api}/auth/me`)
      return me.ok() ? (await me.json()).user.username : ''
    }).toBe('bobby')
    await expect(oldPage.getByRole('button', { name: '登录', exact: true })).toBeVisible()
    await expect(oldPage.getByText('你好，alice')).toHaveCount(0)
    await expect(oldPage.getByText('BOBBY-PRIVATE-MARKER')).toHaveCount(0)
    gate.release()
    await gate.completed
    await expect(loginPage.locator('.topbar-greeting').getByText('你好，bobby')).toBeVisible()
  } finally {
    gate?.release()
    await context.close()
    await preparation.close()
  }
})

test('failed logout clears private UI, reports failure and retries real revocation', async ({ browser }) => {
  const context = await browser.newContext()
  const page = await context.newPage()
  let gate: ReturnType<typeof proxy.gate> | undefined
  try {
    await login(page, 'alice')
    gate = proxy.gate(`${api}/auth/logout`, 'fail')
    await page.getByRole('button', { name: '退出登录' }).last().click()
    await gate.seen
    await expect(page.getByRole('alert')).toContainText('退出登录未完成')
    await expect(page.getByText('你好，alice')).toHaveCount(0)
    expect((await context.request.get(`${proxy.origin}${api}/auth/me`)).status()).toBe(200)
    await page.getByRole('button', { name: '重试退出' }).click()
    await expect(page.getByRole('button', { name: '登录', exact: true })).toBeVisible()
    expect((await context.request.get(`${proxy.origin}${api}/auth/me`)).status()).toBe(401)
  } finally {
    gate?.release()
    await context.close()
  }
})

test('a queued second login completes after the first login response', async ({ browser }) => {
  const context = await browser.newContext()
  const first = await context.newPage()
  const second = await context.newPage()
  let gate: ReturnType<typeof proxy.gate> | undefined
  try {
    await first.goto(`${proxy.origin}/app/`)
    await second.goto(`${proxy.origin}/app/`)
    await expect(first.getByRole('button', { name: '登录', exact: true })).toBeVisible()
    await expect(second.getByRole('button', { name: '登录', exact: true })).toBeVisible()
    gate = proxy.gate(`${api}/auth/login`, 'body')
    await first.getByLabel('用户名').fill('alice')
    await first.getByLabel('密码', { exact: true }).fill('fixture-password-123')
    await first.getByRole('button', { name: '登录', exact: true }).click()
    await gate.seen
    await second.getByLabel('用户名').fill('bobby')
    await second.getByLabel('密码', { exact: true }).fill('fixture-password-123')
    await second.getByRole('button', { name: '登录', exact: true }).click()
    await expect(second.getByRole('button', { name: '正在登录…' })).toBeVisible()
    gate.release()
    await gate.completed
    await expect(second.locator('.topbar-greeting').getByText('你好，bobby')).toBeVisible()
    const me = await context.request.get(`${proxy.origin}${api}/auth/me`)
    expect(me.status()).toBe(200)
    expect((await me.json()).user.username).toBe('bobby')
    await expect(first.getByText('你好，alice')).toHaveCount(0)
  } finally {
    gate?.release()
    await context.close()
  }
})

test('logout retry enables login when the old server session is already revoked', async ({ browser }) => {
  const context = await browser.newContext()
  const page = await context.newPage()
  let gate: ReturnType<typeof proxy.gate> | undefined
  try {
    await login(page, 'alice')
    gate = proxy.gate(`${api}/auth/logout`, 'fail')
    await page.getByRole('button', { name: '退出登录' }).last().click()
    await expect(page.getByRole('alert')).toContainText('退出登录未完成')
    const me = await context.request.get(`${proxy.origin}${api}/auth/me`)
    expect(me.status()).toBe(200)
    const identity = await me.json()
    const revoked = await context.request.post(`${proxy.origin}${api}/auth/logout`, {
      headers: { Origin: proxy.origin, 'X-CSRF-Token': identity.csrf_token },
    })
    expect(revoked.status()).toBe(204)
    await page.getByRole('button', { name: '重试退出' }).click()
    await expect(page.getByRole('alert')).toHaveCount(0)
    await expect(page.getByRole('button', { name: '登录', exact: true })).toBeEnabled()
    await page.getByLabel('用户名').fill('bobby')
    await page.getByLabel('密码', { exact: true }).fill('fixture-password-123')
    await page.getByRole('button', { name: '登录', exact: true }).click()
    await expect(page.locator('.topbar-greeting').getByText('你好，bobby')).toBeVisible()
  } finally {
    gate?.release()
    await context.close()
  }
})


test('logout retry keeps failure when its queued identity recheck is unavailable', async ({ browser }) => {
  const context = await browser.newContext()
  const page = await context.newPage()
  let logoutGate: ReturnType<typeof proxy.gate> | undefined
  let firstMe: ReturnType<typeof proxy.gate> | undefined
  let failedMe: ReturnType<typeof proxy.gate> | undefined
  try {
    await login(page, 'bobby')
    logoutGate = proxy.gate(`${api}/auth/logout`, 'fail')
    await page.getByRole('button', { name: '退出登录' }).last().click()
    await expect(page.getByRole('alert')).toContainText('退出登录未完成')
    firstMe = proxy.gate(`${api}/auth/me`, 'response')
    await page.getByRole('button', { name: '重试退出' }).click()
    await firstMe.seen
    failedMe = proxy.gate(`${api}/auth/me`, 'fail')
    firstMe.release()
    await failedMe.seen
    await expect(page.getByRole('alert')).toContainText('退出登录未完成')
    await expect(page.getByRole('button', { name: '登录', exact: true })).toBeDisabled()
    expect((await context.request.get(`${proxy.origin}${api}/auth/me`)).status()).toBe(200)
    await page.getByRole('button', { name: '重试退出' }).click()
    await expect(page.getByRole('alert')).toHaveCount(0)
    await expect(page.getByRole('button', { name: '登录', exact: true })).toBeEnabled()
    expect((await context.request.get(`${proxy.origin}${api}/auth/me`)).status()).toBe(401)
  } finally {
    logoutGate?.release()
    firstMe?.release()
    failedMe?.release()
    await context.close()
  }
})
