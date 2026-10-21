import { expect, test, type Page } from '@playwright/test'
import fs from 'node:fs/promises'
import path from 'node:path'

const screenshots = path.resolve('../../docs/assets/frontend-redesign-2026-10-10')
async function login(page: Page, username = 'owner') {
  await page.goto('/app/')
  await page.getByLabel('用户名').fill(username)
  await page.getByLabel('密码', { exact: true }).fill('fixture-password-123')
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await expect(page.getByRole('heading', { name: '家庭概况', exact: true })).toBeVisible()
}
const surfaces = [
  ['home', '/app/', /^你好，/],
  ['areas', '/app/areas', '家庭区域'],
  ['area', '/app/areas/a_bGl2aW5n', '客厅'],
  ['device', '/app/areas/a_b3RoZXI/devices/e_c2Vuc29yLnRlbXBlcmF0dXJl', '独立温度计'],
  ['knowledge', '/app/knowledge', '公开知识'],
  ['personal-knowledge', '/app/admin/knowledge', '管理员私人知识'],
  ['account', '/app/account', '我的'],
  ['members', '/app/members', '家庭成员'],
  ['chat', '/app/chat', '和家序聊聊'],
  ['collection', '/app/admin/collection', '家庭数据采集'],
  ['automations', '/app/automations', '自动化建议与结果'],
  ['assistant', '/app/admin/assistant', '助手设置'],
] as const

for (const width of [320, 390, 768, 1440]) {
  test(`all twelve surfaces use one main and fit ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 })
    await login(page)
    await fs.mkdir(screenshots, { recursive: true })
    for (const [name, url, title] of surfaces) {
      await page.goto(url)
      await expect(page.getByRole('heading', { name: title, exact: true, level: 1 })).toBeVisible()
      await expect(page.getByRole('main')).toHaveCount(1)
      await expect(page.locator('.jx-legacy-page')).toHaveCount(0)
      const metrics = await page.evaluate(() => ({ scroll: document.documentElement.scrollWidth, viewport: innerWidth }))
      expect(metrics.scroll, `${name}: ${JSON.stringify(metrics)}`).toBeLessThanOrEqual(width)
      expect(await page.getByRole('heading', { level: 1 }).evaluate(node => getComputedStyle(node).fontFamily)).toContain('Microsoft YaHei')
      if (width === 390 || width === 1440) await page.screenshot({ path: path.join(screenshots, `${name}-${width}.png`), fullPage: true })
      await page.evaluate(() => { const sheet = document.styleSheets[0]; sheet.insertRule('html{font-size:32px}', sheet.cssRules.length) })
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), name + ' with CSS 200% text').toBe(true)
      if (name === 'assistant' && width === 390) await page.screenshot({ path: path.join(screenshots, 'assistant-390-css200.png'), fullPage: true })
    }
  })
}

for (const width of [759, 760, 761]) {
  test(`chat navigation and Enter contract at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 }); await login(page); await page.goto('/app/chat')
    const mobile = width <= 760
    await expect(page.getByRole('navigation', { name: mobile ? '底部导航' : '主导航' })).toBeVisible()
    await expect(page.getByRole('navigation', { name: mobile ? '主导航' : '底部导航' })).toBeHidden()
    const input = page.getByRole('textbox', { name: '消息内容' })
    const text = `边界换行 ${width}`
    await input.fill(text); await input.press('Enter')
    if (mobile) { await expect(input).toHaveValue(`${text}\n`); await page.getByRole('button', { name: '发送消息' }).click() }
    await expect(page.getByRole('log')).toContainText(text)
    await expect(page.getByRole('log')).toContainText('离线测试回复')
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  })
}

test('mobile primary entry, keyboard dialog and CSS 200% assistive layout', async ({ page }) => {
  await page.setViewportSize({ width: 320, height: 740 }); await login(page)
  const entry = page.getByRole('button', { name: /开启新对话/ })
  await expect(entry).toBeInViewport()
  // This is an assistive CSS enlargement check; it is not OS text scaling.
  await page.evaluate(() => { const sheet = document.styleSheets[0]; sheet.insertRule('html{font-size:32px}', sheet.cssRules.length) })
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  await page.goto('/app/members')
  await page.evaluate(() => { const sheet = document.styleSheets[0]; sheet.insertRule('html{font-size:32px}', sheet.cssRules.length) })
  const trigger = page.getByRole('button', { name: '创建成员', exact: true })
  await trigger.focus(); await page.keyboard.press('Enter')
  await expect(page.getByRole('dialog', { name: '创建家庭成员', exact: true })).toBeVisible()
  await page.getByLabel('登录名').fill('unsubmitted_keyboard_draft')
  await page.screenshot({ path: path.join(screenshots, 'member-dialog-320-css200.png'), fullPage: true })
  await page.keyboard.press('Escape')
  await expect(page.getByRole('dialog')).toBeHidden(); await expect(trigger).toBeFocused()
})

test('login error and member private-denial states remain explicit', async ({ page }) => {
  await page.goto('/app/'); await page.getByLabel('用户名').fill('familyreader')
  await page.getByLabel('密码', { exact: true }).fill('wrong-fixture-password')
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await expect(page.getByRole('alert')).toContainText('用户名或密码不正确')
  await expect(page.getByLabel('密码', { exact: true })).toHaveValue('')
  await fs.mkdir(screenshots, { recursive: true })
  await page.screenshot({ path: path.join(screenshots, 'login-error.png'), fullPage: true })
  await login(page, 'familyreader'); await page.goto('/app/admin/knowledge')
  await expect(page.getByRole('heading', { name: '无权查看私人知识', exact: true })).toBeVisible()
  await expect(page.getByLabel('私人问题')).toHaveCount(0)
  await page.screenshot({ path: path.join(screenshots, 'private-denied.png'), fullPage: true })
})
