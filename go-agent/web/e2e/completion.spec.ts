import { expect, test, type Page } from '@playwright/test'

// These flows use the real same-origin Go Console server and its Reader/Chain/
// Manager. HA and inference are disposable loopback mocks, never real devices.
async function login(page: Page, username: string) {
  await page.goto('/app/')
  await page.getByLabel('用户名').fill(username)
  await page.getByLabel('密码', { exact: true }).fill('fixture-password-123')
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await expect(page.getByRole('heading', { name: '家庭概况' })).toBeVisible()
}

test('member public knowledge and private deep-link boundaries use real Go handlers', async ({ page }) => {
  await login(page, 'familyreader')
  await page.goto('/app/knowledge')
  await page.getByLabel('搜索公开知识').fill('紫藤')
  await page.getByRole('button', { name: '搜索知识' }).click()
  await page.getByRole('link', { name: /家庭公开指南/ }).click()
  await expect(page.getByText(/紫藤放在书房窗边/)).toBeVisible()
  await page.reload()
  await expect(page.getByText(/紫藤放在书房窗边/)).toBeVisible()
  await page.goto('/app/admin/knowledge')
  await expect(page.getByRole('heading', { name: '无权查看私人知识' })).toBeVisible()
  const denied = await page.request.get('/api/console/v1/admin/vault/page?path=concepts%2Fprivate-note.md')
  expect(denied.status()).toBe(403)
})

test('admin personal answer sources and explicit ingest remain private', async ({ page }) => {
  await login(page, 'owner')
  await page.goto('/app/admin/knowledge')
  await page.getByLabel('搜索私人知识').fill('紫藤')
  await page.getByRole('button', { name: '检索私人知识' }).click()
  await page.getByRole('link', { name: /管理员私人笔记/ }).click()
  await expect(page.getByText(/P42/)).toBeVisible()
  await page.getByRole('link', { name: '返回私人知识' }).click()
  await page.getByLabel('私人问题').fill('紫藤的私人物品编号是什么？')
  await page.getByRole('button', { name: '单次问答', exact: true }).click()
  await expect(page.getByRole('region', { name: '本次私人回答' })).toContainText('P42')
  await expect(page.getByRole('region', { name: '本次私人回答' }).getByRole('link', { name: '管理员私人笔记' })).toBeVisible()
  await page.getByLabel('来源标题').fill('浏览器私人导入')
  await page.getByLabel('导入文本').fill('离线浏览器唯一私人导入事实：青石柜第九层。')
  await page.getByRole('button', { name: '导入到 personal' }).click()
  await expect(page.getByText(/尚未公开发布。/)).toBeVisible()
  await page.goto('/app/knowledge')
  await page.getByLabel('搜索公开知识').fill('青石柜')
  await page.getByRole('button', { name: '搜索知识' }).click()
  await expect(page.getByText('没有找到可读取的公开资料。')).toBeVisible()
})

test('binding creates a new version, separate mock installation and explicit sharing reaches a member', async ({ page, browser }) => {
  await login(page, 'owner')
  await page.goto('/app/automations?id=fixture_time')
  await page.getByRole('button', { name: '补齐有人在家条件' }).click()
  await page.getByLabel('代表全家的占用实体').selectOption('group.family_home')
  await page.getByLabel('有人在家时的状态').fill('on')
  await page.getByRole('button', { name: '保存绑定并查看新版本' }).click()
  await expect(page).toHaveURL((url) => url.pathname === '/app/automations' && Boolean(url.searchParams.get('id')) && url.searchParams.get('id') !== 'fixture_time')
  const newId = new URL(page.url()).searchParams.get('id')!
  await expect(page.getByText('新版本：' + newId, { exact: true })).toBeVisible()
  const stored = await page.request.get('/api/console/v1/suggestions')
  expect(stored.ok()).toBe(true)
  const version = (await stored.json() as { suggestions: { id: string; title: string; status: string }[] }).suggestions.find((item) => item.id === newId)!
  expect(version).toBeDefined()
  expect(version.status).toBe('pending')
  const result = { suggestion: version }
  await expect(page.getByRole('button', { name: '确认创建自动化' })).toBeEnabled()
  await expect(page.getByText('switch.turn_on', { exact: true })).toBeVisible()
  const before = await page.request.get('/api/console/v1/suggestions')
  expect((await before.json() as { suggestions: { id: string; status: string }[] }).suggestions.find((item) => item.id === result.suggestion.id)?.status).toBe('pending')
  await page.getByRole('button', { name: '确认创建自动化' }).click()
  await expect(page.getByText('已确认安装并归档', { exact: true })).toBeVisible()
  await page.getByRole('link', { name: '返回自动化结果' }).click()
  await page.getByLabel(`共享：${result.suggestion.title}`).check()
  await page.getByRole('button', { name: '保存结果共享' }).click()
  await expect(page.getByRole('button', { name: '保存结果共享' })).toBeDisabled()
  await expect.poll(async () => {
    const shared = await page.request.get('/api/console/v1/admin/sharing')
    expect(shared.ok()).toBe(true)
    return (await shared.json() as { suggestion_ids: string[] }).suggestion_ids.includes(newId)
  }).toBe(true)
  const memberContext = await browser.newContext({ baseURL: 'http://127.0.0.1:18081' })
  try {
    const memberPage = await memberContext.newPage()
    await login(memberPage, 'familyreader')
    await memberPage.goto('/app/automations')
    await memberPage.getByRole('link', { name: new RegExp(result.suggestion.title) }).click()
    await expect(memberPage.getByText('已确认安装并归档', { exact: true })).toBeVisible()
    await expect(memberPage.getByRole('button', { name: '确认创建自动化' })).toHaveCount(0)
    await expect(memberPage.getByText('fixture_time', { exact: true })).toHaveCount(0)
  } finally { await memberContext.close() }
})

