import { expect, test, type Page } from '@playwright/test'
import { startAuthProxy } from './helpers/auth-proxy'
import fs from 'node:fs/promises'
import path from 'node:path'

const screenshotDir = path.resolve('../../.superpowers/sdd/2026-10-08-family-areas-live-integration/screenshots')
async function login(page: Page, username = 'familyreader', origin = '') {
  await page.goto(`${origin}/app/`)
  await page.getByLabel('用户名').fill(username)
  await page.getByLabel('密码', { exact: true }).fill('fixture-password-123')
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await expect(page.getByRole('heading', { name: '家庭概况' })).toBeVisible()
  await expect(page.getByText('gemma4:12b 已安装')).toBeVisible()
  const response = await page.request.get(`${origin}/api/console/v1/auth/me`)
  expect(response.status()).toBe(200)
  const identity = await response.json() as { user: { username: string }; capabilities: string[] }
  expect(identity.user.username).toBe(username)
  expect(identity.capabilities).toContain('areas:read')
}

async function revealEntityId(page: Page, id: string) {
  const card = page.locator('.entity-card').filter({ has: page.getByText(id, { exact: true }) })
  await card.getByText('技术详情', { exact: true }).click()
  await expect(card.getByText(id, { exact: true })).toBeVisible()
}

// This suite uses a dedicated temporary familyreader so its logins do not
// consume the original auth suite's per-user production rate-limit bucket.
// These views exercise the real Go gateway/HA client/catalog/DTO projection.
// The fixture's HA and Ollama-tags servers are disposable loopback mocks;
// chatting remains a visibly canned offline response, never real inference.
test('family areas navigate through real registry grouping and preserve covered controllers', async ({ page }) => {
  await login(page)
  await page.goto('/app/areas')
  await expect(page.getByRole('heading', { name: '家庭区域' })).toBeVisible()
  await expect(page.getByText('3 个设备注册项', { exact: true })).toBeVisible()
  await expect(page.getByText('6 个实体', { exact: true })).toBeVisible()
  await expect(page.getByLabel('全屋去重统计').getByText('2 个独立实体', { exact: true })).toBeVisible()
  await fs.mkdir(screenshotDir, { recursive: true })
  await page.screenshot({ path: path.join(screenshotDir, 'areas-desktop.png'), fullPage: true })
  await page.getByRole('link', { name: /客厅.*2 个设备注册项.*2 个实体/ }).click()
  await expect(page.getByRole('heading', { name: '客厅', exact: true })).toBeVisible()
  await page.getByRole('link', { name: /实体已覆盖的控制器/ }).click()
  await expect(page.getByText('此区域没有该设备的实体，实体可能已覆盖到其他区域。')).toBeVisible()
  await page.getByRole('link', { name: '客厅', exact: true }).click()
  await page.getByRole('link', { name: /客厅三路开关/ }).click()
  await revealEntityId(page, 'switch.channel_1')
  await expect(page.getByText('switch.channel_2', { exact: true })).toHaveCount(0)
  const details = page.getByText('配置与诊断实体（1）', { exact: true })
  await expect(page.getByText('sensor.diagnostic', { exact: true })).toBeHidden()
  await details.focus(); await page.keyboard.press('Space')
  await revealEntityId(page, 'sensor.diagnostic')
  await expect(page.getByText('暂无状态', { exact: true })).toBeVisible()
  await expect(page.getByText('已禁用', { exact: true })).toBeVisible()
  await expect(page.getByText('已隐藏', { exact: true })).toBeVisible()
  await expect(page.getByText(/控制器归属；负载地点未验证/)).toBeVisible()
  await page.screenshot({ path: path.join(screenshotDir, 'device-desktop.png'), fullPage: true })
  await page.getByRole('link', { name: '全部区域', exact: true }).click()
  await page.getByRole('link', { name: /厨房.*2 个设备注册项.*2 个实体/ }).click()
  await page.getByRole('link', { name: /客厅三路开关/ }).click()
  // The kitchen list has multiple entity-override tags until navigation settles.
  // Wait for this device's detail view before checking its own membership tag.
  await expect(page.getByRole('heading', { name: '客厅三路开关', exact: true, level: 1 })).toBeVisible()
  await expect(page.locator('.device-context').getByText('实体覆盖到此区域', { exact: true })).toBeVisible()
  await revealEntityId(page, 'switch.channel_2')
  await expect(page.getByText('switch.channel_1', { exact: true })).toHaveCount(0)
  const privateDTO = await page.request.get('/api/console/v1/areas/a_a2l0Y2hlbg/devices/d_bGl2aW5nLWNvbnRyb2w')
  expect(privateDTO.status()).toBe(200)
  const text = await privateDTO.text()
  expect(text).not.toContain('fixture-ha-secret')
  expect(text).not.toContain('attributes')
  expect(text).not.toContain('connections')
})

