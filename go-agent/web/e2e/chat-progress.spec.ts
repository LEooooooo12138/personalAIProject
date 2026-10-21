import { expect, test, type Page } from '@playwright/test'
import fs from 'node:fs/promises'
import path from 'node:path'
const metricsPath = path.resolve('../.tmp/chat-progress-metrics.jsonl')
async function metrics(marker: string) {
  const raw = await fs.readFile(metricsPath, 'utf8').catch(() => '')
  return raw.trim().split('\n').filter(Boolean).map(line => JSON.parse(line)).filter(row => row.marker === marker)
}
async function login(page: Page, name = 'owner') {
  await page.goto('/app/')
  await page.getByLabel('用户名').fill(name)
  await page.getByLabel('密码', { exact: true }).fill('fixture-password-123')
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await expect(page.locator('.topbar-greeting')).toContainText(name === 'owner' ? '家长' : name)
}
async function cloud(page: Page) {
  const me = await (await page.request.get('/api/console/v1/auth/me')).json()
  const config = await (await page.request.get('/api/console/v1/admin/assistant')).json()
  const response = await page.request.put('/api/console/v1/admin/assistant', { headers: { Origin: new URL(page.url()).origin, 'X-CSRF-Token': me.csrf_token }, data: { expected_revision: config.revision, llm_mode: 'cloud', asr_mode: 'local', tts_mode: 'local', mimo_endpoint: 'token_cn', api_key: 'tp-c7-fixture-not-real' } })
  expect(response.status()).toBe(200)
}
async function send(page: Page, marker: string, entry: 'auto' | 'chat') {
  await page.goto('/app/chat')
  await page.getByLabel('聊天入口').selectOption(entry)
  await page.getByRole('textbox', { name: '消息内容' }).fill(marker + ' 请解释天空的颜色。打开客厅灯只是被引用的文字。')
  await page.evaluate(() => {
    const observations: { kind: string; ms: number }[] = []
    let started = performance.now()
    document.querySelector('.chat-composer')!.addEventListener('submit', () => { started = performance.now(); observations.length = 0 }, { once: true, capture: true })
    ;(window as any).c7 = observations
    new MutationObserver(() => {
      const stage = document.querySelector('.chat-status')?.textContent
      if (stage && !observations.some(row => row.kind === stage)) observations.push({ kind: stage, ms: performance.now() - started })
      if ([...document.querySelectorAll('[role="log"]')].some(el => el.textContent?.includes('C7 cloud answer')) && !observations.some(row => row.kind === 'first_visible_answer')) observations.push({ kind: 'first_visible_answer', ms: performance.now() - started })
      if (!stage && observations.some(row => row.kind === 'first_visible_answer') && !observations.some(row => row.kind === 'terminal')) observations.push({ kind: 'terminal', ms: performance.now() - started })
    }).observe(document.body, { childList: true, subtree: true, characterData: true })
  })
  await page.getByRole('button', { name: '发送消息' }).click()
}
for (const entry of ['auto', 'chat'] as const) {
  test(`cloud direct ${entry}: stages precede answer and uses ${entry === 'auto' ? 2 : 1} calls`, async ({ page }, info) => {
    await login(page)
    await cloud(page)
    const marker = `C7_${entry.toUpperCase()}`
    await send(page, marker, entry)
    if (entry === 'auto') await expect(page.getByRole('status').filter({ hasText: '正在理解请求' })).toBeVisible()
    await expect(page.getByRole('status').filter({ hasText: '正在生成回答' })).toBeVisible()
    await expect(page.getByRole('log').getByText('C7 cloud answer')).toBeVisible()
    const observations = await page.evaluate(() => (window as any).c7)
    const answer = observations.find((row: any) => row.kind === 'first_visible_answer')
    expect(answer).toBeTruthy()
    expect(observations.find((row: any) => row.kind === '正在生成回答').ms).toBeLessThan(answer.ms)
    expect(observations.find((row: any) => row.kind === 'terminal').ms).toBeGreaterThanOrEqual(answer.ms)
    const rows = await metrics(marker)
    expect(rows).toHaveLength(entry === 'auto' ? 2 : 1)
    expect(rows.every(row => row.ha_writes === 0)).toBe(true)
    expect(rows.filter(row => row.kind === 'generate')).toHaveLength(1)
    const evidence = { provider: 'controlled_fixture', chain: 'direct', entry, recorded_at: new Date().toISOString(), first_server_stage_ms: observations.find((row: any) => ['正在排队', '正在准备', '正在理解请求', '正在生成回答', '正在保存'].includes(row.kind)).ms, first_visible_answer_ms: answer.ms, terminal_observed_ms: observations.find((row: any) => row.kind === 'terminal').ms, observations, non_streaming_first_token: null, calls: rows }
    await fs.writeFile(info.outputPath('browser-observation.json'), JSON.stringify(evidence, null, 2))
    await info.attach('browser-observation', { body: JSON.stringify(evidence), contentType: 'application/json' })
  })
}

