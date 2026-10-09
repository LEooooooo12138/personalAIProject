import { expect, test, type Locator, type Page } from '@playwright/test'

// Full browser → Go Console → ControlService → disposable loopback HA fixture.
// No transport interception, production model, HA credential or real device.
const password = 'fixture-password-123'
async function login(page: Page, username: 'owner' | 'familyreader') {
  await page.goto('/app/')
  await page.getByLabel('用户名').fill(username)
  await page.getByLabel('密码', { exact: true }).fill(password)
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await expect(page.getByRole('heading', { name: '家庭概况' })).toBeVisible()
  const response = await page.request.get('/api/console/v1/auth/me')
  expect(response.status()).toBe(200)
  const identity = await response.json() as { user: { username: string }; capabilities: string[] }
  expect(identity.user.username).toBe(username)
  return identity
}
async function send(page: Page, content: string) {
  await page.goto('/app/chat')
  await page.getByRole('textbox', { name: '消息内容' }).fill(content)
  await page.getByRole('button', { name: '发送消息' }).click()
  await expect(page.getByRole('log', { name: '对话消息' }).getByText(content, { exact: true })).toBeVisible()
  await expect.poll(() => new URL(page.url()).searchParams.get('sid')).not.toBeNull()
  await expect(page.getByRole('textbox', { name: '消息内容' })).toBeEnabled()
  return new URL(page.url()).searchParams.get('sid')!
}
function card(page: Page) { return page.getByRole('region', { name: '开关 1控制提议' }) }
async function query(page: Page, state: '开启' | '关闭') {
  await send(page, '开关 1状态')
  const result = page.getByRole('region', { name: '开关 1设备状态' })
  await expect(result).toContainText(`当前状态：${state}`)
  await expect(result).toContainText('观察时间：')
}
async function ensureOn(page: Page) {
  await send(page, '打开开关 1')
  const confirm = card(page).getByRole('button', { name: '确认打开开关 1' })
  if (await confirm.count()) {
    await confirm.click()
    await expect(card(page)).toContainText('已观察到目标状态')
  } else {
    await expect(page.getByRole('region', { name: '开关 1设备状态' })).toContainText('当前状态：开启')
  }
}
async function assertFits(page: Page, controlCard: Locator) {
  const layout = await page.evaluate(() => ({ width: document.documentElement.clientWidth, scroll: document.documentElement.scrollWidth }))
  expect(layout.scroll, JSON.stringify(layout)).toBeLessThanOrEqual(layout.width)
  const bounds = await controlCard.boundingBox()
  expect(bounds).not.toBeNull()
  expect(bounds!.x).toBeGreaterThanOrEqual(0)
  expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(layout.width)
  expect(await controlCard.evaluate((element) => element.scrollWidth <= element.clientWidth)).toBeTruthy()
}

test('owner cancels without state change, then keyboard confirms and restores current result from history', async ({ page }) => {
  const identity = await login(page, 'owner')
  expect(identity.capabilities).toContain('devices:control')
  await ensureOn(page)
  const cancelledSid = await send(page, '关闭开关 1')
  await expect(card(page)).toContainText('客厅')
  await expect(card(page)).toContainText('当前状态：开启')
  await expect(card(page)).toContainText('目标动作：关闭')
  await expect(card(page)).toContainText('有效期至：')
  await card(page).getByRole('button', { name: '取消提议' }).click()
  await expect(card(page)).toContainText('提议已取消')
  await expect(card(page).getByRole('button', { name: /确认|取消/ })).toHaveCount(0)
  await page.reload()
  await expect(card(page)).toContainText('提议已取消')
  await query(page, '开启') // cancellation must not switch the mock load off
  const confirmedSid = await send(page, '关闭开关 1')
  const confirm = card(page).getByRole('button', { name: '确认关闭开关 1' })
  await confirm.focus()
  await expect(confirm).toBeFocused()
  await page.keyboard.press('Enter')
  await expect(card(page)).toContainText('已观察到目标状态')
  await expect(card(page)).toContainText('核对状态：关闭')
  await expect(card(page)).toContainText('现场核验')
  await expect(card(page).getByRole('button')).toHaveCount(0)
  await page.reload()
  await expect(card(page)).toContainText('已观察到目标状态')
  await expect(card(page)).toContainText('核对状态：关闭')
  await expect(card(page).getByRole('button')).toHaveCount(0)
  expect(new URL(page.url()).searchParams.get('sid')).toBe(confirmedSid)
  expect(confirmedSid).not.toBe(cancelledSid)
  await query(page, '关闭')
  await ensureOn(page)
})

test('member queries the live fixture state with no control capability or confirm button', async ({ page }) => {
  const identity = await login(page, 'familyreader')
  expect(identity.capabilities).toContain('devices:query')
  expect(identity.capabilities).not.toContain('devices:control')
  await query(page, '开启')
  await expect(page.getByRole('button', { name: /确认打开|确认关闭|取消提议/ })).toHaveCount(0)
})

test('pending controls and long display names fit 320, 390, 768 and 1440px without horizontal overflow', async ({ page }) => {
  await login(page, 'owner')
  await ensureOn(page)
  for (const width of [320, 390, 768, 1440]) {
    await page.setViewportSize({ width, height: 1000 })
    await send(page, '关闭开关 1')
    await expect(card(page).getByRole('button', { name: '确认关闭开关 1' })).toBeVisible()
    await assertFits(page, card(page))
    // Only extend display text for a layout stress check; the real API request,
    // proposal ID, action callbacks and state remain the fixture's authority.
    await card(page).locator('header strong').evaluate((element) => { element.textContent = '客厅靠窗长名称多路控制器主灯'.repeat(12) })
    await card(page).locator('header span').evaluate((element) => { element.textContent = '客厅与餐厅连接处的已核验负载位置'.repeat(6) })
    await assertFits(page, card(page))
    await test.info().attach(`control-${width}px`, { body: await page.screenshot({ fullPage: true }), contentType: 'image/png' })
    await card(page).getByRole('button', { name: '取消提议' }).click()
    await expect(card(page)).toContainText('提议已取消')
  }
  await query(page, '开启')
})