test('server search leaves whole-house totals intact and is keyboard reachable', async ({ page }) => {
  await login(page); await page.goto('/app/areas')
  const search = page.getByRole('searchbox', { name: '搜索区域、设备或实体' })
  await search.fill('switch.channel_2'); await search.focus(); await page.keyboard.press('Tab'); await expect(page.getByRole('button', { name: '搜索', exact: true })).toBeFocused(); await page.keyboard.press('Enter')
  await expect(page).toHaveURL(/q=switch.channel_2/)
  await expect(page.getByText('匹配 1 个区域', { exact: true })).toBeVisible()
  await expect(page.getByText('3 个设备注册项', { exact: true })).toBeVisible()
  await expect(page.getByRole('link', { name: /客厅三路开关.*设备/ })).toBeVisible()
  await expect(page.getByRole('link', { name: /客厅.*2 个设备注册项/ })).toHaveCount(0)
  await search.fill('sensor.temperature'); await page.getByRole('button', { name: '搜索', exact: true }).click()
  await expect(page).toHaveURL(/q=sensor.temperature/)
  await expect(page.getByRole('link', { name: /独立温度计.*独立实体/ })).toBeVisible()
  await page.goBack()
  await expect(page).toHaveURL(/q=switch.channel_2/)
  await expect(search).toHaveValue('switch.channel_2')
  await expect(page.getByRole('link', { name: /客厅三路开关.*设备/ })).toBeVisible()
  await expect(page.getByRole('link', { name: /独立温度计.*独立实体/ })).toHaveCount(0)
  await page.goForward()
  await expect(page).toHaveURL(/q=sensor.temperature/)
  await expect(search).toHaveValue('sensor.temperature')
  await expect(page.getByRole('link', { name: /独立温度计.*独立实体/ })).toBeVisible()
  await page.getByRole('button', { name: '清除搜索', exact: true }).click()
  await page.getByRole('link', { name: /空房间.*0 个设备注册项/ }).click()
  await expect(page.getByText('这个区域还没有设备或独立实体。')).toBeVisible()
})

test('nested device deep links reload and wrong combinations return a real 404', async ({ page }) => {
  await login(page, 'bobby')
  await page.goto('/app/areas/a_b3RoZXI/devices/e_c2Vuc29yLnRlbXBlcmF0dXJl')
  await expect(page.getByRole('heading', { name: '独立温度计', level: 1, exact: true })).toBeVisible()
  await expect(page.getByText('23.5', { exact: true })).toBeVisible()
  await expect(page.getByText('°C', { exact: true })).toBeVisible()
  await page.reload()
  await expect(page.getByRole('heading', { name: '独立温度计', level: 1, exact: true })).toBeVisible()
  await page.getByRole('link', { name: '其他', exact: true }).click()
  await expect(page.getByRole('heading', { name: '独立实体', exact: true })).toBeVisible()
  await page.goto('/app/areas/a_bGl2aW5n/devices/e_c2Vuc29yLnRlbXBlcmF0dXJl')
  await expect(page.getByText('未找到这个区域或设备。', { exact: true })).toBeVisible()
  const wrong = await page.request.get('/api/console/v1/areas/a_bGl2aW5n/devices/e_c2Vuc29yLnRlbXBlcmF0dXJl')
  expect(wrong.status()).toBe(404)
  const nonexistent = await page.request.get('/app/areas/a_bGl2aW5n/not-a-route')
  expect(nonexistent.status()).toBe(404)
})

test('mobile family paths fit 390px and browser back returns to the parent', async ({ browser }) => {
  const context = await browser.newContext({ viewport: { width: 390, height: 844 } })
  try {
    const page = await context.newPage(); await login(page, 'owner')
    const nav = page.getByRole('navigation', { name: '底部导航' })
    await expect(nav.getByRole('link', { name: '成员', exact: true })).toHaveCount(0)
    await expect(nav.getByRole('link')).toHaveCount(5)
    await nav.getByRole('link', { name: '设备', exact: true }).click()
    await expect(page.getByRole('heading', { name: '家庭区域' })).toBeVisible()
    await expect(page.getByRole('link', { name: /客厅.*设备注册项/ })).toBeVisible()
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBeTruthy()
    await fs.mkdir(screenshotDir, { recursive: true })
    await page.screenshot({ path: path.join(screenshotDir, 'areas-mobile.png'), fullPage: true })
    await page.getByRole('link', { name: /客厅.*2 个设备注册项/ }).click()
    await page.getByRole('link', { name: /客厅三路开关/ }).click()
    await revealEntityId(page, 'switch.channel_1')
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBeTruthy()
    await page.screenshot({ path: path.join(screenshotDir, 'device-mobile.png'), fullPage: true })
    await page.goBack(); await expect(page.getByRole('heading', { name: '客厅', exact: true })).toBeVisible()
    await page.goBack(); await expect(page.getByRole('heading', { name: '家庭区域', exact: true })).toBeVisible()
    await nav.getByRole('link', { name: '我的', exact: true }).click()
    await expect(page.getByRole('link', { name: /成员管理/ })).toBeVisible()
  } finally { await context.close() }
})

test('a failed refresh retains dated family data and retry recovers through the real Worker', async ({ browser }) => {
  const proxy = await startAuthProxy('http://127.0.0.1:18081')
  const context = await browser.newContext()
  try {
    const page = await context.newPage(); await login(page, 'familyreader', proxy.origin)
    await page.goto(`${proxy.origin}/app/areas`)
    await expect(page.getByRole('link', { name: /客厅.*设备注册项/ })).toBeVisible()
    const before = await page.getByRole('region', { name: 'HA 与目录状态' }).locator('time').first().textContent()
    const gate = proxy.gate('/api/console/v1/areas', 'fail')
    await page.getByRole('button', { name: '重新检查', exact: true }).click()
    await gate.completed
    await expect(page.getByText('正在显示旧数据', { exact: true })).toBeVisible()
    await expect(page.getByRole('link', { name: /客厅.*设备注册项/ })).toBeVisible()
    expect(await page.getByRole('region', { name: 'HA 与目录状态' }).locator('time').first().textContent()).toBe(before)
    await page.getByRole('button', { name: '重新检查', exact: true }).click()
    await expect(page.getByText('HA 最近检查成功', { exact: true })).toBeVisible()
    await expect(page.getByText('正在显示旧数据', { exact: true })).toHaveCount(0)
  } finally { await context.close(); await proxy.close() }
})