test('phone layout, navigation, Enter newline and history return use the real worker', async ({ page }) => {
  await page.setViewportSize({ width: 320, height: 740 })
  await login(page, 'familyreader')
  const bottom = page.getByRole('navigation', { name: '底部导航' })
  await expect(bottom.getByRole('link')).toHaveCount(5)
  await bottom.getByRole('link', { name: '对话', exact: true }).click()
  const input = page.getByRole('textbox', { name: '消息内容' })
  await input.fill('手机第一行')
  await input.press('Enter')
  await input.type('第二行')
  await expect(input).toHaveValue('手机第一行\n第二行')
  await page.getByRole('button', { name: '发送消息' }).click()
  await expect(page.getByRole('log')).toContainText('手机第一行')
  await expect(page.getByRole('log')).toContainText('离线测试回复')
  await page.getByRole('button', { name: '查看对话列表' }).click()
  await expect(page.getByRole('complementary', { name: '最近对话' })).toBeVisible()
  await page.getByRole('button', { name: '返回当前对话' }).click()
  await expect(page.getByRole('region', { name: '当前对话' })).toBeVisible()
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true)
})

test('collection analysis and all new views remain readable at supported content widths', async ({ page }) => {
  await login(page, 'owner')
  await expect(page.getByRole('heading', { name: '采集状态与异常' })).toBeVisible()
  await page.getByRole('link', { name: '采集详情与分析' }).click()
  await expect(page.getByRole('heading', { name: '家庭数据采集' })).toBeVisible()
  await page.getByRole('button', { name: '分析最近 14 天' }).click()
  await expect(page.getByText(/分析完成，产生 \d+ 条建议；尚未安装自动化。/)).toBeVisible()
  // 305px models the 320px browser content remaining after a 15px classic scrollbar.
  // This is a content-width simulation; the root CUA run verified the actual classic scrollbar.
  for (const width of [305, 320, 390, 768, 1440]) {
    await page.setViewportSize({ width, height: 900 })
    for (const url of ['/app/knowledge', '/app/admin/knowledge', '/app/admin/collection', '/app/automations', '/app/areas', '/app/account']) {
      await page.goto(url)
      await expect(page.locator('main h1')).toBeVisible()
      await page.evaluate(() => {
        document.documentElement.style.overflowY = 'scroll'
        document.documentElement.style.scrollbarGutter = 'stable'
        document.body.style.minHeight = '200vh'
      })
      const layout = await page.evaluate(() => ({ inner: innerWidth, available: document.documentElement.clientWidth, scroll: document.documentElement.scrollWidth }))
      expect(layout.scroll, JSON.stringify(layout)).toBeLessThanOrEqual(layout.available)
      const buttons = page.locator('main button:visible')
      for (let index = 0; index < await buttons.count(); index++) {
        const box = await buttons.nth(index).boundingBox()
        expect(box?.height).toBeGreaterThanOrEqual(44)
      }
    }
  }
})
