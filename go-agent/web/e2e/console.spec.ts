import { expect, test, type Page } from '@playwright/test'

const password = 'fixture-password-123'
const unique = () => `member_${Date.now().toString(36)}`

async function login(page: Page, username: string, secret = password) {
  await page.goto('/app/')
  await page.getByLabel('用户名').fill(username)
  await page.getByLabel('密码', { exact: true }).fill(secret)
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await expect(page.getByRole('navigation', { name: '底部导航' }).or(page.getByRole('navigation', { name: '主导航' }))).toBeVisible()
  const response = await page.request.get('/api/console/v1/auth/me')
  expect(response.ok()).toBeTruthy()
  const identity = await response.json() as { csrf_token: string; user: { username: string } }
  expect(identity.user.username).toBe(username)
  return identity
}

async function send(page: Page, content: string) {
  await page.goto('/app/chat')
  await page.getByRole('textbox', { name: '消息内容' }).fill(content)
  await page.getByRole('button', { name: '发送消息' }).click()
  await expect(page.getByRole('log', { name: '对话消息' }).getByText(content)).toBeVisible()
  await expect(page.getByRole('log', { name: '对话消息' }).getByText('离线测试回复')).toBeVisible()
  await expect.poll(() => new URL(page.url()).searchParams.get('sid')).not.toBeNull()
  return new URL(page.url()).searchParams.get('sid')!
}

test('owner creates a member; forced password change and member denial use real Cookie/CSRF', async ({ browser, page }) => {
  await login(page, 'owner')
  await page.getByRole('navigation', { name: '主导航' }).getByRole('link', { name: '我的', exact: true }).click()
  await page.getByRole('link', { name: '成员管理' }).click()
  await page.getByRole('button', { name: '创建成员' }).click()
  const name = unique()
  await page.getByLabel('登录名').fill(name)
  await page.getByLabel('显示名称').fill('新成员')
  await page.getByRole('button', { name: '确认创建' }).click()
  const temporary = (await page.locator('.temporary-password strong').textContent())?.trim()
  expect(temporary).toBeTruthy()
  await page.getByRole('button', { name: '关闭', exact: true }).last().click()
  await page.getByRole('button', { name: '退出登录' }).last().click()
  const member = await browser.newContext()
  try {
    const memberPage = await member.newPage()
    await memberPage.goto('/app/')
    await memberPage.getByLabel('用户名').fill(name)
    await memberPage.getByLabel('密码', { exact: true }).fill(temporary!)
    await memberPage.getByRole('button', { name: '登录', exact: true }).click()
    await expect(memberPage.getByRole('heading', { name: '设置新密码' })).toBeVisible()
    await memberPage.getByLabel('当前密码').fill(temporary!)
    await memberPage.getByLabel('新密码', { exact: true }).fill('changed-fixture-password-123')
    await memberPage.getByLabel('确认新密码').fill('changed-fixture-password-123')
    await memberPage.getByRole('button', { name: '保存并重新登录' }).click()
    await expect(memberPage.getByRole('button', { name: '登录', exact: true })).toBeVisible()
    const identity = await login(memberPage, name, 'changed-fixture-password-123')
    await expect(memberPage.getByRole('link', { name: '成员' })).toHaveCount(0)
    await memberPage.goto('/app/members')
    await expect(memberPage.getByText('无权查看成员管理')).toBeVisible()
    const denied = await memberPage.request.post('/api/console/v1/admin/members', {
      headers: { Origin: 'http://127.0.0.1:18081', 'X-CSRF-Token': identity.csrf_token },
      data: { username: unique(), display_name: '无权创建' },
    })
    expect(denied.status()).toBe(403)
  } finally { await member.close() }
})

test('two accounts stay isolated and the same account can resume on another device', async ({ browser }) => {
  const alice = await browser.newContext()
  const bobby = await browser.newContext()
  const aliceOther = await browser.newContext()
  try {
    const alicePage = await alice.newPage()
    const bobbyPage = await bobby.newPage()
    const otherPage = await aliceOther.newPage()
    await login(alicePage, 'alice')
    await login(bobbyPage, 'bobby')
    const secret = `Alice 私人问题 ${Date.now()}`
    const sid = await send(alicePage, secret)
    await bobbyPage.goto(`/app/chat?sid=${encodeURIComponent(sid)}`)
    await expect(bobbyPage.getByText('该对话已不存在。')).toBeVisible()
    await expect(bobbyPage.getByText(secret)).toHaveCount(0)
    await expect.poll(() => new URL(bobbyPage.url()).searchParams.get('sid')).toBeNull()
    const bobbyHistory = await bobbyPage.request.get('/api/console/v1/sessions')
    expect(bobbyHistory.status()).toBe(200)
    const bobbySessions = (await bobbyHistory.json()).sessions as { id: string; preview: string }[]
    expect(bobbySessions.some((session) => session.id === sid || session.preview.includes(secret))).toBe(false)
    await login(otherPage, 'alice')
    await otherPage.goto(`/app/chat?sid=${encodeURIComponent(sid)}`)
    await expect(otherPage.getByRole('log', { name: '对话消息' }).getByText(secret)).toBeVisible()
    await otherPage.reload()
    await expect(otherPage.getByRole('log', { name: '对话消息' }).getByText(secret)).toBeVisible()
    await otherPage.getByRole('button', { name: '退出登录' }).last().click()
    await expect(otherPage.getByRole('button', { name: '登录', exact: true })).toBeVisible()
    await expect(otherPage.getByText(secret)).toHaveCount(0)
  } finally { await alice.close(); await bobby.close(); await aliceOther.close() }
})

for (const width of [390, 1440]) {
  test(`chat fits ${width}px without horizontal overflow`, async ({ browser }) => {
    const context = await browser.newContext({ viewport: { width, height: 900 } })
    try {
      const page = await context.newPage()
      await login(page, 'alice')
      await page.goto('/app/chat')
      await expect(page.getByRole('textbox', { name: '消息内容' })).toBeVisible()
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBeTruthy()
      if (width === 390) {
        const transcript = page.getByRole('log', { name: '对话消息' })
        for (let turn = 1; turn <= 4; turn++) {
          await page.getByRole('textbox', { name: '消息内容' }).fill(`滚动检查 ${turn}`)
          await page.getByRole('button', { name: '发送消息' }).click()
          await expect(transcript.getByText('离线测试回复')).toHaveCount(turn)
          await expect(page.getByRole('textbox', { name: '消息内容' })).toBeEnabled()
        }
        const scroll = await transcript.evaluate((area) => ({ height: area.scrollHeight, top: area.scrollTop, viewport: area.clientHeight }))
        expect(scroll.height - scroll.top - scroll.viewport, JSON.stringify(scroll)).toBeLessThan(3)
      }
    } finally { await context.close() }
  })
}