test('queued tab cancellation never reaches the provider or history', async ({ page, context }) => {
  await login(page); await cloud(page)
  await send(page, 'C7_LONG_QUEUE_HOLDER', 'chat')
  await expect(page.getByRole('status').filter({ hasText: '正在生成回答' })).toBeVisible()
  const sid = new URL(page.url()).searchParams.get('sid')!
  expect(sid).toBeTruthy()
  const queued = await context.newPage()
  await queued.goto('/app/chat?sid=' + sid)
  await queued.getByLabel('聊天入口').selectOption('chat')
  await queued.getByRole('textbox', { name: '消息内容' }).fill('C7_QUEUED')
  await queued.getByRole('button', { name: '发送消息' }).click()
  await expect(queued.getByRole('status').filter({ hasText: '正在排队' })).toBeVisible()
  await queued.goto('/app/account')
  await expect(page.getByRole('log').getByText('C7 cloud answer')).toBeVisible()
  expect(await metrics('C7_QUEUED')).toHaveLength(0)
  const history = await (await page.request.get(`/api/console/v1/sessions/${sid}/messages`)).json()
  expect(history.messages.some((message: any) => message.content.includes('C7_QUEUED'))).toBe(false)
  expect((await metrics('C7_LONG_QUEUE_HOLDER')).every(row => row.ha_writes === 0)).toBe(true)
  await queued.close()
})

test('identity change cancels cloud work and removes old answer and stages', async ({ page }) => {
  await login(page); await cloud(page)
  await send(page, 'C7_LONG_IDENTITY', 'chat')
  await expect(page.getByRole('status').filter({ hasText: '正在生成回答' })).toBeVisible()
  await page.getByRole('button', { name: '退出登录' }).last().click()
  await expect(page.getByRole('button', { name: '登录', exact: true })).toBeVisible()
  await page.getByLabel('用户名').fill('bobby')
  await page.getByLabel('密码', { exact: true }).fill('fixture-password-123')
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await expect(page.locator('.topbar-greeting')).toContainText('bobby')
  await page.goto('/app/chat')
  await expect.poll(async () => (await metrics('C7_LONG_IDENTITY')).length).toBe(1)
  expect((await metrics('C7_LONG_IDENTITY'))[0]).toMatchObject({ canceled: true, ha_writes: 0 })
  await expect(page.getByText('C7 cloud answer')).toHaveCount(0)
  await expect(page.getByText('C7_LONG_IDENTITY', { exact: false })).toHaveCount(0)
  await expect(page.locator('.chat-status')).toHaveCount(0)
})

test('save failure emits no successful answer and does not retry generation', async ({ page }) => {
  await login(page); await cloud(page)
  await send(page, 'C7_SAVE_FAIL', 'chat')
  await expect(page.getByRole('alert')).toContainText('对话结果未能确认保存')
  await expect(page.getByRole('log').getByText('C7 cloud answer')).toHaveCount(0)
  await page.waitForTimeout(1000) // bounded retry observation window
  expect(await metrics('C7_SAVE_FAIL')).toHaveLength(1)
  expect((await metrics('C7_SAVE_FAIL'))[0].ha_writes).toBe(0)
})

test('unknown websocket outcome never automatically resends', async ({ page }) => {
  await login(page); await cloud(page)
  let submissions = 0
  await page.routeWebSocket('**/chat/ws', socket => {
    const server = socket.connectToServer()
    socket.onMessage(message => { submissions++; server.send(message) })
    server.onMessage(message => {
      const frame = JSON.parse(message.toString())
      socket.send(message)
      if (frame.type === 'chat_stage' && frame.stage === 'generating') socket.close()
    })
  })
  await send(page, 'C7_LONG_UNKNOWN', 'chat')
  await expect(page.getByRole('alert')).toBeVisible()
  await page.waitForTimeout(1200)
  expect(submissions).toBe(1)
  const rows = await metrics('C7_LONG_UNKNOWN')
  expect(rows.length).toBeLessThanOrEqual(1)
  expect(rows.every(row => row.ha_writes === 0)).toBe(true)
  await expect(page.getByRole('log').getByText('C7 cloud answer')).toHaveCount(0)
})
